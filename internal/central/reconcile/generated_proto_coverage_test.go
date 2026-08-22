package reconcile

import (
	"testing"
	"time"

	"github.com/cineko-org/central/internal/observation/planning"
)

func TestProtoLocalDatesSkipsInvalidProtoDateInputs(t *testing.T) {
	dates := protoLocalDates([]string{"2026-08-20", "invalid", "2026-02-30", "2026-08-21"})
	if len(dates) != 2 || dates[0].GetYear() != 2026 || dates[0].GetMonth() != 8 || dates[0].GetDay() != 20 ||
		dates[1].GetDay() != 21 {
		t.Fatalf("protoLocalDates() = %+v", dates)
	}
	if got := protoLocalDates(nil); len(got) != 0 {
		t.Fatalf("nil protoLocalDates() = %+v", got)
	}
	if got := protoLocalDates([]string{"2026-08-22", "2026-08-20", "2026-08-20"}); len(got) != 3 {
		t.Fatalf("duplicate protoLocalDates() = %+v", got)
	}
}

func TestNormalizeAssignmentTargetDatesRejectsDuplicatesAndSorts(t *testing.T) {
	dates, err := normalizeAssignmentTargetDates([]string{"2026-08-22", "2026-08-20", "2026-08-21"})
	if err != nil || len(dates) != 3 || dates[0] != "2026-08-20" || dates[1] != "2026-08-21" || dates[2] != "2026-08-22" {
		t.Fatalf("sorted assignment target dates = %+v, error = %v", dates, err)
	}
	if _, err := normalizeAssignmentTargetDates([]string{"2026-08-20", "2026-08-20"}); err == nil {
		t.Fatal("duplicate assignment target dates accepted")
	}
	if _, err := normalizeAssignmentTargetDates([]string{"2026-02-30"}); err == nil {
		t.Fatal("invalid assignment target date accepted")
	}
}

func TestNewAssignmentRejectsDuplicateTargetDates(t *testing.T) {
	engine := &Engine{newID: func() (string, error) { return "assignment-1", nil }}
	_, err := engine.newAssignment(
		Policy{},
		planning.Result{TargetDates: []string{"2026-08-20", "2026-08-20"}},
		nil,
		time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	)
	if err == nil {
		t.Fatal("duplicate assignment target dates accepted")
	}
}
