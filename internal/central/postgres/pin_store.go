package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (store *Store) ListClientPINUsers(ctx context.Context) ([]central.ClientPINUser, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at,
			(pins.user_id IS NOT NULL AND pins.revoked_at IS NULL) AS pin_active,
			COUNT(devices.installation_id)
		FROM client_users AS users
		LEFT JOIN client_pins AS pins ON pins.user_id = users.id
		LEFT JOIN client_devices AS devices ON devices.user_id = users.id
		GROUP BY users.id, pins.user_id, pins.revoked_at
		ORDER BY users.created_at, users.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list Client PIN users: %w", err)
	}
	defer rows.Close()
	users := make([]central.ClientPINUser, 0)
	for rows.Next() {
		var item central.ClientPINUser
		if err := rows.Scan(
			&item.User.ID, &item.User.DisplayName, &item.User.CreatedAt, &item.User.UpdatedAt,
			&item.PINActive, &item.DeviceCount,
		); err != nil {
			return nil, fmt.Errorf("scan Client PIN user: %w", err)
		}
		users = append(users, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client PIN users: %w", err)
	}
	return users, nil
}

func (store *Store) CreateClientPINUser(
	ctx context.Context,
	user central.ClientUser,
	digest [32]byte,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Client PIN user creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
	`, user.ID, user.DisplayName, user.CreatedAt); err != nil {
		return pinWriteError("create Client PIN user", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_pins (user_id, pin_digest, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
	`, user.ID, digest[:], user.CreatedAt); err != nil {
		return pinWriteError("create Client PIN", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return pinWriteError("commit Client PIN user creation", err)
	}
	return nil
}

func (store *Store) RotateClientPIN(
	ctx context.Context,
	userID string,
	digest [32]byte,
	now time.Time,
) (central.ClientUser, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("begin Client PIN rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var user central.ClientUser
	if err := tx.QueryRow(ctx, `
		SELECT id, display_name, created_at, updated_at FROM client_users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&user.ID, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return central.ClientUser{}, central.ErrNotFound
	} else if err != nil {
		return central.ClientUser{}, fmt.Errorf("lock Client PIN user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_pins (user_id, pin_digest, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			pin_digest = EXCLUDED.pin_digest, revoked_at = NULL, updated_at = EXCLUDED.updated_at
	`, userID, digest[:], now); err != nil {
		return central.ClientUser{}, pinWriteError("rotate Client PIN", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE client_users SET updated_at = $2 WHERE id = $1`, userID, now); err != nil {
		return central.ClientUser{}, fmt.Errorf("update Client PIN user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ClientUser{}, pinWriteError("commit Client PIN rotation", err)
	}
	user.UpdatedAt = now
	return user, nil
}

func (store *Store) DeleteClientPINUser(ctx context.Context, userID string, now time.Time) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Client user deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedUserID string
	if err := tx.QueryRow(ctx, `
		SELECT id FROM client_users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&lockedUserID); errors.Is(err, pgx.ErrNoRows) {
		return central.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock Client user for deletion: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE assignment_attempts AS attempt
		SET status = 'expired', finished_at = $2, error_code = 'probe_owner_removed'
		FROM observation_assignments AS assignment
		JOIN probe_runtimes AS probe ON probe.id = assignment.probe_id
		WHERE attempt.assignment_id = assignment.id
			AND attempt.probe_id = probe.id
			AND attempt.status = 'leased'
			AND assignment.status = 'leased'
			AND probe.owner_user_id = $1
	`, userID, now); err != nil {
		return fmt.Errorf("expire deleted Client user Probe attempts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE observation_assignments AS assignment
		SET status = 'queued', probe_id = NULL, lease_token_hash = NULL, lease_expires_at = NULL,
			started_at = NULL, updated_at = $2
		FROM probe_runtimes AS probe
		WHERE assignment.probe_id = probe.id
			AND assignment.status = 'leased'
			AND probe.owner_user_id = $1
	`, userID, now); err != nil {
		return fmt.Errorf("requeue deleted Client user Probe assignments: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM assignment_eligible_probes
		WHERE probe_id IN (SELECT id FROM probe_runtimes WHERE owner_user_id = $1)
	`, userID); err != nil {
		return fmt.Errorf("delete Client user Probe eligibility: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM probe_runtimes WHERE owner_user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete Client user Probes: %w", err)
	}

	for _, statement := range []string{
		`DELETE FROM client_execution_commands WHERE user_id = $1`,
		`DELETE FROM client_events WHERE user_id = $1`,
		`DELETE FROM client_commands WHERE user_id = $1`,
		`DELETE FROM client_resources WHERE user_id = $1`,
		`DELETE FROM client_launch_tickets WHERE user_id = $1`,
		`DELETE FROM client_devices WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, statement, userID); err != nil {
			return fmt.Errorf("delete Client user data: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM client_users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete Client user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Client user deletion: %w", err)
	}
	return nil
}

func (store *Store) ExchangeClientPIN(
	ctx context.Context,
	digest [32]byte,
	scopes []central.PINAttemptScope,
	now time.Time,
) (central.ClientUser, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("begin Client PIN exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_pin_attempts
		WHERE updated_at < $1 AND (blocked_until IS NULL OR blocked_until < $2)
	`, now.Add(-24*time.Hour), now); err != nil {
		return central.ClientUser{}, fmt.Errorf("clean Client PIN attempts: %w", err)
	}
	sort.Slice(scopes, func(left, right int) bool {
		return bytes.Compare(scopes[left].Hash[:], scopes[right].Hash[:]) < 0
	})
	if err := requireAvailablePINScopes(ctx, tx, scopes, now); err != nil {
		return central.ClientUser{}, err
	}
	user, err := clientUserByPIN(ctx, tx, digest)
	if err == nil {
		return commitSuccessfulPINExchange(ctx, tx, scopes, user)
	}
	if !errors.Is(err, central.ErrUnauthorized) {
		return central.ClientUser{}, err
	}
	return central.ClientUser{}, commitFailedPINExchange(ctx, tx, scopes, now)
}

func requireAvailablePINScopes(
	ctx context.Context,
	tx pgx.Tx,
	scopes []central.PINAttemptScope,
	now time.Time,
) error {
	for _, scope := range scopes {
		blocked, err := lockPINAttempt(ctx, tx, scope.Hash, now)
		if err != nil {
			return err
		}
		if blocked {
			return central.ErrRateLimited
		}
	}
	return nil
}

func commitSuccessfulPINExchange(
	ctx context.Context,
	tx pgx.Tx,
	scopes []central.PINAttemptScope,
	user central.ClientUser,
) (central.ClientUser, error) {
	for _, scope := range scopes {
		if !scope.ResetOnSuccess {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM client_pin_attempts WHERE scope_hash = $1`, scope.Hash[:]); err != nil {
			return central.ClientUser{}, fmt.Errorf("reset Client PIN attempts: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ClientUser{}, fmt.Errorf("commit Client PIN exchange: %w", err)
	}
	return user, nil
}

func commitFailedPINExchange(
	ctx context.Context,
	tx pgx.Tx,
	scopes []central.PINAttemptScope,
	now time.Time,
) error {
	limited := false
	for _, scope := range scopes {
		blocked, err := failPINAttempt(
			ctx, tx, scope.Hash, now, scope.FailureLimit, scope.BlockTime,
		)
		if err != nil {
			return err
		}
		limited = limited || blocked
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed Client PIN exchange: %w", err)
	}
	if limited {
		return central.ErrRateLimited
	}
	return central.ErrUnauthorized
}

func lockPINAttempt(ctx context.Context, tx pgx.Tx, scope [32]byte, now time.Time) (bool, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_pin_attempts (scope_hash, failure_count, updated_at)
		VALUES ($1, 0, $2) ON CONFLICT (scope_hash) DO NOTHING
	`, scope[:], now); err != nil {
		return false, fmt.Errorf("initialize Client PIN attempt: %w", err)
	}
	var blockedUntil *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT blocked_until FROM client_pin_attempts WHERE scope_hash = $1 FOR UPDATE
	`, scope[:]).Scan(&blockedUntil); err != nil {
		return false, fmt.Errorf("lock Client PIN attempt: %w", err)
	}
	return blockedUntil != nil && blockedUntil.After(now), nil
}

func failPINAttempt(
	ctx context.Context,
	tx pgx.Tx,
	scope [32]byte,
	now time.Time,
	failureLimit int,
	blockTime time.Duration,
) (bool, error) {
	var blockedUntil *time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE client_pin_attempts SET
			failure_count = CASE WHEN blocked_until IS NOT NULL AND blocked_until <= $2 THEN 1 ELSE failure_count + 1 END,
			blocked_until = CASE
				WHEN (CASE WHEN blocked_until IS NOT NULL AND blocked_until <= $2 THEN 1 ELSE failure_count + 1 END) >= $3
				THEN $4::timestamptz ELSE NULL::timestamptz END,
			updated_at = $2
		WHERE scope_hash = $1
		RETURNING blocked_until
	`, scope[:], now, failureLimit, now.Add(blockTime)).Scan(&blockedUntil); err != nil {
		return false, fmt.Errorf("record Client PIN failure: %w", err)
	}
	return blockedUntil != nil && blockedUntil.After(now), nil
}

func clientUserByPIN(ctx context.Context, tx pgx.Tx, digest [32]byte) (central.ClientUser, error) {
	var user central.ClientUser
	err := tx.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at
		FROM client_pins AS pins
		JOIN client_users AS users ON users.id = pins.user_id
		WHERE pins.pin_digest = $1 AND pins.revoked_at IS NULL
	`, digest[:]).Scan(&user.ID, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientUser{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("exchange Client PIN: %w", err)
	}
	return user, nil
}

func pinWriteError(operation string, err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return central.ErrConflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}
