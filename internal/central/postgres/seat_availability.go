package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	seatavailabilitydomain "github.com/cineko-org/central/internal/domain/seatavailability"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"

	"github.com/jackc/pgx/v5"
)

// storeLiveSeatResult atomically persists the static layout and the exact
// showtime availability returned by a Probe. The collection state is cleared
// by putSeatMapTx in the same transaction as both writes.
func storeLiveSeatResult(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	task *observationpb.AssignmentTask,
) error {
	liveSeat, err := completedLiveSeatObservation(commit.Result)
	if err != nil {
		return err
	}
	target, err := assignmentLiveSeatTarget(task)
	if err != nil {
		return err
	}
	availability := liveSeat.GetAvailability()
	layout := liveSeat.GetLayout()
	if err := validateLiveSeatTarget(liveSeat, target); err != nil {
		return err
	}
	if err := catalogdomain.NormalizeSeatMap(layout, commit.CommittedAt); err != nil {
		return fmt.Errorf("%w: normalize live-seat layout: %w", central.ErrInvalid, err)
	}
	if availability.GetLayoutHash() != layout.GetLayoutHash() {
		return fmt.Errorf("%w: live-seat availability layout hash does not match normalized layout", central.ErrInvalid)
	}
	if err := seatavailabilitydomain.Normalize(availability, commit.CommittedAt); err != nil {
		return fmt.Errorf("%w: normalize seat availability: %w", central.ErrInvalid, err)
	}
	if _, err := putSeatMapTx(ctx, tx, layout); err != nil {
		return err
	}
	contentHash := seatavailabilitydomain.ContentHash(availability)
	changed, snapshotID, err := storeDistinctSeatAvailability(ctx, tx, availability, contentHash, commit.CommittedAt)
	if err != nil || !changed {
		return err
	}
	if target.availabilityTask == nil {
		return nil
	}
	return applySeatAvailability(ctx, tx, target.availabilityTask, availability, snapshotID, commit.CommittedAt)
}

type liveSeatTarget struct {
	auditoriumID     string
	showtimeID       string
	availabilityTask *observationpb.SeatAvailabilityTask
}

func completedLiveSeatObservation(result *observationpb.AssignmentResult) (*seatmappb.LiveSeatObservation, error) {
	completed := result.GetCompleted()
	if completed == nil {
		return nil, fmt.Errorf("%w: live-seat assignment result is incomplete", central.ErrInvalid)
	}
	liveSeat := completed.GetLiveSeat()
	if liveSeat == nil || liveSeat.GetLayout() == nil || liveSeat.GetAvailability() == nil ||
		completed.GetCatalog() != nil || completed.GetSchedule() != nil {
		return nil, fmt.Errorf("%w: live-seat assignment result is incomplete", central.ErrInvalid)
	}
	return liveSeat, nil
}

func assignmentLiveSeatTarget(task *observationpb.AssignmentTask) (liveSeatTarget, error) {
	if seatMapTask := task.GetSeatMap(); seatMapTask != nil {
		if seatMapTask.GetAuditorium() == nil {
			return liveSeatTarget{}, fmt.Errorf("%w: live-seat assignment auditorium is required", central.ErrInvalid)
		}
		target := liveSeatTarget{auditoriumID: seatMapTask.GetAuditorium().GetId()}
		if seatMapTask.GetShowtime() != nil {
			target.showtimeID = seatMapTask.GetShowtime().GetId()
		}
		return target, nil
	}
	availabilityTask := task.GetSeatAvailability()
	if availabilityTask == nil {
		return liveSeatTarget{}, fmt.Errorf("%w: live-seat assignment target is required", central.ErrInvalid)
	}
	if availabilityTask.GetAuditorium() == nil || availabilityTask.GetShowtime() == nil {
		return liveSeatTarget{}, fmt.Errorf("%w: live-seat assignment target is incomplete", central.ErrInvalid)
	}
	return liveSeatTarget{
		auditoriumID:     availabilityTask.GetAuditorium().GetId(),
		showtimeID:       availabilityTask.GetShowtime().GetId(),
		availabilityTask: availabilityTask,
	}, nil
}

func validateLiveSeatTarget(liveSeat *seatmappb.LiveSeatObservation, target liveSeatTarget) error {
	layout, availability := liveSeat.GetLayout(), liveSeat.GetAvailability()
	if target.auditoriumID == "" || layout.GetAuditoriumId() != target.auditoriumID ||
		availability.GetAuditoriumId() != target.auditoriumID ||
		target.showtimeID != "" && availability.GetShowtimeId() != target.showtimeID ||
		availability.GetLayoutHash() == "" || availability.GetLayoutHash() != layout.GetLayoutHash() {
		return fmt.Errorf("%w: live-seat result does not match its exact assignment", central.ErrInvalid)
	}
	return nil
}

func storeDistinctSeatAvailability(
	ctx context.Context,
	tx pgx.Tx,
	snapshot *seatmappb.AvailabilitySnapshot,
	availabilityHash string,
	now time.Time,
) (bool, string, error) {
	var latestHash string
	var latestObservedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT content_hash, observed_at
		FROM seat_availability_snapshots
		WHERE showtime_id = $1
		ORDER BY observed_at DESC, id DESC
		LIMIT 1
	`, snapshot.GetShowtimeId()).Scan(&latestHash, &latestObservedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, "", fmt.Errorf("read latest seat availability: %w", err)
	}
	observedAt := snapshot.GetObservedAt().AsTime()
	if err == nil && (!observedAt.After(latestObservedAt) || latestHash == availabilityHash) {
		return false, "", nil
	}
	snapshotID := "seat_availability_" + contentHash([]byte(strings.Join([]string{
		snapshot.GetShowtimeId(), observedAt.UTC().Format(time.RFC3339Nano), availabilityHash,
	}, "\x00")))
	if _, err := tx.Exec(ctx, `
		INSERT INTO seat_availability_snapshots (
			id, showtime_id, auditorium_id, layout_hash, content_hash, observed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, snapshotID, snapshot.GetShowtimeId(), snapshot.GetAuditoriumId(), snapshot.GetLayoutHash(),
		availabilityHash, observedAt, now); err != nil {
		return false, "", fmt.Errorf("store seat availability snapshot: %w", err)
	}
	rows := make([][]any, 0, len(snapshot.GetAvailableSeats()))
	for position, seat := range snapshot.GetAvailableSeats() {
		rows = append(rows, []any{snapshotID, position + 1, seat.GetSeatId()})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_availability_snapshot_seats"},
		[]string{"snapshot_id", "position", "seat_id"}, pgx.CopyFromRows(rows)); err != nil {
		return false, "", fmt.Errorf("store available seat identities: %w", err)
	}
	return true, snapshotID, nil
}

func applySeatAvailability(
	ctx context.Context,
	tx pgx.Tx,
	task *observationpb.SeatAvailabilityTask,
	snapshot *seatmappb.AvailabilitySnapshot,
	snapshotID string,
	now time.Time,
) error {
	location, err := time.LoadLocation(task.GetTimeZone())
	if err != nil {
		return fmt.Errorf("load seat-availability time zone: %w", err)
	}
	targets, err := loadExecutionTargets(ctx, tx, task.GetTheater().GetId())
	if err != nil {
		return err
	}
	layout, err := loadExactSeatLayout(ctx, tx, snapshot.GetAuditoriumId(), snapshot.GetLayoutHash())
	if err != nil {
		return err
	}
	requestLayoutValidation := layout == nil
	observedAt := snapshot.GetObservedAt().AsTime()
	for _, target := range targets {
		needsValidation, err := applySeatAvailabilityTarget(
			ctx, tx, task, target, layout, snapshot, snapshotID, observedAt, now, location,
		)
		if err != nil {
			return err
		}
		if needsValidation {
			requestLayoutValidation = true
		}
	}
	if requestLayoutValidation {
		trigger := (&collectionpb.Trigger_builder{ActiveMonitor: (&collectionpb.ActiveMonitor_builder{}).Build()}).Build()
		if err := queueSeatMapCollectionStateTx(ctx, tx, snapshot.GetAuditoriumId(), trigger, 60, now, ""); err != nil {
			return fmt.Errorf("request changed seat-map validation: %w", err)
		}
	}
	return nil
}

// applySeatAvailabilityTarget updates one eligible Monitor and emits only a
// newly positive execution edge.
func applySeatAvailabilityTarget(
	ctx context.Context,
	tx pgx.Tx,
	task *observationpb.SeatAvailabilityTask,
	target executionTarget,
	layout *seatmappb.Layout,
	snapshot *seatmappb.AvailabilitySnapshot,
	snapshotID string,
	observedAt time.Time,
	now time.Time,
	location *time.Location,
) (bool, error) {
	if !executionTargetMatches(target, task.GetShowtime(), now, location) {
		return false, nil
	}
	match := seatavailabilitydomain.Evaluate(layout, target.preset, snapshot)
	previous, exists, err := previousSeatAvailabilityMatch(
		ctx, tx, target.userID, target.monitor.GetId(), snapshot.GetShowtimeId(), observedAt,
	)
	if err != nil {
		return false, err
	}
	if err := writeSeatAvailabilityMatch(
		ctx, tx, target, snapshot.GetShowtimeId(), snapshotID, match.Available, observedAt, now,
	); err != nil {
		return false, err
	}
	if !match.Available || (exists && previous) {
		return !match.Exact, nil
	}
	if err := insertExecutionCommand(
		ctx, tx, target, task.GetShowtime(), observedAt, now, exists && !previous,
	); err != nil {
		return false, err
	}
	return !match.Exact, nil
}

func loadExactSeatLayout(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	layoutHash string,
) (*seatmappb.Layout, error) {
	var versionID string
	err := tx.QueryRow(ctx, `
		SELECT id FROM seat_map_versions WHERE auditorium_id = $1 AND layout_hash = $2
	`, auditoriumID, layoutHash).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read exact seat-map version: %w", err)
	}
	return readSeatMapLayout(ctx, tx, versionID, auditoriumID)
}

func previousSeatAvailabilityMatch(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	monitorID string,
	showtimeID string,
	observedAt time.Time,
) (bool, bool, error) {
	var matched bool
	var previousObservedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT matched, observed_at
		FROM monitor_showtime_availability
		WHERE user_id = $1 AND monitor_id = $2 AND showtime_id = $3
		FOR UPDATE
	`, userID, monitorID, showtimeID).Scan(&matched, &previousObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read monitor seat-availability state: %w", err)
	}
	if !observedAt.After(previousObservedAt) {
		// A stale snapshot cannot create a new false-to-true edge.
		return true, true, nil
	}
	return matched, true, nil
}

func writeSeatAvailabilityMatch(
	ctx context.Context,
	tx pgx.Tx,
	target executionTarget,
	showtimeID string,
	snapshotID string,
	matched bool,
	observedAt time.Time,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitor_showtime_availability (
			user_id, monitor_id, showtime_id, snapshot_id, matched, observed_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, monitor_id, showtime_id) DO UPDATE SET
			snapshot_id = EXCLUDED.snapshot_id,
			matched = EXCLUDED.matched,
			observed_at = EXCLUDED.observed_at,
			updated_at = EXCLUDED.updated_at
		WHERE monitor_showtime_availability.observed_at < EXCLUDED.observed_at
	`, target.userID, target.monitor.GetId(), showtimeID, snapshotID, matched, observedAt, now); err != nil {
		return fmt.Errorf("write monitor seat-availability state: %w", err)
	}
	return nil
}

// markExecutionAvailabilityUnavailable records the Client's authoritative live
// miss so only a later distinct positive snapshot may rearm the command.
func markExecutionAvailabilityUnavailable(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	monitorID string,
	showtimeID string,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitor_showtime_availability (
			user_id, monitor_id, showtime_id, snapshot_id, matched, observed_at, updated_at
		) VALUES ($1, $2, $3, NULL, false, $4, $4)
		ON CONFLICT (user_id, monitor_id, showtime_id) DO UPDATE SET
			snapshot_id = NULL,
			matched = false,
			observed_at = EXCLUDED.observed_at,
			updated_at = EXCLUDED.updated_at
		WHERE monitor_showtime_availability.observed_at < EXCLUDED.observed_at
	`, userID, monitorID, showtimeID, now); err != nil {
		return fmt.Errorf("record Client live-seat miss: %w", err)
	}
	return nil
}
