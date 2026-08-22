package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
)

// nextReconcileDeadlineQuery deliberately returns only the next fast-lane
// deadline. Ordinary cleanup, lease expiry, and catalog maintenance continue
// to use the bounded engine maintenance interval. Keeping this query separate
// from the full reconcile transaction means an idle engine sleeps without
// repeatedly executing the full cycle.
const nextReconcileDeadlineQuery = `
WITH demand_theaters AS (
	SELECT DISTINCT preset.theater_id
	FROM client_monitors AS monitor
	JOIN client_resources AS monitor_resource
		ON monitor_resource.user_id = monitor.user_id
		AND monitor_resource.kind = 'monitors'
		AND monitor_resource.id = monitor.id
		AND monitor_resource.deleted_at IS NULL
	JOIN client_presets AS preset
		ON preset.user_id = monitor.user_id
		AND preset.resource_kind = 'presets'
		AND preset.id = monitor.preset_id
	JOIN client_resources AS preset_resource
		ON preset_resource.user_id = preset.user_id
		AND preset_resource.kind = 'presets'
		AND preset_resource.id = preset.id
		AND preset_resource.deleted_at IS NULL
	WHERE monitor.resource_kind = 'monitors'
		AND monitor.state IN ('pending', 'running')
), policy_deadlines AS (
	SELECT CASE
		WHEN demand.theater_id IS NOT NULL THEN $1::timestamptz
		ELSE policy.next_run_at
	END AS deadline
	FROM observation_policies AS policy
	LEFT JOIN demand_theaters AS demand ON demand.theater_id = policy.theater_id
	WHERE policy.enabled AND policy.deleted_at IS NULL
		AND (demand.theater_id IS NOT NULL OR policy.next_run_at IS NOT NULL)
		AND NOT EXISTS (
			SELECT 1
			FROM observation_assignments AS active
			WHERE active.policy_id = policy.id
				AND active.status IN ('queued', 'leased', 'retry_pending')
				AND NOT (
					active.status = 'queued'
					AND active.lane = 'baseline'
					AND demand.theater_id IS NOT NULL
				)
		)
), availability_deadlines AS (
	SELECT CASE
		WHEN latest.finished_at IS NULL THEN $1::timestamptz
		ELSE latest.finished_at + make_interval(
			secs => 2 + mod(hashtext(latest.id)::bigint + 2147483648, 4)::integer
		)
	END AS deadline
	FROM showtimes AS showtime
	JOIN theaters AS theater ON theater.id = showtime.theater_id AND theater.active
	JOIN movies AS movie ON movie.id = showtime.movie_id AND movie.active
	JOIN auditoriums AS auditorium ON auditorium.id = showtime.auditorium_id AND auditorium.active
	LEFT JOIN LATERAL (
		SELECT assignment.id, assignment.finished_at
		FROM observation_assignments AS assignment
		WHERE assignment.task_kind = $2
			AND assignment.showtime_id = showtime.id
			AND assignment.status IN ('completed', 'partial', 'failed', 'missed')
		ORDER BY assignment.finished_at DESC NULLS LAST, assignment.created_at DESC
		LIMIT 1
	) AS latest ON true
	WHERE showtime.active AND showtime.starts_at > $1
		AND showtime.provider_id = $3
		AND theater.provider_id = $3
		AND movie.provider_id = $3
		AND EXISTS (
			SELECT 1
			FROM client_monitors AS monitor
			JOIN client_resources AS monitor_resource
				ON monitor_resource.user_id = monitor.user_id
				AND monitor_resource.kind = 'monitors'
				AND monitor_resource.id = monitor.id
				AND monitor_resource.deleted_at IS NULL
			JOIN client_presets AS preset
				ON preset.user_id = monitor.user_id
				AND preset.resource_kind = 'presets'
				AND preset.id = monitor.preset_id
			JOIN client_resources AS preset_resource
				ON preset_resource.user_id = preset.user_id
				AND preset_resource.kind = 'presets'
				AND preset_resource.id = preset.id
				AND preset_resource.deleted_at IS NULL
			WHERE monitor.resource_kind = 'monitors'
				AND monitor.state IN ('pending', 'running')
				AND monitor.movie_id = showtime.movie_id
				AND preset.auditorium_id = showtime.auditorium_id
		)
		AND NOT EXISTS (
			SELECT 1
			FROM observation_assignments AS active
			WHERE active.task_kind = $2
				AND active.showtime_id = showtime.id
				AND active.status IN ('queued', 'leased', 'retry_pending')
		)
)
SELECT MIN(deadline)
FROM (
	SELECT deadline FROM policy_deadlines
	UNION ALL
	SELECT deadline FROM availability_deadlines
) AS deadlines
WHERE deadline IS NOT NULL
`

// NextReconcileDeadline is a cheap read used only to arm the adaptive timer.
// Due work is still claimed under RunLeaderCycle's advisory lock, so multiple
// Central instances may wake at the same deadline without duplicating work.
func (store *Store) NextReconcileDeadline(ctx context.Context, now time.Time) (*time.Time, error) {
	var deadline *time.Time
	err := store.pool.QueryRow(ctx, nextReconcileDeadlineQuery, now,
		probedomain.CapabilityCGVSeatAvailabilityCapture, catalogdomain.ProviderCGV).Scan(&deadline)
	if err != nil {
		return nil, fmt.Errorf("read next reconcile deadline: %w", err)
	}
	return deadline, nil
}

// WaitForReconcileWakeup blocks on the existing durable-state notifications.
// Assignment result commits and client resource events both use transaction
// scoped NOTIFY, so a wakeup is delivered only after the state that can move a
// fast-lane deadline is committed. The adaptive timer remains the bounded
// fallback if a listener connection is unavailable.
func (store *Store) WaitForReconcileWakeup(ctx context.Context) error {
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire reconcile wakeup listener: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "LISTEN "+assignmentNotifyChannel); err != nil {
		return fmt.Errorf("listen for assignment reconcile wakeups: %w", err)
	}
	if _, err := connection.Exec(ctx, "LISTEN "+clientEventNotifyChannel); err != nil {
		return fmt.Errorf("listen for Client reconcile wakeups: %w", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = connection.Exec(cleanupContext, "UNLISTEN "+assignmentNotifyChannel)
		_, _ = connection.Exec(cleanupContext, "UNLISTEN "+clientEventNotifyChannel)
	}()
	if _, err := connection.Conn().WaitForNotification(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("wait for reconcile wakeup: %w", err)
	}
	return nil
}
