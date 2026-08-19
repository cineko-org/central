package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const clientEventNotifyChannel = "cineko_client_events"

// WaitClientEvents blocks on PostgreSQL NOTIFY after installing its listener
// and then re-checking durable state. That ordering closes the check/listen
// race without periodically querying the database.
func (store *Store) WaitClientEvents(
	ctx context.Context,
	userID string,
	after int64,
	releaseGeneration int64,
) error {
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire client event listener: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "LISTEN "+clientEventNotifyChannel); err != nil {
		return fmt.Errorf("listen for client events: %w", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = connection.Exec(cleanupContext, "UNLISTEN "+clientEventNotifyChannel)
	}()

	ready, err := clientEventWakeReady(ctx, connection.Conn(), userID, after, releaseGeneration)
	if err != nil || ready {
		return err
	}
	for {
		notification, err := connection.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("wait for client event notification: %w", err)
		}
		if notification.Payload == userID || notification.Payload == "release" {
			return nil
		}
	}
}

func clientEventWakeReady(
	ctx context.Context,
	connection *pgx.Conn,
	userID string,
	after int64,
	releaseGeneration int64,
) (bool, error) {
	var ready bool
	err := connection.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM client_events WHERE user_id = $1 AND sequence > $2)
			OR COALESCE((SELECT pruned_through FROM client_event_cursors WHERE user_id = $1), 0) > $2
			OR (SELECT generation FROM desktop_release_registry_state WHERE singleton = true) <> $3
	`, userID, after, releaseGeneration).Scan(&ready)
	if err != nil {
		return false, fmt.Errorf("recheck client event state: %w", err)
	}
	return ready, nil
}

// DeleteExpiredClientEvents advances each affected user's durable prune cursor
// in the same transaction that deletes an ordered batch.
func (store *Store) DeleteExpiredClientEvents(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if limit < 1 {
		return 0, errors.New("client event retention batch size must be positive")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin client event retention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		WITH selected AS (
			SELECT sequence, user_id
			FROM client_events
			WHERE occurred_at < $1
			ORDER BY occurred_at, sequence
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), deleted AS (
			DELETE FROM client_events AS events
			USING selected
			WHERE events.sequence = selected.sequence
			RETURNING events.user_id, events.sequence
		)
		SELECT user_id, MAX(sequence), COUNT(*) FROM deleted GROUP BY user_id
	`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired client events: %w", err)
	}
	var deleted int64
	type prunedUser struct {
		id      string
		through int64
		count   int64
	}
	pruned := make([]prunedUser, 0)
	for rows.Next() {
		var userID string
		var through, count int64
		if err := rows.Scan(&userID, &through, &count); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan client event retention: %w", err)
		}
		pruned = append(pruned, prunedUser{id: userID, through: through, count: count})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate client event retention: %w", err)
	}
	for _, user := range pruned {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_event_cursors (user_id, pruned_through, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE SET
				pruned_through = GREATEST(client_event_cursors.pruned_through, EXCLUDED.pruned_through),
				updated_at = EXCLUDED.updated_at
		`, user.id, user.through); err != nil {
			return 0, fmt.Errorf("advance client event prune cursor: %w", err)
		}
		deleted += user.count
	}
	for _, user := range pruned {
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, clientEventNotifyChannel, user.id); err != nil {
			return 0, fmt.Errorf("notify client event retention: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit client event retention: %w", err)
	}
	return deleted, nil
}
