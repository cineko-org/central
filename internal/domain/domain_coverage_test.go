package domain

import "testing"

func TestSeatTypeValidation(t *testing.T) {
	t.Parallel()

	for _, seatType := range []SeatType{
		SeatTypeStandard, SeatTypeWheelchair, SeatTypeCompanion, SeatTypeCouple,
		SeatTypeRecliner, SeatTypeMotion, SeatTypeBed, SeatTypeUnknown,
	} {
		if !seatType.Valid() {
			t.Fatalf("%q should be valid", seatType)
		}
	}
	if SeatType("invalid").Valid() {
		t.Fatal("unknown seat type accepted")
	}
}
