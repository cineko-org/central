package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
	"github.com/cineko-org/central/internal/domain/clientresources"

	"github.com/jackc/pgx/v5"
)

type executionTarget struct {
	userID  string
	monitor domain.MonitorJob
	preset  domain.Preset
}

const executionAvailabilityRearmCooldown = 30 * time.Second

func enqueueClientExecutions(
	ctx context.Context,
	tx pgx.Tx,
	commit central.ResultCommit,
	theaterID string,
	timeZone string,
) error {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return fmt.Errorf("load execution matching time zone: %w", err)
	}
	targets, err := loadExecutionTargets(ctx, tx, theaterID)
	if err != nil {
		return err
	}
	for _, capture := range commit.Result.Captures {
		for _, showtime := range capture.Showtimes {
			if showtime.SoldOut || showtime.AvailableSeats <= 0 {
				continue
			}
			for _, target := range targets {
				if !executionTargetMatches(target, capture.TargetDate, showtime, commit.CommittedAt, location) {
					continue
				}
				if err := insertExecutionCommand(ctx, tx, target, showtime, capture.ObservedAt, commit.CommittedAt); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func loadExecutionTargets(ctx context.Context, tx pgx.Tx, theaterID string) ([]executionTarget, error) {
	rows, err := tx.Query(ctx, `
		SELECT monitors.user_id, monitors.id, monitors.payload, presets.id, presets.payload
		FROM client_resources AS monitors
		JOIN client_resources AS presets
			ON presets.user_id = monitors.user_id AND presets.kind = 'presets'
			AND presets.id = monitors.payload->>'presetId' AND presets.deleted_at IS NULL
		WHERE monitors.kind = 'monitors' AND monitors.deleted_at IS NULL
			AND monitors.payload->>'status' IN ('pending', 'running')
			AND COALESCE(monitors.payload->>'mode', 'opening') IN ('', 'opening', 'cancellation')
			AND presets.payload->>'theaterId' = $1
	`, theaterID)
	if err != nil {
		return nil, fmt.Errorf("load Client execution targets: %w", err)
	}
	defer rows.Close()
	result := make([]executionTarget, 0)
	for rows.Next() {
		var target executionTarget
		var monitorID, presetID string
		var monitorPayload, presetPayload []byte
		if err := rows.Scan(&target.userID, &monitorID, &monitorPayload, &presetID, &presetPayload); err != nil {
			return nil, fmt.Errorf("scan Client execution target: %w", err)
		}
		if err := clientresources.ValidatePayload(
			target.userID, "monitors", monitorID, monitorPayload,
		); err != nil {
			continue
		}
		if err := clientresources.ValidatePayload(
			target.userID, "presets", presetID, presetPayload,
		); err != nil {
			continue
		}
		if err := json.Unmarshal(monitorPayload, &target.monitor); err != nil {
			continue
		}
		if err := json.Unmarshal(presetPayload, &target.preset); err != nil {
			continue
		}
		result = append(result, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client execution targets: %w", err)
	}
	return result, nil
}

func executionTargetMatches(
	target executionTarget,
	targetDate string,
	showtime central.Showtime,
	now time.Time,
	location *time.Location,
) bool {
	if target.preset.AuditoriumID != showtime.Auditorium.ID ||
		target.monitor.MovieID == "" || showtime.Movie.ID == "" || target.monitor.MovieID != showtime.Movie.ID {
		return false
	}
	return target.monitor.MatchesSchedule(targetDate, showtime.StartsAt, now, location)
}

func insertExecutionCommand(
	ctx context.Context,
	tx pgx.Tx,
	target executionTarget,
	showtime central.Showtime,
	observedAt time.Time,
	now time.Time,
) error {
	payload, err := json.Marshal(central.ExecutionPayload{Showtime: showtime, ObservedAt: observedAt})
	if err != nil {
		return fmt.Errorf("encode Client execution payload: %w", err)
	}
	id := "execution_" + contentHash([]byte(strings.Join([]string{
		target.userID, target.monitor.ID, showtime.ID, showtime.StartsAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	tag, err := tx.Exec(ctx, `
		INSERT INTO client_execution_commands AS command (
			id, user_id, monitor_id, showtime_id, starts_at, payload, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $7)
		ON CONFLICT (user_id, monitor_id, showtime_id, starts_at) DO UPDATE SET
			payload = EXCLUDED.payload,
			status = 'queued',
			leased_installation_id = NULL,
			last_installation_id = NULL,
			lease_token_hash = NULL,
			lease_expires_at = NULL,
			attempt_count = 0,
			reason_code = '',
			completed_at = NULL,
			updated_at = EXCLUDED.updated_at
		WHERE command.status = 'failed'
			AND command.reason_code IN ($9, $10)
			AND command.starts_at > EXCLUDED.updated_at
			AND command.updated_at <= $8
	`, id, target.userID, target.monitor.ID, showtime.ID, showtime.StartsAt, payload, now,
		now.Add(-executionAvailabilityRearmCooldown), executionReasonPreferredSeatsUnavailable,
		executionReasonShowtimeUnavailable)
	if err != nil {
		return fmt.Errorf("enqueue Client execution command: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	identity := strings.Join([]string{
		"availability", observedAt.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano),
	}, ":")
	return recordExecutionReadyEvent(
		ctx, tx, target.userID, id, target.monitor.ID, "availability_observed", identity, now,
	)
}

// recordExecutionReadyEvent persists the durable wake that lets a Client claim
// a newly queued command without polling Central.
func recordExecutionReadyEvent(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	commandID string,
	monitorID string,
	reason string,
	identity string,
	now time.Time,
) error {
	eventPayload, err := json.Marshal(map[string]string{
		"commandId": commandID,
		"monitorId": monitorID,
		"reason":    reason,
	})
	if err != nil {
		return fmt.Errorf("encode Client execution-ready event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ($1, $2, 'execution.ready.v1', 'executions', $3, 1, $4, $5)
	`, clientEventID(userID, "execution.ready.v1\x00"+commandID+"\x00"+identity),
		userID, commandID, eventPayload, now); err != nil {
		return fmt.Errorf("record Client execution-ready event: %w", err)
	}
	return nil
}
