package reconcile

import "testing"

func TestProtoLocalDatesSkipsInvalidProtoDateInputs(t *testing.T) {
	dates := protoLocalDates([]string{"2026-08-20", "invalid", "2026-02-30", "2026-08-21"})
	if len(dates) != 2 || dates[0].GetYear() != 2026 || dates[0].GetMonth() != 8 || dates[0].GetDay() != 20 ||
		dates[1].GetDay() != 21 {
		t.Fatalf("protoLocalDates() = %+v", dates)
	}
	if got := protoLocalDates(nil); len(got) != 0 {
		t.Fatalf("nil protoLocalDates() = %+v", got)
	}
}
