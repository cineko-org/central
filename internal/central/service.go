package central

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	seatavailabilitydomain "github.com/cineko-org/central/internal/domain/seatavailability"
	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/v3/gen/go/cineko/probe"

	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Config struct {
	EnrollmentToken        string
	ProbeTokenTTL          time.Duration
	AssignmentLease        time.Duration
	HeartbeatInterval      time.Duration
	ProbeHeartbeatTTL      time.Duration
	MinimumRuntimeVersion  string
	MinimumBrowserRevision string
	ClientAuthorizer       ClientRegistrationAuthorizer
}

type ClientRegistrationAuthorizer interface {
	Authorize(
		context.Context,
		*probepb.RegisterRequest,
		string,
		time.Time,
	) (RegistrationAuthorization, error)
}

type Service struct {
	repository Repository
	config     Config
	clock      func() time.Time
	random     func([]byte) (int, error)
}

func NewService(repository Repository, config Config) (*Service, error) {
	if repository == nil {
		return nil, errors.New("central repository is required")
	}
	config.EnrollmentToken = strings.TrimSpace(config.EnrollmentToken)
	if config.EnrollmentToken == "" {
		return nil, errors.New("probe enrollment token is required")
	}
	if config.ProbeTokenTTL == 0 {
		config.ProbeTokenTTL = DefaultProbeTokenTTL
	}
	if config.AssignmentLease == 0 {
		config.AssignmentLease = DefaultAssignmentLease
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if config.ProbeHeartbeatTTL == 0 {
		config.ProbeHeartbeatTTL = DefaultProbeHeartbeatTTL
	}
	if config.ProbeTokenTTL <= 0 || config.AssignmentLease <= 0 || config.HeartbeatInterval <= 0 ||
		config.ProbeHeartbeatTTL < config.HeartbeatInterval {
		return nil, errors.New("central token, lease and heartbeat durations are invalid")
	}
	if config.MinimumRuntimeVersion != "" && !semver.IsValid(releasepolicy.CanonicalVersion(config.MinimumRuntimeVersion)) {
		return nil, errors.New("minimum probe runtime version must be semantic versioning")
	}
	if config.MinimumBrowserRevision != "" && !releasepolicy.IsNumericRevision(config.MinimumBrowserRevision) {
		return nil, errors.New("minimum browser revision must be numeric")
	}
	return &Service{
		repository: repository, config: config, clock: time.Now, random: rand.Read,
	}, nil
}

func (service *Service) Ready(ctx context.Context) error {
	return service.repository.Ready(ctx)
}

func (service *Service) ValidateEnrollmentToken(token string) bool {
	want, got := []byte(service.config.EnrollmentToken), []byte(strings.TrimSpace(token))
	return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func (service *Service) RegisterProbe(
	ctx context.Context,
	request *probepb.RegisterRequest,
	remoteAddress string,
	credential string,
) (*probepb.RegisterResponse, error) {
	if err := validateRegistration(request); err != nil {
		return nil, err
	}
	now := service.clock().UTC()
	authorization, err := service.authorizeRegistration(ctx, request, credential, now)
	if err != nil {
		return nil, err
	}
	accessToken, tokenHash, err := service.secret("cpt_")
	if err != nil {
		return nil, err
	}
	probeID, _, err := service.secret("probe_")
	if err != nil {
		return nil, err
	}
	capabilities, err := probedomain.CapabilityKeys(request.GetCapabilities())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	probe, err := service.repository.RegisterProbe(ctx, Probe{
		ID: probeID, InstallationID: request.GetInstallationId(), Kind: probeKindKey(request.GetKind()),
		OwnerUserID: authorization.OwnerUserID, DeviceID: authorization.DeviceID,
		NetworkID: networkID(remoteAddress), NetworkHint: request.GetNetworkHint(),
		Capabilities: capabilities, MaxConcurrency: int(request.GetMaxConcurrency()),
		Runtime: proto.CloneOf(request.GetRuntime()), TokenHash: tokenHash,
		TokenExpiresAt: now.Add(service.config.ProbeTokenTTL),
		Status:         "online", Health: "healthy", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	response := &probepb.RegisterResponse{}
	response.SetProbeId(probe.ID)
	response.SetNetworkId(probe.NetworkID)
	response.SetAccessToken(accessToken)
	response.SetTokenExpiresAt(timestamppb.New(probe.TokenExpiresAt))
	response.SetHeartbeatIntervalSeconds(numeric.ClampInt64ToInt32(int64(service.config.HeartbeatInterval / time.Second)))
	return response, nil
}

func (service *Service) authorizeRegistration(
	ctx context.Context,
	request *probepb.RegisterRequest,
	credential string,
	now time.Time,
) (RegistrationAuthorization, error) {
	if request.GetKind().GetContainer() != nil {
		if !service.ValidateEnrollmentToken(credential) {
			return RegistrationAuthorization{}, ErrUnauthorized
		}
		return RegistrationAuthorization{}, nil
	}
	if service.config.ClientAuthorizer == nil {
		return RegistrationAuthorization{}, ErrUnauthorized
	}
	authorization, err := service.config.ClientAuthorizer.Authorize(ctx, request, credential, now)
	if err != nil {
		return RegistrationAuthorization{}, ErrUnauthorized
	}
	if strings.TrimSpace(authorization.OwnerUserID) == "" || strings.TrimSpace(authorization.DeviceID) == "" ||
		strings.TrimSpace(authorization.TicketID) == "" || !authorization.ExpiresAt.After(now) {
		return RegistrationAuthorization{}, ErrUnauthorized
	}
	if err := service.repository.ConsumeProbeBootstrap(
		ctx, authorization.TicketID, authorization.ExpiresAt, now,
	); err != nil {
		return RegistrationAuthorization{}, err
	}
	return authorization, nil
}

func (service *Service) AuthenticateProbe(
	ctx context.Context,
	probeID string,
	accessToken string,
) (Probe, error) {
	if strings.TrimSpace(accessToken) == "" {
		return Probe{}, ErrUnauthorized
	}
	return service.repository.AuthenticateProbe(ctx, probeID, sha256.Sum256([]byte(accessToken)), service.clock().UTC())
}

func (service *Service) HeartbeatProbe(
	ctx context.Context,
	probe Probe,
	request *probepb.HeartbeatRequest,
) (*probepb.HeartbeatResponse, error) {
	if err := normalizeAndValidateHeartbeat(probe, request); err != nil {
		return nil, err
	}
	now := service.clock().UTC()
	if !service.runtimeCompatible(probe.Runtime) {
		request.SetDraining(true)
	}
	updated, err := service.repository.HeartbeatProbe(ctx, probe.ID, request, now)
	if err != nil {
		return nil, err
	}
	response := &probepb.HeartbeatResponse{}
	response.SetServerTime(timestamppb.New(now))
	response.SetDrain(updated.Draining)
	response.SetMinimumRuntimeVersion(service.config.MinimumRuntimeVersion)
	response.SetMinimumBrowserRevision(service.config.MinimumBrowserRevision)
	return response, nil
}

func normalizeAndValidateHeartbeat(probe Probe, request *probepb.HeartbeatRequest) error {
	if request == nil {
		return fmt.Errorf("%w: heartbeat is required", ErrInvalid)
	}
	if request.GetAvailableCapabilities() == nil {
		capabilities, err := probedomain.Capabilities(probe.Capabilities)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		request.SetAvailableCapabilities(capabilities)
	}
	if request.GetAvailableSlots() < 0 || int(request.GetAvailableSlots()) > probe.MaxConcurrency {
		return fmt.Errorf("%w: availableSlots is outside probe capacity", ErrInvalid)
	}
	health, _ := probeHealthKey(request.GetHealth())
	if health != "healthy" && health != "degraded" {
		return fmt.Errorf("%w: unsupported probe health", ErrInvalid)
	}
	if len(request.GetActiveAssignmentIds())+int(request.GetAvailableSlots()) > probe.MaxConcurrency {
		return fmt.Errorf("%w: active assignments and available slots exceed probe capacity", ErrInvalid)
	}
	if err := validateActiveAssignments(request.GetActiveAssignmentIds()); err != nil {
		return err
	}
	available, err := probedomain.CapabilityKeys(request.GetAvailableCapabilities())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return validateAvailableCapabilities(probe.Capabilities, available)
}

func validateActiveAssignments(values []string) error {
	activeIDs := make(map[string]struct{}, len(values))
	for _, assignmentID := range values {
		assignmentID = strings.TrimSpace(assignmentID)
		if assignmentID == "" {
			return fmt.Errorf("%w: active assignment id cannot be empty", ErrInvalid)
		}
		if _, duplicate := activeIDs[assignmentID]; duplicate {
			return fmt.Errorf("%w: active assignment ids must be unique", ErrInvalid)
		}
		activeIDs[assignmentID] = struct{}{}
	}
	return nil
}

func validateAvailableCapabilities(registeredValues, availableValues []string) error {
	registered := make(map[string]struct{}, len(registeredValues))
	for _, capability := range registeredValues {
		registered[capability] = struct{}{}
	}
	available := make(map[string]struct{}, len(availableValues))
	for _, capability := range availableValues {
		if _, supported := registered[capability]; !supported || !probedomain.IsSupportedCapability(capability) {
			return fmt.Errorf("%w: available capability was not registered", ErrInvalid)
		}
		if _, duplicate := available[capability]; duplicate {
			return fmt.Errorf("%w: available capabilities must be unique", ErrInvalid)
		}
		available[capability] = struct{}{}
	}
	return nil
}

func (service *Service) DisconnectProbe(ctx context.Context, probe Probe) error {
	return service.repository.DisconnectProbe(ctx, probe.ID, service.clock().UTC())
}

func (service *Service) ClaimAssignment(
	ctx context.Context,
	probe Probe,
) (*probepb.ClaimAssignmentResponse, error) {
	now := service.clock().UTC()
	leaseToken, leaseHash, err := service.secret("lease_")
	if err != nil {
		return nil, err
	}
	assignment, err := service.repository.ClaimAssignment(
		ctx, probe.ID, leaseHash, now, now.Add(service.config.AssignmentLease),
		now.Add(-service.config.ProbeHeartbeatTTL),
	)
	if err != nil {
		return nil, err
	}
	lease := &probepb.AssignmentLease{}
	lease.SetAssignmentId(assignment.ID)
	lease.SetLeaseToken(leaseToken)
	lease.SetLeaseExpiresAt(timestamppb.New(assignment.LeaseExpiresAt))
	lease.SetNotBefore(timestamppb.New(assignment.NotBefore))
	lease.SetDeadline(timestamppb.New(assignment.Deadline))
	lease.SetTask(assignment.Task)
	response := &probepb.ClaimAssignmentResponse{}
	response.SetAssignment(lease)
	return response, nil
}

// WaitForAssignment waits for a claimable assignment when the repository
// supports event-driven wakeups. Memory repositories intentionally return
// immediately through the optional capability boundary.
func (service *Service) WaitForAssignment(ctx context.Context, probe Probe) error {
	waiter, ok := service.repository.(AssignmentWaiter)
	if !ok {
		return nil
	}
	return waiter.WaitForAssignment(ctx, probe.ID, service.clock().UTC().Add(-service.config.ProbeHeartbeatTTL))
}

func (service *Service) HeartbeatAssignment(
	ctx context.Context,
	probe Probe,
	assignmentID string,
	leaseToken string,
) (*probepb.HeartbeatAssignmentResponse, error) {
	if strings.TrimSpace(assignmentID) == "" || strings.TrimSpace(leaseToken) == "" {
		return nil, ErrUnauthorized
	}
	now := service.clock().UTC()
	leaseExpiresAt := now.Add(service.config.AssignmentLease)
	if err := service.repository.HeartbeatAssignment(
		ctx, assignmentID, probe.ID, sha256.Sum256([]byte(leaseToken)), now, leaseExpiresAt,
	); err != nil {
		return nil, err
	}
	response := &probepb.HeartbeatAssignmentResponse{}
	response.SetLeaseExpiresAt(timestamppb.New(leaseExpiresAt))
	return response, nil
}

func (service *Service) CommitResult(
	ctx context.Context,
	probe Probe,
	assignmentID string,
	leaseToken string,
	result *observationpb.AssignmentResult,
) (*observationpb.ResultReceipt, error) {
	if completed := result.GetCompleted(); completed != nil {
		if liveSeat := completed.GetLiveSeat(); liveSeat != nil {
			if liveSeat.GetLayout() == nil || liveSeat.GetAvailability() == nil {
				return nil, fmt.Errorf("%w: live seat observation requires layout and availability", ErrInvalid)
			}
			if err := catalogdomain.NormalizeSeatMap(liveSeat.GetLayout(), service.clock().UTC()); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
			if err := seatavailabilitydomain.Normalize(liveSeat.GetAvailability(), service.clock().UTC()); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
		}
		if completed.GetCatalog() != nil {
			if err := catalogdomain.NormalizeSnapshot(completed.GetCatalog()); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
		}
	}
	if err := validateResult(result); err != nil {
		return nil, err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode assignment result: %w", err)
	}
	digest := sha256.Sum256(payload)
	return service.repository.CommitResult(ctx, ResultCommit{
		AssignmentID: assignmentID, ProbeID: probe.ID,
		LeaseHash: sha256.Sum256([]byte(leaseToken)), PayloadHash: hex.EncodeToString(digest[:]),
		Result: result, CommittedAt: service.clock().UTC(),
	})
}

func (service *Service) secret(prefix string) (string, [32]byte, error) {
	buffer := make([]byte, 24)
	if _, err := service.random(buffer); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate Central secret: %w", err)
	}
	value := prefix + base64.RawURLEncoding.EncodeToString(buffer)
	return value, sha256.Sum256([]byte(value)), nil
}

func validateRegistration(request *probepb.RegisterRequest) error {
	if request == nil || strings.TrimSpace(request.GetInstallationId()) == "" {
		return fmt.Errorf("%w: installationId is required", ErrInvalid)
	}
	if request.GetKind() == nil || (!request.GetKind().HasContainer() && !request.GetKind().HasClient()) {
		return fmt.Errorf("%w: kind must be container or client", ErrInvalid)
	}
	if request.GetMaxConcurrency() < 1 || request.GetMaxConcurrency() > 32 {
		return fmt.Errorf("%w: maxConcurrency must be between 1 and 32", ErrInvalid)
	}
	if err := validateRuntime(request.GetRuntime()); err != nil {
		return err
	}
	if len(request.GetCapabilities()) == 0 {
		return fmt.Errorf("%w: at least one capability is required", ErrInvalid)
	}
	if _, err := probedomain.CapabilityKeys(request.GetCapabilities()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func validateRuntime(runtime *commonpb.Runtime) error {
	if runtime == nil || strings.TrimSpace(runtime.GetComponentVersion()) == "" || strings.TrimSpace(runtime.GetBrowserRevision()) == "" ||
		strings.TrimSpace(runtime.GetPlatform()) == "" || strings.TrimSpace(runtime.GetArchitecture()) == "" {
		return fmt.Errorf("%w: runtime is incomplete", ErrInvalid)
	}
	if !semver.IsValid(releasepolicy.CanonicalVersion(runtime.GetComponentVersion())) {
		return fmt.Errorf("%w: runtime version must use semantic versioning", ErrInvalid)
	}
	if !releasepolicy.IsNumericRevision(runtime.GetBrowserRevision()) {
		return fmt.Errorf("%w: browser revision must be numeric", ErrInvalid)
	}
	return nil
}

//nolint:gocyclo,cyclop // Each latest-Proto result variant has distinct required fields and must remain exhaustive.
func validateResult(result *observationpb.AssignmentResult) error {
	if result == nil || strings.TrimSpace(result.GetRunId()) == "" {
		return fmt.Errorf("%w: runId is required", ErrInvalid)
	}
	if result.GetStartedAt() == nil || result.GetFinishedAt() == nil || result.GetStartedAt().CheckValid() != nil || result.GetFinishedAt().CheckValid() != nil || result.GetFinishedAt().AsTime().Before(result.GetStartedAt().AsTime()) {
		return fmt.Errorf("%w: invalid result time range", ErrInvalid)
	}
	completed, deferred, failed := result.GetCompleted(), result.GetDeferred(), result.GetFailed()
	outcomes := 0
	if completed != nil {
		outcomes++
	}
	if deferred != nil {
		outcomes++
	}
	if failed != nil {
		outcomes++
	}
	if outcomes != 1 {
		return fmt.Errorf("%w: assignment result outcome is required", ErrInvalid)
	}
	if deferred != nil {
		if deferred.GetReason() == nil || deferred.GetReason().GetNoBookableShowtime() == nil && deferred.GetReason().GetTargetDateUnavailable() == nil {
			return fmt.Errorf("%w: deferred result reason is required", ErrInvalid)
		}
		return nil
	}
	if completed == nil {
		if failed.GetReason() == nil {
			return fmt.Errorf("%w: failed result reason is required", ErrInvalid)
		}
		return nil
	}
	payloads := 0
	if completed.GetSchedule() != nil {
		payloads++
	}
	if completed.GetCatalog() != nil {
		payloads++
	}
	if completed.GetLiveSeat() != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("%w: completed assignment result payload is required", ErrInvalid)
	}
	for _, capture := range completed.GetSchedule().GetCaptures() {
		if err := validateCapture(capture); err != nil {
			return err
		}
	}
	return nil
}

func validateCapture(capture *observationpb.Capture) error {
	if capture == nil || capture.GetTargetDate() == nil || capture.GetObservedAt() == nil || capture.GetObservedAt().CheckValid() != nil {
		return fmt.Errorf("%w: invalid capture", ErrInvalid)
	}
	if capture.GetComplete() && capture.GetErrorCode() != "" {
		return fmt.Errorf("%w: complete capture cannot include errorCode", ErrInvalid)
	}
	for _, showtime := range capture.GetShowtimes() {
		if err := validateShowtime(showtime); err != nil || !proto.Equal(showtime.GetIdentity().GetCgv().GetScheduleDate(), capture.GetTargetDate()) {
			if err == nil {
				err = errors.New("showtime schedule date does not match capture target date")
			}
			return err
		}
	}
	return nil
}

func validateShowtime(showtime *catalogpb.Showtime) error {
	if !showtimeIdentityComplete(showtime) || !showtimeIdentityCanonical(showtime) ||
		showtime.GetStartsAt() == nil || showtime.GetEndsAt() == nil ||
		!showtime.GetEndsAt().AsTime().After(showtime.GetStartsAt().AsTime()) ||
		showtime.GetAvailableSeats() < 0 || showtime.GetCapacity() < showtime.GetAvailableSeats() {
		return fmt.Errorf("%w: invalid showtime", ErrInvalid)
	}
	return nil
}

func validLocalDate(value *commonpb.LocalDate) bool {
	if value == nil || value.GetYear() < 1 || value.GetMonth() < 1 || value.GetMonth() > 12 || value.GetDay() < 1 || value.GetDay() > 31 {
		return false
	}
	date := time.Date(int(value.GetYear()), time.Month(value.GetMonth()), int(value.GetDay()), 0, 0, 0, 0, time.UTC)
	return date.Year() == int(value.GetYear()) && date.Month() == time.Month(value.GetMonth()) && date.Day() == int(value.GetDay())
}

func showtimeIdentityComplete(showtime *catalogpb.Showtime) bool {
	if showtime == nil || showtime.GetMovie() == nil || showtime.GetAuditorium() == nil {
		return false
	}
	showtimeKey, showtimeKeyOK := catalogdomain.ShowtimeSourceKey(showtime)
	movieKey, movieKeyOK := catalogdomain.MovieSourceKey(showtime.GetMovie())
	auditoriumKey, auditoriumKeyOK := catalogdomain.AuditoriumSourceKey(showtime.GetAuditorium())
	values := []string{
		showtime.GetId(), showtime.GetProviderId(), showtimeKey, showtime.GetTheaterId(),
		showtime.GetMovie().GetId(), movieKey, showtime.GetMovie().GetTitle(),
		showtime.GetAuditorium().GetId(), auditoriumKey, showtime.GetAuditorium().GetName(),
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return showtimeKeyOK && movieKeyOK && auditoriumKeyOK && showtime.GetMovie().GetProviderId() == showtime.GetProviderId() &&
		showtime.GetAuditorium().GetTheaterId() == showtime.GetTheaterId()
}

func showtimeIdentityCanonical(showtime *catalogpb.Showtime) bool {
	providerID := strings.TrimSpace(showtime.GetProviderId())
	showtimeKey, showtimeOK := catalogdomain.ShowtimeSourceKey(showtime)
	movieKey, movieOK := catalogdomain.MovieSourceKey(showtime.GetMovie())
	auditoriumKey, auditoriumOK := catalogdomain.AuditoriumSourceKey(showtime.GetAuditorium())
	return showtimeOK && movieOK && auditoriumOK &&
		showtime.GetId() == catalogdomain.CatalogID(providerID, "showtime", showtimeKey) &&
		showtime.GetMovie().GetId() == catalogdomain.CatalogID(providerID, "movie", movieKey) &&
		showtime.GetAuditorium().GetId() == catalogdomain.CatalogID(providerID, "auditorium", auditoriumKey)
}

func networkID(remoteAddress string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.TrimSpace(remoteAddress)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(host)))
	return "net_" + hex.EncodeToString(digest[:8])
}

func probeKindKey(kind *probepb.ProbeKind) string {
	switch {
	case kind != nil && kind.GetContainer() != nil:
		return "container"
	case kind != nil && kind.GetClient() != nil:
		return "client"
	default:
		return ""
	}
}

func probeHealthKey(health *probepb.ProbeHealth) (string, string) {
	switch {
	case health != nil && health.GetHealthy() != nil:
		return "healthy", ""
	case health != nil && health.GetDegraded() != nil:
		return "degraded", health.GetDegraded().GetReasonCode()
	case health != nil && health.GetUnhealthy() != nil:
		return "unhealthy", health.GetUnhealthy().GetReasonCode()
	default:
		return "", ""
	}
}

func (service *Service) runtimeCompatible(runtime *commonpb.Runtime) bool {
	if runtime == nil || !semver.IsValid(releasepolicy.CanonicalVersion(runtime.GetComponentVersion())) ||
		!releasepolicy.IsNumericRevision(runtime.GetBrowserRevision()) {
		return false
	}
	if minimum := strings.TrimSpace(service.config.MinimumRuntimeVersion); minimum != "" &&
		semver.Compare(releasepolicy.CanonicalVersion(runtime.GetComponentVersion()), releasepolicy.CanonicalVersion(minimum)) < 0 {
		return false
	}
	if minimum := strings.TrimSpace(service.config.MinimumBrowserRevision); minimum != "" &&
		releasepolicy.CompareNumericRevision(runtime.GetBrowserRevision(), minimum) < 0 {
		return false
	}
	return true
}
