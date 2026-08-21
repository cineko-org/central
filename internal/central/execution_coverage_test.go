package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
)

func TestClientExecutionLifecycle(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	repository.device = &clientpb.Device{}
	repository.device.SetInstallationId("install")
	repository.device.SetUserId("user")
	repository.device.SetDeviceId("device")
	principal := ClientPrincipal{UserID: "user"}
	ctx := context.Background()
	if response, err := service.ClaimExecution(ctx, principal, &executionpb.ClaimRequest{}); !errors.Is(err, ErrInvalid) || response != nil {
		t.Fatalf("ClaimExecution(invalid) = %+v, %v", response, err)
	}
	claim := &executionpb.ClaimRequest{}
	claim.SetInstallationId("install")
	repository.fail = "device"
	if response, err := service.ClaimExecution(ctx, principal, claim); !errors.Is(err, ErrUnauthorized) || response != nil {
		t.Fatalf("ClaimExecution(device error) = %+v, %v", response, err)
	}
	repository.fail = ""
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.ClaimExecution(ctx, principal, claim); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ClaimExecution(random error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "execution-not-found"
	claim.SetInstallationId(" install ")
	response, err := service.ClaimExecution(ctx, principal, claim)
	if err != nil || response.GetNoCommand() == nil {
		t.Fatalf("ClaimExecution(empty) = %+v, %v", response, err)
	}
	repository.fail = "execution"
	claim.SetInstallationId("install")
	if _, err := service.ClaimExecution(ctx, principal, claim); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ClaimExecution(repository error) = %v", err)
	}
	repository.fail = ""
	response, err = service.ClaimExecution(ctx, principal, claim)
	command := response.GetCommand()
	if err != nil || command == nil || command.GetLeaseToken() == "" ||
		!command.GetLeaseExpiresAt().AsTime().Equal(clientTestTime.Add(DefaultExecutionLeaseTTL)) {
		t.Fatalf("ClaimExecution() = %+v, %v", response, err)
	}

	if response, err := service.HeartbeatExecution(ctx, principal, &executionpb.HeartbeatRequest{}); !errors.Is(err, ErrInvalid) || response != nil {
		t.Fatalf("HeartbeatExecution(invalid) = %+v, %v", response, err)
	}
	heartbeatRequest := &executionpb.HeartbeatRequest{}
	heartbeatRequest.SetCommandId(command.GetId())
	heartbeatRequest.SetLeaseToken(command.GetLeaseToken())
	repository.err = errInjectedClient
	if _, err := service.HeartbeatExecution(ctx, principal, heartbeatRequest); !errors.Is(err, errInjectedClient) {
		t.Fatalf("HeartbeatExecution(repository error) = %v", err)
	}
	repository.err = nil
	heartbeatRequest.SetCommandId(" execution ")
	heartbeatRequest.SetLeaseToken(" lease ")
	heartbeat, err := service.HeartbeatExecution(ctx, principal, heartbeatRequest)
	if err != nil || !heartbeat.GetLeaseExpiresAt().AsTime().Equal(clientTestTime.Add(DefaultExecutionLeaseTTL)) ||
		repository.hash != sha256.Sum256([]byte("lease")) {
		t.Fatalf("HeartbeatExecution() = %+v, %v", heartbeat, err)
	}

	invalidResult := &executionpb.ResultRequest{}
	invalidResult.SetCommandId("execution")
	invalidResult.SetLeaseToken("lease")
	if err := service.CompleteExecution(ctx, principal, invalidResult); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CompleteExecution(invalid) = %v", err)
	}
	failed := &executionpb.Failed{}
	failed.SetReasonCode(" retry ")
	result := &executionpb.ResultRequest{}
	result.SetCommandId(command.GetId())
	result.SetLeaseToken("lease")
	result.SetFailed(failed)
	repository.err = errInjectedClient
	if err := service.CompleteExecution(ctx, principal, result); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CompleteExecution(repository error) = %v", err)
	}
	repository.err = nil
	result.SetCommandId(" " + command.GetId() + " ")
	result.SetLeaseToken(" lease ")
	result.SetCompleted(&executionpb.Completed{})
	if err := service.CompleteExecution(ctx, principal, result); err != nil {
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
