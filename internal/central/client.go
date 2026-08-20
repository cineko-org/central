package central

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cineko-org/central/internal/domain/clientresources"
	contracts "github.com/cineko-org/contracts/v3"
)

const (
	DefaultClientSessionTTL = 12 * time.Hour
	DefaultClientRefreshTTL = 30 * 24 * time.Hour
	DefaultEventPageSize    = 100
	MaximumEventPageSize    = 500
)

var ErrRevisionConflict = errors.New("revision conflict")

type ClientCredentialSeed struct {
	UserID      string
	DisplayName string
	AccessToken string
}

type ClientUser = contracts.ClientUser

type ClientPrincipal struct {
	UserID    string
	SessionID string
}

type ClientSession struct {
	ID               string
	UserID           string
	TokenHash        [32]byte
	ExpiresAt        time.Time
	RefreshTokenHash [32]byte
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
}

type AuthExchangeRequest = contracts.AuthExchangeRequest

type AuthExchangeResponse = contracts.AuthExchangeResponse

type AuthRefreshRequest = contracts.AuthRefreshRequest

type ClientDevice = contracts.ClientDevice

type ClientResource struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	UserID    string          `json:"-"`
	Revision  int64           `json:"revision"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type ClientEvent = contracts.ClientEvent

type EventResource = contracts.EventResource

type EventStreamControl = contracts.EventStreamControl

type ClientEventPage struct {
	Events            []ClientEvent
	PrunedThrough     int64
	Latest            int64
	ReleaseGeneration int64
}

type ClientBootstrap = contracts.ClientBootstrap

type ResourceMutation struct {
	UserID           string
	Kind             string
	ID               string
	Data             json.RawMessage
	ExpectedRevision *int64
	CommandID        string
	Now              time.Time
}

type ClientRepository interface {
	ProvisionClientCredential(context.Context, ClientUser, [32]byte) error
	ExchangeClientCredential(context.Context, string, [32]byte, time.Time) (ClientUser, error)
	CreateClientSession(context.Context, ClientSession) error
	RotateClientSession(context.Context, [32]byte, ClientSession, time.Time) (ClientUser, error)
	RevokeClientSession(context.Context, string, time.Time) error
	CreateLaunchTicket(context.Context, LaunchTicket) error
	ExchangeLaunchTicket(context.Context, [32]byte, string, int64, ClientSession, time.Time) (LaunchedClient, error)
	AuthenticateClientSession(context.Context, [32]byte, time.Time) (ClientPrincipal, error)
	UpsertClientDevice(context.Context, ClientDevice) (ClientDevice, error)
	GetClientDevice(context.Context, string, string) (ClientDevice, error)
	GetClientUser(context.Context, string) (ClientUser, error)
	ClientResourceRevisions(context.Context, string) (map[string]int64, error)
	ListClientResources(context.Context, string, string) ([]ClientResource, error)
	GetClientResource(context.Context, string, string, string) (ClientResource, error)
	PutClientResource(context.Context, ResourceMutation) (ClientResource, error)
	DeleteClientResource(context.Context, ResourceMutation) (ClientResource, error)
	ListClientEvents(context.Context, string, int64, int) ([]ClientEvent, error)
	ClaimClientExecution(context.Context, ExecutionClaim) (ExecutionCommand, error)
	HeartbeatClientExecution(context.Context, string, string, [32]byte, time.Time, time.Time) error
	CompleteClientExecution(context.Context, ExecutionCompletion) error
}

type ClientService struct {
	repository      ClientRepository
	eventRepository interface {
		ClientEventPage(context.Context, string, int64, int) (ClientEventPage, error)
		WaitClientEvents(context.Context, string, int64, int64) error
	}
	releaseRepository ReleaseRepository
	sessionTTL        time.Duration
	refreshTTL        time.Duration
	clock             func() time.Time
	random            func([]byte) (int, error)
	releaseMu         sync.RWMutex
	releasePublishMu  sync.Mutex
	releaseGeneration atomic.Int64
	clients           map[string]ClientRelease
	browsers          map[string]BrowserRelease
	playwright        map[string]PlaywrightRelease
	launchers         map[string]LauncherRelease
	probes            map[string]ProbeRelease
}

func NewClientService(
	repository ClientRepository,
	sessionTTL time.Duration,
	refreshTTL ...time.Duration,
) (*ClientService, error) {
	if repository == nil {
		return nil, errors.New("client repository is required")
	}
	if sessionTTL == 0 {
		sessionTTL = DefaultClientSessionTTL
	}
	if sessionTTL <= 0 {
		return nil, errors.New("client session TTL must be positive")
	}
	selectedRefreshTTL := DefaultClientRefreshTTL
	if len(refreshTTL) > 0 {
		selectedRefreshTTL = refreshTTL[0]
	}
	if selectedRefreshTTL <= sessionTTL {
		return nil, errors.New("client refresh TTL must be greater than session TTL")
	}
	service := &ClientService{
		repository: repository, sessionTTL: sessionTTL, refreshTTL: selectedRefreshTTL,
		clock: time.Now, random: rand.Read,
		clients: make(map[string]ClientRelease), browsers: make(map[string]BrowserRelease),
		playwright: make(map[string]PlaywrightRelease), launchers: make(map[string]LauncherRelease),
		probes: make(map[string]ProbeRelease),
	}
	service.eventRepository, _ = repository.(interface {
		ClientEventPage(context.Context, string, int64, int) (ClientEventPage, error)
		WaitClientEvents(context.Context, string, int64, int64) error
	})
	service.releaseRepository, _ = repository.(ReleaseRepository)
	return service, nil
}

func (service *ClientService) Provision(ctx context.Context, seeds []ClientCredentialSeed) error {
	if len(seeds) == 0 {
		return nil
	}
	now := service.clock().UTC()
	seen := make(map[string]struct{}, len(seeds))
	for _, seed := range seeds {
		seed.UserID = strings.TrimSpace(seed.UserID)
		seed.DisplayName = strings.TrimSpace(seed.DisplayName)
		seed.AccessToken = strings.TrimSpace(seed.AccessToken)
		if seed.UserID == "" || seed.DisplayName == "" || len(seed.AccessToken) < 32 {
			return errors.New("client credentials require userId, displayName, and a token of at least 32 characters")
		}
		if _, duplicate := seen[seed.UserID]; duplicate {
			return fmt.Errorf("duplicate client credential user %q", seed.UserID)
		}
		seen[seed.UserID] = struct{}{}
		if err := service.repository.ProvisionClientCredential(ctx, ClientUser{
			ID: seed.UserID, DisplayName: seed.DisplayName, CreatedAt: now, UpdatedAt: now,
		}, sha256.Sum256([]byte(seed.AccessToken))); err != nil {
			return fmt.Errorf("provision client credential for %s: %w", seed.UserID, err)
		}
	}
	return nil
}

func (service *ClientService) Exchange(
	ctx context.Context,
	request AuthExchangeRequest,
) (AuthExchangeResponse, error) {
	request.UserID = strings.TrimSpace(request.UserID)
	request.AccessToken = strings.TrimSpace(request.AccessToken)
	if request.UserID == "" || request.AccessToken == "" {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	user, err := service.repository.ExchangeClientCredential(
		ctx, request.UserID, sha256.Sum256([]byte(request.AccessToken)), now,
	)
	if err != nil {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	response, session, err := service.issueSession(user, now)
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	if err := service.repository.CreateClientSession(ctx, session); err != nil {
		return AuthExchangeResponse{}, err
	}
	return response, nil
}

func (service *ClientService) Refresh(
	ctx context.Context,
	request AuthRefreshRequest,
) (AuthExchangeResponse, error) {
	request.RefreshToken = strings.TrimSpace(request.RefreshToken)
	if request.RefreshToken == "" {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	response, session, err := service.issueSession(ClientUser{}, now)
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	user, err := service.repository.RotateClientSession(
		ctx, sha256.Sum256([]byte(request.RefreshToken)), session, now,
	)
	if err != nil {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	response.User = user
	return response, nil
}

func (service *ClientService) Logout(ctx context.Context, principal ClientPrincipal) error {
	if strings.TrimSpace(principal.SessionID) == "" {
		return ErrUnauthorized
	}
	return service.repository.RevokeClientSession(ctx, principal.SessionID, service.clock().UTC())
}

func (service *ClientService) issueSession(
	user ClientUser,
	now time.Time,
) (AuthExchangeResponse, ClientSession, error) {
	accessToken, accessHash, err := service.secret("ccs_")
	if err != nil {
		return AuthExchangeResponse{}, ClientSession{}, err
	}
	refreshToken, refreshHash, err := service.secret("ccr_")
	if err != nil {
		return AuthExchangeResponse{}, ClientSession{}, err
	}
	sessionID, _, err := service.secret("session_")
	if err != nil {
		return AuthExchangeResponse{}, ClientSession{}, err
	}
	session := ClientSession{
		ID: sessionID, UserID: user.ID, TokenHash: accessHash, ExpiresAt: now.Add(service.sessionTTL),
		RefreshTokenHash: refreshHash, RefreshExpiresAt: now.Add(service.refreshTTL), CreatedAt: now,
	}
	return AuthExchangeResponse{
		AccessToken: accessToken, ExpiresAt: session.ExpiresAt,
		RefreshToken: refreshToken, RefreshExpiresAt: session.RefreshExpiresAt, User: user,
	}, session, nil
}

func (service *ClientService) Authenticate(
	ctx context.Context,
	accessToken string,
) (ClientPrincipal, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return ClientPrincipal{}, ErrUnauthorized
	}
	return service.repository.AuthenticateClientSession(
		ctx, sha256.Sum256([]byte(accessToken)), service.clock().UTC(),
	)
}

func (service *ClientService) Bootstrap(
	ctx context.Context,
	principal ClientPrincipal,
	installationID string,
) (ClientBootstrap, error) {
	user, err := service.repository.GetClientUser(ctx, principal.UserID)
	if err != nil {
		return ClientBootstrap{}, err
	}
	revisions, err := service.repository.ClientResourceRevisions(ctx, principal.UserID)
	if err != nil {
		return ClientBootstrap{}, err
	}
	eventCursor := int64(0)
	if service.eventRepository != nil {
		eventPage, eventErr := service.eventRepository.ClientEventPage(ctx, principal.UserID, 0, 1)
		if eventErr != nil {
			return ClientBootstrap{}, eventErr
		}
		eventCursor = eventPage.Latest
	}
	bootstrap := ClientBootstrap{
		User: user, Protocol: ProtocolVersion, EventCursor: eventCursor, Revisions: revisions,
		Features: map[string]bool{"embeddedProbe": true, "eventStream": true, "centralState": true},
	}
	if installationID != "" {
		device, deviceErr := service.repository.GetClientDevice(ctx, principal.UserID, installationID)
		if deviceErr == nil {
			bootstrap.Device = &device
		} else if !errors.Is(deviceErr, ErrNotFound) {
			return ClientBootstrap{}, deviceErr
		}
	}
	return bootstrap, nil
}

func (service *ClientService) UpsertDevice(
	ctx context.Context,
	principal ClientPrincipal,
	device ClientDevice,
) (ClientDevice, error) {
	device.InstallationID = strings.TrimSpace(device.InstallationID)
	device.DeviceID = strings.TrimSpace(device.DeviceID)
	device.Platform = strings.TrimSpace(device.Platform)
	device.Arch = strings.TrimSpace(device.Arch)
	device.AppVersion = strings.TrimSpace(device.AppVersion)
	if device.InstallationID == "" || device.DeviceID == "" || device.Platform == "" ||
		device.Arch == "" || device.AppVersion == "" {
		return ClientDevice{}, fmt.Errorf("%w: client device is incomplete", ErrInvalid)
	}
	now := service.clock().UTC()
	device.UserID, device.LastSeenAt, device.UpdatedAt = principal.UserID, now, now
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	return service.repository.UpsertClientDevice(ctx, device)
}

func (service *ClientService) ListResources(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
) ([]ClientResource, error) {
	if !validClientResourceKind(kind) {
		return nil, fmt.Errorf("%w: unsupported client resource", ErrInvalid)
	}
	resources, err := service.repository.ListClientResources(ctx, principal.UserID, kind)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		if err := validateStoredClientResource(principal.UserID, kind, "", resource); err != nil {
			return nil, err
		}
	}
	return resources, nil
}

func (service *ClientService) GetResource(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
	id string,
) (ClientResource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" {
		return ClientResource{}, fmt.Errorf("%w: invalid client resource identity", ErrInvalid)
	}
	resource, err := service.repository.GetClientResource(ctx, principal.UserID, kind, id)
	if err != nil {
		return ClientResource{}, err
	}
	if err := validateStoredClientResource(principal.UserID, kind, id, resource); err != nil {
		return ClientResource{}, err
	}
	return resource, nil
}

func (service *ClientService) PutResource(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
	id string,
	data json.RawMessage,
	expectedRevision *int64,
	commandID string,
) (ClientResource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" ||
		strings.TrimSpace(commandID) == "" || !json.Valid(data) {
		return ClientResource{}, fmt.Errorf("%w: invalid client resource mutation", ErrInvalid)
	}
	if expectedRevision != nil && *expectedRevision < 1 {
		return ClientResource{}, fmt.Errorf("%w: revision must be positive", ErrInvalid)
	}
	if err := clientresources.ValidatePayload(principal.UserID, kind, id, data); err != nil {
		return ClientResource{}, fmt.Errorf("%w: invalid %s payload: %w", ErrInvalid, kind, err)
	}
	return service.repository.PutClientResource(ctx, ResourceMutation{
		UserID: principal.UserID, Kind: kind, ID: id, Data: data,
		ExpectedRevision: expectedRevision, CommandID: commandID, Now: service.clock().UTC(),
	})
}

func validateStoredClientResource(userID string, kind string, id string, resource ClientResource) error {
	if resource.UserID != userID || resource.Kind != kind || (id != "" && resource.ID != id) {
		return fmt.Errorf("%w: resource storage identity is inconsistent", ErrCorruptResource)
	}
	if err := clientresources.ValidatePayload(resource.UserID, resource.Kind, resource.ID, resource.Data); err != nil {
		return fmt.Errorf("%w: %s/%s: %w", ErrCorruptResource, resource.Kind, resource.ID, err)
	}
	return nil
}

func (service *ClientService) DeleteResource(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
	id string,
	expectedRevision *int64,
	commandID string,
) (ClientResource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" || strings.TrimSpace(commandID) == "" {
		return ClientResource{}, fmt.Errorf("%w: invalid client resource deletion", ErrInvalid)
	}
	if expectedRevision == nil || *expectedRevision < 1 {
		return ClientResource{}, fmt.Errorf("%w: deletion requires a positive revision", ErrInvalid)
	}
	return service.repository.DeleteClientResource(ctx, ResourceMutation{
		UserID: principal.UserID, Kind: kind, ID: id,
		ExpectedRevision: expectedRevision, CommandID: commandID, Now: service.clock().UTC(),
	})
}

func (service *ClientService) Events(
	ctx context.Context,
	principal ClientPrincipal,
	after int64,
	limit int,
) ([]ClientEvent, error) {
	page, err := service.EventPage(ctx, principal, after, limit)
	return page.Events, err
}

func (service *ClientService) EventPage(
	ctx context.Context,
	principal ClientPrincipal,
	after int64,
	limit int,
) (ClientEventPage, error) {
	if after < 0 {
		return ClientEventPage{}, fmt.Errorf("%w: event cursor must not be negative", ErrInvalid)
	}
	if limit == 0 {
		limit = DefaultEventPageSize
	}
	if limit < 1 || limit > MaximumEventPageSize {
		return ClientEventPage{}, fmt.Errorf("%w: event page size is invalid", ErrInvalid)
	}
	if service.eventRepository != nil {
		return service.eventRepository.ClientEventPage(ctx, principal.UserID, after, limit)
	}
	events, err := service.repository.ListClientEvents(ctx, principal.UserID, after, limit)
	if err != nil {
		return ClientEventPage{}, err
	}
	latest := after
	if len(events) > 0 {
		latest = events[len(events)-1].Sequence
	}
	return ClientEventPage{Events: events, Latest: latest, ReleaseGeneration: service.ReleaseGeneration()}, nil
}

func (service *ClientService) WaitEvents(
	ctx context.Context,
	principal ClientPrincipal,
	after int64,
	releaseGeneration int64,
) error {
	if after < 0 || releaseGeneration < 1 {
		return fmt.Errorf("%w: invalid event wait cursor", ErrInvalid)
	}
	if service.eventRepository != nil {
		return service.eventRepository.WaitClientEvents(ctx, principal.UserID, after, releaseGeneration)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (service *ClientService) secret(prefix string) (string, [32]byte, error) {
	buffer := make([]byte, 32)
	if _, err := service.random(buffer); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate client secret: %w", err)
	}
	value := prefix + base64.RawURLEncoding.EncodeToString(buffer)
	return value, sha256.Sum256([]byte(value)), nil
}

func validClientResourceKind(kind string) bool {
	switch kind {
	case "settings", "presets", "monitors", "reservations", "external-operations", "app-events":
		return true
	default:
		return false
	}
}
