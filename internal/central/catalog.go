package central

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/proto"
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

type seatMapStateReader interface {
	SeatMapCollectionState(context.Context, string) (*collectionpb.State, error)
}

type seatMapStateWatcher interface {
	WatchSeatMapCollection(context.Context, string, func() error) error
}

func (service *CatalogService) resolveSeatMap(ctx context.Context, auditoriumID string, enqueue bool) (*seatmappb.Resolution, error) {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return nil, fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	snapshot, err := service.SeatMap(ctx, auditoriumID)
	if err == nil {
		state := &collectionpb.State{}
		if reader, ok := service.repository.(seatMapStateReader); ok {
			state, err = reader.SeatMapCollectionState(ctx, auditoriumID)
			if errors.Is(err, ErrNotFound) {
				state = &collectionpb.State{}
				state.SetIdle(&collectionpb.Idle{})
				err = nil
			}
			if err != nil {
				return nil, err
			}
		} else {
			state.SetIdle(&collectionpb.Idle{})
		}
		return seatmappb.Resolution_builder{Snapshot: snapshot, State: state}.Build(), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if enqueue {
		if err := service.RequestSeatMapBackfill(ctx, auditoriumID); err != nil {
			return nil, err
		}
	}
	if reader, ok := service.repository.(seatMapStateReader); ok {
		state, stateErr := reader.SeatMapCollectionState(ctx, auditoriumID)
		if stateErr == nil {
			return seatmappb.Resolution_builder{State: state}.Build(), nil
		}
		if !errors.Is(stateErr, ErrNotFound) {
			return nil, stateErr
		}
		if !enqueue {
			return nil, ErrNotFound
		}
	}
	state := &collectionpb.State{}
	state.SetQueued((&collectionpb.Queued_builder{
		QueuedAt: timestamppb.New(service.clock().UTC()),
		Trigger:  (&collectionpb.Trigger_builder{ClientRequest: (&collectionpb.ClientRequest_builder{}).Build()}).Build(),
	}).Build())
	return seatmappb.Resolution_builder{State: state}.Build(), nil
}

// ResolveSeatMap returns the current layout or a typed collection state.
func (service *CatalogService) ResolveSeatMap(ctx context.Context, request *servicepb.ResolveSeatMapRequest) (*servicepb.ResolveSeatMapResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: resolve seat-map request is required", ErrInvalid)
	}
	resolution, err := service.resolveSeatMap(ctx, request.GetAuditoriumId(), true)
	if err != nil {
		return nil, err
	}
	return servicepb.ResolveSeatMapResponse_builder{Resolution: resolution}.Build(), nil
}

// WatchSeatMap emits one initial resolution and then waits only on committed
// PostgreSQL state notifications. Identical resolutions are suppressed.
func (service *CatalogService) WatchSeatMap(
	ctx context.Context,
	request *servicepb.WatchSeatMapRequest,
	send func(*servicepb.WatchSeatMapResponse) error,
) error {
	if request == nil || send == nil {
		return fmt.Errorf("%w: watch seat-map request and sender are required", ErrInvalid)
	}
	watcher, ok := service.repository.(seatMapStateWatcher)
	if !ok {
		return errors.New("seat-map watch is unavailable")
	}
	auditoriumID := strings.TrimSpace(request.GetAuditoriumId())
	if auditoriumID == "" {
		return fmt.Errorf("%w: auditorium id is required", ErrInvalid)
	}
	var resolution *seatmappb.Resolution
	initial := true
	return watcher.WatchSeatMapCollection(ctx, auditoriumID, func() error {
		next, err := service.resolveSeatMap(ctx, auditoriumID, initial)
		if errors.Is(err, ErrNotFound) && !initial {
			return nil
		}
		if err != nil {
			return err
		}
		if !initial && proto.Equal(resolution, next) {
			return nil
		}
		initial = false
		resolution = next
		return send(servicepb.WatchSeatMapResponse_builder{Resolution: resolution}.Build())
	})
}
