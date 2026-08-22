package central

import (
	"context"
	"errors"
	"testing"
	"time"

	probedomain "github.com/cineko-org/central/internal/domain/probe"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/v3/gen/go/cineko/probe"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGeneratedProtoServiceResidualValidationBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 22, 5, 0, 0, 0, time.UTC)
	authorizer := &mutatingClientAuthorizer{
		authorization: RegistrationAuthorization{
			OwnerUserID: "user", DeviceID: "device", TicketID: "ticket", ExpiresAt: now.Add(time.Minute),
		},
	}
	service, err := NewService(newMemoryRepository(), Config{
		EnrollmentToken: "enroll", ClientAuthorizer: authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	service.random = deterministicRandom()
	authorizer.mutate = func(request *probepb.RegisterRequest) {
		request.SetCapabilities([]*observationpb.Capability{{}})
	}
	if _, err := service.RegisterProbe(t.Context(), validRegistration(), "127.0.0.1:1", "ticket"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutated registration capabilities = %v", err)
	}
	authorizer.mutate = nil
	authorizer.err = errInjectedClient
	if _, err := service.RegisterProbe(t.Context(), validRegistration(), "127.0.0.1:1", "ticket"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("client authorizer error = %v", err)
	}

	if _, err := service.AuthenticateProbe(t.Context(), "probe", " "); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("blank probe access token = %v", err)
	}
	if err := normalizeAndValidateHeartbeat(Probe{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil heartbeat = %v", err)
	}
	if err := normalizeAndValidateHeartbeat(
		Probe{Capabilities: []string{"unsupported"}}, &probepb.HeartbeatRequest{},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid registered heartbeat capabilities = %v", err)
	}
	capability := probedomain.CapabilityCGVScheduleCapture
	if err := validateAvailableCapabilities([]string{capability}, []string{capability, capability}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate available capabilities = %v", err)
	}

	for _, values := range [][2]string{{"", "token"}, {"assignment", ""}} {
		if _, err := service.HeartbeatAssignment(t.Context(), Probe{}, values[0], values[1]); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalid assignment heartbeat %q/%q = %v", values[0], values[1], err)
		}
	}

	invalidCatalogResult := validCatalogResult(now)
	invalidCatalogResult.GetCompleted().SetCatalog(&catalogpb.CatalogSnapshot{})
	if _, err := service.CommitResult(t.Context(), Probe{}, "assignment", "lease", invalidCatalogResult); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid completed catalog = %v", err)
	}
	noOutcome := &observationpb.AssignmentResult{}
	noOutcome.SetRunId("run")
	noOutcome.SetStartedAt(timestamppb.New(now))
	noOutcome.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
	if err := validateResult(noOutcome); !errors.Is(err, ErrInvalid) {
		t.Fatalf("result without outcome = %v", err)
	}
	blankFailure := &observationpb.Failed{}
	failedResult := &observationpb.AssignmentResult{}
	failedResult.SetRunId("run")
	failedResult.SetStartedAt(timestamppb.New(now))
	failedResult.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
	failedResult.SetFailed(blankFailure)
	if err := validateResult(failedResult); !errors.Is(err, ErrInvalid) {
		t.Fatalf("failed result without reason = %v", err)
	}

	invalidUTF8Failure := &observationpb.Failed{}
	invalidUTF8Reason := &collectionpb.FailureReason{}
	invalidUTF8Reason.SetInvalidResult(&collectionpb.InvalidResult{})
	invalidUTF8Failure.SetReason(invalidUTF8Reason)
	invalidUTF8Result := &observationpb.AssignmentResult{}
	invalidUTF8Result.SetRunId("run")
	invalidUTF8Result.SetStartedAt(timestamppb.New(now))
	invalidUTF8Result.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
	invalidUTF8Result.SetFailed(invalidUTF8Failure)
	if _, err := service.CommitResult(t.Context(), Probe{}, "assignment", "lease", invalidUTF8Result); err == nil {
		t.Fatal("invalid UTF-8 result was committed")
	}

	for _, showtime := range []*catalogpb.Showtime{nil, {}} {
		if showtimeIdentityComplete(showtime) {
			t.Fatalf("incomplete showtime identity accepted: %+v", showtime)
		}
	}
	if got := probeKindKey(nil); got != "" {
		t.Fatalf("nil probe kind = %q", got)
	}
	if got := probeKindKey(&probepb.ProbeKind{}); got != "" {
		t.Fatalf("empty probe kind = %q", got)
	}
	unhealthy := &probepb.Unhealthy{}
	unhealthy.SetReasonCode("offline")
	health := &probepb.ProbeHealth{}
	health.SetUnhealthy(unhealthy)
	if kind, reason := probeHealthKey(health); kind != "unhealthy" || reason != "offline" {
		t.Fatalf("unhealthy probe health = %q/%q", kind, reason)
	}
	if kind, reason := probeHealthKey(nil); kind != "" || reason != "" {
		t.Fatalf("nil probe health = %q/%q", kind, reason)
	}
}

type mutatingClientAuthorizer struct {
	authorization RegistrationAuthorization
	err           error
	mutate        func(*probepb.RegisterRequest)
}

func (authorizer *mutatingClientAuthorizer) Authorize(
	_ context.Context,
	request *probepb.RegisterRequest,
	_ string,
	_ time.Time,
) (RegistrationAuthorization, error) {
	if authorizer.mutate != nil {
		authorizer.mutate(request)
	}
	return authorizer.authorization, authorizer.err
}

var _ ClientRegistrationAuthorizer = (*mutatingClientAuthorizer)(nil)
