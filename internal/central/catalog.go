package central

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CatalogRepository interface {
	AuthorizeCatalogWrite(context.Context, string, string, string) error
	Catalog(context.Context) (*catalogpb.CatalogIndex, error)
	CatalogRefreshStatus(context.Context, time.Time, time.Time) (*adminpb.CatalogRefreshStatus, error)
	RequestCatalogRefresh(context.Context, time.Time) error
	UpsertCatalogSnapshot(context.Context, *catalogpb.CatalogSnapshot) (int64, error)
	PutSeatMap(context.Context, *seatmappb.Snapshot) (int64, error)
	SeatMap(context.Context, string) (*seatmappb.Snapshot, error)
	RequestSeatMapBackfill(context.Context, string, time.Time) error
}

func (service *CatalogService) AuthorizeClientWrite(ctx context.Context, principal ClientPrincipal, installationID, capability string) error {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return ErrUnauthorized
	}
	return service.repository.AuthorizeCatalogWrite(ctx, principal.UserID, installationID, capability)
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

func (service *CatalogService) Catalog(ctx context.Context) (*catalogpb.CatalogIndex, error) {
	return service.repository.Catalog(ctx)
}

func (service *CatalogService) RefreshStatus(ctx context.Context) (*adminpb.CatalogRefreshStatus, error) {
	now := service.clock().UTC()
	status, err := service.repository.CatalogRefreshStatus(ctx, now, now.Add(-DefaultProbeHeartbeatTTL))
	if err != nil {
		return nil, err
	}
	switch {
	case status.GetActive():
		status.SetRunning(&adminpb.CatalogRefreshRunning{})
	case !status.GetCatalogEmpty() && status.GetRequestedAt() == nil:
		status.SetReady(&adminpb.CatalogRefreshReady{})
	case status.GetEligibleProbes() == 0:
		status.SetWaitingForProbe(&adminpb.CatalogRefreshWaitingForProbe{})
	default:
		status.SetQueued(&adminpb.CatalogRefreshQueued{})
	}
	return status, nil
}

func (service *CatalogService) RequestRefresh(ctx context.Context) error {
	return service.repository.RequestCatalogRefresh(ctx, service.clock().UTC())
}

func (service *CatalogService) PutSnapshot(ctx context.Context, snapshot *catalogpb.CatalogSnapshot) (int64, error) {
	now := service.clock().UTC()
	if snapshot == nil {
		return 0, fmt.Errorf("%w: catalog snapshot is required", ErrInvalid)
	}
	if snapshot.GetObservedAt() == nil {
		snapshot.SetObservedAt(timestamppb.New(now))
	}
	if err := snapshot.GetObservedAt().CheckValid(); err != nil {
		return 0, fmt.Errorf("%w: catalog observation time is invalid", ErrInvalid)
	}
	if err := catalogdomain.ValidateObservationTime(snapshot.GetObservedAt().AsTime(), now); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := catalogdomain.NormalizeSnapshot(snapshot); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return service.repository.UpsertCatalogSnapshot(ctx, snapshot)
}

func (service *CatalogService) PutSeatMap(ctx context.Context, snapshot *seatmappb.Snapshot) (int64, error) {
	if err := catalogdomain.NormalizeSeatMap(snapshot, service.clock().UTC()); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return service.repository.PutSeatMap(ctx, snapshot)
}

func (service *CatalogService) SeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Snapshot, error) {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return nil, fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	return service.repository.SeatMap(ctx, auditoriumID)
}

func (service *CatalogService) RequestSeatMapBackfill(ctx context.Context, auditoriumID string) error {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	return service.repository.RequestSeatMapBackfill(ctx, auditoriumID, service.clock().UTC())
}

// ResolveSeatMap returns the current layout or a typed capture state.
func (service *CatalogService) ResolveSeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Resolution, error) {
	snapshot, err := service.SeatMap(ctx, auditoriumID)
	if err == nil {
		return seatmappb.Resolution_builder{Ready: seatmappb.Ready_builder{Snapshot: snapshot}.Build()}.Build(), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := service.RequestSeatMapBackfill(ctx, auditoriumID); err != nil {
		return nil, err
	}
	queued := seatmappb.CaptureQueued_builder{NextCheckAt: timestamppb.New(service.clock().UTC().Add(2 * time.Second))}.Build()
	queued.SetTaskId(auditoriumID)
	return seatmappb.Resolution_builder{CaptureQueued: queued}.Build(), nil
}
