package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	"github.com/cineko-org/central/internal/observation/planning"
	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	reconcilerLockKey          int64 = 0x43494E454B4F52
	seatMapBackfillHorizonDays int   = 14
)

var seatMapBackfillLocation = time.FixedZone("KST", 9*60*60)

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
	type prunedClientEvents struct {
		userID  string
		through int64
		count   int64
	}
	pruned := make([]prunedClientEvents, 0)
	for rows.Next() {
		var userID string
		var through, count int64
		if err := rows.Scan(&userID, &through, &count); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired Client events: %w", err)
		}
		pruned = append(pruned, prunedClientEvents{userID: userID, through: through, count: count})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired Client events: %w", err)
	}
	var deleted int64
	for _, user := range pruned {
		if _, err := store.tx.Exec(ctx, `
			INSERT INTO client_event_cursors (user_id, pruned_through, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE SET
				pruned_through = GREATEST(client_event_cursors.pruned_through, EXCLUDED.pruned_through),
				updated_at = EXCLUDED.updated_at
		`, user.userID, user.through); err != nil {
			return 0, fmt.Errorf("advance Client event prune cursor: %w", err)
		}
		if _, err := store.tx.Exec(ctx, `SELECT pg_notify($1, $2)`, clientEventNotifyChannel, user.userID); err != nil {
			return 0, fmt.Errorf("notify Client event retention: %w", err)
		}
		deleted += user.count
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
	if err := expectOneRow(tag.RowsAffected(), "requeue expired assignment"); err != nil {
		return err
	}
	if err := requeueSeatMapCollectionForAssignmentTx(ctx, store.tx, assignmentID, now); err != nil {
		return fmt.Errorf("requeue seat-map collection: %w", err)
	}
	return notifyAssignmentAvailability(ctx, store.tx)
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
	if err := expectOneRow(tag.RowsAffected(), "finish assignment"); err != nil {
		return err
	}
	if err := requeueSeatMapCollectionForAssignmentTx(ctx, store.tx, assignmentID, now); err != nil {
		return fmt.Errorf("finish seat-map collection: %w", err)
	}
	return notifyAssignmentAvailability(ctx, store.tx)
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
			SELECT preset.theater_id, true AS booking_active
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
			GROUP BY preset.theater_id
		)
		SELECT policy.id, policy.enabled, terminal.finished_at, terminal.status,
			CASE
				WHEN COALESCE(demand.booking_active, false) THEN 2
				WHEN policy.burst_until > $1 THEN 15
				ELSE 300
			END,
			CASE
				WHEN COALESCE(demand.booking_active, false) THEN 5
				WHEN policy.burst_until > $1 THEN 30
				ELSE 900
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
	WITH demand_monitors AS (
			SELECT preset.theater_id
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
		), demand_theaters AS (
			SELECT theater_id, true AS booking_active
			FROM demand_monitors
			GROUP BY theater_id
		), latest_hot AS (
			SELECT latest.policy_id, latest.finished_at, latest.target_dates, latest.fingerprint
			FROM (
				SELECT DISTINCT ON (assignment.policy_id) assignment.policy_id,
					assignment.status, assignment.finished_at, assignment.target_dates::text[] AS target_dates,
					assignment.hot_target_fingerprint AS fingerprint
				FROM observation_assignments AS assignment
				WHERE assignment.policy_id IS NOT NULL
					AND assignment.status IN ('completed', 'partial', 'failed', 'missed')
					AND assignment.lane = 'hot'
				ORDER BY assignment.policy_id, assignment.finished_at DESC NULLS LAST, assignment.created_at DESC
			) AS latest
			WHERE latest.status = 'completed'
		), latest_baseline AS (
			SELECT DISTINCT ON (assignment.policy_id) assignment.policy_id,
				assignment.finished_at, (assignment.target_dates::text[])[1] AS target_date
			FROM observation_assignments AS assignment
			WHERE assignment.policy_id IS NOT NULL
				AND assignment.status = 'completed'
				AND assignment.lane = 'baseline'
			ORDER BY assignment.policy_id, assignment.finished_at DESC NULLS LAST, assignment.created_at DESC
		)
		SELECT policy.id, policy.enabled, policy.task_kind, policy.theater_id,
			policy.theater_provider_id, policy.theater_source_key, policy.theater_region,
			policy.theater_name, policy.target_date_mode, policy.target_dates::text[], LEAST(policy.horizon_days, 14),
			policy.locale, policy.time_zone, policy.egress_policy_id,
			CASE
				WHEN COALESCE(demand.booking_active, false) THEN $2::integer
				WHEN policy.burst_until > $1 THEN $3::integer
				ELSE $4::integer
			END AS effective_priority,
			CASE
				WHEN COALESCE(demand.booking_active, false) THEN 2
				WHEN policy.burst_until > $1 THEN 15
				ELSE 300
			END,
			CASE
				WHEN COALESCE(demand.booking_active, false) THEN 5
				WHEN policy.burst_until > $1 THEN 30
				ELSE 900
			END,
			policy.execution_window_seconds,
			policy.max_interval_seconds,
			COALESCE(policy.next_run_at, $1), policy.last_finished_at, COALESCE(policy.last_outcome, ''),
			latest_hot.finished_at, latest_hot.target_dates, latest_hot.fingerprint,
			latest_baseline.finished_at, latest_baseline.target_date
		FROM observation_policies AS policy
		LEFT JOIN demand_theaters AS demand ON demand.theater_id = policy.theater_id
		LEFT JOIN latest_hot ON latest_hot.policy_id = policy.id
		LEFT JOIN latest_baseline ON latest_baseline.policy_id = policy.id
		WHERE policy.enabled AND policy.deleted_at IS NULL
			AND (policy.next_run_at <= $1 OR demand.booking_active)
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS active
				WHERE active.policy_id = policy.id
					AND active.status IN ('queued', 'leased', 'retry_pending')
					AND NOT (
						active.status = 'queued'
						AND active.lane = 'baseline'
						AND demand.booking_active
					)
			)
		ORDER BY effective_priority DESC, policy.next_run_at, policy.id
		FOR UPDATE OF policy SKIP LOCKED
		LIMIT $5
	`, now, planning.PriorityScheduleDiscovery, planning.PriorityRecentChange,
		planning.PriorityBaselineObservation, limit)
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
	rows.Close()
	for i := range policies {
		policies[i].HotTargets, err = loadDuePolicyMonitorTargets(ctx, store.tx, policies[i].Theater.GetId())
		if err != nil {
			return nil, err
		}
	}
	return policies, nil
}

func (store *cycleStore) PreemptQueuedBaseline(
	ctx context.Context,
	policyID string,
	now time.Time,
) error {
	tag, err := store.tx.Exec(ctx, preemptQueuedBaselineAssignmentsQuery, policyID, now)
	if err != nil {
		return fmt.Errorf("preempt queued baseline assignment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	policyTag, err := store.tx.Exec(ctx, makePreemptedObservationPolicyDueQuery, policyID, now)
	if err != nil {
		return fmt.Errorf("make preempted observation policy due: %w", err)
	}
	return expectOneRow(policyTag.RowsAffected(), "make preempted observation policy due")
}

const preemptQueuedBaselineAssignmentsQuery = `
	UPDATE observation_assignments
	SET status = 'missed', terminal_reason = 'hot_demand_preempted',
		finished_at = $2, updated_at = $2
	WHERE policy_id = $1 AND status = 'queued'
		AND lane = 'baseline'
`

const makePreemptedObservationPolicyDueQuery = `
	UPDATE observation_policies
	SET next_run_at = LEAST(COALESCE(next_run_at, $2), $2), updated_at = $2
	WHERE id = $1
`

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
	`, probedomain.CapabilityCGVCatalogCapture, retryCutoff).Scan(&required)
	if err != nil {
		return false, fmt.Errorf("inspect catalog refresh requirement: %w", err)
	}
	return required, nil
}

func (store *cycleStore) SeatMapBackfillTarget(
	ctx context.Context,
	now time.Time,
) (*reconcile.SeatMapBackfillTarget, error) {
	if err := refreshSeatMapCollectionShowtimeTargetsTx(ctx, store.tx, now); err != nil {
		return nil, fmt.Errorf("refresh seat-map collection showtime targets: %w", err)
	}

	target := &reconcile.SeatMapBackfillTarget{}
	var theaterID, theaterProviderID, theaterSourceKey, theaterRegion, theaterName string
	var auditoriumID, auditoriumTheaterID, auditoriumSourceKey, auditoriumName string
	var auditoriumScreenTypes []string
	var auditoriumCapacity int32
	var layoutHash string
	var triggerKind, showtimeID, showtimeProviderID, showtimeSourceKey, scheduleDate string
	var showtimeStartsAt, showtimeEndsAt time.Time
	var movieID, movieProviderID, movieSourceKey, movieTitle, moviePosterURL string
	err := store.tx.QueryRow(ctx, `
		SELECT theater.id, theater.provider_id, theater.source_key, theater.region, theater.name,
			auditorium.id, auditorium.theater_id, auditorium.source_key, auditorium.name,
			auditorium.screen_types, auditorium.capacity, COALESCE(version.layout_hash, ''),
			state.trigger_kind, state.showtime_id,
			showtime.provider_id, showtime.source_key, showtime.schedule_date::text,
			showtime.starts_at, showtime.ends_at,
			movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url
		FROM seat_map_collection_states AS state
		JOIN auditoriums AS auditorium ON auditorium.id = state.auditorium_id
		JOIN theaters AS theater ON theater.id = auditorium.theater_id AND theater.active
		JOIN showtimes AS showtime ON showtime.id = state.showtime_id
			AND showtime.auditorium_id = auditorium.id AND showtime.active AND showtime.starts_at > $1
		JOIN movies AS movie ON movie.id = showtime.movie_id AND movie.active
		LEFT JOIN seat_map_versions AS version ON version.id = auditorium.current_seat_map_version_id
		WHERE auditorium.active
			AND (state.state = 'queued' OR (state.state = 'retry_scheduled' AND state.next_attempt_at <= $1))
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS assignment
				WHERE assignment.task_kind = $2
					AND assignment.status IN ('queued', 'leased', 'retry_pending')
					AND assignment.auditorium_id = auditorium.id
			)
		ORDER BY state.priority DESC, state.requested_at, state.auditorium_id
		FOR UPDATE OF state, auditorium SKIP LOCKED
		LIMIT 1
	`, now, probedomain.CapabilityCGVSeatMapCapture).Scan(
		&theaterID, &theaterProviderID, &theaterSourceKey, &theaterRegion, &theaterName,
		&auditoriumID, &auditoriumTheaterID, &auditoriumSourceKey, &auditoriumName,
		&auditoriumScreenTypes, &auditoriumCapacity,
		&layoutHash, &triggerKind, &showtimeID,
		&showtimeProviderID, &showtimeSourceKey, &scheduleDate, &showtimeStartsAt, &showtimeEndsAt,
		&movieID, &movieProviderID, &movieSourceKey, &movieTitle, &moviePosterURL,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read seat-map backfill target: %w", err)
	}
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(theaterProviderID)
	if !catalogdomain.SetTheaterSourceKey(theater, theaterSourceKey) {
		return nil, fmt.Errorf("stored theater identity %q is not typed CGV", theaterSourceKey)
	}
	theater.SetRegion(theaterRegion)
	theater.SetName(theaterName)
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(auditoriumTheaterID)
	if !catalogdomain.SetAuditoriumSourceKey(auditorium, auditoriumSourceKey) {
		return nil, fmt.Errorf("stored auditorium identity %q is not typed CGV", auditoriumSourceKey)
	}
	auditorium.SetName(auditoriumName)
	auditorium.SetScreenTypes(auditoriumScreenTypes)
	auditorium.SetCapacity(auditoriumCapacity)
	auditorium.SetCurrentLayoutHash(layoutHash)
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(movieProviderID)
	if !catalogdomain.SetMovieSourceKey(movie, movieSourceKey) {
		return nil, fmt.Errorf("stored movie identity %q is not typed CGV", movieSourceKey)
	}
	movie.SetTitle(movieTitle)
	movie.SetPosterUrl(moviePosterURL)
	showtime := catalogpb.Showtime_builder{
		Movie: movie, Auditorium: auditorium,
		StartsAt: timestamppb.New(showtimeStartsAt), EndsAt: timestamppb.New(showtimeEndsAt),
	}.Build()
	showtime.SetId(showtimeID)
	showtime.SetProviderId(showtimeProviderID)
	if !catalogdomain.SetShowtimeSourceKey(showtime, showtimeSourceKey) {
		return nil, fmt.Errorf("stored showtime identity %q is not typed CGV", showtimeSourceKey)
	}
	showtime.SetTheaterId(theaterID)
	showtime.SetCapacity(auditoriumCapacity)
	parsedDate, err := time.Parse(time.DateOnly, scheduleDate)
	if err != nil {
		return nil, fmt.Errorf("parse seat-map target date: %w", err)
	}
	seatMap := &observationpb.SeatMapTask{}
	seatMap.SetTheater(theater)
	seatMap.SetAuditorium(auditorium)
	seatMap.SetShowtime(showtime)
	seatMap.SetTargetDates([]*commonpb.LocalDate{catalogLocalDate(parsedDate)})
	seatMap.SetLocale("ko-KR")
	seatMap.SetTimeZone("Asia/Seoul")
	task := &observationpb.AssignmentTask{}
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task.SetEgress(egress)
	task.SetSeatMap(seatMap)
	target.Task = task
	target.Requested = triggerKind == seatMapTriggerClientRequest || triggerKind == seatMapTriggerOperatorRequest
	return target, nil
}

// SeatAvailabilityTarget returns one due exact showtime shared by every
// matching active monitor. Completion time, not assignment creation time,
// drives the deterministic 2-5 second cadence.
func (store *cycleStore) SeatAvailabilityTarget(
	ctx context.Context,
	now time.Time,
) (*reconcile.SeatAvailabilityTarget, error) {
	rows, err := store.tx.Query(ctx, `
		SELECT theater.id, theater.provider_id, theater.source_key, theater.region, theater.name,
			showtime.id, showtime.provider_id, showtime.source_key, showtime.schedule_date::text,
			showtime.starts_at, showtime.ends_at,
			movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url,
			auditorium.id, auditorium.theater_id, auditorium.source_key, auditorium.name,
			auditorium.screen_types, auditorium.capacity,
			COALESCE(version.layout_hash, '')
		FROM showtimes AS showtime
		JOIN theaters AS theater ON theater.id = showtime.theater_id AND theater.active
		JOIN movies AS movie ON movie.id = showtime.movie_id AND movie.active
		JOIN auditoriums AS auditorium ON auditorium.id = showtime.auditorium_id AND auditorium.active
		LEFT JOIN seat_map_versions AS version ON version.id = auditorium.current_seat_map_version_id
		LEFT JOIN LATERAL (
			SELECT assignment.id, assignment.finished_at
			FROM observation_assignments AS assignment
			WHERE assignment.task_kind = $2 AND assignment.showtime_id = showtime.id
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
					ON preset.user_id = monitor.user_id AND preset.id = monitor.preset_id
				JOIN client_resources AS preset_resource
					ON preset_resource.user_id = preset.user_id
					AND preset_resource.kind = 'presets'
					AND preset_resource.id = preset.id
					AND preset_resource.deleted_at IS NULL
				WHERE monitor.state IN ('pending', 'running')
					AND monitor.movie_id = showtime.movie_id
					AND preset.auditorium_id = showtime.auditorium_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS active
				WHERE active.task_kind = $2 AND active.showtime_id = showtime.id
					AND active.status IN ('queued', 'leased', 'retry_pending')
			)
			AND (
				latest.finished_at IS NULL
				OR latest.finished_at + make_interval(
					secs => 2 + mod(hashtext(latest.id)::bigint + 2147483648, 4)::integer
				) <= $1
			)
		ORDER BY latest.finished_at NULLS FIRST, showtime.starts_at, showtime.id
		LIMIT 100
	`, now, probedomain.CapabilityCGVSeatAvailabilityCapture, catalogdomain.ProviderCGV)
	if err != nil {
		return nil, fmt.Errorf("query exact-showtime availability targets: %w", err)
	}
	defer rows.Close()
	type seatAvailabilityCandidate struct {
		theater  *catalogpb.Theater
		showtime *catalogpb.Showtime
	}
	candidates := make([]seatAvailabilityCandidate, 0)
	for rows.Next() {
		theater, showtime, err := scanSeatAvailabilityTarget(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, seatAvailabilityCandidate{theater: theater, showtime: showtime})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact-showtime availability targets: %w", err)
	}
	rows.Close()

	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return nil, fmt.Errorf("load exact-showtime matching location: %w", err)
	}
	targetsByTheater := make(map[string][]executionTarget)
	for _, candidate := range candidates {
		theater := candidate.theater
		showtime := candidate.showtime
		targets, loaded := targetsByTheater[theater.GetId()]
		if !loaded {
			targets, err = loadExecutionTargets(ctx, store.tx, theater.GetId())
			if err != nil {
				return nil, err
			}
			targetsByTheater[theater.GetId()] = targets
		}
		matched := false
		for _, target := range targets {
			if executionTargetMatches(target, showtime, now, location) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		availability := &observationpb.SeatAvailabilityTask{}
		availability.SetTheater(theater)
		availability.SetAuditorium(showtime.GetAuditorium())
		availability.SetShowtime(showtime)
		availability.SetLocale("ko-KR")
		availability.SetTimeZone("Asia/Seoul")
		task := &observationpb.AssignmentTask{}
		task.SetEgress(managedAssignmentEgress())
		task.SetSeatAvailability(availability)
		return &reconcile.SeatAvailabilityTarget{Task: task}, nil
	}
	return nil, nil
}

func scanSeatAvailabilityTarget(row pgx.Row) (*catalogpb.Theater, *catalogpb.Showtime, error) {
	var theaterID, theaterProviderID, theaterSourceKey, theaterRegion, theaterName string
	var showtimeID, showtimeProviderID, showtimeSourceKey, scheduleDate string
	var movieID, movieProviderID, movieSourceKey, movieTitle, moviePosterURL string
	var auditoriumID, auditoriumTheaterID, auditoriumSourceKey, auditoriumName, layoutHash string
	var screenTypes []string
	var auditoriumCapacity int32
	var startsAt, endsAt time.Time
	if err := row.Scan(
		&theaterID, &theaterProviderID, &theaterSourceKey, &theaterRegion, &theaterName,
		&showtimeID, &showtimeProviderID, &showtimeSourceKey, &scheduleDate, &startsAt, &endsAt,
		&movieID, &movieProviderID, &movieSourceKey, &movieTitle, &moviePosterURL,
		&auditoriumID, &auditoriumTheaterID, &auditoriumSourceKey, &auditoriumName,
		&screenTypes, &auditoriumCapacity, &layoutHash,
	); err != nil {
		return nil, nil, fmt.Errorf("scan exact-showtime availability target: %w", err)
	}
	if _, err := time.Parse(time.DateOnly, scheduleDate); err != nil {
		return nil, nil, fmt.Errorf("parse exact-showtime schedule date: %w", err)
	}
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(theaterProviderID)
	if !catalogdomain.SetTheaterSourceKey(theater, theaterSourceKey) {
		return nil, nil, fmt.Errorf("stored theater identity %q is not typed CGV", theaterSourceKey)
	}
	theater.SetRegion(theaterRegion)
	theater.SetName(theaterName)
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(movieProviderID)
	if !catalogdomain.SetMovieSourceKey(movie, movieSourceKey) {
		return nil, nil, fmt.Errorf("stored movie identity %q is not typed CGV", movieSourceKey)
	}
	movie.SetTitle(movieTitle)
	movie.SetPosterUrl(moviePosterURL)
	auditorium := catalogpb.Auditorium_builder{ScreenTypes: screenTypes}.Build()
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(auditoriumTheaterID)
	if !catalogdomain.SetAuditoriumSourceKey(auditorium, auditoriumSourceKey) {
		return nil, nil, fmt.Errorf("stored auditorium identity %q is not typed CGV", auditoriumSourceKey)
	}
	auditorium.SetName(auditoriumName)
	auditorium.SetCapacity(auditoriumCapacity)
	auditorium.SetCurrentLayoutHash(layoutHash)
	showtime := catalogpb.Showtime_builder{
		Movie: movie, Auditorium: auditorium,
		StartsAt: timestamppb.New(startsAt), EndsAt: timestamppb.New(endsAt),
	}.Build()
	showtime.SetId(showtimeID)
	showtime.SetProviderId(showtimeProviderID)
	if !catalogdomain.SetShowtimeSourceKey(showtime, showtimeSourceKey) {
		return nil, nil, fmt.Errorf("stored showtime identity %q is not typed CGV", showtimeSourceKey)
	}
	showtime.SetTheaterId(theaterID)
	showtime.SetCapacity(auditoriumCapacity)
	return theater, showtime, nil
}

func (store *cycleStore) CreateAssignment(ctx context.Context, assignment reconcile.NewAssignment) error {
	targetDates, err := assignmentTargetDates(assignment.Task)
	if err != nil {
		return err
	}
	taskData, err := marshalAssignmentTask(assignment)
	if err != nil {
		return fmt.Errorf("encode observation assignment task: %w", err)
	}
	if err := store.insertAssignment(ctx, assignment, targetDates, taskData); err != nil {
		return err
	}
	if err := store.insertEligibleProbes(ctx, assignment); err != nil {
		return err
	}
	if assignment.Task.GetSeatMap() != nil {
		if err := markSeatMapCollectionCollectingTx(
			ctx, store.tx, assignmentTaskAuditoriumID(assignment.Task), assignment.ID, assignment.CreatedAt,
		); err != nil {
			return fmt.Errorf("mark seat-map collection collecting: %w", err)
		}
	}
	if err := store.activateAssignmentPolicy(ctx, assignment); err != nil {
		return err
	}
	return store.notifyQueuedAssignment(ctx, assignment)
}

func (store *cycleStore) insertAssignment(
	ctx context.Context,
	assignment reconcile.NewAssignment,
	targetDates []time.Time,
	taskData []byte,
) error {
	target := assignmentTaskTarget(assignment.Task)
	var insertedID string
	err := store.tx.QueryRow(ctx, `
		INSERT INTO observation_assignments (
			id, policy_id, task_kind, auditorium_id, showtime_id, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, priority, status, not_before, deadline,
			terminal_reason, finished_at, created_at, updated_at, task_data, lane, hot_target_fingerprint
		) VALUES (
			$1, NULLIF($2, ''), $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $21, $22, $23, $24
		)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, assignment.ID, assignment.PolicyID, assignmentTaskKind(assignment.Task), assignmentTaskAuditoriumID(assignment.Task), assignmentTaskShowtimeID(assignment.Task), target.TheaterID,
		target.ProviderID, target.TheaterSourceKey, target.TheaterRegion, target.TheaterName, targetDates, assignmentTaskLocale(assignment.Task),
		assignmentTaskTimeZone(assignment.Task), string(central.EgressPolicyScanDefault), assignment.Priority, assignment.Status,
		assignment.NotBefore, assignment.Deadline, assignment.ReasonCode, nullableTime(assignment.FinishedAt),
		assignment.CreatedAt, taskData, assignment.Lane, assignment.HotTargetFingerprint).Scan(&insertedID)
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
	return nil
}

func (store *cycleStore) insertEligibleProbes(
	ctx context.Context,
	assignment reconcile.NewAssignment,
) error {
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
	return nil
}

func (store *cycleStore) activateAssignmentPolicy(
	ctx context.Context,
	assignment reconcile.NewAssignment,
) error {
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
	if err := expectOneRow(tag.RowsAffected(), "activate observation policy assignment"); err != nil {
		return err
	}
	return nil
}

func (store *cycleStore) notifyQueuedAssignment(
	ctx context.Context,
	assignment reconcile.NewAssignment,
) error {
	if assignment.Status == "queued" {
		return notifyAssignmentAvailability(ctx, store.tx)
	}
	return nil
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
	var minimumSeconds, maximumSeconds, executionWindowSeconds, baselineMaximumSeconds int
	var lastFinishedAt *time.Time
	var lastHotFinishedAt, lastBaselineFinishedAt *time.Time
	var lastHotTargetDates []string
	var lastHotTargetFingerprint *string
	var lastBaselineTargetDate *string
	var theaterID, providerID, sourceKey, region, name string
	err := row.Scan(
		&policy.ID, &policy.Enabled, &policy.TaskKind, &theaterID,
		&providerID, &sourceKey, &region,
		&name, &policy.TargetDateMode, &policy.TargetDates, &horizonDays,
		&policy.Locale, &policy.TimeZone, &policy.EgressPolicyID, &policy.Priority,
		&minimumSeconds, &maximumSeconds, &executionWindowSeconds, &baselineMaximumSeconds,
		&policy.NextRunAt, &lastFinishedAt, &policy.LastOutcome,
		&lastHotFinishedAt, &lastHotTargetDates, &lastHotTargetFingerprint,
		&lastBaselineFinishedAt, &lastBaselineTargetDate,
	)
	if err != nil {
		return reconcile.Policy{}, fmt.Errorf("scan observation policy: %w", err)
	}
	policy.Theater = &catalogpb.Theater{}
	policy.Theater.SetId(theaterID)
	policy.Theater.SetProviderId(providerID)
	if !catalogdomain.SetTheaterSourceKey(policy.Theater, sourceKey) {
		return reconcile.Policy{}, fmt.Errorf("stored theater identity %q is not typed CGV", sourceKey)
	}
	policy.Theater.SetRegion(region)
	policy.Theater.SetName(name)
	if horizonDays != nil {
		policy.HorizonDays = *horizonDays
	}
	if lastFinishedAt != nil {
		policy.LastFinishedAt = *lastFinishedAt
	}
	if lastHotFinishedAt != nil {
		policy.LastHotFinishedAt = *lastHotFinishedAt
	}
	policy.LastHotTargetDates = lastHotTargetDates
	if lastHotTargetFingerprint != nil {
		policy.LastHotTargetFingerprint = *lastHotTargetFingerprint
	}
	if lastBaselineFinishedAt != nil {
		policy.LastBaselineFinishedAt = *lastBaselineFinishedAt
	}
	if lastBaselineTargetDate != nil {
		policy.LastBaselineTargetDate = *lastBaselineTargetDate
	}
	policy.MinimumInterval = time.Duration(minimumSeconds) * time.Second
	policy.MaximumInterval = time.Duration(maximumSeconds) * time.Second
	policy.BaselineMaximumInterval = time.Duration(baselineMaximumSeconds) * time.Second
	policy.ExecutionWindow = time.Duration(executionWindowSeconds) * time.Second
	return policy, nil
}

// loadDuePolicyMonitorTargets reads active monitor demand from normalized
// columns. Keeping this as typed rows avoids rebuilding a JSON projection just
// to feed the planner's in-process model.
func loadDuePolicyMonitorTargets(
	ctx context.Context,
	queryer clientResourceQueryer,
	theaterID string,
) ([]planning.MonitorTarget, error) {
	rows, err := queryer.Query(ctx, `
		SELECT monitor.state, monitor.search_horizon_days,
			COALESCE(ARRAY(
				SELECT target_date::text
				FROM client_monitor_target_dates AS target_date
				WHERE target_date.user_id = monitor.user_id
					AND target_date.monitor_id = monitor.id
				ORDER BY target_date.position
			), ARRAY[]::text[]),
			COALESCE(ARRAY(
				SELECT target_weekday::integer
				FROM client_monitor_target_weekdays AS target_weekday
				WHERE target_weekday.user_id = monitor.user_id
					AND target_weekday.monitor_id = monitor.id
				ORDER BY target_weekday.position
			), ARRAY[]::integer[])
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
			AND preset.theater_id = $1
		ORDER BY monitor.id
	`, theaterID)
	if err != nil {
		return nil, fmt.Errorf("query active Client monitor targets: %w", err)
	}
	defer rows.Close()
	targets := make([]planning.MonitorTarget, 0)
	for rows.Next() {
		var state string
		var searchHorizonDays int
		var targetDates []string
		var targetWeekdays []int32
		if err := rows.Scan(&state, &searchHorizonDays, &targetDates, &targetWeekdays); err != nil {
			return nil, fmt.Errorf("scan active Client monitor target: %w", err)
		}
		switch state {
		case "pending", "running":
		case "triggered", "booked", "failed", "stopped", "payment-unknown":
			continue
		default:
			return nil, fmt.Errorf("unknown normalized Client monitor state %q", state)
		}
		weekdays := make([]int, len(targetWeekdays))
		for i, weekday := range targetWeekdays {
			weekdays[i] = int(weekday)
		}
		targets = append(targets, planning.MonitorTarget{
			TargetDates:       targetDates,
			TargetWeekdays:    weekdays,
			SearchHorizonDays: searchHorizonDays,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active Client monitor targets: %w", err)
	}
	return targets, nil
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func marshalAssignmentTask(assignment reconcile.NewAssignment) ([]byte, error) {
	return protojson.MarshalOptions{UseProtoNames: false}.Marshal(assignment.Task)
}

func assignmentTaskKind(task *observationpb.AssignmentTask) string {
	switch {
	case task.GetSchedule() != nil:
		return probedomain.CapabilityCGVScheduleCapture
	case task.GetCatalog() != nil:
		return probedomain.CapabilityCGVCatalogCapture
	case task.GetSeatMap() != nil:
		return probedomain.CapabilityCGVSeatMapCapture
	case task.GetSeatAvailability() != nil:
		return probedomain.CapabilityCGVSeatAvailabilityCapture
	default:
		return ""
	}
}

func assignmentTaskTheater(task *observationpb.AssignmentTask) *catalogpb.Theater {
	if task.GetSchedule() != nil {
		return task.GetSchedule().GetTheater()
	}
	if task.GetSeatMap() != nil {
		return task.GetSeatMap().GetTheater()
	}
	if task.GetSeatAvailability() != nil {
		return task.GetSeatAvailability().GetTheater()
	}
	return nil
}

type assignmentTarget struct {
	ProviderID       string
	TheaterID        string
	TheaterSourceKey string
	TheaterRegion    string
	TheaterName      string
}

// assignmentTaskTarget projects the typed task target into normalized assignment columns.
// Catalog capture is provider-global, so its theater columns deliberately remain empty.
func assignmentTaskTarget(task *observationpb.AssignmentTask) assignmentTarget {
	if catalog := task.GetCatalog(); catalog != nil {
		return assignmentTarget{ProviderID: catalog.GetProviderId()}
	}
	theater := assignmentTaskTheater(task)
	if theater == nil {
		return assignmentTarget{}
	}
	return assignmentTarget{
		ProviderID:       theater.GetProviderId(),
		TheaterID:        theater.GetId(),
		TheaterSourceKey: catalogTheaterSourceKey(theater),
		TheaterRegion:    theater.GetRegion(),
		TheaterName:      theater.GetName(),
	}
}

func assignmentTaskAuditoriumID(task *observationpb.AssignmentTask) string {
	if task.GetSeatMap() != nil && task.GetSeatMap().GetAuditorium() != nil {
		return task.GetSeatMap().GetAuditorium().GetId()
	}
	if task.GetSeatAvailability() != nil && task.GetSeatAvailability().GetAuditorium() != nil {
		return task.GetSeatAvailability().GetAuditorium().GetId()
	}
	return ""
}

func assignmentTaskShowtimeID(task *observationpb.AssignmentTask) string {
	if task.GetSeatAvailability() == nil || task.GetSeatAvailability().GetShowtime() == nil {
		return ""
	}
	return task.GetSeatAvailability().GetShowtime().GetId()
}

func assignmentTaskLocale(task *observationpb.AssignmentTask) string {
	if task.GetSchedule() != nil {
		return task.GetSchedule().GetLocale()
	}
	if task.GetCatalog() != nil {
		return task.GetCatalog().GetLocale()
	}
	if task.GetSeatMap() != nil {
		return task.GetSeatMap().GetLocale()
	}
	return task.GetSeatAvailability().GetLocale()
}

func assignmentTaskTimeZone(task *observationpb.AssignmentTask) string {
	if task.GetSchedule() != nil {
		return task.GetSchedule().GetTimeZone()
	}
	if task.GetCatalog() != nil {
		return task.GetCatalog().GetTimeZone()
	}
	if task.GetSeatMap() != nil {
		return task.GetSeatMap().GetTimeZone()
	}
	return task.GetSeatAvailability().GetTimeZone()
}

func assignmentTargetDates(task *observationpb.AssignmentTask) ([]time.Time, error) {
	var values []*commonpb.LocalDate
	switch {
	case task.GetSchedule() != nil:
		values = task.GetSchedule().GetTargetDates()
	case task.GetSeatMap() != nil:
		values = task.GetSeatMap().GetTargetDates()
	case task.GetSeatAvailability() != nil && task.GetSeatAvailability().GetShowtime() != nil:
		key, ok := catalogdomain.ShowtimeSourceKey(task.GetSeatAvailability().GetShowtime())
		if !ok || len(key) < 15 {
			return nil, fmt.Errorf("seat-availability showtime identity is not typed CGV")
		}
		date, err := time.Parse(time.DateOnly, key[5:15])
		if err != nil {
			return nil, fmt.Errorf("seat-availability showtime date is invalid: %w", err)
		}
		values = []*commonpb.LocalDate{catalogLocalDate(date)}
	}
	dates := make([]time.Time, 0, len(values))
	for _, value := range values {
		date := time.Date(int(value.GetYear()), time.Month(value.GetMonth()), int(value.GetDay()), 0, 0, 0, 0, time.UTC)
		if date.Year() != int(value.GetYear()) || int(date.Month()) != int(value.GetMonth()) || date.Day() != int(value.GetDay()) {
			return nil, fmt.Errorf("assignment target date is invalid")
		}
		dates = append(dates, date)
	}
	return dates, nil
}

func managedAssignmentEgress() *commonpb.EgressPolicy {
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	return egress
}

// seatMapBackfillDates returns the bounded provider window used to find any
// currently bookable showtime for a missing auditorium layout.
func seatMapBackfillDates(now time.Time) []*commonpb.LocalDate {
	start := now.In(seatMapBackfillLocation)
	dates := make([]*commonpb.LocalDate, 0, seatMapBackfillHorizonDays)
	for offset := range seatMapBackfillHorizonDays {
		value := start.AddDate(0, 0, offset)
		date := &commonpb.LocalDate{}
		date.SetYear(numeric.ClampInt32(value.Year()))
		date.SetMonth(numeric.ClampInt32(int(value.Month())))
		date.SetDay(numeric.ClampInt32(value.Day()))
		dates = append(dates, date)
	}
	return dates
}

func expectOneRow(affected int64, operation string) error {
	if affected != 1 {
		return fmt.Errorf("%s: expected one row, updated %d", operation, affected)
	}
	return nil
}
