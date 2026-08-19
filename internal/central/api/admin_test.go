package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

const (
	adminTestPassword = "admin-password"
	adminTestPepper   = "0123456789abcdef0123456789abcdef"
)

type adminSessionRepository struct {
	credentials map[string]central.AdminCredential
	sessions    map[[32]byte]central.AdminSession
}

type adminOperationsFake struct {
	probes         []AdminProbe
	deletedProbes  []string
	deleteProbeErr error
	summary        AdminDataSummary
	policies       []AdminObservationPolicy
	intelligence   domain.ScheduleIntelligence
	err            error
}

func (operations *adminOperationsFake) ListAdminProbes(context.Context) ([]AdminProbe, error) {
	return operations.probes, operations.err
}

func (operations *adminOperationsFake) DeleteAdminProbe(_ context.Context, probeID string) error {
	if operations.deleteProbeErr != nil {
		return operations.deleteProbeErr
	}
	operations.deletedProbes = append(operations.deletedProbes, probeID)
	return nil
}

func (operations *adminOperationsFake) AdminDataSummary(context.Context) (AdminDataSummary, error) {
	return operations.summary, operations.err
}

func (operations *adminOperationsFake) ListAdminObservationPolicies(context.Context) ([]AdminObservationPolicy, error) {
	return operations.policies, operations.err
}

func (operations *adminOperationsFake) CreateAdminObservationPolicy(
	_ context.Context,
	input AdminObservationPolicyInput,
) (AdminObservationPolicy, error) {
	if operations.err != nil {
		return AdminObservationPolicy{}, operations.err
	}
	policy := AdminObservationPolicy{
		ID: "policy", Revision: 1, AdminObservationPolicyInput: input,
		Theater: central.Theater{
			ID: input.TheaterID, ProviderID: contracts.ProviderCGV,
			SourceKey: "서울/용산아이파크몰", Region: "서울", Name: "용산아이파크몰",
		},
	}
	operations.policies = append(operations.policies, policy)
	return policy, nil
}

func (operations *adminOperationsFake) UpdateAdminObservationPolicy(
	_ context.Context,
	id string,
	revision int64,
	input AdminObservationPolicyInput,
) (AdminObservationPolicy, error) {
	if operations.err != nil {
		return AdminObservationPolicy{}, operations.err
	}
	return AdminObservationPolicy{
		ID: id, Revision: revision + 1, AdminObservationPolicyInput: input,
		Theater: central.Theater{
			ID: input.TheaterID, ProviderID: contracts.ProviderCGV,
			SourceKey: "서울/용산아이파크몰", Region: "서울", Name: "용산아이파크몰",
		},
	}, nil
}

func (operations *adminOperationsFake) DeleteAdminObservationPolicy(context.Context, string, int64) error {
	return operations.err
}

func (operations *adminOperationsFake) AdminObservationIntelligence(
	context.Context,
	*time.Location,
) (domain.ScheduleIntelligence, error) {
	return operations.intelligence, operations.err
}

func newAdminSessionRepository() *adminSessionRepository {
	return &adminSessionRepository{
		credentials: make(map[string]central.AdminCredential),
		sessions:    make(map[[32]byte]central.AdminSession),
	}
}

func (repository *adminSessionRepository) BootstrapAdminCredentials(
	_ context.Context,
	credentials []central.AdminCredential,
) error {
	if len(repository.credentials) > 0 {
		return nil
	}
	if len(credentials) == 0 {
		return context.Canceled
	}
	for _, credential := range credentials {
		repository.credentials[credential.UserID] = credential
	}
	return nil
}

func (repository *adminSessionRepository) FindAdminCredential(
	_ context.Context,
	userID string,
) (central.AdminCredential, error) {
	credential, found := repository.credentials[userID]
	if !found {
		return central.AdminCredential{}, central.ErrUnauthorized
	}
	return credential, nil
}

func (repository *adminSessionRepository) CreateAdminSession(
	_ context.Context,
	session central.AdminSession,
) error {
	repository.sessions[session.TokenHash] = session
	return nil
}

func (repository *adminSessionRepository) AuthenticateAdminSession(
	_ context.Context,
	tokenHash [32]byte,
	now time.Time,
) (central.AdminSession, error) {
	session, found := repository.sessions[tokenHash]
	if !found || !session.ExpiresAt.After(now) {
		return central.AdminSession{}, central.ErrUnauthorized
	}
	return session, nil
}

func (repository *adminSessionRepository) RevokeAdminSession(
	_ context.Context,
	tokenHash [32]byte,
	_ time.Time,
) error {
	delete(repository.sessions, tokenHash)
	return nil
}

func TestAdminAuthValidatesAndPersistsSessions(t *testing.T) {
	credential := AdminCredential{UserID: "admin", DisplayName: "관리자", Password: adminTestPassword}
	repository := newAdminSessionRepository()
	for _, input := range []struct {
		credentials []AdminCredential
		repository  central.AdminSessionRepository
		ttl         time.Duration
	}{
		{[]AdminCredential{credential}, nil, time.Hour},
		{[]AdminCredential{credential}, repository, -time.Second},
		{[]AdminCredential{{}}, repository, time.Hour},
		{[]AdminCredential{credential, credential}, repository, time.Hour},
	} {
		if _, err := NewAdminAuth(input.credentials, adminTestPepper, input.repository, input.ttl); err == nil {
			t.Fatalf("NewAdminAuth(%+v) succeeded", input)
		}
	}
	if _, err := NewAdminAuth([]AdminCredential{credential}, "short", repository, 0); err == nil {
		t.Fatal("short admin password pepper accepted")
	}
	auth, err := NewAdminAuth([]AdminCredential{credential}, adminTestPepper, repository, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	emptyRepository := newAdminSessionRepository()
	emptyAuth, err := NewAdminAuth(nil, adminTestPepper, emptyRepository, time.Hour)
	if err != nil || emptyAuth.Bootstrap(t.Context()) == nil {
		t.Fatal("empty admin bootstrap succeeded")
	}
	now := time.Now().UTC()
	auth.clock = func() time.Time { return now }
	if _, _, err := auth.Login(context.Background(), "198.51.100.1", "missing", adminTestPassword); err == nil {
		t.Fatal("missing admin authenticated")
	}
	if _, _, err := auth.Login(context.Background(), "198.51.100.1", "admin", "wrong"); err == nil {
		t.Fatal("wrong admin token authenticated")
	}
	token, principal, err := auth.Login(context.Background(), "198.51.100.1", " admin ", adminTestPassword)
	if err != nil || principal.DisplayName != "관리자" {
		t.Fatalf("Login() = %q, %+v, %v", token, principal, err)
	}
	verified, err := auth.Verify(context.Background(), token)
	if err != nil || verified.UserID != "admin" {
		t.Fatalf("Verify() = %+v, %v", verified, err)
	}
	for _, invalid := range []string{"", "bad.token", token + "x"} {
		if _, err := auth.Verify(context.Background(), invalid); err == nil {
			t.Fatalf("Verify(%q) succeeded", invalid)
		}
	}
	auth.clock = func() time.Time { return now.Add(defaultAdminTTL + time.Second) }
	if _, err := auth.Verify(context.Background(), token); err == nil {
		t.Fatal("expired admin session accepted")
	}
}

func TestAdminAPIRequiresAdminSession(t *testing.T) {
	repository := &apiRepository{}
	service, err := central.NewService(repository, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	withoutAdmin, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := request(t, withoutAdmin.Handler(), http.MethodGet, "/v1/admin/session", nil, nil)
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "admin_unavailable")

	auth, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "관리자", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	configuration := AdminConfiguration{ListenAddress: ":8080", ReconcileBatchSize: 100}
	operations := &adminOperationsFake{
		probes:  []AdminProbe{{ID: "probe", Kind: "client", Status: "online"}},
		summary: AdminDataSummary{ScheduleCaptures: 42, ShowtimeObservations: 200},
	}
	server, err := New(
		service, WithAdminAuth(auth), WithTrustedProxyCIDRs("192.0.2.0/24"),
		WithAdminConfiguration(configuration), WithAdminOperations(operations),
	)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := request(t, server.Handler(), http.MethodGet, "/v1/admin/status", nil, nil)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "unauthorized")
	badLogin := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": "wrong",
	}, nil)
	assertAPIError(t, badLogin, http.StatusUnauthorized, "unauthorized")
	login := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, map[string]string{"X-Forwarded-Proto": "https"})
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 || !login.Result().Cookies()[0].Secure {
		t.Fatalf("admin login = %d, cookies=%v", login.Code, login.Result().Cookies())
	}
	directServer, err := New(service, WithAdminAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	spoofedHTTPS := request(t, directServer.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, map[string]string{"X-Forwarded-Proto": "https"})
	if spoofedHTTPS.Code != http.StatusOK || spoofedHTTPS.Result().Cookies()[0].Secure {
		t.Fatalf("untrusted forwarded HTTPS changed cookie security = %v", spoofedHTTPS.Result().Cookies())
	}
	crossSite := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, map[string]string{"Origin": "https://evil.example"})
	if crossSite.Code != http.StatusForbidden {
		t.Fatalf("cross-site admin login = %d", crossSite.Code)
	}
	cookie := login.Result().Cookies()[0]
	statusRequest := requestWithCookie(t, server, http.MethodGet, "/v1/admin/status", cookie)
	if statusRequest.Code != http.StatusOK || !strings.Contains(statusRequest.Body.String(), `"ready":true`) {
		t.Fatalf("admin status = %d, %s", statusRequest.Code, statusRequest.Body.String())
	}
	configurationRequest := requestWithCookie(t, server, http.MethodGet, "/v1/admin/configuration", cookie)
	if configurationRequest.Code != http.StatusOK ||
		!strings.Contains(configurationRequest.Body.String(), `"listenAddress":":8080"`) ||
		!strings.Contains(configurationRequest.Body.String(), `"reconcileBatchSize":100`) {
		t.Fatalf("admin configuration = %d, %s", configurationRequest.Code, configurationRequest.Body.String())
	}
	probesRequest := requestWithCookie(t, server, http.MethodGet, "/v1/admin/probes", cookie)
	if probesRequest.Code != http.StatusOK || !strings.Contains(probesRequest.Body.String(), `"id":"probe"`) {
		t.Fatalf("admin probes = %d, %s", probesRequest.Code, probesRequest.Body.String())
	}
	deleteProbe := requestWithCookie(t, server, http.MethodDelete, "/v1/admin/probes/probe%20%2F%20offline", cookie)
	if deleteProbe.Code != http.StatusNoContent || len(operations.deletedProbes) != 1 || operations.deletedProbes[0] != "probe / offline" {
		t.Fatalf("delete admin probe = %d, deleted=%v, body=%s", deleteProbe.Code, operations.deletedProbes, deleteProbe.Body.String())
	}
	operations.deleteProbeErr = central.ErrConflict
	assertAPIError(t, requestWithCookie(t, server, http.MethodDelete, "/v1/admin/probes/probe", cookie), http.StatusConflict, "conflict")
	operations.deleteProbeErr = nil
	dataRequest := requestWithCookie(t, server, http.MethodGet, "/v1/admin/data", cookie)
	if dataRequest.Code != http.StatusOK || !strings.Contains(dataRequest.Body.String(), `"scheduleCaptures":42`) {
		t.Fatalf("admin data = %d, %s", dataRequest.Code, dataRequest.Body.String())
	}
	theaterID := contracts.CatalogID(contracts.ProviderCGV, "theater", "서울/용산아이파크몰")
	validPolicy := AdminObservationPolicyInput{
		Enabled: true, TheaterID: " " + theaterID + " ",
		HorizonDays: 14, Priority: 50, BaselineMinSeconds: 900, BaselineMaxSeconds: 1800,
		DemandMinSeconds: 120, DemandMaxSeconds: 300, BurstMinSeconds: 30,
		BurstMaxSeconds: 90, BurstDurationSeconds: 3600,
	}
	createPolicy := requestWithCookieAndHeaders(
		t, server, http.MethodPost, "/v1/admin/observation-policies", validPolicy, cookie,
		map[string]string{"If-None-Match": "*"},
	)
	if createPolicy.Code != http.StatusCreated ||
		!strings.Contains(createPolicy.Body.String(), `"name":"용산아이파크몰"`) ||
		!strings.Contains(createPolicy.Body.String(), `"theaterId":"`+theaterID+`"`) {
		t.Fatalf("create policy = %d, %s", createPolicy.Code, createPolicy.Body.String())
	}
	listPolicies := requestWithCookie(t, server, http.MethodGet, "/v1/admin/observation-policies", cookie)
	if listPolicies.Code != http.StatusOK || !strings.Contains(listPolicies.Body.String(), `"id":"policy"`) {
		t.Fatalf("list policies = %d, %s", listPolicies.Code, listPolicies.Body.String())
	}
	updatePolicy := requestWithCookieAndHeaders(
		t, server, http.MethodPut, "/v1/admin/observation-policies/policy", validPolicy, cookie,
		map[string]string{"If-Match": `"1"`},
	)
	if updatePolicy.Code != http.StatusOK || !strings.Contains(updatePolicy.Body.String(), `"revision":2`) {
		t.Fatalf("update policy = %d, %s", updatePolicy.Code, updatePolicy.Body.String())
	}
	intelligence := requestWithCookie(t, server, http.MethodGet, "/v1/admin/observation-intelligence", cookie)
	if intelligence.Code != http.StatusOK || !strings.Contains(intelligence.Body.String(), `"snapshotCount":0`) {
		t.Fatalf("observation intelligence = %d, %s", intelligence.Code, intelligence.Body.String())
	}
	deletePolicy := requestWithCookieAndHeaders(
		t, server, http.MethodDelete, "/v1/admin/observation-policies/policy", nil, cookie,
		map[string]string{"If-Match": `"2"`},
	)
	if deletePolicy.Code != http.StatusNoContent {
		t.Fatalf("delete policy = %d, %s", deletePolicy.Code, deletePolicy.Body.String())
	}
	operations.err = context.DeadlineExceeded
	assertAPIError(t, requestWithCookie(t, server, http.MethodGet, "/v1/admin/probes", cookie), http.StatusInternalServerError, "admin_probes_failed")
	assertAPIError(t, requestWithCookie(t, server, http.MethodGet, "/v1/admin/data", cookie), http.StatusInternalServerError, "admin_data_failed")
	serverWithoutOperations, err := New(service, WithAdminAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, requestWithCookie(t, serverWithoutOperations, http.MethodGet, "/v1/admin/probes", cookie), http.StatusServiceUnavailable, "admin_operations_unavailable")
	assertAPIError(t, requestWithCookie(t, serverWithoutOperations, http.MethodDelete, "/v1/admin/probes/probe", cookie), http.StatusServiceUnavailable, "admin_operations_unavailable")
	sessionRequest := requestWithCookie(t, server, http.MethodGet, "/v1/admin/session", cookie)
	if sessionRequest.Code != http.StatusOK || !strings.Contains(sessionRequest.Body.String(), `"userId":"admin"`) {
		t.Fatalf("admin session = %d, %s", sessionRequest.Code, sessionRequest.Body.String())
	}
	logout := requestWithCookie(t, server, http.MethodPost, "/v1/admin/logout", cookie)
	if logout.Code != http.StatusNoContent || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("admin logout = %d, cookies=%v", logout.Code, logout.Result().Cookies())
	}
	revoked := requestWithCookie(t, server, http.MethodGet, "/v1/admin/session", cookie)
	assertAPIError(t, revoked, http.StatusUnauthorized, "unauthorized")
}

func requestWithCookieAndHeaders(
	t *testing.T,
	server *Server,
	method string,
	path string,
	body any,
	cookie *http.Cookie,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(payload))
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	req.AddCookie(cookie)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	return response
}

func TestAdminLoginBoundsArgonWorkAndFailedAttempts(t *testing.T) {
	credential := AdminCredential{UserID: "admin", DisplayName: "Admin", Password: adminTestPassword}
	repository := newAdminSessionRepository()
	auth, err := NewAdminAuth([]AdminCredential{credential}, adminTestPepper, repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	realVerify := auth.verifyPassword
	auth.verifyPassword = func(string, string) (bool, error) { return false, nil }
	for attempt := 1; attempt <= adminLoginFailureLimit; attempt++ {
		_, _, loginErr := auth.Login(t.Context(), "198.51.100.8", "missing-"+string(rune('a'+attempt)), "wrong")
		if attempt < adminLoginFailureLimit && !errors.Is(loginErr, central.ErrUnauthorized) {
			t.Fatalf("failed login %d = %v", attempt, loginErr)
		}
		if attempt == adminLoginFailureLimit && !errors.Is(loginErr, central.ErrRateLimited) {
			t.Fatalf("rate-limited login = %v", loginErr)
		}
	}
	if _, _, err := auth.Login(t.Context(), "198.51.100.8", "admin", adminTestPassword); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("source limit bypassed with valid user = %v", err)
	}

	auth.loginLimiter = newAdminLoginLimiter(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	auth.verifyPassword = realVerify
	verify := auth.verifyPassword
	auth.verifyPassword = func(password, encoded string) (bool, error) {
		close(entered)
		<-release
		return verify(password, encoded)
	}
	result := make(chan error, 1)
	go func() {
		_, _, loginErr := auth.Login(t.Context(), "198.51.100.9", "admin", adminTestPassword)
		result <- loginErr
	}()
	<-entered
	if _, _, err := auth.Login(t.Context(), "198.51.100.10", "admin", adminTestPassword); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("concurrent Argon work was not rejected: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("admitted admin login = %v", err)
	}
}

func TestAdminLoginAPIReportsRateLimit(t *testing.T) {
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "Admin", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth.verifyPassword = func(string, string) (bool, error) { return false, nil }
	server, err := New(service, WithAdminAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	var response *httptest.ResponseRecorder
	for range adminLoginFailureLimit {
		response = request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
			"userId": "admin", "password": "wrong",
		}, nil)
	}
	assertAPIError(t, response, http.StatusTooManyRequests, "rate_limited")
	if response.Header().Get("Retry-After") != "600" {
		t.Fatalf("admin login Retry-After = %q", response.Header().Get("Retry-After"))
	}
}

func TestAdminLoginLimiterBoundsAttemptMemory(t *testing.T) {
	limiter := newAdminLoginLimiter(1)
	now := time.Now()
	for index := range adminLoginAttemptCapacity + 10 {
		limiter.recordFailure("198.51.100."+strconv.Itoa(index), "unknown", false, now.Add(time.Duration(index)))
	}
	if len(limiter.attempts) != adminLoginAttemptCapacity {
		t.Fatalf("attempt capacity = %d", len(limiter.attempts))
	}
	for key, attempt := range limiter.attempts {
		attempt.updatedAt = now.Add(-adminLoginAttemptRetention - time.Second)
		limiter.attempts[key] = attempt
	}
	release, err := limiter.acquire("198.51.100.20", "admin", now)
	if err != nil {
		t.Fatalf("expired attempt cleanup = %v", err)
	}
	release()
}

func TestAdminCookieAndClientBearerCannotCrossAuthenticationPlanes(t *testing.T) {
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	clientRepository := &apiResourceRepository{
		principal: central.ClientPrincipal{UserID: "client-user", SessionID: "client-session"},
		resources: make(map[string]central.ClientResource),
	}
	clients, err := central.NewClientService(clientRepository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "Admin", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	server, err := New(service, WithClientService(clients), WithAdminAuth(admin))
	if err != nil {
		t.Fatal(err)
	}
	login := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, nil)
	adminCookie := login.Result().Cookies()[0]

	adminWithBearer := request(t, server.Handler(), http.MethodGet, "/v1/admin/session", nil, map[string]string{
		"Authorization": "Bearer client-session", "X-Cineko-Protocol": "3",
	})
	assertAPIError(t, adminWithBearer, http.StatusUnauthorized, "unauthorized")

	clientRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/settings", nil)
	clientRequest.Header.Set("X-Cineko-Protocol", "3")
	clientRequest.AddCookie(adminCookie)
	clientResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(clientResponse, clientRequest)
	assertAPIError(t, clientResponse, http.StatusUnauthorized, "unauthorized")
}

func requestWithCookie(
	t *testing.T,
	server *Server,
	method string,
	path string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}
