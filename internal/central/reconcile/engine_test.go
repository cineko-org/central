package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/observation/planning"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestReconcileCycleDecisions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	cycle := newMemoryCycle()
	cycle.staleProbes = 2
	cycle.deletedProbes = 1
	cycle.expired = []ExpiredLease{
		{AssignmentID: "retry", ProbeID: "probe_1", Deadline: now.Add(time.Minute)},
		{AssignmentID: "exhausted", ProbeID: "probe_2", Deadline: now.Add(time.Minute)},
		{AssignmentID: "too_late", ProbeID: "probe_3", Deadline: now.Add(time.Second)},
	}
	cycle.retries["retry"] = RetryAvailability{Remaining: 1}
	cycle.retries["too_late"] = RetryAvailability{Remaining: 1}
	cycle.timedOut = []TimedOutAssignment{
		{AssignmentID: "never_claimed"},
		{AssignmentID: "attempted", AttemptCount: 1},
	}
	cycle.terminal = []TerminalPolicyRun{
		{
			PolicyID: "completed", Enabled: true, FinishedAt: now.Add(-time.Minute), Outcome: OutcomeCompleted,
			MinimumInterval: 10 * time.Second, MaximumInterval: 20 * time.Second,
		},
		{PolicyID: "disabled", FinishedAt: now.Add(-time.Minute), Outcome: OutcomeFailed},
	}
	cycle.due = []Policy{
		validPolicy("queued", "theater_queued", now, "rolling"),
		validPolicy("missed", "theater_missed", now, "explicit"),
		validPolicy("invalid", "theater_invalid", now, "invalid"),
		validPolicy("busy", "theater_busy", now, "explicit"),
	}
	cycle.candidates["queued"] = []CandidateProbe{{ID: "probe_1", NetworkID: "net_1"}}
	cycle.busyPolicies["busy"] = true
	oldestDue := now.Add(-5 * time.Second)
	cycle.oldestDue = &oldestDue
	repository := &memoryRepository{cycle: cycle, leader: true}
	engine := newTestEngine(t, repository, now)
	ids := []string{"assignment_queued", "assignment_missed", "assignment_invalid", "assignment_busy"}
	engine.newID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}

	report, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Leader || report.StaleProbes != 2 || report.DeletedProbes != 1 ||
		report.ExpiredLeases != 3 || report.RequeuedAssignments != 1 ||
		report.FailedAssignments != 3 || report.MissedAssignments != 2 ||
		report.AdvancedPolicies != 3 || report.CreatedAssignments != 2 ||
		report.DeferredPolicies != 1 || report.SuspendedPolicies != 1 || report.OldestDueAgeSeconds != 5 {
		t.Fatalf("report = %+v", report)
	}
	if got := cycle.requeued["retry"]; got != now.Add(2*time.Second) {
		t.Fatalf("retry not-before = %v", got)
	}
	if cycle.finished["exhausted"] != OutcomeFailed || cycle.finished["too_late"] != OutcomeFailed ||
		cycle.finished["never_claimed"] != OutcomeMissed || cycle.finished["attempted"] != OutcomeFailed {
		t.Fatalf("finished assignments = %+v", cycle.finished)
	}
	if len(cycle.created) != 2 {
		t.Fatalf("created assignments = %+v", cycle.created)
	}
	queued := cycle.created[0]
	if queued.Status != "queued" || !slices.Equal(queued.Task.TargetDates, []string{"2026-08-10"}) ||
		len(queued.Candidates) != 1 {
		t.Fatalf("queued assignment = %+v", queued)
	}
	missed := cycle.created[1]
	if missed.Status != OutcomeMissed || missed.ReasonCode != "no_eligible_probe" ||
		!slices.Equal(missed.Task.TargetDates, []string{"2026-08-20"}) {
		t.Fatalf("missed assignment = %+v", missed)
	}
	if cycle.suspended["invalid"] != "invalid_policy" {
		t.Fatalf("suspended policies = %+v", cycle.suspended)
	}
	if len(cycle.advanced) != 3 || cycle.advanced[0].next == nil ||
		*cycle.advanced[0].next != now.Add(10*time.Second) || cycle.advanced[1].next != nil ||
		cycle.advanced[2].next == nil || *cycle.advanced[2].next != now.Add(10*time.Second) {
		t.Fatalf("advanced policies = %+v", cycle.advanced)
	}
}

func TestReconcileFollowerSkipsCycle(t *testing.T) {
	t.Parallel()
	repository := &memoryRepository{cycle: newMemoryCycle(), leader: false}
	engine := newTestEngine(t, repository, time.Now().UTC())
	report, err := engine.RunOnce(context.Background())
	if err != nil || report.Leader || repository.calls != 1 {
		t.Fatalf("report = %+v, error = %v, calls = %d", report, err, repository.calls)
	}
}

func TestHotPolicyPreemptsQueuedBaselineBeforeScheduling(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 5, 0, time.UTC)
	cycle := newMemoryCycle()
	policy := validPolicy("hot", "theater_hot", now, "explicit")
	policy.HotTargets = []planning.MonitorTarget{{TargetDates: []string{"2026-08-21"}}}
	policy.HotTargetFingerprint = planning.Fingerprint(policy.HotTargets)
	policy.LastHotTargetFingerprint = planning.Fingerprint([]planning.MonitorTarget{{TargetDates: []string{"2026-08-20"}}})
	policy.LastHotTargetDates = []string{"2026-08-20"}
	policy.LastHotFinishedAt = now.Add(-time.Minute)
	policy.LastBaselineFinishedAt = now
	cycle.due = []Policy{policy}
	cycle.candidates[policy.ID] = []CandidateProbe{{ID: "probe_hot", NetworkID: "network_hot"}}
	cycle.created = []NewAssignment{{
		PolicyID: policy.ID, Lane: planning.LaneBaseline, Status: "queued",
	}}
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
	if _, err := engine.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cycle.preempted, []string{policy.ID}) {
		t.Fatalf("preempted policies = %v", cycle.preempted)
	}
	if len(cycle.created) != 1 || cycle.created[0].Lane != planning.LaneHot ||
		!slices.Equal(cycle.created[0].Task.TargetDates, []string{"2026-08-21"}) {
		t.Fatalf("created hot assignment = %+v", cycle.created)
	}
}

func TestCatalogRefreshWaitsForProbeAndCreatesOneSystemAssignment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	waiting := newMemoryCycle()
	waiting.catalogRequired = true
	engine := newTestEngine(t, &memoryRepository{cycle: waiting, leader: true}, now)
	report, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.CatalogRefreshWaiting || report.CatalogRefreshCreated || len(waiting.created) != 0 {
		t.Fatalf("waiting report = %+v, assignments = %+v", report, waiting.created)
	}

	ready := newMemoryCycle()
	ready.catalogRequired = true
	ready.candidates[""] = []CandidateProbe{{ID: "probe_catalog", NetworkID: "net_catalog"}}
	engine = newTestEngine(t, &memoryRepository{cycle: ready, leader: true}, now)
	report, err = engine.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.CatalogRefreshCreated || report.CatalogRefreshWaiting || len(ready.created) != 1 {
		t.Fatalf("created report = %+v, assignments = %+v", report, ready.created)
	}
	assignment := ready.created[0]
	if assignment.PolicyID != "" || assignment.Task.Kind != "cgv.catalog.capture.v1" ||
		assignment.Task.Theater.SourceKey != "__catalog__" || len(assignment.Task.TargetDates) != 0 ||
		len(assignment.Candidates) != 1 {
		t.Fatalf("catalog assignment = %+v", assignment)
	}
}

func TestCatalogRefreshFailureAndBusyBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	for _, stage := range []string{"eligible_probes", "create_assignment"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			cycle := newMemoryCycle()
			cycle.catalogRequired = true
			cycle.candidates[""] = []CandidateProbe{{ID: "probe", NetworkID: "net"}}
			cycle.failAt = stage
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			if _, err := engine.RunOnce(context.Background()); err == nil {
				t.Fatalf("catalog stage %q did not fail", stage)
			}
		})
	}
	cycle := newMemoryCycle()
	cycle.catalogRequired = true
	cycle.candidates[""] = []CandidateProbe{{ID: "probe", NetworkID: "net"}}
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
	engine.newID = func() (string, error) { return "", io.ErrUnexpectedEOF }
	if _, err := engine.RunOnce(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("catalog assignment id error = %v", err)
	}

	busy := newMemoryCycle()
	busy.catalogRequired = true
	busy.candidates[""] = []CandidateProbe{{ID: "probe", NetworkID: "net"}}
	busy.busyPolicies[""] = true
	engine = newTestEngine(t, &memoryRepository{cycle: busy, leader: true}, now)
	report, err := engine.RunOnce(context.Background())
	if err != nil || !report.CatalogRefreshWaiting || report.CatalogRefreshCreated {
		t.Fatalf("busy catalog refresh = %+v, %v", report, err)
	}
}

func TestSeatMapBackfillWaitsForAuthenticatedClientAndPrioritizesRequest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	task := central.AssignmentTask{
		Kind:       contracts.CapabilityCGVSeatMapCapture,
		Theater:    central.Theater{ID: "theater", ProviderID: contracts.ProviderCGV},
		Auditorium: &central.Auditorium{ID: "auditorium", TheaterID: "theater"},
		Showtime:   &central.Showtime{ID: "showtime"}, Locale: "ko-KR", TimeZone: "Asia/Seoul",
	}
	waiting := newMemoryCycle()
	waiting.seatMapTarget = &SeatMapBackfillTarget{Task: task, Requested: true}
	engine := newTestEngine(t, &memoryRepository{cycle: waiting, leader: true}, now)
	report, err := engine.RunOnce(context.Background())
	if err != nil || !report.SeatMapBackfillWaiting || len(waiting.created) != 0 {
		t.Fatalf("waiting seat-map report = %+v, assignments = %+v, error = %v", report, waiting.created, err)
	}

	ready := newMemoryCycle()
	ready.seatMapTarget = &SeatMapBackfillTarget{Task: task, Requested: true}
	ready.candidates[""] = []CandidateProbe{{ID: "client", NetworkID: "home"}}
	engine = newTestEngine(t, &memoryRepository{cycle: ready, leader: true}, now)
	report, err = engine.RunOnce(context.Background())
	if err != nil || !report.SeatMapBackfillCreated || len(ready.created) != 1 {
		t.Fatalf("created seat-map report = %+v, assignments = %+v, error = %v", report, ready.created, err)
	}
	assignment := ready.created[0]
	if assignment.Priority != 95 || assignment.Task.Auditorium == nil ||
		assignment.Task.Auditorium.ID != "auditorium" || len(assignment.Candidates) != 1 {
		t.Fatalf("seat-map assignment = %+v", assignment)
	}
}

func TestSeatMapBackfillBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	task := central.AssignmentTask{
		Kind:       contracts.CapabilityCGVSeatMapCapture,
		Theater:    central.Theater{ID: "theater", ProviderID: contracts.ProviderCGV},
		Auditorium: &central.Auditorium{ID: "auditorium", TheaterID: "theater"},
		Showtime:   &central.Showtime{ID: "showtime"}, Locale: "ko-KR", TimeZone: "Asia/Seoul",
	}

	for _, stage := range []string{"seat_map_backfill", "eligible_probes", "create_assignment"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			cycle := newMemoryCycle()
			cycle.failAt = stage
			cycle.seatMapTarget = &SeatMapBackfillTarget{Task: task}
			cycle.candidates[""] = []CandidateProbe{{ID: "client", NetworkID: "home"}}
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			if _, err := engine.RunOnce(context.Background()); err == nil {
				t.Fatalf("stage %q did not fail", stage)
			}
		})
	}

	idFailure := newMemoryCycle()
	idFailure.seatMapTarget = &SeatMapBackfillTarget{Task: task}
	idFailure.candidates[""] = []CandidateProbe{{ID: "client", NetworkID: "home"}}
	engine := newTestEngine(t, &memoryRepository{cycle: idFailure, leader: true}, now)
	engine.newID = func() (string, error) { return "", io.ErrUnexpectedEOF }
	if _, err := engine.RunOnce(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("assignment id error = %v", err)
	}

	busy := newMemoryCycle()
	busy.seatMapTarget = &SeatMapBackfillTarget{Task: task}
	busy.candidates[""] = []CandidateProbe{{ID: "client", NetworkID: "home"}}
	busy.busyPolicies[""] = true
	engine = newTestEngine(t, &memoryRepository{cycle: busy, leader: true}, now)
	report, err := engine.RunOnce(context.Background())
	if err != nil || !report.SeatMapBackfillWaiting || report.SeatMapBackfillCreated {
		t.Fatalf("busy seat-map report = %+v, %v", report, err)
	}

	normal := newMemoryCycle()
	normal.seatMapTarget = &SeatMapBackfillTarget{Task: task}
	normal.candidates[""] = []CandidateProbe{{ID: "client", NetworkID: "home"}}
	engine = newTestEngine(t, &memoryRepository{cycle: normal, leader: true}, now)
	if _, err := engine.RunOnce(context.Background()); err != nil || len(normal.created) != 1 || normal.created[0].Priority != 70 {
		t.Fatalf("normal seat-map assignment = %+v, %v", normal.created, err)
	}
}

func TestReconcilePropagatesCycleFailures(t *testing.T) {
	t.Parallel()
	stages := []string{
		"mark_stale", "delete_retired", "expired_leases", "expire_lease", "retryable_failures", "retry_availability",
		"requeue", "finish_expired", "timed_out", "finish_timed_out", "terminal_runs",
		"advance_terminal", "catalog_refresh", "due_policies", "eligible_probes", "create_assignment", "suspend_policy",
		"advance_missed", "oldest_due",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
			cycle := failureCycle(stage, now)
			engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)
			if _, err := engine.RunOnce(context.Background()); err == nil {
				t.Fatalf("stage %q did not fail", stage)
			}
		})
	}

	repositoryFailure := errors.New("leader transaction failed")
	engine := newTestEngine(t, &memoryRepository{err: repositoryFailure}, time.Now().UTC())
	if _, err := engine.RunOnce(context.Background()); !errors.Is(err, repositoryFailure) {
		t.Fatalf("repository error = %v", err)
	}
}

func TestReconcileRandomnessAndIDFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		cycle *memoryCycle
		idErr bool
	}{
		{name: "retry backoff", cycle: func() *memoryCycle {
			cycle := newMemoryCycle()
			cycle.expired = []ExpiredLease{{AssignmentID: "retry", Deadline: now.Add(time.Minute)}}
			cycle.retries["retry"] = RetryAvailability{Remaining: 1}
			return cycle
		}()},
		{name: "terminal interval", cycle: func() *memoryCycle {
			cycle := newMemoryCycle()
			cycle.terminal = []TerminalPolicyRun{{
				PolicyID: "policy", Enabled: true, FinishedAt: now, Outcome: OutcomeCompleted,
				MinimumInterval: time.Second, MaximumInterval: time.Second,
			}}
			return cycle
		}()},
		{name: "missed interval", cycle: func() *memoryCycle {
			cycle := newMemoryCycle()
			cycle.due = []Policy{validPolicy("missed", "theater", now, "explicit")}
			return cycle
		}()},
		{name: "assignment id", cycle: func() *memoryCycle {
			cycle := newMemoryCycle()
			cycle.due = []Policy{validPolicy("policy", "theater", now, "explicit")}
			return cycle
		}(), idErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := newTestEngine(t, &memoryRepository{cycle: test.cycle, leader: true}, now)
			if test.idErr {
				engine.newID = func() (string, error) { return "", io.ErrUnexpectedEOF }
			} else {
				engine.randomDuration = func(time.Duration, time.Duration) (time.Duration, error) {
					return 0, io.ErrUnexpectedEOF
				}
			}
			if _, err := engine.RunOnce(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestEngineConfigurationLifecycleAndHealth(t *testing.T) {
	t.Parallel()
	if _, err := New(nil, Config{}); err == nil {
		t.Fatal("nil repository was accepted")
	}
	invalid := []Config{
		{TickInterval: -time.Second},
		{RetryMinimum: 2 * time.Second, RetryMaximum: time.Second},
		{BatchSize: 1_001},
	}
	for _, config := range invalid {
		if _, err := New(&memoryRepository{}, config); err == nil {
			t.Fatalf("invalid config was accepted: %+v", config)
		}
	}

	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	repository := &memoryRepository{cycle: newMemoryCycle(), leader: true, called: make(chan struct{}, 1)}
	engine := newTestEngine(t, repository, now)
	if status := engine.Snapshot(); status.Healthy || status.Running {
		t.Fatalf("initial status = %+v", status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- engine.Run(ctx) }()
	<-repository.called
	if err := engine.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second run error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status := engine.Snapshot()
		if status.Healthy && status.Running && status.Leader {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("running status = %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if status := engine.Snapshot(); status.Running {
		t.Fatalf("stopped status = %+v", status)
	}

	repository.err = errors.New("cycle failed")
	engine.runAndRecord(context.Background())
	if status := engine.Snapshot(); status.Healthy || status.LastErrorCode != "cycle_failed" || status.LastErrorAt.IsZero() {
		t.Fatalf("failed status = %+v", status)
	}
	repository.err = nil
	engine.runAndRecord(context.Background())
	now = now.Add(4 * time.Hour)
	engine.clock = func() time.Time { return now }
	if status := engine.Snapshot(); status.Healthy {
		t.Fatalf("stale status = %+v", status)
	}
}

func TestEngineTickerAndShortHealthWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	repository := &memoryRepository{cycle: newMemoryCycle(), leader: true, called: make(chan struct{}, 4)}
	engine := newTestEngine(t, repository, now)
	engine.config.TickInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- engine.Run(ctx) }()
	<-repository.called
	<-repository.called
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if status := engine.Snapshot(); !status.Healthy {
		t.Fatalf("short-window status = %+v", status)
	}
}

func TestPolicyTargetDateValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	invalid := []Policy{
		{},
		func() Policy {
			policy := validPolicy("p", "b", now, "explicit")
			policy.TimeZone = "invalid"
			return policy
		}(),
		func() Policy {
			policy := validPolicy("p", "b", now, "explicit")
			policy.TargetDates = nil
			return policy
		}(),
		func() Policy {
			policy := validPolicy("p", "b", now, "explicit")
			policy.TargetDates = []string{"invalid"}
			return policy
		}(),
		func() Policy {
			policy := validPolicy("p", "b", now, "explicit")
			policy.TargetDates = []string{"2026-08-20", "2026-08-20"}
			return policy
		}(),
		func() Policy { policy := validPolicy("p", "b", now, "rolling"); policy.HorizonDays = 0; return policy }(),
		func() Policy { policy := validPolicy("p", "b", now, "unsupported"); return policy }(),
	}
	for _, policy := range invalid {
		if _, err := policyPlan(policy, now); err == nil {
			t.Fatalf("invalid policy was accepted: %+v", policy)
		}
	}
}

func TestRandomHelpers(t *testing.T) {
	t.Parallel()
	if _, err := secureRandomDuration(2*time.Second, time.Second); err == nil {
		t.Fatal("invalid random range was accepted")
	}
	for range 100 {
		value, err := secureRandomDuration(time.Second, 2*time.Second)
		if err != nil || value < time.Second || value > 2*time.Second {
			t.Fatalf("random duration = %v, %v", value, err)
		}
	}
	if id, err := newAssignmentID(); err != nil || len(id) <= len("assignment_") {
		t.Fatalf("assignment id = %q, %v", id, err)
	}
	if _, err := randomDuration(errorReader{}, time.Second, 2*time.Second); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("random reader error = %v", err)
	}
	if _, err := assignmentID(errorReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("assignment id reader error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func newTestEngine(t *testing.T, repository Repository, now time.Time) *Engine {
	t.Helper()
	engine, err := New(repository, Config{
		TickInterval: time.Hour, ProbeHeartbeatTTL: 90 * time.Second, OfflineRetention: 30 * 24 * time.Hour,
		RetryMinimum: 2 * time.Second, RetryMaximum: 2 * time.Second, BatchSize: 100,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.clock = func() time.Time { return now }
	engine.randomDuration = func(minimum, _ time.Duration) (time.Duration, error) { return minimum, nil }
	engine.newID = func() (string, error) { return "assignment_default", nil }
	return engine
}

func validPolicy(id, theaterID string, now time.Time, mode string) Policy {
	policy := Policy{
		ID: id, Enabled: true, TaskKind: "cgv.schedule.capture.v2",
		Theater: central.Theater{
			ID: theaterID, ProviderID: "cgv", SourceKey: theaterID,
			Region: "서울", Name: "용산아이파크몰",
		},
		TargetDateMode: mode, Locale: "ko-KR", TimeZone: "Asia/Seoul", EgressPolicyID: "scan_default",
		Priority: 50, MinimumInterval: 10 * time.Second, MaximumInterval: 20 * time.Second,
		ExecutionWindow: time.Minute, NextRunAt: now,
	}
	if mode == "rolling" {
		policy.HorizonDays = 2
	} else {
		policy.TargetDates = []string{"2026-08-20"}
	}
	return policy
}

func failureCycle(stage string, now time.Time) *memoryCycle {
	cycle := newMemoryCycle()
	cycle.failAt = stage
	switch stage {
	case "expire_lease", "retry_availability", "requeue", "finish_expired":
		cycle.expired = []ExpiredLease{{AssignmentID: "expired", Deadline: now.Add(time.Minute)}}
		cycle.retries["expired"] = RetryAvailability{Remaining: 1}
		if stage == "finish_expired" {
			cycle.retries["expired"] = RetryAvailability{}
		}
	case "retryable_failures":
	case "finish_timed_out":
		cycle.timedOut = []TimedOutAssignment{{AssignmentID: "timed_out"}}
	case "advance_terminal":
		cycle.terminal = []TerminalPolicyRun{{
			PolicyID: "terminal", Enabled: false, FinishedAt: now, Outcome: OutcomeFailed,
		}}
	case "eligible_probes", "create_assignment":
		cycle.due = []Policy{validPolicy("due", "theater", now, "explicit")}
	case "suspend_policy":
		cycle.due = []Policy{validPolicy("invalid", "theater", now, "invalid")}
	case "advance_missed":
		cycle.due = []Policy{validPolicy("missed", "theater", now, "explicit")}
	}
	return cycle
}

type memoryRepository struct {
	mu     sync.Mutex
	cycle  *memoryCycle
	leader bool
	err    error
	calls  int
	called chan struct{}
}

func (repository *memoryRepository) RunLeaderCycle(
	_ context.Context,
	run func(CycleRepository) error,
) (bool, error) {
	repository.mu.Lock()
	repository.calls++
	if repository.called != nil {
		select {
		case repository.called <- struct{}{}:
		default:
		}
	}
	err := repository.err
	leader := repository.leader
	cycle := repository.cycle
	repository.mu.Unlock()
	if err != nil {
		return false, err
	}
	if !leader {
		return false, nil
	}
	return true, run(cycle)
}

type policyAdvance struct {
	run  TerminalPolicyRun
	next *time.Time
}

type memoryCycle struct {
	failAt          string
	staleProbes     int
	deletedProbes   int
	expired         []ExpiredLease
	retryable       []RetryableFailure
	retries         map[string]RetryAvailability
	timedOut        []TimedOutAssignment
	terminal        []TerminalPolicyRun
	due             []Policy
	candidates      map[string][]CandidateProbe
	busyPolicies    map[string]bool
	oldestDue       *time.Time
	requeued        map[string]time.Time
	finished        map[string]string
	advanced        []policyAdvance
	created         []NewAssignment
	preempted       []string
	suspended       map[string]string
	catalogRequired bool
	seatMapTarget   *SeatMapBackfillTarget
}

func newMemoryCycle() *memoryCycle {
	return &memoryCycle{
		retries: make(map[string]RetryAvailability), candidates: make(map[string][]CandidateProbe),
		busyPolicies: make(map[string]bool), requeued: make(map[string]time.Time),
		finished: make(map[string]string), suspended: make(map[string]string),
	}
}

func (cycle *memoryCycle) failure(stage string) error {
	if cycle.failAt == stage {
		return errors.New("injected " + stage + " failure")
	}
	return nil
}

func (cycle *memoryCycle) MarkStaleProbes(context.Context, time.Time, time.Time) (int, error) {
	return cycle.staleProbes, cycle.failure("mark_stale")
}

func (cycle *memoryCycle) DeleteRetiredProbes(context.Context, time.Time) (int, error) {
	return cycle.deletedProbes, cycle.failure("delete_retired")
}

func (cycle *memoryCycle) DeleteExpiredClientEvents(context.Context, time.Time, int) (int64, error) {
	return 0, cycle.failure("delete_client_events")
}

func (cycle *memoryCycle) ExpiredLeases(context.Context, time.Time, int) ([]ExpiredLease, error) {
	return slices.Clone(cycle.expired), cycle.failure("expired_leases")
}

func (cycle *memoryCycle) ExpireLease(context.Context, ExpiredLease, time.Time) error {
	return cycle.failure("expire_lease")
}

func (cycle *memoryCycle) RetryableFailures(context.Context, int) ([]RetryableFailure, error) {
	return slices.Clone(cycle.retryable), cycle.failure("retryable_failures")
}

func (cycle *memoryCycle) RetryAvailability(_ context.Context, assignmentID string) (RetryAvailability, error) {
	return cycle.retries[assignmentID], cycle.failure("retry_availability")
}

func (cycle *memoryCycle) RequeueAssignment(
	_ context.Context,
	assignmentID string,
	notBefore time.Time,
	_ time.Time,
) error {
	if err := cycle.failure("requeue"); err != nil {
		return err
	}
	cycle.requeued[assignmentID] = notBefore
	return nil
}

func (cycle *memoryCycle) FinishAssignment(
	_ context.Context,
	assignmentID string,
	status string,
	_ string,
	_ time.Time,
) error {
	stage := "finish_expired"
	if assignmentID == "timed_out" {
		stage = "finish_timed_out"
	}
	if err := cycle.failure(stage); err != nil {
		return err
	}
	cycle.finished[assignmentID] = status
	return nil
}

func (cycle *memoryCycle) TimedOutAssignments(context.Context, time.Time, int) ([]TimedOutAssignment, error) {
	return slices.Clone(cycle.timedOut), cycle.failure("timed_out")
}

func (cycle *memoryCycle) TerminalPolicyRuns(context.Context, time.Time, int) ([]TerminalPolicyRun, error) {
	return slices.Clone(cycle.terminal), cycle.failure("terminal_runs")
}

func (cycle *memoryCycle) AdvancePolicy(
	_ context.Context,
	run TerminalPolicyRun,
	next *time.Time,
	_ time.Time,
) error {
	stage := "advance_terminal"
	if run.PolicyID == "missed" {
		stage = "advance_missed"
	}
	if err := cycle.failure(stage); err != nil {
		return err
	}
	cycle.advanced = append(cycle.advanced, policyAdvance{run: run, next: next})
	return nil
}

func (cycle *memoryCycle) DuePolicies(context.Context, time.Time, int) ([]Policy, error) {
	return slices.Clone(cycle.due), cycle.failure("due_policies")
}

func (cycle *memoryCycle) PreemptQueuedBaseline(_ context.Context, policyID string, _ time.Time) error {
	if err := cycle.failure("preempt_baseline"); err != nil {
		return err
	}
	cycle.preempted = append(cycle.preempted, policyID)
	cycle.created = slices.DeleteFunc(cycle.created, func(assignment NewAssignment) bool {
		return assignment.PolicyID == policyID && assignment.Lane == planning.LaneBaseline && assignment.Status == "queued"
	})
	return nil
}

func (cycle *memoryCycle) CatalogRefreshRequired(context.Context, time.Time) (bool, error) {
	return cycle.catalogRequired, cycle.failure("catalog_refresh")
}

func (cycle *memoryCycle) SeatMapBackfillTarget(context.Context, time.Time) (*SeatMapBackfillTarget, error) {
	return cycle.seatMapTarget, cycle.failure("seat_map_backfill")
}

func (cycle *memoryCycle) EligibleProbes(
	_ context.Context,
	policy Policy,
	_ time.Time,
	_ time.Time,
) ([]CandidateProbe, error) {
	return slices.Clone(cycle.candidates[policy.ID]), cycle.failure("eligible_probes")
}

func (cycle *memoryCycle) CreateAssignment(_ context.Context, assignment NewAssignment) error {
	if err := cycle.failure("create_assignment"); err != nil {
		return err
	}
	if cycle.busyPolicies[assignment.PolicyID] {
		return ErrTargetBusy
	}
	cycle.created = append(cycle.created, assignment)
	return nil
}

func (cycle *memoryCycle) SuspendPolicy(
	_ context.Context,
	policyID string,
	reason string,
	_ time.Time,
) error {
	if err := cycle.failure("suspend_policy"); err != nil {
		return err
	}
	cycle.suspended[policyID] = reason
	return nil
}

func (cycle *memoryCycle) OldestDuePolicy(context.Context, time.Time) (*time.Time, error) {
	return cycle.oldestDue, cycle.failure("oldest_due")
}
