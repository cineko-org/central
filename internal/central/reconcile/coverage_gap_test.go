package reconcile

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
)

func TestReconcilePropagatesSeatAvailabilityFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	cycle := newMemoryCycle()
	cycle.failAt = "seat_availability"
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
	if _, err := engine.RunOnce(context.Background()); err == nil {
		t.Fatal("seat-availability failure was not propagated")
	}
}

func TestScheduleSeatAvailabilityBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		failAt     string
		candidates []CandidateProbe
		busy       bool
		idError    bool
		wantError  bool
	}{
		{name: "target lookup", failAt: "seat_availability", wantError: true},
		{name: "probe lookup", failAt: "eligible_probes", candidates: []CandidateProbe{{ID: "probe"}}, wantError: true},
		{name: "no eligible probes"},
		{name: "assignment id", candidates: []CandidateProbe{{ID: "probe"}}, idError: true, wantError: true},
		{name: "busy target", candidates: []CandidateProbe{{ID: "probe"}}, busy: true},
		{name: "assignment failure", failAt: "create_assignment", candidates: []CandidateProbe{{ID: "probe"}}, wantError: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cycle := newMemoryCycle()
			cycle.failAt = test.failAt
			if test.candidates != nil || test.failAt == "eligible_probes" || test.failAt == "create_assignment" || test.name == "no eligible probes" {
				cycle.seatAvailabilityTarget = &SeatAvailabilityTarget{Task: seatAvailabilityAssignmentTask()}
			}
			cycle.candidates[""] = test.candidates
			if test.busy {
				cycle.busyPolicies[""] = true
			}
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			if test.idError {
				engine.newID = func() (string, error) { return "", errors.New("id failure") }
			}
			report := &adminpb.ReconcileReport{}
			err := engine.scheduleSeatAvailability(context.Background(), cycle, now, report)
			if test.wantError != (err != nil) {
				t.Fatalf("error = %v, want error = %t", err, test.wantError)
			}
			if !test.wantError && report.GetCreatedAssignments() != 0 && test.busy {
				t.Fatalf("busy target created an assignment: %+v", cycle.created)
			}
		})
	}
}

func TestRunAndRecordOptionalRepositoryBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)

	withoutDeadline := &repositoryWithoutOptional{delegate: &memoryRepository{cycle: newMemoryCycle(), leader: true}}
	engine := newTestEngine(t, withoutDeadline, now)
	if deadline := engine.runAndRecord(context.Background()); deadline != nil {
		t.Fatalf("deadline from repository without optional capability = %v", deadline)
	}

	deadlineError := errors.New("deadline unavailable")
	withError := &memoryRepository{cycle: newMemoryCycle(), leader: true, deadlineErr: deadlineError}
	engine = newTestEngine(t, withError, now)
	if deadline := engine.runAndRecord(context.Background()); deadline != nil {
		t.Fatalf("deadline from failed lookup = %v", deadline)
	}

	wanted := now.Add(2 * time.Second)
	withDeadline := &memoryRepository{cycle: newMemoryCycle(), leader: true, deadline: &wanted}
	engine = newTestEngine(t, withDeadline, now)
	if deadline := engine.runAndRecord(context.Background()); deadline == nil || !deadline.Equal(wanted) {
		t.Fatalf("deadline = %v, want %v", deadline, wanted)
	}
}

func TestStartWakeupListenerOptionalAndRetryBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	withoutWakeup := &repositoryWithoutOptional{delegate: &memoryRepository{cycle: newMemoryCycle(), leader: true}}
	engine := newTestEngine(t, withoutWakeup, now)
	if wakeups := engine.startWakeupListener(context.Background()); wakeups != nil {
		t.Fatal("repository without wakeup capability returned a channel")
	}

	repository := &scriptedWakeupRepository{
		calls:    make(chan struct{}, 2),
		results:  make(chan error, 1),
		returned: make(chan struct{}, 2),
	}
	engine = newTestEngine(t, repository, now)
	engine.config.TickInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	wakeups := engine.startWakeupListener(ctx)
	if wakeups == nil {
		t.Fatal("wakeup-capable repository returned no channel")
	}
	repository.results <- errors.New("temporary wakeup failure")
	select {
	case <-repository.calls:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not call the repository")
	}
	select {
	case <-repository.calls:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not retry after the failure")
	}
	repository.results <- nil
	select {
	case <-wakeups:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not publish a successful wakeup")
	}
	cancel()
	select {
	case <-repository.returned:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not stop after cancellation")
	}
}

func TestStartWakeupListenerCancelsClampedRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	repository := &scriptedWakeupRepository{
		calls:    make(chan struct{}, 2),
		results:  make(chan error, 1),
		returned: make(chan struct{}, 2),
	}
	repository.results <- errors.New("temporary wakeup failure")
	done := make(chan struct{})
	ctx := &cancelAfterWakeupErrorContext{Context: context.Background(), done: done}
	engine := newTestEngine(t, repository, now)
	engine.config.TickInterval = 2 * time.Second
	if wakeups := engine.startWakeupListener(ctx); wakeups == nil {
		t.Fatal("wakeup-capable repository returned no channel")
	}
	select {
	case <-repository.returned:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not return the injected failure")
	}
	select {
	case <-repository.calls:
	case <-time.After(time.Second):
		t.Fatal("wakeup listener did not call the repository")
	}
	select {
	case <-repository.calls:
		t.Fatal("wakeup listener retried after cancellation")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSchedulerTimerStopAndReconcileDelayBoundaries(t *testing.T) {
	t.Parallel()
	if (schedulerTimer{}).Stop() {
		t.Fatal("zero scheduler timer reported stopped")
	}
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	maintenance := 5 * time.Second
	deadline := now.Add(2 * time.Second)
	for _, test := range []struct {
		name     string
		deadline *time.Time
		want     time.Duration
	}{
		{name: "no deadline", want: maintenance},
		{name: "past deadline", deadline: func() *time.Time { value := now.Add(-time.Second); return &value }(), want: maintenance},
		{name: "equal maintenance", deadline: func() *time.Time { value := now.Add(maintenance); return &value }(), want: maintenance},
		{name: "future deadline", deadline: &deadline, want: 2 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nextReconcileDelay(now, test.deadline, maintenance); got != test.want {
				t.Fatalf("nextReconcileDelay() = %v, want %v", got, test.want)
			}
		})
	}
}

type repositoryWithoutOptional struct {
	delegate *memoryRepository
}

func (repository *repositoryWithoutOptional) RunLeaderCycle(
	ctx context.Context,
	run func(CycleRepository) error,
) (bool, error) {
	return repository.delegate.RunLeaderCycle(ctx, run)
}

type scriptedWakeupRepository struct {
	calls    chan struct{}
	results  chan error
	returned chan struct{}
}

type cancelAfterWakeupErrorContext struct {
	context.Context
	done   chan struct{}
	checks atomic.Int32
}

func (ctx *cancelAfterWakeupErrorContext) Done() <-chan struct{} { return ctx.done }

func (ctx *cancelAfterWakeupErrorContext) Err() error {
	if ctx.checks.Add(1) == 1 {
		close(ctx.done)
		return nil
	}
	return context.Canceled
}

func (repository *scriptedWakeupRepository) RunLeaderCycle(
	context.Context,
	func(CycleRepository) error,
) (bool, error) {
	return false, nil
}

func (repository *scriptedWakeupRepository) WaitForReconcileWakeup(ctx context.Context) error {
	select {
	case repository.calls <- struct{}{}:
	default:
	}
	var err error
	select {
	case err = <-repository.results:
	case <-ctx.Done():
		err = ctx.Err()
	}
	select {
	case repository.returned <- struct{}{}:
	default:
	}
	return err
}
