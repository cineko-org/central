package domain

import (
	"reflect"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
)

func validMonitorProto() *clientpb.Monitor {
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	monitor := &clientpb.Monitor{}
	monitor.SetId("m1")
	monitor.SetUserId("u1")
	monitor.SetPresetId("p1")
	monitor.SetMovieId("movie_1")
	monitor.SetState(state)
	monitor.SetSearchHorizonDays(DefaultSearchHorizonDays)
	monitor.SetTargetDates([]*commonpb.LocalDate{protoDate(8, 10)})
	return monitor
}

func protoDate(month, day int32) *commonpb.LocalDate {
	value := &commonpb.LocalDate{}
	value.SetYear(2026)
	value.SetMonth(month)
	value.SetDay(day)
	return value
}

func protoTime(hour int32) *commonpb.LocalTime {
	value := &commonpb.LocalTime{}
	value.SetHour(hour)
	value.SetMinute(0)
	return value
}

func TestValidateMonitorChecksIdentityScheduleAndTimeWindow(t *testing.T) {
	t.Parallel()
	monitor := validMonitorProto()
	monitor.SetEarliestTime(protoTime(20))
	monitor.SetLatestTime(protoTime(18))
	if err := ValidateMonitor(monitor); err != nil {
		t.Fatalf("ValidateMonitor() rejected an overnight window: %v", err)
	}
	monitor.SetLatestTime(protoTime(20))
	if err := ValidateMonitor(monitor); err == nil {
		t.Fatal("ValidateMonitor() accepted an empty window")
	}
	monitor.SetLatestTime(protoTime(21))
	monitor.SetMovieId("")
	if err := ValidateMonitor(monitor); err == nil {
		t.Fatal("ValidateMonitor() accepted a missing movie identity")
	}
	monitor.SetMovieId("movie_1")
	monitor.SetTargetDates([]*commonpb.LocalDate{protoDate(2, 30)})
	if err := ValidateMonitor(monitor); err == nil {
		t.Fatal("ValidateMonitor() accepted an invalid calendar date")
	}
}

func TestMonitorTimeWindowSupportsOvernightAndOpenBounds(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	monitor := &clientpb.Monitor{}
	monitor.SetEarliestTime(protoTime(21))
	monitor.SetLatestTime(protoTime(6))
	cases := []struct {
		hour, minute int
		want         bool
	}{
		{20, 59, false}, {21, 0, true}, {23, 59, true}, {1, 0, true}, {6, 0, false},
	}
	for _, test := range cases {
		start := time.Date(2026, time.August, 15, test.hour, test.minute, 0, 0, location)
		if got := monitorMatchesTimeWindow(monitor, start, location); got != test.want {
			t.Fatalf("monitorMatchesTimeWindow(%s) = %t, want %t", start, got, test.want)
		}
	}
	monitor.SetEarliestTime(nil)
	monitor.SetLatestTime(protoTime(6))
	if !monitorMatchesTimeWindow(monitor, time.Date(2026, 8, 15, 5, 59, 0, 0, location), location) {
		t.Fatal("latest-only window rejected a time before its end")
	}
	monitor.SetLatestTime(nil)
	if !monitorMatchesTimeWindow(monitor, time.Date(2026, 8, 15, 12, 0, 0, 0, location), location) {
		t.Fatal("unrestricted window rejected a time")
	}
}

func TestMonitorScheduleUsesLocalCalendarDate(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	monitor := validMonitorProto()
	monitor.SetTargetDates(nil)
	monitor.SetTargetWeekdays([]int32{int32(time.Saturday)})
	monitor.SetSearchHorizonDays(2)
	monitor.SetEarliestTime(protoTime(0))
	monitor.SetLatestTime(protoTime(6))
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	saturday := time.Date(2026, 8, 15, 1, 0, 0, 0, location)
	if !MonitorMatchesSchedule(monitor, saturday, now, location) {
		t.Fatal("MonitorMatchesSchedule() rejected Saturday 01:00")
	}
}

func TestMonitorTargetDatesAndExpiry(t *testing.T) {
	t.Parallel()
	monitor := validMonitorProto()
	monitor.SetTargetDates([]*commonpb.LocalDate{protoDate(8, 20)})
	monitor.SetTargetWeekdays([]int32{int32(time.Monday), int32(time.Saturday)})
	monitor.SetSearchHorizonDays(7)
	now := time.Date(2026, 8, 9, 18, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	want := []string{"2026-08-10", "2026-08-15", "2026-08-20"}
	if got := MonitorTargetDates(monitor, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("MonitorTargetDates() = %v, want %v", got, want)
	}
	monitor.SetTargetWeekdays(nil)
	monitor.SetTargetDates([]*commonpb.LocalDate{protoDate(8, 7), protoDate(8, 8)})
	if !MonitorExpired(monitor, now) {
		t.Fatal("MonitorExpired() did not reject dates before today")
	}
}

func TestMonitorHorizonIsBoundedByProductPolicy(t *testing.T) {
	t.Parallel()
	monitor := validMonitorProto()
	monitor.SetSearchHorizonDays(DefaultSearchHorizonDays + 1)
	if err := ValidateMonitor(monitor); err == nil {
		t.Fatal("ValidateMonitor() accepted a horizon above the product limit")
	}
}
