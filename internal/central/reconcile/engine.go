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
	"github.com/cineko-org/central/internal/telemetry"
	contracts "github.com/cineko-org/contracts/v3"
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

type Status struct {
	Running       bool      `json:"running"`
	Healthy       bool      `json:"healthy"`
	Leader        bool      `json:"leader"`
	LastAttemptAt time.Time `json:"lastAttemptAt,omitempty"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitempty"`
	LastErrorAt   time.Time `json:"lastErrorAt,omitempty"`
	LastErrorCode string    `json:"lastErrorCode,omitempty"`
	LastReport    Report    `json:"lastReport"`
}

type Engine struct {
	repository     Repository
	config         Config
	clock          func() time.Time
	randomDuration func(time.Duration, time.Duration) (time.Duration, error)
	newID          func() (string, error)

	mu            sync.RWMutex
	running       bool
	lastAttemptAt time.Time
	lastSuccessAt time.Time
	lastErrorAt   time.Time
	lastErrorCode string
	lastReport    Report
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

	engine.runAndRecord(ctx)
	ticker := time.NewTicker(engine.config.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			engine.runAndRecord(ctx)
		}
	}
}

func (engine *Engine) RunOnce(ctx context.Context) (Report, error) {
	now := engine.clock().UTC()
	report := Report{StartedAt: now}
	leader, err := engine.repository.RunLeaderCycle(ctx, func(cycle CycleRepository) error {
		return engine.reconcile(ctx, cycle, now, &report)
	})
	report.Leader = leader
	report.FinishedAt = engine.clock().UTC()
	return report, err
}

func (engine *Engine) Snapshot() Status {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	healthDeadline := 3 * engine.config.TickInterval
	if healthDeadline < 30*time.Second {
		healthDeadline = 30 * time.Second
	}
	now := engine.clock().UTC()
	healthy := engine.lastErrorCode == "" && !engine.lastSuccessAt.IsZero() &&
		now.Sub(engine.lastSuccessAt) <= healthDeadline
	return Status{
		Running: engine.running, Healthy: healthy, Leader: engine.lastReport.Leader,
		LastAttemptAt: engine.lastAttemptAt, LastSuccessAt: engine.lastSuccessAt,
		LastErrorAt: engine.lastErrorAt, LastErrorCode: engine.lastErrorCode, LastReport: engine.lastReport,
	}
}

func (engine *Engine) reconcile(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
) error {
	stale, err := cycle.MarkStaleProbes(ctx, now.Add(-engine.config.ProbeHeartbeatTTL), now)
	if err != nil {
		return fmt.Errorf("mark stale probes: %w", err)
	}
	report.StaleProbes = stale
	deleted, err := cycle.DeleteRetiredProbes(ctx, now.Add(-engine.config.OfflineRetention))
	if err != nil {
		return fmt.Errorf("delete retired probes: %w", err)
	}
	report.DeletedProbes = deleted
	deletedClientEvents, err := cycle.DeleteExpiredClientEvents(
		ctx, now.Add(-defaultClientEventRetention), engine.config.BatchSize,
	)
	if err != nil {
		return fmt.Errorf("delete expired Client events: %w", err)
	}
	report.DeletedClientEvents = deletedClientEvents
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
	if err := engine.scheduleDuePolicies(ctx, cycle, now, report); err != nil {
		return err
	}
	oldestDue, err := cycle.OldestDuePolicy(ctx, now)
	if err != nil {
		return fmt.Errorf("read reconcile queue lag: %w", err)
	}
	if oldestDue != nil {
		report.OldestDueAgeSeconds = max(0, int64(now.Sub(*oldestDue)/time.Second))
	}
	return nil
}

func (engine *Engine) scheduleSeatMapBackfill(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
) error {
	target, err := cycle.SeatMapBackfillTarget(ctx, now)
	if err != nil {
		return fmt.Errorf("inspect seat-map backfill: %w", err)
	}
	if target == nil {
		return nil
	}
	policy := Policy{TaskKind: contracts.CapabilityCGVSeatMapCapture}
	candidates, err := cycle.EligibleProbes(ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL))
	if err != nil {
		return fmt.Errorf("list seat-map backfill probes: %w", err)
	}
	if len(candidates) == 0 {
		report.SeatMapBackfillWaiting = true
		return nil
	}
	id, err := engine.newID()
	if err != nil {
		return fmt.Errorf("generate seat-map backfill assignment id: %w", err)
	}
	priority := 70
	if target.Requested {
		priority = 95
	}
	assignment := NewAssignment{
		ID: id, Priority: priority, Status: "queued", NotBefore: now,
		Deadline: now.Add(seatMapBackfillWindow), CreatedAt: now,
		Candidates: slices.Clone(candidates), Task: target.Task,
	}
	if err := cycle.CreateAssignment(ctx, assignment); err != nil {
		if errors.Is(err, ErrTargetBusy) {
			report.SeatMapBackfillWaiting = true
			return nil
		}
		return fmt.Errorf("create seat-map backfill assignment: %w", err)
	}
	report.CreatedAssignments++
	report.SeatMapBackfillCreated = true
	return nil
}

func (engine *Engine) scheduleCatalogRefresh(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
) error {
	required, err := cycle.CatalogRefreshRequired(ctx, now.Add(-catalogRefreshRetryDelay))
	if err != nil {
		return fmt.Errorf("inspect catalog refresh: %w", err)
	}
	if !required {
		return nil
	}
	policy := Policy{TaskKind: contracts.CapabilityCGVCatalogCapture}
	candidates, err := cycle.EligibleProbes(ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL))
	if err != nil {
		return fmt.Errorf("list catalog refresh probes: %w", err)
	}
	if len(candidates) == 0 {
		report.CatalogRefreshWaiting = true
		return nil
	}
	id, err := engine.newID()
	if err != nil {
		return fmt.Errorf("generate catalog refresh assignment id: %w", err)
	}
	sourceKey := "__catalog__"
	assignment := NewAssignment{
		ID: id, Priority: 100, Status: "queued", NotBefore: now,
		Deadline: now.Add(catalogRefreshWindow), CreatedAt: now, Candidates: slices.Clone(candidates),
		Task: central.AssignmentTask{
			Kind: contracts.CapabilityCGVCatalogCapture,
			Theater: central.Theater{
				ID:         contracts.CatalogID(contracts.ProviderCGV, "theater", sourceKey),
				ProviderID: contracts.ProviderCGV, SourceKey: sourceKey,
				Region: "system", Name: "CGV catalog",
			},
			Locale: "ko-KR", TimeZone: "Asia/Seoul", EgressPolicyID: "scan_default",
		},
	}
	if err := cycle.CreateAssignment(ctx, assignment); err != nil {
		if errors.Is(err, ErrTargetBusy) {
			report.CatalogRefreshWaiting = true
			return nil
		}
		return fmt.Errorf("create catalog refresh assignment: %w", err)
	}
	report.CreatedAssignments++
	report.CatalogRefreshCreated = true
	return nil
}

func (engine *Engine) reconcileExpiredLeases(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
) error {
	leases, err := cycle.ExpiredLeases(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list expired leases: %w", err)
	}
	for _, lease := range leases {
		if err := cycle.ExpireLease(ctx, lease, now); err != nil {
			return fmt.Errorf("expire assignment %s: %w", lease.AssignmentID, err)
		}
		report.ExpiredLeases++
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
				report.RequeuedAssignments++
				continue
			}
		}
		if err := cycle.FinishAssignment(ctx, lease.AssignmentID, OutcomeFailed, "eligible_probes_exhausted", now); err != nil {
			return fmt.Errorf("fail assignment %s: %w", lease.AssignmentID, err)
		}
		report.FailedAssignments++
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
	report *Report,
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
				report.RequeuedAssignments++
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
		report.FailedAssignments++
	}
	return nil
}

func (engine *Engine) reconcileTimedOutAssignments(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
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
			report.MissedAssignments++
		} else {
			report.FailedAssignments++
		}
	}
	return nil
}

func (engine *Engine) advanceTerminalPolicies(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
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
		report.AdvancedPolicies++
	}
	return nil
}

func (engine *Engine) scheduleDuePolicies(
	ctx context.Context,
	cycle CycleRepository,
	now time.Time,
	report *Report,
) error {
	policies, err := cycle.DuePolicies(ctx, now, engine.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list due policies: %w", err)
	}
	for _, policy := range policies {
		targetDates, err := policyTargetDates(policy, now)
		if err != nil {
			if suspendErr := cycle.SuspendPolicy(ctx, policy.ID, "invalid_policy", now); suspendErr != nil {
				return fmt.Errorf("suspend invalid policy %s: %w", policy.ID, suspendErr)
			}
			report.SuspendedPolicies++
			engine.config.Logger.ErrorContext(ctx, "Observation policy suspended",
				"domain", "observation", "event", "observation.policy.suspended", "outcome", "failed",
				"policy_id", policy.ID, "reason", "invalid_policy", "error_type", telemetry.ErrorType(err))
			continue
		}
		candidates, err := cycle.EligibleProbes(
			ctx, policy, now, now.Add(-engine.config.ProbeHeartbeatTTL),
		)
		if err != nil {
			return fmt.Errorf("list policy %s eligible probes: %w", policy.ID, err)
		}
		assignment, err := engine.newAssignment(policy, targetDates, candidates, now)
		if err != nil {
			return err
		}
		if err := cycle.CreateAssignment(ctx, assignment); err != nil {
			if errors.Is(err, ErrTargetBusy) {
				report.DeferredPolicies++
				continue
			}
			return fmt.Errorf("create policy %s assignment: %w", policy.ID, err)
		}
		report.CreatedAssignments++
		if assignment.Status == OutcomeMissed {
			report.MissedAssignments++
			if err := engine.advanceMissedPolicy(ctx, cycle, policy, assignment, now); err != nil {
				return err
			}
			report.AdvancedPolicies++
		}
	}
	return nil
}

func (engine *Engine) newAssignment(
	policy Policy,
	targetDates []string,
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
	return NewAssignment{
		ID: id, PolicyID: policy.ID, Priority: policy.Priority, Status: status,
		Task: central.AssignmentTask{
			Kind: policy.TaskKind, Theater: policy.Theater, TargetDates: targetDates,
			Locale: policy.Locale, TimeZone: policy.TimeZone, EgressPolicyID: policy.EgressPolicyID,
		},
		NotBefore: now, Deadline: now.Add(policy.ExecutionWindow), FinishedAt: finishedAt,
		ReasonCode: reason, CreatedAt: now, Candidates: slices.Clone(candidates),
	}, nil
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

func policyTargetDates(policy Policy, now time.Time) ([]string, error) {
	location, err := validatePolicyRuntime(policy)
	if err != nil {
		return nil, err
	}
	switch policy.TargetDateMode {
	case "explicit":
		return explicitTargetDates(policy.TargetDates)
	case "rolling":
		return rollingTargetDates(policy.HorizonDays, now, location)
	default:
		return nil, fmt.Errorf("unsupported target date mode %q", policy.TargetDateMode)
	}
}

func validatePolicyRuntime(policy Policy) (*time.Location, error) {
	if strings.TrimSpace(policy.TaskKind) == "" || strings.TrimSpace(policy.Theater.ID) == "" ||
		strings.TrimSpace(policy.Theater.ProviderID) == "" || strings.TrimSpace(policy.Theater.SourceKey) == "" ||
		strings.TrimSpace(policy.Locale) == "" || policy.MinimumInterval <= 0 ||
		policy.MaximumInterval < policy.MinimumInterval || policy.ExecutionWindow <= 0 {
		return nil, errors.New("policy runtime configuration is incomplete")
	}
	location, err := time.LoadLocation(strings.TrimSpace(policy.TimeZone))
	if err != nil {
		return nil, fmt.Errorf("load policy time zone: %w", err)
	}
	return location, nil
}

func explicitTargetDates(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("explicit policy has no target dates")
	}
	seen := make(map[string]struct{}, len(values))
	for _, date := range values {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return nil, fmt.Errorf("invalid target date %q", date)
		}
		if _, duplicate := seen[date]; duplicate {
			return nil, fmt.Errorf("duplicate target date %q", date)
		}
		seen[date] = struct{}{}
	}
	return slices.Clone(values), nil
}

func rollingTargetDates(horizonDays int, now time.Time, location *time.Location) ([]string, error) {
	if horizonDays < 1 || horizonDays > 90 {
		return nil, errors.New("rolling policy horizon is outside 1..90")
	}
	start := now.In(location)
	dates := make([]string, horizonDays)
	for index := range dates {
		dates[index] = start.AddDate(0, 0, index).Format(time.DateOnly)
	}
	return dates, nil
}

func (engine *Engine) runAndRecord(ctx context.Context) {
	report, err := engine.RunOnce(ctx)
	now := engine.clock().UTC()
	engine.mu.Lock()
	previousLeader := engine.lastReport.Leader
	engine.lastAttemptAt = now
	engine.lastReport = report
	if err == nil {
		engine.lastSuccessAt = now
		engine.lastErrorCode = ""
	} else {
		engine.lastErrorAt = now
		engine.lastErrorCode = "cycle_failed"
	}
	engine.mu.Unlock()
	duration := report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	if err != nil {
		engine.config.Logger.ErrorContext(ctx, "Observation reconciliation failed",
			"domain", "observation", "event", "observation.reconcile.completed", "outcome", "failed",
			"reason", "cycle_failed", "error_type", telemetry.ErrorType(err), "duration_ms", duration,
			"leader", report.Leader)
	} else if report.Leader && reportHasActivity(report) {
		engine.config.Logger.InfoContext(ctx, "Observation reconciliation completed",
			"domain", "observation", "event", "observation.reconcile.completed", "outcome", "succeeded",
			"duration_ms", duration, "created_assignments", report.CreatedAssignments,
			"requeued_assignments", report.RequeuedAssignments, "failed_assignments", report.FailedAssignments,
			"missed_assignments", report.MissedAssignments, "stale_probes", report.StaleProbes,
			"oldest_due_age_seconds", report.OldestDueAgeSeconds)
	}
	if previousLeader != report.Leader {
		engine.config.Logger.InfoContext(ctx, "Observation leadership changed",
			"domain", "observation", "event", "observation.leadership.changed", "outcome", "succeeded",
			"leader", report.Leader)
	}
}

func reportHasActivity(report Report) bool {
	return report.StaleProbes+report.DeletedProbes+report.ExpiredLeases+report.RequeuedAssignments+
		report.FailedAssignments+report.MissedAssignments+report.AdvancedPolicies+
		report.CreatedAssignments+report.DeferredPolicies+report.SuspendedPolicies > 0 ||
		report.DeletedClientEvents > 0 || report.CatalogRefreshCreated || report.CatalogRefreshWaiting ||
		report.SeatMapBackfillCreated || report.SeatMapBackfillWaiting
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
