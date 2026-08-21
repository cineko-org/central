package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	"github.com/cineko-org/central/internal/support/numeric"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (server *Server) adminLogin(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if server.admin == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_unavailable", "admin login is unavailable", true)
		return
	}
	input := &adminpb.LoginRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	token, principal, err := server.admin.Login(
		request.Context(), server.clientAddress(request), input.GetUserId(), input.GetPassword(),
	)
	if err != nil {
		switch {
		case errors.Is(err, central.ErrRateLimited):
			writer.Header().Set("Retry-After", "600")
			server.writeAPIError(writer, request, http.StatusTooManyRequests, "rate_limited", "try again later", true)
		case errors.Is(err, central.ErrUnauthorized):
			server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication failed", false)
		default:
			server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_login_failed", "login failed", true)
		}
		return
	}
	server.setAdminCookie(writer, request, token, principal.GetExpiresAt().AsTime())
	response := &adminpb.LoginResponse{}
	response.SetPrincipal(principal)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) adminLogout(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if server.admin != nil {
		if cookie, err := request.Cookie(adminSessionCookie); err == nil {
			if err := server.admin.Logout(request.Context(), cookie.Value); err != nil {
				server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_logout_failed", "logout failed", true)
				return
			}
		}
	}
	server.setAdminCookie(writer, request, "", time.Unix(1, 0))
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) adminSession(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedAdmin(writer, request)
	if ok {
		response := &adminpb.GetSessionResponse{}
		response.SetPrincipal(principal)
		server.writeProtoJSON(writer, http.StatusOK, response)
	}
}

func (server *Server) adminStatus(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	ready := server.service.Ready(request.Context()) == nil
	status := &adminpb.Status{}
	status.SetReady(ready)
	if server.reconciler != nil {
		status.SetReconciler(reconcileStatusProto(server.reconciler.Snapshot()))
	}
	response := &adminpb.GetStatusResponse{}
	response.SetStatus(status)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) adminConfigurationView(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	response := &adminpb.GetConfigurationResponse{}
	response.SetConfiguration(server.adminConfiguration)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) currentAdminProbeRelease(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requireClientService(writer, request) {
		return
	}
	release, err := server.clients.CurrentProbeRelease(defaultString(request.URL.Query().Get("channel"), "stable"))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, release)
}

func (server *Server) authenticatedAdmin(
	writer http.ResponseWriter,
	request *http.Request,
) (*adminpb.Principal, bool) {
	if server.admin == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_unavailable", "admin login is unavailable", true)
		return nil, false
	}
	cookie, err := request.Cookie(adminSessionCookie)
	if err != nil {
		server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required", false)
		return nil, false
	}
	principal, err := server.admin.Verify(request.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, central.ErrUnauthorized) {
			server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required", false)
		} else {
			server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_session_failed", "session validation failed", true)
		}
		return nil, false
	}
	return principal, true
}

func (server *Server) setAdminCookie(
	writer http.ResponseWriter,
	request *http.Request,
	value string,
	expiresAt time.Time,
) {
	// #nosec G124 -- Secure is required for TLS and trusted proxy HTTPS; plain HTTP remains usable for local development.
	http.SetCookie(writer, &http.Cookie{
		Name: adminSessionCookie, Value: value, Path: "/", Expires: expiresAt,
		MaxAge: cookieMaxAge(expiresAt), HttpOnly: true, Secure: server.secureRequest(request), SameSite: http.SameSiteStrictMode,
	})
}

func cookieMaxAge(expiresAt time.Time) int {
	if expiresAt.Before(time.Now()) {
		return -1
	}
	return int(time.Until(expiresAt).Seconds())
}

func reconcileStatusProto(value reconcile.Status) *adminpb.ReconcileStatus {
	status := &adminpb.ReconcileStatus{}
	status.SetRunning(value.Running)
	status.SetHealthy(value.Healthy)
	status.SetLeader(value.Leader)
	if !value.LastAttemptAt.IsZero() {
		status.SetLastAttemptAt(timestamppb.New(value.LastAttemptAt))
	}
	if !value.LastSuccessAt.IsZero() {
		status.SetLastSuccessAt(timestamppb.New(value.LastSuccessAt))
	}
	if !value.LastErrorAt.IsZero() {
		status.SetLastErrorAt(timestamppb.New(value.LastErrorAt))
	}
	status.SetLastErrorCode(value.LastErrorCode)
	status.SetLastReport(reconcileReportProto(value.LastReport))
	return status
}

func reconcileReportProto(value reconcile.Report) *adminpb.ReconcileReport {
	report := &adminpb.ReconcileReport{}
	report.SetLeader(value.Leader)
	if !value.StartedAt.IsZero() {
		report.SetStartedAt(timestamppb.New(value.StartedAt))
	}
	if !value.FinishedAt.IsZero() {
		report.SetFinishedAt(timestamppb.New(value.FinishedAt))
	}
	report.SetStaleProbes(numeric.ClampInt32(value.StaleProbes))
	report.SetDeletedProbes(numeric.ClampInt32(value.DeletedProbes))
	report.SetDeletedClientEvents(value.DeletedClientEvents)
	report.SetExpiredLeases(numeric.ClampInt32(value.ExpiredLeases))
	report.SetRequeuedAssignments(numeric.ClampInt32(value.RequeuedAssignments))
	report.SetFailedAssignments(numeric.ClampInt32(value.FailedAssignments))
	report.SetMissedAssignments(numeric.ClampInt32(value.MissedAssignments))
	report.SetAdvancedPolicies(numeric.ClampInt32(value.AdvancedPolicies))
	report.SetCreatedAssignments(numeric.ClampInt32(value.CreatedAssignments))
	report.SetDeferredPolicies(numeric.ClampInt32(value.DeferredPolicies))
	report.SetSuspendedPolicies(numeric.ClampInt32(value.SuspendedPolicies))
	report.SetCatalogRefreshCreated(value.CatalogRefreshCreated)
	report.SetCatalogRefreshWaiting(value.CatalogRefreshWaiting)
	report.SetSeatMapBackfillCreated(value.SeatMapBackfillCreated)
	report.SetSeatMapBackfillWaiting(value.SeatMapBackfillWaiting)
	report.SetOldestDueAgeSeconds(value.OldestDueAgeSeconds)
	return report
}

func (server *Server) secureRequest(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	peer := net.ParseIP(addressHost(request.RemoteAddr))
	return peer != nil && server.trustedProxies.contains(peer) &&
		strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
}

func (server *Server) sameOriginAdminRequest(writer http.ResponseWriter, request *http.Request) bool {
	if strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site") {
		http.Error(writer, "cross-site request rejected", http.StatusForbidden)
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	expectedScheme := "http"
	if server.secureRequest(request) {
		expectedScheme = "https"
	}
	if err != nil || !strings.EqualFold(parsed.Scheme, expectedScheme) ||
		!strings.EqualFold(parsed.Host, request.Host) {
		http.Error(writer, "cross-site request rejected", http.StatusForbidden)
		return false
	}
	return true
}
