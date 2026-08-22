package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type executionTarget struct {
	userID  string
	monitor *clientpb.Monitor
	preset  *clientpb.Preset
}

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
	for _, capture := range commit.Result.GetCompleted().GetCaptures() {
		for _, showtime := range capture.GetShowtimes() {
			if showtime.GetSoldOut() || showtime.GetAvailableSeats() <= 0 {
				continue
			}
			for _, target := range targets {
				if !executionTargetMatches(target, showtime, commit.CommittedAt, location) {
					continue
				}
				if err := insertExecutionCommand(ctx, tx, target, showtime, capture.GetObservedAt().AsTime(), commit.CommittedAt, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func loadExecutionTargets(ctx context.Context, tx pgx.Tx, theaterID string) ([]executionTarget, error) {
	rows, err := tx.Query(ctx, `
		SELECT monitors.user_id, monitors.id, monitors.preset_id,
			monitors.movie_id, monitors.movie_title,
			COALESCE(ARRAY(
				SELECT target_date::text
				FROM client_monitor_target_dates AS target_date
				WHERE target_date.user_id = monitors.user_id
					AND target_date.monitor_id = monitors.id
				ORDER BY target_date.position
			), ARRAY[]::text[]),
			COALESCE(ARRAY(
				SELECT target_weekday::integer
				FROM client_monitor_target_weekdays AS target_weekday
				WHERE target_weekday.user_id = monitors.user_id
					AND target_weekday.monitor_id = monitors.id
				ORDER BY target_weekday.position
			), ARRAY[]::integer[]),
		monitors.search_horizon_days,
		monitors.earliest_minute, monitors.latest_minute, monitors.state,
			presets.id, presets.theater_id, presets.auditorium_id,
			presets.seat_count, presets.together, presets.avoid_edges,
			COALESCE(ARRAY(
				SELECT explicit.seat_label
				FROM client_preset_explicit_seats AS explicit
				WHERE explicit.user_id = presets.user_id AND explicit.preset_id = presets.id
				ORDER BY explicit.position
			), ARRAY[]::text[])
		FROM client_monitors AS monitors
		JOIN client_resources AS monitor_resource
			ON monitor_resource.user_id = monitors.user_id
			AND monitor_resource.kind = 'monitors'
			AND monitor_resource.id = monitors.id
			AND monitor_resource.deleted_at IS NULL
		JOIN client_presets AS presets
			ON presets.user_id = monitors.user_id
			AND presets.resource_kind = 'presets'
			AND presets.id = monitors.preset_id
		JOIN client_resources AS preset_resource
			ON preset_resource.user_id = presets.user_id
			AND preset_resource.kind = 'presets'
			AND preset_resource.id = presets.id
			AND preset_resource.deleted_at IS NULL
		WHERE monitors.resource_kind = 'monitors'
			AND monitors.state IN ('pending', 'running')
			AND presets.theater_id = $1
	`, theaterID)
	if err != nil {
		return nil, fmt.Errorf("load Client execution targets: %w", err)
	}
	defer rows.Close()
	result := make([]executionTarget, 0)
	for rows.Next() {
		var target executionTarget
		var monitorID, presetID, presetTheaterID, presetAuditoriumID string
		var monitorPresetID, movieID, movieTitle, state string
		var targetDates []string
		var targetWeekdays []int32
		var searchHorizonDays int32
		var seatCount int32
		var together, avoidEdges bool
		var explicitSeats []string
		var earliestMinute, latestMinute *int32
		if err := rows.Scan(
			&target.userID, &monitorID, &monitorPresetID,
			&movieID, &movieTitle, &targetDates, &targetWeekdays, &searchHorizonDays,
			&earliestMinute, &latestMinute, &state,
			&presetID, &presetTheaterID, &presetAuditoriumID,
			&seatCount, &together, &avoidEdges, &explicitSeats,
		); err != nil {
			return nil, fmt.Errorf("scan Client execution target: %w", err)
		}
		target.monitor, err = executionMonitor(
			monitorID, target.userID, monitorPresetID, movieID, movieTitle,
			targetDates, targetWeekdays, searchHorizonDays,
			earliestMinute, latestMinute, state,
		)
		if err != nil {
			return nil, fmt.Errorf("decode normalized Client execution target: %w", err)
		}
		target.preset = &clientpb.Preset{}
		target.preset.SetId(presetID)
		target.preset.SetUserId(target.userID)
		target.preset.SetTheaterId(presetTheaterID)
		target.preset.SetAuditoriumId(presetAuditoriumID)
		target.preset.SetSeatCount(seatCount)
		preference := &clientpb.SeatPreference{}
		preference.SetExplicitSeats(explicitSeats)
		preference.SetTogether(together)
		preference.SetAvoidEdges(avoidEdges)
		target.preset.SetSeatPreference(preference)
		if !target.monitor.GetState().HasPending() && !target.monitor.GetState().HasRunning() {
			continue
		}
		result = append(result, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client execution targets: %w", err)
	}
	return result, nil
}

func executionMonitor(
	id, userID, presetID, movieID, movieTitle string,
	targetDates []string,
	targetWeekdays []int32,
	searchHorizonDays int32,
	earliestMinute, latestMinute *int32,
	state string,
) (*clientpb.Monitor, error) {
	monitor := &clientpb.Monitor{}
	monitor.SetId(id)
	monitor.SetUserId(userID)
	monitor.SetPresetId(presetID)
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle(movieTitle)
	dates, err := executionLocalDates(targetDates)
	if err != nil {
		return nil, err
	}
	monitor.SetTargetDates(dates)
	monitor.SetTargetWeekdays(targetWeekdays)
	monitor.SetSearchHorizonDays(searchHorizonDays)
	earliest, err := executionLocalTimeMinute(earliestMinute)
	if err != nil {
		return nil, err
	}
	latest, err := executionLocalTimeMinute(latestMinute)
	if err != nil {
		return nil, err
	}
	monitor.SetEarliestTime(earliest)
	monitor.SetLatestTime(latest)
	monitorState, err := executionMonitorState(state)
	if err != nil {
		return nil, err
	}
	monitor.SetState(monitorState)
	return monitor, nil
}

func executionLocalDates(values []string) ([]*commonpb.LocalDate, error) {
	result := make([]*commonpb.LocalDate, 0, len(values))
	for _, value := range values {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return nil, fmt.Errorf("invalid normalized Client target date %q: %w", value, err)
		}
		date, err := clientLocalDateFromTime(parsed)
		if err != nil {
			return nil, fmt.Errorf("normalize Client target date: %w", err)
		}
		result = append(result, date)
	}
	return result, nil
}

func executionLocalTimeMinute(value *int32) (*commonpb.LocalTime, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 || *value >= 24*60 {
		return nil, fmt.Errorf("invalid normalized Client local time minute %d", *value)
	}
	result := &commonpb.LocalTime{}
	result.SetHour(*value / 60)
	result.SetMinute(*value % 60)
	return result, nil
}

func executionMonitorState(value string) (*clientpb.MonitorState, error) {
	state := &clientpb.MonitorState{}
	switch value {
	case "running":
		state.SetRunning(&clientpb.MonitorRunning{})
	case "pending":
		state.SetPending(&clientpb.MonitorPending{})
	case "triggered":
		state.SetTriggered(&clientpb.MonitorTriggered{})
	case "booked":
		state.SetBooked(&clientpb.MonitorBooked{})
	case "failed":
		state.SetFailed(&clientpb.MonitorFailed{})
	case "stopped":
		state.SetStopped(&clientpb.MonitorStopped{})
	case "payment-unknown":
		state.SetPaymentUnknown(&clientpb.MonitorPaymentUnknown{})
	default:
		return nil, fmt.Errorf("unknown normalized Client monitor state %q", value)
	}
	return state, nil
}

func executionTargetMatches(
	target executionTarget,
	showtime *catalogpb.Showtime,
	now time.Time,
	location *time.Location,
) bool {
	if target.preset.GetAuditoriumId() != showtime.GetAuditorium().GetId() ||
		target.monitor.GetMovieId() == "" || showtime.GetMovie().GetId() == "" || target.monitor.GetMovieId() != showtime.GetMovie().GetId() {
		return false
	}
	return domain.MonitorMatchesScheduleDate(
		target.monitor,
		localDateString(showtime.GetScheduleDate()),
		showtime.GetStartsAt().AsTime(),
		now,
		location,
	)
}

func insertExecutionCommand(
	ctx context.Context,
	tx pgx.Tx,
	target executionTarget,
	showtime *catalogpb.Showtime,
	observedAt time.Time,
	now time.Time,
	allowAvailabilityRearm bool,
) error {
	payloadMessage := &executionpb.Payload{}
	payloadMessage.SetShowtime(showtime)
	payloadMessage.SetObservedAt(timestamppb.New(observedAt))
	payload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(payloadMessage)
	if err != nil {
		return fmt.Errorf("encode Client execution payload: %w", err)
	}
	id := "execution_" + contentHash([]byte(strings.Join([]string{
		target.userID, target.monitor.GetId(), showtime.GetId(), showtime.GetStartsAt().AsTime().UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	tag, err := tx.Exec(ctx, `
		INSERT INTO client_execution_commands AS command (
			id, user_id, monitor_id, showtime_id, starts_at, payload, observed_at, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $8)
		ON CONFLICT (user_id, monitor_id, showtime_id, starts_at) DO UPDATE SET
			payload = EXCLUDED.payload,
			observed_at = EXCLUDED.observed_at,
			status = 'queued',
			leased_installation_id = NULL,
			last_installation_id = NULL,
			lease_token_hash = NULL,
			lease_expires_at = NULL,
			attempt_count = 0,
			reason_code = '',
			completed_at = NULL,
			updated_at = EXCLUDED.updated_at
		WHERE $9
			AND command.status = 'failed'
			AND command.reason_code IN ($10, $11)
			AND command.starts_at > EXCLUDED.updated_at
			AND command.updated_at < EXCLUDED.observed_at
	`, id, target.userID, target.monitor.GetId(), showtime.GetId(), showtime.GetStartsAt().AsTime(), payload, observedAt, now,
		allowAvailabilityRearm, executionReasonPreferredSeatsUnavailable,
		executionReasonShowtimeUnavailable)
	if err != nil {
		return fmt.Errorf("enqueue Client execution command: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	reason := "showtime_observed"
	if allowAvailabilityRearm {
		reason = "availability_transition"
	}
	identity := strings.Join([]string{reason, observedAt.UTC().Format(time.RFC3339Nano)}, ":")
	return recordExecutionReadyEvent(
		ctx, tx, target.userID, id, target.monitor.GetId(), reason, identity, now,
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
	event := &clientpb.ExecutionReady{}
	event.SetCommandId(commandID)
	event.SetMonitorId(monitorID)
	event.SetReason(reason)
	eventPayload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode Client execution-ready event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ($1, $2, 'execution.ready', 'executions', $3, 1, $4, $5)
	`, clientEventID(userID, "execution.ready\x00"+commandID+"\x00"+identity),
		userID, commandID, eventPayload, now); err != nil {
		return fmt.Errorf("record Client execution-ready event: %w", err)
	}
	return nil
}
