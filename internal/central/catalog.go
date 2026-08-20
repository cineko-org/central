package central

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

const maximumCatalogClockSkew = 5 * time.Minute

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
	if snapshot.ObservedAt.After(now.Add(maximumCatalogClockSkew)) {
		return 0, fmt.Errorf("%w: catalog observation is in the future", ErrInvalid)
	}
	if err := NormalizeCatalogSnapshot(&snapshot); err != nil {
		return 0, err
	}
	return service.repository.UpsertCatalogSnapshot(ctx, snapshot)
}

func (service *CatalogService) PutSeatMapVersion(
	ctx context.Context,
	version contracts.SeatMapVersion,
) (int64, error) {
	now := service.clock().UTC()
	if err := NormalizeSeatMapVersion(&version, now); err != nil {
		return 0, err
	}
	return service.repository.PutSeatMapVersion(ctx, version)
}

func NormalizeSeatMapVersion(version *contracts.SeatMapVersion, now time.Time) error {
	if version == nil {
		return fmt.Errorf("%w: seat map version is required", ErrInvalid)
	}
	if version.ObservedAt.IsZero() {
		version.ObservedAt = now
	}
	version.ObservedAt = version.ObservedAt.UTC()
	if version.ObservedAt.After(now.Add(maximumCatalogClockSkew)) {
		return fmt.Errorf("%w: seat map observation is in the future", ErrInvalid)
	}
	version.AuditoriumID = strings.TrimSpace(version.AuditoriumID)
	claimedID := strings.TrimSpace(version.ID)
	if version.AuditoriumID == "" || version.Capacity < 1 {
		return fmt.Errorf("%w: seat map identity and capacity are required", ErrInvalid)
	}
	canonical, hash, err := canonicalLayout(version.Layout)
	if err != nil {
		return err
	}
	version.Layout, version.LayoutHash = canonical, hash
	version.ID = contracts.SeatMapVersionID(version.AuditoriumID, hash)
	if claimedID != "" && claimedID != version.ID {
		return fmt.Errorf("%w: seat map version id is not canonical", ErrInvalid)
	}
	return nil
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

// NormalizeCatalogSnapshot validates provider identities and cross-entity
// relationships, then replaces caller-supplied IDs with their canonical form.
// Every catalog ingress, including Probe result commits, must pass this boundary.
func NormalizeCatalogSnapshot(snapshot *contracts.CatalogSnapshot) error {
	snapshot.Provider.ID = strings.TrimSpace(snapshot.Provider.ID)
	snapshot.Provider.Name = strings.TrimSpace(snapshot.Provider.Name)
	if snapshot.Provider.ID == "" || snapshot.Provider.Name == "" {
		return fmt.Errorf("%w: catalog provider is incomplete", ErrInvalid)
	}
	seen := make(map[string]struct{})
	theaters, err := normalizeCatalogTheaters(snapshot, seen)
	if err != nil {
		return err
	}
	movies, err := normalizeCatalogMovies(snapshot, seen)
	if err != nil {
		return err
	}
	auditoriums, err := normalizeCatalogAuditoriums(snapshot, theaters, seen)
	if err != nil {
		return err
	}
	return normalizeCatalogShowtimes(snapshot, theaters, movies, auditoriums, seen)
}

func normalizeCatalogTheaters(
	snapshot *contracts.CatalogSnapshot,
	seen map[string]struct{},
) (map[string]contracts.Theater, error) {
	theaters := make(map[string]contracts.Theater, len(snapshot.Theaters))
	for index := range snapshot.Theaters {
		theater := &snapshot.Theaters[index]
		theater.ProviderID = strings.TrimSpace(theater.ProviderID)
		theater.SourceKey = strings.TrimSpace(theater.SourceKey)
		theater.Region = strings.TrimSpace(theater.Region)
		theater.Name = strings.TrimSpace(theater.Name)
		theater.ID = contracts.CatalogID(snapshot.Provider.ID, "theater", theater.SourceKey)
		if theater.ProviderID != snapshot.Provider.ID || theater.SourceKey == "" ||
			theater.Region == "" || theater.Name == "" {
			return nil, fmt.Errorf("%w: catalog theater is incomplete", ErrInvalid)
		}
		if err := rememberCatalogID(seen, theater.ID); err != nil {
			return nil, err
		}
		theaters[theater.ID] = *theater
	}
	return theaters, nil
}

func normalizeCatalogMovies(
	snapshot *contracts.CatalogSnapshot,
	seen map[string]struct{},
) (map[string]contracts.Movie, error) {
	movies := make(map[string]contracts.Movie, len(snapshot.Movies))
	for index := range snapshot.Movies {
		movie := &snapshot.Movies[index]
		movie.ProviderID = strings.TrimSpace(movie.ProviderID)
		movie.SourceKey = strings.TrimSpace(movie.SourceKey)
		movie.Title = strings.TrimSpace(movie.Title)
		movie.PosterURL = strings.TrimSpace(movie.PosterURL)
		movie.ID = contracts.CatalogID(snapshot.Provider.ID, "movie", movie.SourceKey)
		if movie.ProviderID != snapshot.Provider.ID || movie.SourceKey == "" || movie.Title == "" {
			return nil, fmt.Errorf("%w: catalog movie is incomplete", ErrInvalid)
		}
		if err := rememberCatalogID(seen, movie.ID); err != nil {
			return nil, err
		}
		movies[movie.ID] = *movie
	}
	return movies, nil
}

func normalizeCatalogAuditoriums(
	snapshot *contracts.CatalogSnapshot,
	theaters map[string]contracts.Theater,
	seen map[string]struct{},
) (map[string]contracts.Auditorium, error) {
	auditoriums := make(map[string]contracts.Auditorium, len(snapshot.Auditoriums))
	for index := range snapshot.Auditoriums {
		auditorium := &snapshot.Auditoriums[index]
		auditorium.TheaterID = strings.TrimSpace(auditorium.TheaterID)
		auditorium.SourceKey = strings.TrimSpace(auditorium.SourceKey)
		auditorium.Name = strings.TrimSpace(auditorium.Name)
		auditorium.ScreenTypes = normalizedStrings(auditorium.ScreenTypes)
		auditorium.ID = contracts.CatalogID(snapshot.Provider.ID, "auditorium", auditorium.SourceKey)
		if _, known := theaters[auditorium.TheaterID]; !known || auditorium.SourceKey == "" || auditorium.Name == "" ||
			auditorium.Capacity < 0 {
			return nil, fmt.Errorf("%w: catalog auditorium is incomplete", ErrInvalid)
		}
		if err := rememberCatalogID(seen, auditorium.ID); err != nil {
			return nil, err
		}
		auditoriums[auditorium.ID] = *auditorium
	}
	return auditoriums, nil
}

func normalizeCatalogShowtimes(
	snapshot *contracts.CatalogSnapshot,
	theaters map[string]contracts.Theater,
	movies map[string]contracts.Movie,
	auditoriums map[string]contracts.Auditorium,
	seen map[string]struct{},
) error {
	for index := range snapshot.Showtimes {
		showtime := &snapshot.Showtimes[index]
		showtime.ProviderID = strings.TrimSpace(showtime.ProviderID)
		showtime.SourceKey = strings.TrimSpace(showtime.SourceKey)
		showtime.TheaterID = strings.TrimSpace(showtime.TheaterID)
		showtime.ID = contracts.CatalogID(snapshot.Provider.ID, "showtime", showtime.SourceKey)
		movie, movieKnown := movies[strings.TrimSpace(showtime.Movie.ID)]
		auditorium, auditoriumKnown := auditoriums[strings.TrimSpace(showtime.Auditorium.ID)]
		_, theaterKnown := theaters[showtime.TheaterID]
		if showtime.ProviderID != snapshot.Provider.ID || showtime.SourceKey == "" || !theaterKnown ||
			!movieKnown || !auditoriumKnown || auditorium.TheaterID != showtime.TheaterID ||
			showtime.StartsAt.IsZero() || !showtime.EndsAt.After(showtime.StartsAt) {
			return fmt.Errorf("%w: catalog showtime is incomplete", ErrInvalid)
		}
		showtime.Movie = movie
		showtime.Auditorium = auditorium
		if err := rememberCatalogID(seen, showtime.ID); err != nil {
			return err
		}
	}
	return nil
}

func rememberCatalogID(seen map[string]struct{}, id string) error {
	if _, duplicate := seen[id]; duplicate {
		return fmt.Errorf("%w: duplicate catalog entity", ErrInvalid)
	}
	seen[id] = struct{}{}
	return nil
}

func normalizedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func canonicalLayout(raw json.RawMessage) (json.RawMessage, string, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, "", fmt.Errorf("%w: seat map layout must be a JSON object", ErrInvalid)
	}
	// Values produced by decoding JSON into map[string]any are always JSON
	// encodable, so there is no second failure mode after Unmarshal succeeds.
	canonical, _ := json.Marshal(value)
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}
