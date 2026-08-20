package postgres

import "testing"

func TestExecutionWaitsForAvailability(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{reason: executionReasonPreferredSeatsUnavailable, want: true},
		{reason: executionReasonShowtimeUnavailable, want: true},
		{reason: "booking_preparation_failed", want: false},
		{reason: "execution_lease_lost", want: false},
	}
	for _, test := range tests {
		if got := executionWaitsForAvailability(test.reason); got != test.want {
			t.Fatalf("executionWaitsForAvailability(%q) = %t, want %t", test.reason, got, test.want)
		}
	}
}
