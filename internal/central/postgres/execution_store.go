package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	executionReasonPreferredSeatsUnavailable = "preferred_seats_unavailable"
	executionReasonShowtimeUnavailable       = "showtime_unavailable"
)

func (store *Store) ClaimClientExecution(
	ctx context.Context,
	claim central.ExecutionClaim,
) (*executionpb.Command, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin Client execution claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := expireClientExecutionLeases(ctx, tx, claim.UserID, claim.Now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'failed', reason_code = 'showtime_started', completed_at = $2, updated_at = $2
		WHERE user_id = $1 AND status = 'queued' AND starts_at <= $2
	`, claim.UserID, claim.Now); err != nil {
		return nil, fmt.Errorf("expire stale Client execution commands: %w", err)
	}
	command := &executionpb.Command{}
	var commandID, monitorID string
	var attempt int32
	var createdAt time.Time
	var payload []byte
	err = tx.QueryRow(ctx, `
		WITH active_targets AS (
			SELECT monitors.id AS monitor_id, presets.auditorium_id
			FROM client_monitors AS monitors
			JOIN client_resources AS monitor_resource
				ON monitor_resource.user_id = monitors.user_id
				AND monitor_resource.kind = 'monitors'
				AND monitor_resource.id = monitors.id
				AND monitor_resource.deleted_at IS NULL
			JOIN client_presets AS presets
				ON presets.user_id = monitors.user_id
				AND presets.resource_kind = 'presets'
				AND presets.id = monitors.preset_id
			JOIN client_resources AS preset_resource
				ON preset_resource.user_id = presets.user_id
				AND preset_resource.kind = 'presets'
				AND preset_resource.id = presets.id
				AND preset_resource.deleted_at IS NULL
			WHERE monitors.user_id = $1
				AND monitors.resource_kind = 'monitors'
				AND monitors.state IN ('pending', 'running')
		)
		SELECT command.id, command.monitor_id, command.payload,
			command.attempt_count, command.created_at
		FROM client_execution_commands AS command
		JOIN active_targets AS target ON target.monitor_id = command.monitor_id
		-- The command payload is the immutable showtime snapshot. Catalog rows may
		-- be retired after enqueueing, but that must not strand the command.
		LEFT JOIN showtimes AS showtime ON showtime.id = command.showtime_id
		WHERE command.user_id = $1 AND command.status = 'queued'
			AND command.attempt_count < 3 AND command.starts_at > $3
		ORDER BY (
			CASE WHEN target.monitor_id IS NOT NULL
				AND target.auditorium_id = showtime.auditorium_id
				THEN 0 ELSE 1 END
			) ASC,
			command.created_at DESC,
			(command.last_installation_id = $2),
			command.id
		LIMIT 1 FOR UPDATE OF command SKIP LOCKED
		`, claim.UserID, claim.InstallationID, claim.Now).Scan(
		&commandID, &monitorID, &payload, &attempt, &createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lease and stale-command expiry run in this transaction before the
		// selection. Preserve those terminal state transitions even when they
		// leave no command for the caller to claim.
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("commit empty Client execution claim: %w", commitErr)
		}
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock Client execution command: %w", err)
	}
	payloadMessage := &executionpb.Payload{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, payloadMessage); err != nil {
		return nil, fmt.Errorf("decode Client execution command: %w", err)
	}
	command.SetId(commandID)
	command.SetMonitorId(monitorID)
	command.SetInstallationId(claim.InstallationID)
	command.SetAttempt(attempt + 1)
	command.SetPayload(payloadMessage)
	command.SetLeaseExpiresAt(timestamppb.New(claim.LeaseExpiresAt))
	command.SetCreatedAt(timestamppb.New(createdAt))
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'leased', leased_installation_id = $2, lease_token_hash = $3,
			lease_expires_at = $4, attempt_count = attempt_count + 1, reason_code = '', updated_at = $5
		WHERE id = $1
	`, commandID, claim.InstallationID, claim.LeaseHash[:], claim.LeaseExpiresAt, claim.Now); err != nil {
		return nil, fmt.Errorf("lease Client execution command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Client execution claim: %w", err)
	}
	return command, nil
}

// expireClientExecutionLeases closes an ambiguous execution instead of silently
// assigning it again. The previous Client may already have selected seats or
// reached payment before losing its lease, so only an explicit user retry is safe.
func expireClientExecutionLeases(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		UPDATE client_execution_commands
		SET status = 'failed',
			last_installation_id = leased_installation_id, leased_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL,
			reason_code = 'execution_lease_lost', completed_at = $2, updated_at = $2
		WHERE user_id = $1 AND status = 'leased' AND lease_expires_at <= $2
		RETURNING id, monitor_id
	`, userID, now)
	if err != nil {
		return fmt.Errorf("expire Client execution leases: %w", err)
	}
	type expiredLease struct {
		commandID string
		monitorID string
	}
	expired := make([]expiredLease, 0)
	for rows.Next() {
		var lease expiredLease
		if err := rows.Scan(&lease.commandID, &lease.monitorID); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired Client execution lease: %w", err)
		}
		expired = append(expired, lease)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired Client execution leases: %w", err)
	}
	rows.Close()
	for _, lease := range expired {
		completion := central.ExecutionCompletion{
			UserID: userID, CommandID: lease.commandID,
			Status: "failed", ReasonCode: "execution_lease_lost", Now: now,
		}
		if err := finishExecutionMonitor(ctx, tx, completion, lease.monitorID); err != nil {
			return fmt.Errorf("finish expired Client execution monitor: %w", err)
		}
	}
	return nil
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
	locked, err := lockClientExecutionCompletion(ctx, tx, completion)
	if err != nil {
		return err
	}
	if !locked.matchesLease(completion) {
		return central.ErrLeaseExpired
	}
	nextStatus, completedAt := executionCompletionState(completion, locked.attempts)
	if err := updateClientExecutionCompletion(ctx, tx, completion, nextStatus, completedAt); err != nil {
		return err
	}
	if nextStatus == "failed" && executionWaitsForAvailability(completion.ReasonCode) {
		if err := markExecutionAvailabilityUnavailable(
			ctx, tx, completion.UserID, locked.monitorID, locked.showtimeID, completion.Now,
		); err != nil {
			return err
		}
	}
	if nextStatus == "failed" && !executionWaitsForAvailability(completion.ReasonCode) {
		if err := finishExecutionMonitor(ctx, tx, completion, locked.monitorID); err != nil {
			return err
		}
	}
	if nextStatus == "queued" {
		identity := fmt.Sprintf("automatic-retry:%d:%s", locked.attempts, completion.Now.Format(time.RFC3339Nano))
		if err := recordExecutionReadyEvent(
			ctx, tx, completion.UserID, completion.CommandID, locked.monitorID,
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

type lockedExecutionCompletion struct {
	status     string
	storedHash []byte
	expiresAt  *time.Time
	attempts   int
	monitorID  string
	showtimeID string
}

func lockClientExecutionCompletion(
	ctx context.Context,
	tx pgx.Tx,
	completion central.ExecutionCompletion,
) (lockedExecutionCompletion, error) {
	var locked lockedExecutionCompletion
	err := tx.QueryRow(ctx, `
		SELECT status, lease_token_hash, lease_expires_at, attempt_count, monitor_id, showtime_id
		FROM client_execution_commands WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, completion.CommandID, completion.UserID).Scan(
		&locked.status,
		&locked.storedHash,
		&locked.expiresAt,
		&locked.attempts,
		&locked.monitorID,
		&locked.showtimeID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedExecutionCompletion{}, central.ErrNotFound
	}
	if err != nil {
		return lockedExecutionCompletion{}, fmt.Errorf("lock Client execution completion: %w", err)
	}
	return locked, nil
}

// matchesLease verifies ownership and expiry without exposing the stored lease token.
func (locked lockedExecutionCompletion) matchesLease(completion central.ExecutionCompletion) bool {
	return locked.status == "leased" &&
		locked.expiresAt != nil && locked.expiresAt.After(completion.Now) &&
		len(locked.storedHash) == len(completion.LeaseHash) &&
		subtle.ConstantTimeCompare(locked.storedHash, completion.LeaseHash[:]) == 1
}

func executionCompletionState(completion central.ExecutionCompletion, attempts int) (string, any) {
	nextStatus := completion.Status
	completedAt := any(completion.Now)
	if nextStatus == "retry_requested" {
		if attempts < 3 {
			nextStatus = "queued"
			completedAt = nil
		} else {
			nextStatus = "failed"
		}
	}
	return nextStatus, completedAt
}

// finishExecutionMonitor publishes the terminal execution outcome through the
// same normalized resource and event stream consumed by every Client device.
func finishExecutionMonitor(
	ctx context.Context,
	tx pgx.Tx,
	completion central.ExecutionCompletion,
	monitorID string,
) error {
	resource, deleted, err := lockClientResource(ctx, tx, completion.UserID, "monitors", monitorID)
	if errors.Is(err, pgx.ErrNoRows) || deleted {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock execution monitor: %w", err)
	}
	monitor := resource.body.GetMonitor()
	if monitor == nil {
		return errors.New("execution monitor resource is missing its monitor body")
	}
	if !monitor.GetState().HasPending() && !monitor.GetState().HasRunning() {
		return nil
	}
	state := monitor.GetState()
	if executionOutcomeIsUnknown(completion.ReasonCode) {
		state.SetPaymentUnknown(&clientpb.MonitorPaymentUnknown{})
	} else {
		monitorFailure := &clientpb.MonitorFailed{}
		monitorFailure.SetReason(completion.ReasonCode)
		state.SetFailed(monitorFailure)
	}
	monitor.SetUpdatedAt(timestamppb.New(completion.Now))
	resource.revision++
	resource.updatedAt = completion.Now
	if err := writeClientResource(ctx, tx, resource, false, false); err != nil {
		return err
	}
	mutation := central.ResourceMutation{
		UserID: completion.UserID, Kind: "monitors", ID: monitorID,
		Resource: resource.body,
		CommandID: fmt.Sprintf(
			"execution-failure:%s:%d",
			completion.CommandID,
			resource.revision,
		),
		Now: completion.Now,
	}
	if err := recordClientMutation(ctx, tx, mutation, clientMutationOperation(false), "monitors.updated", resource); err != nil {
		return fmt.Errorf("publish terminal execution monitor: %w", err)
	}
	return nil
}

func executionOutcomeIsUnknown(reasonCode string) bool {
	return reasonCode == "execution_lease_lost" || reasonCode == "client_interrupted"
}

func updateClientExecutionCompletion(
	ctx context.Context,
	tx pgx.Tx,
	completion central.ExecutionCompletion,
	nextStatus string,
	completedAt any,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = $2, last_installation_id = leased_installation_id, leased_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL, reason_code = $3,
			completed_at = $4, updated_at = $5
		WHERE id = $1
	`, completion.CommandID, nextStatus, completion.ReasonCode, completedAt, completion.Now); err != nil {
		return fmt.Errorf("complete Client execution command: %w", err)
	}
	return nil
}

// executionWaitsForAvailability identifies failures that require fresh CGV
// availability evidence before the Client opens another browser.
func executionWaitsForAvailability(reasonCode string) bool {
	return reasonCode == executionReasonPreferredSeatsUnavailable ||
		reasonCode == executionReasonShowtimeUnavailable
}
