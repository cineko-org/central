package seatavailability

import (
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNormalizeCanonicalizesAvailabilitySet(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	snapshot := availabilitySnapshot(now, "seat-b", " seat-a ")
	if err := Normalize(snapshot, now); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got := snapshot.GetAvailableSeats(); len(got) != 2 || got[0].GetSeatId() != "seat-a" || got[1].GetSeatId() != "seat-b" {
		t.Fatalf("normalized seats = %+v", got)
	}
	firstHash := ContentHash(snapshot)
	reordered := availabilitySnapshot(now.Add(time.Second), "seat-a", "seat-b")
	if err := Normalize(reordered, now.Add(time.Second)); err != nil {
		t.Fatalf("Normalize(reordered) error = %v", err)
	}
	if secondHash := ContentHash(reordered); secondHash != firstHash {
		t.Fatalf("ContentHash() = %s, want %s", secondHash, firstHash)
	}
}

func TestNormalizeRejectsDuplicateSeats(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	if err := Normalize(availabilitySnapshot(now, "seat-a", "seat-a"), now); err == nil {
		t.Fatal("Normalize() accepted duplicate seat ids")
	}
}

func TestEvaluateRequiresAnAdjacentExplicitGroup(t *testing.T) {
	t.Parallel()
	preference := &clientpb.SeatPreference{}
	preference.SetTogether(true)
	preference.SetExplicitSeats([]string{"A1", "A2"})
	preset := &clientpb.Preset{}
	preset.SetSeatCount(2)
	preset.SetSeatPreference(preference)
	layout := &seatmappb.Layout{}
	layout.SetSeats([]*seatmappb.Seat{
		seat("seat-a1", "A1", "A", 1, false, false),
		seat("seat-a2", "A2", "A", 2, false, false),
		seat("seat-a3", "A3", "A", 3, false, false),
	})

	matched := Evaluate(layout, preset, availabilitySnapshot(time.Now().UTC(), "seat-a1", "seat-a2"))
	if !matched.Exact || !matched.Available {
		t.Fatalf("Evaluate(adjacent) = %+v", matched)
	}
	notMatched := Evaluate(layout, preset, availabilitySnapshot(time.Now().UTC(), "seat-a1", "seat-a3"))
	if !notMatched.Exact || notMatched.Available {
		t.Fatalf("Evaluate(non-adjacent) = %+v", notMatched)
	}
}

func TestEvaluateFallsBackToCoarseWakeWithoutMatchingLayout(t *testing.T) {
	t.Parallel()
	preference := &clientpb.SeatPreference{}
	preference.SetTogether(true)
	preset := &clientpb.Preset{}
	preset.SetSeatCount(2)
	preset.SetSeatPreference(preference)
	match := Evaluate(nil, preset, availabilitySnapshot(time.Now().UTC(), "seat-a1", "seat-a2"))
	if match.Exact || !match.Available {
		t.Fatalf("Evaluate(nil layout) = %+v", match)
	}
}

func TestEvaluateCountsAvailableSeatsWithoutAdjacencyRequirement(t *testing.T) {
	t.Parallel()
	preset := &clientpb.Preset{}
	preset.SetSeatCount(2)
	preset.SetSeatPreference(&clientpb.SeatPreference{})

	matched := Evaluate(nil, preset, availabilitySnapshot(time.Now().UTC(), "seat-a1", "seat-a2"))
	if !matched.Exact || !matched.Available {
		t.Fatalf("Evaluate(enough seats) = %+v", matched)
	}
	notMatched := Evaluate(nil, preset, availabilitySnapshot(time.Now().UTC(), "seat-a1"))
	if !notMatched.Exact || notMatched.Available {
		t.Fatalf("Evaluate(insufficient seats) = %+v", notMatched)
	}
}

func availabilitySnapshot(observedAt time.Time, seats ...string) *seatmappb.AvailabilitySnapshot {
	snapshot := &seatmappb.AvailabilitySnapshot{}
	snapshot.SetShowtimeId("showtime-1")
	snapshot.SetAuditoriumId("auditorium-1")
	snapshot.SetLayoutHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	snapshot.SetObservedAt(timestamppb.New(observedAt))
	values := make([]*seatmappb.AvailableSeat, 0, len(seats))
	for _, id := range seats {
		value := &seatmappb.AvailableSeat{}
		value.SetSeatId(id)
		values = append(values, value)
	}
	snapshot.SetAvailableSeats(values)
	return snapshot
}

func seat(id, label, row string, number int32, leftAisle, rightAisle bool) *seatmappb.Seat {
	value := &seatmappb.Seat{}
	value.SetId(id)
	value.SetLabel(label)
	value.SetRow(row)
	value.SetNumber(number)
	value.SetLeftAisle(leftAisle)
	value.SetRightAisle(rightAisle)
	return value
}
