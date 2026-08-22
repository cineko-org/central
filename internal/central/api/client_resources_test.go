package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestClientResourceAPISavesSettingsPresetsAndMonitors(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: "user", SessionID: "session"}
	repository := &apiResourceRepository{
		principal: principal,
		resources: make(map[string]*clientpb.Resource),
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
		"Authorization": "Bearer client-session",
		"If-None-Match": "*",
	}

	presetHeaders := cloneHeaders(headers)
	presetHeaders["Idempotency-Key"] = "create-preset"
	preset := request(t, server.Handler(), http.MethodPost, "/v1/presets", apiPresetResource("user", "Preset"), presetHeaders)
	if preset.Code != http.StatusCreated {
		t.Fatalf("create preset = %d, %s", preset.Code, preset.Body.String())
	}
	presetUpdateHeaders := cloneHeaders(headers)
	delete(presetUpdateHeaders, "If-None-Match")
	presetUpdateHeaders["If-Match"] = `"1"`
	presetUpdateHeaders["Idempotency-Key"] = "update-preset"
	presetUpdate := request(t, server.Handler(), http.MethodPut, "/v1/presets/preset", apiPresetResource("user", "Updated preset"), presetUpdateHeaders)
	if presetUpdate.Code != http.StatusOK {
		t.Fatalf("update preset = %d, %s", presetUpdate.Code, presetUpdate.Body.String())
	}

	monitorHeaders := cloneHeaders(headers)
	monitorHeaders["Idempotency-Key"] = "create-monitor"
	monitor := request(t, server.Handler(), http.MethodPost, "/v1/monitors", apiMonitorResource("movie_1"), monitorHeaders)
	if monitor.Code != http.StatusCreated {
		t.Fatalf("create monitor = %d, %s", monitor.Code, monitor.Body.String())
	}
	invalidMonitorHeaders := cloneHeaders(headers)
	invalidMonitorHeaders["Idempotency-Key"] = "create-invalid-monitor"
	invalidMonitorResource := apiMonitorResource("")
	invalidMonitorResource.GetIdentity().SetId("invalid-monitor")
	invalidMonitorResource.GetMonitor().SetId("invalid-monitor")
	invalidMonitor := request(t, server.Handler(), http.MethodPost, "/v1/monitors", invalidMonitorResource, invalidMonitorHeaders)
	assertAPIError(t, invalidMonitor, http.StatusBadRequest, "invalid_request")

	settingsHeaders := cloneHeaders(headers)
	settingsHeaders["Idempotency-Key"] = "save-settings"
	settingsResource := &clientpb.Resource{}
	settingsIdentity := &commonpb.ResourceIdentity{}
	settingsIdentity.SetId("settings")
	settingsResource.SetIdentity(settingsIdentity)
	settingsResource.SetSettings(&clientpb.Settings{})
	settings := request(t, server.Handler(), http.MethodPut, "/v1/settings", settingsResource, settingsHeaders)
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
	foreignResource := apiPresetResource("other", "Foreign")
	foreignResource.GetIdentity().SetId("foreign")
	foreignResource.GetPreset().SetId("foreign")
	foreign := request(t, server.Handler(), http.MethodPost, "/v1/presets", foreignResource, foreignHeaders)
	assertAPIError(t, foreign, http.StatusBadRequest, "invalid_request")
}

func apiPresetResource(userID, name string) *clientpb.Resource {
	identity := &commonpb.ResourceIdentity{}
	identity.SetId("preset")
	preset := &clientpb.Preset{}
	preset.SetId("preset")
	preset.SetUserId(userID)
	preset.SetName(name)
	preset.SetTheaterId("theater")
	preset.SetAuditoriumId("auditorium")
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	resource := &clientpb.Resource{}
	resource.SetIdentity(identity)
	resource.SetPreset(preset)
	return resource
}

func apiMonitorResource(movieID string) *clientpb.Resource {
	identity := &commonpb.ResourceIdentity{}
	identity.SetId("monitor")
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(12)
	monitor := &clientpb.Monitor{}
	monitor.SetId("monitor")
	monitor.SetUserId("user")
	monitor.SetPresetId("preset")
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle("Movie")
	monitor.SetTargetDates([]*commonpb.LocalDate{date})
	monitor.SetSearchHorizonDays(14)
	monitor.SetState(state)
	resource := &clientpb.Resource{}
	resource.SetIdentity(identity)
	resource.SetMonitor(monitor)
	return resource
}

type apiResourceRepository struct {
	central.ClientRepository
	principal central.ClientPrincipal
	resources map[string]*clientpb.Resource
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
) (*clientpb.Resource, error) {
	key := mutation.Kind + "\x00" + mutation.ID
	revision := int64(1)
	createdAt := mutation.Now
	if existing, ok := repository.resources[key]; ok {
		revision = existing.GetIdentity().GetRevision() + 1
		createdAt = existing.GetIdentity().GetCreatedAt().AsTime()
	}
	resource := proto.CloneOf(mutation.Resource)
	resource.SetIdentity(commonpb.ResourceIdentity_builder{
		Id: &mutation.ID, Revision: &revision,
		CreatedAt: timestamppb.New(createdAt), UpdatedAt: timestamppb.New(mutation.Now),
	}.Build())
	repository.resources[key] = resource
	return resource, nil
}

func (repository *apiResourceRepository) GetClientResource(
	_ context.Context,
	userID string,
	kind string,
	id string,
) (*clientpb.Resource, error) {
	resource, ok := repository.resources[kind+"\x00"+id]
	if !ok || resourceOwnerID(resource) != userID {
		return nil, central.ErrNotFound
	}
	return resource, nil
}

func resourceOwnerID(resource *clientpb.Resource) string {
	switch {
	case resource.GetPreset() != nil:
		return resource.GetPreset().GetUserId()
	case resource.GetMonitor() != nil:
		return resource.GetMonitor().GetUserId()
	case resource.GetReservation() != nil:
		return resource.GetReservation().GetUserId()
	case resource.GetExternalOperation() != nil:
		return resource.GetExternalOperation().GetUserId()
	case resource.GetAppEvent() != nil:
		return resource.GetAppEvent().GetUserId()
	default:
		return userIDForSettings
	}
}

const userIDForSettings = "user"
