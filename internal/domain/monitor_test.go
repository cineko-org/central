package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestMonitorJobValidateChecksDatesAndTimeWindow(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		ID: "m1", UserID: "u1", PresetID: "p1", MovieID: "movie_1", Movie: "오디세이",
		TargetDates: []string{"2026-08-10"}, PollInterval: 5 * time.Second,
		EarliestTime: "20:00", LatestTime: "18:00",
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() rejected an overnight time window: %v", err)
	}
	job.EarliestTime, job.LatestTime = "20:00", "20:00"
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty time window")
	}
	job.EarliestTime, job.LatestTime = "18:00", "20:00"
	job.Movie = ""
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() rejected a monitor without a display title snapshot: %v", err)
	}
	job.MovieID = ""
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted a monitor without a canonical movie id")
	}
	job.MovieID = "movie_1"
	job.TargetDates = []string{"08/10/2026"}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-ISO date")
	}
}

func TestMonitorJobMatchesTimeWindow(t *testing.T) {
	t.Parallel()

	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	job := MonitorJob{EarliestTime: "21:00", LatestTime: "06:00"}
	cases := []struct {
		name string
		date time.Time
		want bool
	}{
		{name: "before overnight start", date: time.Date(2026, time.August, 14, 20, 59, 0, 0, location), want: false},
		{name: "overnight start inclusive", date: time.Date(2026, time.August, 14, 21, 0, 0, 0, location), want: true},
		{name: "before midnight", date: time.Date(2026, time.August, 14, 23, 59, 0, 0, location), want: true},
		{name: "Saturday after midnight", date: time.Date(2026, time.August, 15, 1, 0, 0, 0, location), want: true},
		{name: "overnight end exclusive", date: time.Date(2026, time.August, 15, 6, 0, 0, 0, location), want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := job.MatchesTimeWindow(test.date, location); got != test.want {
				t.Fatalf("MatchesTimeWindow() = %t, want %t", got, test.want)
			}
		})
	}

	normal := MonitorJob{EarliestTime: "18:00", LatestTime: "21:00"}
	if !normal.MatchesTimeWindow(time.Date(2026, time.August, 15, 18, 0, 0, 0, location), location) ||
		normal.MatchesTimeWindow(time.Date(2026, time.August, 15, 21, 0, 0, 0, location), location) {
		t.Fatal("MatchesTimeWindow() did not apply a normal half-open interval")
	}
	if !(MonitorJob{}).MatchesTimeWindow(time.Date(2026, time.August, 15, 1, 0, 0, 0, location), location) {
		t.Fatal("MatchesTimeWindow() rejected an unrestricted window")
	}
}

func TestMonitorJobMatchesScheduleUsesLocalCalendarDate(t *testing.T) {
	t.Parallel()

	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, location)
	job := MonitorJob{
		TargetWeekdays:    []int{int(time.Saturday)},
		SearchHorizonDays: 2,
		EarliestTime:      "00:00",
		LatestTime:        "06:00",
	}
	saturday := time.Date(2026, time.August, 15, 1, 0, 0, 0, location)
	if !job.MatchesSchedule("2026-08-15", saturday, now, location) {
		t.Fatal("MatchesSchedule() rejected Saturday 01:00")
	}
	if job.MatchesSchedule("2026-08-14", saturday, now, location) {
		t.Fatal("MatchesSchedule() attributed Saturday 01:00 to Friday")
	}
}

func TestMonitorJobValidateAcceptsWeekdaySchedule(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		ID: "m1", UserID: "u1", PresetID: "p1", MovieID: "movie_1", Movie: "오디세이",
		TargetWeekdays:    []int{int(time.Monday), int(time.Saturday)},
		SearchHorizonDays: 28, PollInterval: 5 * time.Second,
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	job.TargetWeekdays = []int{int(time.Monday), int(time.Monday)}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted a duplicate weekday")
	}
	job.TargetWeekdays = []int{7}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted an out-of-range weekday")
	}
	job.TargetWeekdays = []int{int(time.Monday)}
	job.SearchHorizonDays = 0
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty weekday search horizon")
	}
}

func TestMonitorJobResolveTargetDatesUsesRollingWeekdayWindow(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		TargetDates:       []string{"2026-08-20"},
		TargetWeekdays:    []int{int(time.Monday), int(time.Saturday)},
		SearchHorizonDays: 7,
	}
	now := time.Date(2026, time.August, 9, 18, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	want := []string{"2026-08-10", "2026-08-15", "2026-08-20"}

	if got := job.ResolveTargetDates(now); !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveTargetDates() = %v, want %v", got, want)
	}
}

func TestMonitorJobExpiredOnlyWhenAllExactDatesHavePassed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 18, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	job := MonitorJob{TargetDates: []string{"2026-08-08", "2026-08-09"}}
	if job.Expired(now) {
		t.Fatal("Expired() treated today's target as expired")
	}
	job.TargetDates = []string{"2026-08-07", "2026-08-08"}
	if !job.Expired(now) {
		t.Fatal("Expired() did not reject dates before today")
	}
	job.TargetWeekdays = []int{int(time.Monday)}
	if job.Expired(now) {
		t.Fatal("Expired() treated a rolling weekday schedule as expired")
	}
}

func TestCancellationMonitorRequiresExactDates(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		ID: "m1", UserID: "u1", PresetID: "p1", Mode: MonitorModeCancellation,
		MovieID: "movie_1",
		Movie:   "오디세이", TargetWeekdays: []int{int(time.Saturday)},
		SearchHorizonDays: 28, PollInterval: 5 * time.Second,
	}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted recurring weekdays for a cancellation-seat monitor")
	}
}
