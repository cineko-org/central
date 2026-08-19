package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCatalogValueValidation(t *testing.T) {
	t.Parallel()

	validTypes := []SeatType{
		SeatTypeStandard, SeatTypeWheelchair, SeatTypeCompanion, SeatTypeCouple,
		SeatTypeRecliner, SeatTypeMotion, SeatTypeBed, SeatTypeUnknown,
	}
	for _, seatType := range validTypes {
		if !seatType.Valid() {
			t.Fatalf("%q should be valid", seatType)
		}
	}
	if SeatType("invalid").Valid() {
		t.Fatal("unknown seat type accepted")
	}

	theater := Theater{ID: "theater", Region: "서울", Name: "용산"}
	if err := theater.Validate(); err != nil {
		t.Fatalf("valid theater: %v", err)
	}
	for _, mutate := range []func(*Theater){
		func(value *Theater) { value.ID = "" },
		func(value *Theater) { value.Region = " " },
		func(value *Theater) { value.Name = " " },
	} {
		candidate := theater
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid theater accepted: %+v", candidate)
		}
	}

	auditorium := Auditorium{ID: "auditorium", TheaterID: "theater", Name: "IMAX", Capacity: 1}
	if err := auditorium.Validate(); err != nil {
		t.Fatalf("valid auditorium: %v", err)
	}
	for _, mutate := range []func(*Auditorium){
		func(value *Auditorium) { value.ID = "" },
		func(value *Auditorium) { value.Capacity = -1 },
	} {
		candidate := auditorium
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid auditorium accepted: %+v", candidate)
		}
	}

	seat := Seat{
		ID: "seat", AuditoriumID: "auditorium", Label: "A1", Row: "A", Number: 1,
		X: 0.5, Y: 0.5,
	}
	if err := seat.Validate(); err != nil {
		t.Fatalf("valid seat: %v", err)
	}
	for _, mutate := range []func(*Seat){
		func(value *Seat) { value.ID = "" },
		func(value *Seat) { value.Number = 0 },
		func(value *Seat) { value.X = 2 },
	} {
		candidate := seat
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid seat accepted: %+v", candidate)
		}
	}
}

func TestLayoutValidation(t *testing.T) {
	t.Parallel()

	zone := LayoutZone{Code: "center", Name: "중앙", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1, Capacity: 1}
	if err := zone.Validate(); err != nil {
		t.Fatalf("valid zone: %v", err)
	}
	for _, mutate := range []func(*LayoutZone){
		func(value *LayoutZone) { value.Code = "" },
		func(value *LayoutZone) { value.Capacity = -1 },
		func(value *LayoutZone) { value.MinX = 2 },
	} {
		candidate := zone
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid zone accepted: %+v", candidate)
		}
	}

	block := LayoutBlock{Code: "main", Name: "본관", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1}
	if err := block.Validate(); err != nil {
		t.Fatalf("valid block: %v", err)
	}
	for _, mutate := range []func(*LayoutBlock){
		func(value *LayoutBlock) { value.Name = "" },
		func(value *LayoutBlock) { value.MaxY = 2 },
	} {
		candidate := block
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid block accepted: %+v", candidate)
		}
	}
}

func TestSeatMapValidationCoversEveryInvariant(t *testing.T) {
	t.Parallel()

	valid := validSeatMap()
	valid.Zones = []LayoutZone{{Code: "zone", Name: "zone", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1}}
	valid.Blocks = []LayoutBlock{{Code: "block", Name: "block", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid seat map: %v", err)
	}

	cases := []func(*SeatMap){
		func(value *SeatMap) { value.Version = "" },
		func(value *SeatMap) { value.Seats = nil },
		func(value *SeatMap) { value.Evidence.ScreenshotPath = "" },
		func(value *SeatMap) { value.Evidence.CapturedAt = time.Time{} },
		func(value *SeatMap) { value.Evidence.DOMSeatCount = 9 },
		func(value *SeatMap) { value.Seats[0].Number = 0 },
		func(value *SeatMap) { value.Seats[0].AuditoriumID = "other" },
		func(value *SeatMap) {
			duplicate := value.Seats[0]
			value.Seats = append(value.Seats, duplicate)
			value.Evidence.DOMSeatCount++
			value.Evidence.SnapshotSeatCount++
		},
		func(value *SeatMap) { value.Zones[0].Code = "" },
		func(value *SeatMap) { value.Blocks[0].Code = "" },
	}
	for index, mutate := range cases {
		candidate := valid
		candidate.Seats = append([]Seat(nil), valid.Seats...)
		candidate.Zones = append([]LayoutZone(nil), valid.Zones...)
		candidate.Blocks = append([]LayoutBlock(nil), valid.Blocks...)
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("case %d accepted: %+v", index, candidate)
		}
	}
}

func TestSeatMapSortingAndEmptyAnalysis(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "B2", Row: "B", Number: 2, X: 0.8, Y: 0.7, Type: SeatTypeStandard},
		{Label: "A2", Row: "A", Number: 2, X: 0.6, Y: 0.3, Type: SeatTypeStandard},
		{Label: "A1", Row: "A", Number: 1, X: 0.4, Y: 0.3, Type: SeatTypeStandard},
	}}
	got := seatMap.SortedSeats()
	labels := []string{got[0].Label, got[1].Label, got[2].Label}
	if !reflect.DeepEqual(labels, []string{"A1", "A2", "B2"}) {
		t.Fatalf("SortedSeats() = %v", labels)
	}
	if seatMap.Seats[0].Label != "B2" {
		t.Fatal("SortedSeats mutated source")
	}
	empty := AnalyzeSeatMap(SeatMap{AuditoriumID: "empty"})
	if empty.MinX != 0 || empty.MinY != 0 || empty.Capacity != 0 {
		t.Fatalf("empty analysis = %+v", empty)
	}
}

func TestPresetValidationAndPreferenceHelpers(t *testing.T) {
	t.Parallel()

	zone := SeatZone{Name: "center", MinX: 0.2, MaxX: 0.8, MinY: 0.2, MaxY: 0.8}
	if !zone.Contains(0.5, 0.5) || zone.Contains(0.1, 0.1) {
		t.Fatal("SeatZone.Contains returned an invalid result")
	}
	if err := zone.Validate(); err != nil {
		t.Fatalf("valid zone: %v", err)
	}
	for _, mutate := range []func(*SeatZone){
		func(value *SeatZone) { value.Name = "" },
		func(value *SeatZone) { value.MinX = 2 },
	} {
		candidate := zone
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid preference zone accepted: %+v", candidate)
		}
	}

	preset := validPreset()
	if err := preset.Validate(nil); err != nil {
		t.Fatalf("valid preset: %v", err)
	}
	for _, mutate := range []func(*Preset){
		func(value *Preset) { value.ID = "" },
		func(value *Preset) { value.Name = "" },
		func(value *Preset) { value.TheaterID = "" },
		func(value *Preset) { value.SeatCount = 0 },
		func(value *Preset) {
			value.SeatPreference.PreferredZones = []SeatZone{{Name: "bad", MinX: 1, MaxX: 0}}
		},
		func(value *Preset) { value.SeatPreference.PreferredTypes = []SeatType{"bad"} },
	} {
		candidate := preset
		mutate(&candidate)
		if candidate.Validate(nil) == nil {
			t.Fatalf("invalid preset accepted: %+v", candidate)
		}
	}

	seatMap := validSeatMap()
	preset.SeatPreference.ExplicitSeats = []string{"A1"}
	if err := preset.Validate(&seatMap); err != nil {
		t.Fatalf("valid map-bound preset: %v", err)
	}
	wrongMap := seatMap
	wrongMap.AuditoriumID = "other"
	if preset.Validate(&wrongMap) == nil {
		t.Fatal("mismatched seat map accepted")
	}
	preset.SeatPreference.ExplicitSeats = []string{"Z99"}
	if preset.Validate(&seatMap) == nil {
		t.Fatal("missing explicit seat accepted")
	}
	if !preset.Owns("user") || preset.Owns("other") {
		t.Fatal("Preset.Owns returned an invalid result")
	}
	preference := SeatPreference{PreferredTypes: []SeatType{SeatTypeWheelchair, SeatTypeStandard}}
	if preference.TypeRank(SeatTypeStandard) != 1 || preference.TypeRank(SeatTypeBed) != 3 {
		t.Fatal("SeatPreference.TypeRank returned an invalid rank")
	}
}

func TestMonitorValidationAndStateHelpers(t *testing.T) {
	t.Parallel()

	job := validMonitorJob()
	if err := job.Validate(); err != nil {
		t.Fatalf("valid monitor: %v", err)
	}
	mutations := []func(*MonitorJob){
		func(value *MonitorJob) { value.ID = "" },
		func(value *MonitorJob) { value.Movie = "" },
		func(value *MonitorJob) { value.Mode = "bad" },
		func(value *MonitorJob) {
			value.Mode = MonitorModeCancellation
			value.TargetWeekdays = []int{1}
			value.SearchHorizonDays = 10
		},
		func(value *MonitorJob) { value.PollInterval = time.Second },
		func(value *MonitorJob) { value.PollIntervalMax = value.PollInterval },
		func(value *MonitorJob) { value.TargetDates = []string{"bad"} },
		func(value *MonitorJob) {
			value.TargetWeekdays = []int{8}
			value.SearchHorizonDays = 10
		},
		func(value *MonitorJob) {
			value.TargetWeekdays = []int{1, 1}
			value.SearchHorizonDays = 10
		},
		func(value *MonitorJob) {
			value.TargetWeekdays = []int{1}
			value.SearchHorizonDays = 0
		},
		func(value *MonitorJob) { value.EarliestTime = "bad" },
		func(value *MonitorJob) { value.LatestTime = "bad" },
		func(value *MonitorJob) { value.EarliestTime, value.LatestTime = "20:00", "10:00" },
	}
	for index, mutate := range mutations {
		candidate := job
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("case %d accepted: %+v", index, candidate)
		}
	}
	if got := job.EffectivePollIntervalMax(); got != job.PollInterval+job.PollInterval/5 {
		t.Fatalf("EffectivePollIntervalMax(legacy) = %v", got)
	}
	job.PollIntervalMax = 9 * time.Second
	if got := job.EffectivePollIntervalMax(); got != 9*time.Second {
		t.Fatalf("EffectivePollIntervalMax(explicit) = %v", got)
	}

	if (MonitorJob{}).EffectiveMode() != MonitorModeOpening ||
		(MonitorJob{Mode: MonitorModeCancellation}).EffectiveMode() != MonitorModeCancellation {
		t.Fatal("EffectiveMode returned an invalid mode")
	}
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	resolved := (MonitorJob{
		TargetDates: []string{"bad", "2026-08-08", "2026-08-10"},
	}).ResolveTargetDates(now)
	if !reflect.DeepEqual(resolved, []string{"2026-08-10"}) {
		t.Fatalf("ResolveTargetDates() = %v", resolved)
	}
	if (MonitorJob{}).Expired(now) {
		t.Fatal("empty schedule expired")
	}
	job.Transition(MonitorBooked, now)
	if job.Status != MonitorBooked || !job.UpdatedAt.Equal(now) {
		t.Fatal("Transition did not update state")
	}
	job.RecordCheck(now, nil)
	if job.LastError != "" || job.LastCheckedAt == nil {
		t.Fatal("RecordCheck did not clear error")
	}
	wantErr := errors.New("failed")
	job.RecordCheck(now, wantErr)
	if job.LastError != wantErr.Error() {
		t.Fatal("RecordCheck did not save error")
	}
}

func TestSeatRankerIndependentAndErrorPaths(t *testing.T) {
	t.Parallel()

	ranker := SeatRanker{}
	if _, err := ranker.Rank(SeatMap{}, nil, 0, SeatPreference{}); err == nil {
		t.Fatal("zero seat count accepted")
	}
	seatMap := SeatMap{Seats: []Seat{
		{Label: "A1", Row: "A", Number: 1, X: 0.01, Y: 0.2, Type: SeatTypeStandard},
		{Label: "A2", Row: "A", Number: 2, X: 0.5, Y: 0.5, Type: SeatTypeWheelchair},
	}}
	live := []LiveSeat{{Label: "A1", Available: true}, {Label: "A2", Available: true}}
	preference := SeatPreference{
		PreferredRows: []string{"A"}, PreferredZones: []SeatZone{{Name: "all", MaxX: 1, MaxY: 1, Weight: 2}},
		PreferredTypes: []SeatType{SeatTypeWheelchair}, AvoidEdges: true,
	}
	groups, err := ranker.Rank(seatMap, live, 1, preference)
	if err != nil || len(groups) != 2 || groups[0].Seats[0].Label != "A2" {
		t.Fatalf("independent ranking = %+v, %v", groups, err)
	}
	groups, err = ranker.Rank(seatMap, live, 2, preference)
	if err != nil || len(groups) != 1 || len(groups[0].Seats) != 2 {
		t.Fatalf("separate group = %+v, %v", groups, err)
	}
	if _, err := ranker.Rank(seatMap, live[:1], 2, preference); err == nil {
		t.Fatal("insufficient separate seats accepted")
	}
	tiedMap := SeatMap{Seats: []Seat{
		{Label: "B1", Row: "B", Number: 1, X: 0.5, Y: 0.5},
		{Label: "A1", Row: "A", Number: 1, X: 0.5, Y: 0.5},
	}}
	tiedLive := []LiveSeat{{Label: "B1", Available: true}, {Label: "A1", Available: true}}
	tied, err := ranker.Rank(tiedMap, tiedLive, 1, SeatPreference{Together: true})
	if err != nil || tied[0].Seats[0].Label != "A1" {
		t.Fatalf("tied ranking = %+v, %v", tied, err)
	}
}

func validPreset() Preset {
	return Preset{
		ID: "preset", UserID: "user", Name: "center", TheaterID: "theater",
		AuditoriumID: "auditorium-1", SeatCount: 1,
		SeatPreference: SeatPreference{PreferredTypes: []SeatType{SeatTypeStandard}},
	}
}

func validMonitorJob() MonitorJob {
	return MonitorJob{
		ID: "monitor", UserID: "user", PresetID: "preset", Mode: MonitorModeOpening,
		Movie: "Movie", TargetDates: []string{"2026-08-10"}, PollInterval: 2 * time.Second,
	}
}
