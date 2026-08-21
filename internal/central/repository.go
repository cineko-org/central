package central

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
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

// PublicError marks a message as safe to return across the HTTP boundary while
// retaining a stable sentinel for errors.Is. Internal errors must not use this type.
type PublicError struct {
	cause   error
	message string
}

func (err *PublicError) Error() string         { return fmt.Sprintf("%v: %s", err.cause, err.message) }
func (err *PublicError) Unwrap() error         { return err.cause }
func (err *PublicError) PublicMessage() string { return err.message }

func InvalidRequest(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "request is invalid"
	}
	return &PublicError{cause: ErrInvalid, message: message}
}

type ResultCommit struct {
	AssignmentID string
	ProbeID      string
	LeaseHash    [32]byte
	PayloadHash  string
	Payload      []byte
	Result       *observationpb.AssignmentResult
	CommittedAt  time.Time
}

type Repository interface {
	Ready(context.Context) error
	ConsumeProbeBootstrap(context.Context, string, time.Time, time.Time) error
	RegisterProbe(context.Context, Probe) (Probe, error)
	AuthenticateProbe(context.Context, string, [32]byte, time.Time) (Probe, error)
	HeartbeatProbe(context.Context, string, *probepb.HeartbeatRequest, time.Time) (Probe, error)
	DisconnectProbe(context.Context, string, time.Time) error
	ClaimAssignment(context.Context, string, [32]byte, time.Time, time.Time, time.Time) (Assignment, error)
	HeartbeatAssignment(context.Context, string, string, [32]byte, time.Time, time.Time) error
	CommitResult(context.Context, ResultCommit) (*observationpb.ResultReceipt, error)
}

// AssignmentWaiter is an optional repository capability for event-driven
// probe claims. Implementations re-check durable claim eligibility before and
// after waiting so a PostgreSQL notification cannot lose a state transition.
type AssignmentWaiter interface {
	WaitForAssignment(context.Context, string, time.Time) error
}
