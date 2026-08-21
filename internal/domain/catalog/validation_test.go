package catalog

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

func TestNormalizeSnapshotCanonicalizesRelationships(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	snapshot := catalogSnapshotFixture(observedAt)
	snapshot.Provider.ID = " " + snapshot.Provider.ID + " "
	snapshot.Provider.Name = " " + snapshot.Provider.Name + " "
	snapshot.Auditoriums[0].ScreenTypes = []string{" IMAX ", "2D", "IMAX", " "}
	snapshot.Showtimes[0].Movie.Title = "stale movie title"
	snapshot.Showtimes[0].Auditorium.Name = "stale auditorium name"

	if err := NormalizeSnapshot(&snapshot); err != nil {
		t.Fatalf("NormalizeSnapshot() error = %v", err)
	}
	if snapshot.Provider.ID != contracts.ProviderCGV || snapshot.Provider.Name != "CGV" {
		t.Fatalf("provider = %+v", snapshot.Provider)
	}
	auditorium := snapshot.Auditoriums[0]
	if len(auditorium.ScreenTypes) != 2 || auditorium.ScreenTypes[0] != "2D" || auditorium.ScreenTypes[1] != "IMAX" {
		t.Fatalf("screen types = %v", auditorium.ScreenTypes)
	}
	if snapshot.Showtimes[0].Movie != snapshot.Movies[0] {
		t.Fatalf("showtime movie = %+v, want %+v", snapshot.Showtimes[0].Movie, snapshot.Movies[0])
	}
	if !reflect.DeepEqual(snapshot.Showtimes[0].Auditorium, auditorium) {
		t.Fatalf("showtime auditorium = %+v, want %+v", snapshot.Showtimes[0].Auditorium, auditorium)
	}
}

func TestNormalizeSnapshotKeepsIdentityWhenDisplayNamesChange(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	original := catalogSnapshotFixture(observedAt)
	if err := NormalizeSnapshot(&original); err != nil {
		t.Fatal(err)
	}
	originalTheaterID := original.Theaters[0].ID
	originalMovieID := original.Movies[0].ID
	originalAuditoriumID := original.Auditoriums[0].ID
	originalShowtimeID := original.Showtimes[0].ID

	renamed := catalogSnapshotFixture(observedAt)
	renamed.Theaters[0].Name = "용산아이파크몰 (리뉴얼)"
	renamed.Theaters[0].Region = "서울특별시"
	renamed.Movies[0].Title = "테스트 영화 (재개봉)"
	renamed.Auditoriums[0].Name = "IMAX Laser"
	if err := NormalizeSnapshot(&renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Theaters[0].ID != originalTheaterID || renamed.Movies[0].ID != originalMovieID ||
		renamed.Auditoriums[0].ID != originalAuditoriumID || renamed.Showtimes[0].ID != originalShowtimeID {
		t.Fatalf("display rename changed identity: original=%q/%q/%q/%q renamed=%q/%q/%q/%q",
			originalTheaterID, originalMovieID, originalAuditoriumID, originalShowtimeID,
			renamed.Theaters[0].ID, renamed.Movies[0].ID, renamed.Auditoriums[0].ID, renamed.Showtimes[0].ID)
	}
	if renamed.Showtimes[0].Movie.Title != "테스트 영화 (재개봉)" || renamed.Showtimes[0].Auditorium.Name != "IMAX Laser" {
		t.Fatalf("renamed display projection was not refreshed: %+v", renamed.Showtimes[0])
	}
}

func TestNormalizeSnapshotRejectsInvalidEntities(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	tests := map[string]func(*contracts.CatalogSnapshot){
		"nil snapshot": func(*contracts.CatalogSnapshot) {},
		"provider":     func(snapshot *contracts.CatalogSnapshot) { snapshot.Provider.Name = " " },
		"theater":      func(snapshot *contracts.CatalogSnapshot) { snapshot.Theaters[0].Name = " " },
		"movie":        func(snapshot *contracts.CatalogSnapshot) { snapshot.Movies[0].Title = " " },
		"auditorium": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Auditoriums[0].Name = " "
		},
		"showtime": func(snapshot *contracts.CatalogSnapshot) { snapshot.Showtimes[0].SourceKey = " " },
		"unknown auditorium theater": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Auditoriums[0].TheaterID = "unknown"
		},
		"unknown showtime movie": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Showtimes[0].Movie.ID = "unknown"
		},
		"cross-theater showtime": func(snapshot *contracts.CatalogSnapshot) {
			other := snapshot.Theaters[0]
			other.SourceKey = "서울/영등포"
			other.Name = "영등포"
			other.ID = contracts.CatalogID(other.ProviderID, "theater", other.SourceKey)
			snapshot.Theaters = append(snapshot.Theaters, other)
			snapshot.Showtimes[0].TheaterID = other.ID
		},
		"duplicate theater": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Theaters = append(snapshot.Theaters, snapshot.Theaters[0])
		},
		"duplicate movie": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Movies = append(snapshot.Movies, snapshot.Movies[0])
		},
		"duplicate auditorium": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Auditoriums = append(snapshot.Auditoriums, snapshot.Auditoriums[0])
		},
		"duplicate showtime": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Showtimes = append(snapshot.Showtimes, snapshot.Showtimes[0])
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if name == "nil snapshot" {
				if err := NormalizeSnapshot(nil); err == nil {
					t.Fatal("NormalizeSnapshot(nil) accepted a nil snapshot")
				}
				return
			}
			snapshot := catalogSnapshotFixture(observedAt)
			mutate(&snapshot)
			if err := NormalizeSnapshot(&snapshot); err == nil {
				t.Fatal("NormalizeSnapshot() accepted invalid catalog")
			}
		})
	}
}

func TestNormalizeSeatMapVersionCanonicalizesLayout(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	layout := validSeatMapLayout(2)
	layout.Seats[0].Features = []string{" aisle ", "premium", "aisle"}
	layout.Seats[0], layout.Seats[1] = layout.Seats[1], layout.Seats[0]
	raw, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	version := contracts.SeatMapVersion{
		AuditoriumID: " auditorium ",
		Capacity:     2,
		Layout:       raw,
	}

	if err := NormalizeSeatMapVersion(&version, now); err != nil {
		t.Fatalf("NormalizeSeatMapVersion() error = %v", err)
	}
	var normalized contracts.SeatMapLayout
	if err := json.Unmarshal(version.Layout, &normalized); err != nil {
		t.Fatal(err)
	}
	if version.AuditoriumID != "auditorium" || len(normalized.Seats) != 2 ||
		normalized.Seats[0].Label != "A1" || !reflect.DeepEqual(normalized.Seats[0].Features, []string{"aisle", "premium"}) {
		t.Fatalf("canonical version = %+v", version)
	}
	if version.LayoutHash == "" || version.ID != contracts.SeatMapVersionID(version.AuditoriumID, version.LayoutHash) {
		t.Fatalf("seat-map identity = %+v", version)
	}
	if version.ObservedAt != now {
		t.Fatalf("observedAt = %v, want %v", version.ObservedAt, now)
	}
}

func TestNormalizeSeatMapVersionRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	validLayout := mustLayoutJSON(t, validSeatMapLayout(1))
	tests := map[string]contracts.SeatMapVersion{
		"nil version":        {},
		"missing auditorium": {Capacity: 1, Layout: validLayout},
		"missing capacity":   {AuditoriumID: "auditorium", Layout: validLayout},
		"invalid layout":     {AuditoriumID: "auditorium", Capacity: 1, Layout: json.RawMessage(`[]`)},
		"future observation": {
			AuditoriumID: "auditorium", Capacity: 1, Layout: validLayout,
			ObservedAt: now.Add(MaximumObservationClockSkew + time.Second),
		},
		"noncanonical id": {
			ID: "wrong", AuditoriumID: "auditorium", Capacity: 1, Layout: validLayout,
		},
	}
	for name, version := range tests {
		t.Run(name, func(t *testing.T) {
			if name == "nil version" {
				if err := NormalizeSeatMapVersion(nil, now); err == nil {
					t.Fatal("NormalizeSeatMapVersion(nil) accepted a nil value")
				}
				return
			}
			if err := NormalizeSeatMapVersion(&version, now); err == nil {
				t.Fatal("NormalizeSeatMapVersion() accepted invalid input")
			}
		})
	}
}

func TestNormalizeSeatMapVersionRejectsCorruptCanonicalLayout(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	tests := map[string]func(*contracts.SeatMapLayout){
		"wrong auditorium": func(layout *contracts.SeatMapLayout) { layout.Seats[0].AuditoriumID = "other" },
		"wrong id":         func(layout *contracts.SeatMapLayout) { layout.Seats[0].ID = "wrong" },
		"wrong label":      func(layout *contracts.SeatMapLayout) { layout.Seats[0].Label = "A2" },
		"lowercase row":    func(layout *contracts.SeatMapLayout) { layout.Seats[0].Row = "a" },
		"missing type":     func(layout *contracts.SeatMapLayout) { layout.Seats[0].Type = " " },
		"invalid x":        func(layout *contracts.SeatMapLayout) { layout.Seats[0].X = 1.01 },
		"duplicate label": func(layout *contracts.SeatMapLayout) {
			duplicate := layout.Seats[0]
			layout.Seats = append(layout.Seats, duplicate)
		},
		"invalid zone":  func(layout *contracts.SeatMapLayout) { layout.Zones[0].MinX = 0.8; layout.Zones[0].MaxX = 0.2 },
		"invalid block": func(layout *contracts.SeatMapLayout) { layout.Blocks[0].MaxY = 1.1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			layout := validSeatMapLayout(1)
			mutate(&layout)
			version := contracts.SeatMapVersion{
				AuditoriumID: "auditorium", Capacity: len(layout.Seats), Layout: mustLayoutJSON(t, layout),
			}
			if err := NormalizeSeatMapVersion(&version, now); err == nil {
				t.Fatal("NormalizeSeatMapVersion() accepted corrupt canonical layout")
			}
		})
	}
	version := contracts.SeatMapVersion{
		AuditoriumID: "auditorium", Capacity: 2,
		Layout: mustLayoutJSON(t, validSeatMapLayout(1)),
	}
	if err := NormalizeSeatMapVersion(&version, now); err == nil {
		t.Fatal("NormalizeSeatMapVersion() accepted a capacity mismatch")
	}
}

func validSeatMapLayout(count int) contracts.SeatMapLayout {
	const auditoriumID = "auditorium"
	seats := make([]contracts.SeatMapSeat, 0, count)
	for number := 1; number <= count; number++ {
		label := "A" + string(rune('0'+number))
		seats = append(seats, contracts.SeatMapSeat{
			ID: contracts.SeatID(auditoriumID, label), AuditoriumID: auditoriumID,
			Label: label, Row: "A", Number: number, X: float64(number) / float64(count+1), Y: 0.5,
			Type: "standard", Features: []string{},
		})
	}
	return contracts.SeatMapLayout{
		Seats:  seats,
		Zones:  []contracts.LayoutZone{{Code: "zone-1", Name: "일반", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1, Capacity: count}},
		Blocks: []contracts.LayoutBlock{{Code: "block-1", Name: "중앙", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1}},
	}
}

func mustLayoutJSON(t *testing.T, layout contracts.SeatMapLayout) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestValidateObservationTime(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	if err := ValidateObservationTime(time.Time{}, now); err != nil {
		t.Fatalf("zero observation time = %v", err)
	}
	if err := ValidateObservationTime(now.Add(MaximumObservationClockSkew), now); err != nil {
		t.Fatalf("maximum tolerated clock skew = %v", err)
	}
	if err := ValidateObservationTime(now.Add(MaximumObservationClockSkew+time.Nanosecond), now); err == nil {
		t.Fatal("future observation beyond tolerance was accepted")
	}
}

func catalogSnapshotFixture(observedAt time.Time) contracts.CatalogSnapshot {
	provider := contracts.Provider{ID: contracts.ProviderCGV, Name: "CGV"}
	theaterSourceKey := "0056"
	theater := contracts.Theater{
		ID:         contracts.CatalogID(provider.ID, "theater", theaterSourceKey),
		ProviderID: provider.ID, SourceKey: theaterSourceKey, Region: "서울", Name: "용산아이파크몰",
	}
	movie := contracts.Movie{
		ID:         contracts.CatalogID(provider.ID, "movie", "00001234"),
		ProviderID: provider.ID, SourceKey: "00001234", Title: "테스트 영화",
	}
	auditoriumSourceKey := theaterSourceKey + "/0007"
	auditorium := contracts.Auditorium{
		ID:        contracts.CatalogID(provider.ID, "auditorium", auditoriumSourceKey),
		TheaterID: theater.ID, SourceKey: auditoriumSourceKey, Name: "IMAX관", Capacity: 624,
	}
	showtimeSourceKey := theaterSourceKey + "/2026-08-14/0007/0003"
	showtime := contracts.Showtime{
		ID:         contracts.CatalogID(provider.ID, "showtime", showtimeSourceKey),
		ProviderID: provider.ID, SourceKey: showtimeSourceKey, TheaterID: theater.ID,
		Movie: movie, Auditorium: auditorium,
		StartsAt: observedAt.Add(time.Hour), EndsAt: observedAt.Add(3 * time.Hour), Capacity: 624,
	}
	return contracts.CatalogSnapshot{
		Provider: provider, Theaters: []contracts.Theater{theater}, Movies: []contracts.Movie{movie},
		Auditoriums: []contracts.Auditorium{auditorium}, Showtimes: []contracts.Showtime{showtime},
		ObservedAt: observedAt,
	}
}
