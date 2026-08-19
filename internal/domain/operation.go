package domain

import "time"

type ExternalOperationKind string

const (
	ExternalOperationCancellation ExternalOperationKind = "cancellation"
)

type ExternalOperationStatus string

const (
	ExternalOperationPrepared   ExternalOperationStatus = "prepared"
	ExternalOperationUnknown    ExternalOperationStatus = "unknown"
	ExternalOperationAttention  ExternalOperationStatus = "attention_required"
	ExternalOperationConfirmed  ExternalOperationStatus = "confirmed"
	ExternalOperationReconciled ExternalOperationStatus = "reconciled"
)

// ExternalOperation is the durable boundary around an irreversible browser
// action. A confirmed operation can always rebuild its local reservation state
// after a crash or a transient storage write failure.
type ExternalOperation struct {
	ID            string                  `json:"id"`
	UserID        string                  `json:"userId"`
	MonitorID     string                  `json:"monitorId,omitempty"`
	ReservationID string                  `json:"reservationId"`
	Kind          ExternalOperationKind   `json:"kind"`
	Status        ExternalOperationStatus `json:"status"`
	RefundAmount  string                  `json:"refundAmount,omitempty"`
	LastError     string                  `json:"lastError,omitempty"`
	CreatedAt     time.Time               `json:"createdAt"`
	UpdatedAt     time.Time               `json:"updatedAt"`
}

type EventTone string

const (
	EventInfo    EventTone = "info"
	EventSuccess EventTone = "success"
	EventWarning EventTone = "warning"
	EventError   EventTone = "error"
)

// AppEvent is durable notification history owned by the application rather
// than a particular renderer process.
type AppEvent struct {
	ID        string     `json:"id"`
	UserID    string     `json:"userId"`
	Kind      string     `json:"kind"`
	Tone      EventTone  `json:"tone"`
	Message   string     `json:"message"`
	CreatedAt time.Time  `json:"createdAt"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
}
