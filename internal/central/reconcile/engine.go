package reconcile

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	"github.com/cineko-org/central/internal/observation/planning"
	"github.com/cineko-org/central/internal/support/numeric"
	"github.com/cineko-org/central/internal/telemetry"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultTickInterval         = 5 * time.Second
	defaultOfflineRetention     = 30 * 24 * time.Hour
	defaultClientEventRetention = 180 * 24 * time.Hour
	defaultRetryMinimum         = time.Second
	defaultRetryMaximum         = 5 * time.Second
	defaultBatchSize            = 100
	catalogRefreshRetryDelay    = time.Minute
	catalogRefreshWindow        = 10 * time.Minute
	seatMapBackfillWindow       = 10 * time.Minute
	seatAvailabilityWindow      = 30 * time.Second
)

var ErrAlreadyRunning = errors.New("reconciler is already running")

type Config struct {
	TickInterval      time.Duration
	ProbeHeartbeatTTL time.Duration
	OfflineRetention  time.Duration
	RetryMinimum      time.Duration
	RetryMaximum      time.Duration
	BatchSize         int
	Logger            *slog.Logger
}

type Engine struct {
	repository     Repository
	config         Config
	clock          func() time.Time
	randomDuration func(time.Duration, time.Duration) (time.Duration, error)
	newID          func() (string, error)
	newTimer       func(time.Duration) schedulerTimer

	mu            sync.RWMutex
	running       bool
	lastAttemptAt time.Time
	lastSuccessAt time.Time
	lastErrorAt   time.Time
	lastErrorCode string
	lastReport    *adminpb.ReconcileReport
}

func New(repository Repository, config Config) (*Engine, error) {
	if repository == nil {
		return nil, errors.New("reconcile repository is required")
	}
	applyConfigDefaults(&config)
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Engine{
		repository: repository, config: config, clock: time.Now,
		randomDuration: secureRandomDuration, newID: newAssignmentID,
		newTimer: newSchedulerTimer,
	}, nil
}

func applyConfigDefaults(config *Config) {
	if config.TickInterval == 0 {
		config.TickInterval = defaultTickInterval
	}
	if config.ProbeHeartbeatTTL == 0 {
		config.ProbeHeartbeatTTL = 3 * central.DefaultHeartbeatInterval
	}
	if config.OfflineRetention == 0 {
		config.OfflineRetention = defaultOfflineRetention
	}
	if config.RetryMinimum == 0 {
		config.RetryMinimum = defaultRetryMinimum
	}
	if config.RetryMaximum == 0 {
		config.RetryMaximum = defaultRetryMaximum
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
}

func validateConfig(config Config) error {
	if config.TickInterval <= 0 || config.ProbeHeartbeatTTL <= 0 || config.OfflineRetention <= 0 {
		return errors.New("reconcile intervals must be positive")
	}
	if config.RetryMinimum <= 0 || config.RetryMaximum < config.RetryMinimum {
		return errors.New("reconcile retry range is invalid")
	}
	if config.BatchSize < 1 || config.BatchSize > 1_000 {
		return errors.New("reconcile batch size must be between 1 and 1000")
	}
	return nil
}

func (engine *Engine) Run(ctx context.Context) error {
	if !engine.setRunning(true) {
		return ErrAlreadyRunning
	}
	defer engine.setRunning(false)

	wakeupContext, cancelWakeups := context.WithCancel(ctx)
	defer cancelWakeups()
	wakeups := engine.startWakeupListener(wakeupContext)
	nextDeadline := engine.runAndRecord(ctx)
	for {
		delay := nextReconcileDelay(engine.clock().UTC(), nextDeadline, engine.config.TickInterval)
		timer := engine.newTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-wakeups:
			timer.Stop()
			nextDeadline = engine.runAndRecord(ctx)
		case <-timer.C():
			timer.Stop()
			nextDeadline = engine.runAndRecord(ctx)
		}
	}
}

func (engine *Engine) RunOnce(ctx context.Context) (*adminpb.ReconcileReport, error) {
	now := engine.clock().UTC()
	report := &adminpb.ReconcileReport{}
	report.SetStartedAt(timestamppb.New(now))
	leader, err := engine.repository.RunLeaderCycle(ctx, func(cycle CycleRepository) error {
		return engine.reconcile(ctx, cycle, now, report)
	})
	report.SetLeader(leader)
	report.SetFinishedAt(timestamppb.New(engine.clock().UTC()))
	return report, err
}

func (engine *Engine) Snapshot() *adminpb.ReconcileStatus {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	healthDeadline := 3 * engine.config.TickInterval
	if healthDeadline < 30*time.Second {
		healthDeadline = 30 * time.Second
	}
	now := engine.clock().UTC()
	healthy := engine.lastErrorCode == "" && !engine.lastSuccessAt.IsZero() &&
		now.Sub(engine.lastSuccessAt) <= healthDeadline
	status := &adminpb.ReconcileStatus{}
	status.SetRunning(engine.running)
	status.SetHealthy(healthy)
	status.SetLeader(engine.lastReport.GetLeader())
	if !engine.lastAttemptAt.IsZero() {
		status.SetLastAttemptAt(timestamppb.New(engine.lastAttemptAt))
	}
	if !engine.lastSuccessAt.IsZero() {
		status.SetLastSuccessAt(timestamppb.New(engine.lastSuccessAt))
	}
	if !engine.lastErrorAt.IsZero() {
		status.SetLastErrorAt(timestamppb.New(engine.lastErrorAt))
	}
	status.SetLastErrorCode(engine.lastErrorCode)
	if engine.lastReport != nil {
		status.SetLastReport(proto.CloneOf(engine.lastReport))
	}
	return status
}

func (engine *Engine) reconcile(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	stale, err := cycle.MarkStaleProbes(ctx, now.Add(-engine.config.ProbeHeartbeatTTL), now)
	if err != nil {
		return fmt.Errorf("mark stale probes: %w", err)
	}
	report.SetStaleProbes(numeric.ClampInt32(stale))
	deleted, err := cycle.DeleteRetiredProbes(ctx, now.Add(-engine.config.OfflineRetention))
	if err != nil {
		return fmt.Errorf("delete retired probes: %w", err)
	}
	report.SetDeletedProbes(numeric.ClampInt32(deleted))
	deletedClientEvents, err := cycle.DeleteExpiredClientEvents(
		ctx, now.Add(-defaultClientEventRetention), engine.config.BatchSize,
	)
	if err != nil {
		return fmt.Errorf("delete expired Client events: %w", err)
	}
	report.SetDeletedClientEvents(deletedClientEvents)
	if err := engine.reconcileExpiredLeases(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.reconcileRetryableFailures(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.reconcileTimedOutAssignments(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.advanceTerminalPolicies(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.scheduleCatalogRefresh(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.scheduleSeatMapBackfill(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.scheduleSeatAvailability(ctx, cycle, now, report); err != nil {
		return err
	}
	if err := engine.scheduleDuePolicies(ctx, cycle, now, report); err != nil {
		return err
	}
	oldestDue, err := cycle.OldestDuePolicy(ctx, now)
	if err != nil {
		return fmt.Errorf("read reconcile queue lag: %w", err)
	}
	if oldestDue != nil {
		report.SetOldestDueAgeSeconds(max(0, int64(now.Sub(*oldestDue)/time.Second)))
	}
	return nil
}

func (engine *Engine) scheduleSeatAvailability(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	target, err := cycle.SeatAvailabilityTarget(ctx, now)
	if err != nil {
		return fmt.Errorf("inspect exact-showtime availability: %w", err)
	}
	if target == nil {
		return nil
	}
	policy := Policy{TaskKind: probedomain.CapabilityCGVSeatAvailabilityCapture}
	candidates, err := cycle.EligibleProbes(ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL))
	if err != nil {
		return fmt.Errorf("list exact-showtime availability probes: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}
	id, err := engine.newID()
	if err != nil {
		return fmt.Errorf("generate exact-showtime availability assignment id: %w", err)
	}
	assignment := NewAssignment{
		ID: id, Priority: planning.PrioritySeatAvailability, Status: "queued", NotBefore: now,
		Deadline: now.Add(seatAvailabilityWindow), CreatedAt: now,
		Candidates: slices.Clone(candidates), Task: target.Task,
	}
	assignment.Task.SetEgress(managedScanEgress())
	if err := cycle.CreateAssignment(ctx, assignment); err != nil {
		if errors.Is(err, ErrTargetBusy) {
			return nil
		}
		return fmt.Errorf("create exact-showtime availability assignment: %w", err)
	}
	report.SetCreatedAssignments(report.GetCreatedAssignments() + 1)
	return nil
}

func (engine *Engine) scheduleSeatMapBackfill(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	target, err := cycle.SeatMapBackfillTarget(ctx, now)
	if err != nil {
		return fmt.Errorf("inspect seat-map backfill: %w", err)
	}
	if target == nil {
		return nil
	}
	policy := Policy{TaskKind: probedomain.CapabilityCGVSeatMapCapture}
	candidates, err := cycle.EligibleProbes(ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL))
	if err != nil {
		return fmt.Errorf("list seat-map backfill probes: %w", err)
	}
	if len(candidates) == 0 {
		report.SetSeatMapBackfillWaiting(true)
		return nil
	}
	id, err := engine.newID()
	if err != nil {
		return fmt.Errorf("generate seat-map backfill assignment id: %w", err)
	}
	priority := planning.PriorityBaselineObservation
	if target.Requested {
		priority = planning.PriorityRequestedSeatMap
	}
	assignment := NewAssignment{
		ID: id, Priority: priority, Status: "queued", NotBefore: now,
		Deadline: now.Add(seatMapBackfillWindow), CreatedAt: now,
		Candidates: slices.Clone(candidates), Task: target.Task,
	}
	assignment.Task.SetEgress(managedScanEgress())
	if err := cycle.CreateAssignment(ctx, assignment); err != nil {
		if errors.Is(err, ErrTargetBusy) {
			report.SetSeatMapBackfillWaiting(true)
			return nil
		}
		return fmt.Errorf("create seat-map backfill assignment: %w", err)
	}
	report.SetCreatedAssignments(report.GetCreatedAssignments() + 1)
	report.SetSeatMapBackfillCreated(true)
	return nil
}

func (engine *Engine) scheduleCatalogRefresh(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	required, err := cycle.CatalogRefreshRequired(ctx, now.Add(-catalogRefreshRetryDelay))
	if err != nil {
		return fmt.Errorf("inspect catalog refresh: %w", err)
	}
	if !required {
		return nil
	}
	policy := Policy{TaskKind: probedomain.CapabilityCGVCatalogCapture}
	candidates, err := cycle.EligibleProbes(ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL))
	if err != nil {
		return fmt.Errorf("list catalog refresh probes: %w", err)
	}
	if len(candidates) == 0 {
		report.SetCatalogRefreshWaiting(true)
		return nil
	}
	id, err := engine.newID()
	if err != nil {
		return fmt.Errorf("generate catalog refresh assignment id: %w", err)
	}
	assignment := NewAssignment{
		ID: id, Priority: planning.PriorityCatalogRefresh, Status: "queued", NotBefore: now,
		Deadline: now.Add(catalogRefreshWindow), CreatedAt: now, Candidates: slices.Clone(candidates),
		Task: catalogAssignmentTask(contracts.ProviderCGV, "ko-KR", "Asia/Seoul"),
	}
	if err := cycle.CreateAssignment(ctx, assignment); err != nil {
		if errors.Is(err, ErrTargetBusy) {
			report.SetCatalogRefreshWaiting(true)
			return nil
		}
		return fmt.Errorf("create catalog refresh assignment: %w", err)
	}
	report.SetCreatedAssignments(report.GetCreatedAssignments() + 1)
	report.SetCatalogRefreshCreated(true)
	return nil
}

func (engine *Engine) reconcileExpiredLeases(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	leases, err := cycle.ExpiredLeases(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list expired leases: %w", err)
	}
	for _, lease := range leases {
		if err := cycle.ExpireLease(ctx, lease, now); err != nil {
			return fmt.Errorf("expire assignment %s: %w", lease.AssignmentID, err)
		}
		report.SetExpiredLeases(report.GetExpiredLeases() + 1)
		availability, err := cycle.RetryAvailability(ctx, lease.AssignmentID)
		if err != nil {
			return fmt.Errorf("inspect assignment %s retries: %w", lease.AssignmentID, err)
		}
		if availability.Remaining > 0 && lease.Deadline.After(now) {
			requeued, err := engine.requeueBeforeDeadline(ctx, cycle, lease.AssignmentID, lease.Deadline, now)
			if err != nil {
				return err
			}
			if requeued {
				report.SetRequeuedAssignments(report.GetRequeuedAssignments() + 1)
				continue
			}
		}
		if err := cycle.FinishAssignment(ctx, lease.AssignmentID, OutcomeFailed, "eligible_probes_exhausted", now); err != nil {
			return fmt.Errorf("fail assignment %s: %w", lease.AssignmentID, err)
		}
		report.SetFailedAssignments(report.GetFailedAssignments() + 1)
	}
	return nil
}

func (engine *Engine) requeueBeforeDeadline(
	ctx context.Context,
	cycle CycleRepository,
	assignmentID string,
	deadline time.Time,
	now time.Time,
) (bool, error) {
	backoff, err := engine.randomDuration(engine.config.RetryMinimum, engine.config.RetryMaximum)
	if err != nil {
		return false, fmt.Errorf("choose assignment retry backoff: %w", err)
	}
	notBefore := now.Add(backoff)
	if !notBefore.Before(deadline) {
		return false, nil
	}
	if err := cycle.RequeueAssignment(ctx, assignmentID, notBefore, now); err != nil {
		return false, fmt.Errorf("requeue assignment %s: %w", assignmentID, err)
	}
	return true, nil
}

func (engine *Engine) reconcileRetryableFailures(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	failures, err := cycle.RetryableFailures(ctx, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list retryable assignment failures: %w", err)
	}
	for _, failure := range failures {
		availability, err := cycle.RetryAvailability(ctx, failure.AssignmentID)
		if err != nil {
			return fmt.Errorf("inspect assignment %s retries: %w", failure.AssignmentID, err)
		}
		if availability.Remaining > 0 && failure.Deadline.After(now) {
			requeued, err := engine.requeueBeforeDeadline(
				ctx, cycle, failure.AssignmentID, failure.Deadline, now,
			)
			if err != nil {
				return err
			}
			if requeued {
				report.SetRequeuedAssignments(report.GetRequeuedAssignments() + 1)
				continue
			}
		}
		reason := "eligible_probes_exhausted"
		if availability.Remaining > 0 {
			reason = "retry_deadline_exceeded"
		}
		if err := cycle.FinishAssignment(
			ctx, failure.AssignmentID, OutcomeFailed, reason, now,
		); err != nil {
			return fmt.Errorf("fail assignment %s: %w", failure.AssignmentID, err)
		}
		report.SetFailedAssignments(report.GetFailedAssignments() + 1)
	}
	return nil
}

func (engine *Engine) reconcileTimedOutAssignments(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	assignments, err := cycle.TimedOutAssignments(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list timed out assignments: %w", err)
	}
	for _, assignment := range assignments {
		outcome, reason := OutcomeMissed, "not_claimed_before_deadline"
		if assignment.AttemptCount > 0 {
			outcome, reason = OutcomeFailed, "retry_deadline_exceeded"
		}
		if err := cycle.FinishAssignment(ctx, assignment.AssignmentID, outcome, reason, now); err != nil {
			return fmt.Errorf("finish timed out assignment %s: %w", assignment.AssignmentID, err)
		}
		if outcome == OutcomeMissed {
			report.SetMissedAssignments(report.GetMissedAssignments() + 1)
		} else {
			report.SetFailedAssignments(report.GetFailedAssignments() + 1)
		}
	}
	return nil
}

func (engine *Engine) advanceTerminalPolicies(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	runs, err := cycle.TerminalPolicyRuns(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list terminal policy runs: %w", err)
	}
	for _, run := range runs {
		var nextRunAt *time.Time
		if run.Enabled {
			interval, err := engine.randomDuration(run.MinimumInterval, run.MaximumInterval)
			if err != nil {
				return fmt.Errorf("choose policy %s interval: %w", run.PolicyID, err)
			}
			base := run.FinishedAt
			if base.Before(now) {
				base = now
			}
			next := base.Add(interval)
			nextRunAt = &next
		}
		if err := cycle.AdvancePolicy(ctx, run, nextRunAt, now); err != nil {
			return fmt.Errorf("advance policy %s: %w", run.PolicyID, err)
		}
		report.SetAdvancedPolicies(report.GetAdvancedPolicies() + 1)
	}
	return nil
}

func (engine *Engine) scheduleDuePolicies(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *adminpb.ReconcileReport,
) error {
	policies, err := cycle.DuePolicies(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list due policies: %w", err)
	}
	for _, policy := range policies {
		plan, err := policyPlan(policy, now)
		if err != nil {
			if suspendErr := cycle.SuspendPolicy(ctx, policy.ID, "invalid_policy", now); suspendErr != nil {
				return fmt.Errorf("suspend invalid policy %s: %w", policy.ID, suspendErr)
			}
			report.SetSuspendedPolicies(report.GetSuspendedPolicies() + 1)
			engine.config.Logger.ErrorContext(ctx, "Observation policy suspended",
				"domain", "observation", "event", "observation.policy.suspended", "outcome", "failed",
				"policy_id", policy.ID, "reason", "invalid_policy", "error_type", telemetry.ErrorType(err))
			continue
		}
		if len(plan.TargetDates) == 0 {
			continue
		}
		if plan.Lane == planning.LaneHot {
			if err := cycle.PreemptQueuedBaseline(ctx, policy.ID, now); err != nil {
				return fmt.Errorf("preempt baseline for policy %s: %w", policy.ID, err)
			}
		}
		candidates, err := cycle.EligibleProbes(
			ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL),
		)
		if err != nil {
			return fmt.Errorf("list policy %s eligible probes: %w", policy.ID, err)
		}
		assignment, err := engine.newAssignment(policy, plan, candidates, now)
		if err != nil {
			return err
		}
		if err := cycle.CreateAssignment(ctx, assignment); err != nil {
			if errors.Is(err, ErrTargetBusy) {
				report.SetDeferredPolicies(report.GetDeferredPolicies() + 1)
				continue
			}
			return fmt.Errorf("create policy %s assignment: %w", policy.ID, err)
		}
		report.SetCreatedAssignments(report.GetCreatedAssignments() + 1)
		if assignment.Status == OutcomeMissed {
			report.SetMissedAssignments(report.GetMissedAssignments() + 1)
			if err := engine.advanceMissedPolicy(ctx, cycle, policy, assignment, now); err != nil {
				return err
			}
			report.SetAdvancedPolicies(report.GetAdvancedPolicies() + 1)
		}
	}
	return nil
}

func (engine *Engine) newAssignment(
	policy Policy,
	plan planning.Result,
	candidates []CandidateProbe,
	now time.Time,
) (NewAssignment, error) {
	id, err := engine.newID()
	if err != nil {
		return NewAssignment{}, fmt.Errorf("generate assignment id: %w", err)
	}
	status, reason, finishedAt := "queued", "", time.Time{}
	if len(candidates) == 0 {
		status, reason, finishedAt = OutcomeMissed, "no_eligible_probe", now
	}
	targetDates, err := normalizeAssignmentTargetDates(plan.TargetDates)
	if err != nil {
		return NewAssignment{}, fmt.Errorf("normalize assignment target dates: %w", err)
	}
	return NewAssignment{
		ID: id, PolicyID: policy.ID, Priority: policy.Priority, Status: status,
		Lane: plan.Lane, HotTargetFingerprint: plan.HotTargetFingerprint,
		Task:      scheduleAssignmentTask(policy.Theater, targetDates, policy.Locale, policy.TimeZone),
		NotBefore: now, Deadline: now.Add(policy.ExecutionWindow), FinishedAt: finishedAt,
		ReasonCode: reason, CreatedAt: now, Candidates: slices.Clone(candidates),
	}, nil
}

func managedScanEgress() *commonpb.EgressPolicy {
	policy := &commonpb.EgressPolicy{}
	policy.SetManagedScan(&commonpb.ManagedScanEgress{})
	return policy
}

func scheduleAssignmentTask(
	theater *catalogpb.Theater,
	targetDates []string,
	locale string,
	timeZone string,
) *observationpb.AssignmentTask {
	return observationAssignmentTask(func(task *observationpb.AssignmentTask) {
		schedule := &observationpb.ScheduleTask{}
		schedule.SetTheater(theater)
		schedule.SetTargetDates(protoLocalDates(targetDates))
		schedule.SetLocale(locale)
		schedule.SetTimeZone(timeZone)
		task.SetSchedule(schedule)
	})
}

func catalogAssignmentTask(
	providerID string,
	locale string,
	timeZone string,
) *observationpb.AssignmentTask {
	return observationAssignmentTask(func(task *observationpb.AssignmentTask) {
		catalog := &observationpb.CatalogTask{}
		catalog.SetProviderId(providerID)
		catalog.SetLocale(locale)
		catalog.SetTimeZone(timeZone)
		task.SetCatalog(catalog)
	})
}

func observationAssignmentTask(setPayload func(*observationpb.AssignmentTask)) *observationpb.AssignmentTask {
	task := &observationpb.AssignmentTask{}
	setPayload(task)
	task.SetEgress(managedScanEgress())
	return task
}

func normalizeAssignmentTargetDates(values []string) ([]string, error) {
	result := slices.Clone(values)
	seen := make(map[string]struct{}, len(result))
	for _, value := range result {
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			return nil, fmt.Errorf("assignment target date is invalid: %s", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate assignment target date %s", value)
		}
		seen[value] = struct{}{}
	}
	slices.Sort(result)
	return result, nil
}

func protoLocalDates(values []string) []*commonpb.LocalDate {
	parsedDates := make([]time.Time, 0, len(values))
	for _, value := range values {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			continue
		}
		parsedDates = append(parsedDates, parsed)
	}
	slices.SortFunc(parsedDates, func(left, right time.Time) int {
		if left.Before(right) {
			return -1
		}
		if left.After(right) {
			return 1
		}
		return 0
	})
	dates := make([]*commonpb.LocalDate, 0, len(parsedDates))
	for _, parsed := range parsedDates {
		date := &commonpb.LocalDate{}
		date.SetYear(numeric.ClampInt32(parsed.Year()))
		date.SetMonth(numeric.ClampInt32(int(parsed.Month())))
		date.SetDay(numeric.ClampInt32(parsed.Day()))
		dates = append(dates, date)
	}
	return dates
}

func (engine *Engine) advanceMissedPolicy(
	ctx context.Context,
	cycle CycleRepository,
	policy Policy,
	assignment NewAssignment,
	now time.Time,
) error {
	interval, err := engine.randomDuration(policy.MinimumInterval, policy.MaximumInterval)
	if err != nil {
		return fmt.Errorf("choose policy %s interval: %w", policy.ID, err)
	}
	next := now.Add(interval)
	run := TerminalPolicyRun{
		PolicyID: policy.ID, Enabled: policy.Enabled, FinishedAt: assignment.FinishedAt,
		Outcome: OutcomeMissed, MinimumInterval: policy.MinimumInterval, MaximumInterval: policy.MaximumInterval,
	}
	if err := cycle.AdvancePolicy(ctx, run, &next, now); err != nil {
		return fmt.Errorf("advance missed policy %s: %w", policy.ID, err)
	}
	return nil
}

func policyPlan(policy Policy, now time.Time) (planning.Result, error) {
	location, err := validatePolicyRuntime(policy)
	if err != nil {
		return planning.Result{}, err
	}
	return planning.Build(planning.Input{
		Now: now, Location: location, TargetDateMode: policy.TargetDateMode,
		ExplicitTargetDates: policy.TargetDates, HorizonDays: policy.HorizonDays,
		HotTargets: policy.HotTargets, NextRunAt: policy.NextRunAt,
		HotTargetFingerprint:     policy.HotTargetFingerprint,
		LastHotFinishedAt:        policy.LastHotFinishedAt,
		LastHotTargetDates:       policy.LastHotTargetDates,
		LastHotTargetFingerprint: policy.LastHotTargetFingerprint,
		HotMinimumInterval:       policy.MinimumInterval,
		LastBaselineFinishedAt:   policy.LastBaselineFinishedAt,
		BaselineMaximumInterval:  policy.BaselineMaximumInterval,
		LastBaselineTargetDate:   policy.LastBaselineTargetDate,
	})
}

func validatePolicyRuntime(policy Policy) (*time.Location, error) {
	if strings.TrimSpace(policy.TaskKind) == "" || policy.Theater == nil || strings.TrimSpace(policy.Theater.GetId()) == "" ||
		strings.TrimSpace(policy.Theater.GetProviderId()) == "" || func() string { key, _ := contracts.TheaterSourceKey(policy.Theater); return key }() == "" ||
		strings.TrimSpace(policy.Locale) == "" || policy.MinimumInterval <= 0 ||
		policy.MaximumInterval < policy.MinimumInterval ||
		policy.ExecutionWindow <= 0 {
		return nil, errors.New("policy runtime configuration is incomplete")
	}
	if err := central.RequireEgressPolicy(central.EgressPolicyID(policy.EgressPolicyID)); err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(strings.TrimSpace(policy.TimeZone))
	if err != nil {
		return nil, fmt.Errorf("load policy time zone: %w", err)
	}
	return location, nil
}

func (engine *Engine) runAndRecord(ctx context.Context) *time.Time {
	report, err := engine.RunOnce(ctx)
	now := engine.clock().UTC()
	engine.mu.Lock()
	previousLeader := engine.lastReport.GetLeader()
	engine.lastAttemptAt = now
	engine.lastReport = proto.CloneOf(report)
	if err == nil {
		engine.lastSuccessAt = now
		engine.lastErrorCode = ""
	} else {
		engine.lastErrorAt = now
		engine.lastErrorCode = "cycle_failed"
	}
	engine.mu.Unlock()
	duration := report.GetFinishedAt().AsTime().Sub(report.GetStartedAt().AsTime()).Milliseconds()
	if err != nil {
		engine.config.Logger.ErrorContext(ctx, "Observation reconciliation failed",
			"domain", "observation", "event", "observation.reconcile.completed", "outcome", "failed",
			"reason", "cycle_failed", "error_type", telemetry.ErrorType(err), "duration_ms", duration,
			"leader", report.GetLeader())
	} else if report.GetLeader() && reportHasActivity(report) {
		engine.config.Logger.InfoContext(ctx, "Observation reconciliation completed",
			"domain", "observation", "event", "observation.reconcile.completed", "outcome", "succeeded",
			"duration_ms", duration, "created_assignments", report.GetCreatedAssignments(),
			"requeued_assignments", report.GetRequeuedAssignments(), "failed_assignments", report.GetFailedAssignments(),
			"missed_assignments", report.GetMissedAssignments(), "stale_probes", report.GetStaleProbes(),
			"oldest_due_age_seconds", report.GetOldestDueAgeSeconds())
	}
	if previousLeader != report.GetLeader() {
		engine.config.Logger.InfoContext(ctx, "Observation leadership changed",
			"domain", "observation", "event", "observation.leadership.changed", "outcome", "succeeded",
			"leader", report.GetLeader())
	}
	if err != nil {
		return nil
	}
	deadlineRepository, ok := engine.repository.(ReconcileDeadlineRepository)
	if !ok {
		return nil
	}
	deadline, deadlineErr := deadlineRepository.NextReconcileDeadline(ctx, now)
	if deadlineErr != nil {
		engine.config.Logger.WarnContext(ctx, "Observation reconcile deadline unavailable",
			"domain", "observation", "event", "observation.reconcile.deadline", "outcome", "failed",
			"error_type", telemetry.ErrorType(deadlineErr))
		return nil
	}
	return deadline
}

func (engine *Engine) startWakeupListener(ctx context.Context) <-chan struct{} {
	waiter, ok := engine.repository.(ReconcileWakeupRepository)
	if !ok {
		return nil
	}
	wakeups := make(chan struct{}, 1)
	go func() {
		for {
			err := waiter.WaitForReconcileWakeup(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				engine.config.Logger.WarnContext(ctx, "Observation reconcile wakeup unavailable",
					"domain", "observation", "event", "observation.reconcile.wakeup", "outcome", "failed",
					"error_type", telemetry.ErrorType(err))
				retryDelay := engine.config.TickInterval
				if retryDelay > time.Second {
					retryDelay = time.Second
				}
				retryTimer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					if !retryTimer.Stop() {
						select {
						case <-retryTimer.C:
						default:
						}
					}
					return
				case <-retryTimer.C:
				}
				continue
			}
			select {
			case wakeups <- struct{}{}:
			default:
			}
		}
	}()
	return wakeups
}

// schedulerTimer is deliberately smaller than time.Timer so the run loop can
// be tested without sleeping. Production uses time.NewTimer; tests can provide
// a deterministic channel and observe Stop on cancellation.
type schedulerTimer struct {
	channel <-chan time.Time
	stop    func() bool
}

func (timer schedulerTimer) C() <-chan time.Time { return timer.channel }

func (timer schedulerTimer) Stop() bool {
	if timer.stop == nil {
		return false
	}
	return timer.stop()
}

func newSchedulerTimer(delay time.Duration) schedulerTimer {
	timer := time.NewTimer(delay)
	return schedulerTimer{channel: timer.C, stop: timer.Stop}
}

func nextReconcileDelay(now time.Time, deadline *time.Time, maintenance time.Duration) time.Duration {
	if deadline == nil {
		return maintenance
	}
	delay := deadline.Sub(now)
	if delay <= 0 || delay >= maintenance {
		return maintenance
	}
	return delay
}

func reportHasActivity(report *adminpb.ReconcileReport) bool {
	return report.GetStaleProbes()+report.GetDeletedProbes()+report.GetExpiredLeases()+report.GetRequeuedAssignments()+
		report.GetFailedAssignments()+report.GetMissedAssignments()+report.GetAdvancedPolicies()+
		report.GetCreatedAssignments()+report.GetDeferredPolicies()+report.GetSuspendedPolicies() > 0 ||
		report.GetDeletedClientEvents() > 0 || report.GetCatalogRefreshCreated() || report.GetCatalogRefreshWaiting() ||
		report.GetSeatMapBackfillCreated() || report.GetSeatMapBackfillWaiting()
}

func (engine *Engine) setRunning(value bool) bool {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if value && engine.running {
		return false
	}
	engine.running = value
	return true
}

func secureRandomDuration(minimum, maximum time.Duration) (time.Duration, error) {
	return randomDuration(rand.Reader, minimum, maximum)
}

func randomDuration(reader io.Reader, minimum, maximum time.Duration) (time.Duration, error) {
	if maximum < minimum {
		return 0, errors.New("random duration range is invalid")
	}
	span := new(big.Int).SetInt64(int64(maximum - minimum + 1))
	offset, err := rand.Int(reader, span)
	if err != nil {
		return 0, fmt.Errorf("read secure randomness: %w", err)
	}
	return minimum + time.Duration(offset.Int64()), nil
}

func newAssignmentID() (string, error) {
	return assignmentID(rand.Reader)
}

func assignmentID(reader io.Reader) (string, error) {
	buffer := make([]byte, 18)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("read assignment id randomness: %w", err)
	}
	return "assignment_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}
