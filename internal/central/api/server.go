package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/bootstrap"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/v3/gen/go/cineko/probe"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxRequestBody           = 2 << 20
	assignmentClaimWaitLimit = 5 * time.Second
	releaseGenerationHeader  = "X-Cineko-Release-Generation"
)

type Server struct {
	service              *central.Service
	clients              *central.ClientService
	catalog              *central.CatalogService
	pins                 pinService
	probeBootstrapSigner *bootstrap.Signer
	admin                *AdminAuth
	reconciler           interface {
		Snapshot() *adminpb.ReconcileStatus
	}
	adminConfiguration  *adminpb.Configuration
	adminOperations     adminOperations
	releasePublishHash  [32]byte
	releasePublishReady bool
	trustedProxies      trustedProxySet
	eventHeartbeat      time.Duration
	optionErrors        []error
	handler             http.Handler
}

type pinService interface {
	ListUsers(context.Context) ([]*adminpb.ClientPinUser, error)
	CreateUser(context.Context, string) (*adminpb.ClientPinIssue, error)
	Rotate(context.Context, string) (*adminpb.ClientPinIssue, error)
	DeleteUser(context.Context, string) error
	Exchange(context.Context, *clientpb.PinExchangeRequest, string) (*clientpb.AuthenticationResponse, error)
}

type Option func(*Server)

func WithReconciler(reconcilerStatus interface {
	Snapshot() *adminpb.ReconcileStatus
}) Option {
	return func(server *Server) { server.reconciler = reconcilerStatus }
}

func WithClientService(service *central.ClientService) Option {
	return func(server *Server) { server.clients = service }
}

func WithCatalogService(service *central.CatalogService) Option {
	return func(server *Server) { server.catalog = service }
}

func WithPINService(service pinService) Option {
	return func(server *Server) { server.pins = service }
}

func WithProbeBootstrapSigner(signer *bootstrap.Signer) Option {
	return func(server *Server) { server.probeBootstrapSigner = signer }
}

func WithAdminAuth(auth *AdminAuth) Option {
	return func(server *Server) { server.admin = auth }
}

func WithAdminConfiguration(configuration *adminpb.Configuration) Option {
	return func(server *Server) { server.adminConfiguration = configuration }
}

func WithTrustedProxyCIDRs(cidrs string) Option {
	return func(server *Server) {
		proxies, err := parseTrustedProxyCIDRs(cidrs)
		if err != nil {
			server.optionErrors = append(server.optionErrors, err)
			return
		}
		server.trustedProxies = proxies
	}
}

func WithReleasePublishToken(token string) Option {
	return func(server *Server) {
		if token == "" {
			return
		}
		server.releasePublishHash = sha256.Sum256([]byte(token))
		server.releasePublishReady = true
	}
}

func New(service *central.Service, options ...Option) (*Server, error) {
	if service == nil {
		return nil, errors.New("central service is required")
	}
	server := &Server{
		service: service, eventHeartbeat: 15 * time.Second,
	}
	for _, option := range options {
		option(server)
	}
	if len(server.optionErrors) > 0 {
		return nil, errors.Join(server.optionErrors...)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.readyz)
	mux.HandleFunc("GET /livez", server.livez)
	mux.HandleFunc("GET /readyz", server.readyz)
	mux.HandleFunc("GET /health/reconciler", server.reconcilerHealth)
	mux.HandleFunc("POST /v1/admin/login", server.adminLogin)
	mux.HandleFunc("POST /v1/admin/logout", server.adminLogout)
	mux.HandleFunc("GET /v1/admin/session", server.adminSession)
	mux.HandleFunc("GET /v1/admin/status", server.adminStatus)
	mux.HandleFunc("GET /v1/admin/configuration", server.adminConfigurationView)
	mux.HandleFunc("GET /v1/admin/probes", server.adminProbes)
	mux.HandleFunc("DELETE /v1/admin/probes/{probeId}", server.deleteAdminProbe)
	mux.HandleFunc("GET /v1/admin/data", server.adminData)
	mux.HandleFunc("GET /v1/admin/catalog", server.getAdminCatalog)
	mux.HandleFunc("GET /v1/admin/catalog-refresh", server.getAdminCatalogRefresh)
	mux.HandleFunc("POST /v1/admin/catalog-refresh", server.requestAdminCatalogRefresh)
	mux.HandleFunc("GET /v1/admin/observation-policies", server.listAdminObservationPolicies)
	mux.HandleFunc("POST /v1/admin/observation-policies", server.createAdminObservationPolicy)
	mux.HandleFunc("PUT /v1/admin/observation-policies/{policyId}", server.updateAdminObservationPolicy)
	mux.HandleFunc("DELETE /v1/admin/observation-policies/{policyId}", server.deleteAdminObservationPolicy)
	mux.HandleFunc("GET /v1/admin/observation-intelligence", server.adminObservationIntelligence)
	mux.HandleFunc("GET /v1/admin/releases", server.adminReleases)
	mux.HandleFunc("GET /v1/admin/releases/probe/current", server.currentAdminProbeRelease)
	mux.HandleFunc("GET /v1/admin/users", server.listAdminUsers)
	mux.HandleFunc("POST /v1/admin/users", server.createAdminUser)
	mux.HandleFunc("POST /v1/admin/users/{userId}/pin", server.rotateAdminUserPIN)
	mux.HandleFunc("DELETE /v1/admin/users/{userId}", server.deleteAdminUser)
	mux.Handle("GET /", centralWebHandler())
	mux.HandleFunc("POST /v1/auth/exchange", server.exchangeClientCredential)
	mux.HandleFunc("POST /v1/auth/pin", server.exchangeClientPIN)
	mux.HandleFunc("POST /v1/auth/refresh", server.refreshClientSession)
	mux.HandleFunc("POST /v1/auth/logout", server.logoutClientSession)
	mux.HandleFunc("GET /v1/releases/runtime/current", server.currentRuntimeRelease)
	mux.HandleFunc("GET /v1/releases/launcher/current", server.currentLauncherRelease)
	mux.HandleFunc("POST /v1/release-registry/{component}", server.publishRelease)
	mux.HandleFunc("POST /v1/launch-tickets", server.issueLaunchTicket)
	mux.HandleFunc("POST /v1/client-sessions", server.exchangeLaunchTicket)
	mux.HandleFunc("POST /v1/probe-bootstrap-tickets", server.issueProbeBootstrapTicket)
	mux.HandleFunc("POST /v1/executions:claim", server.claimClientExecution)
	mux.HandleFunc("PUT /v1/executions/{executionId}/heartbeat", server.heartbeatClientExecution)
	mux.HandleFunc("PUT /v1/executions/{executionId}/result", server.completeClientExecution)
	mux.HandleFunc("PUT /v1/devices/{installationId}", server.upsertClientDevice)
	mux.HandleFunc("GET /v1/client/bootstrap", server.clientBootstrap)
	mux.HandleFunc("GET /v1/events/stream", server.streamClientEvents)
	mux.HandleFunc("GET /v1/settings", server.getClientSettings)
	mux.HandleFunc("PUT /v1/settings", server.putClientSettings)
	mux.HandleFunc("GET /v1/catalog", server.getClientCatalog)
	mux.HandleFunc("POST /v1/catalog/snapshots", server.putClientCatalogSnapshot)
	mux.HandleFunc("GET /v1/catalog/auditoriums/{auditoriumId}/seat-map", server.getClientSeatMapVersion)
	mux.HandleFunc("POST /v1/catalog/auditoriums/{auditoriumId}/seat-map:resolve", server.resolveClientSeatMap)
	mux.HandleFunc("GET /v1/catalog/auditoriums/{auditoriumId}/seat-map:watch", server.watchClientSeatMap)
	for _, resource := range []string{
		"presets", "monitors", "reservations", "external-operations", "app-events",
	} {
		mux.HandleFunc("GET /v1/"+resource, server.listClientResources)
		mux.HandleFunc("POST /v1/"+resource, server.createClientResource)
		mux.HandleFunc("GET /v1/"+resource+"/{resourceId}", server.getClientResource)
		mux.HandleFunc("PUT /v1/"+resource+"/{resourceId}", server.putClientResource)
		mux.HandleFunc("DELETE /v1/"+resource+"/{resourceId}", server.deleteClientResource)
	}
	mux.HandleFunc("POST /v1/probes/register", server.registerProbe)
	mux.HandleFunc("PUT /v1/probes/{probeId}/heartbeat", server.heartbeatProbe)
	mux.HandleFunc("POST /v1/probes/{probeId}/disconnect", server.disconnectProbe)
	mux.HandleFunc("POST /v1/probes/{probeId}/assignments:claim", server.claimAssignment)
	mux.HandleFunc("PUT /v1/assignments/{assignmentId}/heartbeat", server.heartbeatAssignment)
	mux.HandleFunc("PUT /v1/assignments/{assignmentId}/result", server.commitResult)
	server.handler = server.releaseGenerationHeader(server.requestContext(mux))
	return server, nil
}

func (server *Server) reconcilerHealth(writer http.ResponseWriter, request *http.Request) {
	if server.reconciler == nil {
		server.writeAPIError(
			writer, request, http.StatusServiceUnavailable, "reconciler_unavailable", "reconciler is unavailable", true,
		)
		return
	}
	status := server.reconciler.Snapshot()
	httpStatus := http.StatusOK
	if !status.GetHealthy() {
		httpStatus = http.StatusServiceUnavailable
	}
	server.writeProtoJSON(writer, httpStatus, status)
}

func (server *Server) Handler() http.Handler { return server.handler }

func serveProto[Request proto.Message, Response proto.Message](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	input Request,
	handle func(context.Context, Request) (Response, error),
) {
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	response, err := handle(request.Context(), input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func writeProtoCall[Response proto.Message](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	load func(context.Context) (Response, error),
) {
	response, err := load(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, status, response)
}

func writeAdminProtoCall[Response proto.Message](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	code string,
	message string,
	load func(context.Context) (Response, error),
) {
	response, err := load(request.Context())
	if err != nil {
		server.writeAPIError(writer, request, http.StatusInternalServerError, code, message, true)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) releaseGenerationHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		generation := int64(0)
		if server.clients != nil {
			current, err := server.clients.CurrentReleaseGeneration(request.Context())
			if err == nil {
				generation = current
			}
		}
		writer.Header().Set(releaseGenerationHeader, strconv.FormatInt(generation, 10))
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) livez(writer http.ResponseWriter, _ *http.Request) {
	status := &commonpb.ServiceHealth{}
	status.SetLive(&commonpb.Live{})
	server.writeProtoJSON(writer, http.StatusOK, status)
}

func (server *Server) readyz(writer http.ResponseWriter, request *http.Request) {
	if err := server.service.Ready(request.Context()); err != nil {
		server.writeError(writer, request, err)
		return
	}
	status := &commonpb.ServiceHealth{}
	status.SetReady(&commonpb.Ready{})
	server.writeProtoJSON(writer, http.StatusOK, status)
}

func (server *Server) registerProbe(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	input := &probepb.RegisterRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	response, err := server.service.RegisterProbe(
		request.Context(), input, request.RemoteAddr, bearerToken(request),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) heartbeatProbe(writer http.ResponseWriter, request *http.Request) {
	probe, ok := server.authenticatedProbe(writer, request)
	if !ok {
		return
	}
	serveProto(server, writer, request, &probepb.HeartbeatRequest{}, func(
		ctx context.Context, input *probepb.HeartbeatRequest,
	) (*probepb.HeartbeatResponse, error) {
		return server.service.HeartbeatProbe(ctx, probe, input)
	})
}

func (server *Server) disconnectProbe(writer http.ResponseWriter, request *http.Request) {
	probe, ok := server.authenticatedProbe(writer, request)
	if !ok {
		return
	}
	if err := server.service.DisconnectProbe(request.Context(), probe); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) claimAssignment(writer http.ResponseWriter, request *http.Request) {
	probe, ok := server.authenticatedProbe(writer, request)
	if !ok {
		return
	}
	response, err := server.service.ClaimAssignment(request.Context(), probe)
	if errors.Is(err, central.ErrNoAssignment) {
		waitContext, cancel := context.WithTimeout(request.Context(), assignmentClaimWaitLimit)
		defer cancel()
		if waitErr := server.service.WaitForAssignment(waitContext, probe); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			if errors.Is(waitErr, context.Canceled) && request.Context().Err() != nil {
				return
			}
			server.writeError(writer, request, waitErr)
			return
		}
		response, err = server.service.ClaimAssignment(request.Context(), probe)
		if errors.Is(err, central.ErrNoAssignment) {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) heartbeatAssignment(writer http.ResponseWriter, request *http.Request) {
	probe, ok := server.authenticatedProbe(writer, request)
	if !ok {
		return
	}
	response, err := server.service.HeartbeatAssignment(
		request.Context(), probe, request.PathValue("assignmentId"), request.Header.Get("X-Cineko-Lease-Token"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) commitResult(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	probe, ok := server.authenticatedProbe(writer, request)
	if !ok {
		return
	}
	input := &observationpb.AssignmentResult{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	if request.Header.Get("Idempotency-Key") != input.GetRunId() {
		server.writeError(writer, request, central.InvalidRequest("Idempotency-Key must equal runId"))
		return
	}
	receipt, err := server.service.CommitResult(
		request.Context(), probe, request.PathValue("assignmentId"),
		request.Header.Get("X-Cineko-Lease-Token"), input,
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, receipt)
}

func (server *Server) authenticatedProbe(
	writer http.ResponseWriter,
	request *http.Request,
) (central.Probe, bool) {
	probe, err := server.service.AuthenticateProbe(
		request.Context(), request.PathValue("probeId"), bearerToken(request),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return central.Probe{}, false
	}
	return probe, true
}

func (server *Server) requireIdempotencyKey(writer http.ResponseWriter, request *http.Request) bool {
	if strings.TrimSpace(request.Header.Get("Idempotency-Key")) != "" {
		return true
	}
	server.writeAPIError(writer, request, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required", false)
	return false
}

func (server *Server) decodeProtoJSON(writer http.ResponseWriter, request *http.Request, output proto.Message) bool {
	payload, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxRequestBody))
	if err != nil || (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, output) != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_json", "request body is invalid", false)
		return false
	}
	if err := protovalidate.Validate(output); err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request body violates the contract", false)
		return false
	}
	return true
}

func (server *Server) writeProtoJSON(writer http.ResponseWriter, status int, value proto.Message) {
	payload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(value)
	if err != nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload) // #nosec G705 -- payload is serialized ProtoJSON, not HTML.
}

func (server *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-Id")
		if strings.TrimSpace(requestID) == "" {
			requestID = newRequestID()
		}
		writer.Header().Set("X-Request-Id", requestID)
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, central.ErrUnauthorized):
		server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication failed", false)
	case errors.Is(err, central.ErrRateLimited):
		writer.Header().Set("Retry-After", "600")
		server.writeAPIError(writer, request, http.StatusTooManyRequests, "rate_limited", "try again in 10 minutes", true)
	case errors.Is(err, central.ErrInvalid):
		message := "request is invalid"
		var public interface{ PublicMessage() string }
		if errors.As(err, &public) {
			message = public.PublicMessage()
		}
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", message, false)
	case errors.Is(err, central.ErrNotFound):
		server.writeAPIError(writer, request, http.StatusNotFound, "not_found", "resource was not found", false)
	case errors.Is(err, central.ErrLeaseExpired):
		server.writeAPIError(writer, request, http.StatusConflict, "lease_expired", "assignment lease expired", false)
	case errors.Is(err, central.ErrIdempotencyConflict):
		server.writeAPIError(writer, request, http.StatusConflict, "idempotency_conflict", "idempotency key was already used for another operation", false)
	case errors.Is(err, central.ErrRevisionConflict):
		server.writeAPIError(writer, request, http.StatusConflict, "revision_conflict", "resource revision does not match", false)
	case errors.Is(err, central.ErrConflict):
		server.writeAPIError(writer, request, http.StatusConflict, "conflict", "resource already exists with different data", false)
	case errors.Is(err, central.ErrStaleRelease):
		server.writeAPIError(writer, request, http.StatusConflict, "stale_release", "runtime release is no longer current", true)
	case errors.Is(err, central.ErrCorruptResource):
		logHTTPFailure(request, writer.Header().Get("X-Request-Id"), err)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "corrupt_resource", "stored resource is invalid", false)
	default:
		logHTTPFailure(request, writer.Header().Get("X-Request-Id"), err)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "internal server error", true)
	}
}

func logHTTPFailure(request *http.Request, requestID string, err error) {
	slog.ErrorContext(
		request.Context(), "HTTP request failed",
		"request_id", requestID,
		"method", request.Method,
		"route", request.Pattern,
		"error", err,
	)
}

func (server *Server) writeAPIError(
	writer http.ResponseWriter,
	_ *http.Request,
	status int,
	code string,
	message string,
	retryable bool,
) {
	detail := &commonpb.APIError{}
	detail.SetCode(code)
	detail.SetMessage(message)
	detail.SetRetryable(retryable)
	detail.SetRequestId(writer.Header().Get("X-Request-Id"))
	response := &commonpb.APIErrorResponse{}
	response.SetError(detail)
	server.writeProtoJSON(writer, status, response)
}

func bearerToken(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "req_unavailable_" + fmt.Sprint(time.Now().UnixNano())
	}
	return "req_" + base64.RawURLEncoding.EncodeToString(buffer)
}
