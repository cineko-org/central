package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestClientExecutionLifecycle(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	repository.device = ClientDevice{InstallationID: "install", UserID: "user", DeviceID: "device"}
	principal := ClientPrincipal{UserID: "user"}
	ctx := context.Background()
	if command, err := service.ClaimExecution(ctx, principal, ExecutionClaimRequest{}); !errors.Is(err, ErrInvalid) || command != nil {
		t.Fatalf("ClaimExecution(invalid) = %+v, %v", command, err)
	}
	repository.fail = "device"
	if command, err := service.ClaimExecution(ctx, principal, ExecutionClaimRequest{InstallationID: "install"}); !errors.Is(err, ErrUnauthorized) || command != nil {
		t.Fatalf("ClaimExecution(device error) = %+v, %v", command, err)
	}
	repository.fail = ""
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.ClaimExecution(ctx, principal, ExecutionClaimRequest{InstallationID: "install"}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ClaimExecution(random error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "execution-not-found"
	command, err := service.ClaimExecution(ctx, principal, ExecutionClaimRequest{InstallationID: " install "})
	if err != nil || command != nil {
		t.Fatalf("ClaimExecution(empty) = %+v, %v", command, err)
	}
	repository.fail = "execution"
	if _, err := service.ClaimExecution(ctx, principal, ExecutionClaimRequest{InstallationID: "install"}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ClaimExecution(repository error) = %v", err)
	}
	repository.fail = ""
	command, err = service.ClaimExecution(ctx, principal, ExecutionClaimRequest{InstallationID: "install"})
	if err != nil || command == nil || command.LeaseToken == "" ||
		!command.LeaseExpiresAt.Equal(clientTestTime.Add(DefaultExecutionLeaseTTL)) {
		t.Fatalf("ClaimExecution() = %+v, %v", command, err)
	}

	if _, err := service.HeartbeatExecution(ctx, principal, "", ExecutionHeartbeatRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("HeartbeatExecution(invalid) = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.HeartbeatExecution(ctx, principal, command.ID, ExecutionHeartbeatRequest{
		LeaseToken: command.LeaseToken,
	}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("HeartbeatExecution(repository error) = %v", err)
	}
	repository.err = nil
	heartbeat, err := service.HeartbeatExecution(ctx, principal, " execution ", ExecutionHeartbeatRequest{
		LeaseToken: " lease ",
	})
	if err != nil || !heartbeat.LeaseExpiresAt.Equal(clientTestTime.Add(DefaultExecutionLeaseTTL)) ||
		repository.hash != sha256.Sum256([]byte("lease")) {
		t.Fatalf("HeartbeatExecution() = %+v, %v", heartbeat, err)
	}

	for _, input := range []ExecutionResultRequest{{}, {LeaseToken: "lease", Status: "unknown"}} {
		if err := service.CompleteExecution(ctx, principal, "execution", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CompleteExecution(invalid) = %v", err)
		}
	}
	repository.err = errInjectedClient
	if err := service.CompleteExecution(ctx, principal, command.ID, ExecutionResultRequest{
		LeaseToken: "lease", Status: "failed", ReasonCode: " retry ",
	}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CompleteExecution(repository error) = %v", err)
	}
	repository.err = nil
	if err := service.CompleteExecution(ctx, principal, command.ID, ExecutionResultRequest{
		LeaseToken: " lease ", Status: " completed ",
	}); err != nil {
		t.Fatal(err)
	}
	if repository.hash != sha256.Sum256([]byte("lease")) {
		t.Fatalf("completion lease hash = %x", repository.hash)
	}
	if err := service.RetryExecution(ctx, principal, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RetryExecution(invalid) = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.RetryExecution(ctx, principal, " execution "); !errors.Is(err, errInjectedClient) {
		t.Fatalf("RetryExecution(repository error) = %v", err)
	}
	repository.err = nil
	if err := service.RetryExecution(ctx, principal, " execution "); err != nil {
		t.Fatalf("RetryExecution() = %v", err)
	}
}

func TestExecutionDefaults(t *testing.T) {
	if DefaultExecutionLeaseTTL != 90*time.Second {
		t.Fatalf("execution lease TTL = %v", DefaultExecutionLeaseTTL)
	}
}
