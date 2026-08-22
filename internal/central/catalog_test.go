package central

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	"github.com/cineko-org/central/internal/support/numeric"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCatalogServiceNormalizesAndDelegatesSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFake{generation: 7}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	snapshot := validCatalogSnapshot(now)
	snapshot.GetAuditoriums()[0].SetScreenTypes([]string{" IMAX ", "2D", "IMAX"})
	snapshot.GetShowtimes()[0].GetMovie().SetTitle("untrusted duplicate")

	generation, err := service.PutSnapshot(t.Context(), snapshot)
	if err != nil || generation != 7 {
		t.Fatalf("PutSnapshot() = %d, %v", generation, err)
	}
	showtime := repository.snapshot.GetShowtimes()[0]
	if showtime.GetMovie() != repository.snapshot.GetMovies()[0] ||
		!proto.Equal(showtime.GetAuditorium(), repository.snapshot.GetAuditoriums()[0]) ||
		len(repository.snapshot.GetAuditoriums()[0].GetScreenTypes()) != 2 {
		t.Fatalf("normalized snapshot = %+v", repository.snapshot)
	}
	if !repository.snapshot.GetObservedAt().AsTime().Equal(now) {
		t.Fatalf("observedAt = %v, want %v", repository.snapshot.GetObservedAt().AsTime(), now)
	}
}

func TestCatalogRefreshStateAndRequest(t *testing.T) {
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		input *adminpb.CatalogRefreshStatus
		state string
	}{
		{name: "ready", input: &adminpb.CatalogRefreshStatus{}, state: "ready"},
		{name: "running", input: refreshStatus(true, false, 0), state: "running"},
		{name: "waiting", input: refreshStatus(false, true, 0), state: "waiting"},
		{name: "queued", input: refreshStatus(false, true, 1), state: "queued"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &catalogRepositoryFake{refresh: test.input}
			service, err := NewCatalogService(repository)
			if err != nil {
				t.Fatal(err)
			}
			service.clock = func() time.Time { return now }
			status, err := service.RefreshStatus(t.Context())
			if err != nil || refreshState(status) != test.state {
				t.Fatalf("status = %+v, error = %v", status, err)
			}
		})
	}
	repository := &catalogRepositoryFake{}
	service, _ := NewCatalogService(repository)
	service.clock = func() time.Time { return now }
	if err := service.RequestRefresh(t.Context()); err != nil || !repository.requested {
		t.Fatalf("request refresh = %v, requested = %t", err, repository.requested)
	}
	repository.err = errors.New("refresh unavailable")
	if _, err := service.RefreshStatus(t.Context()); !errors.Is(err, repository.err) {
		t.Fatalf("RefreshStatus() error = %v", err)
	}
}

func TestCatalogClientWriteAuthorization(t *testing.T) {
	repository := &catalogRepositoryFake{}
	service, _ := NewCatalogService(repository)
	principal := ClientPrincipal{UserID: "user"}
	capability := probedomain.CapabilityCGVCatalogCapture
	if err := service.AuthorizeClientWrite(t.Context(), principal, " ", capability); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing installation authorization = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.AuthorizeClientWrite(t.Context(), principal, " install ", capability); !errors.Is(err, errInjectedClient) {
		t.Fatalf("repository authorization error = %v", err)
	}
	repository.err = nil
	if err := service.AuthorizeClientWrite(t.Context(), principal, " install ", capability); err != nil {
		t.Fatalf("authorization = %v", err)
	}
	if repository.authorizedUser != "user" || repository.authorizedInstallation != "install" ||
		repository.authorizedCapability != capability {
		t.Fatalf("authorization boundary = %q %q %q", repository.authorizedUser, repository.authorizedInstallation, repository.authorizedCapability)
	}
}

func TestCatalogServiceRejectsBrokenRelationships(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	service, _ := NewCatalogService(&catalogRepositoryFake{})
	service.clock = func() time.Time { return now }
	tests := map[string]func(*catalogpb.CatalogSnapshot){
		"unknown auditorium theater": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.GetAuditoriums()[0].SetTheaterId("unknown")
		},
		"unknown showtime movie": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.GetShowtimes()[0].GetMovie().SetId("unknown")
		},
		"cross-theater showtime": func(snapshot *catalogpb.CatalogSnapshot) {
			other := proto.CloneOf(snapshot.GetTheaters()[0])
			catalogdomain.SetTheaterSourceKey(other, "0043")
			other.SetName("영등포")
			other.SetId(catalogdomain.CatalogID(other.GetProviderId(), "theater", "0043"))
			snapshot.SetTheaters(append(snapshot.GetTheaters(), other))
			snapshot.GetShowtimes()[0].SetTheaterId(other.GetId())
		},
		"future observation": func(snapshot *catalogpb.CatalogSnapshot) {
			snapshot.SetObservedAt(timestamppb.New(now.Add(catalogdomain.MaximumObservationClockSkew + time.Second)))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := validCatalogSnapshot(now)
			mutate(snapshot)
			if _, err := service.PutSnapshot(t.Context(), snapshot); !errors.Is(err, ErrInvalid) {
				t.Fatalf("PutSnapshot() error = %v", err)
			}
		})
	}
}

func TestCatalogServiceCanonicalizesSeatMap(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFake{generation: 9}
	service, _ := NewCatalogService(repository)
	service.clock = func() time.Time { return now }
	snapshot := seatMapSnapshot(" auditorium ", 2)

	generation, err := service.PutSeatMap(t.Context(), snapshot)
	if err != nil || generation != 9 {
		t.Fatalf("PutSeatMap() = %d, %v", generation, err)
	}
	stored := repository.seatMap
	if stored.GetAuditoriumId() != "auditorium" || stored.GetLayoutHash() == "" ||
		stored.GetId() != catalogdomain.SeatMapVersionID(stored.GetAuditoriumId(), stored.GetLayoutHash()) ||
		!stored.GetObservedAt().AsTime().Equal(now) {
		t.Fatalf("canonical seat map = %+v", stored)
	}
	if _, err := service.PutSeatMap(t.Context(), seatMapSnapshot("auditorium", 0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid seat map error = %v", err)
	}
}

func TestCatalogServiceBoundariesAndBackfill(t *testing.T) {
	if _, err := NewCatalogService(nil); err == nil {
		t.Fatal("nil catalog repository accepted")
	}
	index := &catalogpb.CatalogIndex{}
	index.SetGeneration(3)
	stored := seatMapSnapshot("auditorium", 1)
	stored.SetId("version")
	repository := &catalogRepositoryFake{index: index, seatMap: stored}
	service, _ := NewCatalogService(repository)
	if got, err := service.Catalog(t.Context()); err != nil || got.GetGeneration() != 3 {
		t.Fatalf("Catalog() = %+v, %v", got, err)
	}
	request := &servicepb.ResolveSeatMapRequest{}
	request.SetAuditoriumId("auditorium")
	resolution, err := service.ResolveSeatMap(t.Context(), request)
	if err != nil || resolution.GetResolution().GetSnapshot().GetId() != "version" || repository.requested {
		t.Fatalf("stored ResolveSeatMap() = %+v, requested=%t, error=%v", resolution, repository.requested, err)
	}
	repository.seatMap = nil
	request.SetAuditoriumId("missing")
	resolution, err = service.ResolveSeatMap(t.Context(), request)
	if err != nil || resolution.GetResolution().GetState().GetQueued() == nil || !repository.requested {
		t.Fatalf("missing ResolveSeatMap() = %+v, requested=%t, error=%v", resolution, repository.requested, err)
	}
	if err := service.RequestSeatMapBackfill(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank RequestSeatMapBackfill() error = %v", err)
	}
	repository.err = errInjectedClient
	request.SetAuditoriumId("auditorium")
	if _, err := service.ResolveSeatMap(t.Context(), request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("seat-map lookup error = %v", err)
	}
}

func TestCatalogServiceWatchSeatMapEmitsInitialAndChangedStates(t *testing.T) {
	queued := &collectionpb.State{}
	queued.SetQueued((&collectionpb.Queued_builder{
		QueuedAt: timestamppb.New(time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC)),
		Trigger:  (&collectionpb.Trigger_builder{ClientRequest: (&collectionpb.ClientRequest_builder{}).Build()}).Build(),
	}).Build())
	collecting := &collectionpb.State{}
	assignmentID := "assignment-1"
	collecting.SetCollecting((&collectionpb.Collecting_builder{
		AssignmentId: &assignmentID,
		StartedAt:    timestamppb.New(time.Date(2026, 8, 23, 1, 0, 1, 0, time.UTC)),
	}).Build())
	stop := errors.New("stop watch")
	repository := &catalogWatchRepository{
		catalogRepositoryFake: &catalogRepositoryFake{},
		states:                []*collectionpb.State{queued, proto.CloneOf(queued), collecting},
		stop:                  stop,
	}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	request := &servicepb.WatchSeatMapRequest{}
	request.SetAuditoriumId(" auditorium ")
	var responses []*servicepb.WatchSeatMapResponse
	err = service.WatchSeatMap(t.Context(), request, func(response *servicepb.WatchSeatMapResponse) error {
		responses = append(responses, proto.CloneOf(response))
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("WatchSeatMap() error = %v", err)
	}
	if repository.auditoriumID != "auditorium" || !repository.requested {
		t.Fatalf("watch boundary auditorium=%q requested=%t", repository.auditoriumID, repository.requested)
	}
	if len(responses) != 2 || responses[0].GetResolution().GetState().GetQueued() == nil ||
		responses[1].GetResolution().GetState().GetCollecting().GetAssignmentId() != "assignment-1" {
		t.Fatalf("watch responses = %+v", responses)
	}
}

func validCatalogSnapshot(observedAt time.Time) *catalogpb.CatalogSnapshot {
	provider := &catalogpb.Provider{}
	provider.SetId(catalogdomain.ProviderCGV)
	provider.SetName("CGV")
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(provider.GetId(), "theater", "0056"))
	theater.SetProviderId(provider.GetId())
	catalogdomain.SetTheaterSourceKey(theater, "0056")
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	movie := &catalogpb.Movie{}
	movie.SetId(catalogdomain.CatalogID(provider.GetId(), "movie", "00001234"))
	movie.SetProviderId(provider.GetId())
	catalogdomain.SetMovieSourceKey(movie, "00001234")
	movie.SetTitle("테스트 영화")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(catalogdomain.CatalogID(provider.GetId(), "auditorium", "0056/0007"))
	auditorium.SetTheaterId(theater.GetId())
	catalogdomain.SetAuditoriumSourceKey(auditorium, "0056/0007")
	auditorium.SetName("IMAX관")
	auditorium.SetCapacity(624)
	showtime := &catalogpb.Showtime{}
	showtime.SetId(catalogdomain.CatalogID(provider.GetId(), "showtime", "0056/2026-08-14/0007/0003"))
	showtime.SetProviderId(provider.GetId())
	catalogdomain.SetShowtimeSourceKey(showtime, "0056/2026-08-14/0007/0003")
	showtime.SetTheaterId(theater.GetId())
	showtime.SetMovie(proto.CloneOf(movie))
	showtime.SetAuditorium(proto.CloneOf(auditorium))
	showtime.SetStartsAt(timestamppb.New(observedAt.Add(time.Hour)))
	showtime.SetEndsAt(timestamppb.New(observedAt.Add(3 * time.Hour)))
	showtime.SetCapacity(624)
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetMovies([]*catalogpb.Movie{movie})
	snapshot.SetAuditoriums([]*catalogpb.Auditorium{auditorium})
	snapshot.SetShowtimes([]*catalogpb.Showtime{showtime})
	snapshot.SetObservedAt(timestamppb.New(observedAt))
	return snapshot
}

func seatMapSnapshot(auditoriumID string, count int) *seatmappb.Snapshot {
	seats := make([]*seatmappb.Seat, 0, count)
	for number := 1; number <= count; number++ {
		label := fmt.Sprintf("A%d", number)
		seat := &seatmappb.Seat{}
		seat.SetId(catalogdomain.SeatID("auditorium", label))
		seat.SetAuditoriumId("auditorium")
		seat.SetLabel(label)
		seat.SetRow("A")
		seat.SetNumber(int32(number))
		seat.SetX(float64(number) / float64(count+1))
		seat.SetY(0.5)
		seat.SetType("standard")
		seats = append(seats, seat)
	}
	layout := &seatmappb.Layout{}
	layout.SetSeats(seats)
	snapshot := &seatmappb.Snapshot{}
	snapshot.SetAuditoriumId(auditoriumID)
	snapshot.SetCapacity(numeric.ClampInt32(count))
	snapshot.SetLayout(layout)
	return snapshot
}

func refreshStatus(active, empty bool, eligible int32) *adminpb.CatalogRefreshStatus {
	status := &adminpb.CatalogRefreshStatus{}
	status.SetActive(active)
	status.SetCatalogEmpty(empty)
	status.SetEligibleProbes(eligible)
	return status
}

func refreshState(status *adminpb.CatalogRefreshStatus) string {
	switch {
	case status.GetReady() != nil:
		return "ready"
	case status.GetRunning() != nil:
		return "running"
	case status.GetWaitingForProbe() != nil:
		return "waiting"
	case status.GetQueued() != nil:
		return "queued"
	default:
		return ""
	}
}

type catalogRepositoryFake struct {
	index                  *catalogpb.CatalogIndex
	refresh                *adminpb.CatalogRefreshStatus
	requested              bool
	snapshot               *catalogpb.CatalogSnapshot
	seatMap                *seatmappb.Snapshot
	generation             int64
	authorizedUser         string
	authorizedInstallation string
	authorizedCapability   string
	err                    error
	requestError           error
}

type catalogWatchRepository struct {
	*catalogRepositoryFake
	states       []*collectionpb.State
	current      *collectionpb.State
	auditoriumID string
	stop         error
}

func (repository *catalogWatchRepository) SeatMapCollectionState(
	_ context.Context,
	auditoriumID string,
) (*collectionpb.State, error) {
	repository.auditoriumID = auditoriumID
	if repository.current == nil {
		return nil, ErrNotFound
	}
	return proto.CloneOf(repository.current), nil
}

func (repository *catalogWatchRepository) WatchSeatMapCollection(
	_ context.Context,
	auditoriumID string,
	observe func() error,
) error {
	repository.auditoriumID = auditoriumID
	for _, state := range repository.states {
		repository.current = proto.CloneOf(state)
		if err := observe(); err != nil {
			return err
		}
	}
	return repository.stop
}

func (repository *catalogRepositoryFake) AuthorizeCatalogWrite(_ context.Context, userID, installationID, capability string) error {
	repository.authorizedUser = userID
	repository.authorizedInstallation = installationID
	repository.authorizedCapability = capability
	return repository.err
}

func (repository *catalogRepositoryFake) Catalog(context.Context) (*catalogpb.CatalogIndex, error) {
	return repository.index, repository.err
}

func (repository *catalogRepositoryFake) CatalogRefreshStatus(context.Context, time.Time, time.Time) (*adminpb.CatalogRefreshStatus, error) {
	if repository.refresh == nil {
		repository.refresh = &adminpb.CatalogRefreshStatus{}
	}
	return repository.refresh, repository.err
}

func (repository *catalogRepositoryFake) RequestCatalogRefresh(context.Context, time.Time) error {
	repository.requested = true
	return repository.err
}

func (repository *catalogRepositoryFake) UpsertCatalogSnapshot(_ context.Context, snapshot *catalogpb.CatalogSnapshot) (int64, error) {
	repository.snapshot = snapshot
	return repository.generation, repository.err
}

func (repository *catalogRepositoryFake) PutSeatMap(_ context.Context, snapshot *seatmappb.Snapshot) (int64, error) {
	repository.seatMap = snapshot
	return repository.generation, repository.err
}

func (repository *catalogRepositoryFake) SeatMap(context.Context, string) (*seatmappb.Snapshot, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.seatMap == nil {
		return nil, ErrNotFound
	}
	return repository.seatMap, nil
}

func (repository *catalogRepositoryFake) RequestSeatMapBackfill(context.Context, string, time.Time) error {
	repository.requested = true
	return repository.requestError
}
