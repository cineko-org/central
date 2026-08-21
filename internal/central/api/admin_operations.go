package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
	"github.com/cineko-org/central/internal/support/numeric"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

type adminOperations interface {
	ListAdminProbes(context.Context) ([]*adminpb.Probe, error)
	DeleteAdminProbe(context.Context, string) error
	AdminDataSummary(context.Context) (*adminpb.DataSummary, error)
	ListAdminObservationPolicies(context.Context) ([]*adminpb.ObservationPolicy, error)
	CreateAdminObservationPolicy(context.Context, *adminpb.ObservationPolicyInput) (*adminpb.ObservationPolicy, error)
	UpdateAdminObservationPolicy(context.Context, string, int64, *adminpb.ObservationPolicyInput) (*adminpb.ObservationPolicy, error)
	DeleteAdminObservationPolicy(context.Context, string, int64) error
	AdminObservationIntelligence(context.Context, *time.Location) (domain.ScheduleIntelligence, error)
}

func WithAdminOperations(operations adminOperations) Option {
	return func(server *Server) { server.adminOperations = operations }
}

func (server *Server) adminProbes(writer http.ResponseWriter, request *http.Request) {
	if !server.requireAdminOperations(writer, request) {
		return
	}
	writeAdminProtoCall(server, writer, request, "admin_probes_failed", "load probes failed", server.loadAdminProbes)
}

func (server *Server) loadAdminProbes(ctx context.Context) (*adminpb.ListProbesResponse, error) {
	probes, err := server.adminOperations.ListAdminProbes(ctx)
	response := &adminpb.ListProbesResponse{}
	response.SetProbes(probes)
	return response, err
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
	if !server.requireAdminOperations(writer, request) {
		return
	}
	writeAdminProtoCall(server, writer, request, "admin_data_failed", "load data summary failed", server.loadAdminData)
}

func (server *Server) loadAdminData(ctx context.Context) (*adminpb.GetDataSummaryResponse, error) {
	summary, err := server.adminOperations.AdminDataSummary(ctx)
	response := &adminpb.GetDataSummaryResponse{}
	response.SetSummary(summary)
	return response, err
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
	response := &adminpb.ListObservationPoliciesResponse{}
	response.SetPolicies(policies)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) createAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	if expected, valid := server.expectedRevision(writer, request); !valid || expected != nil {
		if valid {
			server.writeInvalidRevision(writer, request, "policy creation requires If-None-Match: *")
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
	response := &adminpb.CreateObservationPolicyResponse{}
	response.SetPolicy(policy)
	server.writeProtoJSON(writer, http.StatusCreated, response)
}

func (server *Server) updateAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	revision, valid := server.expectedRevision(writer, request)
	if !valid || revision == nil {
		if valid {
			server.writeInvalidRevision(writer, request, "policy update requires If-Match")
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
	response := &adminpb.UpdateObservationPolicyResponse{}
	response.SetPolicy(policy)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) deleteAdminObservationPolicy(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) || !server.requireAdminOperations(writer, request) {
		return
	}
	revision, valid := server.expectedRevision(writer, request)
	if !valid || revision == nil {
		if valid {
			server.writeInvalidRevision(writer, request, "policy deletion requires If-Match")
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
	response := &adminpb.GetObservationIntelligenceResponse{}
	response.SetIntelligence(observationIntelligenceProto(value))
	server.writeProtoJSON(writer, http.StatusOK, response)
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
) (*adminpb.ObservationPolicyInput, bool) {
	input := &adminpb.ObservationPolicyInput{}
	if !server.decodeProtoJSON(writer, request, input) {
		return nil, false
	}
	normalized, err := normalizeObservationPolicyInput(input)
	if err != nil {
		server.writeError(writer, request, err)
		return nil, false
	}
	return normalized, true
}

func normalizeObservationPolicyInput(input *adminpb.ObservationPolicyInput) (*adminpb.ObservationPolicyInput, error) {
	input.SetTheaterId(strings.TrimSpace(input.GetTheaterId()))
	input.SetPriority(observationPolicyPriority)
	input.SetBaselineMinSeconds(observationBaselineMinSeconds)
	input.SetBaselineMaxSeconds(observationBaselineMaxSeconds)
	input.SetDemandMinSeconds(observationDemandStoredMinSeconds)
	input.SetDemandMaxSeconds(observationDemandStoredMaxSeconds)
	input.SetBurstMinSeconds(observationBurstStoredMinSeconds)
	input.SetBurstMaxSeconds(observationBurstStoredMaxSeconds)
	input.SetBurstDurationSeconds(observationBurstDurationSeconds)
	input.SetLocale("ko-KR")
	input.SetTimeZone("Asia/Seoul")
	input.SetEgressPolicyId(string(central.EgressPolicyScanDefault))
	if input.GetTheaterId() == "" {
		return nil, central.InvalidRequest("theater id is required")
	}
	if err := validateObservationPolicyRanges(input); err != nil {
		return nil, err
	}
	return input, nil
}

func validateObservationPolicyRanges(input *adminpb.ObservationPolicyInput) error {
	if input.GetHorizonDays() < 1 || input.GetHorizonDays() > observationPolicyMaximumHorizonDays {
		return central.InvalidRequest(fmt.Sprintf(
			"observation horizon must be between 1 and %d days", observationPolicyMaximumHorizonDays,
		))
	}
	ranges := []struct {
		minimum int
		maximum int
		floor   int
	}{
		{int(input.GetBaselineMinSeconds()), int(input.GetBaselineMaxSeconds()), 30},
		{int(input.GetDemandMinSeconds()), int(input.GetDemandMaxSeconds()), 30},
		{int(input.GetBurstMinSeconds()), int(input.GetBurstMaxSeconds()), 15},
	}
	for _, interval := range ranges {
		if interval.minimum < interval.floor || interval.maximum <= interval.minimum {
			return central.InvalidRequest("observation intervals are invalid")
		}
	}
	if input.GetBurstDurationSeconds() < 300 || input.GetBurstDurationSeconds() > 21600 {
		return central.InvalidRequest("observation intervals are invalid")
	}
	if input.GetDemandMinSeconds() > input.GetBaselineMinSeconds() || input.GetDemandMaxSeconds() > input.GetBaselineMaxSeconds() ||
		input.GetBurstMinSeconds() > input.GetDemandMinSeconds() || input.GetBurstMaxSeconds() > input.GetDemandMaxSeconds() {
		return central.InvalidRequest("boosted intervals must be no slower than baseline")
	}
	return nil
}

func observationIntelligenceProto(value domain.ScheduleIntelligence) *adminpb.ObservationIntelligence {
	result := &adminpb.ObservationIntelligence{}
	result.SetSnapshotCount(numeric.ClampInt32(value.SnapshotCount))
	result.SetShowtimeObservations(numeric.ClampInt32(value.ShowtimeObservations))
	if !value.LastObservedAt.IsZero() {
		result.SetLastObservedAt(timestamppb.New(value.LastObservedAt))
	}
	openingPatterns := make([]*adminpb.OpeningPattern, 0, len(value.OpeningPatterns))
	for _, value := range value.OpeningPatterns {
		pattern := &adminpb.OpeningPattern{}
		pattern.SetTheaterId(value.TheaterID)
		pattern.SetTheaterName(value.TheaterName)
		pattern.SetAuditoriumId(value.AuditoriumID)
		pattern.SetAuditoriumName(value.AuditoriumName)
		pattern.SetMovie(value.Movie)
		pattern.SetScreenTypes(value.ScreenTypes)
		pattern.SetSampleSize(numeric.ClampInt32(value.SampleSize))
		pattern.SetTypicalOpenTime(value.TypicalOpenTime)
		pattern.SetTypicalLeadHours(numeric.ClampInt32(value.TypicalLeadHours))
		pattern.SetTypicalPrecisionMinutes(numeric.ClampInt32(value.TypicalPrecisionMins))
		pattern.SetLastObservedAt(timestamppb.New(value.LastObservedAt))
		openingPatterns = append(openingPatterns, pattern)
	}
	result.SetOpeningPatterns(openingPatterns)
	demandPatterns := make([]*adminpb.DemandPattern, 0, len(value.DemandPatterns))
	for _, value := range value.DemandPatterns {
		pattern := &adminpb.DemandPattern{}
		pattern.SetTheaterId(value.TheaterID)
		pattern.SetTheaterName(value.TheaterName)
		pattern.SetAuditoriumId(value.AuditoriumID)
		pattern.SetAuditoriumName(value.AuditoriumName)
		pattern.SetMovie(value.Movie)
		pattern.SetOccurrenceCount(numeric.ClampInt32(value.OccurrenceCount))
		pattern.SetFirstHourSampleSize(numeric.ClampInt32(value.FirstHourSampleSize))
		pattern.SetTypicalFirstHourSellThrough(numeric.ClampInt32(value.TypicalFirstHourSellThrough))
		pattern.SetHalfSoldSampleSize(numeric.ClampInt32(value.HalfSoldSampleSize))
		pattern.SetTypicalHalfSoldMinutes(numeric.ClampInt32(value.TypicalHalfSoldMinutes))
		pattern.SetSoldOutSampleSize(numeric.ClampInt32(value.SoldOutSampleSize))
		pattern.SetTypicalSoldOutMinutes(numeric.ClampInt32(value.TypicalSoldOutMinutes))
		pattern.SetLastObservedAt(timestamppb.New(value.LastObservedAt))
		demandPatterns = append(demandPatterns, pattern)
	}
	result.SetDemandPatterns(demandPatterns)
	return result
}
