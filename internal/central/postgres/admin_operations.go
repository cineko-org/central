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
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	"github.com/cineko-org/central/internal/observation/planning"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (store *Store) ListAdminProbes(ctx context.Context) ([]*adminpb.Probe, error) {
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
	probes := make([]*adminpb.Probe, 0)
	for rows.Next() {
		var id, kind, ownerUserID, networkID, runtimeVersion, browserRevision, platform, architecture string
		var status, health, reasonCode string
		var draining bool
		var availableSlots, maxConcurrency int32
		var lastHeartbeatAt *time.Time
		var updatedAt time.Time
		if err := rows.Scan(
			&id, &kind, &ownerUserID, &networkID, &runtimeVersion,
			&browserRevision, &platform, &architecture, &status, &draining,
			&availableSlots, &maxConcurrency, &health, &reasonCode,
			&lastHeartbeatAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin probe: %w", err)
		}
		probe, err := adminProbe(
			id, kind, ownerUserID, networkID, runtimeVersion, browserRevision, platform, architecture,
			status, draining, availableSlots, maxConcurrency, health, reasonCode, lastHeartbeatAt, updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("decode admin probe: %w", err)
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

func (store *Store) AdminDataSummary(ctx context.Context) (*adminpb.DataSummary, error) {
	var providers, theaters, auditoriums, movies, showtimes, seatMapVersions int64
	var scheduleCaptures, showtimeObservations, observationPolicies, activeObservationPolicies int64
	var queuedAssignments, leasedAssignments, completedAssignments, failedAssignments int64
	var latestScheduleObservedAt *time.Time
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
		&providers, &theaters, &auditoriums, &movies, &showtimes, &seatMapVersions,
		&scheduleCaptures, &showtimeObservations, &observationPolicies,
		&activeObservationPolicies, &queuedAssignments, &leasedAssignments,
		&completedAssignments, &failedAssignments, &latestScheduleObservedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("summarize admin data: %w", err)
	}
	summary := &adminpb.DataSummary{}
	summary.SetProviders(providers)
	summary.SetTheaters(theaters)
	summary.SetAuditoriums(auditoriums)
	summary.SetMovies(movies)
	summary.SetShowtimes(showtimes)
	summary.SetSeatMapVersions(seatMapVersions)
	summary.SetScheduleCaptures(scheduleCaptures)
	summary.SetShowtimeObservations(showtimeObservations)
	summary.SetObservationPolicies(observationPolicies)
	summary.SetActiveObservationPolicies(activeObservationPolicies)
	summary.SetQueuedAssignments(queuedAssignments)
	summary.SetLeasedAssignments(leasedAssignments)
	summary.SetCompletedAssignments(completedAssignments)
	summary.SetFailedAssignments(failedAssignments)
	if latestScheduleObservedAt != nil {
		summary.SetLatestScheduleObservedAt(timestamppb.New(*latestScheduleObservedAt))
	}
	return summary, nil
}

func (store *Store) ListAdminObservationPolicies(
	ctx context.Context,
) ([]*adminpb.ObservationPolicy, error) {
	rows, err := store.pool.Query(ctx, adminObservationPolicySelect+`
		ORDER BY policy.enabled DESC, effective_priority DESC, policy.display_name, policy.id
	`, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list observation policies: %w", err)
	}
	defer rows.Close()
	policies := make([]*adminpb.ObservationPolicy, 0)
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
	input *adminpb.ObservationPolicyInput,
) (*adminpb.ObservationPolicy, error) {
	now := time.Now().UTC()
	policy := planning.DefaultProductPolicy
	theater, err := store.adminCatalogTheater(ctx, input.GetTheaterId())
	if err != nil {
		return nil, err
	}
	id := adminObservationPolicyID(theater.GetId())
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
	`, id, theater.GetName(), input.GetEnabled(), probedomain.CapabilityCGVScheduleCapture,
		theater.GetId(), theater.GetProviderId(), theater.GetSourceKey(), theater.GetRegion(), theater.GetName(), input.GetHorizonDays(),
		"ko-KR", "Asia/Seoul", string(central.EgressPolicyScanDefault), policy.Priority,
		seconds32(policy.BaselineMinimum), seconds32(policy.BaselineMaximum),
		seconds32(policy.DemandMinimum), seconds32(policy.DemandMaximum),
		seconds32(policy.RecentMinimum), seconds32(policy.RecentMaximum), seconds32(policy.RecentDuration),
		seconds32(policy.ExecutionWindow), now,
	).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrConflict
	}
	if isConcurrentClientResourceCreate(err) {
		return nil, central.ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("create observation policy: %w", err)
	}
	return store.adminObservationPolicy(ctx, insertedID, now)
}

func (store *Store) UpdateAdminObservationPolicy(
	ctx context.Context,
	id string,
	revision int64,
	input *adminpb.ObservationPolicyInput,
) (*adminpb.ObservationPolicy, error) {
	now := time.Now().UTC()
	policy := planning.DefaultProductPolicy
	theater, err := store.adminCatalogTheater(ctx, input.GetTheaterId())
	if err != nil {
		return nil, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin observation policy update: %w", err)
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
				ELSE $22::timestamptz
			END,
			updated_at = $22::timestamptz
		WHERE policy.id = $1 AND policy.revision = $2 AND policy.theater_id = $5
			AND policy.deleted_at IS NULL
	`, strings.TrimSpace(id), revision, theater.GetName(), input.GetEnabled(),
		theater.GetId(), theater.GetProviderId(), theater.GetSourceKey(), theater.GetRegion(), theater.GetName(),
		input.GetHorizonDays(), policy.Priority,
		seconds32(policy.BaselineMinimum), seconds32(policy.BaselineMaximum),
		seconds32(policy.DemandMinimum), seconds32(policy.DemandMaximum),
		seconds32(policy.RecentMinimum), seconds32(policy.RecentMaximum),
		seconds32(policy.RecentDuration), "ko-KR", "Asia/Seoul", string(central.EgressPolicyScanDefault), now)
	if err != nil {
		return nil, fmt.Errorf("update observation policy: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, central.ErrRevisionConflict
	}
	if !input.GetEnabled() {
		if err := cancelActivePolicyAssignments(ctx, tx, id, "policy_disabled", now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit observation policy update: %w", err)
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
) (*adminpb.ObservationPolicy, error) {
	policy, err := scanAdminObservationPolicy(store.pool.QueryRow(ctx, adminObservationPolicySelect+`
		AND policy.id = $2
	`, now, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	return policy, err
}

const adminObservationPolicySelect = `
	WITH demand_theaters AS (
		SELECT preset.theater_id, true AS demand_active
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
	), effective_policies AS (
		SELECT policy.*, COALESCE(demand.demand_active, false) AS demand_active,
			CASE
				WHEN COALESCE(demand.demand_active, false) THEN 'demand'
				WHEN policy.burst_until > $1 THEN 'burst'
				ELSE 'baseline'
			END AS effective_mode,
			policy.priority AS effective_priority,
			CASE
				WHEN COALESCE(demand.demand_active, false)
					THEN policy.demand_min_interval_seconds
				WHEN policy.burst_until > $1 THEN policy.burst_min_interval_seconds
				ELSE policy.min_interval_seconds
			END AS effective_min_seconds,
			CASE
				WHEN COALESCE(demand.demand_active, false)
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

func scanAdminObservationPolicy(row rowScanner) (*adminpb.ObservationPolicy, error) {
	var id, effectiveMode, lastOutcome, lastErrorCode string
	var revision int64
	var enabled, demandActive bool
	var theaterID, providerID, sourceKey, region, name string
	var horizonDays, priority, baselineMinSeconds, baselineMaxSeconds int32
	var demandMinSeconds, demandMaxSeconds, burstMinSeconds, burstMaxSeconds, burstDurationSeconds int32
	var locale, timeZone, egressPolicyID string
	var effectivePriority, effectiveMinSeconds, effectiveMaxSeconds int32
	var burstUntil, nextRunAt, lastFinishedAt *time.Time
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&id, &revision, &enabled,
		&theaterID, &providerID, &sourceKey, &region, &name,
		&horizonDays, &priority,
		&baselineMinSeconds, &baselineMaxSeconds,
		&demandMinSeconds, &demandMaxSeconds,
		&burstMinSeconds, &burstMaxSeconds, &burstDurationSeconds,
		&locale, &timeZone, &egressPolicyID,
		&effectiveMode, &effectivePriority,
		&effectiveMinSeconds, &effectiveMaxSeconds,
		&demandActive, &burstUntil, &nextRunAt,
		&lastFinishedAt, &lastOutcome, &lastErrorCode,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan observation policy: %w", err)
	}
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(providerID)
	theater.SetSourceKey(sourceKey)
	theater.SetRegion(region)
	theater.SetName(name)
	input := &adminpb.ObservationPolicyInput{}
	input.SetTheaterId(theaterID)
	input.SetEnabled(enabled)
	input.SetHorizonDays(horizonDays)
	mode, err := adminObservationMode(effectiveMode)
	if err != nil {
		return nil, err
	}
	policy := &adminpb.ObservationPolicy{}
	policy.SetId(id)
	policy.SetRevision(revision)
	policy.SetTheater(theater)
	policy.SetInput(input)
	policy.SetEffectiveMode(mode)
	policy.SetEffectivePriority(effectivePriority)
	policy.SetEffectiveMinSeconds(effectiveMinSeconds)
	policy.SetEffectiveMaxSeconds(effectiveMaxSeconds)
	policy.SetDemandActive(demandActive)
	if burstUntil != nil {
		policy.SetBurstUntil(timestamppb.New(*burstUntil))
	}
	if nextRunAt != nil {
		policy.SetNextRunAt(timestamppb.New(*nextRunAt))
	}
	if lastFinishedAt != nil {
		policy.SetLastFinishedAt(timestamppb.New(*lastFinishedAt))
	}
	if lastOutcome != "" {
		outcome, err := adminObservationOutcome(lastOutcome)
		if err != nil {
			return nil, err
		}
		policy.SetLastOutcome(outcome)
	}
	policy.SetLastErrorCode(lastErrorCode)
	policy.SetCreatedAt(timestamppb.New(createdAt))
	policy.SetUpdatedAt(timestamppb.New(updatedAt))
	return policy, nil
}

func (store *Store) adminCatalogTheater(ctx context.Context, id string) (*catalogpb.Theater, error) {
	var theaterID, providerID, sourceKey, region, name string
	err := store.pool.QueryRow(ctx, `
		SELECT id, provider_id, source_key, region, name
		FROM theaters WHERE id = $1 AND active
	`, strings.TrimSpace(id)).Scan(
		&theaterID, &providerID, &sourceKey, &region, &name,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read observation policy theater: %w", err)
	}
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(providerID)
	theater.SetSourceKey(sourceKey)
	theater.SetRegion(region)
	theater.SetName(name)
	return theater, nil
}

func adminObservationPolicyID(theaterID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(theaterID)))
	return "policy_" + hex.EncodeToString(digest[:12])
}

func adminProbe(
	id, kind, ownerUserID, networkID, runtimeVersion, browserRevision, platform, architecture string,
	status string,
	draining bool,
	availableSlots, maxConcurrency int32,
	health, reasonCode string,
	lastHeartbeatAt *time.Time,
	updatedAt time.Time,
) (*adminpb.Probe, error) {
	probeKind := &probepb.ProbeKind{}
	switch kind {
	case "container":
		probeKind.SetContainer(&probepb.ContainerProbe{})
	case "client":
		probeKind.SetClient(&probepb.ClientProbe{})
	default:
		return nil, fmt.Errorf("unsupported probe kind %q", kind)
	}
	state := &adminpb.ProbeState{}
	switch status {
	case "online":
		state.SetOnline(&adminpb.OnlineProbe{})
	case "offline":
		state.SetOffline(&adminpb.OfflineProbe{})
	default:
		return nil, fmt.Errorf("unsupported probe state %q", status)
	}
	probeHealth := &probepb.ProbeHealth{}
	switch health {
	case "healthy":
		probeHealth.SetHealthy(&probepb.Healthy{})
	case "degraded":
		value := &probepb.Degraded{}
		value.SetReasonCode(reasonCode)
		probeHealth.SetDegraded(value)
	case "unhealthy":
		value := &probepb.Unhealthy{}
		value.SetReasonCode(reasonCode)
		probeHealth.SetUnhealthy(value)
	default:
		return nil, fmt.Errorf("unsupported probe health %q", health)
	}
	runtime := &commonpb.Runtime{}
	runtime.SetComponentVersion(runtimeVersion)
	runtime.SetBrowserRevision(browserRevision)
	runtime.SetPlatform(platform)
	runtime.SetArchitecture(architecture)
	probe := &adminpb.Probe{}
	probe.SetId(id)
	probe.SetKind(probeKind)
	probe.SetOwnerUserId(ownerUserID)
	probe.SetNetworkId(networkID)
	probe.SetRuntime(runtime)
	probe.SetState(state)
	probe.SetDraining(draining)
	probe.SetAvailableSlots(availableSlots)
	probe.SetMaxConcurrency(maxConcurrency)
	probe.SetHealth(probeHealth)
	if lastHeartbeatAt != nil {
		probe.SetLastHeartbeatAt(timestamppb.New(*lastHeartbeatAt))
	}
	probe.SetUpdatedAt(timestamppb.New(updatedAt))
	return probe, nil
}

//nolint:dupl // Mode and outcome populate different generated Proto oneofs; keeping branches explicit preserves exhaustiveness.
func adminObservationMode(value string) (*adminpb.ObservationMode, error) {
	mode := &adminpb.ObservationMode{}
	switch value {
	case "baseline":
		mode.SetBaseline(&adminpb.BaselineMode{})
	case "demand":
		mode.SetDemand(&adminpb.DemandMode{})
	case "burst":
		mode.SetBurst(&adminpb.BurstMode{})
	case "seat-availability":
		mode.SetSeatAvailability(&adminpb.SeatAvailabilityMode{})
	default:
		return nil, fmt.Errorf("unsupported observation mode %q", value)
	}
	return mode, nil
}

func seconds32(value time.Duration) int32 {
	return int32(value / time.Second)
}

//nolint:dupl // Mode and outcome populate different generated Proto oneofs; keeping branches explicit preserves exhaustiveness.
func adminObservationOutcome(value string) (*adminpb.ObservationOutcome, error) {
	outcome := &adminpb.ObservationOutcome{}
	switch value {
	case "completed":
		outcome.SetCompleted(&adminpb.CompletedOutcome{})
	case "partial":
		outcome.SetPartial(&adminpb.PartialOutcome{})
	case "failed":
		outcome.SetFailed(&adminpb.FailedOutcome{})
	case "missed":
		outcome.SetMissed(&adminpb.MissedOutcome{})
	default:
		return nil, fmt.Errorf("unsupported observation outcome %q", value)
	}
	return outcome, nil
}
