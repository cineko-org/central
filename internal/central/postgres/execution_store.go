package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
)

func (store *Store) ClaimClientExecution(
	ctx context.Context,
	claim central.ExecutionClaim,
) (central.ExecutionCommand, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("begin Client execution claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = CASE WHEN attempt_count >= 3 THEN 'failed' ELSE 'queued' END,
			last_installation_id = leased_installation_id, leased_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL,
			reason_code = CASE WHEN attempt_count >= 3 THEN 'lease_attempts_exhausted' ELSE 'lease_expired' END,
			completed_at = CASE WHEN attempt_count >= 3 THEN $2 ELSE NULL END, updated_at = $2
		WHERE user_id = $1 AND status = 'leased' AND lease_expires_at <= $2
	`, claim.UserID, claim.Now); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("expire Client execution leases: %w", err)
	}
	var command central.ExecutionCommand
	var payload []byte
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, monitor_id, payload, attempt_count, created_at
		FROM client_execution_commands
		WHERE user_id = $1 AND status = 'queued' AND attempt_count < 3
		ORDER BY (last_installation_id = $2), created_at, id
		LIMIT 1 FOR UPDATE SKIP LOCKED
	`, claim.UserID, claim.InstallationID).Scan(
		&command.ID, &command.UserID, &command.MonitorID, &payload, &command.Attempt, &command.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ExecutionCommand{}, central.ErrNotFound
	}
	if err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("lock Client execution command: %w", err)
	}
	if err := json.Unmarshal(payload, &command.Payload); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("decode Client execution command: %w", err)
	}
	command.Attempt++
	command.InstallationID = claim.InstallationID
	command.LeaseExpiresAt = claim.LeaseExpiresAt
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'leased', leased_installation_id = $2, lease_token_hash = $3,
			lease_expires_at = $4, attempt_count = attempt_count + 1, reason_code = '', updated_at = $5
		WHERE id = $1
	`, command.ID, claim.InstallationID, claim.LeaseHash[:], claim.LeaseExpiresAt, claim.Now); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("lease Client execution command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("commit Client execution claim: %w", err)
	}
	return command, nil
}

func (store *Store) HeartbeatClientExecution(
	ctx context.Context,
	userID string,
	commandID string,
	leaseHash [32]byte,
	now time.Time,
	expiresAt time.Time,
) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE client_execution_commands SET lease_expires_at = $5, updated_at = $4
		WHERE id = $1 AND user_id = $2 AND status = 'leased'
			AND lease_token_hash = $3 AND lease_expires_at > $4
	`, commandID, userID, leaseHash[:], now, expiresAt)
	if err != nil {
		return fmt.Errorf("heartbeat Client execution command: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrLeaseExpired
	}
	return nil
}

func (store *Store) CompleteClientExecution(ctx context.Context, completion central.ExecutionCompletion) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Client execution completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storedHash []byte
	var status string
	var expiresAt *time.Time
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT status, lease_token_hash, lease_expires_at, attempt_count
		FROM client_execution_commands WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, completion.CommandID, completion.UserID).Scan(&status, &storedHash, &expiresAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock Client execution completion: %w", err)
	}
	if status != "leased" || expiresAt == nil || !expiresAt.After(completion.Now) ||
		len(storedHash) != len(completion.LeaseHash) ||
		subtle.ConstantTimeCompare(storedHash, completion.LeaseHash[:]) != 1 {
		return central.ErrLeaseExpired
	}
	nextStatus := completion.Status
	completedAt := any(completion.Now)
	if completion.Status == "failed" && attempts < 3 {
		nextStatus = "queued"
		completedAt = nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = $2, last_installation_id = leased_installation_id, leased_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL, reason_code = $3,
			completed_at = $4, updated_at = $5
		WHERE id = $1
	`, completion.CommandID, nextStatus, completion.ReasonCode, completedAt, completion.Now); err != nil {
		return fmt.Errorf("complete Client execution command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Client execution completion: %w", err)
	}
	return nil
}
