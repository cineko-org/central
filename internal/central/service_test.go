package central

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestProbeLifecycleAndResultIdempotency(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service, err := NewService(repository, Config{EnrollmentToken: "enroll", AssignmentLease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	service.random = deterministicRandom()
	request := validRegistration()
	request.Kind = "container"

	registered, err := service.RegisterProbe(context.Background(), request, "203.0.113.4:443", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if registered.ProbeID == "" || registered.AccessToken == "" || registered.NetworkID == "" {
		t.Fatalf("registration = %+v", registered)
	}
	probe, err := service.AuthenticateProbe(context.Background(), registered.ProbeID, registered.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.HeartbeatProbe(context.Background(), probe, ProbeHeartbeatRequest{
		AvailableSlots: 2, Health: "healthy",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-capacity heartbeat error = %v", err)
	}
	if _, err := service.HeartbeatProbe(context.Background(), probe, ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}); err != nil {
		t.Fatal(err)
	}

	repository.assignments["assignment_01"] = Assignment{
		ID: "assignment_01", Status: "queued", NotBefore: now.Add(-time.Minute), Deadline: now.Add(time.Hour),
		Task: AssignmentTask{
			Kind: contracts.CapabilityCGVScheduleCapture,
			Theater: Theater{
				ID:         contracts.CatalogID(contracts.ProviderCGV, "theater", "0056"),
				ProviderID: contracts.ProviderCGV, SourceKey: "0056",
				Region: "서울", Name: "용산아이파크몰",
			},
			TargetDates: []string{"2026-08-20"}, Locale: "ko-KR", TimeZone: "Asia/Seoul",
			EgressPolicyID: "scan_default",
		},
	}
	claim, err := service.ClaimAssignment(context.Background(), probe)
	if err != nil {
		t.Fatal(err)
	}
	if claim.AssignmentID != "assignment_01" || claim.LeaseToken == "" {
		t.Fatalf("claim = %+v", claim)
	}
	if _, err := service.HeartbeatAssignment(
		context.Background(), probe, claim.AssignmentID, claim.LeaseToken,
	); err != nil {
		t.Fatal(err)
	}
	result := validResult(now)
	receipt, err := service.CommitResult(
		context.Background(), probe, claim.AssignmentID, claim.LeaseToken, result,
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.CommitResult(
		context.Background(), probe, claim.AssignmentID, claim.LeaseToken, result,
	)
	if err != nil || repeated != receipt {
		t.Fatalf("repeated receipt = %+v, %v; want %+v", repeated, err, receipt)
	}
	result.Status = "partial"
	if _, err := service.CommitResult(
		context.Background(), probe, claim.AssignmentID, claim.LeaseToken, result,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting result error = %v", err)
	}

	reregistered, err := service.RegisterProbe(context.Background(), request, "203.0.113.4:443", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if reregistered.ProbeID != registered.ProbeID || reregistered.AccessToken == registered.AccessToken {
		t.Fatalf("re-registration = %+v; first = %+v", reregistered, registered)
	}
	if _, err := service.AuthenticateProbe(
		context.Background(), registered.ProbeID, registered.AccessToken,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token authentication error = %v", err)
	}
}

func TestServiceRejectsInvalidRegistrationAndResult(t *testing.T) {
	t.Parallel()
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	invalid := validRegistration()
	invalid.Runtime.Protocol = ProtocolVersion + 1
	if _, err := service.RegisterProbe(context.Background(), invalid, "127.0.0.1:1", "enroll"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid registration error = %v", err)
	}
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", AssignmentResult{},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid result error = %v", err)
	}
	now := time.Now().UTC()
	invalidSeatMap := validResult(now)
	invalidSeatMap.Captures = nil
	invalidSeatMap.SeatMap = &contracts.SeatMapVersion{}
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", invalidSeatMap,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid seat-map result error = %v", err)
	}
	seatMap := validResult(now)
	seatMap.Captures = nil
	seatMap.SeatMap = &contracts.SeatMapVersion{
		AuditoriumID: "auditorium", Capacity: 1, Layout: seatMapLayoutJSON(t, 1),
	}
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", seatMap,
	); err == nil {
		t.Fatal("seat-map result unexpectedly committed without an assignment")
	}
	both := seatMap
	both.Catalog = &contracts.CatalogSnapshot{}
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", both,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed catalog and seat-map result error = %v", err)
	}
	partial := seatMap
	partial.Status = "partial"
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", partial,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial seat-map result error = %v", err)
	}
	seatMapWithCapture := seatMap
	seatMapWithCapture.Captures = validResult(now).Captures
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", seatMapWithCapture,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seat-map result with captures error = %v", err)
	}
	if _, err := service.RegisterProbe(
		context.Background(), containerRegistration(), "127.0.0.1:1", "wrong-enrollment-token",
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid container enrollment error = %v", err)
	}
}

func TestClientProbeRegistrationRequiresOneTimeBootstrap(t *testing.T) {
	t.Parallel()
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
	response, err := service.RegisterProbe(context.Background(), registration, "203.0.113.5:443", "signed-ticket")
	if err != nil {
		t.Fatal(err)
	}
	stored := repository.probesByID[response.ProbeID]
	if stored.OwnerUserID != "user_01" || stored.DeviceID != "device_01" || authorizer.token != "signed-ticket" {
		t.Fatalf("client Probe registration = %+v, authorizer token = %q", stored, authorizer.token)
	}
	if _, err := service.RegisterProbe(
		context.Background(), registration, "203.0.113.5:443", "signed-ticket",
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed bootstrap ticket error = %v", err)
	}

	missingAuthorizer, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingAuthorizer.RegisterProbe(
		context.Background(), registration, "203.0.113.5:443", "ticket",
	); !errors.Is(err, ErrUnauthorized) {
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
		if _, err := invalidService.RegisterProbe(
			context.Background(), registration, "203.0.113.5:443", "ticket",
		); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalid authorization %+v error = %v", authorization, err)
		}
	}
	authorizer.err = ErrUnauthorized
	if _, err := service.RegisterProbe(
		context.Background(), registration, "203.0.113.5:443", "other-ticket",
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("client authorizer rejection error = %v", err)
	}
}

func TestServiceConfigurationAndDelegationErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil, Config{EnrollmentToken: "enroll"}); err == nil {
		t.Fatal("nil repository was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{}); err == nil {
		t.Fatal("empty enrollment token was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{
		EnrollmentToken: "enroll", ProbeTokenTTL: -time.Second,
	}); err == nil {
		t.Fatal("negative token TTL was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{
		EnrollmentToken: "enroll", MinimumRuntimeVersion: "invalid",
	}); err == nil {
		t.Fatal("invalid minimum runtime version was accepted")
	}
	if _, err := NewService(newMemoryRepository(), Config{
		EnrollmentToken: "enroll", MinimumBrowserRevision: "invalid",
	}); err == nil {
		t.Fatal("invalid minimum browser revision was accepted")
	}

	repository := newMemoryRepository()
	service, err := NewService(repository, Config{EnrollmentToken: " enroll "})
	if err != nil {
		t.Fatal(err)
	}
	if service.config.ProbeTokenTTL != DefaultProbeTokenTTL ||
		service.config.AssignmentLease != DefaultAssignmentLease ||
		service.config.HeartbeatInterval != DefaultHeartbeatInterval ||
		service.config.ProbeHeartbeatTTL != DefaultProbeHeartbeatTTL {
		t.Fatalf("defaults = %+v", service.config)
	}
	if !service.ValidateEnrollmentToken("enroll") || service.ValidateEnrollmentToken("wrong") {
		t.Fatal("enrollment token validation mismatch")
	}
	if err := service.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateProbe(context.Background(), "probe", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty access token error = %v", err)
	}

	failure := errors.New("repository failure")
	repository.err = failure
	if err := service.Ready(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("ready error = %v", err)
	}
	registration := validRegistration()
	registration.Kind = "container"
	if _, err := service.RegisterProbe(context.Background(), registration, "127.0.0.1:1", "enroll"); !errors.Is(err, failure) {
		t.Fatalf("registration repository error = %v", err)
	}
	probe := Probe{ID: "probe", MaxConcurrency: 1}
	if _, err := service.HeartbeatProbe(context.Background(), probe, ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "degraded", ReasonCode: "browser_unavailable",
	}); !errors.Is(err, failure) {
		t.Fatalf("heartbeat repository error = %v", err)
	}
	if err := service.DisconnectProbe(context.Background(), probe); !errors.Is(err, failure) {
		t.Fatalf("disconnect repository error = %v", err)
	}
	if _, err := service.ClaimAssignment(context.Background(), probe); !errors.Is(err, failure) {
		t.Fatalf("claim repository error = %v", err)
	}
	if _, err := service.HeartbeatAssignment(context.Background(), probe, "assignment", "lease"); !errors.Is(err, failure) {
		t.Fatalf("assignment heartbeat repository error = %v", err)
	}
	if _, err := service.CommitResult(
		context.Background(), probe, "assignment", "lease", validResult(time.Now().UTC()),
	); !errors.Is(err, failure) {
		t.Fatalf("result repository error = %v", err)
	}
}

func TestServiceSecretGenerationFailures(t *testing.T) {
	t.Parallel()
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.RegisterProbe(
		context.Background(), containerRegistration(), "127.0.0.1:1", "enroll",
	); !errors.Is(err, io.ErrUnexpectedEOF) {
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
	if _, err := service.RegisterProbe(
		context.Background(), containerRegistration(), "127.0.0.1:1", "enroll",
	); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("probe id generation error = %v", err)
	}

	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.ClaimAssignment(context.Background(), Probe{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lease generation error = %v", err)
	}

	service.marshal = func(any) ([]byte, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := service.CommitResult(
		context.Background(), Probe{}, "assignment", "lease", validResult(time.Now().UTC()),
	); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("result encoding error = %v", err)
	}
}

func TestServiceValidationBoundaries(t *testing.T) {
	t.Parallel()
	valid := validRegistration()
	registrations := []RegisterProbeRequest{
		func() RegisterProbeRequest { value := valid; value.InstallationID = ""; return value }(),
		func() RegisterProbeRequest { value := valid; value.Kind = "unknown"; return value }(),
		func() RegisterProbeRequest { value := valid; value.MaxConcurrency = 0; return value }(),
		func() RegisterProbeRequest { value := valid; value.Runtime.Version = ""; return value }(),
		func() RegisterProbeRequest { value := valid; value.Runtime.Version = "invalid"; return value }(),
		func() RegisterProbeRequest { value := valid; value.Runtime.BrowserRevision = "invalid"; return value }(),
		func() RegisterProbeRequest { value := valid; value.Capabilities = nil; return value }(),
		func() RegisterProbeRequest { value := valid; value.Capabilities = []string{""}; return value }(),
	}
	for _, registration := range registrations {
		if err := validateRegistration(registration); !errors.Is(err, ErrInvalid) {
			t.Fatalf("registration validation error = %v for %+v", err, registration)
		}
	}

	now := time.Now().UTC()
	results := []AssignmentResult{
		func() AssignmentResult { value := validResult(now); value.RunID = ""; return value }(),
		func() AssignmentResult { value := validResult(now); value.Status = "unknown"; return value }(),
		func() AssignmentResult {
			value := validResult(now)
			value.FinishedAt = now.Add(-time.Second)
			return value
		}(),
		func() AssignmentResult {
			value := validResult(now)
			value.Captures[0].TargetDate = "not-a-date"
			return value
		}(),
		func() AssignmentResult {
			value := validResult(now)
			value.Captures[0].ErrorCode = "unexpected"
			return value
		}(),
		func() AssignmentResult {
			value := validResult(now)
			value.Captures[0].Showtimes[0].SourceKey = ""
			return value
		}(),
	}
	for _, result := range results {
		if err := validateResult(result); !errors.Is(err, ErrInvalid) {
			t.Fatalf("result validation error = %v for %+v", err, result)
		}
	}
	catalog := validCatalogSnapshot(now)
	validCatalogResult := AssignmentResult{
		RunID: "catalog_run", Status: "completed", StartedAt: now,
		FinishedAt: now.Add(time.Second), Catalog: &catalog,
	}
	if err := validateResult(validCatalogResult); err != nil {
		t.Fatalf("valid catalog result rejected: %v", err)
	}
	invalidCatalogResults := []AssignmentResult{
		func() AssignmentResult { value := validCatalogResult; value.Status = "partial"; return value }(),
		func() AssignmentResult {
			value := validCatalogResult
			value.Captures = validResult(now).Captures
			return value
		}(),
		func() AssignmentResult {
			value := validCatalogResult
			broken := *value.Catalog
			broken.Provider.Name = ""
			value.Catalog = &broken
			return value
		}(),
	}
	for _, result := range invalidCatalogResults {
		if err := validateResult(result); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid catalog result error = %v", err)
		}
	}

	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	probe := Probe{MaxConcurrency: 2, Capabilities: []string{contracts.CapabilityCGVScheduleCapture}}
	invalidHeartbeats := []ProbeHeartbeatRequest{
		{AvailableSlots: -1, Health: "healthy"},
		{AvailableSlots: 0, Health: "unknown"},
		{AvailableSlots: 0, Health: "healthy", ReasonCode: "unexpected"},
		{AvailableSlots: 2, ActiveAssignmentIDs: []string{"assignment"}, Health: "healthy"},
		{ActiveAssignmentIDs: []string{""}, Health: "healthy"},
		{ActiveAssignmentIDs: []string{"assignment", "assignment"}, Health: "healthy"},
		{AvailableCapabilities: []string{contracts.CapabilityCGVSeatMapCapture}, Health: "healthy"},
		{AvailableCapabilities: []string{
			contracts.CapabilityCGVScheduleCapture, contracts.CapabilityCGVScheduleCapture,
		}, Health: "healthy"},
	}
	for _, heartbeat := range invalidHeartbeats {
		if _, err := service.HeartbeatProbe(context.Background(), probe, heartbeat); !errors.Is(err, ErrInvalid) {
			t.Fatalf("heartbeat validation error = %v", err)
		}
	}
	if _, err := service.HeartbeatAssignment(context.Background(), probe, "", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty assignment lease error = %v", err)
	}
	if got := networkID("203.0.113.4"); got == "" {
		t.Fatal("network id is empty")
	}
}

func TestRuntimeCompatibilityDrainsOutdatedProbe(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	service, err := NewService(repository, Config{
		EnrollmentToken: "enroll", MinimumRuntimeVersion: "1.2.0", MinimumBrowserRevision: "2000",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := Probe{
		ID: "probe", MaxConcurrency: 1,
		Runtime: Runtime{Version: "1.1.0", BrowserRevision: "1999", Platform: "linux", Arch: "amd64"},
	}
	repository.probesByID[probe.ID] = probe
	response, err := service.HeartbeatProbe(context.Background(), probe, ProbeHeartbeatRequest{Health: "healthy"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Drain {
		t.Fatalf("heartbeat response = %+v", response)
	}
	compatibility := []struct {
		runtime Runtime
		want    bool
	}{
		{runtime: Runtime{Version: "invalid", BrowserRevision: "2000"}},
		{runtime: Runtime{Version: "1.1.0", BrowserRevision: "2000"}},
		{runtime: Runtime{Version: "v1.2.0", BrowserRevision: "1999"}},
		{runtime: Runtime{Version: "v1.2.0", BrowserRevision: "2000"}, want: true},
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

func validRegistration() RegisterProbeRequest {
	return RegisterProbeRequest{
		InstallationID: "install_01", Kind: "client",
		Capabilities: []string{contracts.CapabilityCGVScheduleCapture}, MaxConcurrency: 1,
		Runtime: Runtime{
			Version: "0.1.0", Protocol: ProtocolVersion, BrowserRevision: "1228",
			Platform: "darwin", Arch: "arm64",
		},
	}
}

func containerRegistration() RegisterProbeRequest {
	registration := validRegistration()
	registration.Kind = "container"
	return registration
}

func validResult(now time.Time) AssignmentResult {
	providerID := contracts.ProviderCGV
	theaterID := contracts.CatalogID(providerID, "theater", "0056")
	movieSourceKey := "00001234"
	auditoriumSourceKey := "0056/0007"
	showtimeSourceKey := "0056/2026-08-20/0007/0003"
	return AssignmentResult{
		RunID: "run_01", Status: "completed", StartedAt: now, FinishedAt: now.Add(10 * time.Second),
		Captures: []Capture{{
			TargetDate: "2026-08-20", Complete: true, ObservedAt: now.Add(9 * time.Second),
			Showtimes: []Showtime{{
				ID:         contracts.CatalogID(providerID, "showtime", showtimeSourceKey),
				ProviderID: providerID, SourceKey: showtimeSourceKey, TheaterID: theaterID,
				Movie: Movie{
					ID:         contracts.CatalogID(providerID, "movie", movieSourceKey),
					ProviderID: providerID, SourceKey: movieSourceKey, Title: "영화",
				},
				Auditorium: Auditorium{
					ID:        contracts.CatalogID(providerID, "auditorium", auditoriumSourceKey),
					TheaterID: theaterID, SourceKey: auditoriumSourceKey,
					Name: "IMAX관", ScreenTypes: []string{"IMAX"}, Capacity: 624,
				},
				StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(26 * time.Hour),
				AvailableSeats: 500, Capacity: 624,
			}},
		}},
	}
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
	resultByAssignID map[string]ResultReceipt
	resultHash       map[string]string
	consumedTickets  map[string]struct{}
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		probesByID: make(map[string]Probe), probeByInstall: make(map[string]string),
		assignments: make(map[string]Assignment), resultByAssignID: make(map[string]ResultReceipt),
		resultHash: make(map[string]string), consumedTickets: make(map[string]struct{}),
	}
}

func (repository *memoryRepository) Ready(context.Context) error { return repository.err }

func (repository *memoryRepository) ConsumeProbeBootstrap(
	_ context.Context,
	ticketID string,
	_ time.Time,
	_ time.Time,
) error {
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
	_ RegisterProbeRequest,
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
	if probeID != "" {
		probe, exists := repository.probesByID[probeID]
		if !exists || probe.TokenExpiresAt.Before(now) || subtle.ConstantTimeCompare(probe.TokenHash[:], tokenHash[:]) != 1 {
			return Probe{}, ErrUnauthorized
		}
		return probe, nil
	}
	for _, probe := range repository.probesByID {
		if probe.TokenExpiresAt.After(now) && subtle.ConstantTimeCompare(probe.TokenHash[:], tokenHash[:]) == 1 {
			return probe, nil
		}
	}
	return Probe{}, ErrUnauthorized
}

func (repository *memoryRepository) HeartbeatProbe(
	_ context.Context,
	probeID string,
	heartbeat ProbeHeartbeatRequest,
	now time.Time,
) (Probe, error) {
	if repository.err != nil {
		return Probe{}, repository.err
	}
	probe, exists := repository.probesByID[probeID]
	if !exists {
		return Probe{}, ErrNotFound
	}
	probe.Status = "online"
	probe.Draining = heartbeat.Draining
	probe.AvailableSlots = heartbeat.AvailableSlots
	probe.Health = heartbeat.Health
	probe.ReasonCode = heartbeat.ReasonCode
	probe.AvailableCapabilities = slices.Clone(heartbeat.AvailableCapabilities)
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
			!slices.Contains(capabilities, assignment.Task.Kind) {
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

func (repository *memoryRepository) CommitResult(_ context.Context, commit ResultCommit) (ResultReceipt, error) {
	if repository.err != nil {
		return ResultReceipt{}, repository.err
	}
	if previous, exists := repository.resultByAssignID[commit.AssignmentID]; exists {
		if repository.resultHash[commit.AssignmentID] == commit.PayloadHash && previous.RunID == commit.Result.RunID {
			return previous, nil
		}
		return ResultReceipt{}, ErrIdempotencyConflict
	}
	assignment, exists := repository.assignments[commit.AssignmentID]
	if !exists || assignment.ProbeID != commit.ProbeID {
		return ResultReceipt{}, ErrNotFound
	}
	if assignment.LeaseExpiresAt.Before(commit.CommittedAt) {
		return ResultReceipt{}, ErrLeaseExpired
	}
	if subtle.ConstantTimeCompare(assignment.LeaseTokenHash[:], commit.LeaseHash[:]) != 1 {
		return ResultReceipt{}, ErrUnauthorized
	}
	receipt := ResultReceipt{
		AssignmentID: commit.AssignmentID, RunID: commit.Result.RunID,
		ContentHash: commit.PayloadHash, Status: commit.Result.Status,
	}
	repository.resultByAssignID[commit.AssignmentID] = receipt
	repository.resultHash[commit.AssignmentID] = commit.PayloadHash
	return receipt, nil
}
