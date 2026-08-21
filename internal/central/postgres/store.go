package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Central database URL: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "cineko-central"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Central database: %w", err)
	}
	store := &Store{pool: pool}
	if err := store.Ready(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) Ready(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("central database is unavailable")
	}
	if err := store.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping Central database: %w", err)
	}
	return nil
}

func (store *Store) ConsumeProbeBootstrap(
	ctx context.Context,
	ticketID string,
	expiresAt time.Time,
	now time.Time,
) error {
	tag, err := store.pool.Exec(ctx, `
		WITH expired AS (
			DELETE FROM consumed_probe_bootstrap_tickets WHERE expires_at <= $3
		)
		INSERT INTO consumed_probe_bootstrap_tickets (ticket_id, expires_at, consumed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (ticket_id) DO NOTHING
	`, ticketID, expiresAt, now)
	if err != nil {
		return fmt.Errorf("consume client Probe bootstrap ticket: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrUnauthorized
	}
	return nil
}

func (store *Store) RegisterProbe(ctx context.Context, probe central.Probe) (central.Probe, error) {
	row := store.pool.QueryRow(ctx, `
		INSERT INTO probe_runtimes (
			id, installation_id, owner_user_id, device_id, kind, network_id, network_hint, capabilities,
			available_capabilities, max_concurrency,
			runtime_version, browser_revision, platform, architecture,
			token_hash, token_expires_at, status, draining, available_slots, health, reason_code,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, '{}', $9, $10, $11, $12, $13,
			$14, $15, 'online', false, 0, 'healthy', '', $16, $16
		)
		ON CONFLICT (installation_id) DO UPDATE SET
			owner_user_id = EXCLUDED.owner_user_id,
			device_id = EXCLUDED.device_id,
			kind = EXCLUDED.kind,
			network_id = EXCLUDED.network_id,
			network_hint = EXCLUDED.network_hint,
			capabilities = EXCLUDED.capabilities,
			available_capabilities = '{}',
			max_concurrency = EXCLUDED.max_concurrency,
			runtime_version = EXCLUDED.runtime_version,
			browser_revision = EXCLUDED.browser_revision,
			platform = EXCLUDED.platform,
			architecture = EXCLUDED.architecture,
			token_hash = EXCLUDED.token_hash,
			token_expires_at = EXCLUDED.token_expires_at,
			status = 'online',
			draining = false,
			available_slots = 0,
			health = 'healthy',
			reason_code = '',
			updated_at = EXCLUDED.updated_at
		WHERE probe_runtimes.owner_user_id = EXCLUDED.owner_user_id
			AND probe_runtimes.device_id = EXCLUDED.device_id
		RETURNING id, created_at
	`, probe.ID, probe.InstallationID, probe.OwnerUserID, probe.DeviceID, probe.Kind, probe.NetworkID, probe.NetworkHint,
		probe.Capabilities, probe.MaxConcurrency, probe.Runtime.GetComponentVersion(),
		probe.Runtime.GetBrowserRevision(), probe.Runtime.GetPlatform(), probe.Runtime.GetArchitecture(),
		probe.TokenHash[:], probe.TokenExpiresAt, probe.CreatedAt)
	if err := row.Scan(&probe.ID, &probe.CreatedAt); err != nil {
		return central.Probe{}, fmt.Errorf("register probe: %w", err)
	}
	return probe, nil
}

func (store *Store) AuthenticateProbe(
	ctx context.Context,
	probeID string,
	tokenHash [32]byte,
	now time.Time,
) (central.Probe, error) {
	var probe central.Probe
	var storedHash []byte
	var err error
	if probeID == "" {
		probe, storedHash, err = store.probeByToken(ctx, tokenHash)
	} else {
		probe, storedHash, err = store.probeByID(ctx, probeID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return central.Probe{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.Probe{}, err
	}
	if probe.TokenExpiresAt.Before(now) || len(storedHash) != len(tokenHash) ||
		subtle.ConstantTimeCompare(storedHash, tokenHash[:]) != 1 {
		return central.Probe{}, central.ErrUnauthorized
	}
	return probe, nil
}

func (store *Store) HeartbeatProbe(
	ctx context.Context,
	probeID string,
	heartbeat *probepb.HeartbeatRequest,
	now time.Time,
) (central.Probe, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return central.Probe{}, fmt.Errorf("begin probe heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previousSlots int
	if err := tx.QueryRow(ctx, `
		SELECT available_slots FROM probe_runtimes WHERE id = $1 FOR UPDATE
	`, probeID).Scan(&previousSlots); errors.Is(err, pgx.ErrNoRows) {
		return central.Probe{}, central.ErrNotFound
	} else if err != nil {
		return central.Probe{}, fmt.Errorf("lock probe heartbeat: %w", err)
	}
	var probe central.Probe
	availableCapabilities, err := probedomain.CapabilityKeys(heartbeat.GetAvailableCapabilities())
	if err != nil {
		return central.Probe{}, fmt.Errorf("encode heartbeat capabilities: %w", err)
	}
	health, reasonCode := heartbeatHealth(heartbeat.GetHealth())
	err = tx.QueryRow(ctx, `
		UPDATE probe_runtimes
		SET status = 'online', draining = $2, available_slots = $3, health = $4,
			reason_code = $5, available_capabilities = COALESCE($6::text[], capabilities),
			last_heartbeat_at = $7, updated_at = $7
		WHERE id = $1
		RETURNING id, draining
	`, probeID, heartbeat.GetDraining(), heartbeat.GetAvailableSlots(), health,
		reasonCode, availableCapabilities, now).Scan(&probe.ID, &probe.Draining)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.Probe{}, central.ErrNotFound
	}
	if err != nil {
		return central.Probe{}, fmt.Errorf("heartbeat probe: %w", err)
	}
	if previousSlots < 1 && heartbeat.GetAvailableSlots() > 0 && health == "healthy" && !heartbeat.GetDraining() {
		if err := notifyAssignmentAvailability(ctx, tx); err != nil {
			return central.Probe{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return central.Probe{}, fmt.Errorf("commit probe heartbeat: %w", err)
	}
	return probe, nil
}

func (store *Store) DisconnectProbe(ctx context.Context, probeID string, now time.Time) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE probe_runtimes
		SET status = 'offline', available_slots = 0, updated_at = $2
		WHERE id = $1
	`, probeID, now)
	if err != nil {
		return fmt.Errorf("disconnect probe: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return central.ErrNotFound
	}
	return nil
}

func (store *Store) ClaimAssignment(
	ctx context.Context,
	probeID string,
	leaseHash [32]byte,
	now time.Time,
	leaseExpiresAt time.Time,
	heartbeatCutoff time.Time,
) (central.Assignment, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return central.Assignment{}, fmt.Errorf("begin assignment claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	capabilities, networkID, err := lockEligibleClaimingProbe(ctx, tx, probeID, heartbeatCutoff)
	if err != nil {
		return central.Assignment{}, err
	}
	assignment, err := claimQueuedAssignment(
		ctx, tx, probeID, capabilities, networkID, leaseHash, now, leaseExpiresAt,
	)
	if err != nil {
		return central.Assignment{}, err
	}
	if err := recordAssignmentAttempt(ctx, tx, assignment.ID, probeID, leaseHash, now); err != nil {
		return central.Assignment{}, err
	}
	if err := reserveProbeSlot(ctx, tx, probeID, now); err != nil {
		return central.Assignment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return central.Assignment{}, fmt.Errorf("commit assignment claim: %w", err)
	}
	return assignment, nil
}

func lockEligibleClaimingProbe(
	ctx context.Context,
	tx pgx.Tx,
	probeID string,
	heartbeatCutoff time.Time,
) ([]string, string, error) {
	var capabilities []string
	var networkID, status, health string
	var draining bool
	var availableSlots int
	var lastSeen time.Time
	err := tx.QueryRow(ctx, `
		SELECT available_capabilities, network_id, status, draining, health, available_slots,
			COALESCE(last_heartbeat_at, updated_at)
		FROM probe_runtimes WHERE id = $1 FOR UPDATE
	`, probeID).Scan(&capabilities, &networkID, &status, &draining, &health, &availableSlots, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", central.ErrUnauthorized
	}
	if err != nil {
		return nil, "", fmt.Errorf("lock claiming probe: %w", err)
	}
	if status != "online" || draining || health != "healthy" || availableSlots < 1 || lastSeen.Before(heartbeatCutoff) {
		return nil, "", central.ErrNoAssignment
	}
	return capabilities, networkID, nil
}

func claimQueuedAssignment(
	ctx context.Context,
	tx pgx.Tx,
	probeID string,
	capabilities []string,
	networkID string,
	leaseHash [32]byte,
	now time.Time,
	leaseExpiresAt time.Time,
) (central.Assignment, error) {
	assignment, err := scanAssignment(tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT assignment.id
			FROM observation_assignments AS assignment
			WHERE assignment.status = 'queued'
				AND assignment.not_before <= $3
				AND assignment.deadline > $3
				AND assignment.task_kind = ANY($2)
				AND EXISTS (
					SELECT 1 FROM assignment_eligible_probes AS eligible
					WHERE eligible.assignment_id = assignment.id AND eligible.probe_id = $1
						AND eligible.network_id = $6
						AND NOT EXISTS (
							SELECT 1
							FROM assignment_attempts AS attempted
							LEFT JOIN assignment_eligible_probes AS attempted_eligible
								ON attempted_eligible.assignment_id = attempted.assignment_id
								AND attempted_eligible.probe_id = attempted.probe_id
							WHERE attempted.assignment_id = assignment.id
								AND COALESCE(attempted.network_id, attempted_eligible.network_id) = eligible.network_id
						)
				)
				AND NOT EXISTS (
					SELECT 1 FROM observation_assignments AS active
					WHERE active.task_kind = assignment.task_kind
						AND active.theater_id = assignment.theater_id
						AND active.status = 'leased'
				)
			ORDER BY assignment.priority DESC, assignment.not_before, assignment.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE observation_assignments AS assignment
		SET status = 'leased', probe_id = $1, lease_token_hash = $4,
			lease_expires_at = LEAST($5, assignment.deadline), updated_at = $3
		FROM candidate
		WHERE assignment.id = candidate.id
		RETURNING assignment.id, assignment.status, assignment.not_before, assignment.deadline,
			assignment.probe_id, assignment.lease_expires_at, assignment.created_at, assignment.updated_at,
			assignment.task_data
	`, probeID, capabilities, now, leaseHash[:], leaseExpiresAt, networkID))
	if errors.Is(err, pgx.ErrNoRows) {
		return central.Assignment{}, central.ErrNoAssignment
	}
	if err != nil {
		return central.Assignment{}, fmt.Errorf("claim assignment: %w", err)
	}
	return assignment, nil
}

func recordAssignmentAttempt(
	ctx context.Context,
	tx pgx.Tx,
	assignmentID string,
	probeID string,
	leaseHash [32]byte,
	now time.Time,
) error {
	var attempt int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt), 0) + 1 FROM assignment_attempts WHERE assignment_id = $1
	`, assignmentID).Scan(&attempt); err != nil {
		return fmt.Errorf("number assignment attempt: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO assignment_attempts (
			assignment_id, probe_id, attempt, lease_token_hash, network_id, started_at, status
		)
		SELECT $1, $2, $3, $4, eligible.network_id, $5, 'leased'
		FROM assignment_eligible_probes AS eligible
		WHERE eligible.assignment_id = $1 AND eligible.probe_id = $2
	`, assignmentID, probeID, attempt, leaseHash[:], now)
	if err != nil {
		return fmt.Errorf("record assignment attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("record assignment attempt: expected one eligible probe, inserted %d", tag.RowsAffected())
	}
	return nil
}

func reserveProbeSlot(ctx context.Context, tx pgx.Tx, probeID string, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE probe_runtimes SET available_slots = available_slots - 1, updated_at = $2
		WHERE id = $1 AND available_slots > 0
	`, probeID, now)
	if err != nil {
		return fmt.Errorf("reserve probe slot: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("reserve probe slot: probe capacity invariant violated")
	}
	return nil
}

func (store *Store) HeartbeatAssignment(
	ctx context.Context,
	assignmentID string,
	probeID string,
	leaseHash [32]byte,
	now time.Time,
	leaseExpiresAt time.Time,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin assignment heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storedHash []byte
	var currentExpiry, deadline time.Time
	err = tx.QueryRow(ctx, `
		SELECT lease_token_hash, lease_expires_at, deadline
		FROM observation_assignments
		WHERE id = $1 AND probe_id = $2 AND status = 'leased'
		FOR UPDATE
	`, assignmentID, probeID).Scan(&storedHash, &currentExpiry, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read assignment lease: %w", err)
	}
	if err := authorizeAssignmentHeartbeat(storedHash, currentExpiry, deadline, leaseHash, now); err != nil {
		return err
	}
	if leaseExpiresAt.After(deadline) {
		leaseExpiresAt = deadline
	}
	_, err = tx.Exec(ctx, `
		UPDATE observation_assignments SET lease_expires_at = $2, updated_at = $3 WHERE id = $1
	`, assignmentID, leaseExpiresAt, now)
	if err != nil {
		return fmt.Errorf("extend assignment lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit assignment heartbeat: %w", err)
	}
	return nil
}

func authorizeAssignmentHeartbeat(
	storedHash []byte,
	leaseExpiresAt time.Time,
	deadline time.Time,
	leaseHash [32]byte,
	now time.Time,
) error {
	if !leaseExpiresAt.After(now) || !deadline.After(now) {
		return central.ErrLeaseExpired
	}
	if len(storedHash) != len(leaseHash) || subtle.ConstantTimeCompare(storedHash, leaseHash[:]) != 1 {
		return central.ErrUnauthorized
	}
	return nil
}

func (store *Store) CommitResult(ctx context.Context, commit central.ResultCommit) (*observationpb.ResultReceipt, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin result commit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	state, err := lockAssignmentResult(ctx, tx, commit.AssignmentID)
	if err != nil {
		return nil, err
	}
	if receipt, committed, err := existingAttemptReceipt(ctx, tx, commit); committed || err != nil {
		return receipt, err
	}
	if receipt, committed, err := existingReceipt(state, commit); committed || err != nil {
		return receipt, err
	}
	if err := authorizeResultCommit(state, commit); err != nil {
		return nil, err
	}
	if err := writeAssignmentResult(ctx, tx, commit); err != nil {
		return nil, err
	}
	if err := finishAssignmentAttempt(ctx, tx, commit); err != nil {
		return nil, err
	}
	if commit.Result.GetCompleted() != nil {
		if err := storeSuccessfulResult(ctx, tx, commit, state); err != nil {
			return nil, err
		}
	}
	if err := notifyAssignmentAvailability(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit assignment result: %w", err)
	}
	return resultReceipt(commit.AssignmentID, commit.Result.GetRunId(), commit.PayloadHash, false), nil
}

func storeSuccessfulResult(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	state assignmentResultState,
) error {
	switch state.taskKind {
	case probedomain.CapabilityCGVCatalogCapture:
		return storeCatalogResult(ctx, tx, commit.Result)
	case probedomain.CapabilityCGVSeatMapCapture:
		return storeSeatMapResult(ctx, tx, commit.Result, state.task)
	default:
		return storeScheduleResult(ctx, tx, commit, state)
	}
}

func storeCatalogResult(ctx context.Context, tx pgx.Tx, result *observationpb.AssignmentResult) error {
	completed := result.GetCompleted()
	if completed == nil || completed.GetCatalog() == nil || len(completed.GetCaptures()) != 0 || completed.GetSeatMap() != nil {
		return fmt.Errorf("%w: catalog assignment result is incomplete", central.ErrInvalid)
	}
	snapshot := completed.GetCatalog()
	// A full refresh must enumerate at least one theater. Otherwise an upstream
	// parser failure could suppress the retry while leaving the catalog empty.
	if len(snapshot.GetTheaters()) == 0 {
		return fmt.Errorf("%w: catalog assignment contains no theaters", central.ErrInvalid)
	}
	if err := catalogdomain.NormalizeSnapshot(snapshot); err != nil {
		return fmt.Errorf("validate Probe catalog snapshot: %w", err)
	}
	if _, err := upsertCatalogSnapshotTx(ctx, tx, snapshot); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE catalog_state SET refresh_requested_at = NULL WHERE id = 1`); err != nil {
		return fmt.Errorf("complete catalog refresh: %w", err)
	}
	return nil
}

func storeSeatMapResult(
	ctx context.Context,
	tx pgx.Tx,
	result *observationpb.AssignmentResult,
	task *observationpb.AssignmentTask,
) error {
	completed := result.GetCompleted()
	seatMap := completed.GetSeatMap()
	if completed == nil || seatMap == nil || len(completed.GetCaptures()) != 0 || completed.GetCatalog() != nil {
		return fmt.Errorf("%w: seat-map assignment result is incomplete", central.ErrInvalid)
	}
	if task.GetSeatMap() == nil || task.GetSeatMap().GetAuditorium() == nil ||
		seatMap.GetAuditoriumId() != task.GetSeatMap().GetAuditorium().GetId() {
		return fmt.Errorf("%w: seat-map result does not match assignment auditorium", central.ErrInvalid)
	}
	_, err := putSeatMapTx(ctx, tx, seatMap)
	return err
}

func storeScheduleResult(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	state assignmentResultState,
) error {
	completed := commit.Result.GetCompleted()
	if completed == nil || completed.GetCatalog() != nil || completed.GetSeatMap() != nil {
		return fmt.Errorf("%w: schedule assignment cannot include another result type", central.ErrInvalid)
	}
	snapshot := catalogSnapshotFromResult(state.theater, commit.Result, commit.CommittedAt)
	if err := catalogdomain.NormalizeSnapshot(snapshot); err != nil {
		return fmt.Errorf("validate Probe catalog snapshot: %w", err)
	}
	if _, err := upsertCatalogSnapshotTx(ctx, tx, snapshot); err != nil {
		return err
	}
	if err := storeCaptures(ctx, tx, commit, state.theaterID, state.timeZone); err != nil {
		return err
	}
	return enqueueClientExecutions(ctx, tx, commit, state.theaterID, state.timeZone)
}

type assignmentResultState struct {
	taskKind       string
	status         string
	probeID        string
	completedBy    string
	storedLease    []byte
	leaseExpiresAt *time.Time
	deadline       time.Time
	runID          string
	resultHash     string
	theaterID      string
	theater        *catalogpb.Theater
	timeZone       string
	task           *observationpb.AssignmentTask
}

func lockAssignmentResult(ctx context.Context, tx pgx.Tx, assignmentID string) (assignmentResultState, error) {
	var state assignmentResultState
	var taskData []byte
	var theaterID, providerID, sourceKey, region, name string
	err := tx.QueryRow(ctx, `
		SELECT task_kind, status, COALESCE(probe_id, ''), COALESCE(completed_by_probe_id, ''),
			COALESCE(lease_token_hash, ''::bytea),
			lease_expires_at, deadline, COALESCE(run_id, ''), COALESCE(result_hash, ''),
			theater_id, theater_provider_id, theater_source_key, theater_region, theater_name, time_zone,
			COALESCE(task_data, '{}'::jsonb)
		FROM observation_assignments WHERE id = $1 FOR UPDATE
	`, assignmentID).Scan(
		&state.taskKind, &state.status, &state.probeID, &state.completedBy, &state.storedLease,
		&state.leaseExpiresAt, &state.deadline,
		&state.runID, &state.resultHash, &theaterID, &providerID,
		&sourceKey, &region, &name, &state.timeZone, &taskData,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return assignmentResultState{}, central.ErrNotFound
	}
	if err != nil {
		return assignmentResultState{}, fmt.Errorf("lock assignment result: %w", err)
	}
	state.theater = &catalogpb.Theater{}
	state.theater.SetId(theaterID)
	state.theater.SetProviderId(providerID)
	state.theater.SetSourceKey(sourceKey)
	state.theater.SetRegion(region)
	state.theater.SetName(name)
	state.theaterID = theaterID
	if len(taskData) > 2 {
		state.task = &observationpb.AssignmentTask{}
		if err := protojson.Unmarshal(taskData, state.task); err != nil {
			return assignmentResultState{}, fmt.Errorf("decode assignment task: %w", err)
		}
	}
	return state, nil
}

func existingAttemptReceipt(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
) (*observationpb.ResultReceipt, bool, error) {
	var runID, resultHash, status string
	var storedLease []byte
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(run_id, ''), COALESCE(result_hash, ''), status,
			COALESCE(lease_token_hash, ''::bytea)
		FROM assignment_attempts
		WHERE assignment_id = $1 AND probe_id = $2
	`, commit.AssignmentID, commit.ProbeID).Scan(&runID, &resultHash, &status, &storedLease)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && resultHash == "") {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read assignment attempt receipt: %w", err)
	}
	leaseMatches := len(storedLease) == len(commit.LeaseHash) &&
		subtle.ConstantTimeCompare(storedLease, commit.LeaseHash[:]) == 1
	if runID != commit.Result.GetRunId() || resultHash != commit.PayloadHash || !leaseMatches {
		return nil, true, central.ErrIdempotencyConflict
	}
	_ = status
	return resultReceipt(commit.AssignmentID, runID, resultHash, true), true, nil
}

func existingReceipt(
	state assignmentResultState,
	commit central.ResultCommit,
) (*observationpb.ResultReceipt, bool, error) {
	if state.resultHash == "" {
		return nil, false, nil
	}
	if state.completedBy != commit.ProbeID || state.runID != commit.Result.GetRunId() || state.resultHash != commit.PayloadHash {
		return nil, true, central.ErrIdempotencyConflict
	}
	return resultReceipt(commit.AssignmentID, state.runID, state.resultHash, true), true, nil
}

func resultReceipt(assignmentID, runID, contentHash string, duplicate bool) *observationpb.ResultReceipt {
	receipt := &observationpb.ResultReceipt{}
	receipt.SetAssignmentId(assignmentID)
	receipt.SetRunId(runID)
	receipt.SetContentHash(contentHash)
	if duplicate {
		receipt.SetDuplicate(&observationpb.Duplicate{})
	} else {
		receipt.SetAccepted(&observationpb.Accepted{})
	}
	return receipt
}

func authorizeResultCommit(state assignmentResultState, commit central.ResultCommit) error {
	leaseMatches := len(state.storedLease) == len(commit.LeaseHash) &&
		subtle.ConstantTimeCompare(state.storedLease, commit.LeaseHash[:]) == 1
	if state.status != "leased" || state.probeID != commit.ProbeID || !leaseMatches {
		return central.ErrUnauthorized
	}
	if state.leaseExpiresAt == nil || !state.leaseExpiresAt.After(commit.CommittedAt) ||
		!state.deadline.After(commit.CommittedAt) {
		return central.ErrLeaseExpired
	}
	return nil
}

func writeAssignmentResult(ctx context.Context, tx pgx.Tx, commit central.ResultCommit) error {
	status := assignmentResultStatus(commit.Result)
	if status == "failed" {
		_, err := tx.Exec(ctx, `
			UPDATE observation_assignments
			SET status = 'retry_pending', probe_id = NULL, lease_token_hash = NULL,
				lease_expires_at = NULL, terminal_reason = '', updated_at = $2
			WHERE id = $1
		`, commit.AssignmentID, commit.CommittedAt)
		if err != nil {
			return fmt.Errorf("store retryable assignment failure: %w", err)
		}
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE observation_assignments
		SET status = $2, probe_id = NULL, lease_token_hash = NULL, lease_expires_at = NULL,
			completed_by_probe_id = $3, run_id = $4, result_hash = $5, result_payload = $6,
			started_at = $7, finished_at = $8, updated_at = $9
		WHERE id = $1
	`, commit.AssignmentID, status, commit.ProbeID, commit.Result.GetRunId(), commit.PayloadHash,
		string(commit.Payload), commit.Result.GetStartedAt().AsTime(), commit.Result.GetFinishedAt().AsTime(), commit.CommittedAt)
	if err != nil {
		return fmt.Errorf("store assignment result: %w", err)
	}
	return nil
}

func finishAssignmentAttempt(ctx context.Context, tx pgx.Tx, commit central.ResultCommit) error {
	status := assignmentResultStatus(commit.Result)
	tag, err := tx.Exec(ctx, `
		UPDATE assignment_attempts
		SET status = $3, finished_at = $4, run_id = $5, result_hash = $6, result_payload = $7
		WHERE assignment_id = $1 AND probe_id = $2 AND status = 'leased'
	`, commit.AssignmentID, commit.ProbeID, status, commit.Result.GetFinishedAt().AsTime(),
		commit.Result.GetRunId(), commit.PayloadHash, string(commit.Payload))
	if err != nil {
		return fmt.Errorf("finish assignment attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish assignment attempt: expected one active attempt, updated %d", tag.RowsAffected())
	}
	if tag, err := tx.Exec(ctx, `
		UPDATE probe_runtimes
		SET available_slots = CASE
			WHEN status = 'online' THEN LEAST(max_concurrency, available_slots + 1)
			ELSE 0
		END, updated_at = $2
		WHERE id = $1
	`, commit.ProbeID, commit.CommittedAt); err != nil {
		return fmt.Errorf("release probe slot: %w", err)
	} else if tag.RowsAffected() != 1 {
		return fmt.Errorf("release probe slot: probe runtime was not found")
	}
	return nil
}

func assignmentResultStatus(result *observationpb.AssignmentResult) string {
	if result.GetFailed() != nil {
		return "failed"
	}
	for _, capture := range result.GetCompleted().GetCaptures() {
		if !capture.GetComplete() {
			return "partial"
		}
	}
	return "completed"
}

func storeCaptures(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	theaterID string,
	timeZone string,
) error {
	for _, capture := range commit.Result.GetCompleted().GetCaptures() {
		opened, newShowtimes, err := captureIntroducesNewShowtimes(ctx, tx, theaterID, capture)
		if err != nil {
			return err
		}
		if err := storeCapture(ctx, tx, commit, theaterID, capture); err != nil {
			return err
		}
		if err := requestMonitoredSeatMapValidation(
			ctx, tx, theaterID, timeZone, localDateString(capture.GetTargetDate()), newShowtimes, commit.CommittedAt,
		); err != nil {
			return err
		}
		if opened {
			if err := activateObservationBurst(ctx, tx, theaterID, commit.CommittedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func captureIntroducesNewShowtimes(
	ctx context.Context,
	tx pgx.Tx,
	theaterID string,
	capture *observationpb.Capture,
) (bool, []*catalogpb.Showtime, error) {
	if !capture.GetComplete() || len(capture.GetShowtimes()) == 0 {
		return false, nil, nil
	}
	showtimes := capture.GetShowtimes()
	sourceKeys := make([]string, len(showtimes))
	startTimes := make([]time.Time, len(showtimes))
	for index, showtime := range showtimes {
		sourceKeys[index] = showtime.GetSourceKey()
		startTimes[index] = showtime.GetStartsAt().AsTime()
	}
	var hasPrior bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM schedule_captures AS previous
			JOIN observation_assignments AS assignment ON assignment.id = previous.assignment_id
			WHERE assignment.theater_id = $1 AND previous.target_date = $2
				AND previous.complete AND previous.observed_at < $3
		)
	`, theaterID, localDateString(capture.GetTargetDate()), capture.GetObservedAt().AsTime()).Scan(&hasPrior)
	if err != nil {
		return false, nil, fmt.Errorf("detect prior schedule capture: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT current.ordinality
		FROM UNNEST($2::text[], $3::timestamptz[]) WITH ORDINALITY AS current(source_key, starts_at, ordinality)
		WHERE NOT EXISTS (
			SELECT 1 FROM showtime_observations AS previous
			WHERE previous.theater_id = $1
				AND previous.source_key = current.source_key
				AND previous.starts_at = current.starts_at
				AND previous.observed_at < $4
		)
	`, theaterID, sourceKeys, startTimes, capture.GetObservedAt().AsTime())
	if err != nil {
		return false, nil, fmt.Errorf("detect newly observed showtimes: %w", err)
	}
	defer rows.Close()
	newShowtimes := make([]*catalogpb.Showtime, 0)
	for rows.Next() {
		var ordinal int
		if err := rows.Scan(&ordinal); err != nil {
			return false, nil, fmt.Errorf("scan newly observed showtime: %w", err)
		}
		if ordinal < 1 || ordinal > len(showtimes) {
			return false, nil, errors.New("newly observed showtime ordinal is out of range")
		}
		newShowtimes = append(newShowtimes, showtimes[ordinal-1])
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("iterate newly observed showtimes: %w", err)
	}
	return hasPrior && len(newShowtimes) > 0, newShowtimes, nil
}

// requestMonitoredSeatMapValidation marks only the auditorium whose newly
// observed showtime satisfies an active monitor. Reconciliation chooses the
// Probe and provider visit; Clients do not receive those mechanics.
func requestMonitoredSeatMapValidation(
	ctx context.Context,
	tx pgx.Tx,
	theaterID string,
	timeZone string,
	targetDate string,
	showtimes []*catalogpb.Showtime,
	now time.Time,
) error {
	if len(showtimes) == 0 {
		return nil
	}
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return fmt.Errorf("load seat-map validation time zone: %w", err)
	}
	targets, err := loadExecutionTargets(ctx, tx, theaterID)
	if err != nil {
		return err
	}
	auditoriumIDs := make(map[string]struct{})
	for _, showtime := range showtimes {
		for _, target := range targets {
			if executionTargetMatches(target, targetDate, showtime, now, location) {
				auditoriumIDs[showtime.GetAuditorium().GetId()] = struct{}{}
				break
			}
		}
	}
	for auditoriumID := range auditoriumIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE auditoriums
			SET seat_map_requested_at = COALESCE(seat_map_requested_at, $2)
			WHERE id = $1 AND active
		`, auditoriumID, now); err != nil {
			return fmt.Errorf("request monitored seat-map validation: %w", err)
		}
	}
	return nil
}

func activateObservationBurst(ctx context.Context, tx pgx.Tx, theaterID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE observation_policies
		SET burst_until = GREATEST(
			COALESCE(burst_until, $2),
			$2 + burst_duration_seconds * INTERVAL '1 second'
		), updated_at = $2
		WHERE theater_id = $1 AND enabled AND deleted_at IS NULL
	`, theaterID, now); err != nil {
		return fmt.Errorf("activate observation burst: %w", err)
	}
	return nil
}

func storeCapture(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	theaterID string,
	capture *observationpb.Capture,
) error {
	payload, err := protojson.Marshal(capture)
	if err != nil {
		return fmt.Errorf("encode schedule capture: %w", err)
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(capture)
	if err != nil {
		return fmt.Errorf("encode canonical schedule capture: %w", err)
	}
	hash := contentHash(canonical)
	if _, err := tx.Exec(ctx, `
		INSERT INTO observation_payloads (content_hash, payload, created_at)
		VALUES ($1, $2, $3) ON CONFLICT (content_hash) DO NOTHING
	`, hash, string(payload), commit.CommittedAt); err != nil {
		return fmt.Errorf("store observation payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schedule_captures (
			assignment_id, run_id, target_date, observed_at, complete, error_code, content_hash, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, commit.AssignmentID, commit.Result.GetRunId(), localDateString(capture.GetTargetDate()), capture.GetObservedAt().AsTime(),
		capture.GetComplete(), capture.GetErrorCode(), hash, commit.CommittedAt); err != nil {
		return fmt.Errorf("store schedule capture: %w", err)
	}
	for _, showtime := range capture.GetShowtimes() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO showtime_observations (
				assignment_id, run_id, target_date, source_key, theater_id,
				auditorium_id, auditorium_name, screen_types, movie_id, movie_title, poster_url,
				starts_at, ends_at, available_seats, capacity, sold_out, observed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		`, commit.AssignmentID, commit.Result.GetRunId(), localDateString(capture.GetTargetDate()), showtime.GetSourceKey(), theaterID,
			showtime.GetAuditorium().GetId(), showtime.GetAuditorium().GetName(), showtime.GetAuditorium().GetScreenTypes(), showtime.GetMovie().GetId(),
			showtime.GetMovie().GetTitle(),
			showtime.GetMovie().GetPosterUrl(), showtime.GetStartsAt().AsTime(), showtime.GetEndsAt().AsTime(), showtime.GetAvailableSeats(),
			showtime.GetCapacity(), showtime.GetSoldOut(), capture.GetObservedAt().AsTime()); err != nil {
			return fmt.Errorf("store showtime observation: %w", err)
		}
	}
	return nil
}

func catalogSnapshotFromResult(
	theater *catalogpb.Theater,
	result *observationpb.AssignmentResult,
	observedAt time.Time,
) *catalogpb.CatalogSnapshot {
	provider := &catalogpb.Provider{}
	provider.SetId(theater.GetProviderId())
	provider.SetName(providerName(theater.GetProviderId()))
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetObservedAt(timestamppb.New(observedAt))
	movies := make(map[string]struct{})
	auditoriums := make(map[string]*catalogpb.Auditorium)
	showtimes := make(map[string]*catalogpb.Showtime)
	for _, capture := range result.GetCompleted().GetCaptures() {
		for _, showtime := range capture.GetShowtimes() {
			if _, exists := movies[showtime.GetMovie().GetId()]; !exists {
				movies[showtime.GetMovie().GetId()] = struct{}{}
				snapshot.SetMovies(append(snapshot.GetMovies(), showtime.GetMovie()))
			}
			auditoriums[showtime.GetAuditorium().GetId()] = showtime.GetAuditorium()
			showtimes[showtime.GetId()] = showtime
		}
	}
	for _, auditorium := range auditoriums {
		snapshot.SetAuditoriums(append(snapshot.GetAuditoriums(), auditorium))
	}
	for _, showtime := range showtimes {
		snapshot.SetShowtimes(append(snapshot.GetShowtimes(), showtime))
	}
	slices.SortFunc(snapshot.GetAuditoriums(), func(left, right *catalogpb.Auditorium) int { return strings.Compare(left.GetId(), right.GetId()) })
	slices.SortFunc(snapshot.GetShowtimes(), func(left, right *catalogpb.Showtime) int { return strings.Compare(left.GetId(), right.GetId()) })
	return snapshot
}

func providerName(providerID string) string {
	if providerID == catalogdomain.ProviderCGV {
		return "CGV"
	}
	return providerID
}

func localDateString(value *commonpb.LocalDate) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", value.GetYear(), value.GetMonth(), value.GetDay())
}

func (store *Store) probeByID(ctx context.Context, probeID string) (central.Probe, []byte, error) {
	return store.scanProbe(store.pool.QueryRow(ctx, probeSelect+` WHERE id = $1`, probeID))
}

func (store *Store) probeByToken(ctx context.Context, tokenHash [32]byte) (central.Probe, []byte, error) {
	return store.scanProbe(store.pool.QueryRow(ctx, probeSelect+` WHERE token_hash = $1`, tokenHash[:]))
}

const probeSelect = `
	SELECT id, installation_id, owner_user_id, device_id, kind, network_id, network_hint, capabilities,
		available_capabilities, max_concurrency,
		runtime_version, browser_revision, platform, architecture,
		token_hash, token_expires_at, status, draining, available_slots, health, reason_code,
		last_heartbeat_at, created_at, updated_at
	FROM probe_runtimes`

func (store *Store) scanProbe(row rowScanner) (central.Probe, []byte, error) {
	var probe central.Probe
	var tokenHash []byte
	var lastHeartbeatAt *time.Time
	var runtimeVersion, browserRevision, platform, architecture string
	err := row.Scan(
		&probe.ID, &probe.InstallationID, &probe.OwnerUserID, &probe.DeviceID,
		&probe.Kind, &probe.NetworkID, &probe.NetworkHint,
		&probe.Capabilities, &probe.AvailableCapabilities, &probe.MaxConcurrency,
		&runtimeVersion, &browserRevision, &platform, &architecture,
		&tokenHash, &probe.TokenExpiresAt, &probe.Status, &probe.Draining, &probe.AvailableSlots,
		&probe.Health, &probe.ReasonCode, &lastHeartbeatAt, &probe.CreatedAt, &probe.UpdatedAt,
	)
	if err == nil {
		probe.Runtime = &commonpb.Runtime{}
		probe.Runtime.SetComponentVersion(runtimeVersion)
		probe.Runtime.SetBrowserRevision(browserRevision)
		probe.Runtime.SetPlatform(platform)
		probe.Runtime.SetArchitecture(architecture)
	}
	if lastHeartbeatAt != nil {
		probe.LastHeartbeatAt = *lastHeartbeatAt
	}
	return probe, tokenHash, err
}

func heartbeatHealth(health *probepb.ProbeHealth) (string, string) {
	switch {
	case health != nil && health.GetHealthy() != nil:
		return "healthy", ""
	case health != nil && health.GetDegraded() != nil:
		return "degraded", health.GetDegraded().GetReasonCode()
	case health != nil && health.GetUnhealthy() != nil:
		return "unhealthy", health.GetUnhealthy().GetReasonCode()
	default:
		return "", ""
	}
}

type rowScanner interface {
	Scan(...any) error
}

func scanAssignment(row rowScanner) (central.Assignment, error) {
	var assignment central.Assignment
	var probeID *string
	var leaseExpiresAt *time.Time
	var taskData []byte
	err := row.Scan(
		&assignment.ID, &assignment.Status, &assignment.NotBefore, &assignment.Deadline, &probeID,
		&leaseExpiresAt, &assignment.CreatedAt, &assignment.UpdatedAt, &taskData,
	)
	if err == nil && len(taskData) > 0 {
		assignment.Task = &observationpb.AssignmentTask{}
		err = protojson.Unmarshal(taskData, assignment.Task)
	}
	if probeID != nil {
		assignment.ProbeID = *probeID
	}
	if leaseExpiresAt != nil {
		assignment.LeaseExpiresAt = *leaseExpiresAt
	}
	return assignment, err
}

func contentHash(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
