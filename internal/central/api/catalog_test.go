package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestClientCatalogAPI(t *testing.T) {
	t.Parallel()
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: "user", SessionID: "session"}
	repository := &apiCatalogRepository{
		apiResourceRepository: &apiResourceRepository{principal: principal, resources: make(map[string]central.ClientResource)},
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
		contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
		"Idempotency-Key":        "catalog-write",
		clientInstallationHeader: "install-catalog-client",
	}
	snapshot := apiCatalogSnapshot(time.Now().UTC())
	missingInstallation := cloneHeaders(headers)
	delete(missingInstallation, clientInstallationHeader)
	unauthorizedWrite := request(t, server.Handler(), http.MethodPost, "/v1/catalog/snapshots", snapshot, missingInstallation)
	assertAPIError(t, unauthorizedWrite, http.StatusUnauthorized, "unauthorized")
	published := request(t, server.Handler(), http.MethodPost, "/v1/catalog/snapshots", snapshot, headers)
	if published.Code != http.StatusOK || published.Header().Get(contracts.CatalogGenerationHeader) != "4" {
		t.Fatalf("publish catalog = %d, %s, generation %q", published.Code, published.Body.String(), published.Header().Get(contracts.CatalogGenerationHeader))
	}

	getHeaders := cloneHeaders(headers)
	delete(getHeaders, "Idempotency-Key")
	loaded := request(t, server.Handler(), http.MethodGet, "/v1/catalog", nil, getHeaders)
	if loaded.Code != http.StatusOK || loaded.Header().Get(contracts.CatalogGenerationHeader) != "4" {
		t.Fatalf("get catalog = %d, %s", loaded.Code, loaded.Body.String())
	}

	repository.seatMap = contracts.SeatMapVersion{
		ID: "stored-version", AuditoriumID: snapshot.Auditoriums[0].ID,
	}
	current := request(t, server.Handler(), http.MethodGet,
		"/v1/catalog/auditoriums/"+snapshot.Auditoriums[0].ID+"/seat-map", nil, getHeaders)
	if current.Code != http.StatusOK {
		t.Fatalf("get seat map = %d, %s", current.Code, current.Body.String())
	}
	backfill := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+snapshot.Auditoriums[0].ID+"/seat-map:resolve", nil, getHeaders)
	if backfill.Code != http.StatusOK || repository.requestedSeatMap != "" ||
		!strings.Contains(backfill.Body.String(), `"status":"ready"`) {
		t.Fatalf("request seat map = %d, %s; requested %q", backfill.Code, backfill.Body.String(), repository.requestedSeatMap)
	}
	repository.seatMap = contracts.SeatMapVersion{}
	waiting := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+snapshot.Auditoriums[0].ID+"/seat-map:resolve", nil, getHeaders)
	if waiting.Code != http.StatusOK || repository.requestedSeatMap != snapshot.Auditoriums[0].ID ||
		!strings.Contains(waiting.Body.String(), `"status":"waiting"`) {
		t.Fatalf("resolve missing seat map = %d, %s; requested %q", waiting.Code, waiting.Body.String(), repository.requestedSeatMap)
	}
	retiredDirectWrite := request(t, server.Handler(), http.MethodPut,
		"/v1/catalog/seat-map-versions/legacy", map[string]string{"id": "legacy"}, headers)
	if retiredDirectWrite.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired direct seat-map write = %d, %s", retiredDirectWrite.Code, retiredDirectWrite.Body.String())
	}
}

func apiCatalogSnapshot(observedAt time.Time) contracts.CatalogSnapshot {
	provider := contracts.Provider{ID: contracts.ProviderCGV, Name: "CGV"}
	theaterSource := "서울/용산아이파크몰"
	theater := contracts.Theater{
		ID: contracts.CatalogID(provider.ID, "theater", theaterSource), ProviderID: provider.ID,
		SourceKey: theaterSource, Region: "서울", Name: "용산아이파크몰",
	}
	movie := contracts.Movie{
		ID: contracts.CatalogID(provider.ID, "movie", "테스트 영화"), ProviderID: provider.ID,
		SourceKey: "테스트 영화", Title: "테스트 영화",
	}
	auditoriumSource := theaterSource + "/IMAX관"
	auditorium := contracts.Auditorium{
		ID: contracts.CatalogID(provider.ID, "auditorium", auditoriumSource), TheaterID: theater.ID,
		SourceKey: auditoriumSource, Name: "IMAX관", Capacity: 624,
	}
	return contracts.CatalogSnapshot{
		Provider: provider, Theaters: []contracts.Theater{theater}, Movies: []contracts.Movie{movie},
		Auditoriums: []contracts.Auditorium{auditorium}, ObservedAt: observedAt,
	}
}

type apiCatalogRepository struct {
	*apiResourceRepository
	index            contracts.CatalogIndex
	snapshot         contracts.CatalogSnapshot
	seatMap          contracts.SeatMapVersion
	requestedSeatMap string
	generation       int64
	refresh          central.CatalogRefreshStatus
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

func (repository *apiCatalogRepository) Catalog(context.Context) (contracts.CatalogIndex, error) {
	repository.index.Generation = repository.generation
	return repository.index, nil
}

func (repository *apiCatalogRepository) CatalogRefreshStatus(
	context.Context,
	time.Time,
	time.Time,
) (central.CatalogRefreshStatus, error) {
	return repository.refresh, nil
}

func (repository *apiCatalogRepository) RequestCatalogRefresh(_ context.Context, at time.Time) error {
	repository.refresh.RequestedAt = &at
	return nil
}

func (repository *apiCatalogRepository) UpsertCatalogSnapshot(
	_ context.Context,
	snapshot contracts.CatalogSnapshot,
) (int64, error) {
	repository.snapshot = snapshot
	repository.index = contracts.CatalogIndex{
		Generation: repository.generation, Providers: []contracts.Provider{snapshot.Provider},
		Theaters: snapshot.Theaters, Movies: snapshot.Movies,
		Auditoriums: snapshot.Auditoriums, Showtimes: snapshot.Showtimes,
	}
	return repository.generation, nil
}

func (repository *apiCatalogRepository) PutSeatMapVersion(
	_ context.Context,
	version contracts.SeatMapVersion,
) (int64, error) {
	repository.seatMap = version
	return repository.generation, nil
}

func (repository *apiCatalogRepository) SeatMapVersion(
	_ context.Context,
	auditoriumID string,
) (contracts.SeatMapVersion, error) {
	if repository.seatMap.AuditoriumID != auditoriumID {
		return contracts.SeatMapVersion{}, central.ErrNotFound
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
