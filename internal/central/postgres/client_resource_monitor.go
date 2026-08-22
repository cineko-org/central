package postgres

import (
	"context"
	"fmt"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"

	"github.com/jackc/pgx/v5"
)

func loadClientMonitor(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var presetID, movieID, movieTitle, state, stateReason, reservationID string
	var searchHorizonDays int32
	var earliestMinute, latestMinute *int32
	var lastCheckedAt, createdAt, updatedAt *time.Time
	if err := queryer.QueryRow(ctx, `
		SELECT preset_id, movie_id, movie_title, search_horizon_days,
			earliest_minute, latest_minute,
			state, state_reason, last_checked_at, COALESCE(reservation_id, ''),
			monitor_created_at, monitor_updated_at
		FROM client_monitors WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(
		&presetID, &movieID, &movieTitle, &searchHorizonDays,
		&earliestMinute, &latestMinute,
		&state, &stateReason, &lastCheckedAt, &reservationID, &createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("read normalized Client monitor: %w", err)
	}
	monitor := &clientpb.Monitor{}
	monitor.SetId(id)
	monitor.SetUserId(userID)
	monitor.SetPresetId(presetID)
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle(movieTitle)
	monitor.SetSearchHorizonDays(searchHorizonDays)
	monitor.SetEarliestTime(clientLocalTime(earliestMinute))
	monitor.SetLatestTime(clientLocalTime(latestMinute))
	monitorState, err := clientMonitorState(state, stateReason)
	if err != nil {
		return nil, err
	}
	monitor.SetState(monitorState)
	monitor.SetLastCheckedAt(nullableProtoTimestamp(lastCheckedAt))
	monitor.SetReservationId(reservationID)
	monitor.SetCreatedAt(nullableProtoTimestamp(createdAt))
	monitor.SetUpdatedAt(nullableProtoTimestamp(updatedAt))
	targetDates, err := loadClientMonitorTargetDates(ctx, queryer, userID, id)
	if err != nil {
		return nil, err
	}
	monitor.SetTargetDates(targetDates)
	targetWeekdays, err := loadClientMonitorTargetWeekdays(ctx, queryer, userID, id)
	if err != nil {
		return nil, err
	}
	monitor.SetTargetWeekdays(targetWeekdays)
	resource := &clientpb.Resource{}
	resource.SetMonitor(monitor)
	return resource, nil
}

func clientMonitorState(value string, reason string) (*clientpb.MonitorState, error) {
	state := &clientpb.MonitorState{}
	switch value {
	case "pending":
		state.SetPending(&clientpb.MonitorPending{})
	case "running":
		state.SetRunning(&clientpb.MonitorRunning{})
	case "triggered":
		state.SetTriggered(&clientpb.MonitorTriggered{})
	case "payment-unknown":
		state.SetPaymentUnknown(&clientpb.MonitorPaymentUnknown{})
	case "booked":
		state.SetBooked(&clientpb.MonitorBooked{})
	case "failed":
		failed := &clientpb.MonitorFailed{}
		failed.SetReason(reason)
		state.SetFailed(failed)
	case "stopped":
		state.SetStopped(&clientpb.MonitorStopped{})
	default:
		return nil, fmt.Errorf("unknown normalized client monitor state %q", value)
	}
	return state, nil
}

func loadClientMonitorTargetDates(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	monitorID string,
) ([]*commonpb.LocalDate, error) {
	rows, err := queryer.Query(ctx, `
		SELECT target_date::text FROM client_monitor_target_dates
		WHERE user_id = $1 AND monitor_id = $2 ORDER BY position
	`, userID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("list Client monitor target dates: %w", err)
	}
	defer rows.Close()
	values := make([]*commonpb.LocalDate, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan Client monitor target date: %w", err)
		}
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return nil, fmt.Errorf("parse Client monitor target date: %w", err)
		}
		date, err := clientLocalDateFromTime(parsed)
		if err != nil {
			return nil, fmt.Errorf("normalize client monitor target date: %w", err)
		}
		values = append(values, date)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client monitor target dates: %w", err)
	}
	return values, nil
}

func loadClientMonitorTargetWeekdays(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	monitorID string,
) ([]int32, error) {
	rows, err := queryer.Query(ctx, `
		SELECT target_weekday FROM client_monitor_target_weekdays
		WHERE user_id = $1 AND monitor_id = $2 ORDER BY position
	`, userID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("list Client monitor target weekdays: %w", err)
	}
	defer rows.Close()
	values := make([]int32, 0)
	for rows.Next() {
		var value int32
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan Client monitor target weekday: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client monitor target weekdays: %w", err)
	}
	return values, nil
}

func writeClientMonitor(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	monitor := resource.body.GetMonitor()
	if monitor == nil {
		return fmt.Errorf("client monitor is required")
	}
	state, stateReason, err := normalizedClientMonitorState(monitor.GetState())
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_monitors (
			user_id, resource_kind, id, preset_id, movie_id, movie_title,
			search_horizon_days, earliest_minute, latest_minute,
			state, state_reason,
			last_checked_at, reservation_id, monitor_created_at, monitor_updated_at
		) VALUES ($1, 'monitors', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (user_id, id) DO UPDATE SET
			preset_id = EXCLUDED.preset_id,
			movie_id = EXCLUDED.movie_id,
			movie_title = EXCLUDED.movie_title,
			search_horizon_days = EXCLUDED.search_horizon_days,
			earliest_minute = EXCLUDED.earliest_minute,
			latest_minute = EXCLUDED.latest_minute,
			state = EXCLUDED.state,
			state_reason = EXCLUDED.state_reason,
			last_checked_at = EXCLUDED.last_checked_at,
			reservation_id = EXCLUDED.reservation_id,
			monitor_created_at = EXCLUDED.monitor_created_at,
			monitor_updated_at = EXCLUDED.monitor_updated_at
	`, resource.userID, resource.id, monitor.GetPresetId(), monitor.GetMovieId(), monitor.GetMovieTitle(),
		monitor.GetSearchHorizonDays(), normalizedClientLocalTime(monitor.GetEarliestTime()),
		normalizedClientLocalTime(monitor.GetLatestTime()), state, stateReason,
		protoTimestamp(monitor.GetLastCheckedAt()), nullableText(monitor.GetReservationId()),
		protoTimestamp(monitor.GetCreatedAt()), protoTimestamp(monitor.GetUpdatedAt())); err != nil {
		return fmt.Errorf("write normalized Client monitor: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_monitor_target_dates WHERE user_id = $1 AND monitor_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear Client monitor target dates: %w", err)
	}
	for position, value := range monitor.GetTargetDates() {
		date, err := clientLocalDateString(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_monitor_target_dates (user_id, monitor_id, position, target_date)
			VALUES ($1, $2, $3, $4::date)
		`, resource.userID, resource.id, position, date); err != nil {
			return fmt.Errorf("write Client monitor target date: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_monitor_target_weekdays WHERE user_id = $1 AND monitor_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear Client monitor target weekdays: %w", err)
	}
	for position, value := range monitor.GetTargetWeekdays() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_monitor_target_weekdays (user_id, monitor_id, position, target_weekday)
			VALUES ($1, $2, $3, $4)
		`, resource.userID, resource.id, position, value); err != nil {
			return fmt.Errorf("write Client monitor target weekday: %w", err)
		}
	}
	return nil
}

func normalizedClientMonitorState(state *clientpb.MonitorState) (string, string, error) {
	switch {
	case state == nil:
		return "", "", fmt.Errorf("client monitor state is required")
	case state.HasPending():
		return "pending", "", nil
	case state.HasRunning():
		return "running", "", nil
	case state.HasTriggered():
		return "triggered", "", nil
	case state.HasPaymentUnknown():
		return "payment-unknown", "", nil
	case state.HasBooked():
		return "booked", "", nil
	case state.HasFailed():
		return "failed", state.GetFailed().GetReason(), nil
	case state.HasStopped():
		return "stopped", "", nil
	default:
		return "", "", fmt.Errorf("client monitor state is required")
	}
}

func normalizedClientLocalTime(value *commonpb.LocalTime) *int32 {
	if value == nil {
		return nil
	}
	minutes := value.GetHour()*60 + value.GetMinute()
	return &minutes
}

func clientLocalTime(value *int32) *commonpb.LocalTime {
	if value == nil {
		return nil
	}
	result := &commonpb.LocalTime{}
	result.SetHour(*value / 60)
	result.SetMinute(*value % 60)
	return result
}

func clientLocalDateFromTime(value time.Time) (*commonpb.LocalDate, error) {
	year, err := clientDatePart(value.Year())
	if err != nil {
		return nil, err
	}
	month, err := clientDatePart(int(value.Month()))
	if err != nil {
		return nil, err
	}
	day, err := clientDatePart(value.Day())
	if err != nil {
		return nil, err
	}
	date := &commonpb.LocalDate{}
	date.SetYear(year)
	date.SetMonth(month)
	date.SetDay(day)
	return date, nil
}

func clientDatePart(value int) (int32, error) {
	if value < 0 || value > 9999 {
		return 0, fmt.Errorf("client date component %d is out of range", value)
	}
	return int32(value), nil
}

func clientLocalDateString(value *commonpb.LocalDate) (string, error) {
	if value == nil {
		return "", fmt.Errorf("client monitor target date is required")
	}
	date := time.Date(int(value.GetYear()), time.Month(value.GetMonth()), int(value.GetDay()), 0, 0, 0, 0, time.UTC)
	year, err := clientDatePart(date.Year())
	if err != nil {
		return "", err
	}
	month, err := clientDatePart(int(date.Month()))
	if err != nil {
		return "", err
	}
	day, err := clientDatePart(date.Day())
	if err != nil {
		return "", err
	}
	if year != value.GetYear() || month != value.GetMonth() || day != value.GetDay() {
		return "", fmt.Errorf("invalid Client monitor target date")
	}
	return date.Format(time.DateOnly), nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
