package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestClientCatalogAPI(t *testing.T) {
	t.Parallel()
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: "user", SessionID: "session"}
	repository := &apiCatalogRepository{
		apiResourceRepository: &apiResourceRepository{principal: principal, resources: make(map[string]*clientpb.Resource)},
		generation:            4,
	}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalogService, err := central.NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(probeService, WithClientService(clients), WithCatalogService(catalogService))
	if err != nil {
		t.Fatal(err)
	}
	crossSite := request(t, server.Handler(), http.MethodPost, "/v1/admin/catalog-refresh", nil, map[string]string{
		"Origin": "https://attacker.invalid",
	})
	if crossSite.Code != http.StatusForbidden {
		t.Fatalf("cross-site catalog refresh = %d, %s", crossSite.Code, crossSite.Body.String())
	}
	headers := map[string]string{
		"Authorization":          "Bearer client-session",
		"Idempotency-Key":        "catalog-write",
		clientInstallationHeader: "install-catalog-client",
	}
	snapshot := apiCatalogSnapshot(time.Now().UTC())
	missingInstallation := cloneHeaders(headers)
	delete(missingInstallation, clientInstallationHeader)
	unauthorizedWrite := request(t, server.Handler(), http.MethodPost, "/v1/catalog/snapshots", snapshot, missingInstallation)
	assertAPIError(t, unauthorizedWrite, http.StatusUnauthorized, "unauthorized")
	published := request(t, server.Handler(), http.MethodPost, "/v1/catalog/snapshots", snapshot, headers)
	if published.Code != http.StatusOK || published.Header().Get(catalogGenerationHeader) != "4" {
		t.Fatalf("publish catalog = %d, %s, generation %q", published.Code, published.Body.String(), published.Header().Get(catalogGenerationHeader))
	}

	getHeaders := cloneHeaders(headers)
	delete(getHeaders, "Idempotency-Key")
	loaded := request(t, server.Handler(), http.MethodGet, "/v1/catalog", nil, getHeaders)
	if loaded.Code != http.StatusOK || loaded.Header().Get(catalogGenerationHeader) != "4" {
		t.Fatalf("get catalog = %d, %s", loaded.Code, loaded.Body.String())
	}

	auditoriumID := snapshot.GetAuditoriums()[0].GetId()
	repository.seatMap = &seatmappb.Snapshot{}
	repository.seatMap.SetId("stored-version")
	repository.seatMap.SetAuditoriumId(auditoriumID)
	current := request(t, server.Handler(), http.MethodGet,
		"/v1/catalog/auditoriums/"+auditoriumID+"/seat-map", nil, getHeaders)
	if current.Code != http.StatusOK {
		t.Fatalf("get seat map = %d, %s", current.Code, current.Body.String())
	}
	backfill := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+auditoriumID+"/seat-map:resolve", nil, getHeaders)
	if backfill.Code != http.StatusOK || repository.requestedSeatMap != "" ||
		!strings.Contains(backfill.Body.String(), `"snapshot"`) {
		t.Fatalf("request seat map = %d, %s; requested %q", backfill.Code, backfill.Body.String(), repository.requestedSeatMap)
	}
	repository.seatMap = nil
	waiting := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+auditoriumID+"/seat-map:resolve", nil, getHeaders)
	if waiting.Code != http.StatusOK || repository.requestedSeatMap != auditoriumID ||
		!strings.Contains(waiting.Body.String(), `"queued"`) {
		t.Fatalf("resolve missing seat map = %d, %s; requested %q", waiting.Code, waiting.Body.String(), repository.requestedSeatMap)
	}
	retiredDirectWrite := request(t, server.Handler(), http.MethodPut,
		"/v1/catalog/seat-map-versions/legacy", map[string]string{"id": "legacy"}, headers)
	if retiredDirectWrite.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired direct seat-map write = %d, %s", retiredDirectWrite.Code, retiredDirectWrite.Body.String())
	}
}

func TestClientSeatMapWatchUsesSSEFramingAndSuppressesDuplicates(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: "user", SessionID: "session"}
	base := &apiCatalogRepository{
		apiResourceRepository: &apiResourceRepository{principal: principal, resources: make(map[string]*clientpb.Resource)},
	}
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
	repository := &apiCatalogWatchRepository{
		apiCatalogRepository: base,
		states:               []*collectionpb.State{queued, proto.CloneOf(queued), collecting},
	}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalogService, err := central.NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(probeService, WithClientService(clients), WithCatalogService(catalogService))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	writer := newCancelResponseWriter(cancel, `"collecting"`)
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"/v1/catalog/auditoriums/auditorium/seat-map:watch",
		nil,
	)
	request.Header.Set("Authorization", "Bearer client-session")
	server.Handler().ServeHTTP(writer, request)
	body := writer.body.String()
	if writer.status != http.StatusOK || writer.header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("seat-map watch response = %d, headers=%v, body=%q", writer.status, writer.header, body)
	}
	if strings.Count(body, "event: cineko.seat-map\n") != 2 ||
		strings.Count(body, "data: {") != 2 || strings.Contains(body, `\\n`) {
		t.Fatalf("seat-map SSE framing = %q", body)
	}
}

func apiCatalogSnapshot(observedAt time.Time) *catalogpb.CatalogSnapshot {
	provider := &catalogpb.Provider{}
	provider.SetId(catalogdomain.ProviderCGV)
	provider.SetName("CGV")
	theaterSource := "0056"
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(provider.GetId(), "theater", theaterSource))
	theater.SetProviderId(provider.GetId())
	catalogdomain.SetTheaterSourceKey(theater, theaterSource)
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	movie := &catalogpb.Movie{}
	movie.SetId(catalogdomain.CatalogID(provider.GetId(), "movie", "00001234"))
	movie.SetProviderId(provider.GetId())
	catalogdomain.SetMovieSourceKey(movie, "00001234")
	movie.SetTitle("테스트 영화")
	auditoriumSource := theaterSource + "/0007"
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(catalogdomain.CatalogID(provider.GetId(), "auditorium", auditoriumSource))
	auditorium.SetTheaterId(theater.GetId())
	catalogdomain.SetAuditoriumSourceKey(auditorium, auditoriumSource)
	auditorium.SetName("IMAX관")
	auditorium.SetCapacity(624)
	return catalogpb.CatalogSnapshot_builder{
		Provider: provider, Theaters: []*catalogpb.Theater{theater}, Movies: []*catalogpb.Movie{movie},
		Auditoriums: []*catalogpb.Auditorium{auditorium}, ObservedAt: timestamppb.New(observedAt),
	}.Build()
}

type apiCatalogRepository struct {
	*apiResourceRepository
	index            *catalogpb.CatalogIndex
	snapshot         *catalogpb.CatalogSnapshot
	seatMap          *seatmappb.Snapshot
	requestedSeatMap string
	generation       int64
	refresh          *adminpb.CatalogRefreshStatus
}

type apiCatalogWatchRepository struct {
	*apiCatalogRepository
	states  []*collectionpb.State
	current *collectionpb.State
}

func (repository *apiCatalogWatchRepository) SeatMapCollectionState(
	context.Context,
	string,
) (*collectionpb.State, error) {
	if repository.current == nil {
		return nil, central.ErrNotFound
	}
	return proto.CloneOf(repository.current), nil
}

func (repository *apiCatalogWatchRepository) WatchSeatMapCollection(
	ctx context.Context,
	_ string,
	observe func() error,
) error {
	for _, state := range repository.states {
		repository.current = proto.CloneOf(state)
		if err := observe(); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (repository *apiCatalogRepository) AuthorizeCatalogWrite(
	_ context.Context,
	userID string,
	installationID string,
	_ string,
) error {
	if userID != "user" || installationID != "install-catalog-client" {
		return central.ErrUnauthorized
	}
	return nil
}

func (repository *apiCatalogRepository) Catalog(context.Context) (*catalogpb.CatalogIndex, error) {
	if repository.index == nil {
		repository.index = &catalogpb.CatalogIndex{}
	}
	repository.index.SetGeneration(repository.generation)
	return repository.index, nil
}

func (repository *apiCatalogRepository) CatalogRefreshStatus(
	context.Context,
	time.Time,
	time.Time,
) (*adminpb.CatalogRefreshStatus, error) {
	if repository.refresh == nil {
		repository.refresh = &adminpb.CatalogRefreshStatus{}
	}
	return repository.refresh, nil
}

func (repository *apiCatalogRepository) RequestCatalogRefresh(_ context.Context, at time.Time) error {
	if repository.refresh == nil {
		repository.refresh = &adminpb.CatalogRefreshStatus{}
	}
	repository.refresh.SetRequestedAt(timestamppb.New(at))
	return nil
}

func (repository *apiCatalogRepository) UpsertCatalogSnapshot(
	_ context.Context,
	snapshot *catalogpb.CatalogSnapshot,
) (int64, error) {
	repository.snapshot = snapshot
	repository.index = catalogpb.CatalogIndex_builder{
		Generation: &repository.generation, Providers: []*catalogpb.Provider{snapshot.GetProvider()},
		Theaters: snapshot.GetTheaters(), Movies: snapshot.GetMovies(),
		Auditoriums: snapshot.GetAuditoriums(), Showtimes: snapshot.GetShowtimes(),
	}.Build()
	return repository.generation, nil
}

func (repository *apiCatalogRepository) PutSeatMap(
	_ context.Context,
	version *seatmappb.Snapshot,
) (int64, error) {
	repository.seatMap = version
	return repository.generation, nil
}

func (repository *apiCatalogRepository) SeatMap(
	_ context.Context,
	auditoriumID string,
) (*seatmappb.Snapshot, error) {
	if repository.seatMap == nil || repository.seatMap.GetAuditoriumId() != auditoriumID {
		return nil, central.ErrNotFound
	}
	return repository.seatMap, nil
}

func (repository *apiCatalogRepository) RequestSeatMapBackfill(_ context.Context, auditoriumID string, _ time.Time) error {
	repository.requestedSeatMap = auditoriumID
	return nil
}

func (repository *apiCatalogRepository) CurrentReleaseGeneration(context.Context) (int64, error) {
	return 1, nil
}

func (repository *apiCatalogRepository) AuthenticateClientSession(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (central.ClientPrincipal, error) {
	if repository.apiResourceRepository == nil {
		return central.ClientPrincipal{}, central.ErrUnauthorized
	}
	return repository.apiResourceRepository.AuthenticateClientSession(ctx, tokenHash, now)
}
