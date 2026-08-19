package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/bootstrap"
	contracts "github.com/cineko-org/contracts/v3"
)

type resourceWriteRequest struct {
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data"`
}

func (server *Server) exchangeClientCredential(writer http.ResponseWriter, request *http.Request) {
	serveClientExchange(server, writer, request, server.clients.Exchange)
}

func (server *Server) refreshClientSession(writer http.ResponseWriter, request *http.Request) {
	serveClientExchange(server, writer, request, server.clients.Refresh)
}

func (server *Server) logoutClientSession(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	if err := server.clients.Logout(request.Context(), principal); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) currentRuntimeRelease(writer http.ResponseWriter, request *http.Request) {
	serveCurrentDesktopRelease(server, writer, request, server.clients.CurrentRuntimeReleaseSnapshot)
}

func (server *Server) currentLauncherRelease(writer http.ResponseWriter, request *http.Request) {
	serveCurrentDesktopRelease(server, writer, request, server.clients.CurrentLauncherReleaseSnapshot)
}

func serveCurrentDesktopRelease[Release any](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	current func(context.Context, string, string, string) (Release, int64, error),
) {
	if _, ok := server.authenticatedClient(writer, request); !ok {
		return
	}
	release, generation, err := current(
		request.Context(),
		defaultString(request.URL.Query().Get("channel"), "stable"),
		request.URL.Query().Get("platform"),
		request.URL.Query().Get("arch"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set("X-Cineko-Release-Generation", strconv.FormatInt(generation, 10))
	server.writeJSON(writer, http.StatusOK, release)
}

func (server *Server) issueLaunchTicket(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input central.LaunchTicketRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	if request.Header.Get("Idempotency-Key") != input.Nonce {
		server.writeError(writer, request, fmt.Errorf("%w: Idempotency-Key must equal nonce", central.ErrInvalid))
		return
	}
	response, err := server.clients.IssueLaunchTicket(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, response)
}

func (server *Server) exchangeLaunchTicket(writer http.ResponseWriter, request *http.Request) {
	serveClientExchange(server, writer, request, server.clients.ExchangeLaunchTicket)
}

func serveClientExchange[Request any, Response any](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	exchange func(context.Context, Request) (Response, error),
) {
	if !server.requireClientService(writer, request) || !server.requireProtocol(writer, request) {
		return
	}
	var input Request
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	response, err := exchange(request.Context(), input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, response)
}

func (server *Server) issueProbeBootstrapTicket(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	if server.probeBootstrapSigner == nil {
		server.writeAPIError(
			writer, request, http.StatusServiceUnavailable, "probe_bootstrap_unavailable",
			"Client Probe bootstrap is unavailable", true,
		)
		return
	}
	var input central.ProbeBootstrapTicketRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	device, err := server.clients.AuthorizeProbeBootstrap(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	lifetime := time.Minute
	ticket, err := server.probeBootstrapSigner.Issue(bootstrap.Claims{
		UserID: principal.UserID, TicketID: "ticket_" + newRequestID(),
		InstallationID: input.InstallationID, DeviceID: device.DeviceID, Kind: "client",
		Capabilities: input.Capabilities, MaxConcurrency: input.MaxConcurrency, Runtime: input.Runtime,
	}, lifetime)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, central.ProbeBootstrapTicketResponse{
		Ticket: ticket, ExpiresAt: time.Now().UTC().Add(lifetime),
	})
}

func (server *Server) claimClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input central.ExecutionClaimRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	command, err := server.clients.ClaimExecution(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	if command == nil {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	server.writeJSON(writer, http.StatusOK, command)
}

func (server *Server) heartbeatClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input central.ExecutionHeartbeatRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	response, err := server.clients.HeartbeatExecution(
		request.Context(), principal, request.PathValue("executionId"), input,
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, response)
}

func (server *Server) completeClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input central.ExecutionResultRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	if err := server.clients.CompleteExecution(
		request.Context(), principal, request.PathValue("executionId"), input,
	); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) upsertClientDevice(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input central.ClientDevice
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	input.InstallationID = request.PathValue("installationId")
	device, err := server.clients.UpsertDevice(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, device)
}

func (server *Server) clientBootstrap(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	bootstrap, err := server.clients.Bootstrap(
		request.Context(), principal, strings.TrimSpace(request.URL.Query().Get("installationId")),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, bootstrap)
}

func (server *Server) getClientSettings(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	resource, err := server.clients.GetResource(request.Context(), principal, "settings", "settings")
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, resource)
}

func (server *Server) putClientSettings(writer http.ResponseWriter, request *http.Request) {
	server.writeClientResource(writer, request, "settings", "settings")
}

func (server *Server) listClientResources(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	kind := clientResourceKind(request.URL.Path)
	resources, err := server.clients.ListResources(request.Context(), principal, kind)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"data": resources})
}

func (server *Server) createClientResource(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	expected, valid := expectedRevision(writer, request)
	if !valid || expected != nil {
		if valid {
			writeInvalidRevision(writer, "resource creation requires If-None-Match: *")
		}
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input resourceWriteRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	resource, err := server.clients.PutResource(
		request.Context(), principal, clientResourceKind(request.URL.Path), input.ID, input.Data,
		nil, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, resource)
}

func (server *Server) getClientResource(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	resource, err := server.clients.GetResource(
		request.Context(), principal, clientResourceKind(request.URL.Path), request.PathValue("resourceId"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, resource)
}

func (server *Server) putClientResource(writer http.ResponseWriter, request *http.Request) {
	server.writeClientResource(
		writer, request, clientResourceKind(request.URL.Path), request.PathValue("resourceId"),
	)
}

func (server *Server) writeClientResource(
	writer http.ResponseWriter,
	request *http.Request,
	kind string,
	id string,
) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	var input resourceWriteRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	expected, valid := expectedRevision(writer, request)
	if !valid {
		return
	}
	resource, err := server.clients.PutResource(
		request.Context(), principal, kind, id, input.Data, expected, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, resource)
}

func (server *Server) deleteClientResource(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	expected, valid := expectedRevision(writer, request)
	if !valid {
		return
	}
	resource, err := server.clients.DeleteResource(
		request.Context(), principal, clientResourceKind(request.URL.Path), request.PathValue("resourceId"),
		expected, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, resource)
}

func (server *Server) streamClientEvents(writer http.ResponseWriter, request *http.Request) {
	token := bearerToken(request)
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	cursor, err := clientEventCursor(request)
	if err != nil {
		server.writeError(writer, request, fmt.Errorf("%w: invalid event cursor", central.ErrInvalid))
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		server.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unavailable", "event stream is unavailable", true)
		return
	}
	page, err := server.clients.EventPage(request.Context(), principal, cursor, central.DefaultEventPageSize)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set(contracts.ReleaseGenerationHeader, strconv.FormatInt(page.ReleaseGeneration, 10))
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	sentControl := false
	for {
		if !server.clientEventPrincipalCurrent(request.Context(), token, principal) {
			return
		}
		cursor, ok = reconcileClientEventCursor(writer, flusher, &page, cursor, sentControl)
		if !ok {
			return
		}
		sentControl = true
		cursor, ok = writeClientEventBatch(writer, page.Events, cursor)
		if !ok {
			return
		}
		if len(page.Events) > 0 {
			flusher.Flush()
		} else if !server.waitForClientEvents(writer, flusher, request, token, principal, cursor, page.ReleaseGeneration) {
			return
		}
		page, err = server.clients.EventPage(request.Context(), principal, cursor, central.DefaultEventPageSize)
		if err != nil {
			return
		}
	}
}

func (server *Server) clientEventPrincipalCurrent(
	ctx context.Context,
	token string,
	expected central.ClientPrincipal,
) bool {
	principal, err := server.clients.Authenticate(ctx, token)
	return err == nil && principal == expected
}

func reconcileClientEventCursor(
	writer io.Writer,
	flusher http.Flusher,
	page *central.ClientEventPage,
	cursor int64,
	sentControl bool,
) (int64, bool) {
	if cursor > page.Latest || cursor < page.PrunedThrough {
		reason := contracts.EventStreamResetInvalidCursor
		if cursor < page.PrunedThrough {
			reason = contracts.EventStreamResetRetentionGap
		}
		cursor = page.Latest
		page.Events = nil
		ok := writeEventStreamControl(
			writer, flusher, contracts.EventStreamActionFullResync, reason,
			page.ReleaseGeneration, cursor,
		)
		return cursor, ok
	}
	if sentControl {
		return cursor, true
	}
	ok := writeEventStreamControl(
		writer, flusher, contracts.EventStreamActionReady, "", page.ReleaseGeneration, cursor,
	)
	return cursor, ok
}

func writeClientEventBatch(writer io.Writer, events []central.ClientEvent, cursor int64) (int64, bool) {
	for _, event := range events {
		if event.Sequence <= cursor || strings.TrimSpace(event.Type) == "" {
			return cursor, false
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return cursor, false
		}
		if _, err := fmt.Fprintf(
			writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload,
		); err != nil {
			return cursor, false
		}
		cursor = event.Sequence
	}
	return cursor, true
}

func (server *Server) waitForClientEvents(
	writer io.Writer,
	flusher http.Flusher,
	request *http.Request,
	token string,
	principal central.ClientPrincipal,
	cursor int64,
	releaseGeneration int64,
) bool {
	waitContext, cancel := context.WithTimeout(request.Context(), server.eventHeartbeat)
	waitErr := server.clients.WaitEvents(waitContext, principal, cursor, releaseGeneration)
	cancel()
	if waitErr == nil {
		return true
	}
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		return false
	}
	return server.clientEventPrincipalCurrent(request.Context(), token, principal) &&
		writeEventStreamControl(
			writer, flusher, contracts.EventStreamActionHeartbeat, "", releaseGeneration, cursor,
		)
}

func clientEventCursor(request *http.Request) (int64, error) {
	queryValue := strings.TrimSpace(request.URL.Query().Get("after"))
	headerValue := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if queryValue != "" && headerValue != "" && queryValue != headerValue {
		return 0, errors.New("event cursor sources disagree")
	}
	value := headerValue
	if value == "" {
		value = defaultString(queryValue, "0")
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, errors.New("event cursor must be a non-negative integer")
	}
	return cursor, nil
}

func writeEventStreamControl(
	writer io.Writer,
	flusher http.Flusher,
	action string,
	reason string,
	releaseGeneration int64,
	cursor int64,
) bool {
	if releaseGeneration < 1 || cursor < 0 {
		return false
	}
	payload, err := json.Marshal(contracts.EventStreamControl{
		Protocol: contracts.ProtocolVersion, ReleaseGeneration: releaseGeneration,
		Cursor: cursor, Action: action, Reason: reason,
	})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(writer, "event: cineko.control\ndata: %s\n\n", payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (server *Server) authenticatedClient(
	writer http.ResponseWriter,
	request *http.Request,
) (central.ClientPrincipal, bool) {
	if !server.requireClientService(writer, request) || !server.requireProtocol(writer, request) {
		return central.ClientPrincipal{}, false
	}
	principal, err := server.clients.Authenticate(request.Context(), bearerToken(request))
	if err != nil {
		server.writeError(writer, request, err)
		return central.ClientPrincipal{}, false
	}
	return principal, true
}

func (server *Server) requireClientService(writer http.ResponseWriter, request *http.Request) bool {
	if server.clients != nil {
		return true
	}
	server.writeAPIError(
		writer, request, http.StatusServiceUnavailable, "client_plane_unavailable", "client plane is unavailable", true,
	)
	return false
}

func expectedRevision(writer http.ResponseWriter, request *http.Request) (*int64, bool) {
	ifMatch := strings.TrimSpace(request.Header.Get("If-Match"))
	ifNoneMatch := strings.TrimSpace(request.Header.Get("If-None-Match"))
	if ifMatch != "" && ifNoneMatch != "" {
		writeInvalidRevision(writer, "If-Match and If-None-Match cannot be combined")
		return nil, false
	}
	if ifNoneMatch != "" {
		if ifNoneMatch != "*" {
			writeInvalidRevision(writer, "If-None-Match must be *")
			return nil, false
		}
		return nil, true
	}
	value := ifMatch
	if value == "" {
		writeInvalidRevision(writer, "If-Match or If-None-Match is required")
		return nil, false
	}
	value = strings.Trim(value, `"`)
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 {
		writeInvalidRevision(writer, "If-Match must be a positive revision")
		return nil, false
	}
	return &revision, true
}

func writeInvalidRevision(writer http.ResponseWriter, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"code": "invalid_revision", "message": message, "retryable": false,
		"requestId": writer.Header().Get("X-Request-Id"),
	}})
}

func clientResourceKind(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) >= 2 {
		return segments[1]
	}
	return ""
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
