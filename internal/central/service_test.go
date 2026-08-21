package central

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	probedomain "github.com/cineko-org/central/internal/domain/probe"
	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestProbeLifecycleAndResultIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service, err := NewService(repository, Config{EnrollmentToken: "enroll", AssignmentLease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	service.random = deterministicRandom()
	request := containerRegistration()

	registered, err := service.RegisterProbe(t.Context(), request, "203.0.113.4:443", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if registered.GetProbeId() == "" || registered.GetAccessToken() == "" || registered.GetNetworkId() == "" {
		t.Fatalf("registration = %+v", registered)
	}
	probe, err := service.AuthenticateProbe(t.Context(), registered.GetProbeId(), registered.GetAccessToken())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.HeartbeatProbe(t.Context(), probe, healthyHeartbeat(2)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-capacity heartbeat error = %v", err)
	}
	if _, err := service.HeartbeatProbe(t.Context(), probe, healthyHeartbeat(1)); err != nil {
		t.Fatal(err)
	}

	repository.assignments["assignment_01"] = Assignment{
		ID: "assignment_01", Status: "queued", NotBefore: now.Add(-time.Minute), Deadline: now.Add(time.Hour),
		Task: validAssignmentTask(now),
	}
	claim, err := service.ClaimAssignment(t.Context(), probe)
	if err != nil {
		t.Fatal(err)
	}
	lease := claim.GetAssignment()
	if lease.GetAssignmentId() != "assignment_01" || lease.GetLeaseToken() == "" {
		t.Fatalf("claim = %+v", claim)
	}
	if _, err := service.HeartbeatAssignment(t.Context(), probe, lease.GetAssignmentId(), lease.GetLeaseToken()); err != nil {
		t.Fatal(err)
	}
	result := validResult(now)
	receipt, err := service.CommitResult(t.Context(), probe, lease.GetAssignmentId(), lease.GetLeaseToken(), result)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.CommitResult(t.Context(), probe, lease.GetAssignmentId(), lease.GetLeaseToken(), result)
	if err != nil || repeated != receipt {
		t.Fatalf("repeated receipt = %+v, %v; want %+v", repeated, err, receipt)
	}
	conflict := proto.CloneOf(result)
	conflict.GetCompleted().GetCaptures()[0].SetComplete(false)
	if _, err := service.CommitResult(t.Context(), probe, lease.GetAssignmentId(), lease.GetLeaseToken(), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting result error = %v", err)
	}

	reregistered, err := service.RegisterProbe(t.Context(), request, "203.0.113.4:443", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if reregistered.GetProbeId() != registered.GetProbeId() || reregistered.GetAccessToken() == registered.GetAccessToken() {
		t.Fatalf("re-registration = %+v; first = %+v", reregistered, registered)
	}
	if _, err := service.AuthenticateProbe(t.Context(), registered.GetProbeId(), registered.GetAccessToken()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token authentication error = %v", err)
	}
}

func TestServiceRejectsInvalidRegistrationAndResult(t *testing.T) {
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	invalid := validRegistration()
	invalid.GetRuntime().SetComponentVersion("invalid")
	if _, err := service.RegisterProbe(t.Context(), invalid, "127.0.0.1:1", "enroll"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid registration error = %v", err)
	}
	if _, err := service.CommitResult(t.Context(), Probe{ID: "probe"}, "assignment", "lease", &observationpb.AssignmentResult{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid result error = %v", err)
	}
	now := time.Now().UTC()
	invalidSeatMap := validResult(now)
	invalidSeatMap.GetCompleted().SetCaptures(nil)
	invalidSeatMap.GetCompleted().SetSeatMap(seatMapSnapshot("", 1))
	if _, err := service.CommitResult(t.Context(), Probe{ID: "probe"}, "assignment", "lease", invalidSeatMap); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid seat-map result error = %v", err)
	}
	seatMapResult := validResult(now)
	seatMapResult.GetCompleted().SetCaptures(nil)
	seatMapResult.GetCompleted().SetSeatMap(seatMapSnapshot("auditorium", 1))
	if _, err := service.CommitResult(t.Context(), Probe{ID: "probe"}, "assignment", "lease", seatMapResult); err == nil {
		t.Fatal("seat-map result unexpectedly committed without an assignment")
	}
	both := proto.CloneOf(seatMapResult)
	both.GetCompleted().SetCatalog(validCatalogSnapshot(now))
	if _, err := service.CommitResult(t.Context(), Probe{ID: "probe"}, "assignment", "lease", both); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed catalog and seat-map result error = %v", err)
	}
	withCapture := proto.CloneOf(seatMapResult)
	withCapture.GetCompleted().SetCaptures(validResult(now).GetCompleted().GetCaptures())
	if _, err := service.CommitResult(t.Context(), Probe{ID: "probe"}, "assignment", "lease", withCapture); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seat-map result with captures error = %v", err)
	}
	if _, err := service.RegisterProbe(t.Context(), containerRegistration(), "127.0.0.1:1", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid container enrollment error = %v", err)
	}
}

func TestClientProbeRegistrationRequiresOneTimeBootstrap(t *testing.T) {
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	authorizer := &memoryClientAuthorizer{authorization: RegistrationAuthorization{
		OwnerUserID: "user_01", DeviceID: "device_01", TicketID: "ticket_01", ExpiresAt: now.Add(time.Minute),
	}}
	service, err := NewService(repository, Config{EnrollmentToken: "enroll", ClientAuthorizer: authorizer})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	service.random = deterministicRandom()
	registration := validRegistration()
	response, err := service.RegisterProbe(t.Context(), registration, "203.0.113.5:443", "signed-ticket")
	if err != nil {
		t.Fatal(err)
	}
	stored := repository.probesByID[response.GetProbeId()]
	if stored.OwnerUserID != "user_01" || stored.DeviceID != "device_01" || authorizer.token != "signed-ticket" {
		t.Fatalf("client probe registration = %+v, token = %q", stored, authorizer.token)
	}
	if _, err := service.RegisterProbe(t.Context(), registration, "203.0.113.5:443", "signed-ticket"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed bootstrap ticket error = %v", err)
	}

	missingAuthorizer, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingAuthorizer.RegisterProbe(t.Context(), registration, "203.0.113.5:443", "ticket"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing client authorizer error = %v", err)
	}
	for _, authorization := range []RegistrationAuthorization{
		{DeviceID: "device", TicketID: "ticket", ExpiresAt: now.Add(time.Minute)},
		{OwnerUserID: "user", TicketID: "ticket", ExpiresAt: now.Add(time.Minute)},
		{OwnerUserID: "user", DeviceID: "device", ExpiresAt: now.Add(time.Minute)},
		{OwnerUserID: "user", DeviceID: "device", TicketID: "ticket", ExpiresAt: now},
	} {
		invalidAuthorizer := &memoryClientAuthorizer{authorization: authorization}
		invalidService, serviceErr := NewService(newMemoryRepository(), Config{
			EnrollmentToken: "enroll", ClientAuthorizer: invalidAuthorizer,
		})
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		invalidService.clock = func() time.Time { return now }
		if _, err := invalidService.RegisterProbe(t.Context(), registration, "203.0.113.5:443", "ticket"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalid authorization %+v error = %v", authorization, err)
		}
	}
}

func TestServiceConfigurationAndDelegationErrors(t *testing.T) {
	if _, err := NewService(nil, Config{EnrollmentToken: "enroll"}); err == nil {
		t.Fatal("nil repository was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{}); err == nil {
		t.Fatal("empty enrollment token was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll", ProbeTokenTTL: -time.Second}); err == nil {
		t.Fatal("negative token TTL was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll", MinimumRuntimeVersion: "invalid"}); err == nil {
		t.Fatal("invalid minimum runtime version was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll", MinimumBrowserRevision: "invalid"}); err == nil {
		t.Fatal("invalid minimum browser revision was accepted")
	}
	repository := newMemoryRepository()
	service, err := NewService(repository, Config{EnrollmentToken: " enroll "})
	if err != nil {
		t.Fatal(err)
	}
	if service.config.ProbeTokenTTL != DefaultProbeTokenTTL || service.config.AssignmentLease != DefaultAssignmentLease ||
		service.config.HeartbeatInterval != DefaultHeartbeatInterval || service.config.ProbeHeartbeatTTL != DefaultProbeHeartbeatTTL {
		t.Fatalf("defaults = %+v", service.config)
	}
	if !service.ValidateEnrollmentToken("enroll") || service.ValidateEnrollmentToken("wrong") {
		t.Fatal("enrollment token validation mismatch")
	}
	failure := errors.New("repository failure")
	repository.err = failure
	if err := service.Ready(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("ready error = %v", err)
	}
	if _, err := service.RegisterProbe(t.Context(), containerRegistration(), "127.0.0.1:1", "enroll"); !errors.Is(err, failure) {
		t.Fatalf("registration repository error = %v", err)
	}
	probe := Probe{ID: "probe", MaxConcurrency: 1, Capabilities: []string{probedomain.CapabilityCGVScheduleCapture}}
	if _, err := service.HeartbeatProbe(t.Context(), probe, degradedHeartbeat(1, "browser_unavailable")); !errors.Is(err, failure) {
		t.Fatalf("heartbeat repository error = %v", err)
	}
	if err := service.DisconnectProbe(t.Context(), probe); !errors.Is(err, failure) {
		t.Fatalf("disconnect repository error = %v", err)
	}
	if _, err := service.ClaimAssignment(t.Context(), probe); !errors.Is(err, failure) {
		t.Fatalf("claim repository error = %v", err)
	}
	if _, err := service.HeartbeatAssignment(t.Context(), probe, "assignment", "lease"); !errors.Is(err, failure) {
		t.Fatalf("assignment heartbeat repository error = %v", err)
	}
	if _, err := service.CommitResult(t.Context(), probe, "assignment", "lease", validResult(time.Now().UTC())); !errors.Is(err, failure) {
		t.Fatalf("result repository error = %v", err)
	}
}

func TestServiceSecretGenerationFailures(t *testing.T) {
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.RegisterProbe(t.Context(), containerRegistration(), "127.0.0.1:1", "enroll"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("access token generation error = %v", err)
	}
	calls := 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, io.ErrUnexpectedEOF
		}
		for index := range buffer {
			buffer[index] = 1
		}
		return len(buffer), nil
	}
	if _, err := service.RegisterProbe(t.Context(), containerRegistration(), "127.0.0.1:1", "enroll"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("probe id generation error = %v", err)
	}
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.ClaimAssignment(t.Context(), Probe{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lease generation error = %v", err)
	}
}

func TestServiceValidationBoundaries(t *testing.T) {
	valid := validRegistration()
	registrations := []*probepb.RegisterRequest{
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.SetInstallationId("")
			return value
		}(),
		func() *probepb.RegisterRequest { value := cloneRegistration(valid); value.ClearKind(); return value }(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.SetMaxConcurrency(0)
			return value
		}(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.GetRuntime().SetComponentVersion("")
			return value
		}(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.GetRuntime().SetComponentVersion("invalid")
			return value
		}(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.GetRuntime().SetBrowserRevision("invalid")
			return value
		}(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.SetCapabilities(nil)
			return value
		}(),
		func() *probepb.RegisterRequest {
			value := cloneRegistration(valid)
			value.SetCapabilities([]*observationpb.Capability{{}})
			return value
		}(),
	}
	for _, registration := range registrations {
		if err := validateRegistration(registration); !errors.Is(err, ErrInvalid) {
			t.Fatalf("registration validation error = %v for %+v", err, registration)
		}
	}

	now := time.Now().UTC()
	results := []*observationpb.AssignmentResult{
		func() *observationpb.AssignmentResult { value := validResult(now); value.SetRunId(""); return value }(),
		func() *observationpb.AssignmentResult {
			value := validResult(now)
			value.SetFinishedAt(timestamppb.New(now.Add(-time.Second)))
			return value
		}(),
		func() *observationpb.AssignmentResult {
			value := validResult(now)
			value.GetCompleted().GetCaptures()[0].ClearTargetDate()
			return value
		}(),
		func() *observationpb.AssignmentResult {
			value := validResult(now)
			value.GetCompleted().GetCaptures()[0].SetErrorCode("unexpected")
			return value
		}(),
		func() *observationpb.AssignmentResult {
			value := validResult(now)
			value.GetCompleted().GetCaptures()[0].GetShowtimes()[0].SetSourceKey("")
			return value
		}(),
	}
	for _, result := range results {
		if err := validateResult(result); !errors.Is(err, ErrInvalid) {
			t.Fatalf("result validation error = %v for %+v", err, result)
		}
	}
	catalogResult := validCatalogResult(now)
	if err := validateResult(catalogResult); err != nil {
		t.Fatalf("valid catalog result rejected: %v", err)
	}
	withCaptures := proto.CloneOf(catalogResult)
	withCaptures.GetCompleted().SetCaptures(validResult(now).GetCompleted().GetCaptures())
	if err := validateResult(withCaptures); !errors.Is(err, ErrInvalid) {
		t.Fatalf("catalog result with captures error = %v", err)
	}

	probe := Probe{MaxConcurrency: 2, Capabilities: []string{probedomain.CapabilityCGVScheduleCapture}}
	duplicateCapabilities, _ := probedomain.Capabilities([]string{
		probedomain.CapabilityCGVScheduleCapture, probedomain.CapabilityCGVScheduleCapture,
	})
	seatMapCapability, _ := probedomain.Capabilities([]string{probedomain.CapabilityCGVSeatMapCapture})
	invalidHeartbeats := []*probepb.HeartbeatRequest{
		healthyHeartbeat(-1),
		new(probepb.HeartbeatRequest),
		func() *probepb.HeartbeatRequest {
			value := healthyHeartbeat(2)
			value.SetActiveAssignmentIds([]string{"assignment"})
			return value
		}(),
		func() *probepb.HeartbeatRequest {
			value := healthyHeartbeat(0)
			value.SetActiveAssignmentIds([]string{""})
			return value
		}(),
		func() *probepb.HeartbeatRequest {
			value := healthyHeartbeat(0)
			value.SetActiveAssignmentIds([]string{"assignment", "assignment"})
			return value
		}(),
		func() *probepb.HeartbeatRequest {
			value := healthyHeartbeat(0)
			value.SetAvailableCapabilities(seatMapCapability)
			return value
		}(),
		func() *probepb.HeartbeatRequest {
			value := healthyHeartbeat(0)
			value.SetAvailableCapabilities(duplicateCapabilities)
			return value
		}(),
	}
	for _, heartbeat := range invalidHeartbeats {
		if _, err := (&Service{repository: newMemoryRepository(), clock: time.Now}).HeartbeatProbe(t.Context(), probe, heartbeat); !errors.Is(err, ErrInvalid) {
			t.Fatalf("heartbeat validation error = %v", err)
		}
	}
	if got := networkID("203.0.113.4"); got == "" {
		t.Fatal("network id is empty")
	}
}

func TestRuntimeCompatibilityDrainsOutdatedProbe(t *testing.T) {
	repository := newMemoryRepository()
	service, err := NewService(repository, Config{
		EnrollmentToken: "enroll", MinimumRuntimeVersion: "1.2.0", MinimumBrowserRevision: "2000",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := Probe{
		ID: "probe", MaxConcurrency: 1, Capabilities: []string{probedomain.CapabilityCGVScheduleCapture},
		Runtime: runtimeFixture("1.1.0", "1999"),
	}
	repository.probesByID[probe.ID] = probe
	response, err := service.HeartbeatProbe(t.Context(), probe, healthyHeartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetDrain() {
		t.Fatalf("heartbeat response = %+v", response)
	}
	compatibility := []struct {
		runtime *commonpb.Runtime
		want    bool
	}{
		{runtime: runtimeFixture("invalid", "2000")},
		{runtime: runtimeFixture("1.1.0", "2000")},
		{runtime: runtimeFixture("v1.2.0", "1999")},
		{runtime: runtimeFixture("v1.2.0", "2000"), want: true},
	}
	for _, test := range compatibility {
		if got := service.runtimeCompatible(test.runtime); got != test.want {
			t.Fatalf("runtime compatibility for %+v = %t, want %t", test.runtime, got, test.want)
		}
	}
	if releasepolicy.CanonicalVersion(" v1.2.0 ") != "v1.2.0" ||
		releasepolicy.CompareNumericRevision("2001", "2000") <= 0 {
		t.Fatal("version normalization or browser revision comparison failed")
	}
}

func validRegistration() *probepb.RegisterRequest {
	capabilities, _ := probedomain.Capabilities([]string{probedomain.CapabilityCGVScheduleCapture})
	request := &probepb.RegisterRequest{}
	request.SetInstallationId("install_01")
	kind := &probepb.ProbeKind{}
	kind.SetClient(&probepb.ClientProbe{})
	request.SetKind(kind)
	request.SetCapabilities(capabilities)
	request.SetMaxConcurrency(1)
	request.SetRuntime(runtimeFixture("0.1.0", "1228"))
	return request
}

func cloneRegistration(request *probepb.RegisterRequest) *probepb.RegisterRequest {
	return proto.CloneOf(request)
}

func containerRegistration() *probepb.RegisterRequest {
	registration := validRegistration()
	kind := &probepb.ProbeKind{}
	kind.SetContainer(&probepb.ContainerProbe{})
	registration.SetKind(kind)
	return registration
}

func runtimeFixture(version, browserRevision string) *commonpb.Runtime {
	runtime := &commonpb.Runtime{}
	runtime.SetComponentVersion(version)
	runtime.SetBrowserRevision(browserRevision)
	runtime.SetPlatform("darwin")
	runtime.SetArchitecture("arm64")
	return runtime
}

func healthyHeartbeat(slots int32) *probepb.HeartbeatRequest {
	health := &probepb.ProbeHealth{}
	health.SetHealthy(&probepb.Healthy{})
	request := &probepb.HeartbeatRequest{}
	request.SetAvailableSlots(slots)
	request.SetHealth(health)
	return request
}

func degradedHeartbeat(slots int32, reason string) *probepb.HeartbeatRequest {
	degraded := &probepb.Degraded{}
	degraded.SetReasonCode(reason)
	health := &probepb.ProbeHealth{}
	health.SetDegraded(degraded)
	request := &probepb.HeartbeatRequest{}
	request.SetAvailableSlots(slots)
	request.SetHealth(health)
	return request
}

func validAssignmentTask(now time.Time) *observationpb.AssignmentTask {
	snapshot := validCatalogSnapshot(now)
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(20)
	schedule := &observationpb.ScheduleTask{}
	schedule.SetTheater(proto.CloneOf(snapshot.GetTheaters()[0]))
	schedule.SetTargetDates([]*commonpb.LocalDate{date})
	schedule.SetLocale("ko-KR")
	schedule.SetTimeZone("Asia/Seoul")
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task := &observationpb.AssignmentTask{}
	task.SetEgress(egress)
	task.SetSchedule(schedule)
	return task
}

func validResult(now time.Time) *observationpb.AssignmentResult {
	snapshot := validCatalogSnapshot(now)
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(20)
	capture := &observationpb.Capture{}
	capture.SetTargetDate(date)
	capture.SetComplete(true)
	capture.SetObservedAt(timestamppb.New(now.Add(9 * time.Second)))
	capture.SetShowtimes([]*catalogpb.Showtime{proto.CloneOf(snapshot.GetShowtimes()[0])})
	completed := &observationpb.Completed{}
	completed.SetCaptures([]*observationpb.Capture{capture})
	result := &observationpb.AssignmentResult{}
	result.SetRunId("run_01")
	result.SetStartedAt(timestamppb.New(now))
	result.SetFinishedAt(timestamppb.New(now.Add(10 * time.Second)))
	result.SetCompleted(completed)
	return result
}

func validCatalogResult(now time.Time) *observationpb.AssignmentResult {
	completed := &observationpb.Completed{}
	completed.SetCatalog(validCatalogSnapshot(now))
	result := &observationpb.AssignmentResult{}
	result.SetRunId("catalog_run")
	result.SetStartedAt(timestamppb.New(now))
	result.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
	result.SetCompleted(completed)
	return result
}

func deterministicRandom() func([]byte) (int, error) {
	value := byte(1)
	return func(buffer []byte) (int, error) {
		for index := range buffer {
			buffer[index] = value
			value++
		}
		return len(buffer), nil
	}
}

type memoryRepository struct {
	err              error
	probesByID       map[string]Probe
	probeByInstall   map[string]string
	assignments      map[string]Assignment
	resultByAssignID map[string]*observationpb.ResultReceipt
	resultHash       map[string]string
	consumedTickets  map[string]struct{}
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		probesByID: make(map[string]Probe), probeByInstall: make(map[string]string),
		assignments: make(map[string]Assignment), resultByAssignID: make(map[string]*observationpb.ResultReceipt),
		resultHash: make(map[string]string), consumedTickets: make(map[string]struct{}),
	}
}

func (repository *memoryRepository) Ready(context.Context) error { return repository.err }

func (repository *memoryRepository) ConsumeProbeBootstrap(_ context.Context, ticketID string, _ time.Time, _ time.Time) error {
	if repository.err != nil {
		return repository.err
	}
	if _, consumed := repository.consumedTickets[ticketID]; consumed {
		return ErrUnauthorized
	}
	repository.consumedTickets[ticketID] = struct{}{}
	return nil
}

type memoryClientAuthorizer struct {
	authorization RegistrationAuthorization
	token         string
	err           error
}

func (authorizer *memoryClientAuthorizer) Authorize(
	_ context.Context,
	_ *probepb.RegisterRequest,
	token string,
	_ time.Time,
) (RegistrationAuthorization, error) {
	authorizer.token = token
	return authorizer.authorization, authorizer.err
}

func (repository *memoryRepository) RegisterProbe(_ context.Context, probe Probe) (Probe, error) {
	if repository.err != nil {
		return Probe{}, repository.err
	}
	if existingID := repository.probeByInstall[probe.InstallationID]; existingID != "" {
		probe.ID = existingID
		probe.CreatedAt = repository.probesByID[existingID].CreatedAt
	}
	repository.probesByID[probe.ID] = probe
	repository.probeByInstall[probe.InstallationID] = probe.ID
	return probe, nil
}

func (repository *memoryRepository) AuthenticateProbe(
	_ context.Context,
	probeID string,
	tokenHash [32]byte,
	now time.Time,
) (Probe, error) {
	if repository.err != nil {
		return Probe{}, repository.err
	}
	probe, exists := repository.probesByID[probeID]
	if !exists || probe.TokenExpiresAt.Before(now) || subtle.ConstantTimeCompare(probe.TokenHash[:], tokenHash[:]) != 1 {
		return Probe{}, ErrUnauthorized
	}
	return probe, nil
}

func (repository *memoryRepository) HeartbeatProbe(
	_ context.Context,
	probeID string,
	heartbeat *probepb.HeartbeatRequest,
	now time.Time,
) (Probe, error) {
	if repository.err != nil {
		return Probe{}, repository.err
	}
	probe, exists := repository.probesByID[probeID]
	if !exists {
		return Probe{}, ErrNotFound
	}
	health, reason := probeHealthKey(heartbeat.GetHealth())
	available, err := probedomain.CapabilityKeys(heartbeat.GetAvailableCapabilities())
	if err != nil {
		return Probe{}, err
	}
	probe.Status = "online"
	probe.Draining = heartbeat.GetDraining()
	probe.AvailableSlots = int(heartbeat.GetAvailableSlots())
	probe.Health = health
	probe.ReasonCode = reason
	probe.AvailableCapabilities = available
	probe.LastHeartbeatAt = now
	repository.probesByID[probeID] = probe
	return probe, nil
}

func (repository *memoryRepository) DisconnectProbe(_ context.Context, probeID string, _ time.Time) error {
	if repository.err != nil {
		return repository.err
	}
	probe, exists := repository.probesByID[probeID]
	if !exists {
		return ErrNotFound
	}
	probe.Status = "offline"
	repository.probesByID[probeID] = probe
	return nil
}

func (repository *memoryRepository) ClaimAssignment(
	_ context.Context,
	probeID string,
	leaseHash [32]byte,
	now time.Time,
	leaseExpiresAt time.Time,
	_ time.Time,
) (Assignment, error) {
	if repository.err != nil {
		return Assignment{}, repository.err
	}
	capabilities := repository.probesByID[probeID].AvailableCapabilities
	if capabilities == nil {
		capabilities = repository.probesByID[probeID].Capabilities
	}
	for id, assignment := range repository.assignments {
		if assignment.Status != "queued" || assignment.NotBefore.After(now) || !assignment.Deadline.After(now) ||
			!slices.Contains(capabilities, assignmentCapability(assignment.Task)) {
			continue
		}
		assignment.Status = "leased"
		assignment.ProbeID = probeID
		assignment.LeaseTokenHash = leaseHash
		assignment.LeaseExpiresAt = leaseExpiresAt
		repository.assignments[id] = assignment
		return assignment, nil
	}
	return Assignment{}, ErrNoAssignment
}

func assignmentCapability(task *observationpb.AssignmentTask) string {
	switch {
	case task.GetSchedule() != nil:
		return probedomain.CapabilityCGVScheduleCapture
	case task.GetCatalog() != nil:
		return probedomain.CapabilityCGVCatalogCapture
	case task.GetSeatMap() != nil:
		return probedomain.CapabilityCGVSeatMapCapture
	default:
		return ""
	}
}

func (repository *memoryRepository) HeartbeatAssignment(
	_ context.Context,
	assignmentID string,
	probeID string,
	leaseHash [32]byte,
	now time.Time,
	leaseExpiresAt time.Time,
) error {
	if repository.err != nil {
		return repository.err
	}
	assignment, exists := repository.assignments[assignmentID]
	if !exists || assignment.ProbeID != probeID {
		return ErrNotFound
	}
	if assignment.LeaseExpiresAt.Before(now) {
		return ErrLeaseExpired
	}
	if subtle.ConstantTimeCompare(assignment.LeaseTokenHash[:], leaseHash[:]) != 1 {
		return ErrUnauthorized
	}
	assignment.LeaseExpiresAt = leaseExpiresAt
	repository.assignments[assignmentID] = assignment
	return nil
}

func (repository *memoryRepository) CommitResult(_ context.Context, commit ResultCommit) (*observationpb.ResultReceipt, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	if previous, exists := repository.resultByAssignID[commit.AssignmentID]; exists {
		if repository.resultHash[commit.AssignmentID] == commit.PayloadHash && previous.GetRunId() == commit.Result.GetRunId() {
			return previous, nil
		}
		return nil, ErrIdempotencyConflict
	}
	assignment, exists := repository.assignments[commit.AssignmentID]
	if !exists || assignment.ProbeID != commit.ProbeID {
		return nil, ErrNotFound
	}
	if assignment.LeaseExpiresAt.Before(commit.CommittedAt) {
		return nil, ErrLeaseExpired
	}
	if subtle.ConstantTimeCompare(assignment.LeaseTokenHash[:], commit.LeaseHash[:]) != 1 {
		return nil, ErrUnauthorized
	}
	receipt := &observationpb.ResultReceipt{}
	receipt.SetAssignmentId(commit.AssignmentID)
	receipt.SetRunId(commit.Result.GetRunId())
	receipt.SetContentHash(commit.PayloadHash)
	receipt.SetAccepted(&observationpb.Accepted{})
	repository.resultByAssignID[commit.AssignmentID] = receipt
	repository.resultHash[commit.AssignmentID] = commit.PayloadHash
	return receipt, nil
}

var _ Repository = (*memoryRepository)(nil)
var _ ClientRegistrationAuthorizer = (*memoryClientAuthorizer)(nil)
