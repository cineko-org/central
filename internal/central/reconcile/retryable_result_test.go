package reconcile

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestFailedResultRetriesAnotherEligibleProbe(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	cycle := newMemoryCycle()
	cycle.retryable = []RetryableFailure{{AssignmentID: "failed_attempt", Deadline: now.Add(time.Minute)}}
	cycle.retries["failed_attempt"] = RetryAvailability{Remaining: 1}
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)

	report, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.GetRequeuedAssignments() != 1 || report.GetFailedAssignments() != 0 {
		t.Fatalf("report = %+v", report)
	}
	if got := cycle.requeued["failed_attempt"]; got != now.Add(2*time.Second) {
		t.Fatalf("retry not-before = %v", got)
	}
	if _, terminal := cycle.finished["failed_attempt"]; terminal {
		t.Fatalf("failed attempt was terminalized: %+v", cycle.finished)
	}
}

func TestFailedResultRetryFailuresArePropagated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		failAt    string
		remaining int
	}{
		{name: "list retryable failures", failAt: "retryable_failures"},
		{name: "inspect retry availability", failAt: "retry_availability", remaining: 1},
		{name: "requeue retry", failAt: "requeue", remaining: 1},
		{name: "finish exhausted retry", failAt: "finish_expired"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cycle := newMemoryCycle()
			cycle.failAt = test.failAt
			if test.failAt != "retryable_failures" {
				cycle.retryable = []RetryableFailure{{AssignmentID: "failed_attempt", Deadline: now.Add(time.Minute)}}
				cycle.retries["failed_attempt"] = RetryAvailability{Remaining: test.remaining}
			}
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			if _, err := engine.RunOnce(context.Background()); err == nil {
				t.Fatalf("%s did not fail", test.name)
			}
		})
	}
}

func TestFailedResultRetryBackoffFailureIsPropagated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	cycle := newMemoryCycle()
	cycle.retryable = []RetryableFailure{{AssignmentID: "failed_attempt", Deadline: now.Add(time.Minute)}}
	cycle.retries["failed_attempt"] = RetryAvailability{Remaining: 1}
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
	engine.randomDuration = func(time.Duration, time.Duration) (time.Duration, error) {
		return 0, io.ErrUnexpectedEOF
	}
	if _, err := engine.RunOnce(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("retry backoff error = %v", err)
	}
}

func TestFailedResultTerminalizesOnlyWhenRetryPolicyIsExhausted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		deadline  time.Time
		remaining int
	}{
		{name: "eligible probes exhausted", deadline: now.Add(time.Minute)},
		{name: "target deadline reached", deadline: now, remaining: 1},
		{name: "backoff cannot fit", deadline: now.Add(time.Second), remaining: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cycle := newMemoryCycle()
			cycle.retryable = []RetryableFailure{{AssignmentID: "failed_attempt", Deadline: test.deadline}}
			cycle.retries["failed_attempt"] = RetryAvailability{Remaining: test.remaining}
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			report, err := engine.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if report.GetFailedAssignments() != 1 || report.GetRequeuedAssignments() != 0 ||
				cycle.finished["failed_attempt"] != OutcomeFailed {
				t.Fatalf("report = %+v, finished = %+v", report, cycle.finished)
			}
		})
	}
}
