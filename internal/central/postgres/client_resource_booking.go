package postgres

import (
	"context"
	"fmt"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func loadClientReservation(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var monitorID, bookingNumber, totalPrice, refundAmount, state string
	var bookedAt, cancelledAt *time.Time
	if err := queryer.QueryRow(ctx, `
		SELECT monitor_id, booking_number, total_price, booked_at, cancelled_at,
			refund_amount, state
		FROM client_reservations WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(
		&monitorID, &bookingNumber, &totalPrice, &bookedAt, &cancelledAt, &refundAmount, &state,
	); err != nil {
		return nil, fmt.Errorf("read normalized Client reservation: %w", err)
	}
	reservation := &clientpb.Reservation{}
	reservation.SetId(id)
	reservation.SetUserId(userID)
	reservation.SetMonitorId(monitorID)
	reservation.SetBookingNumber(bookingNumber)
	reservation.SetTotalPrice(totalPrice)
	reservation.SetBookedAt(nullableProtoTimestamp(bookedAt))
	reservation.SetCancelledAt(nullableProtoTimestamp(cancelledAt))
	reservation.SetRefundAmount(refundAmount)
	if err := setClientReservationState(reservation, state); err != nil {
		return nil, err
	}
	seats, err := loadOrderedStrings(ctx, queryer, `
		SELECT seat_label FROM client_reservation_seats
		WHERE user_id = $1 AND reservation_id = $2 ORDER BY position
	`, userID, id)
	if err != nil {
		return nil, fmt.Errorf("read Client reservation seats: %w", err)
	}
	reservation.SetSeatLabels(seats)
	showtime, err := loadClientReservationShowtime(ctx, queryer, userID, id)
	if err != nil {
		return nil, err
	}
	reservation.SetShowtime(showtime)
	resource := &clientpb.Resource{}
	resource.SetReservation(reservation)
	return resource, nil
}

func loadClientReservationShowtime(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	reservationID string,
) (*catalogpb.Showtime, error) {
	var showtimeID, providerID, sourceKey, theaterID string
	var movieID, movieProviderID, movieSourceKey, movieTitle, moviePosterURL string
	var auditoriumID, auditoriumTheaterID, auditoriumSourceKey, auditoriumName, auditoriumLayoutHash string
	var auditoriumCapacity, availableSeats, capacity int32
	var scheduleDate, startsAt, endsAt time.Time
	var soldOut bool
	if err := queryer.QueryRow(ctx, `
		SELECT showtime_id, provider_id, source_key, theater_id,
			movie_id, movie_provider_id, movie_source_key, movie_title, movie_poster_url,
			auditorium_id, auditorium_theater_id, auditorium_source_key, auditorium_name,
			auditorium_capacity, auditorium_layout_hash, schedule_date, starts_at, ends_at,
			available_seats, capacity, sold_out
		FROM client_reservation_showtimes
		WHERE user_id = $1 AND reservation_id = $2
	`, userID, reservationID).Scan(
		&showtimeID, &providerID, &sourceKey, &theaterID,
		&movieID, &movieProviderID, &movieSourceKey, &movieTitle, &moviePosterURL,
		&auditoriumID, &auditoriumTheaterID, &auditoriumSourceKey, &auditoriumName,
		&auditoriumCapacity, &auditoriumLayoutHash, &scheduleDate, &startsAt, &endsAt,
		&availableSeats, &capacity, &soldOut,
	); err != nil {
		return nil, fmt.Errorf("read Client reservation showtime: %w", err)
	}
	screenTypes, err := loadOrderedStrings(ctx, queryer, `
		SELECT screen_type FROM client_reservation_showtime_screen_types
		WHERE user_id = $1 AND reservation_id = $2 ORDER BY position
	`, userID, reservationID)
	if err != nil {
		return nil, fmt.Errorf("read Client reservation auditorium screen types: %w", err)
	}
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(movieProviderID)
	if !catalogdomain.SetMovieSourceKey(movie, movieSourceKey) {
		return nil, fmt.Errorf("stored movie identity %q is not typed CGV", movieSourceKey)
	}
	movie.SetTitle(movieTitle)
	movie.SetPosterUrl(moviePosterURL)
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(auditoriumTheaterID)
	if !catalogdomain.SetAuditoriumSourceKey(auditorium, auditoriumSourceKey) {
		return nil, fmt.Errorf("stored auditorium identity %q is not typed CGV", auditoriumSourceKey)
	}
	auditorium.SetName(auditoriumName)
	auditorium.SetScreenTypes(screenTypes)
	auditorium.SetCapacity(auditoriumCapacity)
	auditorium.SetCurrentLayoutHash(auditoriumLayoutHash)
	showtime := &catalogpb.Showtime{}
	showtime.SetId(showtimeID)
	showtime.SetProviderId(providerID)
	if !catalogdomain.SetShowtimeSourceKey(showtime, sourceKey) {
		return nil, fmt.Errorf("stored showtime identity %q is not typed CGV", sourceKey)
	}
	showtime.SetTheaterId(theaterID)
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetStartsAt(timestamppb.New(startsAt))
	showtime.SetEndsAt(timestamppb.New(endsAt))
	showtime.SetAvailableSeats(availableSeats)
	showtime.SetCapacity(capacity)
	showtime.SetSoldOut(soldOut)
	return showtime, nil
}

func writeClientReservation(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	reservation := resource.body.GetReservation()
	if reservation == nil || reservation.GetShowtime() == nil || reservation.GetShowtime().GetMovie() == nil ||
		reservation.GetShowtime().GetAuditorium() == nil {
		return fmt.Errorf("client reservation showtime is required")
	}
	state, err := normalizedClientReservationState(reservation)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_reservations (
			user_id, resource_kind, id, monitor_id, booking_number, total_price,
			booked_at, cancelled_at, refund_amount, state
		) VALUES ($1, 'reservations', $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, id) DO UPDATE SET
			monitor_id = EXCLUDED.monitor_id,
			booking_number = EXCLUDED.booking_number,
			total_price = EXCLUDED.total_price,
			booked_at = EXCLUDED.booked_at,
			cancelled_at = EXCLUDED.cancelled_at,
			refund_amount = EXCLUDED.refund_amount,
			state = EXCLUDED.state
	`, resource.userID, resource.id, reservation.GetMonitorId(), reservation.GetBookingNumber(),
		reservation.GetTotalPrice(), protoTimestamp(reservation.GetBookedAt()),
		protoTimestamp(reservation.GetCancelledAt()), reservation.GetRefundAmount(), state); err != nil {
		return fmt.Errorf("write normalized Client reservation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_reservation_seats WHERE user_id = $1 AND reservation_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear Client reservation seats: %w", err)
	}
	for position, seat := range reservation.GetSeatLabels() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_reservation_seats (user_id, reservation_id, position, seat_label)
			VALUES ($1, $2, $3, $4)
		`, resource.userID, resource.id, position, seat); err != nil {
			return fmt.Errorf("write Client reservation seat: %w", err)
		}
	}
	if err := writeClientReservationShowtime(ctx, tx, resource.userID, resource.id, reservation.GetShowtime()); err != nil {
		return err
	}
	return nil
}

func writeClientReservationShowtime(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	reservationID string,
	showtime *catalogpb.Showtime,
) error {
	movie := showtime.GetMovie()
	auditorium := showtime.GetAuditorium()
	showtimeSourceKey, ok := catalogdomain.ShowtimeSourceKey(showtime)
	if !ok {
		return fmt.Errorf("reservation showtime identity is not typed CGV")
	}
	movieSourceKey, ok := catalogdomain.MovieSourceKey(movie)
	if !ok {
		return fmt.Errorf("reservation movie identity is not typed CGV")
	}
	auditoriumSourceKey, ok := catalogdomain.AuditoriumSourceKey(auditorium)
	if !ok {
		return fmt.Errorf("reservation auditorium identity is not typed CGV")
	}
	scheduleDate, err := clientLocalDateString(showtime.GetIdentity().GetCgv().GetScheduleDate())
	if err != nil {
		return fmt.Errorf("write Client reservation schedule date: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_reservation_showtimes (
			user_id, reservation_id, showtime_id, provider_id, source_key, theater_id,
			movie_id, movie_provider_id, movie_source_key, movie_title, movie_poster_url,
			auditorium_id, auditorium_theater_id, auditorium_source_key, auditorium_name,
			auditorium_capacity, auditorium_layout_hash, schedule_date, starts_at, ends_at,
			available_seats, capacity, sold_out
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		ON CONFLICT (user_id, reservation_id) DO UPDATE SET
			showtime_id = EXCLUDED.showtime_id,
			provider_id = EXCLUDED.provider_id,
			source_key = EXCLUDED.source_key,
			theater_id = EXCLUDED.theater_id,
			movie_id = EXCLUDED.movie_id,
			movie_provider_id = EXCLUDED.movie_provider_id,
			movie_source_key = EXCLUDED.movie_source_key,
			movie_title = EXCLUDED.movie_title,
			movie_poster_url = EXCLUDED.movie_poster_url,
			auditorium_id = EXCLUDED.auditorium_id,
			auditorium_theater_id = EXCLUDED.auditorium_theater_id,
			auditorium_source_key = EXCLUDED.auditorium_source_key,
			auditorium_name = EXCLUDED.auditorium_name,
			auditorium_capacity = EXCLUDED.auditorium_capacity,
			auditorium_layout_hash = EXCLUDED.auditorium_layout_hash,
			schedule_date = EXCLUDED.schedule_date,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			available_seats = EXCLUDED.available_seats,
			capacity = EXCLUDED.capacity,
			sold_out = EXCLUDED.sold_out
	`, userID, reservationID, showtime.GetId(), showtime.GetProviderId(), showtimeSourceKey,
		showtime.GetTheaterId(), movie.GetId(), movie.GetProviderId(), movieSourceKey, movie.GetTitle(),
		movie.GetPosterUrl(), auditorium.GetId(), auditorium.GetTheaterId(), auditoriumSourceKey,
		auditorium.GetName(), auditorium.GetCapacity(), auditorium.GetCurrentLayoutHash(),
		scheduleDate, showtime.GetStartsAt().AsTime(), showtime.GetEndsAt().AsTime(), showtime.GetAvailableSeats(),
		showtime.GetCapacity(), showtime.GetSoldOut()); err != nil {
		return fmt.Errorf("write Client reservation showtime: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_reservation_showtime_screen_types
		WHERE user_id = $1 AND reservation_id = $2
	`, userID, reservationID); err != nil {
		return fmt.Errorf("clear Client reservation auditorium screen types: %w", err)
	}
	for position, screenType := range auditorium.GetScreenTypes() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_reservation_showtime_screen_types (
				user_id, reservation_id, position, screen_type
			) VALUES ($1, $2, $3, $4)
		`, userID, reservationID, position, screenType); err != nil {
			return fmt.Errorf("write Client reservation auditorium screen type: %w", err)
		}
	}
	return nil
}

func normalizedClientReservationState(reservation *clientpb.Reservation) (string, error) {
	switch {
	case reservation.HasPrepared():
		return "prepared", nil
	case reservation.HasBooked():
		return "booked", nil
	case reservation.HasCancellationCommitting():
		return "cancellation-committing", nil
	case reservation.HasCancellationUnknown():
		return "cancellation-unknown", nil
	case reservation.HasCancelled():
		return "cancelled", nil
	default:
		return "", fmt.Errorf("client reservation state is required")
	}
}

func setClientReservationState(reservation *clientpb.Reservation, state string) error {
	var setState func()
	switch state {
	case "prepared":
		setState = func() { reservation.SetPrepared(&clientpb.ReservationPrepared{}) }
	case "booked":
		setState = func() { reservation.SetBooked(&clientpb.ReservationBooked{}) }
	case "cancellation-committing":
		setState = func() { reservation.SetCancellationCommitting(&clientpb.ReservationCancellationCommitting{}) }
	case "cancellation-unknown":
		setState = func() { reservation.SetCancellationUnknown(&clientpb.ReservationCancellationUnknown{}) }
	case "cancelled":
		setState = func() { reservation.SetCancelled(&clientpb.ReservationCancelled{}) }
	default:
		return fmt.Errorf("unknown normalized client reservation state %q", state)
	}
	setState()
	return nil
}

func loadClientExternalOperation(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var monitorID, reservationID, refundAmount, lastError, kind, state string
	var createdAt, updatedAt *time.Time
	if err := queryer.QueryRow(ctx, `
		SELECT COALESCE(monitor_id, ''), reservation_id, refund_amount, last_error,
			operation_created_at, operation_updated_at, kind, state
		FROM client_external_operations WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(
		&monitorID, &reservationID, &refundAmount, &lastError,
		&createdAt, &updatedAt, &kind, &state,
	); err != nil {
		return nil, fmt.Errorf("read normalized Client external operation: %w", err)
	}
	operation := &clientpb.ExternalOperation{}
	operation.SetId(id)
	operation.SetUserId(userID)
	operation.SetMonitorId(monitorID)
	operation.SetReservationId(reservationID)
	operation.SetRefundAmount(refundAmount)
	operation.SetLastError(lastError)
	operation.SetCreatedAt(nullableProtoTimestamp(createdAt))
	operation.SetUpdatedAt(nullableProtoTimestamp(updatedAt))
	if kind != "cancellation" {
		return nil, fmt.Errorf("unknown normalized Client operation kind %q", kind)
	}
	operation.SetCancellation(&clientpb.CancellationOperation{})
	if err := setClientExternalOperationState(operation, state); err != nil {
		return nil, err
	}
	resource := &clientpb.Resource{}
	resource.SetExternalOperation(operation)
	return resource, nil
}

func writeClientExternalOperation(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	operation := resource.body.GetExternalOperation()
	if operation == nil || !operation.HasCancellation() {
		return fmt.Errorf("client cancellation operation is required")
	}
	state, err := normalizedClientExternalOperationState(operation)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_external_operations (
			user_id, resource_kind, id, monitor_id, reservation_id, refund_amount,
			last_error, operation_created_at, operation_updated_at, kind, state
		) VALUES ($1, 'external-operations', $2, $3, $4, $5, $6, $7, $8, 'cancellation', $9)
		ON CONFLICT (user_id, id) DO UPDATE SET
			monitor_id = EXCLUDED.monitor_id,
			reservation_id = EXCLUDED.reservation_id,
			refund_amount = EXCLUDED.refund_amount,
			last_error = EXCLUDED.last_error,
			operation_created_at = EXCLUDED.operation_created_at,
			operation_updated_at = EXCLUDED.operation_updated_at,
			kind = EXCLUDED.kind,
			state = EXCLUDED.state
	`, resource.userID, resource.id, nullableText(operation.GetMonitorId()), operation.GetReservationId(),
		operation.GetRefundAmount(), operation.GetLastError(), protoTimestamp(operation.GetCreatedAt()),
		protoTimestamp(operation.GetUpdatedAt()), state); err != nil {
		return fmt.Errorf("write normalized Client external operation: %w", err)
	}
	return nil
}

func normalizedClientExternalOperationState(operation *clientpb.ExternalOperation) (string, error) {
	switch {
	case operation.HasPrepared():
		return "prepared", nil
	case operation.HasUnknown():
		return "unknown", nil
	case operation.HasAttentionRequired():
		return "attention-required", nil
	case operation.HasConfirmed():
		return "confirmed", nil
	case operation.HasReconciled():
		return "reconciled", nil
	default:
		return "", fmt.Errorf("client external operation state is required")
	}
}

func setClientExternalOperationState(operation *clientpb.ExternalOperation, state string) error {
	switch state {
	case "prepared":
		operation.SetPrepared(&clientpb.OperationPrepared{})
	case "unknown":
		operation.SetUnknown(&clientpb.OperationUnknown{})
	case "attention-required":
		operation.SetAttentionRequired(&clientpb.OperationAttentionRequired{})
	case "confirmed":
		operation.SetConfirmed(&clientpb.OperationConfirmed{})
	case "reconciled":
		operation.SetReconciled(&clientpb.OperationReconciled{})
	default:
		return fmt.Errorf("unknown normalized client external operation state %q", state)
	}
	return nil
}

func loadClientAppEvent(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var kind, message, tone string
	var createdAt *time.Time
	var readAt *time.Time
	if err := queryer.QueryRow(ctx, `
		SELECT kind, message, event_created_at, read_at, tone
		FROM client_app_events WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(&kind, &message, &createdAt, &readAt, &tone); err != nil {
		return nil, fmt.Errorf("read normalized Client app event: %w", err)
	}
	event := &clientpb.AppEvent{}
	event.SetId(id)
	event.SetUserId(userID)
	event.SetKind(kind)
	event.SetMessage(message)
	event.SetCreatedAt(nullableProtoTimestamp(createdAt))
	event.SetReadAt(nullableProtoTimestamp(readAt))
	if err := setClientAppEventTone(event, tone); err != nil {
		return nil, err
	}
	resource := &clientpb.Resource{}
	resource.SetAppEvent(event)
	return resource, nil
}

func writeClientAppEvent(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	event := resource.body.GetAppEvent()
	if event == nil {
		return fmt.Errorf("client app event is required")
	}
	tone, err := normalizedClientAppEventTone(event)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_app_events (
			user_id, resource_kind, id, kind, message, event_created_at, read_at, tone
		) VALUES ($1, 'app-events', $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, id) DO UPDATE SET
			kind = EXCLUDED.kind,
			message = EXCLUDED.message,
			event_created_at = EXCLUDED.event_created_at,
			read_at = EXCLUDED.read_at,
			tone = EXCLUDED.tone
	`, resource.userID, resource.id, event.GetKind(), event.GetMessage(),
		protoTimestamp(event.GetCreatedAt()), protoTimestamp(event.GetReadAt()), tone); err != nil {
		return fmt.Errorf("write normalized Client app event: %w", err)
	}
	return nil
}

func normalizedClientAppEventTone(event *clientpb.AppEvent) (string, error) {
	switch {
	case event.HasInfo():
		return "info", nil
	case event.HasSuccess():
		return "success", nil
	case event.HasWarning():
		return "warning", nil
	case event.HasError():
		return "error", nil
	default:
		return "", fmt.Errorf("client app event tone is required")
	}
}

func setClientAppEventTone(event *clientpb.AppEvent, tone string) error {
	switch tone {
	case "info":
		event.SetInfo(&clientpb.EventInfo{})
	case "success":
		event.SetSuccess(&clientpb.EventSuccess{})
	case "warning":
		event.SetWarning(&clientpb.EventWarning{})
	case "error":
		event.SetError(&clientpb.EventError{})
	default:
		return fmt.Errorf("unknown normalized Client app event tone %q", tone)
	}
	return nil
}
