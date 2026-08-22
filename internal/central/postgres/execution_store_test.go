package postgres

import (
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
)

func TestExecutionCompletionStateHonorsProtoOutcome(t *testing.T) {
	now := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		completion central.ExecutionCompletion
		attempts   int
		wantStatus string
		wantDone   bool
	}{
		{
			name: "terminal failed is never inferred retryable",
			completion: central.ExecutionCompletion{
				Status: "failed", ReasonCode: "authentication_required", Now: now,
			},
			attempts: 1, wantStatus: "failed", wantDone: true,
		},
		{
			name: "explicit retry request uses remaining budget",
			completion: central.ExecutionCompletion{
				Status: "retry_requested", ReasonCode: "booking_preparation_failed", Now: now,
			},
			attempts: 1, wantStatus: "queued", wantDone: false,
		},
		{
			name: "explicit retry request exhausts budget",
			completion: central.ExecutionCompletion{
				Status: "retry_requested", ReasonCode: "booking_preparation_failed", Now: now,
			},
			attempts: 3, wantStatus: "failed", wantDone: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, completedAt := executionCompletionState(test.completion, test.attempts)
			if status != test.wantStatus || (completedAt != nil) != test.wantDone {
				t.Fatalf("state = %q/%v, want %q/done=%t", status, completedAt, test.wantStatus, test.wantDone)
			}
		})
	}
}

func TestExecutionOutcomeIsUnknown(t *testing.T) {
	for _, reason := range []string{"execution_lease_lost", "client_interrupted"} {
		if !executionOutcomeIsUnknown(reason) {
			t.Fatalf("%q must require user confirmation", reason)
		}
	}
	for _, reason := range []string{"authentication_required", "booking_preparation_failed"} {
		if executionOutcomeIsUnknown(reason) {
			t.Fatalf("%q must not be classified as an unknown result", reason)
		}
	}
}
