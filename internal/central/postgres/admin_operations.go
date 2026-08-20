package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	centralapi "github.com/cineko-org/central/internal/central/api"
	contracts "github.com/cineko-org/contracts/v3"

	"github.com/jackc/pgx/v5"
)

const adminObservationExecutionWindow = 10 * time.Minute

func (store *Store) ListAdminProbes(ctx context.Context) ([]centralapi.AdminProbe, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, kind, COALESCE(owner_user_id, ''), network_id, runtime_version, browser_revision,
			platform, architecture, status, draining, available_slots, max_concurrency,
			health, reason_code, last_heartbeat_at, updated_at
		FROM probe_runtimes
		ORDER BY status = 'online' DESC, last_heartbeat_at DESC NULLS LAST, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list admin probes: %w", err)
	}
	defer rows.Close()
	probes := make([]centralapi.AdminProbe, 0)
	for rows.Next() {
		var probe centralapi.AdminProbe
		if err := rows.Scan(
			&probe.ID, &probe.Kind, &probe.OwnerUserID, &probe.NetworkID, &probe.RuntimeVersion,
			&probe.BrowserRevision, &probe.Platform, &probe.Arch, &probe.Status, &probe.Draining,
			&probe.AvailableSlots, &probe.MaxConcurrency, &probe.Health, &probe.ReasonCode,
			&probe.LastHeartbeatAt, &probe.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin probe: %w", err)
		}
		probes = append(probes, probe)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin probes: %w", err)
	}
	return probes, nil
}

func (store *Store) DeleteAdminProbe(ctx context.Context, probeID string) error {
	tag, err := store.pool.Exec(ctx, `
		DELETE FROM probe_runtimes AS probe
		WHERE probe.id = $1
			AND probe.status = 'offline'
			AND NOT EXISTS (
				SELECT 1 FROM observation_assignments AS assignment
				WHERE assignment.probe_id = probe.id AND assignment.status = 'leased'
			)
	`, probeID)
	if err != nil {
		return fmt.Errorf("delete admin probe: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM probe_runtimes WHERE id = $1)`, probeID).Scan(&exists); err != nil {
		return fmt.Errorf("check admin probe: %w", err)
	}
	if !exists {
		return central.ErrNotFound
	}
	return central.ErrConflict
}

func (store *Store) AdminDataSummary(ctx context.Context) (centralapi.AdminDataSummary, error) {
	var summary centralapi.AdminDataSummary
	err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM providers),
			(SELECT count(*) FROM theaters WHERE active),
			(SELECT count(*) FROM auditoriums WHERE active),
			(SELECT count(*) FROM movies WHERE active),
			(SELECT count(*) FROM showtimes WHERE active),
			(SELECT count(*) FROM seat_map_versions),
			(SELECT count(*) FROM schedule_captures),
			(SELECT count(*) FROM showtime_observations),
			(SELECT count(*) FROM observation_policies WHERE deleted_at IS NULL),
			(SELECT count(*) FROM observation_policies WHERE enabled AND deleted_at IS NULL),
			(SELECT count(*) FROM observation_assignments WHERE status = 'queued'),
			(SELECT count(*) FROM observation_assignments WHERE status = 'leased'),
			(SELECT count(*) FROM observation_assignments WHERE status IN ('completed', 'partial')),
			(SELECT count(*) FROM observation_assignments WHERE status IN ('failed', 'missed')),
			(SELECT max(observed_at) FROM schedule_captures)
	`).Scan(
		&summary.Providers, &summary.Theaters, &summary.Auditoriums,
		&summary.Movies, &summary.Showtimes, &summary.SeatMapVersions,
		&summary.ScheduleCaptures, &summary.ShowtimeObservations, &summary.ObservationPolicies,
		&summary.ActiveObservationPolicies, &summary.QueuedAssignments, &summary.LeasedAssignments,
		&summary.CompletedAssignments, &summary.FailedAssignments, &summary.LatestScheduleObservedAt,
	)
	if err != nil {
		return centralapi.AdminDataSummary{}, fmt.Errorf("summarize admin data: %w", err)
	}
	return summary, nil
}

func (store *Store) ListAdminObservationPolicies(
	ctx context.Context,
) ([]centralapi.AdminObservationPolicy, error) {
	rows, err := store.pool.Query(ctx, adminObservationPolicySelect+`
		ORDER BY policy.enabled DESC, effective_priority DESC, policy.display_name, policy.id
	`, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list observation policies: %w", err)
	}
	defer rows.Close()
	policies := make([]centralapi.AdminObservationPolicy, 0)
	for rows.Next() {
		policy, err := scanAdminObservationPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation policies: %w", err)
	}
	return policies, nil
}

func (store *Store) CreateAdminObservationPolicy(
	ctx context.Context,
	input centralapi.AdminObservationPolicyInput,
) (centralapi.AdminObservationPolicy, error) {
	now := time.Now().UTC()
	theater, err := store.adminCatalogTheater(ctx, input.TheaterID)
	if err != nil {
		return centralapi.AdminObservationPolicy{}, err
	}
	id := adminObservationPolicyID(theater.ID)
	var insertedID string
	err = store.pool.QueryRow(ctx, `
		INSERT INTO observation_policies (
			id, display_name, enabled, revision, task_kind, theater_id,
			theater_provider_id, theater_source_key, theater_region, theater_name,
			target_date_mode, target_dates, horizon_days, locale, time_zone, egress_policy_id,
			priority, min_interval_seconds, max_interval_seconds,
			demand_min_interval_seconds, demand_max_interval_seconds,
			burst_min_interval_seconds, burst_max_interval_seconds, burst_duration_seconds,
			execution_window_seconds, next_run_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 1, $4, $5, $6, $7, $8, $9, 'rolling', '{}', $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20, $21, $22,
			CASE WHEN $3 THEN $23::timestamptz ELSE NULL::timestamptz END,
			$23::timestamptz, $23::timestamptz
		)
		ON CONFLICT (id) DO UPDATE SET
			display_name = EXCLUDED.display_name, enabled = EXCLUDED.enabled,
			revision = observation_policies.revision + 1,
			theater_provider_id = EXCLUDED.theater_provider_id,
			theater_source_key = EXCLUDED.theater_source_key,
			theater_region = EXCLUDED.theater_region, theater_name = EXCLUDED.theater_name,
			horizon_days = EXCLUDED.horizon_days, locale = EXCLUDED.locale,
			time_zone = EXCLUDED.time_zone, egress_policy_id = EXCLUDED.egress_policy_id,
			priority = EXCLUDED.priority, min_interval_seconds = EXCLUDED.min_interval_seconds,
			max_interval_seconds = EXCLUDED.max_interval_seconds,
			demand_min_interval_seconds = EXCLUDED.demand_min_interval_seconds,
			demand_max_interval_seconds = EXCLUDED.demand_max_interval_seconds,
			burst_min_interval_seconds = EXCLUDED.burst_min_interval_seconds,
			burst_max_interval_seconds = EXCLUDED.burst_max_interval_seconds,
			burst_duration_seconds = EXCLUDED.burst_duration_seconds,
			burst_until = NULL, next_run_at = EXCLUDED.next_run_at,
			deleted_at = NULL, updated_at = EXCLUDED.updated_at
		WHERE observation_policies.deleted_at IS NOT NULL
		RETURNING id
	`, id, theater.Name, input.Enabled, contracts.CapabilityCGVScheduleCapture,
		theater.ID, theater.ProviderID, theater.SourceKey, theater.Region, theater.Name, input.HorizonDays,
		input.Locale, input.TimeZone, input.EgressPolicyID, input.Priority,
		input.BaselineMinSeconds, input.BaselineMaxSeconds,
		input.DemandMinSeconds, input.DemandMaxSeconds,
		input.BurstMinSeconds, input.BurstMaxSeconds, input.BurstDurationSeconds,
		int(adminObservationExecutionWindow/time.Second), now,
	).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return centralapi.AdminObservationPolicy{}, central.ErrConflict
	}
	if isConcurrentClientResourceCreate(err) {
		return centralapi.AdminObservationPolicy{}, central.ErrConflict
	}
	if err != nil {
		return centralapi.AdminObservationPolicy{}, fmt.Errorf("create observation policy: %w", err)
	}
	return store.adminObservationPolicy(ctx, insertedID, now)
}

func (store *Store) UpdateAdminObservationPolicy(
	ctx context.Context,
	id string,
	revision int64,
	input centralapi.AdminObservationPolicyInput,
) (centralapi.AdminObservationPolicy, error) {
	now := time.Now().UTC()
	theater, err := store.adminCatalogTheater(ctx, input.TheaterID)
	if err != nil {
		return centralapi.AdminObservationPolicy{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return centralapi.AdminObservationPolicy{}, fmt.Errorf("begin observation policy update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE observation_policies AS policy SET
			display_name = $3, enabled = $4, revision = revision + 1,
			theater_provider_id = $6, theater_source_key = $7,
			theater_region = $8, theater_name = $9, horizon_days = $10,
			priority = $11, min_interval_seconds = $12, max_interval_seconds = $13,
			demand_min_interval_seconds = $14, demand_max_interval_seconds = $15,
			burst_min_interval_seconds = $16, burst_max_interval_seconds = $17,
			burst_duration_seconds = $18, locale = $19, time_zone = $20,
			egress_policy_id = $21,
			next_run_at = CASE
				WHEN NOT $4 THEN NULL
				WHEN EXISTS (
					SELECT 1 FROM observation_assignments AS assignment
					WHERE assignment.policy_id = policy.id
						AND assignment.status IN ('queued', 'leased', 'retry_pending')
				) THEN NULL
				ELSE $22
			END,
			updated_at = $22
		WHERE policy.id = $1 AND policy.revision = $2 AND policy.theater_id = $5
			AND policy.deleted_at IS NULL
	`, strings.TrimSpace(id), revision, theater.Name, input.Enabled,
		theater.ID, theater.ProviderID, theater.SourceKey, theater.Region, theater.Name,
		input.HorizonDays, input.Priority,
		input.BaselineMinSeconds, input.BaselineMaxSeconds, input.DemandMinSeconds,
		input.DemandMaxSeconds, input.BurstMinSeconds, input.BurstMaxSeconds,
		input.BurstDurationSeconds, input.Locale, input.TimeZone, input.EgressPolicyID, now)
	if err != nil {
		return centralapi.AdminObservationPolicy{}, fmt.Errorf("update observation policy: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return centralapi.AdminObservationPolicy{}, central.ErrRevisionConflict
	}
	if !input.Enabled {
		if err := cancelActivePolicyAssignments(ctx, tx, id, "policy_disabled", now); err != nil {
			return centralapi.AdminObservationPolicy{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return centralapi.AdminObservationPolicy{}, fmt.Errorf("commit observation policy update: %w", err)
	}
	return store.adminObservationPolicy(ctx, id, now)
}

func (store *Store) DeleteAdminObservationPolicy(
	ctx context.Context,
	id string,
	revision int64,
) error {
	now := time.Now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin observation policy deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE observation_policies SET enabled = false, revision = revision + 1,
			next_run_at = NULL, deleted_at = $3, updated_at = $3
		WHERE id = $1 AND revision = $2 AND deleted_at IS NULL
	`, strings.TrimSpace(id), revision, now)
	if err != nil {
		return fmt.Errorf("delete observation policy: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrRevisionConflict
	}
	if err := cancelActivePolicyAssignments(ctx, tx, id, "policy_deleted", now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit observation policy deletion: %w", err)
	}
	return nil
}

func cancelActivePolicyAssignments(
	ctx context.Context,
	tx pgx.Tx,
	policyID string,
	reason string,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE observation_assignments SET
			status = 'missed', terminal_reason = $2, finished_at = $3, updated_at = $3,
			probe_id = NULL, lease_token_hash = NULL, lease_expires_at = NULL
		WHERE policy_id = $1 AND status IN ('queued', 'leased', 'retry_pending')
	`, strings.TrimSpace(policyID), reason, now); err != nil {
		return fmt.Errorf("cancel disabled policy assignments: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE assignment_attempts AS attempt
		SET status = 'failed', finished_at = $3, error_code = $2
		FROM observation_assignments AS assignment
		WHERE assignment.id = attempt.assignment_id AND assignment.policy_id = $1
			AND attempt.status = 'leased'
	`, strings.TrimSpace(policyID), reason, now); err != nil {
		return fmt.Errorf("finish disabled policy assignment attempts: %w", err)
	}
	return nil
}

func (store *Store) adminObservationPolicy(
	ctx context.Context,
	id string,
	now time.Time,
) (centralapi.AdminObservationPolicy, error) {
	policy, err := scanAdminObservationPolicy(store.pool.QueryRow(ctx, adminObservationPolicySelect+`
		AND policy.id = $2
	`, now, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return centralapi.AdminObservationPolicy{}, central.ErrNotFound
	}
	return policy, err
}

const adminObservationPolicySelect = `
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
	), effective_policies AS (
		SELECT policy.*, COALESCE(demand.opening_active OR demand.cancellation_active, false) AS demand_active,
			CASE
				WHEN COALESCE(demand.opening_active, false) THEN 'demand'
				WHEN policy.burst_until > $1 THEN 'burst'
				WHEN COALESCE(demand.cancellation_active, false) THEN 'cancellation'
				ELSE 'baseline'
			END AS effective_mode,
			policy.priority AS effective_priority,
			CASE
				WHEN COALESCE(demand.opening_active OR demand.cancellation_active, false)
					THEN policy.demand_min_interval_seconds
				WHEN policy.burst_until > $1 THEN policy.burst_min_interval_seconds
				ELSE policy.min_interval_seconds
			END AS effective_min_seconds,
			CASE
				WHEN COALESCE(demand.opening_active OR demand.cancellation_active, false)
					THEN policy.demand_max_interval_seconds
				WHEN policy.burst_until > $1 THEN policy.burst_max_interval_seconds
				ELSE policy.max_interval_seconds
			END AS effective_max_seconds
		FROM observation_policies AS policy
		LEFT JOIN demand_theaters AS demand ON demand.theater_id = policy.theater_id
		WHERE policy.deleted_at IS NULL
	)
	SELECT policy.id, policy.revision, policy.enabled,
		theater.id, theater.provider_id, theater.source_key, theater.region, theater.name,
		LEAST(policy.horizon_days, 14), policy.priority,
		policy.min_interval_seconds, policy.max_interval_seconds,
		policy.demand_min_interval_seconds, policy.demand_max_interval_seconds,
		policy.burst_min_interval_seconds, policy.burst_max_interval_seconds,
		policy.burst_duration_seconds, policy.locale, policy.time_zone, policy.egress_policy_id,
		policy.effective_mode, policy.effective_priority,
		policy.effective_min_seconds, policy.effective_max_seconds,
		policy.demand_active, policy.burst_until, policy.next_run_at,
		policy.last_finished_at, COALESCE(policy.last_outcome, ''), policy.last_error_code,
		policy.created_at, policy.updated_at
	FROM effective_policies AS policy
	JOIN theaters AS theater ON theater.id = policy.theater_id
	WHERE true
`

func scanAdminObservationPolicy(row rowScanner) (centralapi.AdminObservationPolicy, error) {
	var policy centralapi.AdminObservationPolicy
	err := row.Scan(
		&policy.ID, &policy.Revision, &policy.Enabled,
		&policy.Theater.ID, &policy.Theater.ProviderID, &policy.Theater.SourceKey,
		&policy.Theater.Region, &policy.Theater.Name,
		&policy.HorizonDays, &policy.Priority,
		&policy.BaselineMinSeconds, &policy.BaselineMaxSeconds,
		&policy.DemandMinSeconds, &policy.DemandMaxSeconds,
		&policy.BurstMinSeconds, &policy.BurstMaxSeconds, &policy.BurstDurationSeconds,
		&policy.Locale, &policy.TimeZone, &policy.EgressPolicyID,
		&policy.EffectiveMode, &policy.EffectivePriority,
		&policy.EffectiveMinSeconds, &policy.EffectiveMaxSeconds,
		&policy.DemandActive, &policy.BurstUntil, &policy.NextRunAt,
		&policy.LastFinishedAt, &policy.LastOutcome, &policy.LastErrorCode,
		&policy.CreatedAt, &policy.UpdatedAt,
	)
	if err != nil {
		return centralapi.AdminObservationPolicy{}, fmt.Errorf("scan observation policy: %w", err)
	}
	return policy, nil
}

func (store *Store) adminCatalogTheater(ctx context.Context, id string) (contracts.Theater, error) {
	var theater contracts.Theater
	err := store.pool.QueryRow(ctx, `
		SELECT id, provider_id, source_key, region, name
		FROM theaters WHERE id = $1 AND active
	`, strings.TrimSpace(id)).Scan(
		&theater.ID, &theater.ProviderID, &theater.SourceKey, &theater.Region, &theater.Name,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Theater{}, central.ErrNotFound
	}
	if err != nil {
		return contracts.Theater{}, fmt.Errorf("read observation policy theater: %w", err)
	}
	return theater, nil
}

func adminObservationPolicyID(theaterID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(theaterID)))
	return "policy_" + hex.EncodeToString(digest[:12])
}
