package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/observation/planning"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
)

type adminOperations interface {
	ListAdminProbes(context.Context) ([]*adminpb.Probe, error)
	DeleteAdminProbe(context.Context, string) error
	AdminDataSummary(context.Context) (*adminpb.DataSummary, error)
	ListAdminObservationPolicies(context.Context) ([]*adminpb.ObservationPolicy, error)
	CreateAdminObservationPolicy(context.Context, *adminpb.ObservationPolicyInput) (*adminpb.ObservationPolicy, error)
	UpdateAdminObservationPolicy(context.Context, string, int64, *adminpb.ObservationPolicyInput) (*adminpb.ObservationPolicy, error)
	DeleteAdminObservationPolicy(context.Context, string, int64) error
	AdminObservationIntelligence(context.Context, *time.Location) (*adminpb.ObservationIntelligence, error)
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
	response.SetIntelligence(value)
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
	if input.GetTheaterId() == "" {
		return nil, central.InvalidRequest("theater id is required")
	}
	if err := validateObservationPolicyRanges(input); err != nil {
		return nil, err
	}
	return input, nil
}

func validateObservationPolicyRanges(input *adminpb.ObservationPolicyInput) error {
	maximum := planning.DefaultProductPolicy.MaximumHorizonDay
	if input.GetHorizonDays() < 1 || input.GetHorizonDays() > maximum {
		return central.InvalidRequest(fmt.Sprintf(
			"observation horizon must be between 1 and %d days", maximum,
		))
	}
	return nil
}
