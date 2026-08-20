package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestMonitorJobValidateChecksDatesAndTimeWindow(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		ID: "m1", UserID: "u1", PresetID: "p1", MovieID: "movie-1", Movie: "오디세이",
		TargetDates: []string{"2026-08-10"}, PollInterval: 5 * time.Second,
		EarliestTime: "20:00", LatestTime: "18:00",
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() rejected a cross-midnight time window: %v", err)
	}
	job.EarliestTime, job.LatestTime = "18:00", "20:00"
	job.TargetDates = []string{"08/10/2026"}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-ISO date")
	}
	job.TargetDates = []string{"2026-08-10"}
	job.EarliestTime, job.LatestTime = "18:00", "18:00"
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty time window")
	}
}

func TestTimeWindowContainsUsesHalfOpenAndCrossMidnightSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, showtime, earliest, latest string
		want                             bool
	}{
		{name: "empty window allows all", showtime: "01:00", want: true},
		{name: "start inclusive", showtime: "18:00", earliest: "18:00", latest: "21:00", want: true},
		{name: "end exclusive", showtime: "21:00", earliest: "18:00", latest: "21:00", want: false},
		{name: "cross midnight early", showtime: "01:00", earliest: "21:00", latest: "06:00", want: true},
		{name: "cross midnight end", showtime: "06:00", earliest: "21:00", latest: "06:00", want: false},
		{name: "equal bounds empty", showtime: "18:00", earliest: "18:00", latest: "18:00", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TimeWindowContains(test.showtime, test.earliest, test.latest); got != test.want {
				t.Fatalf("TimeWindowContains(%q, %q, %q) = %t, want %t", test.showtime, test.earliest, test.latest, got, test.want)
			}
		})
	}
}

func TestMonitorJobValidateAcceptsWeekdaySchedule(t *testing.T) {
	t.Parallel()

	job := MonitorJob{
		ID: "m1", UserID: "u1", PresetID: "p1", MovieID: "movie-1", Movie: "오디세이",
		TargetWeekdays:    []int{int(time.Monday), int(time.Saturday)},
		SearchHorizonDays: 14, PollInterval: 5 * time.Second,
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
		Movie: "오디세이", TargetWeekdays: []int{int(time.Saturday)},
		SearchHorizonDays: 14, PollInterval: 5 * time.Second,
	}
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted recurring weekdays for a cancellation-seat monitor")
	}
}
