package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/cineko-org/central/internal/observation/planning"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
)

const (
	OutcomeCompleted = "completed"
	OutcomePartial   = "partial"
	OutcomeFailed    = "failed"
	OutcomeMissed    = "missed"
)

var ErrTargetBusy = errors.New("observation target already has an active assignment")

type Policy struct {
	ID                       string
	Enabled                  bool
	TaskKind                 string
	Theater                  *catalogpb.Theater
	TargetDateMode           string
	TargetDates              []string
	HorizonDays              int
	Locale                   string
	TimeZone                 string
	EgressPolicyID           string
	Priority                 int
	MinimumInterval          time.Duration
	MaximumInterval          time.Duration
	ExecutionWindow          time.Duration
	NextRunAt                time.Time
	LastFinishedAt           time.Time
	LastOutcome              string
	HotTargets               []planning.MonitorTarget
	HotTargetFingerprint     string
	LastHotTargetFingerprint string
	LastHotFinishedAt        time.Time
	LastHotTargetDates       []string
	LastBaselineFinishedAt   time.Time
	LastBaselineTargetDate   string
	BaselineMaximumInterval  time.Duration
}

type CandidateProbe struct {
	ID        string
	NetworkID string
}

type ExpiredLease struct {
	AssignmentID string
	ProbeID      string
	Deadline     time.Time
}

type RetryAvailability struct {
	Remaining int
}

type RetryableFailure struct {
	AssignmentID string
	Deadline     time.Time
}

type TimedOutAssignment struct {
	AssignmentID string
	AttemptCount int
}

type TerminalPolicyRun struct {
	PolicyID        string
	Enabled         bool
	FinishedAt      time.Time
	Outcome         string
	MinimumInterval time.Duration
	MaximumInterval time.Duration
}

type NewAssignment struct {
	ID                   string
	PolicyID             string
	Task                 *observationpb.AssignmentTask
	Lane                 planning.Lane
	HotTargetFingerprint string
	Priority             int
	Status               string
	NotBefore            time.Time
	Deadline             time.Time
	FinishedAt           time.Time
	ReasonCode           string
	CreatedAt            time.Time
	Candidates           []CandidateProbe
}

type SeatMapBackfillTarget struct {
	Task      *observationpb.AssignmentTask
	Requested bool
}

type Report struct {
	Leader                 bool      `json:"leader"`
	StartedAt              time.Time `json:"startedAt"`
	FinishedAt             time.Time `json:"finishedAt"`
	StaleProbes            int       `json:"staleProbes"`
	DeletedProbes          int       `json:"deletedProbes"`
	DeletedClientEvents    int64     `json:"deletedClientEvents"`
	ExpiredLeases          int       `json:"expiredLeases"`
	RequeuedAssignments    int       `json:"requeuedAssignments"`
	FailedAssignments      int       `json:"failedAssignments"`
	MissedAssignments      int       `json:"missedAssignments"`
	AdvancedPolicies       int       `json:"advancedPolicies"`
	CreatedAssignments     int       `json:"createdAssignments"`
	DeferredPolicies       int       `json:"deferredPolicies"`
	SuspendedPolicies      int       `json:"suspendedPolicies"`
	CatalogRefreshCreated  bool      `json:"catalogRefreshCreated"`
	CatalogRefreshWaiting  bool      `json:"catalogRefreshWaiting"`
	SeatMapBackfillCreated bool      `json:"seatMapBackfillCreated"`
	SeatMapBackfillWaiting bool      `json:"seatMapBackfillWaiting"`
	OldestDueAgeSeconds    int64     `json:"oldestDueAgeSeconds"`
}

type CycleRepository interface {
	MarkStaleProbes(context.Context, time.Time, time.Time) (int, error)
	DeleteRetiredProbes(context.Context, time.Time) (int, error)
	DeleteExpiredClientEvents(context.Context, time.Time, int) (int64, error)
	ExpiredLeases(context.Context, time.Time, int) ([]ExpiredLease, error)
	ExpireLease(context.Context, ExpiredLease, time.Time) error
	RetryableFailures(context.Context, int) ([]RetryableFailure, error)
	RetryAvailability(context.Context, string) (RetryAvailability, error)
	RequeueAssignment(context.Context, string, time.Time, time.Time) error
	FinishAssignment(context.Context, string, string, string, time.Time) error
	TimedOutAssignments(context.Context, time.Time, int) ([]TimedOutAssignment, error)
	TerminalPolicyRuns(context.Context, time.Time, int) ([]TerminalPolicyRun, error)
	AdvancePolicy(context.Context, TerminalPolicyRun, *time.Time, time.Time) error
	CatalogRefreshRequired(context.Context, time.Time) (bool, error)
	SeatMapBackfillTarget(context.Context, time.Time) (*SeatMapBackfillTarget, error)
	DuePolicies(context.Context, time.Time, int) ([]Policy, error)
	PreemptQueuedBaseline(context.Context, string, time.Time) error
	EligibleProbes(context.Context, Policy, time.Time, time.Time) ([]CandidateProbe, error)
	CreateAssignment(context.Context, NewAssignment) error
	SuspendPolicy(context.Context, string, string, time.Time) error
	OldestDuePolicy(context.Context, time.Time) (*time.Time, error)
}

type Repository interface {
	RunLeaderCycle(context.Context, func(CycleRepository) error) (bool, error)
}
