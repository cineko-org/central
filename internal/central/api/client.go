package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/bootstrap"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/execution"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (server *Server) exchangeClientCredential(writer http.ResponseWriter, request *http.Request) {
	if !server.requireClientService(writer, request) {
		return
	}
	serveProto(server, writer, request, &clientpb.TokenExchangeRequest{}, server.clients.Exchange)
}

func (server *Server) refreshClientSession(writer http.ResponseWriter, request *http.Request) {
	if !server.requireClientService(writer, request) {
		return
	}
	serveProto(server, writer, request, &clientpb.TokenRefreshRequest{}, server.clients.Refresh)
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

func serveCurrentDesktopRelease[Release proto.Message](
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
	server.writeProtoJSON(writer, http.StatusOK, release)
}

func (server *Server) issueLaunchTicket(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	input := &clientpb.LaunchTicketRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	if request.Header.Get("Idempotency-Key") != input.GetNonce() {
		server.writeError(writer, request, central.InvalidRequest("Idempotency-Key must equal nonce"))
		return
	}
	response, err := server.clients.IssueLaunchTicket(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusCreated, response)
}

func (server *Server) exchangeLaunchTicket(writer http.ResponseWriter, request *http.Request) {
	if !server.requireClientService(writer, request) {
		return
	}
	serveProto(server, writer, request, &clientpb.SessionExchangeRequest{}, server.clients.ExchangeLaunchTicket)
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
	input := &clientpb.ProbeBootstrapTicketRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
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
		InstallationID: input.GetInstallationId(), DeviceID: device.GetDeviceId(), Kind: "client",
		Capabilities: capabilityNames(input.GetCapabilities()), MaxConcurrency: int(input.GetMaxConcurrency()),
		RuntimeVersion:  input.GetRuntime().GetComponentVersion(),
		BrowserRevision: input.GetRuntime().GetBrowserRevision(), Platform: input.GetRuntime().GetPlatform(),
		Architecture: input.GetRuntime().GetArchitecture(),
	}, lifetime)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	response := &clientpb.ProbeBootstrapTicketResponse{}
	response.SetTicket(ticket)
	response.SetExpiresAt(timestamppb.New(time.Now().UTC().Add(lifetime)))
	server.writeProtoJSON(writer, http.StatusCreated, response)
}

func capabilityNames(values []*observationpb.Capability) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch {
		case value.GetScheduleCapture() != nil:
			result = append(result, "cgv.schedule.capture")
		case value.GetCatalogCapture() != nil:
			result = append(result, "cgv.catalog.capture")
		case value.GetSeatMapCapture() != nil:
			result = append(result, "cgv.seat-map.capture")
		}
	}
	return result
}

func (server *Server) claimClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	serveProto(server, writer, request, &executionpb.ClaimRequest{}, func(
		ctx context.Context, input *executionpb.ClaimRequest,
	) (*executionpb.ClaimResponse, error) {
		return server.clients.ClaimExecution(ctx, principal, input)
	})
}

func (server *Server) heartbeatClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	input := &executionpb.HeartbeatRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	if !bindExecutionID(input.GetCommandId(), request.PathValue("executionId"), input.SetCommandId) {
		server.writeError(writer, request, central.InvalidRequest("commandId must match the request path"))
		return
	}
	response, err := server.clients.HeartbeatExecution(
		request.Context(), principal, input,
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) completeClientExecution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	input := &executionpb.ResultRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	if !bindExecutionID(input.GetCommandId(), request.PathValue("executionId"), input.SetCommandId) {
		server.writeError(writer, request, central.InvalidRequest("commandId must match the request path"))
		return
	}
	if err := server.clients.CompleteExecution(request.Context(), principal, input); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func bindExecutionID(messageID, pathID string, set func(string)) bool {
	pathID = strings.TrimSpace(pathID)
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		set(pathID)
		return pathID != ""
	}
	return messageID == pathID
}

func (server *Server) upsertClientDevice(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	input := &clientpb.Device{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	input.SetInstallationId(request.PathValue("installationId"))
	device, err := server.clients.UpsertDevice(request.Context(), principal, input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, device)
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
	server.writeProtoJSON(writer, http.StatusOK, bootstrap)
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
	response := &servicepb.GetResourceResponse{}
	response.SetResource(resource)
	server.writeProtoJSON(writer, http.StatusOK, response)
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
	response := &servicepb.ListResourcesResponse{}
	response.SetResources(resources)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) createClientResource(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	expected, valid := server.expectedRevision(writer, request)
	if !valid || expected != nil {
		if valid {
			server.writeInvalidRevision(writer, request, "resource creation requires If-None-Match: *")
		}
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	input := &clientpb.Resource{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	resource, err := server.clients.PutResource(
		request.Context(), principal, clientResourceKind(request.URL.Path), input.GetIdentity().GetId(), input,
		nil, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	response := &servicepb.PutResourceResponse{}
	response.SetResource(resource)
	server.writeProtoJSON(writer, http.StatusCreated, response)
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
	response := &servicepb.GetResourceResponse{}
	response.SetResource(resource)
	server.writeProtoJSON(writer, http.StatusOK, response)
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
	input := &clientpb.Resource{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	expected, valid := server.expectedRevision(writer, request)
	if !valid {
		return
	}
	resource, err := server.clients.PutResource(
		request.Context(), principal, kind, id, input, expected, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	response := &servicepb.PutResourceResponse{}
	response.SetResource(resource)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) deleteClientResource(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	expected, valid := server.expectedRevision(writer, request)
	if !valid {
		return
	}
	_, err := server.clients.DeleteResource(
		request.Context(), principal, clientResourceKind(request.URL.Path), request.PathValue("resourceId"),
		expected, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, &servicepb.DeleteResourceResponse{})
}

func (server *Server) streamClientEvents(writer http.ResponseWriter, request *http.Request) {
	token := bearerToken(request)
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	cursor, err := clientEventCursor(request)
	if err != nil {
		server.writeError(writer, request, central.InvalidRequest("event cursor is invalid"))
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
	writer.Header().Set(releaseGenerationHeader, strconv.FormatInt(page.ReleaseGeneration, 10))
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
	page *central.ClientEventBatch,
	cursor int64,
	sentControl bool,
) (int64, bool) {
	if cursor > page.Latest || cursor < page.PrunedThrough {
		reason := eventStreamResetInvalidCursor
		if cursor < page.PrunedThrough {
			reason = eventStreamResetRetentionGap
		}
		cursor = page.Latest
		page.Events = nil
		ok := writeEventStreamControl(
			writer, flusher, eventStreamActionFullResync, reason,
			page.ReleaseGeneration, cursor,
		)
		return cursor, ok
	}
	if sentControl {
		return cursor, true
	}
	ok := writeEventStreamControl(
		writer, flusher, eventStreamActionReady, "", page.ReleaseGeneration, cursor,
	)
	return cursor, ok
}

func writeClientEventBatch(writer io.Writer, events []*clientpb.ClientEvent, cursor int64) (int64, bool) {
	for _, event := range events {
		if event.GetSequence() <= cursor || !event.HasEvent() {
			return cursor, false
		}
		payload, err := protojson.Marshal(event)
		if err != nil {
			return cursor, false
		}
		if _, err := fmt.Fprintf(
			writer, "id: %d\nevent: cineko.resource\ndata: %s\n\n", event.GetSequence(), payload,
		); err != nil {
			return cursor, false
		}
		cursor = event.GetSequence()
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
			writer, flusher, eventStreamActionHeartbeat, "", releaseGeneration, cursor,
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
	control := &clientpb.StreamControl{}
	control.SetReleaseGeneration(releaseGeneration)
	switch action {
	case eventStreamActionReady:
		ready := &clientpb.StreamReady{}
		ready.SetCursor(cursor)
		control.SetReady(ready)
	case eventStreamActionHeartbeat:
		heartbeat := &clientpb.StreamHeartbeat{}
		heartbeat.SetCursor(cursor)
		control.SetHeartbeat(heartbeat)
	case eventStreamActionFullResync:
		if reason == eventStreamResetRetentionGap {
			gap := &clientpb.RetentionGap{}
			gap.SetCursor(cursor)
			control.SetRetentionGap(gap)
		} else {
			invalid := &clientpb.InvalidCursor{}
			invalid.SetCursor(cursor)
			control.SetInvalidCursor(invalid)
		}
	default:
		return false
	}
	payload, err := protojson.Marshal(control)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(writer, "event: cineko.control\ndata: %s\n\n", payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

const (
	eventStreamActionReady        = "ready"
	eventStreamActionHeartbeat    = "heartbeat"
	eventStreamActionFullResync   = "full_resync"
	eventStreamResetInvalidCursor = "invalid_cursor"
	eventStreamResetRetentionGap  = "retention_gap"
)

func (server *Server) authenticatedClient(
	writer http.ResponseWriter,
	request *http.Request,
) (central.ClientPrincipal, bool) {
	if !server.requireClientService(writer, request) {
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

func (server *Server) expectedRevision(writer http.ResponseWriter, request *http.Request) (*int64, bool) {
	ifMatch := strings.TrimSpace(request.Header.Get("If-Match"))
	ifNoneMatch := strings.TrimSpace(request.Header.Get("If-None-Match"))
	if ifMatch != "" && ifNoneMatch != "" {
		server.writeInvalidRevision(writer, request, "If-Match and If-None-Match cannot be combined")
		return nil, false
	}
	if ifNoneMatch != "" {
		if ifNoneMatch != "*" {
			server.writeInvalidRevision(writer, request, "If-None-Match must be *")
			return nil, false
		}
		return nil, true
	}
	value := ifMatch
	if value == "" {
		server.writeInvalidRevision(writer, request, "If-Match or If-None-Match is required")
		return nil, false
	}
	value = strings.Trim(value, `"`)
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 {
		server.writeInvalidRevision(writer, request, "If-Match must be a positive revision")
		return nil, false
	}
	return &revision, true
}

func (server *Server) writeInvalidRevision(writer http.ResponseWriter, request *http.Request, message string) {
	server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_revision", message, false)
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
