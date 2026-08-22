package clientresources

import (
	"testing"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateResourcesUseCanonicalProto(t *testing.T) {
	t.Parallel()
	preset := validPresetPayload()
	presetResource := &clientpb.Resource{}
	presetResource.SetPreset(preset)
	if err := Validate("user", "presets", "preset", presetResource); err != nil {
		t.Fatalf("preset validation = %v", err)
	}
	monitor := validMonitorPayload()
	monitorResource := &clientpb.Resource{}
	monitorResource.SetMonitor(monitor)
	if err := Validate("user", "monitors", "monitor", monitorResource); err != nil {
		t.Fatalf("monitor validation = %v", err)
	}
	monitor.SetMovieId("")
	if err := Validate("user", "monitors", "monitor", monitorResource); err == nil {
		t.Fatal("monitor validation accepted a missing movie identity")
	}
	settingsResource := &clientpb.Resource{}
	settingsResource.SetSettings(&clientpb.Settings{})
	if err := Validate("user", "settings", "settings", settingsResource); err != nil {
		t.Fatalf("settings validation = %v", err)
	}
	reservationResource := &clientpb.Resource{}
	reservationResource.SetReservation(validReservationPayload())
	if err := Validate("user", "reservations", "reservation", reservationResource); err != nil {
		t.Fatalf("reservation validation = %v", err)
	}
	operationResource := &clientpb.Resource{}
	operationResource.SetExternalOperation(validExternalOperationPayload())
	if err := Validate("user", "external-operations", "operation", operationResource); err != nil {
		t.Fatalf("external operation validation = %v", err)
	}
	appEventResource := &clientpb.Resource{}
	appEventResource.SetAppEvent(validAppEventPayload())
	if err := Validate("user", "app-events", "event", appEventResource); err != nil {
		t.Fatalf("app event validation = %v", err)
	}
}

func TestValidateResourcesRejectCorruption(t *testing.T) {
	t.Parallel()
	preset := validPresetPayload()
	presetResource := &clientpb.Resource{}
	presetResource.SetPreset(preset)
	if err := Validate("user", "presets", "other", presetResource); err == nil {
		t.Fatal("mismatched preset identity was accepted")
	}
	preset.SetUserId("other")
	if err := Validate("user", "presets", "preset", presetResource); err == nil {
		t.Fatal("mismatched preset owner was accepted")
	}
	settingsResource := &clientpb.Resource{}
	settingsResource.SetSettings(&clientpb.Settings{})
	if err := Validate("user", "settings", "other", settingsResource); err == nil {
		t.Fatal("mismatched settings identity was accepted")
	}
	if err := Validate("user", "unknown-kind", "resource", settingsResource); err == nil {
		t.Fatal("unsupported resource kind was accepted")
	}
	invalidNetwork := &clientpb.NetworkSettings{}
	invalidSettings := &clientpb.Settings{}
	invalidSettings.SetNetwork(invalidNetwork)
	settingsResource.SetSettings(invalidSettings)
	if err := Validate("user", "settings", "settings", settingsResource); err == nil {
		t.Fatal("settings without a generated network mode were accepted")
	}
	reservationResource := &clientpb.Resource{}
	reservation := validReservationPayload()
	reservation.GetShowtime().SetEndsAt(reservation.GetShowtime().GetStartsAt())
	reservationResource.SetReservation(reservation)
	if err := Validate("user", "reservations", "reservation", reservationResource); err == nil {
		t.Fatal("reservation with a non-positive showtime duration was accepted")
	}
	monitorResource := &clientpb.Resource{}
	monitor := validMonitorPayload()
	monitor.SetSearchHorizonDays(15)
	monitorResource.SetMonitor(monitor)
	if err := Validate("user", "monitors", "monitor", monitorResource); err == nil {
		t.Fatal("monitor with an excessive search horizon was accepted")
	}
}

func validPresetPayload() *clientpb.Preset {
	preset := &clientpb.Preset{}
	preset.SetId("preset")
	preset.SetUserId("user")
	preset.SetName("Preset")
	preset.SetTheaterId("theater")
	preset.SetAuditoriumId("auditorium")
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	return preset
}

func validMonitorPayload() *clientpb.Monitor {
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(12)
	monitor := &clientpb.Monitor{}
	monitor.SetId("monitor")
	monitor.SetUserId("user")
	monitor.SetPresetId("preset")
	monitor.SetMovieId("movie_1")
	monitor.SetTargetDates([]*commonpb.LocalDate{date})
	monitor.SetSearchHorizonDays(14)
	monitor.SetState(state)
	return monitor
}

func validReservationPayload() *clientpb.Reservation {
	startsAt := time.Date(2026, 8, 22, 19, 0, 0, 0, time.UTC)
	movie := &catalogpb.Movie{}
	movie.SetId("movie_1")
	catalogdomain.SetMovieSourceKey(movie, "1")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId("auditorium_1")
	auditorium.SetTheaterId("theater_1")
	auditorium.SetCapacity(100)
	catalogdomain.SetAuditoriumSourceKey(auditorium, "1/1")
	showtime := &catalogpb.Showtime{}
	showtime.SetId("showtime_1")
	showtime.SetTheaterId("theater_1")
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	catalogdomain.SetShowtimeSourceKey(showtime, "1/2026-08-22/1/1")
	showtime.SetStartsAt(timestamppb.New(startsAt))
	showtime.SetEndsAt(timestamppb.New(startsAt.Add(2 * time.Hour)))
	showtime.SetAvailableSeats(50)
	showtime.SetCapacity(100)
	reservation := &clientpb.Reservation{}
	reservation.SetId("reservation")
	reservation.SetUserId("user")
	reservation.SetMonitorId("monitor")
	reservation.SetPrepared(&clientpb.ReservationPrepared{})
	reservation.SetShowtime(showtime)
	return reservation
}

func validExternalOperationPayload() *clientpb.ExternalOperation {
	operation := &clientpb.ExternalOperation{}
	operation.SetId("operation")
	operation.SetUserId("user")
	operation.SetReservationId("reservation")
	operation.SetCancellation(&clientpb.CancellationOperation{})
	operation.SetPrepared(&clientpb.OperationPrepared{})
	return operation
}

func validAppEventPayload() *clientpb.AppEvent {
	event := &clientpb.AppEvent{}
	event.SetId("event")
	event.SetUserId("user")
	event.SetKind("reservation.ready")
	event.SetInfo(&clientpb.EventInfo{})
	return event
}
