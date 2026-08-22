package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	executionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/execution"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const DefaultExecutionLeaseTTL = 90 * time.Second

type ExecutionClaim struct {
	UserID         string
	InstallationID string
	LeaseHash      [32]byte
	LeaseExpiresAt time.Time
	Now            time.Time
}

type ExecutionCompletion struct {
	UserID     string
	CommandID  string
	LeaseHash  [32]byte
	Status     string
	ReasonCode string
	Now        time.Time
}

func (service *ClientService) ClaimExecution(
	ctx context.Context,
	principal ClientPrincipal,
	request *executionpb.ClaimRequest,
) (*executionpb.ClaimResponse, error) {
	installationID := strings.TrimSpace(request.GetInstallationId())
	if installationID == "" {
		return nil, ErrInvalid
	}
	if _, err := service.repository.GetClientDevice(ctx, principal.UserID, installationID); err != nil {
		return nil, ErrUnauthorized
	}
	token, leaseHash, err := service.secret("exl_")
	if err != nil {
		return nil, err
	}
	now := service.clock().UTC()
	command, err := service.repository.ClaimClientExecution(ctx, ExecutionClaim{
		UserID: principal.UserID, InstallationID: installationID,
		LeaseHash: leaseHash, LeaseExpiresAt: now.Add(DefaultExecutionLeaseTTL), Now: now,
	})
	if errors.Is(err, ErrNotFound) {
		response := &executionpb.ClaimResponse{}
		response.SetNoCommand(&executionpb.NoCommand{})
		return response, nil
	}
	if err != nil {
		return nil, err
	}
	command.SetLeaseToken(token)
	response := &executionpb.ClaimResponse{}
	response.SetCommand(command)
	return response, nil
}

func (service *ClientService) HeartbeatExecution(
	ctx context.Context,
	principal ClientPrincipal,
	request *executionpb.HeartbeatRequest,
) (*executionpb.HeartbeatResponse, error) {
	commandID := strings.TrimSpace(request.GetCommandId())
	leaseToken := strings.TrimSpace(request.GetLeaseToken())
	if commandID == "" || leaseToken == "" {
		return nil, ErrInvalid
	}
	now := service.clock().UTC()
	expiresAt := now.Add(DefaultExecutionLeaseTTL)
	if err := service.repository.HeartbeatClientExecution(
		ctx, principal.UserID, commandID, sha256.Sum256([]byte(leaseToken)), now, expiresAt,
	); err != nil {
		return nil, err
	}
	response := &executionpb.HeartbeatResponse{}
	response.SetLeaseExpiresAt(timestamppb.New(expiresAt))
	return response, nil
}

func (service *ClientService) CompleteExecution(
	ctx context.Context,
	principal ClientPrincipal,
	request *executionpb.ResultRequest,
) error {
	commandID := strings.TrimSpace(request.GetCommandId())
	leaseToken := strings.TrimSpace(request.GetLeaseToken())
	status, reasonCode := executionResult(request)
	if commandID == "" || leaseToken == "" || status == "" {
		return ErrInvalid
	}
	return service.repository.CompleteClientExecution(ctx, ExecutionCompletion{
		UserID: principal.UserID, CommandID: commandID,
		LeaseHash: sha256.Sum256([]byte(leaseToken)), Status: status,
		ReasonCode: reasonCode, Now: service.clock().UTC(),
	})
}

func executionResult(request *executionpb.ResultRequest) (string, string) {
	switch {
	case request == nil:
		return "", ""
	case request.GetCompleted() != nil:
		return "completed", ""
	case request.GetFailed() != nil:
		return "failed", strings.TrimSpace(request.GetFailed().GetReasonCode())
	case request.GetRetryRequested() != nil:
		return "retry_requested", strings.TrimSpace(request.GetRetryRequested().GetReasonCode())
	default:
		return "", ""
	}
}
