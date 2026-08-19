package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

const DefaultExecutionLeaseTTL = 90 * time.Second

type ExecutionCommand struct {
	ID             string           `json:"id"`
	UserID         string           `json:"-"`
	MonitorID      string           `json:"monitorId"`
	InstallationID string           `json:"installationId"`
	Attempt        int              `json:"attempt"`
	Payload        ExecutionPayload `json:"payload"`
	LeaseToken     string           `json:"leaseToken"`
	LeaseExpiresAt time.Time        `json:"leaseExpiresAt"`
	CreatedAt      time.Time        `json:"createdAt"`
}

type ExecutionPayload = contracts.ExecutionPayload

type ExecutionClaimRequest = contracts.ExecutionClaimRequest

type ExecutionHeartbeatRequest = contracts.ExecutionHeartbeatRequest

type ExecutionHeartbeatResponse = contracts.ExecutionHeartbeatResponse

type ExecutionResultRequest = contracts.ExecutionResultRequest

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
	request ExecutionClaimRequest,
) (*ExecutionCommand, error) {
	request.InstallationID = strings.TrimSpace(request.InstallationID)
	if request.InstallationID == "" {
		return nil, ErrInvalid
	}
	if _, err := service.repository.GetClientDevice(ctx, principal.UserID, request.InstallationID); err != nil {
		return nil, ErrUnauthorized
	}
	token, leaseHash, err := service.secret("exl_")
	if err != nil {
		return nil, err
	}
	now := service.clock().UTC()
	command, err := service.repository.ClaimClientExecution(ctx, ExecutionClaim{
		UserID: principal.UserID, InstallationID: request.InstallationID,
		LeaseHash: leaseHash, LeaseExpiresAt: now.Add(DefaultExecutionLeaseTTL), Now: now,
	})
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	command.LeaseToken = token
	return &command, nil
}

func (service *ClientService) HeartbeatExecution(
	ctx context.Context,
	principal ClientPrincipal,
	commandID string,
	request ExecutionHeartbeatRequest,
) (ExecutionHeartbeatResponse, error) {
	commandID = strings.TrimSpace(commandID)
	request.LeaseToken = strings.TrimSpace(request.LeaseToken)
	if commandID == "" || request.LeaseToken == "" {
		return ExecutionHeartbeatResponse{}, ErrInvalid
	}
	now := service.clock().UTC()
	expiresAt := now.Add(DefaultExecutionLeaseTTL)
	if err := service.repository.HeartbeatClientExecution(
		ctx, principal.UserID, commandID, sha256.Sum256([]byte(request.LeaseToken)), now, expiresAt,
	); err != nil {
		return ExecutionHeartbeatResponse{}, err
	}
	return ExecutionHeartbeatResponse{LeaseExpiresAt: expiresAt}, nil
}

func (service *ClientService) CompleteExecution(
	ctx context.Context,
	principal ClientPrincipal,
	commandID string,
	request ExecutionResultRequest,
) error {
	commandID = strings.TrimSpace(commandID)
	request.LeaseToken = strings.TrimSpace(request.LeaseToken)
	request.Status = strings.TrimSpace(request.Status)
	request.ReasonCode = strings.TrimSpace(request.ReasonCode)
	if commandID == "" || request.LeaseToken == "" ||
		(request.Status != "completed" && request.Status != "failed") {
		return ErrInvalid
	}
	return service.repository.CompleteClientExecution(ctx, ExecutionCompletion{
		UserID: principal.UserID, CommandID: commandID,
		LeaseHash: sha256.Sum256([]byte(request.LeaseToken)), Status: request.Status,
		ReasonCode: request.ReasonCode, Now: service.clock().UTC(),
	})
}
