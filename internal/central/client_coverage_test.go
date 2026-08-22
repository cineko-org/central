package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errInjectedClient = errors.New("injected client failure")
	clientTestTime    = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
)

const clientTestToken = "0123456789abcdef0123456789abcdef"

func TestClientAuthenticationUsesGeneratedProto(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	request := &clientpb.TokenExchangeRequest{}
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Exchange(empty) = %v", err)
	}
	request.SetUserId(" user ")
	request.SetAccessToken(" " + clientTestToken + " ")
	response, err := service.Exchange(t.Context(), request)
	if err != nil || response.GetUser().GetId() != "user" || response.GetAccessToken() == "" || response.GetRefreshToken() == "" {
		t.Fatalf("Exchange() = %+v, %v", response, err)
	}
	if repository.hash != sha256.Sum256([]byte(clientTestToken)) || repository.session.ID == "" {
		t.Fatalf("credential digest/session = %x, %+v", repository.hash, repository.session)
	}
	refresh := &clientpb.TokenRefreshRequest{}
	refresh.SetRefreshToken(" refresh-token ")
	refreshed, err := service.Refresh(t.Context(), refresh)
	if err != nil || refreshed.GetUser().GetId() != "user" || repository.hash != sha256.Sum256([]byte("refresh-token")) {
		t.Fatalf("Refresh() = %+v, %v", refreshed, err)
	}
}

func TestClientBootstrapDeviceAndResourceUseGeneratedProto(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	principal := ClientPrincipal{UserID: "user", SessionID: "session"}
	bootstrap, err := service.Bootstrap(t.Context(), principal, "install")
	if err != nil || bootstrap.GetUser().GetId() != "user" || bootstrap.GetDevice().GetInstallationId() != "install" ||
		!bootstrap.GetFeatures()["centralState"] {
		t.Fatalf("Bootstrap() = %+v, %v", bootstrap, err)
	}

	device := &clientpb.Device{}
	device.SetInstallationId(" install ")
	device.SetDeviceId(" device ")
	device.SetPlatform(" darwin ")
	device.SetArchitecture(" arm64 ")
	device.SetAppVersion(" 1.0.0 ")
	storedDevice, err := service.UpsertDevice(t.Context(), principal, device)
	if err != nil || storedDevice.GetUserId() != "user" || storedDevice.GetCreatedAt() == nil {
		t.Fatalf("UpsertDevice() = %+v, %v", storedDevice, err)
	}

	resource := validClientPresetResource()
	stored, err := service.PutResource(t.Context(), principal, "presets", "id", resource, nil, "command")
	if err != nil || stored.GetPreset().GetId() != "id" || repository.mutation.Resource != resource {
		t.Fatalf("PutResource() = %+v, %+v, %v", stored, repository.mutation, err)
	}
	revision := stored.GetIdentity().GetRevision()
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", &revision, "command"); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(t.Context(), principal, 2, 0)
	if err != nil || len(events) != 1 || events[0].GetSequence() != 3 || repository.after != 2 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
}

func TestClientServiceRejectsInvalidGeneratedResources(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	principal := ClientPrincipal{UserID: "user"}
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", nil, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil resource error = %v", err)
	}
	foreign := validClientPresetResource()
	foreign.GetPreset().SetUserId("other")
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", foreign, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign resource error = %v", err)
	}
	zero := int64(0)
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid delete revision error = %v", err)
	}
}

func newClientServiceHarness(t *testing.T) (*ClientService, *clientRepositoryFake) {
	t.Helper()
	repository := &clientRepositoryFake{
		user:      clientTestUser(),
		principal: ClientPrincipal{UserID: "user", SessionID: "session"},
		device:    clientTestDevice(),
		resource:  validClientPresetResource(),
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom
	return service, repository
}

func clientTestUser() *clientpb.User {
	user := &clientpb.User{}
	user.SetId("user")
	user.SetDisplayName("User")
	user.SetCreatedAt(timestamppb.New(clientTestTime))
	user.SetUpdatedAt(timestamppb.New(clientTestTime))
	return user
}

func clientTestDevice() *clientpb.Device {
	device := &clientpb.Device{}
	device.SetInstallationId("install")
	device.SetUserId("user")
	device.SetDeviceId("device")
	device.SetPlatform("darwin")
	device.SetArchitecture("arm64")
	device.SetAppVersion("1.0.0")
	return device
}

func validClientPresetResource() *clientpb.Resource {
	identity := &commonpb.ResourceIdentity{}
	identity.SetId("id")
	identity.SetRevision(1)
	preset := &clientpb.Preset{}
	preset.SetId("id")
	preset.SetUserId("user")
	preset.SetName("Preset")
	preset.SetTheaterId("theater")
	preset.SetAuditoriumId("auditorium")
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	resource := &clientpb.Resource{}
	resource.SetIdentity(identity)
	resource.SetPreset(preset)
	return resource
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
	user             *clientpb.User
	hash             [32]byte
	session          ClientSession
	principal        ClientPrincipal
	device           *clientpb.Device
	resource         *clientpb.Resource
	mutation         ResourceMutation
	ticket           LaunchTicket
	execution        *executionpb.Command
	after            int64
	limit            int
	revokedSessionID string
}

func (repository *clientRepositoryFake) RevokeClientSession(_ context.Context, sessionID string, _ time.Time) error {
	repository.revokedSessionID = sessionID
	return repository.err
}

func (repository *clientRepositoryFake) ProvisionClientCredential(_ context.Context, user *clientpb.User, hash [32]byte) error {
	repository.user, repository.hash = user, hash
	return repository.err
}

func (repository *clientRepositoryFake) ExchangeClientCredential(_ context.Context, _ string, hash [32]byte, _ time.Time) (*clientpb.User, error) {
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

func (repository *clientRepositoryFake) RotateClientSession(_ context.Context, hash [32]byte, session ClientSession, _ time.Time) (*clientpb.User, error) {
	repository.hash, repository.session = hash, session
	if repository.fail == "rotate-session" {
		return nil, errInjectedClient
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

func (repository *clientRepositoryFake) ExchangeLaunchTicket(_ context.Context, hash [32]byte, _ string, generation int64, session ClientSession, _ time.Time) (LaunchedClient, error) {
	repository.hash, repository.session = hash, session
	if repository.fail == "exchange-launch-ticket" {
		return LaunchedClient{}, errInjectedClient
	}
	if repository.fail == "exchange-stale-release" {
		return LaunchedClient{}, ErrStaleRelease
	}
	launch := &clientpb.LaunchContext{}
	launch.SetInstallationId("install")
	launch.SetDeviceId("device")
	launch.SetReleaseGeneration(generation)
	launch.SetClientVersion("1.0.0")
	launch.SetArtifactSha256(validClientRelease().GetArtifact().GetSha256())
	launch.SetBrowserRevision("1234")
	launch.SetBrowserArtifactSha256(validBrowserRelease().GetArtifact().GetSha256())
	launch.SetPlaywrightVersion(validPlaywrightRelease().GetVersion())
	launch.SetPlaywrightArtifactSha256(validPlaywrightRelease().GetArtifact().GetSha256())
	return LaunchedClient{User: repository.user, Context: launch}, repository.err
}

func (repository *clientRepositoryFake) AuthenticateClientSession(_ context.Context, hash [32]byte, _ time.Time) (ClientPrincipal, error) {
	repository.hash = hash
	return repository.principal, repository.err
}

func (repository *clientRepositoryFake) UpsertClientDevice(_ context.Context, device *clientpb.Device) (*clientpb.Device, error) {
	repository.device = device
	return device, repository.err
}

func (repository *clientRepositoryFake) GetClientDevice(context.Context, string, string) (*clientpb.Device, error) {
	switch repository.fail {
	case "device-not-found":
		return nil, ErrNotFound
	case "device":
		return nil, errInjectedClient
	default:
		return repository.device, nil
	}
}

func (repository *clientRepositoryFake) GetClientUser(context.Context, string) (*clientpb.User, error) {
	if repository.fail == "user" {
		return nil, errInjectedClient
	}
	return repository.user, nil
}

func (repository *clientRepositoryFake) ClientResourceRevisions(context.Context, string) (map[string]int64, error) {
	if repository.fail == "revisions" {
		return nil, errInjectedClient
	}
	return map[string]int64{"presets": 1}, nil
}

func (repository *clientRepositoryFake) ListClientResources(context.Context, string, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{repository.resource}, repository.err
}

func (repository *clientRepositoryFake) GetClientResource(context.Context, string, string, string) (*clientpb.Resource, error) {
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) PutClientResource(_ context.Context, mutation ResourceMutation) (*clientpb.Resource, error) {
	repository.mutation = mutation
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) DeleteClientResource(_ context.Context, mutation ResourceMutation) (*clientpb.Resource, error) {
	repository.mutation = mutation
	return repository.resource, repository.err
}

func (repository *clientRepositoryFake) ListClientEvents(_ context.Context, _ string, after int64, limit int) ([]*clientpb.ClientEvent, error) {
	repository.after, repository.limit = after, limit
	event := &clientpb.ClientEvent{}
	event.SetSequence(3)
	event.SetId("event")
	return []*clientpb.ClientEvent{event}, repository.err
}

func (repository *clientRepositoryFake) ClaimClientExecution(_ context.Context, claim ExecutionClaim) (*executionpb.Command, error) {
	if repository.fail == "execution-not-found" {
		return nil, ErrNotFound
	}
	if repository.fail == "execution" {
		return nil, errInjectedClient
	}
	repository.execution = &executionpb.Command{}
	repository.execution.SetId("execution")
	repository.execution.SetInstallationId(claim.InstallationID)
	repository.execution.SetLeaseExpiresAt(timestamppb.New(claim.LeaseExpiresAt))
	return repository.execution, nil
}

func (repository *clientRepositoryFake) HeartbeatClientExecution(_ context.Context, _, _ string, hash [32]byte, _, _ time.Time) error {
	repository.hash = hash
	return repository.err
}

func (repository *clientRepositoryFake) CompleteClientExecution(_ context.Context, completion ExecutionCompletion) error {
	repository.hash = completion.LeaseHash
	return repository.err
}
