package planning

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildProjectsHotDatesAndAlternatesBaseline(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	input := Input{
		Now:            now,
		Location:       location,
		TargetDateMode: "rolling",
		HorizonDays:    14,
		HotTargets:     []MonitorTarget{{TargetDates: []string{"2026-08-20"}, TargetWeekdays: []int{int(time.Saturday)}, SearchHorizonDays: 14}},
	}
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneHot || !reflect.DeepEqual(got.TargetDates, []string{"2026-08-15", "2026-08-20", "2026-08-22"}) {
		t.Fatalf("hot plan = %+v", got)
	}

	input.HotTargets = nil
	got, err = Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneBaseline || !reflect.DeepEqual(got.TargetDates, []string{"2026-08-14"}) {
		t.Fatalf("baseline plan = %+v", got)
	}
}

func TestBuildHotDemandPreemptsBaselineAndLeavesSlack(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	base := Input{
		Now:                     now,
		Location:                location,
		TargetDateMode:          "rolling",
		HorizonDays:             14,
		HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-20"}}},
		HotMinimumInterval:      time.Minute,
		BaselineMaximumInterval: time.Hour,
	}
	got, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneHot {
		t.Fatalf("hot plan = %+v", got)
	}

	base.LastHotFinishedAt = now
	base.LastHotTargetDates = []string{"2026-08-20"}
	got, err = Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneBaseline || !reflect.DeepEqual(got.TargetDates, []string{"2026-08-14"}) {
		t.Fatalf("slack baseline plan = %+v", got)
	}

	base.LastBaselineFinishedAt = now
	got, err = Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneIdle || len(got.TargetDates) != 0 {
		t.Fatalf("idle plan = %+v", got)
	}

	base.LastHotFinishedAt = now.Add(-2 * time.Minute)
	got, err = Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneHot {
		t.Fatalf("hot preemption plan = %+v", got)
	}
}

func TestBuildBaselineCursorEventuallyCoversRollingHorizon(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	input := Input{
		Now:                     now,
		Location:                location,
		TargetDateMode:          "rolling",
		HorizonDays:             14,
		HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-20"}}},
		HotMinimumInterval:      time.Hour,
		BaselineMaximumInterval: time.Nanosecond,
		LastHotFinishedAt:       now,
		LastHotTargetDates:      []string{"2026-08-20"},
	}
	seen := make(map[string]struct{}, input.HorizonDays)
	for range input.HorizonDays {
		got, err := Build(input)
		if err != nil {
			t.Fatal(err)
		}
		if got.Lane != LaneBaseline || len(got.TargetDates) != 1 {
			t.Fatalf("baseline cursor plan = %+v", got)
		}
		date := got.TargetDates[0]
		if _, exists := seen[date]; exists {
			t.Fatalf("baseline cursor repeated %s", date)
		}
		seen[date] = struct{}{}
		input.LastBaselineTargetDate = date
		input.LastBaselineFinishedAt = now.Add(-time.Second)
	}
	if len(seen) != input.HorizonDays {
		t.Fatalf("covered %d baseline dates, want %d", len(seen), input.HorizonDays)
	}
}

func TestBuildFiveSecondTickCompletesHotBaselineHotSequence(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	start := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	input := Input{
		Now:                     start,
		Location:                location,
		TargetDateMode:          "rolling",
		HorizonDays:             14,
		HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-20"}}},
		HotMinimumInterval:      2 * time.Second,
		BaselineMaximumInterval: 15 * time.Minute,
	}
	hot, err := Build(input)
	if err != nil || hot.Lane != LaneHot {
		t.Fatalf("initial five-second tick plan = %+v, %v", hot, err)
	}

	// The five-second scheduler tick misses the nominal two-second slack. The
	// persisted lane timestamps still require one baseline date before hot work
	// can recur.
	input.Now = start.Add(5 * time.Second)
	input.LastHotFinishedAt = start
	input.LastHotTargetDates = []string{"2026-08-20"}
	baseline, err := Build(input)
	if err != nil || baseline.Lane != LaneBaseline || len(baseline.TargetDates) != 1 {
		t.Fatalf("baseline five-second tick plan = %+v, %v", baseline, err)
	}

	input.Now = start.Add(10 * time.Second)
	input.LastBaselineFinishedAt = start.Add(6 * time.Second)
	hot, err = Build(input)
	if err != nil || hot.Lane != LaneHot {
		t.Fatalf("hot after baseline five-second tick plan = %+v, %v", hot, err)
	}
}

func TestBuildNewHotTargetPreemptsPendingBaseline(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	input := Input{
		Now:                     now,
		Location:                location,
		TargetDateMode:          "rolling",
		HorizonDays:             14,
		HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-21"}}},
		LastHotFinishedAt:       now,
		LastHotTargetDates:      []string{"2026-08-20"},
		HotMinimumInterval:      time.Hour,
		BaselineMaximumInterval: time.Hour,
	}
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneHot || !reflect.DeepEqual(got.TargetDates, []string{"2026-08-21"}) {
		t.Fatalf("new target plan = %+v", got)
	}
}

func TestFingerprintDetectsProjectionChangeWithSameHotDates(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	previous := []MonitorTarget{{TargetDates: []string{"2026-08-20"}, SearchHorizonDays: 1}}
	current := []MonitorTarget{{TargetDates: []string{"2026-08-20"}, SearchHorizonDays: 2}}
	input := Input{
		Now:                      now,
		Location:                 location,
		TargetDateMode:           "rolling",
		HorizonDays:              14,
		HotTargets:               current,
		HotTargetFingerprint:     Fingerprint(current),
		LastHotFinishedAt:        now,
		LastHotTargetDates:       []string{"2026-08-20"},
		LastHotTargetFingerprint: Fingerprint(previous),
		HotMinimumInterval:       time.Hour,
		BaselineMaximumInterval:  time.Hour,
	}
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneHot {
		t.Fatalf("changed projection plan = %+v", got)
	}
}

func TestBuildFailedOrMissedHotDoesNotUnlockBaseline(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	for _, outcome := range []string{"partial", "failed", "missed"} {
		t.Run(outcome, func(t *testing.T) {
			// Failed and missed assignments are deliberately absent from the
			// persisted lane-progress inputs. Without a successful hot run,
			// the planner must retry hot demand instead of opening baseline
			// work.
			got, err := Build(Input{
				Now:                     now,
				Location:                location,
				TargetDateMode:          "rolling",
				HorizonDays:             14,
				HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-20"}}},
				LastBaselineFinishedAt:  now,
				LastBaselineTargetDate:  "2026-08-14",
				HotMinimumInterval:      2 * time.Second,
				BaselineMaximumInterval: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Lane != LaneHot {
				t.Fatalf("%s hot retry plan = %+v", outcome, got)
			}
		})
	}
}

func TestBuildFailedOrMissedBaselineDoesNotAdvanceCursor(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	for _, outcome := range []string{"partial", "failed", "missed"} {
		t.Run(outcome, func(t *testing.T) {
			input := Input{
				Now:                     now,
				Location:                location,
				TargetDateMode:          "rolling",
				HorizonDays:             14,
				HotTargets:              []MonitorTarget{{TargetDates: []string{"2026-08-20"}}},
				LastHotFinishedAt:       now,
				LastHotTargetDates:      []string{"2026-08-20"},
				LastBaselineFinishedAt:  now.Add(-time.Minute),
				LastBaselineTargetDate:  "2026-08-13",
				HotMinimumInterval:      time.Hour,
				BaselineMaximumInterval: time.Hour,
			}
			first, err := Build(input)
			if err != nil {
				t.Fatal(err)
			}
			if first.Lane != LaneBaseline || !reflect.DeepEqual(first.TargetDates, []string{"2026-08-14"}) {
				t.Fatalf("%s baseline retry plan = %+v", outcome, first)
			}
			// A failed/missed attempt does not write lane progress. Reusing
			// the same successful cursor must therefore retry the same date.
			second, err := Build(input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(second, first) {
				t.Fatalf("%s baseline cursor advanced after failure: first=%+v second=%+v", outcome, first, second)
			}
		})
	}
}

func TestFingerprintIsInputOrderInvariant(t *testing.T) {
	left := []MonitorTarget{
		{TargetDates: []string{"2026-08-22", "2026-08-20"}, TargetWeekdays: []int{6, 1}, SearchHorizonDays: 14},
		{TargetDates: []string{"2026-08-25"}, TargetWeekdays: []int{2}, SearchHorizonDays: 7},
	}
	right := []MonitorTarget{
		{TargetDates: []string{"2026-08-25"}, TargetWeekdays: []int{2}, SearchHorizonDays: 7},
		{TargetDates: []string{"2026-08-20", "2026-08-22"}, TargetWeekdays: []int{1, 6}, SearchHorizonDays: 14},
	}
	if leftFingerprint, rightFingerprint := Fingerprint(left), Fingerprint(right); leftFingerprint != rightFingerprint {
		t.Fatalf("equivalent projections have different fingerprints: left=%s right=%s", leftFingerprint, rightFingerprint)
	}
}

func TestBuildUsesExplicitBaselineAndRejectsMalformedTargets(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, location)
	base := Input{
		Now:                 now,
		Location:            location,
		TargetDateMode:      "explicit",
		ExplicitTargetDates: []string{"2026-08-20", "2026-08-21"},
	}
	got, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != LaneBaseline || !reflect.DeepEqual(got.TargetDates, []string{"2026-08-20"}) {
		t.Fatalf("explicit plan = %+v", got)
	}

	base.HotTargets = []MonitorTarget{{TargetDates: []string{"not-a-date"}}}
	if _, err := Build(base); err == nil {
		t.Fatal("malformed hot target was accepted")
	}
}

func TestDecodeMonitorTargetsIsStrict(t *testing.T) {
	valid, err := DecodeMonitorTargets([]byte(`[{"targetDates":["2026-08-20"],"targetWeekdays":[6],"searchHorizonDays":14}]`))
	if err != nil || len(valid) != 1 || valid[0].SearchHorizonDays != 14 {
		t.Fatalf("decoded targets = %+v, %v", valid, err)
	}
	if _, err := DecodeMonitorTargets([]byte(`[{"targetDates":[],"unexpected":true}]`)); err == nil {
		t.Fatal("unknown projection field was accepted")
	}
}
