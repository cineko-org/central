package api

import (
	"context"
	"encoding/json"
	"net/http"
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
	headers := map[string]string{
		"Authorization":          "Bearer client-session",
		contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
		"Idempotency-Key":        "catalog-write",
	}
	snapshot := apiCatalogSnapshot(time.Now().UTC())
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

	layout := json.RawMessage(`{"seats":[{"id":"A1"}],"zones":[],"blocks":[]}`)
	versionID := contracts.SeatMapVersionID(snapshot.Auditoriums[0].ID, catalogLayoutHash(t, layout))
	version := contracts.SeatMapVersion{
		ID: versionID, AuditoriumID: snapshot.Auditoriums[0].ID,
		Capacity: 1, Layout: layout, ObservedAt: time.Now().UTC(),
	}
	seatMap := request(t, server.Handler(), http.MethodPut,
		"/v1/catalog/seat-map-versions/"+versionID, version, headers)
	if seatMap.Code != http.StatusOK || repository.seatMap.ID != versionID {
		t.Fatalf("put seat map = %d, %s; stored %+v", seatMap.Code, seatMap.Body.String(), repository.seatMap)
	}
	current := request(t, server.Handler(), http.MethodGet,
		"/v1/catalog/auditoriums/"+snapshot.Auditoriums[0].ID+"/seat-map", nil, getHeaders)
	if current.Code != http.StatusOK {
		t.Fatalf("get seat map = %d, %s", current.Code, current.Body.String())
	}
	backfill := request(t, server.Handler(), http.MethodPost,
		"/v1/catalog/auditoriums/"+snapshot.Auditoriums[0].ID+"/seat-map:request", nil, getHeaders)
	if backfill.Code != http.StatusAccepted || repository.requestedSeatMap != snapshot.Auditoriums[0].ID {
		t.Fatalf("request seat map = %d, %s; requested %q", backfill.Code, backfill.Body.String(), repository.requestedSeatMap)
	}

	mismatched := request(t, server.Handler(), http.MethodPut,
		"/v1/catalog/seat-map-versions/other", version, headers)
	assertAPIError(t, mismatched, http.StatusBadRequest, "invalid_request")
}

func catalogLayoutHash(t *testing.T, layout json.RawMessage) string {
	t.Helper()
	fake := &apiCatalogRepository{generation: 1}
	service, err := central.NewCatalogService(fake)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PutSeatMapVersion(t.Context(), contracts.SeatMapVersion{
		AuditoriumID: "hash-only", Capacity: 1, Layout: layout, ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return fake.seatMap.LayoutHash
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
