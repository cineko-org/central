package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
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
		!strings.Contains(backfill.Body.String(), `"ready"`) {
		t.Fatalf("request seat map = %d, %s; requested %q", backfill.Code, backfill.Body.String(), repository.requestedSeatMap)
	}
	repository.seatMap = nil
	waiting := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+auditoriumID+"/seat-map:resolve", nil, getHeaders)
	if waiting.Code != http.StatusOK || repository.requestedSeatMap != auditoriumID ||
		!strings.Contains(waiting.Body.String(), `"captureQueued"`) {
		t.Fatalf("resolve missing seat map = %d, %s; requested %q", waiting.Code, waiting.Body.String(), repository.requestedSeatMap)
	}
	retiredDirectWrite := request(t, server.Handler(), http.MethodPut,
		"/v1/catalog/seat-map-versions/legacy", map[string]string{"id": "legacy"}, headers)
	if retiredDirectWrite.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired direct seat-map write = %d, %s", retiredDirectWrite.Code, retiredDirectWrite.Body.String())
	}
}

func apiCatalogSnapshot(observedAt time.Time) *catalogpb.CatalogSnapshot {
	provider := &catalogpb.Provider{}
	provider.SetId(catalogdomain.ProviderCGV)
	provider.SetName("CGV")
	theaterSource := "서울/용산아이파크몰"
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(provider.GetId(), "theater", theaterSource))
	theater.SetProviderId(provider.GetId())
	theater.SetSourceKey(theaterSource)
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	movie := &catalogpb.Movie{}
	movie.SetId(catalogdomain.CatalogID(provider.GetId(), "movie", "테스트 영화"))
	movie.SetProviderId(provider.GetId())
	movie.SetSourceKey("테스트 영화")
	movie.SetTitle("테스트 영화")
	auditoriumSource := theaterSource + "/IMAX관"
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(catalogdomain.CatalogID(provider.GetId(), "auditorium", auditoriumSource))
	auditorium.SetTheaterId(theater.GetId())
	auditorium.SetSourceKey(auditoriumSource)
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
