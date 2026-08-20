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
		SET status = CASE WHEN attempt_count >= 3 OR starts_at <= $2 THEN 'failed' ELSE 'queued' END,
			last_installation_id = leased_installation_id, leased_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL,
			reason_code = CASE
				WHEN starts_at <= $2 THEN 'showtime_started'
				WHEN attempt_count >= 3 THEN 'lease_attempts_exhausted'
				ELSE 'lease_expired'
			END,
			completed_at = CASE WHEN attempt_count >= 3 OR starts_at <= $2 THEN $2 ELSE NULL END,
			updated_at = $2
		WHERE user_id = $1 AND status = 'leased' AND lease_expires_at <= $2
	`, claim.UserID, claim.Now); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("expire Client execution leases: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'failed', reason_code = 'showtime_started', completed_at = $2, updated_at = $2
		WHERE user_id = $1 AND status = 'queued' AND starts_at <= $2
	`, claim.UserID, claim.Now); err != nil {
		return central.ExecutionCommand{}, fmt.Errorf("expire stale Client execution commands: %w", err)
	}
	var command central.ExecutionCommand
	var payload []byte
	err = tx.QueryRow(ctx, `
		WITH active_targets AS (
			SELECT monitors.id AS monitor_id,
				presets.payload->>'auditoriumId' AS auditorium_id
			FROM client_resources AS monitors
			JOIN client_resources AS presets
				ON presets.user_id = monitors.user_id
				AND presets.kind = 'presets'
				AND presets.id = monitors.payload->>'presetId'
				AND presets.deleted_at IS NULL
			WHERE monitors.user_id = $1 AND monitors.kind = 'monitors'
				AND monitors.deleted_at IS NULL
				AND monitors.payload->>'status' IN ('pending', 'running')
		)
		SELECT command.id, command.user_id, command.monitor_id, command.payload,
			command.attempt_count, command.created_at
		FROM client_execution_commands AS command
		LEFT JOIN active_targets AS target ON target.monitor_id = command.monitor_id
		WHERE command.user_id = $1 AND command.status = 'queued'
			AND command.attempt_count < 3 AND command.starts_at > $3
		ORDER BY (
			CASE WHEN target.monitor_id IS NOT NULL
				AND target.auditorium_id = command.payload->'showtime'->'auditorium'->>'id'
				THEN 0 ELSE 1 END
		) ASC,
		command.created_at DESC,
		(command.last_installation_id = $2),
		command.id
		LIMIT 1 FOR UPDATE SKIP LOCKED
	`, claim.UserID, claim.InstallationID, claim.Now).Scan(
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
	var monitorID string
	err = tx.QueryRow(ctx, `
		SELECT status, lease_token_hash, lease_expires_at, attempt_count, monitor_id
		FROM client_execution_commands WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, completion.CommandID, completion.UserID).Scan(&status, &storedHash, &expiresAt, &attempts, &monitorID)
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
	if nextStatus == "queued" {
		identity := fmt.Sprintf("automatic-retry:%d:%s", attempts, completion.Now.Format(time.RFC3339Nano))
		if err := recordExecutionReadyEvent(
			ctx, tx, completion.UserID, completion.CommandID, monitorID,
			"automatic_retry", identity, completion.Now,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Client execution completion: %w", err)
	}
	return nil
}

func (store *Store) RetryClientExecution(
	ctx context.Context,
	userID string,
	commandID string,
	now time.Time,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Client execution retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var monitorID string
	err = tx.QueryRow(ctx, `
		UPDATE client_execution_commands SET
			status = 'queued', leased_installation_id = NULL, last_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL, attempt_count = 0,
			reason_code = '', completed_at = NULL, updated_at = $3
		WHERE id = $1 AND user_id = $2 AND status = 'failed'
		RETURNING monitor_id
	`, commandID, userID, now).Scan(&monitorID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("retry Client execution command: %w", err)
	}
	if err == nil {
		identity := "explicit-retry:" + now.Format(time.RFC3339Nano)
		if err := recordExecutionReadyEvent(
			ctx, tx, userID, commandID, monitorID, "explicit_retry", identity, now,
		); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit Client execution retry: %w", err)
		}
		return nil
	}
	var status string
	err = tx.QueryRow(ctx, `
		SELECT status FROM client_execution_commands WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, commandID, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read Client execution retry state: %w", err)
	}
	if status == "queued" {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit idempotent Client execution retry: %w", err)
		}
		return nil
	}
	return central.ErrConflict
}
