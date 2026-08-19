package central

import (
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

const (
	ProtocolVersion          = contracts.ProtocolVersion
	DefaultHeartbeatInterval = 30 * time.Second
	DefaultProbeHeartbeatTTL = 3 * DefaultHeartbeatInterval
	DefaultProbeTokenTTL     = 24 * time.Hour
	DefaultAssignmentLease   = 90 * time.Second
)

type Runtime = contracts.Runtime

type RegisterProbeRequest = contracts.RegisterProbeRequest

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
	Runtime               Runtime
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

type RegisterProbeResponse = contracts.RegisterProbeResponse

type ProbeHeartbeatRequest = contracts.ProbeHeartbeatRequest

type ProbeHeartbeatResponse = contracts.ProbeHeartbeatResponse

type Theater = contracts.Theater

type AssignmentTask = contracts.AssignmentTask

type Assignment struct {
	ID             string
	Task           AssignmentTask
	Status         string
	NotBefore      time.Time
	Deadline       time.Time
	ProbeID        string
	LeaseTokenHash [32]byte
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ClaimAssignmentResponse = contracts.ClaimAssignmentResponse

type AssignmentHeartbeatResponse = contracts.AssignmentHeartbeatResponse

type Movie = contracts.Movie

type Auditorium = contracts.Auditorium

type Showtime = contracts.Showtime

type Capture = contracts.Capture

type AssignmentResult = contracts.AssignmentResult

type ResultReceipt = contracts.ResultReceipt
