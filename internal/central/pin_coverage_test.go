package central

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

const pinTestPepper = "0123456789abcdef0123456789abcdef"

func TestPINServiceConfigurationAndUserManagement(t *testing.T) {
	clients, _ := newClientServiceHarness(t)
	repository := &pinRepositoryFake{}
	for _, input := range []struct {
		repository ClientPINRepository
		clients    *ClientService
		pepper     string
	}{{nil, clients, pinTestPepper}, {repository, nil, pinTestPepper}, {repository, clients, "short"}} {
		if _, err := NewPINService(input.repository, input.clients, input.pepper); err == nil {
			t.Fatalf("NewPINService(%+v) succeeded", input)
		}
	}
	service, err := NewPINService(repository, clients, pinTestPepper)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom
	repository.users = []ClientPINUser{{User: ClientUser{ID: "user"}, PINActive: true}}
	users, err := service.ListUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("ListUsers() = %+v, %v", users, err)
	}
	for _, name := range []string{"", strings.Repeat("가", 101)} {
		if _, err := service.CreateUser(t.Context(), name); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CreateUser(%q) = %v", name, err)
		}
	}
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.CreateUser(t.Context(), "User"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CreateUser(random error) = %v", err)
	}
	service.random = func(buffer []byte) (int, error) { return len(buffer) - 1, nil }
	if _, err := service.CreateUser(t.Context(), "User"); err == nil {
		t.Fatal("CreateUser(short random read) succeeded")
	}
	service.random = func(buffer []byte) (int, error) {
		if len(buffer) == 4 {
			return 0, errInjectedClient
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := service.CreateUser(t.Context(), "User"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CreateUser(PIN random error) = %v", err)
	}
	service.random = func(buffer []byte) (int, error) {
		if len(buffer) == 4 {
			return len(buffer) - 1, nil
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := service.CreateUser(t.Context(), "User"); err == nil {
		t.Fatal("CreateUser(short PIN random read) succeeded")
	}
	service.random = deterministicClientRandom
	repository.err = errInjectedClient
	if _, err := service.CreateUser(t.Context(), "User"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CreateUser(repository error) = %v", err)
	}
	repository.err = nil
	repository.conflicts = pinGenerationAttempts
	if _, err := service.CreateUser(t.Context(), "User"); err == nil {
		t.Fatal("CreateUser(exhausted PIN conflicts) succeeded")
	}
	repository.conflicts = 1
	issue, err := service.CreateUser(t.Context(), " User ")
	if err != nil || issue.User.ID == "" || issue.User.DisplayName != "User" || !validPIN(issue.PIN) {
		t.Fatalf("CreateUser() = %+v, %v", issue, err)
	}
	if repository.digest != service.digest("pin", issue.PIN) {
		t.Fatal("CreateUser stored the wrong PIN digest")
	}

	if _, err := service.Rotate(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Rotate(empty) = %v", err)
	}
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.Rotate(t.Context(), "user"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Rotate(random error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.err = errInjectedClient
	if _, err := service.Rotate(t.Context(), "user"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Rotate(repository error) = %v", err)
	}
	repository.err = nil
	repository.conflicts = pinGenerationAttempts
	if _, err := service.Rotate(t.Context(), "user"); err == nil {
		t.Fatal("Rotate(exhausted PIN conflicts) succeeded")
	}
	repository.conflicts = 0
	repository.user = ClientUser{ID: "user", DisplayName: "User"}
	rotated, err := service.Rotate(t.Context(), " user ")
	if err != nil || rotated.User.ID != "user" || !validPIN(rotated.PIN) {
		t.Fatalf("Rotate() = %+v, %v", rotated, err)
	}
	if err := service.DeleteUser(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteUser(empty) = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.DeleteUser(t.Context(), "user"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("DeleteUser(repository error) = %v", err)
	}
	repository.err = nil
	if err := service.DeleteUser(t.Context(), " user "); err != nil || repository.userID != "user" {
		t.Fatalf("DeleteUser() = %q, %v", repository.userID, err)
	}
}

func TestPINServiceExchangeAndSampling(t *testing.T) {
	clients, clientRepository := newClientServiceHarness(t)
	repository := &pinRepositoryFake{user: ClientUser{ID: "user", DisplayName: "User"}}
	service, err := NewPINService(repository, clients, pinTestPepper)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom
	valid := ClientPINExchangeRequest{
		PIN: "123456", InstallationID: "install_1234567890", DeviceID: "device_123456789012",
	}
	for _, input := range []struct {
		request ClientPINExchangeRequest
		source  string
	}{
		{request: ClientPINExchangeRequest{}, source: "source"},
		{request: ClientPINExchangeRequest{PIN: "12345a", InstallationID: valid.InstallationID, DeviceID: valid.DeviceID}, source: "source"},
		{request: ClientPINExchangeRequest{PIN: valid.PIN, InstallationID: "short", DeviceID: valid.DeviceID}, source: "source"},
		{request: ClientPINExchangeRequest{PIN: valid.PIN, InstallationID: valid.InstallationID, DeviceID: "short"}, source: "source"},
		{request: valid, source: " "},
	} {
		if _, err := service.Exchange(t.Context(), input.request, input.source); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Exchange(%+v, %q) = %v", input.request, input.source, err)
		}
	}
	repository.err = ErrRateLimited
	if _, err := service.Exchange(t.Context(), valid, "source"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Exchange(rate limited) = %v", err)
	}
	repository.err = nil
	clients.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.Exchange(t.Context(), valid, "source"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(session random error) = %v", err)
	}
	clients.random = deterministicClientRandom
	clientRepository.fail = "create-session"
	if _, err := service.Exchange(t.Context(), valid, "source"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("Exchange(session store error) = %v", err)
	}
	clientRepository.fail = ""
	response, err := service.Exchange(t.Context(), valid, " source ")
	if err != nil || response.User.ID != "user" || response.AccessToken == "" || len(repository.scopes) != 2 {
		t.Fatalf("Exchange() = %+v, scopes=%d, %v", response, len(repository.scopes), err)
	}
	if repository.digest != service.digest("pin", valid.PIN) || repository.failureLimit != ClientPINFailureLimit ||
		repository.blockTime != ClientPINBlockTime {
		t.Fatalf("Exchange PIN policy = digest:%x limit:%d block:%v", repository.digest, repository.failureLimit, repository.blockTime)
	}
	if repository.scopes[0].ResetOnSuccess || !repository.scopes[1].ResetOnSuccess {
		t.Fatalf("Exchange PIN reset policies = %+v", repository.scopes)
	}

	calls := 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 1 {
			binary.BigEndian.PutUint32(buffer, ^uint32(0))
		} else {
			binary.BigEndian.PutUint32(buffer, 42)
		}
		return len(buffer), nil
	}
	pin, err := service.pin()
	if err != nil || pin != "000042" || calls != 2 {
		t.Fatalf("pin(rejection sampling) = %q, calls=%d, %v", pin, calls, err)
	}
}

type pinRepositoryFake struct {
	users        []ClientPINUser
	user         ClientUser
	userID       string
	digest       [32]byte
	scopes       []PINAttemptScope
	failureLimit int
	blockTime    time.Duration
	conflicts    int
	err          error
}

func (repository *pinRepositoryFake) ListClientPINUsers(context.Context) ([]ClientPINUser, error) {
	return repository.users, repository.err
}

func (repository *pinRepositoryFake) CreateClientPINUser(
	_ context.Context,
	user ClientUser,
	digest [32]byte,
) error {
	repository.user, repository.digest = user, digest
	return repository.result()
}

func (repository *pinRepositoryFake) RotateClientPIN(
	_ context.Context,
	userID string,
	digest [32]byte,
	_ time.Time,
) (ClientUser, error) {
	repository.userID, repository.digest = userID, digest
	return repository.user, repository.result()
}

func (repository *pinRepositoryFake) DeleteClientPINUser(
	_ context.Context,
	userID string,
	_ time.Time,
) error {
	repository.userID = userID
	return repository.err
}

func (repository *pinRepositoryFake) ExchangeClientPIN(
	_ context.Context,
	digest [32]byte,
	scopes []PINAttemptScope,
	_ time.Time,
) (ClientUser, error) {
	repository.digest, repository.scopes = digest, scopes
	repository.failureLimit, repository.blockTime = scopes[0].FailureLimit, scopes[0].BlockTime
	return repository.user, repository.err
}

func (repository *pinRepositoryFake) result() error {
	if repository.conflicts > 0 {
		repository.conflicts--
		return ErrConflict
	}
	return repository.err
}
