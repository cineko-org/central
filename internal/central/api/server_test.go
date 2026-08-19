package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
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
	if health.Code != http.StatusOK || health.Body.String() != "{\"status\":\"ready\"}\n" {
		t.Fatalf("health status = %d, body = %s", health.Code, health.Body.String())
	}

	registration := map[string]any{
		"installationId": "install_api", "kind": "container",
		"capabilities": []string{"cgv.schedule.capture.v2"}, "maxConcurrency": 1,
		"runtime": map[string]any{
			"version": "0.1.0", "protocol": central.ProtocolVersion, "browserRevision": "1228",
			"platform": "darwin", "arch": "arm64",
		},
	}
	missingProtocol := request(t, server.Handler(), http.MethodPost, "/v1/probes/register", registration, map[string]string{
		"Authorization": "Bearer enroll", "Idempotency-Key": "register_api",
	})
	assertAPIError(t, missingProtocol, http.StatusBadRequest, "unsupported_protocol")

	baseHeaders := map[string]string{
		"X-Cineko-Protocol": "3", "Authorization": "Bearer enroll",
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
	var registrationResponse central.RegisterProbeResponse
	if err := json.Unmarshal(registered.Body.Bytes(), &registrationResponse); err != nil {
		t.Fatal(err)
	}
	if registrationResponse.ProbeID == "" || registrationResponse.AccessToken == "" {
		t.Fatalf("registration response = %+v", registrationResponse)
	}

	probeHeaders := map[string]string{
		"X-Cineko-Protocol": "3", "Authorization": "Bearer " + registrationResponse.AccessToken,
	}
	heartbeat := request(t, server.Handler(), http.MethodPut,
		"/v1/probes/"+registrationResponse.ProbeID+"/heartbeat",
		map[string]any{"draining": false, "activeAssignmentIds": []string{}, "availableSlots": 1, "health": "healthy"},
		probeHeaders,
	)
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", heartbeat.Code, heartbeat.Body.String())
	}
	claim := request(t, server.Handler(), http.MethodPost,
		"/v1/probes/"+registrationResponse.ProbeID+"/assignments:claim", nil, probeHeaders,
	)
	if claim.Code != http.StatusNoContent {
		t.Fatalf("empty claim status = %d, body = %s", claim.Code, claim.Body.String())
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
	request.Header.Set("X-Cineko-Protocol", "3")
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
			got, valid := expectedRevision(recorder, request)
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

	provider := &reconcilerStatus{status: reconcile.Status{Healthy: true, Leader: true}}
	server, err := New(service, WithReconciler(provider))
	if err != nil {
		t.Fatal(err)
	}
	healthy := request(t, server.Handler(), http.MethodGet, "/health/reconciler", nil, nil)
	if healthy.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body = %s", healthy.Code, healthy.Body.String())
	}
	provider.status.Healthy = false
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
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
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
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != code {
		t.Fatalf("error code = %q, want %q", response.Error.Code, code)
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
	status reconcile.Status
}

func (provider *reconcilerStatus) Snapshot() reconcile.Status { return provider.status }

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
	heartbeat central.ProbeHeartbeatRequest,
	_ time.Time,
) (central.Probe, error) {
	repository.probe.Draining = heartbeat.Draining
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

func (*apiRepository) CommitResult(context.Context, central.ResultCommit) (central.ResultReceipt, error) {
	return central.ResultReceipt{}, errors.New("not implemented")
}
