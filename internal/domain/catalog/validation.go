package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

// MaximumObservationClockSkew is the future tolerance for provider and seat
// map observations accepted by Central.
const MaximumObservationClockSkew = 5 * time.Minute

// NormalizeSnapshot canonicalizes catalog identities and verifies all
// provider, theater, movie, auditorium, and showtime relationships.
func NormalizeSnapshot(snapshot *contracts.CatalogSnapshot) error {
	if snapshot == nil {
		return errors.New("catalog snapshot is required")
	}
	snapshot.Provider.ID = strings.TrimSpace(snapshot.Provider.ID)
	snapshot.Provider.Name = strings.TrimSpace(snapshot.Provider.Name)
	if snapshot.Provider.ID == "" || snapshot.Provider.Name == "" {
		return errors.New("catalog provider is incomplete")
	}
	seen := make(map[string]struct{})
	theaters, err := normalizeTheaters(snapshot, seen)
	if err != nil {
		return err
	}
	movies, err := normalizeMovies(snapshot, seen)
	if err != nil {
		return err
	}
	auditoriums, err := normalizeAuditoriums(snapshot, theaters, seen)
	if err != nil {
		return err
	}
	return normalizeShowtimes(snapshot, theaters, movies, auditoriums, seen)
}

// NormalizeSeatMapVersion canonicalizes layout bytes and derives the stable
// seat-map version ID from the auditorium and canonical layout hash.
func NormalizeSeatMapVersion(version *contracts.SeatMapVersion, now time.Time) error {
	if version == nil {
		return errors.New("seat map version is required")
	}
	if version.ObservedAt.IsZero() {
		version.ObservedAt = now
	}
	version.ObservedAt = version.ObservedAt.UTC()
	if version.ObservedAt.After(now.Add(MaximumObservationClockSkew)) {
		return errors.New("seat map observation is in the future")
	}
	version.AuditoriumID = strings.TrimSpace(version.AuditoriumID)
	claimedID := strings.TrimSpace(version.ID)
	if version.AuditoriumID == "" || version.Capacity < 1 {
		return errors.New("seat map identity and capacity are required")
	}
	canonical, hash, err := canonicalLayout(version.Layout)
	if err != nil {
		return err
	}
	version.Layout, version.LayoutHash = canonical, hash
	version.ID = contracts.SeatMapVersionID(version.AuditoriumID, hash)
	if claimedID != "" && claimedID != version.ID {
		return errors.New("seat map version id is not canonical")
	}
	return nil
}

func normalizeTheaters(
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
			return nil, errors.New("catalog theater is incomplete")
		}
		if err := rememberID(seen, theater.ID); err != nil {
			return nil, err
		}
		theaters[theater.ID] = *theater
	}
	return theaters, nil
}

func normalizeMovies(
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
			return nil, errors.New("catalog movie is incomplete")
		}
		if err := rememberID(seen, movie.ID); err != nil {
			return nil, err
		}
		movies[movie.ID] = *movie
	}
	return movies, nil
}

func normalizeAuditoriums(
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
			return nil, errors.New("catalog auditorium is incomplete")
		}
		if err := rememberID(seen, auditorium.ID); err != nil {
			return nil, err
		}
		auditoriums[auditorium.ID] = *auditorium
	}
	return auditoriums, nil
}

func normalizeShowtimes(
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
			return errors.New("catalog showtime is incomplete")
		}
		showtime.Movie = movie
		showtime.Auditorium = auditorium
		if err := rememberID(seen, showtime.ID); err != nil {
			return err
		}
	}
	return nil
}

func rememberID(seen map[string]struct{}, id string) error {
	if _, duplicate := seen[id]; duplicate {
		return errors.New("duplicate catalog entity")
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
		return nil, "", errors.New("seat map layout must be a JSON object")
	}
	// A successful decode into map[string]any is always JSON encodable.
	canonical, _ := json.Marshal(value)
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

// ValidateObservationTime checks whether a non-zero observation timestamp is
// within the catalog provider's accepted future tolerance.
func ValidateObservationTime(observedAt, now time.Time) error {
	if !observedAt.IsZero() && observedAt.After(now.Add(MaximumObservationClockSkew)) {
		return errors.New("catalog observation is in the future")
	}
	return nil
}
