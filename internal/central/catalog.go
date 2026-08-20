package central

import (
	"context"
	"fmt"
	"strings"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	contracts "github.com/cineko-org/contracts/v3"
)

type CatalogRepository interface {
	AuthorizeCatalogWrite(context.Context, string, string, string) error
	Catalog(context.Context) (contracts.CatalogIndex, error)
	CatalogRefreshStatus(context.Context, time.Time, time.Time) (CatalogRefreshStatus, error)
	RequestCatalogRefresh(context.Context, time.Time) error
	UpsertCatalogSnapshot(context.Context, contracts.CatalogSnapshot) (int64, error)
	PutSeatMapVersion(context.Context, contracts.SeatMapVersion) (int64, error)
	SeatMapVersion(context.Context, string) (contracts.SeatMapVersion, error)
	RequestSeatMapBackfill(context.Context, string, time.Time) error
}

func (service *CatalogService) AuthorizeClientWrite(
	ctx context.Context,
	principal ClientPrincipal,
	installationID string,
	capability string,
) error {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return ErrUnauthorized
	}
	return service.repository.AuthorizeCatalogWrite(
		ctx, principal.UserID, installationID, capability,
	)
}

type CatalogRefreshStatus struct {
	State           string     `json:"state"`
	CatalogEmpty    bool       `json:"catalogEmpty"`
	RequestedAt     *time.Time `json:"requestedAt,omitempty"`
	Active          bool       `json:"active"`
	EligibleProbes  int        `json:"eligibleProbes"`
	LastStatus      string     `json:"lastStatus,omitempty"`
	LastAttemptedAt *time.Time `json:"lastAttemptedAt,omitempty"`
}

type CatalogService struct {
	repository CatalogRepository
	clock      func() time.Time
}

func NewCatalogService(repository CatalogRepository) (*CatalogService, error) {
	if repository == nil {
		return nil, fmt.Errorf("catalog repository is required")
	}
	return &CatalogService{repository: repository, clock: time.Now}, nil
}

func (service *CatalogService) Catalog(ctx context.Context) (contracts.CatalogIndex, error) {
	return service.repository.Catalog(ctx)
}

func (service *CatalogService) RefreshStatus(ctx context.Context) (CatalogRefreshStatus, error) {
	now := service.clock().UTC()
	status, err := service.repository.CatalogRefreshStatus(
		ctx, now, now.Add(-DefaultProbeHeartbeatTTL),
	)
	if err != nil {
		return CatalogRefreshStatus{}, err
	}
	switch {
	case status.Active:
		status.State = "running"
	case !status.CatalogEmpty && status.RequestedAt == nil:
		status.State = "ready"
	case status.EligibleProbes == 0:
		status.State = "waiting_for_probe"
	default:
		status.State = "queued"
	}
	return status, nil
}

func (service *CatalogService) RequestRefresh(ctx context.Context) error {
	return service.repository.RequestCatalogRefresh(ctx, service.clock().UTC())
}

func (service *CatalogService) PutSnapshot(
	ctx context.Context,
	snapshot contracts.CatalogSnapshot,
) (int64, error) {
	now := service.clock().UTC()
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = now
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if err := catalogdomain.ValidateObservationTime(snapshot.ObservedAt, now); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := catalogdomain.NormalizeSnapshot(&snapshot); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return service.repository.UpsertCatalogSnapshot(ctx, snapshot)
}

func (service *CatalogService) PutSeatMapVersion(
	ctx context.Context,
	version contracts.SeatMapVersion,
) (int64, error) {
	now := service.clock().UTC()
	if err := catalogdomain.NormalizeSeatMapVersion(&version, now); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return service.repository.PutSeatMapVersion(ctx, version)
}

func (service *CatalogService) SeatMapVersion(
	ctx context.Context,
	auditoriumID string,
) (contracts.SeatMapVersion, error) {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return contracts.SeatMapVersion{}, fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	return service.repository.SeatMapVersion(ctx, auditoriumID)
}

func (service *CatalogService) RequestSeatMapBackfill(ctx context.Context, auditoriumID string) error {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	return service.repository.RequestSeatMapBackfill(ctx, auditoriumID, service.clock().UTC())
}
