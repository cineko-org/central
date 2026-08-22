package catalog

import (
	"testing"
	"time"
)

func TestSeatMapRetryDelayUsesBoundedExponentialBackoff(t *testing.T) {
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	for failures, expected := range want {
		if got := SeatMapRetryDelay(failures + 1); got != expected {
			t.Fatalf("SeatMapRetryDelay(%d) = %s, want %s", failures+1, got, expected)
		}
	}
	if got := SeatMapRetryDelay(SeatMapCollectionRetryLimit + 1); got != want[len(want)-1] {
		t.Fatalf("SeatMapRetryDelay(over budget) = %s, want %s", got, want[len(want)-1])
	}
}

func TestSeatMapCollectionBlockedAfterRetryBudget(t *testing.T) {
	if SeatMapCollectionBlockedAfter(SeatMapCollectionRetryLimit) {
		t.Fatal("retry budget blocked before the final retry")
	}
	if !SeatMapCollectionBlockedAfter(SeatMapCollectionRetryLimit + 1) {
		t.Fatal("retry budget did not block after the final retry")
	}
}
