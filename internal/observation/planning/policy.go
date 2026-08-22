package planning

import "time"

// Assignment priorities encode the product lanes in the durable claim queue.
// Fixed bands keep supporting catalog work from outranking booking demand.
const (
	PriorityBaselineObservation = 10
	PriorityCatalogRefresh      = 20
	PriorityRequestedSeatMap    = 30
	PriorityRecentChange        = 60
	PrioritySeatAvailability    = 85
	PriorityScheduleDiscovery   = 90
)

// ProductPolicy is the single Central-owned cadence for shared CGV observation.
// Admin and Client contracts intentionally expose no raw polling intervals.
type ProductPolicy struct {
	Priority          int32
	BaselineMinimum   time.Duration
	BaselineMaximum   time.Duration
	DemandMinimum     time.Duration
	DemandMaximum     time.Duration
	RecentMinimum     time.Duration
	RecentMaximum     time.Duration
	RecentDuration    time.Duration
	ExecutionWindow   time.Duration
	MaximumHorizonDay int32
}

// DefaultProductPolicy contains the initial product values fixed by the
// booking lifecycle specification. Tune these together from telemetry.
var DefaultProductPolicy = ProductPolicy{
	Priority:          50,
	BaselineMinimum:   5 * time.Minute,
	BaselineMaximum:   15 * time.Minute,
	DemandMinimum:     2 * time.Second,
	DemandMaximum:     5 * time.Second,
	RecentMinimum:     15 * time.Second,
	RecentMaximum:     30 * time.Second,
	RecentDuration:    30 * time.Minute,
	ExecutionWindow:   10 * time.Minute,
	MaximumHorizonDay: 14,
}
