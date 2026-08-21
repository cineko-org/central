package domain

import (
	"testing"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

func TestSeatRankerPrefersExplicitContiguousGroup(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "H9", Row: "H", Number: 9, X: .42, Y: .55, Type: SeatTypeStandard},
		{Label: "H10", Row: "H", Number: 10, X: .48, Y: .55, Type: SeatTypeStandard},
		{Label: "H11", Row: "H", Number: 11, X: .52, Y: .55, Type: SeatTypeStandard},
		{Label: "H12", Row: "H", Number: 12, X: .58, Y: .55, Type: SeatTypeStandard},
	}}
	live := []LiveSeat{
		{Label: "H9", Available: true}, {Label: "H10", Available: true},
		{Label: "H11", Available: true}, {Label: "H12", Available: true},
	}
	preference := &clientpb.SeatPreference{}
	preference.SetExplicitSeats([]string{"H11", "H12"})
	preference.SetTogether(true)
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, preference)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if groups[0].Seats[0].Label != "H11" || groups[0].Seats[1].Label != "H12" {
		t.Fatalf("best group = %+v", groups[0].Seats)
	}
}

func TestSeatRankerRejectsGapForTogetherPreference(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "A1", Row: "A", Number: 1, X: .4, Y: .2},
		{Label: "A3", Row: "A", Number: 3, X: .6, Y: .2},
	}}
	live := []LiveSeat{{Label: "A1", Available: true}, {Label: "A3", Available: true}}
	preference := &clientpb.SeatPreference{}
	preference.SetTogether(true)
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, preference)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none", groups)
	}
}

func TestSeatRankerDoesNotJoinSeatsAcrossAisle(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "J10", Row: "J", Number: 10, X: .45, Y: .6, RightAisle: true},
		{Label: "J11", Row: "J", Number: 11, X: .55, Y: .6, LeftAisle: true},
	}}
	live := []LiveSeat{{Label: "J10", Available: true}, {Label: "J11", Available: true}}
	preference := &clientpb.SeatPreference{}
	preference.SetTogether(true)
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, preference)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none across an aisle", groups)
	}
}
