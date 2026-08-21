package catalog

import (
	"reflect"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNormalizeSnapshotCanonicalizesRelationships(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	snapshot := catalogSnapshotFixture(observedAt)
	snapshot.GetProvider().SetId(" " + snapshot.GetProvider().GetId() + " ")
	snapshot.GetProvider().SetName(" " + snapshot.GetProvider().GetName() + " ")
	snapshot.GetAuditoriums()[0].SetScreenTypes([]string{" IMAX ", "2D", "IMAX", " "})
	snapshot.GetShowtimes()[0].GetMovie().SetTitle("stale movie title")
	snapshot.GetShowtimes()[0].GetAuditorium().SetName("stale auditorium name")

	if err := NormalizeSnapshot(snapshot); err != nil {
		t.Fatalf("NormalizeSnapshot() error = %v", err)
	}
	if snapshot.GetProvider().GetId() != ProviderCGV || snapshot.GetProvider().GetName() != "CGV" {
		t.Fatalf("provider = %+v", snapshot.GetProvider())
	}
	auditorium := snapshot.GetAuditoriums()[0]
	if got := auditorium.GetScreenTypes(); !reflect.DeepEqual(got, []string{"2D", "IMAX"}) {
		t.Fatalf("screen types = %v", got)
	}
	if snapshot.GetShowtimes()[0].GetMovie() != snapshot.GetMovies()[0] {
		t.Fatal("showtime movie was not replaced with the canonical catalog movie")
	}
	if !proto.Equal(snapshot.GetShowtimes()[0].GetAuditorium(), auditorium) {
		t.Fatal("showtime auditorium was not replaced with the canonical catalog auditorium")
	}
}

func TestNormalizeSnapshotKeepsIdentityWhenDisplayNamesChange(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	original := catalogSnapshotFixture(observedAt)
	if err := NormalizeSnapshot(original); err != nil {
		t.Fatal(err)
	}
	originalIDs := []string{
		original.GetTheaters()[0].GetId(), original.GetMovies()[0].GetId(),
		original.GetAuditoriums()[0].GetId(), original.GetShowtimes()[0].GetId(),
	}

	renamed := catalogSnapshotFixture(observedAt)
	renamed.GetTheaters()[0].SetName("용산아이파크몰 (리뉴얼)")
	renamed.GetTheaters()[0].SetRegion("서울특별시")
	renamed.GetMovies()[0].SetTitle("테스트 영화 (재개봉)")
	renamed.GetAuditoriums()[0].SetName("IMAX Laser")
	if err := NormalizeSnapshot(renamed); err != nil {
		t.Fatal(err)
	}
	renamedIDs := []string{
		renamed.GetTheaters()[0].GetId(), renamed.GetMovies()[0].GetId(),
		renamed.GetAuditoriums()[0].GetId(), renamed.GetShowtimes()[0].GetId(),
	}
	if !reflect.DeepEqual(renamedIDs, originalIDs) {
		t.Fatalf("display rename changed identity: original=%v renamed=%v", originalIDs, renamedIDs)
	}
	if renamed.GetShowtimes()[0].GetMovie().GetTitle() != "테스트 영화 (재개봉)" ||
		renamed.GetShowtimes()[0].GetAuditorium().GetName() != "IMAX Laser" {
		t.Fatal("renamed display projection was not refreshed")
	}
}

func TestNormalizeSnapshotRejectsInvalidEntities(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	tests := map[string]func(*catalogpb.CatalogSnapshot){
		"provider": func(snapshot *catalogpb.CatalogSnapshot) { snapshot.GetProvider().SetName(" ") },
		"theater":  func(snapshot *catalogpb.CatalogSnapshot) { snapshot.GetTheaters()[0].SetName(" ") },
		"movie":    func(snapshot *catalogpb.CatalogSnapshot) { snapshot.GetMovies()[0].SetTitle(" ") },
		"auditorium": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.GetAuditoriums()[0].SetName(" ")
		},
		"showtime": func(snapshot *catalogpb.CatalogSnapshot) { snapshot.GetShowtimes()[0].SetSourceKey(" ") },
		"unknown auditorium theater": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.GetAuditoriums()[0].SetTheaterId("unknown")
		},
		"unknown showtime movie": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.GetShowtimes()[0].GetMovie().SetId("unknown")
		},
		"cross-theater showtime": func(snapshot *catalogpb.CatalogSnapshot) {
			other := proto.CloneOf(snapshot.GetTheaters()[0])
			other.SetSourceKey("0043")
			other.SetName("영등포")
			other.SetId(CatalogID(other.GetProviderId(), "theater", other.GetSourceKey()))
			snapshot.SetTheaters(append(snapshot.GetTheaters(), other))
			snapshot.GetShowtimes()[0].SetTheaterId(other.GetId())
		},
		"duplicate theater": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.SetTheaters(append(snapshot.GetTheaters(), proto.CloneOf(snapshot.GetTheaters()[0])))
		},
		"duplicate movie": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.SetMovies(append(snapshot.GetMovies(), proto.CloneOf(snapshot.GetMovies()[0])))
		},
		"duplicate auditorium": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.SetAuditoriums(append(snapshot.GetAuditoriums(), proto.CloneOf(snapshot.GetAuditoriums()[0])))
		},
		"duplicate showtime": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.SetShowtimes(append(snapshot.GetShowtimes(), proto.CloneOf(snapshot.GetShowtimes()[0])))
		},
	}
	if err := NormalizeSnapshot(nil); err == nil {
		t.Fatal("NormalizeSnapshot(nil) accepted a nil snapshot")
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := catalogSnapshotFixture(observedAt)
			mutate(snapshot)
			if err := NormalizeSnapshot(snapshot); err == nil {
				t.Fatal("NormalizeSnapshot() accepted invalid catalog")
			}
		})
	}
}

func TestNormalizeSeatMapCanonicalizesLayout(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	layout := validSeatMapLayout(2)
	layout.GetSeats()[0].SetFeatures([]string{" aisle ", "premium", "aisle"})
	layout.SetSeats([]*seatmappb.Seat{layout.GetSeats()[1], layout.GetSeats()[0]})
	snapshot := &seatmappb.Snapshot{}
	snapshot.SetAuditoriumId(" auditorium ")
	snapshot.SetCapacity(2)
	snapshot.SetLayout(layout)

	if err := NormalizeSeatMap(snapshot, now); err != nil {
		t.Fatalf("NormalizeSeatMap() error = %v", err)
	}
	if snapshot.GetAuditoriumId() != "auditorium" || snapshot.GetLayout().GetSeats()[0].GetLabel() != "A1" ||
		!reflect.DeepEqual(snapshot.GetLayout().GetSeats()[0].GetFeatures(), []string{"aisle", "premium"}) {
		t.Fatalf("canonical snapshot = %+v", snapshot)
	}
	if snapshot.GetLayoutHash() == "" || snapshot.GetId() != SeatMapVersionID(snapshot.GetAuditoriumId(), snapshot.GetLayoutHash()) {
		t.Fatalf("seat-map identity = %+v", snapshot)
	}
	if !snapshot.GetObservedAt().AsTime().Equal(now) {
		t.Fatalf("observedAt = %v, want %v", snapshot.GetObservedAt().AsTime(), now)
	}
}

func TestNormalizeSeatMapRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	tests := map[string]*seatmappb.Snapshot{
		"missing auditorium": seatMapFixture("", 1, validSeatMapLayout(1), nil),
		"missing capacity":   seatMapFixture("auditorium", 0, validSeatMapLayout(1), nil),
		"missing layout":     seatMapFixture("auditorium", 1, nil, nil),
		"future observation": seatMapFixture("auditorium", 1, validSeatMapLayout(1), timestamppb.New(now.Add(MaximumObservationClockSkew+time.Second))),
		"noncanonical id":    seatMapFixture("auditorium", 1, validSeatMapLayout(1), nil),
	}
	tests["noncanonical id"].SetId("wrong")
	if err := NormalizeSeatMap(nil, now); err == nil {
		t.Fatal("NormalizeSeatMap(nil) accepted a nil value")
	}
	for name, snapshot := range tests {
		t.Run(name, func(t *testing.T) {
			if err := NormalizeSeatMap(snapshot, now); err == nil {
				t.Fatal("NormalizeSeatMap() accepted invalid input")
			}
		})
	}
}

func TestNormalizeSeatMapRejectsCorruptCanonicalLayout(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	tests := map[string]func(*seatmappb.Layout){
		"wrong auditorium": func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetAuditoriumId("other") },
		"wrong id":         func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetId("wrong") },
		"wrong label":      func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetLabel("A2") },
		"lowercase row":    func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetRow("a") },
		"missing type":     func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetType(" ") },
		"invalid x":        func(layout *seatmappb.Layout) { layout.GetSeats()[0].SetX(1.01) },
		"duplicate label": func(layout *seatmappb.Layout) {
			layout.SetSeats(append(layout.GetSeats(), proto.CloneOf(layout.GetSeats()[0])))
		},
		"invalid zone": func(layout *seatmappb.Layout) {
			layout.GetZones()[0].SetMinX(0.8)
			layout.GetZones()[0].SetMaxX(0.2)
		},
		"invalid block": func(layout *seatmappb.Layout) { layout.GetBlocks()[0].SetMaxY(1.1) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			layout := validSeatMapLayout(1)
			mutate(layout)
			if err := NormalizeSeatMap(seatMapFixture("auditorium", numeric.ClampInt32(len(layout.GetSeats())), layout, nil), now); err == nil {
				t.Fatal("NormalizeSeatMap() accepted corrupt canonical layout")
			}
		})
	}
	if err := NormalizeSeatMap(seatMapFixture("auditorium", 2, validSeatMapLayout(1), nil), now); err == nil {
		t.Fatal("NormalizeSeatMap() accepted a capacity mismatch")
	}
}

func validSeatMapLayout(count int) *seatmappb.Layout {
	const auditoriumID = "auditorium"
	seats := make([]*seatmappb.Seat, 0, count)
	for number := 1; number <= count; number++ {
		label := "A" + string(rune('0'+number))
		seat := &seatmappb.Seat{}
		seat.SetId(SeatID(auditoriumID, label))
		seat.SetAuditoriumId(auditoriumID)
		seat.SetLabel(label)
		seat.SetRow("A")
		seat.SetNumber(int32(number))
		seat.SetX(float64(number) / float64(count+1))
		seat.SetY(0.5)
		seat.SetType("standard")
		seats = append(seats, seat)
	}
	zone := &seatmappb.LayoutZone{}
	zone.SetCode("zone-1")
	zone.SetName("일반")
	zone.SetMinX(0)
	zone.SetMaxX(1)
	zone.SetMinY(0)
	zone.SetMaxY(1)
	zone.SetCapacity(numeric.ClampInt32(count))
	block := &seatmappb.LayoutBlock{}
	block.SetCode("block-1")
	block.SetName("중앙")
	block.SetMinX(0)
	block.SetMaxX(1)
	block.SetMinY(0)
	block.SetMaxY(1)
	layout := &seatmappb.Layout{}
	layout.SetSeats(seats)
	layout.SetZones([]*seatmappb.LayoutZone{zone})
	layout.SetBlocks([]*seatmappb.LayoutBlock{block})
	return layout
}

func seatMapFixture(auditoriumID string, capacity int32, layout *seatmappb.Layout, observedAt *timestamppb.Timestamp) *seatmappb.Snapshot {
	snapshot := &seatmappb.Snapshot{}
	snapshot.SetAuditoriumId(auditoriumID)
	snapshot.SetCapacity(capacity)
	snapshot.SetLayout(layout)
	if observedAt != nil {
		snapshot.SetObservedAt(observedAt)
	}
	return snapshot
}

func TestValidateObservationTime(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	if err := ValidateObservationTime(time.Time{}, now); err == nil {
		t.Fatal("zero observation time was accepted")
	}
	if err := ValidateObservationTime(now.Add(MaximumObservationClockSkew), now); err != nil {
		t.Fatalf("maximum tolerated clock skew = %v", err)
	}
	if err := ValidateObservationTime(now.Add(MaximumObservationClockSkew+time.Nanosecond), now); err == nil {
		t.Fatal("future observation beyond tolerance was accepted")
	}
}

func catalogSnapshotFixture(observedAt time.Time) *catalogpb.CatalogSnapshot {
	provider := &catalogpb.Provider{}
	provider.SetId(ProviderCGV)
	provider.SetName("CGV")
	theater := &catalogpb.Theater{}
	theater.SetProviderId(provider.GetId())
	theater.SetSourceKey("0056")
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	theater.SetId(CatalogID(provider.GetId(), "theater", theater.GetSourceKey()))
	movie := &catalogpb.Movie{}
	movie.SetProviderId(provider.GetId())
	movie.SetSourceKey("00001234")
	movie.SetTitle("테스트 영화")
	movie.SetId(CatalogID(provider.GetId(), "movie", movie.GetSourceKey()))
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetTheaterId(theater.GetId())
	auditorium.SetSourceKey("0056/0007")
	auditorium.SetName("IMAX관")
	auditorium.SetCapacity(624)
	auditorium.SetId(CatalogID(provider.GetId(), "auditorium", auditorium.GetSourceKey()))
	showtime := &catalogpb.Showtime{}
	showtime.SetProviderId(provider.GetId())
	showtime.SetSourceKey("0056/2026-08-14/0007/0003")
	showtime.SetTheaterId(theater.GetId())
	showtime.SetMovie(proto.CloneOf(movie))
	showtime.SetAuditorium(proto.CloneOf(auditorium))
	showtime.SetStartsAt(timestamppb.New(observedAt.Add(time.Hour)))
	showtime.SetEndsAt(timestamppb.New(observedAt.Add(3 * time.Hour)))
	showtime.SetCapacity(624)
	showtime.SetId(CatalogID(provider.GetId(), "showtime", showtime.GetSourceKey()))
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetMovies([]*catalogpb.Movie{movie})
	snapshot.SetAuditoriums([]*catalogpb.Auditorium{auditorium})
	snapshot.SetShowtimes([]*catalogpb.Showtime{showtime})
	snapshot.SetObservedAt(timestamppb.New(observedAt))
	return snapshot
}
