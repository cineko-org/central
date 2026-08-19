package central

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"

	"golang.org/x/mod/semver"
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
		RegisterProbeRequest,
		string,
		time.Time,
	) (RegistrationAuthorization, error)
}

type Service struct {
	repository Repository
	config     Config
	clock      func() time.Time
	random     func([]byte) (int, error)
	marshal    func(any) ([]byte, error)
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
	if config.MinimumRuntimeVersion != "" && !semver.IsValid(canonicalVersion(config.MinimumRuntimeVersion)) {
		return nil, errors.New("minimum probe runtime version must be semantic versioning")
	}
	if config.MinimumBrowserRevision != "" && !isNumericRevision(config.MinimumBrowserRevision) {
		return nil, errors.New("minimum browser revision must be numeric")
	}
	return &Service{
		repository: repository, config: config, clock: time.Now, random: rand.Read, marshal: json.Marshal,
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
	request RegisterProbeRequest,
	remoteAddress string,
	credential string,
) (RegisterProbeResponse, error) {
	if err := validateRegistration(request); err != nil {
		return RegisterProbeResponse{}, err
	}
	now := service.clock().UTC()
	authorization, err := service.authorizeRegistration(ctx, request, credential, now)
	if err != nil {
		return RegisterProbeResponse{}, err
	}
	accessToken, tokenHash, err := service.secret("cpt_")
	if err != nil {
		return RegisterProbeResponse{}, err
	}
	probeID, _, err := service.secret("probe_")
	if err != nil {
		return RegisterProbeResponse{}, err
	}
	probe, err := service.repository.RegisterProbe(ctx, Probe{
		ID: probeID, InstallationID: request.InstallationID, Kind: request.Kind,
		OwnerUserID: authorization.OwnerUserID, DeviceID: authorization.DeviceID,
		NetworkID: networkID(remoteAddress), NetworkHint: request.NetworkHint,
		Capabilities: slices.Clone(request.Capabilities), MaxConcurrency: request.MaxConcurrency,
		Runtime: request.Runtime, TokenHash: tokenHash, TokenExpiresAt: now.Add(service.config.ProbeTokenTTL),
		Status: "online", Health: "healthy", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return RegisterProbeResponse{}, err
	}
	return RegisterProbeResponse{
		ProbeID: probe.ID, NetworkID: probe.NetworkID, AccessToken: accessToken,
		TokenExpiresAt:           probe.TokenExpiresAt,
		HeartbeatIntervalSeconds: int(service.config.HeartbeatInterval / time.Second),
	}, nil
}

func (service *Service) authorizeRegistration(
	ctx context.Context,
	request RegisterProbeRequest,
	credential string,
	now time.Time,
) (RegistrationAuthorization, error) {
	if request.Kind == "container" {
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
	request ProbeHeartbeatRequest,
) (ProbeHeartbeatResponse, error) {
	if err := normalizeAndValidateHeartbeat(probe, &request); err != nil {
		return ProbeHeartbeatResponse{}, err
	}
	now := service.clock().UTC()
	if !service.runtimeCompatible(probe.Runtime) {
		request.Draining = true
	}
	updated, err := service.repository.HeartbeatProbe(ctx, probe.ID, request, now)
	if err != nil {
		return ProbeHeartbeatResponse{}, err
	}
	return ProbeHeartbeatResponse{
		ServerTime: now, Drain: updated.Draining,
		MinimumRuntimeVersion:  service.config.MinimumRuntimeVersion,
		MinimumBrowserRevision: service.config.MinimumBrowserRevision,
	}, nil
}

func normalizeAndValidateHeartbeat(probe Probe, request *ProbeHeartbeatRequest) error {
	if request.AvailableCapabilities == nil {
		request.AvailableCapabilities = slices.Clone(probe.Capabilities)
	}
	if request.AvailableSlots < 0 || request.AvailableSlots > probe.MaxConcurrency {
		return fmt.Errorf("%w: availableSlots is outside probe capacity", ErrInvalid)
	}
	if request.Health != "healthy" && request.Health != "degraded" {
		return fmt.Errorf("%w: unsupported probe health", ErrInvalid)
	}
	if request.Health == "healthy" && strings.TrimSpace(request.ReasonCode) != "" {
		return fmt.Errorf("%w: healthy probe cannot include reasonCode", ErrInvalid)
	}
	if len(request.ActiveAssignmentIDs)+request.AvailableSlots > probe.MaxConcurrency {
		return fmt.Errorf("%w: active assignments and available slots exceed probe capacity", ErrInvalid)
	}
	if err := validateActiveAssignments(request.ActiveAssignmentIDs); err != nil {
		return err
	}
	return validateAvailableCapabilities(probe.Capabilities, request.AvailableCapabilities)
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
		if _, supported := registered[capability]; !supported || !contracts.IsSupportedCapability(capability) {
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
) (ClaimAssignmentResponse, error) {
	now := service.clock().UTC()
	leaseToken, leaseHash, err := service.secret("lease_")
	if err != nil {
		return ClaimAssignmentResponse{}, err
	}
	assignment, err := service.repository.ClaimAssignment(
		ctx, probe.ID, leaseHash, now, now.Add(service.config.AssignmentLease),
		now.Add(-service.config.ProbeHeartbeatTTL),
	)
	if err != nil {
		return ClaimAssignmentResponse{}, err
	}
	return ClaimAssignmentResponse{
		AssignmentID: assignment.ID, LeaseToken: leaseToken, LeaseExpiresAt: assignment.LeaseExpiresAt,
		NotBefore: assignment.NotBefore, Deadline: assignment.Deadline, Task: assignment.Task,
	}, nil
}

func (service *Service) HeartbeatAssignment(
	ctx context.Context,
	probe Probe,
	assignmentID string,
	leaseToken string,
) (AssignmentHeartbeatResponse, error) {
	if strings.TrimSpace(assignmentID) == "" || strings.TrimSpace(leaseToken) == "" {
		return AssignmentHeartbeatResponse{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	leaseExpiresAt := now.Add(service.config.AssignmentLease)
	if err := service.repository.HeartbeatAssignment(
		ctx, assignmentID, probe.ID, sha256.Sum256([]byte(leaseToken)), now, leaseExpiresAt,
	); err != nil {
		return AssignmentHeartbeatResponse{}, err
	}
	return AssignmentHeartbeatResponse{LeaseExpiresAt: leaseExpiresAt}, nil
}

func (service *Service) CommitResult(
	ctx context.Context,
	probe Probe,
	assignmentID string,
	leaseToken string,
	result AssignmentResult,
) (ResultReceipt, error) {
	if result.SeatMap != nil {
		seatMap := *result.SeatMap
		if err := NormalizeSeatMapVersion(&seatMap, service.clock().UTC()); err != nil {
			return ResultReceipt{}, err
		}
		result.SeatMap = &seatMap
	}
	if err := validateResult(result); err != nil {
		return ResultReceipt{}, err
	}
	payload, err := service.marshal(result)
	if err != nil {
		return ResultReceipt{}, fmt.Errorf("encode assignment result: %w", err)
	}
	digest := sha256.Sum256(payload)
	return service.repository.CommitResult(ctx, ResultCommit{
		AssignmentID: assignmentID, ProbeID: probe.ID,
		LeaseHash: sha256.Sum256([]byte(leaseToken)), PayloadHash: hex.EncodeToString(digest[:]),
		Payload: payload, Result: result, CommittedAt: service.clock().UTC(),
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

func validateRegistration(request RegisterProbeRequest) error {
	if strings.TrimSpace(request.InstallationID) == "" {
		return fmt.Errorf("%w: installationId is required", ErrInvalid)
	}
	if request.Kind != "container" && request.Kind != "client" {
		return fmt.Errorf("%w: kind must be container or client", ErrInvalid)
	}
	if request.MaxConcurrency < 1 || request.MaxConcurrency > 32 {
		return fmt.Errorf("%w: maxConcurrency must be between 1 and 32", ErrInvalid)
	}
	if err := validateRuntime(request.Runtime); err != nil {
		return err
	}
	if len(request.Capabilities) == 0 {
		return fmt.Errorf("%w: at least one capability is required", ErrInvalid)
	}
	for _, capability := range request.Capabilities {
		if !contracts.IsSupportedCapability(strings.TrimSpace(capability)) {
			return fmt.Errorf("%w: unsupported capability", ErrInvalid)
		}
	}
	return nil
}

func validateRuntime(runtime Runtime) error {
	if runtime.Protocol != ProtocolVersion {
		return fmt.Errorf("%w: unsupported runtime protocol", ErrInvalid)
	}
	if strings.TrimSpace(runtime.Version) == "" || strings.TrimSpace(runtime.BrowserRevision) == "" ||
		strings.TrimSpace(runtime.Platform) == "" || strings.TrimSpace(runtime.Arch) == "" {
		return fmt.Errorf("%w: runtime is incomplete", ErrInvalid)
	}
	if !semver.IsValid(canonicalVersion(runtime.Version)) {
		return fmt.Errorf("%w: runtime version must use semantic versioning", ErrInvalid)
	}
	if !isNumericRevision(runtime.BrowserRevision) {
		return fmt.Errorf("%w: browser revision must be numeric", ErrInvalid)
	}
	return nil
}

func validateResult(result AssignmentResult) error {
	if strings.TrimSpace(result.RunID) == "" {
		return fmt.Errorf("%w: runId is required", ErrInvalid)
	}
	if result.Status != "completed" && result.Status != "partial" && result.Status != "failed" {
		return fmt.Errorf("%w: unsupported result status", ErrInvalid)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
		return fmt.Errorf("%w: invalid result time range", ErrInvalid)
	}
	if err := validateResultPayload(result); err != nil {
		return err
	}
	for _, capture := range result.Captures {
		if err := validateCapture(capture); err != nil {
			return err
		}
	}
	return nil
}

func validateResultPayload(result AssignmentResult) error {
	if result.Catalog != nil && result.SeatMap != nil {
		return fmt.Errorf("%w: assignment result cannot include catalog and seat map", ErrInvalid)
	}
	if result.Catalog != nil {
		if result.Status != "completed" || len(result.Captures) != 0 {
			return fmt.Errorf("%w: catalog result must be completed without schedule captures", ErrInvalid)
		}
		snapshot := *result.Catalog
		return NormalizeCatalogSnapshot(&snapshot)
	}
	if result.SeatMap != nil && (result.Status != "completed" || len(result.Captures) != 0) {
		return fmt.Errorf("%w: seat-map result must be completed without other payloads", ErrInvalid)
	}
	return nil
}

func validateCapture(capture Capture) error {
	if _, err := time.Parse(time.DateOnly, capture.TargetDate); err != nil || capture.ObservedAt.IsZero() {
		return fmt.Errorf("%w: invalid capture", ErrInvalid)
	}
	if capture.Complete && capture.ErrorCode != "" {
		return fmt.Errorf("%w: complete capture cannot include errorCode", ErrInvalid)
	}
	for _, showtime := range capture.Showtimes {
		if err := validateShowtime(showtime); err != nil {
			return err
		}
	}
	return nil
}

func validateShowtime(showtime Showtime) error {
	if !showtimeIdentityComplete(showtime) || !showtimeIdentityCanonical(showtime) ||
		showtime.StartsAt.IsZero() || !showtime.EndsAt.After(showtime.StartsAt) ||
		showtime.AvailableSeats < 0 || showtime.Capacity < showtime.AvailableSeats {
		return fmt.Errorf("%w: invalid showtime", ErrInvalid)
	}
	return nil
}

func showtimeIdentityComplete(showtime Showtime) bool {
	values := []string{
		showtime.ID, showtime.ProviderID, showtime.SourceKey, showtime.TheaterID,
		showtime.Movie.ID, showtime.Movie.SourceKey, showtime.Movie.Title,
		showtime.Auditorium.ID, showtime.Auditorium.SourceKey, showtime.Auditorium.Name,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return showtime.Movie.ProviderID == showtime.ProviderID &&
		showtime.Auditorium.TheaterID == showtime.TheaterID
}

func showtimeIdentityCanonical(showtime Showtime) bool {
	providerID := strings.TrimSpace(showtime.ProviderID)
	return showtime.ID == contracts.CatalogID(providerID, "showtime", showtime.SourceKey) &&
		showtime.Movie.ID == contracts.CatalogID(providerID, "movie", showtime.Movie.SourceKey) &&
		showtime.Auditorium.ID == contracts.CatalogID(providerID, "auditorium", showtime.Auditorium.SourceKey)
}

func networkID(remoteAddress string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.TrimSpace(remoteAddress)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(host)))
	return "net_" + hex.EncodeToString(digest[:8])
}

func (service *Service) runtimeCompatible(runtime Runtime) bool {
	if !semver.IsValid(canonicalVersion(runtime.Version)) || !isNumericRevision(runtime.BrowserRevision) {
		return false
	}
	if minimum := strings.TrimSpace(service.config.MinimumRuntimeVersion); minimum != "" &&
		semver.Compare(canonicalVersion(runtime.Version), canonicalVersion(minimum)) < 0 {
		return false
	}
	if minimum := strings.TrimSpace(service.config.MinimumBrowserRevision); minimum != "" &&
		compareNumericRevision(runtime.BrowserRevision, minimum) < 0 {
		return false
	}
	return true
}

func canonicalVersion(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

func isNumericRevision(value string) bool {
	_, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	return ok
}

func compareNumericRevision(left, right string) int {
	leftValue, _ := new(big.Int).SetString(strings.TrimSpace(left), 10)
	rightValue, _ := new(big.Int).SetString(strings.TrimSpace(right), 10)
	return leftValue.Cmp(rightValue)
}
