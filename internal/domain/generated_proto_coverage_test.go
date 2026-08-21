package domain

import (
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestGeneratedProtoPresetValidationBoundaries(t *testing.T) {
	if ValidatePreset(nil, nil) == nil {
		t.Fatal("nil preset accepted")
	}
	base := validPresetProto()
	for _, mutate := range []func(*clientpb.Preset){
		func(value *clientpb.Preset) { value.SetId("") },
		func(value *clientpb.Preset) { value.SetUserId("") },
		func(value *clientpb.Preset) { value.SetName(" ") },
		func(value *clientpb.Preset) { value.SetTheaterId("") },
		func(value *clientpb.Preset) { value.SetAuditoriumId("") },
		func(value *clientpb.Preset) { value.SetSeatCount(0) },
		func(value *clientpb.Preset) { value.SetSeatCount(9) },
	} {
		candidate := clonePreset(base)
		mutate(candidate)
		if ValidatePreset(candidate, nil) == nil {
			t.Fatalf("invalid preset accepted: %+v", candidate)
		}
	}
	noPreference := clonePreset(base)
	noPreference.SetSeatPreference(nil)
	if err := ValidatePreset(noPreference, nil); err != nil {
		t.Fatalf("nil seat preference = %v", err)
	}
	seatMap := SeatMap{AuditoriumID: base.GetAuditoriumId(), Seats: []Seat{{Label: "A1"}}}
	withExplicit := clonePreset(base)
	withExplicit.GetSeatPreference().SetExplicitSeats([]string{"A1"})
	if err := ValidatePreset(withExplicit, &seatMap); err != nil {
		t.Fatalf("valid explicit seat = %v", err)
	}
	seatMap.AuditoriumID = "other"
	if ValidatePreset(withExplicit, &seatMap) == nil {
		t.Fatal("mismatched seat map accepted")
	}
	seatMap.AuditoriumID = base.GetAuditoriumId()
	withExplicit.GetSeatPreference().SetExplicitSeats([]string{"missing"})
	if ValidatePreset(withExplicit, &seatMap) == nil {
		t.Fatal("missing explicit seat accepted")
	}

	zone := &clientpb.SeatZone{}
	unnamedPreference := &clientpb.SeatPreference{}
	unnamedPreference.SetPreferredZones([]*clientpb.SeatZone{zone})
	if validateSeatPreference(unnamedPreference) == nil {
		t.Fatal("unnamed preference zone accepted")
	}
	zone.SetName("valid")
	zone.SetMinX(0)
	zone.SetMaxX(1)
	zone.SetMinY(0)
	zone.SetMaxY(1)
	preference := &clientpb.SeatPreference{}
	preference.SetPreferredZones([]*clientpb.SeatZone{zone})
	preference.SetPreferredTypes([]string{string(SeatTypeStandard)})
	if err := validateSeatPreference(preference); err != nil {
		t.Fatalf("valid preference = %v", err)
	}
	for _, mutate := range []func(*clientpb.SeatZone){
		func(value *clientpb.SeatZone) { value.SetMinX(-.1) },
		func(value *clientpb.SeatZone) { value.SetMaxX(1.1) },
		func(value *clientpb.SeatZone) { value.SetMinY(-.1) },
		func(value *clientpb.SeatZone) { value.SetMaxY(1.1) },
		func(value *clientpb.SeatZone) { value.SetMinX(.8); value.SetMaxX(.2) },
		func(value *clientpb.SeatZone) { value.SetMinY(.8); value.SetMaxY(.2) },
	} {
		candidate := protoCloneZone(zone)
		mutate(candidate)
		candidatePreference := &clientpb.SeatPreference{}
		candidatePreference.SetPreferredZones([]*clientpb.SeatZone{candidate})
		if validateSeatPreference(candidatePreference) == nil {
			t.Fatalf("invalid zone accepted: %+v", candidate)
		}
	}
	unknownPreference := &clientpb.SeatPreference{}
	unknownPreference.SetPreferredTypes([]string{string(SeatTypeUnknown)})
	if validateSeatPreference(unknownPreference) != nil {
		t.Fatal("unknown enum seat type should be valid")
	}
}

func clonePreset(value *clientpb.Preset) *clientpb.Preset {
	copy := &clientpb.Preset{}
	copy.SetId(value.GetId())
	copy.SetUserId(value.GetUserId())
	copy.SetName(value.GetName())
	copy.SetTheaterId(value.GetTheaterId())
	copy.SetAuditoriumId(value.GetAuditoriumId())
	copy.SetSeatCount(value.GetSeatCount())
	copy.SetSeatPreference(value.GetSeatPreference())
	return copy
}

func protoCloneZone(value *clientpb.SeatZone) *clientpb.SeatZone {
	copy := &clientpb.SeatZone{}
	copy.SetName(value.GetName())
	copy.SetMinX(value.GetMinX())
	copy.SetMaxX(value.GetMaxX())
	copy.SetMinY(value.GetMinY())
	copy.SetMaxY(value.GetMaxY())
	return copy
}

func TestGeneratedProtoMonitorValidationBoundaries(t *testing.T) {
	if protoDuration(nil) != 0 {
		t.Fatal("nil duration was not zero")
	}
	if ValidateMonitor(nil) == nil {
		t.Fatal("nil monitor accepted")
	}
	base := validMonitorProto()
	for _, mutate := range []func(*clientpb.Monitor){
		func(value *clientpb.Monitor) { value.SetId("") },
		func(value *clientpb.Monitor) { value.SetUserId("") },
		func(value *clientpb.Monitor) { value.SetPresetId("") },
		func(value *clientpb.Monitor) { value.SetMovieId("") },
		func(value *clientpb.Monitor) { value.SetTargetDates(nil); value.SetTargetWeekdays(nil) },
		func(value *clientpb.Monitor) { value.SetMode(&clientpb.MonitorMode{}) },
		func(value *clientpb.Monitor) {
			mode := &clientpb.MonitorMode{}
			mode.SetCancellation(&clientpb.CancellationMonitor{})
			value.SetMode(mode)
			value.SetTargetWeekdays([]int32{int32(time.Monday)})
		},
		func(value *clientpb.Monitor) { value.SetPollInterval(durationpb.New(time.Second)) },
		func(value *clientpb.Monitor) {
			value.SetMaximumPollInterval(durationpb.New(5 * time.Second))
			value.SetPollInterval(durationpb.New(5 * time.Second))
		},
		func(value *clientpb.Monitor) { value.SetTargetDates([]*commonpb.LocalDate{nil}) },
		func(value *clientpb.Monitor) { value.SetTargetWeekdays([]int32{8}) },
		func(value *clientpb.Monitor) {
			value.SetTargetWeekdays([]int32{int32(time.Monday), int32(time.Monday)})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetWeekdays([]int32{int32(time.Monday)})
			value.SetSearchHorizonDays(0)
		},
		func(value *clientpb.Monitor) {
			bad := &commonpb.LocalTime{}
			bad.SetHour(24)
			value.SetEarliestTime(bad)
		},
	} {
		candidate := cloneMonitor(base)
		mutate(candidate)
		if ValidateMonitor(candidate) == nil {
			t.Fatalf("invalid monitor accepted: %+v", candidate)
		}
	}
	valid := cloneMonitor(base)
	valid.SetEarliestTime(nil)
	valid.SetLatestTime(nil)
	if err := ValidateMonitor(valid); err != nil {
		t.Fatalf("open time window = %v", err)
	}
	cancellation := cloneMonitor(base)
	cancellationMode := &clientpb.MonitorMode{}
	cancellationMode.SetCancellation(&clientpb.CancellationMonitor{})
	cancellation.SetMode(cancellationMode)
	if err := ValidateMonitor(cancellation); err != nil {
		t.Fatalf("exact-date cancellation monitor = %v", err)
	}
	if err := validateTargetWeekdays(nil, 0); err != nil {
		t.Fatalf("empty weekdays = %v", err)
	}
	if err := validateTargetWeekdays([]int32{int32(time.Sunday)}, 1); err != nil {
		t.Fatalf("valid weekday = %v", err)
	}
	if err := validateTimeWindow(nil, nil); err != nil {
		t.Fatalf("empty time window = %v", err)
	}
	if err := validateTimeWindow(&commonpb.LocalTime{}, nil); err != nil {
		t.Fatalf("valid midnight lower bound = %v", err)
	}
	earliestOnly := &commonpb.LocalTime{}
	earliestOnly.SetHour(6)
	if err := validateTimeWindow(earliestOnly, nil); err != nil {
		t.Fatalf("earliest-only time window = %v", err)
	}
	equal := &commonpb.LocalTime{}
	equal.SetHour(1)
	if validateTimeWindow(equal, equal) == nil {
		t.Fatal("equal time window accepted")
	}

	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, location)
	if MonitorTargetDates(nil, now) != nil || MonitorExpired(nil, now) {
		t.Fatal("nil monitor date state incorrect")
	}
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(9)
	future := &commonpb.LocalDate{}
	future.SetYear(2026)
	future.SetMonth(8)
	future.SetDay(11)
	dateMonitor := cloneMonitor(base)
	dateMonitor.SetTargetDates([]*commonpb.LocalDate{date, future, nil})
	dateMonitor.SetTargetWeekdays(nil)
	if MonitorExpired(dateMonitor, now) {
		t.Fatal("future explicit date considered expired")
	}
	dateMonitor.SetTargetDates([]*commonpb.LocalDate{date, nil})
	if !MonitorExpired(dateMonitor, now) {
		t.Fatal("past and invalid explicit dates not expired")
	}
	if _, err := localDate(nil, location); err == nil {
		t.Fatal("nil local date accepted")
	}
	if _, err := localDate(&commonpb.LocalDate{}, location); err == nil {
		t.Fatal("invalid local date accepted")
	}
	dateWithNilLocation := &commonpb.LocalDate{}
	dateWithNilLocation.SetYear(2026)
	dateWithNilLocation.SetMonth(8)
	dateWithNilLocation.SetDay(10)
	if _, err := localDate(dateWithNilLocation, nil); err == nil {
		t.Fatal("nil local date location accepted")
	}
	if _, set, valid := localMinutes(nil); set || !valid {
		t.Fatal("nil local time state incorrect")
	}
	badTime := &commonpb.LocalTime{}
	badTime.SetMinute(60)
	if _, set, valid := localMinutes(badTime); !set || valid {
		t.Fatal("invalid local time accepted")
	}
	invalidWindow := &clientpb.Monitor{}
	invalidWindow.SetEarliestTime(badTime)
	if monitorMatchesTimeWindow(invalidWindow, time.Date(2026, 8, 15, 6, 0, 0, 0, location), location) {
		t.Fatal("invalid monitor window accepted")
	}
	window := &clientpb.Monitor{}
	window.SetEarliestTime(protoTime(6))
	if !monitorMatchesTimeWindow(window, time.Date(2026, 8, 15, 6, 0, 0, 0, location), location) {
		t.Fatal("earliest-only monitor window rejected a late start")
	}
}

func cloneMonitor(value *clientpb.Monitor) *clientpb.Monitor {
	copy := &clientpb.Monitor{}
	copy.SetId(value.GetId())
	copy.SetUserId(value.GetUserId())
	copy.SetPresetId(value.GetPresetId())
	copy.SetMovieId(value.GetMovieId())
	copy.SetMode(value.GetMode())
	copy.SetTargetDates(value.GetTargetDates())
	copy.SetTargetWeekdays(value.GetTargetWeekdays())
	copy.SetSearchHorizonDays(value.GetSearchHorizonDays())
	copy.SetEarliestTime(value.GetEarliestTime())
	copy.SetLatestTime(value.GetLatestTime())
	copy.SetPollInterval(value.GetPollInterval())
	copy.SetMaximumPollInterval(value.GetMaximumPollInterval())
	return copy
}
