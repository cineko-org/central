package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cineko-org/central/internal/central"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

func TestPINAuthenticationAndAdminManagementAPI(t *testing.T) {
	repository := &apiRepository{}
	service, err := central.NewService(repository, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	withoutPIN, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := request(t, withoutPIN.Handler(), http.MethodPost, "/v1/auth/pin", map[string]string{
		"pin": "123456",
	}, nil)
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "pin_auth_unavailable")

	user := &clientpb.User{}
	user.SetId("user")
	user.SetDisplayName("User")
	issue := &adminpb.ClientPinIssue{}
	issue.SetUser(user)
	issue.SetPin("123456")
	pins := &apiPINService{issue: issue}
	auth, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "관리자", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	server, err := New(service, WithPINService(pins), WithAdminAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	pins.err = central.ErrRateLimited
	limited := request(t, server.Handler(), http.MethodPost, "/v1/auth/pin", map[string]string{
		"pin": "123456", "installationId": "install_1234567890", "deviceId": "device_123456789012",
	}, nil)
	assertAPIError(t, limited, http.StatusTooManyRequests, "rate_limited")
	if limited.Header().Get("Retry-After") != "600" {
		t.Fatalf("PIN Retry-After = %q", limited.Header().Get("Retry-After"))
	}
	pins.err = nil
	pins.exchange = &clientpb.AuthenticationResponse{}
	pins.exchange.SetUser(user)
	exchanged := request(t, server.Handler(), http.MethodPost, "/v1/auth/pin", map[string]string{
		"pin": "123456", "installationId": "install_1234567890", "deviceId": "device_123456789012",
	}, map[string]string{"X-Forwarded-For": "198.51.100.8"})
	if exchanged.Code != http.StatusOK || pins.source != "192.0.2.1" {
		t.Fatalf("PIN exchange = %d, source=%q, body=%s", exchanged.Code, pins.source, exchanged.Body.String())
	}

	login := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, nil)
	cookie := login.Result().Cookies()[0]
	unauthorizedUsers := request(t, server.Handler(), http.MethodGet, "/v1/admin/users", nil, nil)
	assertAPIError(t, unauthorizedUsers, http.StatusUnauthorized, "unauthorized")
	users := requestWithCookie(t, server, http.MethodGet, "/v1/admin/users", cookie)
	if users.Code != http.StatusOK {
		t.Fatalf("admin users = %d, %s", users.Code, users.Body.String())
	}
	created := requestWithCookieBody(t, server, http.MethodPost, "/v1/admin/users", cookie, map[string]string{
		"displayName": "User",
	})
	if created.Code != http.StatusCreated || pins.displayName != "User" {
		t.Fatalf("create PIN user = %d, %q, %s", created.Code, pins.displayName, created.Body.String())
	}
	rotated := requestWithCookie(t, server, http.MethodPost, "/v1/admin/users/user/pin", cookie)
	if rotated.Code != http.StatusOK || pins.userID != "user" {
		t.Fatalf("rotate PIN = %d, %q", rotated.Code, pins.userID)
	}
	deleted := requestWithCookie(t, server, http.MethodDelete, "/v1/admin/users/user", cookie)
	if deleted.Code != http.StatusNoContent || pins.deletedUserID != "user" {
		t.Fatalf("delete user = %d, %q", deleted.Code, pins.deletedUserID)
	}
}

func TestClientAddressTrustsOnlyPrivateProxyPeers(t *testing.T) {
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, WithTrustedProxyCIDRs("127.0.0.0/8,10.0.0.0/8"))
	if err != nil {
		t.Fatal(err)
	}
	privatePeer := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	privatePeer.RemoteAddr = "127.0.0.1:1234"
	privatePeer.Header.Set("X-Forwarded-For", "192.0.2.77, 203.0.113.9, 10.0.0.1")
	if value := server.clientAddress(privatePeer); value != "203.0.113.9" {
		t.Fatalf("private proxy address = %q", value)
	}
	publicPeer := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	publicPeer.RemoteAddr = "198.51.100.2:1234"
	publicPeer.Header.Set("X-Forwarded-For", "203.0.113.9")
	if value := server.clientAddress(publicPeer); value != "198.51.100.2" {
		t.Fatalf("public peer address = %q", value)
	}
	privatePeer.Header.Set("X-Forwarded-For", "invalid")
	privatePeer.Header.Set("X-Real-IP", "203.0.113.10")
	if value := server.clientAddress(privatePeer); value != "127.0.0.1" {
		t.Fatalf("malformed forwarding chain was trusted = %q", value)
	}
	privatePeer.Header.Del("X-Forwarded-For")
	if value := server.clientAddress(privatePeer); value != "203.0.113.10" {
		t.Fatalf("trusted proxy real IP = %q", value)
	}
	direct, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	if value := direct.clientAddress(privatePeer); value != "127.0.0.1" {
		t.Fatalf("unconfigured proxy accepted forwarding headers: %q", value)
	}
	if _, err := New(service, WithTrustedProxyCIDRs("not-a-cidr")); err == nil {
		t.Fatal("invalid trusted proxy CIDR accepted")
	}
	if value := addressHost("host-without-port"); value != "host-without-port" {
		t.Fatalf("addressHost() = %q", value)
	}
}

func requestWithCookieBody(
	t *testing.T,
	server *Server,
	method string,
	path string,
	cookie *http.Cookie,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(mustJSON(t, body)))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	var builder strings.Builder
	if err := json.NewEncoder(&builder).Encode(value); err != nil {
		t.Fatal(err)
	}
	return builder.String()
}

type apiPINService struct {
	users         []*adminpb.ClientPinUser
	issue         *adminpb.ClientPinIssue
	exchange      *clientpb.AuthenticationResponse
	displayName   string
	userID        string
	deletedUserID string
	source        string
	err           error
}

func (service *apiPINService) ListUsers(context.Context) ([]*adminpb.ClientPinUser, error) {
	return service.users, service.err
}

func (service *apiPINService) CreateUser(_ context.Context, displayName string) (*adminpb.ClientPinIssue, error) {
	service.displayName = displayName
	return service.issue, service.err
}

func (service *apiPINService) Rotate(_ context.Context, userID string) (*adminpb.ClientPinIssue, error) {
	service.userID = userID
	return service.issue, service.err
}

func (service *apiPINService) DeleteUser(_ context.Context, userID string) error {
	service.deletedUserID = userID
	return service.err
}

func (service *apiPINService) Exchange(
	_ context.Context,
	_ *clientpb.PinExchangeRequest,
	source string,
) (*clientpb.AuthenticationResponse, error) {
	service.source = source
	return service.exchange, service.err
}
