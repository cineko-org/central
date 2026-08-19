package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central/reconcile"
	contracts "github.com/cineko-org/contracts/v3"

	"github.com/jackc/pgx/v5"
)

const reconcilerLockKey int64 = 0x43494E454B4F52

type cycleStore struct {
	tx pgx.Tx
}

func (store *Store) RunLeaderCycle(
	ctx context.Context,
	run func(reconcile.CycleRepository) error,
) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, fmt.Errorf("begin reconcile cycle: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var leader bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, reconcilerLockKey).Scan(&leader); err != nil {
		return false, fmt.Errorf("acquire reconcile leadership: %w", err)
	}
	if !leader {
		return false, nil
	}
	if err := run(&cycleStore{tx: tx}); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit reconcile cycle: %w", err)
	}
	return true, nil
}

func (store *cycleStore) MarkStaleProbes(ctx context.Context, cutoff, now time.Time) (int, error) {
	tag, err := store.tx.Exec(ctx, `
		UPDATE probe_runtimes
		SET status = 'offline', available_slots = 0, updated_at = $2
		WHERE status = 'online' AND COALESCE(last_heartbeat_at, updated_at) < $1
	`, cutoff, now)
	if err != nil {
		return 0, fmt.Errorf("mark stale probe runtimes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (store *cycleStore) DeleteRetiredProbes(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := store.tx.Exec(ctx, `
		DELETE FROM probe_runtimes AS probe
		WHERE probe.status = 'offline' AND probe.updated_at < $1
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS assignment
				WHERE assignment.probe_id = probe.id AND assignment.status = 'leased'
			)
			AND NOT EXISTS (
				SELECT 1 FROM assignment_attempts AS attempt
				WHERE attempt.probe_id = probe.id
			)
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete retired probe runtimes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (store *cycleStore) DeleteExpiredClientEvents(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	rows, err := store.tx.Query(ctx, `
		WITH selected AS (
			SELECT sequence
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
	`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired Client events: %w", err)
	}
	defer rows.Close()
	var deleted int64
	for rows.Next() {
		var userID string
		var through, count int64
		if err := rows.Scan(&userID, &through, &count); err != nil {
			return 0, fmt.Errorf("scan expired Client events: %w", err)
		}
		if _, err := store.tx.Exec(ctx, `
			INSERT INTO client_event_cursors (user_id, pruned_through, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE SET
				pruned_through = GREATEST(client_event_cursors.pruned_through, EXCLUDED.pruned_through),
				updated_at = EXCLUDED.updated_at
		`, userID, through); err != nil {
			return 0, fmt.Errorf("advance Client event prune cursor: %w", err)
		}
		if _, err := store.tx.Exec(ctx, `SELECT pg_notify($1, $2)`, clientEventNotifyChannel, userID); err != nil {
			return 0, fmt.Errorf("notify Client event retention: %w", err)
		}
		deleted += count
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired Client events: %w", err)
	}
	return deleted, nil
}

func (store *cycleStore) ExpiredLeases(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]reconcile.ExpiredLease, error) {
	rows, err := store.tx.Query(ctx, `
		SELECT id, probe_id, deadline
		FROM observation_assignments
		WHERE status = 'leased' AND lease_expires_at <= $1
		ORDER BY lease_expires_at, created_at
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired assignment leases: %w", err)
	}
	defer rows.Close()
	leases := make([]reconcile.ExpiredLease, 0)
	for rows.Next() {
		var lease reconcile.ExpiredLease
		if err := rows.Scan(&lease.AssignmentID, &lease.ProbeID, &lease.Deadline); err != nil {
			return nil, fmt.Errorf("scan expired assignment lease: %w", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired assignment leases: %w", err)
	}
	return leases, nil
}

func (store *cycleStore) ExpireLease(
	ctx context.Context,
	lease reconcile.ExpiredLease,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, `
		UPDATE assignment_attempts
		SET status = 'expired', finished_at = $3, error_code = 'lease_expired'
		WHERE assignment_id = $1 AND probe_id = $2 AND status = 'leased'
	`, lease.AssignmentID, lease.ProbeID, now)
	if err != nil {
		return fmt.Errorf("expire assignment attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("expire assignment attempt: expected one active attempt, updated %d", tag.RowsAffected())
	}
	return nil
}

func (store *cycleStore) RetryableFailures(
	ctx context.Context,
	limit int,
) ([]reconcile.RetryableFailure, error) {
	rows, err := store.tx.Query(ctx, `
		SELECT id, deadline
		FROM observation_assignments
		WHERE status = 'retry_pending'
		ORDER BY updated_at, created_at
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query retryable assignment failures: %w", err)
	}
	defer rows.Close()
	failures := make([]reconcile.RetryableFailure, 0)
	for rows.Next() {
		var failure reconcile.RetryableFailure
		if err := rows.Scan(&failure.AssignmentID, &failure.Deadline); err != nil {
			return nil, fmt.Errorf("scan retryable assignment failure: %w", err)
		}
		failures = append(failures, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retryable assignment failures: %w", err)
	}
	return failures, nil
}

func (store *cycleStore) RetryAvailability(
	ctx context.Context,
	assignmentID string,
) (reconcile.RetryAvailability, error) {
	var availability reconcile.RetryAvailability
	err := store.tx.QueryRow(ctx, `
		SELECT count(DISTINCT eligible.network_id)
		FROM assignment_eligible_probes AS eligible
		WHERE eligible.assignment_id = $1
			AND NOT EXISTS (
				SELECT 1 FROM assignment_attempts AS attempt
				LEFT JOIN assignment_eligible_probes AS attempted_eligible
					ON attempted_eligible.assignment_id = attempt.assignment_id
					AND attempted_eligible.probe_id = attempt.probe_id
				WHERE attempt.assignment_id = eligible.assignment_id
					AND COALESCE(attempt.network_id, attempted_eligible.network_id) = eligible.network_id
			)
	`, assignmentID).Scan(&availability.Remaining)
	if err != nil {
		return reconcile.RetryAvailability{}, fmt.Errorf("read remaining assignment probes: %w", err)
	}
	return availability, nil
}

func (store *cycleStore) RequeueAssignment(
	ctx context.Context,
	assignmentID string,
	notBefore time.Time,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, `
		UPDATE observation_assignments
		SET status = 'queued', probe_id = NULL, lease_token_hash = NULL, lease_expires_at = NULL,
			not_before = $2, terminal_reason = '', updated_at = $3
		WHERE id = $1 AND status IN ('leased', 'retry_pending')
	`, assignmentID, notBefore, now)
	if err != nil {
		return fmt.Errorf("requeue expired assignment: %w", err)
	}
	return expectOneRow(tag.RowsAffected(), "requeue expired assignment")
}

func (store *cycleStore) FinishAssignment(
	ctx context.Context,
	assignmentID string,
	status string,
	reason string,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, `
		UPDATE observation_assignments
		SET status = $2, probe_id = NULL, lease_token_hash = NULL, lease_expires_at = NULL,
			terminal_reason = $3, finished_at = $4, updated_at = $4
		WHERE id = $1 AND status IN ('queued', 'leased', 'retry_pending')
	`, assignmentID, status, reason, now)
	if err != nil {
		return fmt.Errorf("finish assignment: %w", err)
	}
	return expectOneRow(tag.RowsAffected(), "finish assignment")
}

func (store *cycleStore) TimedOutAssignments(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]reconcile.TimedOutAssignment, error) {
	rows, err := store.tx.Query(ctx, `
		SELECT assignment.id, (
			SELECT count(*) FROM assignment_attempts AS attempt
			WHERE attempt.assignment_id = assignment.id
		)
		FROM observation_assignments AS assignment
		WHERE assignment.status IN ('queued', 'retry_pending') AND assignment.deadline <= $1
		ORDER BY assignment.deadline, assignment.created_at
		FOR UPDATE OF assignment SKIP LOCKED
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query timed out assignments: %w", err)
	}
	defer rows.Close()
	assignments := make([]reconcile.TimedOutAssignment, 0)
	for rows.Next() {
		var assignment reconcile.TimedOutAssignment
		if err := rows.Scan(&assignment.AssignmentID, &assignment.AttemptCount); err != nil {
			return nil, fmt.Errorf("scan timed out assignment: %w", err)
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timed out assignments: %w", err)
	}
	return assignments, nil
}

func (store *cycleStore) TerminalPolicyRuns(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]reconcile.TerminalPolicyRun, error) {
	rows, err := store.tx.Query(ctx, `
		WITH demand_theaters AS (
			SELECT preset.payload->>'theaterId' AS theater_id,
				BOOL_OR(COALESCE(monitor.payload->>'mode', 'opening') IN ('', 'opening')) AS opening_active,
				BOOL_OR(monitor.payload->>'mode' = 'cancellation') AS cancellation_active
			FROM client_resources AS monitor
			JOIN client_resources AS preset
				ON preset.user_id = monitor.user_id
				AND preset.kind = 'presets'
				AND preset.id = monitor.payload->>'presetId'
				AND preset.deleted_at IS NULL
			WHERE monitor.kind = 'monitors' AND monitor.deleted_at IS NULL
				AND monitor.payload->>'status' IN ('pending', 'running')
			GROUP BY preset.payload->>'theaterId'
		)
		SELECT policy.id, policy.enabled, terminal.finished_at, terminal.status,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 2
				WHEN COALESCE(demand.cancellation_active, false) THEN 30
				WHEN policy.burst_until > $1 THEN 15
				ELSE policy.min_interval_seconds
			END,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 5
				WHEN COALESCE(demand.cancellation_active, false) THEN 45
				WHEN policy.burst_until > $1 THEN 30
				ELSE policy.max_interval_seconds
			END
		FROM observation_policies AS policy
		LEFT JOIN demand_theaters AS demand ON demand.theater_id = policy.theater_id
		JOIN LATERAL (
			SELECT assignment.finished_at, assignment.status
			FROM observation_assignments AS assignment
			WHERE assignment.policy_id = policy.id
				AND assignment.status IN ('completed', 'partial', 'failed', 'missed')
			ORDER BY assignment.finished_at DESC, assignment.created_at DESC
			LIMIT 1
		) AS terminal ON true
		WHERE policy.next_run_at IS NULL AND policy.deleted_at IS NULL
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS active
				WHERE active.policy_id = policy.id AND active.status IN ('queued', 'leased', 'retry_pending')
			)
		ORDER BY terminal.finished_at, policy.id
		FOR UPDATE OF policy SKIP LOCKED
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query terminal policy runs: %w", err)
	}
	defer rows.Close()
	runs := make([]reconcile.TerminalPolicyRun, 0)
	for rows.Next() {
		var run reconcile.TerminalPolicyRun
		var minimumSeconds, maximumSeconds int
		if err := rows.Scan(
			&run.PolicyID, &run.Enabled, &run.FinishedAt, &run.Outcome, &minimumSeconds, &maximumSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan terminal policy run: %w", err)
		}
		run.MinimumInterval = time.Duration(minimumSeconds) * time.Second
		run.MaximumInterval = time.Duration(maximumSeconds) * time.Second
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal policy runs: %w", err)
	}
	return runs, nil
}

func (store *cycleStore) AdvancePolicy(
	ctx context.Context,
	run reconcile.TerminalPolicyRun,
	nextRunAt *time.Time,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, `
		UPDATE observation_policies
		SET next_run_at = $2, last_finished_at = $3, last_outcome = $4,
			last_error_code = '', updated_at = $5
		WHERE id = $1 AND next_run_at IS NULL
	`, run.PolicyID, nextRunAt, run.FinishedAt, run.Outcome, now)
	if err != nil {
		return fmt.Errorf("advance observation policy: %w", err)
	}
	return expectOneRow(tag.RowsAffected(), "advance observation policy")
}

func (store *cycleStore) DuePolicies(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]reconcile.Policy, error) {
	rows, err := store.tx.Query(ctx, `
		WITH demand_theaters AS (
			SELECT preset.payload->>'theaterId' AS theater_id,
				BOOL_OR(COALESCE(monitor.payload->>'mode', 'opening') IN ('', 'opening')) AS opening_active,
				BOOL_OR(monitor.payload->>'mode' = 'cancellation') AS cancellation_active
			FROM client_resources AS monitor
			JOIN client_resources AS preset
				ON preset.user_id = monitor.user_id
				AND preset.kind = 'presets'
				AND preset.id = monitor.payload->>'presetId'
				AND preset.deleted_at IS NULL
			WHERE monitor.kind = 'monitors' AND monitor.deleted_at IS NULL
				AND monitor.payload->>'status' IN ('pending', 'running')
			GROUP BY preset.payload->>'theaterId'
		)
		SELECT policy.id, policy.enabled, policy.task_kind, policy.theater_id,
			policy.theater_provider_id, policy.theater_source_key, policy.theater_region,
			policy.theater_name, policy.target_date_mode, policy.target_dates::text[], policy.horizon_days,
			policy.locale, policy.time_zone, policy.egress_policy_id,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 90 + policy.priority / 11
				WHEN policy.burst_until > $1 THEN 60 + policy.priority / 11
				WHEN COALESCE(demand.cancellation_active, false) THEN 40 + policy.priority / 11
				ELSE policy.priority / 3
			END AS effective_priority,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 2
				WHEN policy.burst_until > $1 THEN 15
				WHEN COALESCE(demand.cancellation_active, false) THEN 30
				ELSE policy.min_interval_seconds
			END,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 5
				WHEN policy.burst_until > $1 THEN 30
				WHEN COALESCE(demand.cancellation_active, false) THEN 45
				ELSE policy.max_interval_seconds
			END,
			policy.execution_window_seconds,
			policy.next_run_at, policy.last_finished_at, COALESCE(policy.last_outcome, '')
		FROM observation_policies AS policy
		LEFT JOIN demand_theaters AS demand ON demand.theater_id = policy.theater_id
		WHERE policy.enabled AND policy.deleted_at IS NULL AND policy.next_run_at <= $1
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS active
				WHERE active.policy_id = policy.id AND active.status IN ('queued', 'leased', 'retry_pending')
			)
		ORDER BY effective_priority DESC, policy.next_run_at, policy.id
		FOR UPDATE OF policy SKIP LOCKED
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query due observation policies: %w", err)
	}
	defer rows.Close()
	policies := make([]reconcile.Policy, 0)
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due observation policies: %w", err)
	}
	return policies, nil
}

func (store *cycleStore) EligibleProbes(
	ctx context.Context,
	policy reconcile.Policy,
	now time.Time,
	heartbeatCutoff time.Time,
) ([]reconcile.CandidateProbe, error) {
	rows, err := store.tx.Query(ctx, `
		SELECT id, network_id
		FROM probe_runtimes
		WHERE status = 'online' AND NOT draining AND health = 'healthy'
			AND COALESCE(last_heartbeat_at, updated_at) >= $1
			AND token_expires_at > $2
			AND $3 = ANY(available_capabilities)
		ORDER BY network_id, id
	`, heartbeatCutoff, now, policy.TaskKind)
	if err != nil {
		return nil, fmt.Errorf("query eligible probe runtimes: %w", err)
	}
	defer rows.Close()
	probes := make([]reconcile.CandidateProbe, 0)
	for rows.Next() {
		var probe reconcile.CandidateProbe
		if err := rows.Scan(&probe.ID, &probe.NetworkID); err != nil {
			return nil, fmt.Errorf("scan eligible probe runtime: %w", err)
		}
		probes = append(probes, probe)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate eligible probe runtimes: %w", err)
	}
	return probes, nil
}

func (store *cycleStore) CatalogRefreshRequired(
	ctx context.Context,
	retryCutoff time.Time,
) (bool, error) {
	var required bool
	err := store.tx.QueryRow(ctx, `
		SELECT
			(
				NOT EXISTS (SELECT 1 FROM theaters WHERE active)
				OR EXISTS (
					SELECT 1 FROM catalog_state
					WHERE id = 1 AND refresh_requested_at IS NOT NULL
				)
			)
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments
				WHERE task_kind = $1 AND status IN ('queued', 'leased', 'retry_pending')
			)
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments
				WHERE task_kind = $1 AND status IN ('completed', 'partial', 'failed', 'missed')
					AND updated_at > $2
			)
	`, contracts.CapabilityCGVCatalogCapture, retryCutoff).Scan(&required)
	if err != nil {
		return false, fmt.Errorf("inspect catalog refresh requirement: %w", err)
	}
	return required, nil
}

func (store *cycleStore) SeatMapBackfillTarget(
	ctx context.Context,
	now time.Time,
) (*reconcile.SeatMapBackfillTarget, error) {
	target := &reconcile.SeatMapBackfillTarget{}
	task := &target.Task
	var auditorium contracts.Auditorium
	var showtime contracts.Showtime
	var startsAt, endsAt time.Time
	err := store.tx.QueryRow(ctx, `
		SELECT theater.id, theater.provider_id, theater.source_key, theater.region, theater.name,
			auditorium.id, auditorium.theater_id, auditorium.source_key, auditorium.name,
			auditorium.screen_types, auditorium.capacity,
			showtime.id, showtime.provider_id, showtime.source_key, showtime.starts_at, showtime.ends_at,
			movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url,
			auditorium.seat_map_requested_at IS NOT NULL
		FROM auditoriums AS auditorium
		JOIN theaters AS theater ON theater.id = auditorium.theater_id AND theater.active
		JOIN LATERAL (
			SELECT candidate.* FROM showtimes AS candidate
			WHERE candidate.auditorium_id = auditorium.id AND candidate.active AND candidate.ends_at > $1
			ORDER BY candidate.starts_at LIMIT 1
		) AS showtime ON true
		JOIN movies AS movie ON movie.id = showtime.movie_id AND movie.active
		WHERE auditorium.active
			AND (auditorium.current_seat_map_version_id IS NULL OR auditorium.seat_map_requested_at IS NOT NULL)
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS assignment
				WHERE assignment.task_kind = $2
					AND assignment.status IN ('queued', 'leased', 'retry_pending')
					AND assignment.task_data->'auditorium'->>'id' = auditorium.id
			)
		ORDER BY auditorium.seat_map_requested_at DESC NULLS LAST, auditorium.updated_at, showtime.starts_at
		FOR UPDATE OF auditorium SKIP LOCKED
		LIMIT 1
	`, now, contracts.CapabilityCGVSeatMapCapture).Scan(
		&task.Theater.ID, &task.Theater.ProviderID, &task.Theater.SourceKey,
		&task.Theater.Region, &task.Theater.Name,
		&auditorium.ID, &auditorium.TheaterID, &auditorium.SourceKey, &auditorium.Name,
		&auditorium.ScreenTypes, &auditorium.Capacity,
		&showtime.ID, &showtime.ProviderID, &showtime.SourceKey, &startsAt, &endsAt,
		&showtime.Movie.ID, &showtime.Movie.ProviderID, &showtime.Movie.SourceKey,
		&showtime.Movie.Title, &showtime.Movie.PosterURL, &target.Requested,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read seat-map backfill target: %w", err)
	}
	showtime.TheaterID = task.Theater.ID
	showtime.Auditorium = auditorium
	showtime.Capacity = auditorium.Capacity
	showtime.StartsAt = startsAt
	showtime.EndsAt = endsAt
	task.Kind = contracts.CapabilityCGVSeatMapCapture
	task.Auditorium = &auditorium
	task.Showtime = &showtime
	task.TargetDates = []string{startsAt.In(time.FixedZone("KST", 9*60*60)).Format(time.DateOnly)}
	task.Locale = "ko-KR"
	task.TimeZone = "Asia/Seoul"
	return target, nil
}

func (store *cycleStore) CreateAssignment(ctx context.Context, assignment reconcile.NewAssignment) error {
	targetDates, err := postgresDates(assignment.Task.TargetDates)
	if err != nil {
		return err
	}
	taskData, err := json.Marshal(assignment.Task)
	if err != nil {
		return fmt.Errorf("encode observation assignment task: %w", err)
	}
	var insertedID string
	err = store.tx.QueryRow(ctx, `
		INSERT INTO observation_assignments (
			id, policy_id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, priority, status, not_before, deadline,
			terminal_reason, finished_at, created_at, updated_at, task_data
		) VALUES (
			$1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19, $20
		)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, assignment.ID, assignment.PolicyID, assignment.Task.Kind, assignment.Task.Theater.ID,
		assignment.Task.Theater.ProviderID, assignment.Task.Theater.SourceKey,
		assignment.Task.Theater.Region, assignment.Task.Theater.Name, targetDates, assignment.Task.Locale,
		assignment.Task.TimeZone, assignment.Task.EgressPolicyID, assignment.Priority, assignment.Status,
		assignment.NotBefore, assignment.Deadline, assignment.ReasonCode, nullableTime(assignment.FinishedAt),
		assignment.CreatedAt, taskData).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var idCollision bool
		if err := store.tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM observation_assignments WHERE id = $1)
		`, assignment.ID).Scan(&idCollision); err != nil {
			return fmt.Errorf("inspect observation assignment conflict: %w", err)
		}
		if idCollision {
			return fmt.Errorf("observation assignment id collision: %s", assignment.ID)
		}
		return reconcile.ErrTargetBusy
	}
	if err != nil {
		return fmt.Errorf("insert observation assignment: %w", err)
	}
	if len(assignment.Candidates) > 0 {
		batch := &pgx.Batch{}
		for _, candidate := range assignment.Candidates {
			batch.Queue(`
				INSERT INTO assignment_eligible_probes (assignment_id, probe_id, network_id, eligible_at)
				VALUES ($1, $2, $3, $4)
			`, assignment.ID, candidate.ID, candidate.NetworkID, assignment.CreatedAt)
		}
		results := store.tx.SendBatch(ctx, batch)
		for range assignment.Candidates {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return fmt.Errorf("insert assignment eligible probe: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("finish assignment eligible probes: %w", err)
		}
	}
	if assignment.PolicyID == "" {
		return nil
	}
	tag, err := store.tx.Exec(ctx, `
		UPDATE observation_policies SET next_run_at = NULL, updated_at = $2
		WHERE id = $1 AND next_run_at IS NOT NULL
	`, assignment.PolicyID, assignment.CreatedAt)
	if err != nil {
		return fmt.Errorf("activate observation policy assignment: %w", err)
	}
	return expectOneRow(tag.RowsAffected(), "activate observation policy assignment")
}

func (store *cycleStore) SuspendPolicy(
	ctx context.Context,
	policyID string,
	reason string,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, `
		UPDATE observation_policies
		SET enabled = false, next_run_at = NULL, last_error_code = $2, updated_at = $3, revision = revision + 1
		WHERE id = $1 AND deleted_at IS NULL
	`, policyID, reason, now)
	if err != nil {
		return fmt.Errorf("suspend observation policy: %w", err)
	}
	return expectOneRow(tag.RowsAffected(), "suspend observation policy")
}

func (store *cycleStore) OldestDuePolicy(ctx context.Context, now time.Time) (*time.Time, error) {
	var oldest *time.Time
	if err := store.tx.QueryRow(ctx, `
		SELECT min(next_run_at)
		FROM observation_policies
		WHERE enabled AND deleted_at IS NULL AND next_run_at <= $1
	`, now).Scan(&oldest); err != nil {
		return nil, fmt.Errorf("read oldest due observation policy: %w", err)
	}
	return oldest, nil
}

func scanPolicy(row rowScanner) (reconcile.Policy, error) {
	var policy reconcile.Policy
	var horizonDays *int
	var minimumSeconds, maximumSeconds, executionWindowSeconds int
	var lastFinishedAt *time.Time
	err := row.Scan(
		&policy.ID, &policy.Enabled, &policy.TaskKind, &policy.Theater.ID,
		&policy.Theater.ProviderID, &policy.Theater.SourceKey, &policy.Theater.Region,
		&policy.Theater.Name, &policy.TargetDateMode, &policy.TargetDates, &horizonDays,
		&policy.Locale, &policy.TimeZone, &policy.EgressPolicyID, &policy.Priority,
		&minimumSeconds, &maximumSeconds, &executionWindowSeconds,
		&policy.NextRunAt, &lastFinishedAt, &policy.LastOutcome,
	)
	if err != nil {
		return reconcile.Policy{}, fmt.Errorf("scan observation policy: %w", err)
	}
	if horizonDays != nil {
		policy.HorizonDays = *horizonDays
	}
	if lastFinishedAt != nil {
		policy.LastFinishedAt = *lastFinishedAt
	}
	policy.MinimumInterval = time.Duration(minimumSeconds) * time.Second
	policy.MaximumInterval = time.Duration(maximumSeconds) * time.Second
	policy.ExecutionWindow = time.Duration(executionWindowSeconds) * time.Second
	return policy, nil
}

func postgresDates(values []string) ([]time.Time, error) {
	dates := make([]time.Time, len(values))
	for index, value := range values {
		date, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return nil, fmt.Errorf("encode assignment target date %q: %w", value, err)
		}
		dates[index] = date
	}
	return dates, nil
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func expectOneRow(affected int64, operation string) error {
	if affected != 1 {
		return fmt.Errorf("%s: expected one row, updated %d", operation, affected)
	}
	return nil
}
