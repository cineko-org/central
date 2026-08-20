package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
)

type AdminProbe struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	OwnerUserID     string     `json:"ownerUserId,omitempty"`
	NetworkID       string     `json:"networkId"`
	RuntimeVersion  string     `json:"runtimeVersion"`
	BrowserRevision string     `json:"browserRevision"`
	Platform        string     `json:"platform"`
	Arch            string     `json:"arch"`
	Status          string     `json:"status"`
	Draining        bool       `json:"draining"`
	AvailableSlots  int        `json:"availableSlots"`
	MaxConcurrency  int        `json:"maxConcurrency"`
	Health          string     `json:"health"`
	ReasonCode      string     `json:"reasonCode,omitempty"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type AdminDataSummary struct {
	Providers                 int64      `json:"providers"`
	Theaters                  int64      `json:"theaters"`
	Auditoriums               int64      `json:"auditoriums"`
	Movies                    int64      `json:"movies"`
	Showtimes                 int64      `json:"showtimes"`
	SeatMapVersions           int64      `json:"seatMapVersions"`
	ScheduleCaptures          int64      `json:"scheduleCaptures"`
	ShowtimeObservations      int64      `json:"showtimeObservations"`
	ObservationPolicies       int64      `json:"observationPolicies"`
	ActiveObservationPolicies int64      `json:"activeObservationPolicies"`
	QueuedAssignments         int64      `json:"queuedAssignments"`
	LeasedAssignments         int64      `json:"leasedAssignments"`
	CompletedAssignments      int64      `json:"completedAssignments"`
	FailedAssignments         int64      `json:"failedAssignments"`
	LatestScheduleObservedAt  *time.Time `json:"latestScheduleObservedAt,omitempty"`
}

type AdminObservationPolicyInput struct {
	TheaterID            string `json:"theaterId"`
	Enabled              bool   `json:"enabled"`
	HorizonDays          int    `json:"horizonDays"`
	Priority             int    `json:"priority"`
	BaselineMinSeconds   int    `json:"baselineMinSeconds"`
	BaselineMaxSeconds   int    `json:"baselineMaxSeconds"`
	DemandMinSeconds     int    `json:"demandMinSeconds"`
	DemandMaxSeconds     int    `json:"demandMaxSeconds"`
	BurstMinSeconds      int    `json:"burstMinSeconds"`
	BurstMaxSeconds      int    `json:"burstMaxSeconds"`
	BurstDurationSeconds int    `json:"burstDurationSeconds"`
	Locale               string `json:"locale"`
	TimeZone             string `json:"timeZone"`
	EgressPolicyID       string `json:"egressPolicyId"`
}

const (
	observationPolicyPriority           = 50
	observationBaselineMinSeconds       = 300
	observationBaselineMaxSeconds       = 900
	observationDemandStoredMinSeconds   = 30
	observationDemandStoredMaxSeconds   = 45
	observationBurstStoredMinSeconds    = 15
	observationBurstStoredMaxSeconds    = 30
	observationBurstDurationSeconds     = 1800
	observationPolicyMaximumHorizonDays = 14
)

type AdminObservationPolicy struct {
	ID       string          `json:"id"`
	Revision int64           `json:"revision"`
	Theater  central.Theater `json:"theater"`
	AdminObservationPolicyInput
	EffectiveMode       string     `json:"effectiveMode"`
	EffectivePriority   int        `json:"effectivePriority"`
	EffectiveMinSeconds int        `json:"effectiveMinSeconds"`
	EffectiveMaxSeconds int        `json:"effectiveMaxSeconds"`
	DemandActive        bool       `json:"demandActive"`
	BurstUntil          *time.Time `json:"burstUntil,omitempty"`
	NextRunAt           *time.Time `json:"nextRunAt,omitempty"`
	LastFinishedAt      *time.Time `json:"lastFinishedAt,omitempty"`
	LastOutcome         string     `json:"lastOutcome,omitempty"`
	LastErrorCode       string     `json:"lastErrorCode,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

type adminOperations interface {
	ListAdminProbes(context.Context) ([]AdminProbe, error)
	DeleteAdminProbe(context.Context, string) error
	AdminDataSummary(context.Context) (AdminDataSummary, error)
	ListAdminObservationPolicies(context.Context) ([]AdminObservationPolicy, error)
	CreateAdminObservationPolicy(context.Context, AdminObservationPolicyInput) (AdminObservationPolicy, error)
	UpdateAdminObservationPolicy(context.Context, string, int64, AdminObservationPolicyInput) (AdminObservationPolicy, error)
	DeleteAdminObservationPolicy(context.Context, string, int64) error
	AdminObservationIntelligence(context.Context, *time.Location) (domain.ScheduleIntelligence, error)
}

func WithAdminOperations(operations adminOperations) Option {
	return func(server *Server) { server.adminOperations = operations }
}

func (server *Server) adminProbes(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	if server.adminOperations == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_operations_unavailable", "admin operations are unavailable", true)
		return
	}
	probes, err := server.adminOperations.ListAdminProbes(request.Context())
	if err != nil {
		server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_probes_failed", "load probes failed", true)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"data": probes})
}

func (server *Server) deleteAdminProbe(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	probeID := strings.TrimSpace(request.PathValue("probeId"))
	if probeID == "" {
		server.writeError(writer, request, central.InvalidRequest("probe id is required"))
		return
	}
	if err := server.adminOperations.DeleteAdminProbe(request.Context(), probeID); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) adminData(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return
	}
	if server.adminOperations == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_operations_unavailable", "admin operations are unavailable", true)
		return
	}
	summary, err := server.adminOperations.AdminDataSummary(request.Context())
	if err != nil {
		server.writeAPIError(writer, request, http.StatusInternalServerError, "admin_data_failed", "load data summary failed", true)
		return
	}
	server.writeJSON(writer, http.StatusOK, summary)
}

func (server *Server) listAdminObservationPolicies(writer http.ResponseWriter, request *http.Request) {
	if !server.requireAdminOperations(writer, request) {
		return
	}
	policies, err := server.adminOperations.ListAdminObservationPolicies(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"data": policies})
}

func (server *Server) createAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	if expected, valid := expectedRevision(writer, request); !valid || expected != nil {
		if valid {
			writeInvalidRevision(writer, "policy creation requires If-None-Match: *")
		}
		return
	}
	input, ok := server.decodeObservationPolicyInput(writer, request)
	if !ok {
		return
	}
	policy, err := server.adminOperations.CreateAdminObservationPolicy(request.Context(), input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, policy)
}

func (server *Server) updateAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	revision, valid := expectedRevision(writer, request)
	if !valid || revision == nil {
		if valid {
			writeInvalidRevision(writer, "policy update requires If-Match")
		}
		return
	}
	input, ok := server.decodeObservationPolicyInput(writer, request)
	if !ok {
		return
	}
	policy, err := server.adminOperations.UpdateAdminObservationPolicy(
		request.Context(), request.PathValue("policyId"), *revision, input,
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, policy)
}

func (server *Server) deleteAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	revision, valid := expectedRevision(writer, request)
	if !valid || revision == nil {
		if valid {
			writeInvalidRevision(writer, "policy deletion requires If-Match")
		}
		return
	}
	if err := server.adminOperations.DeleteAdminObservationPolicy(
		request.Context(), request.PathValue("policyId"), *revision,
	); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) adminObservationIntelligence(writer http.ResponseWriter, request *http.Request) {
	if !server.requireAdminOperations(writer, request) {
		return
	}
	value, err := server.adminOperations.AdminObservationIntelligence(
		request.Context(), time.FixedZone("KST", 9*60*60),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, value)
}

func (server *Server) requireAdminOperations(writer http.ResponseWriter, request *http.Request) bool {
	if _, ok := server.authenticatedAdmin(writer, request); !ok {
		return false
	}
	if server.adminOperations != nil {
		return true
	}
	server.writeAPIError(writer, request, http.StatusServiceUnavailable, "admin_operations_unavailable", "admin operations are unavailable", true)
	return false
}

func (server *Server) decodeObservationPolicyInput(
	writer http.ResponseWriter,
	request *http.Request,
) (AdminObservationPolicyInput, bool) {
	var input AdminObservationPolicyInput
	if !server.decodeJSON(writer, request, &input) {
		return AdminObservationPolicyInput{}, false
	}
	normalized, err := normalizeObservationPolicyInput(input)
	if err != nil {
		server.writeError(writer, request, err)
		return AdminObservationPolicyInput{}, false
	}
	return normalized, true
}

func normalizeObservationPolicyInput(input AdminObservationPolicyInput) (AdminObservationPolicyInput, error) {
	input.TheaterID = strings.TrimSpace(input.TheaterID)
	input.Priority = observationPolicyPriority
	input.BaselineMinSeconds = observationBaselineMinSeconds
	input.BaselineMaxSeconds = observationBaselineMaxSeconds
	input.DemandMinSeconds = observationDemandStoredMinSeconds
	input.DemandMaxSeconds = observationDemandStoredMaxSeconds
	input.BurstMinSeconds = observationBurstStoredMinSeconds
	input.BurstMaxSeconds = observationBurstStoredMaxSeconds
	input.BurstDurationSeconds = observationBurstDurationSeconds
	input.Locale = "ko-KR"
	input.TimeZone = "Asia/Seoul"
	input.EgressPolicyID = "scan_default"
	if input.TheaterID == "" {
		return AdminObservationPolicyInput{}, central.InvalidRequest("theater id is required")
	}
	if err := validateObservationPolicyRanges(input); err != nil {
		return AdminObservationPolicyInput{}, err
	}
	return input, nil
}

func validateObservationPolicyRanges(input AdminObservationPolicyInput) error {
	if input.HorizonDays < 1 || input.HorizonDays > observationPolicyMaximumHorizonDays {
		return central.InvalidRequest(fmt.Sprintf(
			"observation horizon must be between 1 and %d days", observationPolicyMaximumHorizonDays,
		))
	}
	ranges := []struct {
		minimum int
		maximum int
		floor   int
	}{
		{input.BaselineMinSeconds, input.BaselineMaxSeconds, 30},
		{input.DemandMinSeconds, input.DemandMaxSeconds, 30},
		{input.BurstMinSeconds, input.BurstMaxSeconds, 15},
	}
	for _, interval := range ranges {
		if interval.minimum < interval.floor || interval.maximum <= interval.minimum {
			return central.InvalidRequest("observation intervals are invalid")
		}
	}
	if input.BurstDurationSeconds < 300 || input.BurstDurationSeconds > 21600 {
		return central.InvalidRequest("observation intervals are invalid")
	}
	if input.DemandMinSeconds > input.BaselineMinSeconds || input.DemandMaxSeconds > input.BaselineMaxSeconds ||
		input.BurstMinSeconds > input.DemandMinSeconds || input.BurstMaxSeconds > input.DemandMaxSeconds {
		return central.InvalidRequest("boosted intervals must be no slower than baseline")
	}
	return nil
}
