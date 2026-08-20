package central

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrInvalid             = errors.New("invalid request")
	ErrNoAssignment        = errors.New("no assignment")
	ErrLeaseExpired        = errors.New("assignment lease expired")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrConflict            = errors.New("conflict")
	ErrStaleRelease        = errors.New("stale release generation")
	ErrRateLimited         = errors.New("rate limited")
	ErrCorruptResource     = errors.New("corrupt client resource")
)

type ResultCommit struct {
	AssignmentID string
	ProbeID      string
	LeaseHash    [32]byte
	PayloadHash  string
	Payload      []byte
	Result       AssignmentResult
	CommittedAt  time.Time
}

type Repository interface {
	Ready(context.Context) error
	ConsumeProbeBootstrap(context.Context, string, time.Time, time.Time) error
	RegisterProbe(context.Context, Probe) (Probe, error)
	AuthenticateProbe(context.Context, string, [32]byte, time.Time) (Probe, error)
	HeartbeatProbe(context.Context, string, ProbeHeartbeatRequest, time.Time) (Probe, error)
	DisconnectProbe(context.Context, string, time.Time) error
	ClaimAssignment(context.Context, string, [32]byte, time.Time, time.Time, time.Time) (Assignment, error)
	HeartbeatAssignment(context.Context, string, string, [32]byte, time.Time, time.Time) error
	CommitResult(context.Context, ResultCommit) (ResultReceipt, error)
}

// AssignmentWaiter is an optional repository capability for event-driven
// probe claims. Implementations re-check durable claim eligibility before and
// after waiting so a PostgreSQL notification cannot lose a state transition.
type AssignmentWaiter interface {
	WaitForAssignment(context.Context, string, time.Time) error
}
