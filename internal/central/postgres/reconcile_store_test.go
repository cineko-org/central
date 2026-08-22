package postgres

import (
	"testing"
	"time"

	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
)

func TestSeatMapBackfillDatesCoverFourteenLocalCalendarDays(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 21, 15, 30, 0, 0, time.UTC)
	dates := seatMapBackfillDates(now)
	if len(dates) != seatMapBackfillHorizonDays {
		t.Fatalf("seat-map backfill dates = %d", len(dates))
	}
	first, last := dates[0], dates[len(dates)-1]
	if first.GetYear() != 2026 || first.GetMonth() != 8 || first.GetDay() != 22 ||
		last.GetYear() != 2026 || last.GetMonth() != 9 || last.GetDay() != 4 {
		t.Fatalf("seat-map backfill range = %v .. %v", first, last)
	}
}

func TestAssignmentTargetDatesUsesExploratorySeatMapWindow(t *testing.T) {
	t.Parallel()
	seatMap := &observationpb.SeatMapTask{}
	seatMap.SetTargetDates(seatMapBackfillDates(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)))
	task := &observationpb.AssignmentTask{}
	task.SetSeatMap(seatMap)
	dates, err := assignmentTargetDates(task)
	if err != nil || len(dates) != seatMapBackfillHorizonDays {
		t.Fatalf("assignment target dates = %v, %v", dates, err)
	}
}
