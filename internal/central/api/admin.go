package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
)

type AdminConfiguration struct {
	ListenAddress             string `json:"listenAddress"`
	MinimumRuntimeVersion     string `json:"minimumRuntimeVersion,omitempty"`
	MinimumBrowserRevision    string `json:"minimumBrowserRevision,omitempty"`
	ClientSessionSeconds      int64  `json:"clientSessionSeconds"`
	ClientRefreshSeconds      int64  `json:"clientRefreshSeconds"`
	AdminSessionSeconds       int64  `json:"adminSessionSeconds"`
	ReconcileIntervalSeconds  int64  `json:"reconcileIntervalSeconds"`
	ProbeHeartbeatTTLSeconds  int64  `json:"probeHeartbeatTtlSeconds"`
	ProbeOfflineRetentionDays int64  `json:"probeOfflineRetentionDays"`
	AssignmentRetryMinSeconds int64  `json:"assignmentRetryMinSeconds"`
	AssignmentRetryMaxSeconds int64  `json:"assignmentRetryMaxSeconds"`
	ReconcileBatchSize        int    `json:"reconcileBatchSize"`
}

type adminLoginRequest struct {
	UserID   string `json:"userId"`
	Password string `json:"password"`
}

func (server *Server) adminLogin(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if server.admin == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_unavailable", "admin login is unavailable", true)
		return
	}
	var input adminLoginRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	token, principal, err := server.admin.Login(
		request.Context(), server.clientAddress(request), input.UserID, input.Password,
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
	server.setAdminCookie(writer, request, token, time.Unix(principal.ExpiresAt, 0))
	server.writeJSON(writer, http.StatusOK, principal)
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
		server.writeJSON(writer, http.StatusOK, principal)
	}
}

func (server *Server) adminStatus(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	ready := server.service.Ready(request.Context()) == nil
	response := map[string]any{"ready": ready}
	if server.reconciler != nil {
		response["reconciler"] = server.reconciler.Snapshot()
	}
	server.writeJSON(writer, http.StatusOK, response)
}

func (server *Server) adminConfigurationView(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	server.writeJSON(writer, http.StatusOK, server.adminConfiguration)
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
	server.writeJSON(writer, http.StatusOK, release)
}

func (server *Server) authenticatedAdmin(
	writer http.ResponseWriter,
	request *http.Request,
) (adminPrincipal, bool) {
	if server.admin == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_unavailable", "admin login is unavailable", true)
		return adminPrincipal{}, false
	}
	cookie, err := request.Cookie(adminSessionCookie)
	if err != nil {
		server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required", false)
		return adminPrincipal{}, false
	}
	principal, err := server.admin.Verify(request.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, central.ErrUnauthorized) {
			server.writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required", false)
		} else {
			server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_session_failed", "session validation failed", true)
		}
		return adminPrincipal{}, false
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
