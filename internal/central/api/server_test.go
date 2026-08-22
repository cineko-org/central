package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestProbeRegistrationAndHeartbeatContract(t *testing.T) {
	repository := &apiRepository{}
	service, err := central.NewService(repository, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}

	livez := request(t, server.Handler(), http.MethodGet, "/livez", nil, nil)
	if livez.Code != http.StatusOK {
		t.Fatalf("livez status = %d", livez.Code)
	}
	health := request(t, server.Handler(), http.MethodGet, "/health", nil, nil)
	if health.Code != http.StatusOK || health.Body.String() != "{\"ready\":{}}" {
		t.Fatalf("health status = %d, body = %s", health.Code, health.Body.String())
	}

	registration := probepb.RegisterRequest_builder{
		InstallationId: stringPointer("install_api"),
		Kind: probepb.ProbeKind_builder{
			Container: probepb.ContainerProbe_builder{}.Build(),
		}.Build(),
		Capabilities: []*observationpb.Capability{
			observationpb.Capability_builder{
				ScheduleCapture: observationpb.ScheduleCapture_builder{}.Build(),
			}.Build(),
		},
		MaxConcurrency: int32Pointer(1),
		Runtime: commonpb.Runtime_builder{
			ComponentVersion: stringPointer("0.1.0"),
			BrowserRevision:  stringPointer("1228"),
			Platform:         stringPointer("darwin"),
			Architecture:     stringPointer("arm64"),
		}.Build(),
	}.Build()

	baseHeaders := map[string]string{
		"Authorization": "Bearer enroll",
	}
	missingKey := request(t, server.Handler(), http.MethodPost, "/v1/probes/register", registration, baseHeaders)
	assertAPIError(t, missingKey, http.StatusBadRequest, "idempotency_key_required")

	wrongEnrollment := cloneHeaders(baseHeaders)
	wrongEnrollment["Authorization"] = "Bearer wrong"
	wrongEnrollment["Idempotency-Key"] = "register_api"
	unauthorized := request(t, server.Handler(), http.MethodPost, "/v1/probes/register", registration, wrongEnrollment)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "unauthorized")

	registerHeaders := cloneHeaders(baseHeaders)
	registerHeaders["Idempotency-Key"] = "register_api"
	registered := request(t, server.Handler(), http.MethodPost, "/v1/probes/register", registration, registerHeaders)
	if registered.Code != http.StatusOK {
		t.Fatalf("register status = %d, body = %s", registered.Code, registered.Body.String())
	}
	registrationResponse := &probepb.RegisterResponse{}
	if err := protojson.Unmarshal(registered.Body.Bytes(), registrationResponse); err != nil {
		t.Fatal(err)
	}
	if registrationResponse.GetProbeId() == "" || registrationResponse.GetAccessToken() == "" {
		t.Fatalf("registration response = %+v", registrationResponse)
	}

	probeHeaders := map[string]string{
		"Authorization": "Bearer " + registrationResponse.GetAccessToken(),
	}
	heartbeat := request(t, server.Handler(), http.MethodPut,
		"/v1/probes/"+registrationResponse.GetProbeId()+"/heartbeat",
		probepb.HeartbeatRequest_builder{
			Draining:       boolPointer(false),
			AvailableSlots: int32Pointer(1),
			Health: probepb.ProbeHealth_builder{
				Healthy: probepb.Healthy_builder{}.Build(),
			}.Build(),
		}.Build(),
		probeHeaders,
	)
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", heartbeat.Code, heartbeat.Body.String())
	}
	claim := request(t, server.Handler(), http.MethodPost,
		"/v1/probes/"+registrationResponse.GetProbeId()+"/assignments:claim", nil, probeHeaders,
	)
	if claim.Code != http.StatusNoContent {
		t.Fatalf("empty claim status = %d, body = %s", claim.Code, claim.Body.String())
	}
}

func TestWriteErrorExposesOnlyExplicitPublicValidationMessages(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	server := &Server{}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/test", nil)

	public := httptest.NewRecorder()
	public.Header().Set("X-Request-Id", "req_public")
	server.writeError(public, request, central.InvalidRequest("theater id is required"))
	if public.Code != http.StatusBadRequest || !strings.Contains(public.Body.String(), "theater id is required") {
		t.Fatalf("public validation response = %d %s", public.Code, public.Body.String())
	}

	wrapped := httptest.NewRecorder()
	wrapped.Header().Set("X-Request-Id", "req_wrapped")
	server.writeError(wrapped, request, fmt.Errorf("%w: internal relation detail", central.ErrInvalid))
	if wrapped.Code != http.StatusBadRequest || strings.Contains(wrapped.Body.String(), "relation") ||
		!strings.Contains(wrapped.Body.String(), "request is invalid") {
		t.Fatalf("wrapped validation response = %d %s", wrapped.Code, wrapped.Body.String())
	}

	internal := httptest.NewRecorder()
	internal.Header().Set("X-Request-Id", "req_internal")
	server.writeError(internal, request, errors.New("secret database detail"))
	if internal.Code != http.StatusInternalServerError || strings.Contains(internal.Body.String(), "database") ||
		!strings.Contains(internal.Body.String(), "req_internal") {
		t.Fatalf("internal response = %d %s", internal.Code, internal.Body.String())
	}
}

func TestRequestBodyIsStrict(t *testing.T) {
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/v1/probes/register", bytes.NewBufferString(`{"unknown":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer enroll")
	request.Header.Set("Idempotency-Key", "strict")
	server.Handler().ServeHTTP(recorder, request)
	assertAPIError(t, recorder, http.StatusBadRequest, "invalid_json")
}

func TestExpectedRevisionPreconditions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		ifMatch     string
		ifNoneMatch string
		want        *int64
		valid       bool
	}{
		{name: "missing precondition"},
		{name: "create only", ifNoneMatch: "*", valid: true},
		{name: "existing revision", ifMatch: `"7"`, want: pointerToInt64(7), valid: true},
		{name: "invalid create tag", ifNoneMatch: `"etag"`},
		{name: "conflicting headers", ifMatch: "7", ifNoneMatch: "*"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/v1/settings", nil)
			request.Header.Set("If-Match", test.ifMatch)
			request.Header.Set("If-None-Match", test.ifNoneMatch)
			got, valid := (&Server{}).expectedRevision(recorder, request)
			if valid != test.valid || !equalOptionalInt64(got, test.want) {
				t.Fatalf("expectedRevision() = %v, %t; want %v, %t", got, valid, test.want, test.valid)
			}
			if !valid && recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", recorder.Code)
			}
		})
	}
}

func pointerToInt64(value int64) *int64 { return &value }

func stringPointer(value string) *string { return &value }

func int32Pointer(value int32) *int32 { return &value }

func boolPointer(value bool) *bool { return &value }

func equalOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func TestReconcilerHealth(t *testing.T) {
	t.Parallel()
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	withoutReconciler, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := request(t, withoutReconciler.Handler(), http.MethodGet, "/health/reconciler", nil, nil)
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "reconciler_unavailable")

	status := &adminpb.ReconcileStatus{}
	status.SetHealthy(true)
	status.SetLeader(true)
	provider := &reconcilerStatus{status: status}
	server, err := New(service, WithReconciler(provider))
	if err != nil {
		t.Fatal(err)
	}
	healthy := request(t, server.Handler(), http.MethodGet, "/health/reconciler", nil, nil)
	if healthy.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body = %s", healthy.Code, healthy.Body.String())
	}
	provider.status.SetHealthy(false)
	unhealthy := request(t, server.Handler(), http.MethodGet, "/health/reconciler", nil, nil)
	if unhealthy.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d, body = %s", unhealthy.Code, unhealthy.Body.String())
	}
}

func TestCentralWebAssets(t *testing.T) {
	t.Parallel()
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	page := request(t, server.Handler(), http.MethodGet, "/", nil, nil)
	assetPath := regexp.MustCompile(`/assets/central-[A-Za-z0-9_-]+\.js`).FindString(page.Body.String())
	if page.Code != http.StatusOK || assetPath == "" {
		t.Fatalf("Central page = %d, %q", page.Code, page.Body.String())
	}
	asset := request(t, server.Handler(), http.MethodGet, assetPath, nil, nil)
	if asset.Code != http.StatusOK || asset.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("Central asset = %d, headers %v", asset.Code, asset.Header())
	}
	missing := request(t, server.Handler(), http.MethodGet, "/not-found", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing Central path = %d", missing.Code)
	}
}

func request(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if message, ok := body.(proto.Message); ok {
			payload, err := protojson.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			encoded.Write(payload)
		} else if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequestWithContext(context.Background(), method, path, &encoded)
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		httpRequest.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, httpRequest)
	return recorder
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, status, recorder.Body.String())
	}
	response := &commonpb.APIErrorResponse{}
	if err := protojson.Unmarshal(recorder.Body.Bytes(), response); err != nil {
		t.Fatal(err)
	}
	if response.GetError().GetCode() != code {
		t.Fatalf("error code = %q, want %q", response.GetError().GetCode(), code)
	}
}

func cloneHeaders(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

type apiRepository struct {
	probe central.Probe
}

type reconcilerStatus struct {
	status *adminpb.ReconcileStatus
}

func (provider *reconcilerStatus) Snapshot() *adminpb.ReconcileStatus {
	return proto.CloneOf(provider.status)
}

func (*apiRepository) Ready(context.Context) error { return nil }

func (*apiRepository) ConsumeProbeBootstrap(context.Context, string, time.Time, time.Time) error {
	return nil
}

func (repository *apiRepository) RegisterProbe(_ context.Context, probe central.Probe) (central.Probe, error) {
	repository.probe = probe
	return probe, nil
}

func (repository *apiRepository) AuthenticateProbe(
	_ context.Context,
	probeID string,
	tokenHash [32]byte,
	now time.Time,
) (central.Probe, error) {
	if (probeID != "" && probeID != repository.probe.ID) || repository.probe.TokenExpiresAt.Before(now) ||
		subtle.ConstantTimeCompare(repository.probe.TokenHash[:], tokenHash[:]) != 1 {
		return central.Probe{}, central.ErrUnauthorized
	}
	return repository.probe, nil
}

func (repository *apiRepository) HeartbeatProbe(
	_ context.Context,
	_ string,
	heartbeat *probepb.HeartbeatRequest,
	_ time.Time,
) (central.Probe, error) {
	repository.probe.Draining = heartbeat.GetDraining()
	return repository.probe, nil
}

func (*apiRepository) DisconnectProbe(context.Context, string, time.Time) error { return nil }

func (*apiRepository) ClaimAssignment(
	context.Context,
	string,
	[32]byte,
	time.Time,
	time.Time,
	time.Time,
) (central.Assignment, error) {
	return central.Assignment{}, central.ErrNoAssignment
}

func (*apiRepository) HeartbeatAssignment(
	context.Context,
	string,
	string,
	[32]byte,
	time.Time,
	time.Time,
) error {
	return errors.New("not implemented")
}

func (*apiRepository) CommitResult(context.Context, central.ResultCommit) (*observationpb.ResultReceipt, error) {
	return nil, errors.New("not implemented")
}
