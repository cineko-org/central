package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	assignmentNotifyChannel          = "cineko_assignment_available"
	assignmentListenerCleanupTimeout = 2 * time.Second
	assignmentWakeQuery              = `
		SELECT EXISTS (
			SELECT 1
			FROM probe_runtimes AS probe
			JOIN assignment_eligible_probes AS eligible ON eligible.probe_id = probe.id
			JOIN observation_assignments AS assignment ON assignment.id = eligible.assignment_id
			WHERE probe.id = $1
				AND probe.status = 'online' AND NOT probe.draining AND probe.health = 'healthy'
				AND probe.available_slots > 0
				AND COALESCE(probe.last_heartbeat_at, probe.updated_at) >= $2
				AND assignment.status = 'queued'
				AND assignment.not_before <= CURRENT_TIMESTAMP
				AND assignment.deadline > CURRENT_TIMESTAMP
				AND assignment.task_kind = ANY(probe.available_capabilities)
				AND eligible.network_id = probe.network_id
				AND NOT EXISTS (
					SELECT 1
					FROM assignment_attempts AS attempted
					LEFT JOIN assignment_eligible_probes AS attempted_eligible
						ON attempted_eligible.assignment_id = attempted.assignment_id
						AND attempted_eligible.probe_id = attempted.probe_id
					WHERE attempted.assignment_id = assignment.id
						AND COALESCE(attempted.network_id, attempted_eligible.network_id) = eligible.network_id
				)
				AND NOT EXISTS (
					SELECT 1
					FROM observation_assignments AS active
					WHERE active.task_kind = assignment.task_kind
						AND active.theater_provider_id = assignment.theater_provider_id
						AND active.theater_id = assignment.theater_id
						AND active.status = 'leased'
				)
		)
	`
)

// WaitForAssignment wakes on committed assignment or probe state changes and
// rechecks the same durable eligibility rules used by ClaimAssignment.
func (store *Store) WaitForAssignment(ctx context.Context, probeID string, heartbeatCutoff time.Time) error {
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire assignment listener: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "LISTEN "+assignmentNotifyChannel); err != nil {
		return fmt.Errorf("listen for assignments: %w", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), assignmentListenerCleanupTimeout)
		defer cancel()
		_, _ = connection.Exec(cleanupContext, "UNLISTEN "+assignmentNotifyChannel)
	}()

	ready, err := assignmentWakeReady(ctx, connection.Conn(), probeID, heartbeatCutoff)
	if err != nil || ready {
		return err
	}
	for {
		if _, err := connection.Conn().WaitForNotification(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("wait for assignment notification: %w", err)
		}
		ready, err := assignmentWakeReady(ctx, connection.Conn(), probeID, heartbeatCutoff)
		if err != nil || ready {
			return err
		}
	}
}

func assignmentWakeReady(
	ctx context.Context,
	connection *pgx.Conn,
	probeID string,
	heartbeatCutoff time.Time,
) (bool, error) {
	var ready bool
	err := connection.QueryRow(ctx, assignmentWakeQuery, probeID, heartbeatCutoff).Scan(&ready)
	if err != nil {
		return false, fmt.Errorf("recheck assignment eligibility: %w", err)
	}
	return ready, nil
}

func notifyAssignmentAvailability(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, assignmentNotifyChannel, "assignment"); err != nil {
		return fmt.Errorf("notify assignment availability: %w", err)
	}
	return nil
}
