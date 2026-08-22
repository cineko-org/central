package central

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cineko-org/central/internal/domain/clientresources"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/types/known/timestamppb"
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

type ClientEventBatch struct {
	Events            []*clientpb.ClientEvent
	PrunedThrough     int64
	Latest            int64
	ReleaseGeneration int64
}

type ResourceMutation struct {
	UserID           string
	Kind             string
	ID               string
	Resource         *clientpb.Resource
	ExpectedRevision *int64
	CommandID        string
	Now              time.Time
}

type ClientRepository interface {
	ProvisionClientCredential(context.Context, *clientpb.User, [32]byte) error
	ExchangeClientCredential(context.Context, string, [32]byte, time.Time) (*clientpb.User, error)
	CreateClientSession(context.Context, ClientSession) error
	RotateClientSession(context.Context, [32]byte, ClientSession, time.Time) (*clientpb.User, error)
	RevokeClientSession(context.Context, string, time.Time) error
	CreateLaunchTicket(context.Context, LaunchTicket) error
	ExchangeLaunchTicket(context.Context, [32]byte, string, int64, ClientSession, time.Time) (LaunchedClient, error)
	AuthenticateClientSession(context.Context, [32]byte, time.Time) (ClientPrincipal, error)
	UpsertClientDevice(context.Context, *clientpb.Device) (*clientpb.Device, error)
	GetClientDevice(context.Context, string, string) (*clientpb.Device, error)
	GetClientUser(context.Context, string) (*clientpb.User, error)
	ClientResourceRevisions(context.Context, string) (map[string]int64, error)
	ListClientResources(context.Context, string, string) ([]*clientpb.Resource, error)
	GetClientResource(context.Context, string, string, string) (*clientpb.Resource, error)
	PutClientResource(context.Context, ResourceMutation) (*clientpb.Resource, error)
	DeleteClientResource(context.Context, ResourceMutation) (*clientpb.Resource, error)
	ListClientEvents(context.Context, string, int64, int) ([]*clientpb.ClientEvent, error)
	ClaimClientExecution(context.Context, ExecutionClaim) (*executionpb.Command, error)
	HeartbeatClientExecution(context.Context, string, string, [32]byte, time.Time, time.Time) error
	CompleteClientExecution(context.Context, ExecutionCompletion) error
}

type ClientService struct {
	repository      ClientRepository
	eventRepository interface {
		ClientEventPage(context.Context, string, int64, int) (ClientEventBatch, error)
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
	clients           map[string]*releasepb.ClientRelease
	browsers          map[string]*releasepb.BrowserRelease
	playwright        map[string]*releasepb.PlaywrightRelease
	launchers         map[string]*releasepb.LauncherRelease
	probes            map[string]*releasepb.ProbeRelease
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
		clients: make(map[string]*releasepb.ClientRelease), browsers: make(map[string]*releasepb.BrowserRelease),
		playwright: make(map[string]*releasepb.PlaywrightRelease), launchers: make(map[string]*releasepb.LauncherRelease),
		probes: make(map[string]*releasepb.ProbeRelease),
	}
	service.eventRepository, _ = repository.(interface {
		ClientEventPage(context.Context, string, int64, int) (ClientEventBatch, error)
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
		user := &clientpb.User{}
		user.SetId(seed.UserID)
		user.SetDisplayName(seed.DisplayName)
		user.SetCreatedAt(timestamppb.New(now))
		user.SetUpdatedAt(timestamppb.New(now))
		if err := service.repository.ProvisionClientCredential(ctx, user, sha256.Sum256([]byte(seed.AccessToken))); err != nil {
			return fmt.Errorf("provision client credential for %s: %w", seed.UserID, err)
		}
	}
	return nil
}

func (service *ClientService) Exchange(
	ctx context.Context,
	request *clientpb.TokenExchangeRequest,
) (*clientpb.AuthenticationResponse, error) {
	userID := strings.TrimSpace(request.GetUserId())
	accessToken := strings.TrimSpace(request.GetAccessToken())
	if userID == "" || accessToken == "" {
		return nil, ErrUnauthorized
	}
	now := service.clock().UTC()
	user, err := service.repository.ExchangeClientCredential(
		ctx, userID, sha256.Sum256([]byte(accessToken)), now,
	)
	if err != nil {
		return nil, ErrUnauthorized
	}
	response, session, err := service.issueSession(user, now)
	if err != nil {
		return nil, err
	}
	if err := service.repository.CreateClientSession(ctx, session); err != nil {
		return nil, err
	}
	return response, nil
}

func (service *ClientService) Refresh(
	ctx context.Context,
	request *clientpb.TokenRefreshRequest,
) (*clientpb.AuthenticationResponse, error) {
	refreshToken := strings.TrimSpace(request.GetRefreshToken())
	if refreshToken == "" {
		return nil, ErrUnauthorized
	}
	now := service.clock().UTC()
	response, session, err := service.issueSession(&clientpb.User{}, now)
	if err != nil {
		return nil, err
	}
	user, err := service.repository.RotateClientSession(
		ctx, sha256.Sum256([]byte(refreshToken)), session, now,
	)
	if err != nil {
		return nil, ErrUnauthorized
	}
	response.SetUser(user)
	return response, nil
}

func (service *ClientService) Logout(ctx context.Context, principal ClientPrincipal) error {
	if strings.TrimSpace(principal.SessionID) == "" {
		return ErrUnauthorized
	}
	return service.repository.RevokeClientSession(ctx, principal.SessionID, service.clock().UTC())
}

func (service *ClientService) issueSession(
	user *clientpb.User,
	now time.Time,
) (*clientpb.AuthenticationResponse, ClientSession, error) {
	accessToken, accessHash, err := service.secret("ccs_")
	if err != nil {
		return nil, ClientSession{}, err
	}
	refreshToken, refreshHash, err := service.secret("ccr_")
	if err != nil {
		return nil, ClientSession{}, err
	}
	sessionID, _, err := service.secret("session_")
	if err != nil {
		return nil, ClientSession{}, err
	}
	session := ClientSession{
		ID: sessionID, UserID: user.GetId(), TokenHash: accessHash, ExpiresAt: now.Add(service.sessionTTL),
		RefreshTokenHash: refreshHash, RefreshExpiresAt: now.Add(service.refreshTTL), CreatedAt: now,
	}
	response := &clientpb.AuthenticationResponse{}
	response.SetAccessToken(accessToken)
	response.SetExpiresAt(timestamppb.New(session.ExpiresAt))
	response.SetRefreshToken(refreshToken)
	response.SetRefreshExpiresAt(timestamppb.New(session.RefreshExpiresAt))
	response.SetUser(user)
	return response, session, nil
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
) (*clientpb.Bootstrap, error) {
	user, err := service.repository.GetClientUser(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	revisions, err := service.repository.ClientResourceRevisions(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	eventCursor := int64(0)
	if service.eventRepository != nil {
		eventPage, eventErr := service.eventRepository.ClientEventPage(ctx, principal.UserID, 0, 1)
		if eventErr != nil {
			return nil, eventErr
		}
		eventCursor = eventPage.Latest
	}
	bootstrap := &clientpb.Bootstrap{}
	bootstrap.SetUser(user)
	bootstrap.SetEventCursor(eventCursor)
	bootstrap.SetRevisions(revisions)
	bootstrap.SetFeatures(map[string]bool{"embeddedProbe": true, "eventStream": true, "centralState": true})
	if installationID != "" {
		device, deviceErr := service.repository.GetClientDevice(ctx, principal.UserID, installationID)
		if deviceErr == nil {
			bootstrap.SetDevice(device)
		} else if !errors.Is(deviceErr, ErrNotFound) {
			return nil, deviceErr
		}
	}
	return bootstrap, nil
}

func (service *ClientService) UpsertDevice(
	ctx context.Context,
	principal ClientPrincipal,
	device *clientpb.Device,
) (*clientpb.Device, error) {
	device.SetInstallationId(strings.TrimSpace(device.GetInstallationId()))
	device.SetDeviceId(strings.TrimSpace(device.GetDeviceId()))
	device.SetPlatform(strings.TrimSpace(device.GetPlatform()))
	device.SetArchitecture(strings.TrimSpace(device.GetArchitecture()))
	device.SetAppVersion(strings.TrimSpace(device.GetAppVersion()))
	if device.GetInstallationId() == "" || device.GetDeviceId() == "" || device.GetPlatform() == "" ||
		device.GetArchitecture() == "" || device.GetAppVersion() == "" {
		return nil, fmt.Errorf("%w: client device is incomplete", ErrInvalid)
	}
	now := service.clock().UTC()
	device.SetUserId(principal.UserID)
	device.SetLastSeenAt(timestamppb.New(now))
	device.SetUpdatedAt(timestamppb.New(now))
	if device.GetCreatedAt() == nil {
		device.SetCreatedAt(timestamppb.New(now))
	}
	return service.repository.UpsertClientDevice(ctx, device)
}

func (service *ClientService) ListResources(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
) ([]*clientpb.Resource, error) {
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
) (*clientpb.Resource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: invalid client resource identity", ErrInvalid)
	}
	resource, err := service.repository.GetClientResource(ctx, principal.UserID, kind, id)
	if err != nil {
		return nil, err
	}
	if err := validateStoredClientResource(principal.UserID, kind, id, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func (service *ClientService) PutResource(
	ctx context.Context,
	principal ClientPrincipal,
	kind string,
	id string,
	resource *clientpb.Resource,
	expectedRevision *int64,
	commandID string,
) (*clientpb.Resource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" ||
		strings.TrimSpace(commandID) == "" || resource == nil || resource.GetIdentity() == nil {
		return nil, fmt.Errorf("%w: invalid client resource mutation", ErrInvalid)
	}
	if expectedRevision != nil && *expectedRevision < 1 {
		return nil, fmt.Errorf("%w: revision must be positive", ErrInvalid)
	}
	if clientresources.Kind(resource) != kind || resource.GetIdentity().GetId() != id {
		return nil, fmt.Errorf("%w: resource identity does not match the request", ErrInvalid)
	}
	if err := clientresources.Validate(principal.UserID, kind, id, resource); err != nil {
		return nil, fmt.Errorf("%w: invalid %s payload: %w", ErrInvalid, kind, err)
	}
	return service.repository.PutClientResource(ctx, ResourceMutation{
		UserID: principal.UserID, Kind: kind, ID: id, Resource: resource,
		ExpectedRevision: expectedRevision, CommandID: commandID, Now: service.clock().UTC(),
	})
}

func validateStoredClientResource(userID string, kind string, id string, resource *clientpb.Resource) error {
	if resource == nil || resource.GetIdentity() == nil || clientresources.Kind(resource) != kind ||
		(id != "" && resource.GetIdentity().GetId() != id) {
		return fmt.Errorf("%w: resource storage identity is inconsistent", ErrCorruptResource)
	}
	if err := clientresources.Validate(userID, kind, resource.GetIdentity().GetId(), resource); err != nil {
		return fmt.Errorf("%w: %s/%s: %w", ErrCorruptResource, kind, resource.GetIdentity().GetId(), err)
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
) (*clientpb.Resource, error) {
	if !validClientResourceKind(kind) || strings.TrimSpace(id) == "" || strings.TrimSpace(commandID) == "" {
		return nil, fmt.Errorf("%w: invalid client resource deletion", ErrInvalid)
	}
	if expectedRevision == nil || *expectedRevision < 1 {
		return nil, fmt.Errorf("%w: deletion requires a positive revision", ErrInvalid)
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
) ([]*clientpb.ClientEvent, error) {
	page, err := service.EventPage(ctx, principal, after, limit)
	return page.Events, err
}

func (service *ClientService) EventPage(
	ctx context.Context,
	principal ClientPrincipal,
	after int64,
	limit int,
) (ClientEventBatch, error) {
	if after < 0 {
		return ClientEventBatch{}, fmt.Errorf("%w: event cursor must not be negative", ErrInvalid)
	}
	if limit == 0 {
		limit = DefaultEventPageSize
	}
	if limit < 1 || limit > MaximumEventPageSize {
		return ClientEventBatch{}, fmt.Errorf("%w: event page size is invalid", ErrInvalid)
	}
	if service.eventRepository != nil {
		return service.eventRepository.ClientEventPage(ctx, principal.UserID, after, limit)
	}
	events, err := service.repository.ListClientEvents(ctx, principal.UserID, after, limit)
	if err != nil {
		return ClientEventBatch{}, err
	}
	latest := after
	if len(events) > 0 {
		latest = events[len(events)-1].GetSequence()
	}
	return ClientEventBatch{Events: events, Latest: latest, ReleaseGeneration: service.ReleaseGeneration()}, nil
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
