package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestClientResourceAPISavesSettingsPresetsAndMonitors(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: "user", SessionID: "session"}
	repository := &apiResourceRepository{
		principal: principal,
		resources: make(map[string]central.ClientResource),
	}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(probeService, WithClientService(clients))
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		"Authorization":          "Bearer client-session",
		contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
		"If-None-Match":          "*",
	}

	presetHeaders := cloneHeaders(headers)
	presetHeaders["Idempotency-Key"] = "create-preset"
	preset := request(t, server.Handler(), http.MethodPost, "/v1/presets", map[string]any{
		"id": "preset", "data": map[string]any{
			"id": "preset", "userId": "user", "name": "Preset", "theaterId": "theater",
			"auditoriumId": "auditorium", "seatCount": 1, "seatPreference": map[string]any{},
		},
	}, presetHeaders)
	if preset.Code != http.StatusCreated {
		t.Fatalf("create preset = %d, %s", preset.Code, preset.Body.String())
	}
	presetUpdateHeaders := cloneHeaders(headers)
	delete(presetUpdateHeaders, "If-None-Match")
	presetUpdateHeaders["If-Match"] = `"1"`
	presetUpdateHeaders["Idempotency-Key"] = "update-preset"
	presetUpdate := request(t, server.Handler(), http.MethodPut, "/v1/presets/preset", map[string]any{
		"data": map[string]any{
			"id": "preset", "userId": "user", "name": "Updated preset", "theaterId": "theater",
			"auditoriumId": "auditorium", "seatCount": 1, "seatPreference": map[string]any{},
		},
	}, presetUpdateHeaders)
	if presetUpdate.Code != http.StatusOK {
		t.Fatalf("update preset = %d, %s", presetUpdate.Code, presetUpdate.Body.String())
	}

	monitorHeaders := cloneHeaders(headers)
	monitorHeaders["Idempotency-Key"] = "create-monitor"
	monitor := request(t, server.Handler(), http.MethodPost, "/v1/monitors", map[string]any{
		"id": "monitor", "data": map[string]any{
			"id": "monitor", "userId": "user", "presetId": "preset", "movieId": "movie", "movie": "Movie",
			"targetDates": []string{"2026-08-12"}, "pollInterval": int64(2 * time.Second),
			"pollIntervalMax": int64(3 * time.Second), "status": "pending",
		},
	}, monitorHeaders)
	if monitor.Code != http.StatusCreated {
		t.Fatalf("create monitor = %d, %s", monitor.Code, monitor.Body.String())
	}

	settingsHeaders := cloneHeaders(headers)
	settingsHeaders["Idempotency-Key"] = "save-settings"
	settings := request(t, server.Handler(), http.MethodPut, "/v1/settings", map[string]any{
		"data": map[string]any{"language": "ko-KR", "notifications": true},
	}, settingsHeaders)
	if settings.Code != http.StatusOK {
		t.Fatalf("save settings = %d, %s", settings.Code, settings.Body.String())
	}
	getHeaders := cloneHeaders(headers)
	delete(getHeaders, "If-None-Match")
	stored := request(t, server.Handler(), http.MethodGet, "/v1/settings", nil, getHeaders)
	if stored.Code != http.StatusOK || !json.Valid(stored.Body.Bytes()) {
		t.Fatalf("get settings = %d, %s", stored.Code, stored.Body.String())
	}

	foreignHeaders := cloneHeaders(headers)
	foreignHeaders["Idempotency-Key"] = "foreign-preset"
	foreign := request(t, server.Handler(), http.MethodPost, "/v1/presets", map[string]any{
		"id": "foreign", "data": map[string]any{
			"id": "foreign", "userId": "other", "name": "Foreign", "theaterId": "theater",
			"auditoriumId": "auditorium", "seatCount": 1, "seatPreference": map[string]any{},
		},
	}, foreignHeaders)
	assertAPIError(t, foreign, http.StatusBadRequest, "invalid_request")

	retryHeaders := cloneHeaders(headers)
	delete(retryHeaders, "If-None-Match")
	retry := request(t, server.Handler(), http.MethodPost, "/v1/executions/execution/retry", nil, retryHeaders)
	if retry.Code != http.StatusNoContent || repository.retriedExecution != "execution" {
		t.Fatalf("retry execution = %d, %s, command %q", retry.Code, retry.Body.String(), repository.retriedExecution)
	}
}

type apiResourceRepository struct {
	central.ClientRepository
	principal        central.ClientPrincipal
	resources        map[string]central.ClientResource
	retriedExecution string
}

func (repository *apiResourceRepository) AuthenticateClientSession(
	_ context.Context,
	tokenHash [32]byte,
	_ time.Time,
) (central.ClientPrincipal, error) {
	if tokenHash != sha256.Sum256([]byte("client-session")) {
		return central.ClientPrincipal{}, central.ErrUnauthorized
	}
	return repository.principal, nil
}

func (repository *apiResourceRepository) PutClientResource(
	_ context.Context,
	mutation central.ResourceMutation,
) (central.ClientResource, error) {
	key := mutation.Kind + "\x00" + mutation.ID
	revision := int64(1)
	createdAt := mutation.Now
	if existing, ok := repository.resources[key]; ok {
		revision = existing.Revision + 1
		createdAt = existing.CreatedAt
	}
	resource := central.ClientResource{
		Kind: mutation.Kind, ID: mutation.ID, UserID: mutation.UserID, Revision: revision,
		Data: mutation.Data, CreatedAt: createdAt, UpdatedAt: mutation.Now,
	}
	repository.resources[key] = resource
	return resource, nil
}

func (repository *apiResourceRepository) GetClientResource(
	_ context.Context,
	userID string,
	kind string,
	id string,
) (central.ClientResource, error) {
	resource, ok := repository.resources[kind+"\x00"+id]
	if !ok || resource.UserID != userID {
		return central.ClientResource{}, central.ErrNotFound
	}
	return resource, nil
}

func (repository *apiResourceRepository) RetryClientExecution(
	_ context.Context,
	userID string,
	commandID string,
	_ time.Time,
) error {
	if userID != repository.principal.UserID {
		return central.ErrNotFound
	}
	repository.retriedExecution = commandID
	return nil
}
