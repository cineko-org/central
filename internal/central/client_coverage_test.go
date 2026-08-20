package central

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestClientServiceConstructionAndProvisioning(t *testing.T) {
	repository := &clientRepositoryFake{}
	if _, err := NewClientService(nil, time.Hour); err == nil {
		t.Fatal("nil client repository accepted")
	}
	if _, err := NewClientService(repository, -time.Second); err == nil {
		t.Fatal("negative client session TTL accepted")
	}
	if _, err := NewClientService(repository, time.Hour, time.Hour); err == nil {
		t.Fatal("refresh TTL not greater than session TTL accepted")
	}
	service, err := NewClientService(repository, 0)
	if err != nil || service.sessionTTL != DefaultClientSessionTTL {
		t.Fatalf("NewClientService(default) = %+v, %v", service, err)
	}
	service.clock = func() time.Time { return clientTestTime }
	if err := service.Provision(context.Background(), nil); err != nil {
		t.Fatalf("empty optional client credential seed = %v", err)
	}
	invalid := []ClientCredentialSeed{
		{DisplayName: "User", AccessToken: clientTestToken},
		{UserID: "user", AccessToken: clientTestToken},
		{UserID: "user", DisplayName: "User", AccessToken: "short"},
	}
	for _, seed := range invalid {
		if err := service.Provision(context.Background(), []ClientCredentialSeed{seed}); err == nil {
			t.Fatalf("invalid client seed accepted: %+v", seed)
		}
	}
	if err := service.Provision(context.Background(), []ClientCredentialSeed{
		{UserID: " user ", DisplayName: " User ", AccessToken: " " + clientTestToken + " "},
		{UserID: "user", DisplayName: "Duplicate", AccessToken: clientTestToken},
	}); err == nil {
		t.Fatal("duplicate client credential seed accepted")
	}
	repository.err = errInjectedClient
	if err := service.Provision(context.Background(), []ClientCredentialSeed{
		{UserID: "user", DisplayName: "User", AccessToken: clientTestToken},
	}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Provision(repository error) = %v", err)
	}
	repository.err = nil
	if err := service.Provision(context.Background(), []ClientCredentialSeed{
		{UserID: " user ", DisplayName: " User ", AccessToken: " " + clientTestToken + " "},
	}); err != nil {
		t.Fatal(err)
	}
	if repository.user.ID != "user" || repository.user.DisplayName != "User" ||
		repository.hash != sha256.Sum256([]byte(clientTestToken)) || !repository.user.CreatedAt.Equal(clientTestTime) {
		t.Fatalf("provisioned client credential = %+v, %x", repository.user, repository.hash)
	}
}

func TestClientServiceAuthenticationLifecycle(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	ctx := context.Background()
	if _, err := service.Exchange(ctx, AuthExchangeRequest{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Exchange(empty) = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.Exchange(ctx, AuthExchangeRequest{UserID: "user", AccessToken: clientTestToken}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Exchange(repository error) = %v", err)
	}
	repository.err = nil
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.Exchange(ctx, AuthExchangeRequest{UserID: "user", AccessToken: clientTestToken}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(token random error) = %v", err)
	}
	calls := 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, errInjectedClient
		}
		for index := range buffer {
			buffer[index] = byte(index + 1)
		}
		return len(buffer), nil
	}
	if _, err := service.Exchange(ctx, AuthExchangeRequest{UserID: "user", AccessToken: clientTestToken}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(refresh token random error) = %v", err)
	}
	calls = 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 3 {
			return 0, errInjectedClient
		}
		for index := range buffer {
			buffer[index] = byte(index + 1)
		}
		return len(buffer), nil
	}
	if _, err := service.Exchange(ctx, AuthExchangeRequest{UserID: "user", AccessToken: clientTestToken}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(session id random error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "create-session"
	if _, err := service.Exchange(ctx, AuthExchangeRequest{UserID: "user", AccessToken: clientTestToken}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(create session error) = %v", err)
	}
	repository.fail = ""
	response, err := service.Exchange(ctx, AuthExchangeRequest{UserID: " user ", AccessToken: " " + clientTestToken + " "})
	if err != nil || response.AccessToken == "" || response.RefreshToken == "" || response.User.ID != "user" ||
		!response.ExpiresAt.Equal(clientTestTime.Add(time.Hour)) ||
		!response.RefreshExpiresAt.Equal(clientTestTime.Add(DefaultClientRefreshTTL)) || repository.session.ID == "" {
		t.Fatalf("Exchange() = %+v, %+v, %v", response, repository.session, err)
	}
	if _, err := service.Refresh(ctx, AuthRefreshRequest{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh(empty) = %v", err)
	}
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.Refresh(ctx, AuthRefreshRequest{RefreshToken: "refresh-token"}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Refresh(token random error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "rotate-session"
	if _, err := service.Refresh(ctx, AuthRefreshRequest{RefreshToken: "refresh-token"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh(rotation error) = %v", err)
	}
	repository.fail = ""
	refreshed, err := service.Refresh(ctx, AuthRefreshRequest{RefreshToken: " refresh-token "})
	if err != nil || refreshed.User.ID != "user" || refreshed.RefreshToken == "" ||
		repository.hash != sha256.Sum256([]byte("refresh-token")) {
		t.Fatalf("Refresh() = %+v, %v", refreshed, err)
	}
	if _, err := service.Authenticate(ctx, " "); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate(empty) = %v", err)
	}
	principal, err := service.Authenticate(ctx, " session-token ")
	if err != nil || principal.UserID != "user" || repository.hash != sha256.Sum256([]byte("session-token")) {
		t.Fatalf("Authenticate() = %+v, %v", principal, err)
	}
	if err := service.Logout(ctx, ClientPrincipal{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Logout(empty) = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.Logout(ctx, principal); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Logout(repository error) = %v", err)
	}
	repository.err = nil
	principal.SessionID = "session"
	if err := service.Logout(ctx, principal); err != nil || repository.revokedSessionID != "session" {
		t.Fatalf("Logout() = %q, %v", repository.revokedSessionID, err)
	}
}

func TestClientServiceBootstrapAndDevice(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	ctx := context.Background()
	principal := ClientPrincipal{UserID: "user", SessionID: "session"}
	repository.fail = "user"
	if _, err := service.Bootstrap(ctx, principal, ""); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Bootstrap(user error) = %v", err)
	}
	repository.fail = "revisions"
	if _, err := service.Bootstrap(ctx, principal, ""); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Bootstrap(revision error) = %v", err)
	}
	repository.fail = "device-not-found"
	bootstrap, err := service.Bootstrap(ctx, principal, "install")
	if err != nil || bootstrap.Device != nil || bootstrap.Protocol != ProtocolVersion || !bootstrap.Features["centralState"] {
		t.Fatalf("Bootstrap(missing device) = %+v, %v", bootstrap, err)
	}
	repository.fail = "device"
	if _, err := service.Bootstrap(ctx, principal, "install"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Bootstrap(device error) = %v", err)
	}
	repository.fail = ""
	bootstrap, err = service.Bootstrap(ctx, principal, "install")
	if err != nil || bootstrap.Device == nil || bootstrap.Device.InstallationID != "install" {
		t.Fatalf("Bootstrap() = %+v, %v", bootstrap, err)
	}

	invalidDevices := []ClientDevice{
		{},
		{InstallationID: "install"},
		{InstallationID: "install", DeviceID: "device"},
		{InstallationID: "install", DeviceID: "device", Platform: "darwin"},
		{InstallationID: "install", DeviceID: "device", Platform: "darwin", Arch: "arm64"},
	}
	for _, device := range invalidDevices {
		if _, err := service.UpsertDevice(ctx, principal, device); !errors.Is(err, ErrInvalid) {
			t.Fatalf("UpsertDevice(invalid %+v) = %v", device, err)
		}
	}
	device, err := service.UpsertDevice(ctx, principal, ClientDevice{
		InstallationID: " install ", DeviceID: " device ", Platform: " darwin ", Arch: " arm64 ",
		AppVersion: " 1.0.0 ",
	})
	if err != nil || device.UserID != "user" || !device.CreatedAt.Equal(clientTestTime) {
		t.Fatalf("UpsertDevice() = %+v, %v", device, err)
	}
	existingCreatedAt := clientTestTime.Add(-time.Hour)
	device, err = service.UpsertDevice(ctx, principal, ClientDevice{
		InstallationID: "install", DeviceID: "device", Platform: "darwin", Arch: "arm64",
		AppVersion: "1.0.1", CreatedAt: existingCreatedAt,
	})
	if err != nil || !device.CreatedAt.Equal(existingCreatedAt) {
		t.Fatalf("UpsertDevice(existing) = %+v, %v", device, err)
	}
}

func TestClientServiceResourceAndEventOperations(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	ctx := context.Background()
	principal := ClientPrincipal{UserID: "user"}
	if _, err := service.ListResources(ctx, principal, "unknown"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ListResources(invalid) = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.ListResources(ctx, principal, "presets"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ListResources(repository error) = %v", err)
	}
	repository.err = nil
	if values, err := service.ListResources(ctx, principal, "presets"); err != nil || len(values) != 1 {
		t.Fatalf("ListResources() = %+v, %v", values, err)
	}
	for _, input := range []struct{ kind, id string }{{"unknown", "id"}, {"presets", ""}} {
		if _, err := service.GetResource(ctx, principal, input.kind, input.id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("GetResource(%q, %q) = %v", input.kind, input.id, err)
		}
	}
	if _, err := service.GetResource(ctx, principal, "presets", "id"); err != nil {
		t.Fatal(err)
	}
	repository.err = errInjectedClient
	if _, err := service.GetResource(ctx, principal, "presets", "id"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("GetResource(repository error) = %v", err)
	}
	repository.err = nil
	repository.resource.Data = json.RawMessage(`{}`)
	if _, err := service.GetResource(ctx, principal, "presets", "id"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("GetResource(corrupt) = %v", err)
	}
	if _, err := service.ListResources(ctx, principal, "presets"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("ListResources(corrupt) = %v", err)
	}
	repository.resource.Data = validClientPresetPayload()
	repository.resource.UserID = "other"
	if _, err := service.GetResource(ctx, principal, "presets", "id"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("GetResource(foreign row) = %v", err)
	}
	repository.resource.UserID = "user"
	repository.resource.ID = "other"
	if _, err := service.GetResource(ctx, principal, "presets", "id"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("GetResource(wrong id) = %v", err)
	}
	repository.resource.ID = "id"

	data := validClientPresetPayload()
	invalidPut := []struct {
		kind, id, command string
		data              json.RawMessage
	}{
		{"unknown", "id", "command", data},
		{"presets", "", "command", data},
		{"presets", "id", "", data},
		{"presets", "id", "command", json.RawMessage(`{`)},
	}
	for _, input := range invalidPut {
		if _, err := service.PutResource(ctx, principal, input.kind, input.id, input.data, nil, input.command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("PutResource(invalid %+v) = %v", input, err)
		}
	}
	if _, err := service.PutResource(
		ctx, principal, "presets", "id", json.RawMessage(`{"id":"id","userId":"other"}`), nil, "command",
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PutResource(foreign owner) = %v", err)
	}
	zero := int64(0)
	if _, err := service.PutResource(ctx, principal, "presets", "id", data, &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PutResource(zero revision) = %v", err)
	}
	resource, err := service.PutResource(ctx, principal, "presets", "id", data, nil, " command ")
	if err != nil || resource.Kind != "presets" || repository.mutation.CommandID != " command " {
		t.Fatalf("PutResource() = %+v, %+v, %v", resource, repository.mutation, err)
	}

	for _, input := range []struct{ kind, id, command string }{
		{"unknown", "id", "command"}, {"presets", "", "command"}, {"presets", "id", ""},
	} {
		if _, err := service.DeleteResource(ctx, principal, input.kind, input.id, &resource.Revision, input.command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("DeleteResource(invalid %+v) = %v", input, err)
		}
	}
	if _, err := service.DeleteResource(ctx, principal, "presets", "id", nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteResource(nil revision) = %v", err)
	}
	if _, err := service.DeleteResource(ctx, principal, "presets", "id", &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteResource(zero revision) = %v", err)
	}
	if _, err := service.DeleteResource(ctx, principal, "presets", "id", &resource.Revision, "command"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Events(ctx, principal, -1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Events(negative cursor) = %v", err)
	}
	if _, err := service.Events(ctx, principal, 0, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Events(negative limit) = %v", err)
	}
	if _, err := service.Events(ctx, principal, 0, MaximumEventPageSize+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Events(large limit) = %v", err)
	}
	events, err := service.Events(ctx, principal, 2, 0)
	if err != nil || len(events) != 1 || repository.after != 2 || repository.limit != DefaultEventPageSize {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
}

var (
	errInjectedClient = errors.New("injected client failure")
	clientTestTime    = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
)

const clientTestToken = "0123456789abcdef0123456789abcdef"

func newClientServiceHarness(t *testing.T) (*ClientService, *clientRepositoryFake) {
	t.Helper()
	repository := &clientRepositoryFake{
		user:      ClientUser{ID: "user", DisplayName: "User", CreatedAt: clientTestTime, UpdatedAt: clientTestTime},
		principal: ClientPrincipal{UserID: "user", SessionID: "session"},
		device:    ClientDevice{InstallationID: "install", UserID: "user"},
		resource:  ClientResource{Kind: "presets", ID: "id", UserID: "user", Revision: 1, Data: validClientPresetPayload()},
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom
	return service, repository
}

func validClientPresetPayload() json.RawMessage {
	return json.RawMessage(`{"id":"id","userId":"user","name":"Preset","theaterId":"theater","auditoriumId":"auditorium","seatCount":1,"seatPreference":{}}`)
}

func deterministicClientRandom(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(index + 1)
	}
	return len(buffer), nil
}

type clientRepositoryFake struct {
	err              error
	fail             string
	user             ClientUser
	hash             [32]byte
	session          ClientSession
	principal        ClientPrincipal
	device           ClientDevice
	resource         ClientResource
	mutation         ResourceMutation
	ticket           LaunchTicket
	execution        ExecutionCommand
	after            int64
	limit            int
	revokedSessionID string
}

func (repository *clientRepositoryFake) RevokeClientSession(
	_ context.Context,
	sessionID string,
	_ time.Time,
) error {
	repository.revokedSessionID = sessionID
	return repository.err
}

func (repository *clientRepositoryFake) ProvisionClientCredential(
	_ context.Context, user ClientUser, hash [32]byte,
) error {
	repository.user, repository.hash = user, hash
	return repository.err
}

func (repository *clientRepositoryFake) ExchangeClientCredential(
	_ context.Context, _ string, hash [32]byte, _ time.Time,
) (ClientUser, error) {
	repository.hash = hash
	return repository.user, repository.err
}

func (repository *clientRepositoryFake) CreateClientSession(_ context.Context, session ClientSession) error {
	repository.session = session
	if repository.fail == "create-session" {
		return errInjectedClient
	}
	return repository.err
}

func (repository *clientRepositoryFake) RotateClientSession(
	_ context.Context,
	hash [32]byte,
	session ClientSession,
	_ time.Time,
) (ClientUser, error) {
	repository.hash, repository.session = hash, session
	if repository.fail == "rotate-session" {
		return ClientUser{}, errInjectedClient
	}
	return repository.user, repository.err
}

func (repository *clientRepositoryFake) CreateLaunchTicket(_ context.Context, ticket LaunchTicket) error {
	repository.ticket = ticket
	if repository.fail == "create-launch-ticket" {
		return errInjectedClient
	}
	return repository.err
}

func (repository *clientRepositoryFake) ExchangeLaunchTicket(
	_ context.Context,
	hash [32]byte,
	_ string,
	releaseGeneration int64,
	session ClientSession,
	_ time.Time,
) (LaunchedClient, error) {
	repository.hash, repository.session = hash, session
	if repository.fail == "exchange-launch-ticket" {
		return LaunchedClient{}, errInjectedClient
	}
	if repository.fail == "exchange-stale-release" {
		return LaunchedClient{}, ErrStaleRelease
	}
	return LaunchedClient{
		User: repository.user,
		Context: ClientLaunchContext{
			InstallationID: "install", DeviceID: "device", ReleaseGeneration: releaseGeneration,
			ClientVersion:  "1.0.0",
			ArtifactSHA256: validClientRelease().Artifact.SHA256, Protocol: ProtocolVersion,
			BrowserRevision: "1234", BrowserArtifactSHA256: validBrowserRelease().Artifact.SHA256,
			PlaywrightVersion:        validPlaywrightRelease().Version,
			PlaywrightArtifactSHA256: validPlaywrightRelease().Artifact.SHA256,
		},
	}, repository.err
}

func (repository *clientRepositoryFake) AuthenticateClientSession(
	_ context.Context, hash [32]byte, _ time.Time,
) (ClientPrincipal, error) {
	repository.hash = hash
	return repository.principal, repository.err
}

func (repository *clientRepositoryFake) UpsertClientDevice(
	_ context.Context, device ClientDevice,
) (ClientDevice, error) {
	repository.device = device
	return device, repository.err
}

func (repository *clientRepositoryFake) GetClientDevice(
	context.Context, string, string,
) (ClientDevice, error) {
	switch repository.fail {
	case "device-not-found":
		return ClientDevice{}, ErrNotFound
	case "device":
		return ClientDevice{}, errInjectedClient
	default:
		return repository.device, nil
	}
}

func (repository *clientRepositoryFake) GetClientUser(context.Context, string) (ClientUser, error) {
	if repository.fail == "user" {
		return ClientUser{}, errInjectedClient
	}
	return repository.user, nil
}

func (repository *clientRepositoryFake) ClientResourceRevisions(context.Context, string) (map[string]int64, error) {
	if repository.fail == "revisions" {
		return nil, errInjectedClient
	}
	return map[string]int64{"presets": 1}, nil
}

func (repository *clientRepositoryFake) ListClientResources(context.Context, string, string) ([]ClientResource, error) {
	return []ClientResource{repository.resource}, repository.err
}

func (repository *clientRepositoryFake) GetClientResource(context.Context, string, string, string) (ClientResource, error) {
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) PutClientResource(
	_ context.Context, mutation ResourceMutation,
) (ClientResource, error) {
	repository.mutation = mutation
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) DeleteClientResource(
	_ context.Context, mutation ResourceMutation,
) (ClientResource, error) {
	repository.mutation = mutation
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) ListClientEvents(
	_ context.Context, _ string, after int64, limit int,
) ([]ClientEvent, error) {
	repository.after, repository.limit = after, limit
	return []ClientEvent{{Sequence: 3, ID: "event"}}, repository.err
}

func (repository *clientRepositoryFake) ClaimClientExecution(
	_ context.Context,
	claim ExecutionClaim,
) (ExecutionCommand, error) {
	if repository.fail == "execution-not-found" {
		return ExecutionCommand{}, ErrNotFound
	}
	if repository.fail == "execution" {
		return ExecutionCommand{}, errInjectedClient
	}
	repository.execution = ExecutionCommand{
		ID: "execution", UserID: claim.UserID, InstallationID: claim.InstallationID,
		LeaseExpiresAt: claim.LeaseExpiresAt,
	}
	return repository.execution, nil
}

func (repository *clientRepositoryFake) HeartbeatClientExecution(
	_ context.Context,
	_ string,
	_ string,
	hash [32]byte,
	_ time.Time,
	_ time.Time,
) error {
	repository.hash = hash
	return repository.err
}

func (repository *clientRepositoryFake) CompleteClientExecution(
	_ context.Context,
	completion ExecutionCompletion,
) error {
	repository.hash = completion.LeaseHash
	return repository.err
}

func (repository *clientRepositoryFake) RetryClientExecution(
	_ context.Context,
	_ string,
	_ string,
	_ time.Time,
) error {
	return repository.err
}
