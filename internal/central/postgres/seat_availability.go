package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	seatavailabilitydomain "github.com/cineko-org/central/internal/domain/seatavailability"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"

	"github.com/jackc/pgx/v5"
)

// storeSeatAvailabilityResult persists only adjacent state changes, evaluates
// active presets, and emits a command only for a false-to-true match edge.
func storeSeatAvailabilityResult(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	task *observationpb.AssignmentTask,
) error {
	completed := commit.Result.GetCompleted()
	if completed == nil {
		return fmt.Errorf("%w: seat-availability assignment result is incomplete", central.ErrInvalid)
	}
	snapshot := completed.GetSeatAvailability()
	availabilityTask := task.GetSeatAvailability()
	if snapshot == nil || availabilityTask == nil ||
		len(completed.GetCaptures()) != 0 || completed.GetCatalog() != nil || completed.GetSeatMap() != nil {
		return fmt.Errorf("%w: seat-availability assignment result is incomplete", central.ErrInvalid)
	}
	if availabilityTask.GetShowtime() == nil || availabilityTask.GetAuditorium() == nil ||
		snapshot.GetShowtimeId() != availabilityTask.GetShowtime().GetId() ||
		snapshot.GetAuditoriumId() != availabilityTask.GetAuditorium().GetId() {
		return fmt.Errorf("%w: seat-availability result does not match its exact assignment", central.ErrInvalid)
	}
	if err := seatavailabilitydomain.Normalize(snapshot, commit.CommittedAt); err != nil {
		return fmt.Errorf("%w: normalize seat availability: %w", central.ErrInvalid, err)
	}
	contentHash := seatavailabilitydomain.ContentHash(snapshot)
	changed, snapshotID, err := storeDistinctSeatAvailability(ctx, tx, snapshot, contentHash, commit.CommittedAt)
	if err != nil || !changed {
		return err
	}
	return applySeatAvailability(ctx, tx, availabilityTask, snapshot, snapshotID, commit.CommittedAt)
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
	for _, target := range targets {
		if !executionTargetMatches(target, task.GetShowtime(), now, location) {
			continue
		}
		match := seatavailabilitydomain.Evaluate(layout, target.preset, snapshot)
		if !match.Exact {
			requestLayoutValidation = true
		}
		previous, exists, err := previousSeatAvailabilityMatch(
			ctx, tx, target.userID, target.monitor.GetId(), snapshot.GetShowtimeId(), snapshot.GetObservedAt().AsTime(),
		)
		if err != nil {
			return err
		}
		if err := writeSeatAvailabilityMatch(
			ctx, tx, target, snapshot.GetShowtimeId(), snapshotID, match.Available,
			snapshot.GetObservedAt().AsTime(), now,
		); err != nil {
			return err
		}
		if !match.Available || (exists && previous) {
			continue
		}
		if err := insertExecutionCommand(
			ctx, tx, target, task.GetShowtime(), snapshot.GetObservedAt().AsTime(), now, exists && !previous,
		); err != nil {
			return err
		}
	}
	if requestLayoutValidation {
		if _, err := tx.Exec(ctx, `
			UPDATE auditoriums
			SET seat_map_requested_at = COALESCE(seat_map_requested_at, $2), updated_at = $2
			WHERE id = $1
		`, snapshot.GetAuditoriumId(), now); err != nil {
			return fmt.Errorf("request changed seat-map validation: %w", err)
		}
	}
	return nil
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
