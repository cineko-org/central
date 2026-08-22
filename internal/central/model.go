package central

import (
	"fmt"
	"time"

	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
)

const (
	DefaultHeartbeatInterval = 30 * time.Second
	DefaultProbeHeartbeatTTL = 3 * DefaultHeartbeatInterval
	DefaultProbeTokenTTL     = 24 * time.Hour
	DefaultAssignmentLease   = 90 * time.Second
)

type Probe struct {
	ID                    string
	InstallationID        string
	OwnerUserID           string
	DeviceID              string
	Kind                  string
	NetworkID             string
	NetworkHint           string
	Capabilities          []string
	AvailableCapabilities []string
	MaxConcurrency        int
	Runtime               *commonpb.Runtime
	TokenHash             [32]byte
	TokenExpiresAt        time.Time
	Status                string
	Draining              bool
	AvailableSlots        int
	Health                string
	ReasonCode            string
	LastHeartbeatAt       time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type RegistrationAuthorization struct {
	OwnerUserID string
	DeviceID    string
	TicketID    string
	ExpiresAt   time.Time
}

type EgressPolicyID string

const EgressPolicyScanDefault EgressPolicyID = "scan_default"

// RequireEgressPolicy rejects assignment routes Central does not own.
func RequireEgressPolicy(value EgressPolicyID) error {
	if value != EgressPolicyScanDefault {
		return fmt.Errorf("%w: unsupported egress policy", ErrInvalid)
	}
	return nil
}

type Assignment struct {
	ID             string
	Task           *observationpb.AssignmentTask
	Status         string
	NotBefore      time.Time
	Deadline       time.Time
	ProbeID        string
	LeaseTokenHash [32]byte
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
