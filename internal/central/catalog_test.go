package central

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestCatalogServiceNormalizesAndDelegatesSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFake{generation: 7}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	snapshot := validCatalogSnapshot(now)
	snapshot.Auditoriums[0].ScreenTypes = []string{" IMAX ", "2D", "IMAX"}
	snapshot.Showtimes[0].Movie.Title = "untrusted duplicate"

	generation, err := service.PutSnapshot(t.Context(), snapshot)
	if err != nil || generation != 7 {
		t.Fatalf("PutSnapshot() = %d, %v", generation, err)
	}
	wantMovie := repository.snapshot.Movies[0]
	wantAuditorium := repository.snapshot.Auditoriums[0]
	showtime := repository.snapshot.Showtimes[0]
	if showtime.Movie != wantMovie || showtime.Auditorium.ID != wantAuditorium.ID ||
		len(wantAuditorium.ScreenTypes) != 2 || wantAuditorium.ScreenTypes[0] != "2D" ||
		wantAuditorium.ScreenTypes[1] != "IMAX" {
		t.Fatalf("normalized snapshot = %+v", repository.snapshot)
	}
	if repository.snapshot.ObservedAt != now {
		t.Fatalf("observedAt = %v, want %v", repository.snapshot.ObservedAt, now)
	}
}

func TestCatalogRefreshStateAndRequest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		input CatalogRefreshStatus
		state string
	}{
		{name: "ready", input: CatalogRefreshStatus{}, state: "ready"},
		{name: "running", input: CatalogRefreshStatus{Active: true}, state: "running"},
		{name: "waiting", input: CatalogRefreshStatus{CatalogEmpty: true}, state: "waiting_for_probe"},
		{name: "queued", input: CatalogRefreshStatus{CatalogEmpty: true, EligibleProbes: 1}, state: "queued"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &catalogRepositoryFake{refresh: test.input}
			service, err := NewCatalogService(repository)
			if err != nil {
				t.Fatal(err)
			}
			service.clock = func() time.Time { return now }
			status, err := service.RefreshStatus(t.Context())
			if err != nil || status.State != test.state {
				t.Fatalf("status = %+v, error = %v", status, err)
			}
		})
	}
	repository := &catalogRepositoryFake{}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	if err := service.RequestRefresh(t.Context()); err != nil || !repository.requested {
		t.Fatalf("request refresh = %v, requested = %t", err, repository.requested)
	}
	repository.err = errors.New("refresh unavailable")
	if _, err := service.RefreshStatus(t.Context()); !errors.Is(err, repository.err) {
		t.Fatalf("RefreshStatus() error = %v", err)
	}
	if err := service.RequestRefresh(t.Context()); !errors.Is(err, repository.err) {
		t.Fatalf("RequestRefresh() error = %v", err)
	}
}

func TestCatalogClientWriteAuthorization(t *testing.T) {
	t.Parallel()
	repository := &catalogRepositoryFake{}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	principal := ClientPrincipal{UserID: "user"}
	if err := service.AuthorizeClientWrite(
		t.Context(), principal, " ", contracts.CapabilityCGVCatalogCapture,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing installation authorization = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.AuthorizeClientWrite(
		t.Context(), principal, " install ", contracts.CapabilityCGVCatalogCapture,
	); !errors.Is(err, errInjectedClient) {
		t.Fatalf("repository authorization error = %v", err)
	}
	repository.err = nil
	if err := service.AuthorizeClientWrite(
		t.Context(), principal, " install ", contracts.CapabilityCGVCatalogCapture,
	); err != nil {
		t.Fatalf("authorization = %v", err)
	}
	if repository.authorizedUser != "user" || repository.authorizedInstallation != "install" ||
		repository.authorizedCapability != contracts.CapabilityCGVCatalogCapture {
		t.Fatalf("authorization boundary = %q %q %q", repository.authorizedUser,
			repository.authorizedInstallation, repository.authorizedCapability)
	}
}

func TestCatalogServiceRejectsBrokenRelationships(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	service, err := NewCatalogService(&catalogRepositoryFake{})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }

	tests := map[string]func(*contracts.CatalogSnapshot){
		"unknown auditorium theater": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Auditoriums[0].TheaterID = "unknown"
		},
		"unknown showtime movie": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.Showtimes[0].Movie.ID = "unknown"
		},
		"cross-theater showtime": func(snapshot *contracts.CatalogSnapshot) {
			other := snapshot.Theaters[0]
			other.SourceKey = "서울/영등포"
			other.Name = "영등포"
			other.ID = contracts.CatalogID(other.ProviderID, "theater", other.SourceKey)
			snapshot.Theaters = append(snapshot.Theaters, other)
			snapshot.Showtimes[0].TheaterID = other.ID
		},
		"future observation": func(snapshot *contracts.CatalogSnapshot) {
			snapshot.ObservedAt = now.Add(catalogdomain.MaximumObservationClockSkew + time.Second)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := validCatalogSnapshot(now)
			mutate(&snapshot)
			if _, err := service.PutSnapshot(t.Context(), snapshot); !errors.Is(err, ErrInvalid) {
				t.Fatalf("PutSnapshot() error = %v", err)
			}
		})
	}
}

func TestCatalogServiceCanonicalizesSeatMap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFake{generation: 9}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	version := contracts.SeatMapVersion{
		AuditoriumID: " auditorium ", Capacity: 2,
		Layout: seatMapLayoutJSON(t, 2),
	}

	generation, err := service.PutSeatMapVersion(t.Context(), version)
	if err != nil || generation != 9 {
		t.Fatalf("PutSeatMapVersion() = %d, %v", generation, err)
	}
	stored := repository.seatMap
	if stored.AuditoriumID != "auditorium" || stored.LayoutHash == "" ||
		stored.ID != contracts.SeatMapVersionID(stored.AuditoriumID, stored.LayoutHash) ||
		stored.ObservedAt != now || !json.Valid(stored.Layout) {
		t.Fatalf("canonical seat map = %+v", stored)
	}
	if _, err := service.PutSeatMapVersion(t.Context(), contracts.SeatMapVersion{
		AuditoriumID: "auditorium", Capacity: 0, Layout: json.RawMessage(`{}`),
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid seat map error = %v", err)
	}
}

func TestCatalogServiceBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
	errRepository := errors.New("catalog repository failure")
	if _, err := NewCatalogService(nil); err == nil {
		t.Fatal("nil catalog repository accepted")
	}
	repository := &catalogRepositoryFake{
		index:   contracts.CatalogIndex{Generation: 3},
		seatMap: contracts.SeatMapVersion{AuditoriumID: "auditorium"},
	}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	if index, err := service.Catalog(t.Context()); err != nil || index.Generation != 3 {
		t.Fatalf("Catalog() = %+v, %v", index, err)
	}
	if version, err := service.SeatMapVersion(t.Context(), " auditorium "); err != nil ||
		version.AuditoriumID != "auditorium" {
		t.Fatalf("SeatMapVersion() = %+v, %v", version, err)
	}
	if _, err := service.SeatMapVersion(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank SeatMapVersion() error = %v", err)
	}

	repository.err = errRepository
	if _, err := service.Catalog(t.Context()); !errors.Is(err, errRepository) {
		t.Fatalf("Catalog() error = %v", err)
	}
	if _, err := service.SeatMapVersion(t.Context(), "auditorium"); !errors.Is(err, errRepository) {
		t.Fatalf("SeatMapVersion() error = %v", err)
	}
	snapshot := validCatalogSnapshot(now)
	snapshot.ObservedAt = time.Time{}
	if _, err := service.PutSnapshot(t.Context(), snapshot); !errors.Is(err, errRepository) ||
		repository.snapshot.ObservedAt != now {
		t.Fatalf("PutSnapshot() = %+v, %v", repository.snapshot, err)
	}
	if _, err := service.PutSeatMapVersion(t.Context(), contracts.SeatMapVersion{
		AuditoriumID: "auditorium", Capacity: 1, Layout: seatMapLayoutJSON(t, 1),
	}); !errors.Is(err, errRepository) {
		t.Fatalf("PutSeatMapVersion() error = %v", err)
	}

	repository.err = nil
	for name, version := range map[string]contracts.SeatMapVersion{
		"future observation": {
			AuditoriumID: "auditorium", Capacity: 1, Layout: seatMapLayoutJSON(t, 1),
			ObservedAt: now.Add(catalogdomain.MaximumObservationClockSkew + time.Second),
		},
		"invalid layout": {AuditoriumID: "auditorium", Capacity: 1, Layout: json.RawMessage(`[]`)},
		"noncanonical id": {
			ID: "wrong", AuditoriumID: "auditorium", Capacity: 1,
			Layout: seatMapLayoutJSON(t, 1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.PutSeatMapVersion(t.Context(), version); !errors.Is(err, ErrInvalid) {
				t.Fatalf("PutSeatMapVersion() error = %v", err)
			}
		})
	}
}

func seatMapLayoutJSON(t *testing.T, count int) json.RawMessage {
	t.Helper()
	const auditoriumID = "auditorium"
	layout := contracts.SeatMapLayout{Seats: make([]contracts.SeatMapSeat, 0, count)}
	for number := 1; number <= count; number++ {
		label := fmt.Sprintf("A%d", number)
		layout.Seats = append(layout.Seats, contracts.SeatMapSeat{
			ID: contracts.SeatID(auditoriumID, label), AuditoriumID: auditoriumID,
			Label: label, Row: "A", Number: number, X: float64(number) / float64(count+1), Y: 0.5,
			Type: "standard", Features: []string{},
		})
	}
	raw, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCatalogServiceRequestsSeatMapBackfill(t *testing.T) {
	t.Parallel()
	stored := contracts.SeatMapVersion{ID: "version", AuditoriumID: "stored"}
	repository := &catalogRepositoryFake{seatMap: stored}
	service, err := NewCatalogService(repository)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := service.ResolveSeatMap(t.Context(), "stored")
	if err != nil || resolution.Status != contracts.SeatMapResolutionReady ||
		resolution.SeatMap == nil || resolution.SeatMap.ID != stored.ID || repository.requested {
		t.Fatalf("stored ResolveSeatMap() = %+v, requested = %v, error = %v", resolution, repository.requested, err)
	}
	repository.seatMap = contracts.SeatMapVersion{}
	resolution, err = service.ResolveSeatMap(t.Context(), "missing")
	if err != nil || resolution.Status != contracts.SeatMapResolutionWaiting ||
		resolution.SeatMap != nil || !repository.requested {
		t.Fatalf("missing ResolveSeatMap() = %+v, requested = %v, error = %v", resolution, repository.requested, err)
	}
	repository.requested = false
	if err := service.RequestSeatMapBackfill(t.Context(), " auditorium "); err != nil || !repository.requested {
		t.Fatalf("RequestSeatMapBackfill() requested = %v, error = %v", repository.requested, err)
	}
	if err := service.RequestSeatMapBackfill(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank RequestSeatMapBackfill() error = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.ResolveSeatMap(t.Context(), "auditorium"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("seat-map lookup error = %v", err)
	}
	repository.err = nil
	repository.seatMap = contracts.SeatMapVersion{}
	repository.requestError = errInjectedClient
	if _, err := service.ResolveSeatMap(t.Context(), "auditorium"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("seat-map request error = %v", err)
	}
}

func validCatalogSnapshot(observedAt time.Time) contracts.CatalogSnapshot {
	provider := contracts.Provider{ID: contracts.ProviderCGV, Name: "CGV"}
	theaterSourceKey := "0056"
	theater := contracts.Theater{
		ID:         contracts.CatalogID(provider.ID, "theater", theaterSourceKey),
		ProviderID: provider.ID, SourceKey: theaterSourceKey, Region: "서울", Name: "용산아이파크몰",
	}
	movie := contracts.Movie{
		ID:         contracts.CatalogID(provider.ID, "movie", "00001234"),
		ProviderID: provider.ID, SourceKey: "00001234", Title: "테스트 영화",
	}
	auditoriumSourceKey := theaterSourceKey + "/0007"
	auditorium := contracts.Auditorium{
		ID:        contracts.CatalogID(provider.ID, "auditorium", auditoriumSourceKey),
		TheaterID: theater.ID, SourceKey: auditoriumSourceKey, Name: "IMAX관", Capacity: 624,
	}
	showtimeSourceKey := theaterSourceKey + "/2026-08-14/0007/0003"
	showtime := contracts.Showtime{
		ID:         contracts.CatalogID(provider.ID, "showtime", showtimeSourceKey),
		ProviderID: provider.ID, SourceKey: showtimeSourceKey, TheaterID: theater.ID,
		Movie: movie, Auditorium: auditorium,
		StartsAt: observedAt.Add(time.Hour), EndsAt: observedAt.Add(3 * time.Hour), Capacity: 624,
	}
	return contracts.CatalogSnapshot{
		Provider: provider, Theaters: []contracts.Theater{theater}, Movies: []contracts.Movie{movie},
		Auditoriums: []contracts.Auditorium{auditorium}, Showtimes: []contracts.Showtime{showtime},
		ObservedAt: observedAt,
	}
}

type catalogRepositoryFake struct {
	index                  contracts.CatalogIndex
	refresh                CatalogRefreshStatus
	requested              bool
	snapshot               contracts.CatalogSnapshot
	seatMap                contracts.SeatMapVersion
	generation             int64
	authorizedUser         string
	authorizedInstallation string
	authorizedCapability   string
	err                    error
	requestError           error
}

func (repository *catalogRepositoryFake) AuthorizeCatalogWrite(
	_ context.Context,
	userID string,
	installationID string,
	capability string,
) error {
	repository.authorizedUser = userID
	repository.authorizedInstallation = installationID
	repository.authorizedCapability = capability
	return repository.err
}

func (repository *catalogRepositoryFake) Catalog(context.Context) (contracts.CatalogIndex, error) {
	return repository.index, repository.err
}

func (repository *catalogRepositoryFake) CatalogRefreshStatus(
	context.Context,
	time.Time,
	time.Time,
) (CatalogRefreshStatus, error) {
	return repository.refresh, repository.err
}

func (repository *catalogRepositoryFake) RequestCatalogRefresh(context.Context, time.Time) error {
	repository.requested = true
	return repository.err
}

func (repository *catalogRepositoryFake) UpsertCatalogSnapshot(
	_ context.Context,
	snapshot contracts.CatalogSnapshot,
) (int64, error) {
	repository.snapshot = snapshot
	return repository.generation, repository.err
}

func (repository *catalogRepositoryFake) PutSeatMapVersion(
	_ context.Context,
	version contracts.SeatMapVersion,
) (int64, error) {
	repository.seatMap = version
	return repository.generation, repository.err
}

func (repository *catalogRepositoryFake) SeatMapVersion(
	context.Context,
	string,
) (contracts.SeatMapVersion, error) {
	if repository.err != nil {
		return contracts.SeatMapVersion{}, repository.err
	}
	if repository.seatMap.AuditoriumID == "" {
		return contracts.SeatMapVersion{}, ErrNotFound
	}
	return repository.seatMap, repository.err
}

func (repository *catalogRepositoryFake) RequestSeatMapBackfill(context.Context, string, time.Time) error {
	repository.requested = true
	return repository.requestError
}
