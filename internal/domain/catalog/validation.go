package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
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
	canonical, hash, err := canonicalLayout(version.Layout, version.AuditoriumID, version.Capacity)
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

func canonicalLayout(raw json.RawMessage, auditoriumID string, capacity int) (json.RawMessage, string, error) {
	var layout contracts.SeatMapLayout
	if err := json.Unmarshal(raw, &layout); err != nil {
		return nil, "", errors.New("seat map layout must use the canonical schema")
	}
	if len(layout.Seats) != capacity {
		return nil, "", fmt.Errorf("seat map contains %d seats, expected %d", len(layout.Seats), capacity)
	}
	if err := normalizeLayout(&layout, auditoriumID); err != nil {
		return nil, "", err
	}
	// A successfully validated contract layout is always JSON encodable.
	canonical, _ := json.Marshal(layout)
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

func normalizeLayout(layout *contracts.SeatMapLayout, auditoriumID string) error {
	seenIDs := make(map[string]struct{}, len(layout.Seats))
	seenLabels := make(map[string]struct{}, len(layout.Seats))
	for index := range layout.Seats {
		if err := normalizeSeat(&layout.Seats[index], auditoriumID, seenIDs, seenLabels); err != nil {
			return err
		}
	}
	if err := validateLayoutGroups(layout); err != nil {
		return err
	}
	sortLayout(layout)
	return nil
}

func normalizeSeat(
	seat *contracts.SeatMapSeat,
	auditoriumID string,
	seenIDs map[string]struct{},
	seenLabels map[string]struct{},
) error {
	seat.AuditoriumID = strings.TrimSpace(seat.AuditoriumID)
	seat.Label = strings.TrimSpace(seat.Label)
	seat.Row = strings.TrimSpace(seat.Row)
	seat.Type = strings.TrimSpace(seat.Type)
	seat.Features = normalizedStrings(seat.Features)
	seat.SourceClasses = normalizedStrings(seat.SourceClasses)
	canonicalLabel := seat.Row + strconv.Itoa(seat.Number)
	if seat.AuditoriumID != auditoriumID || seat.Row == "" || seat.Row != strings.ToUpper(seat.Row) ||
		seat.Number < 1 || seat.Label != canonicalLabel || seat.Type == "" ||
		seat.ID != contracts.SeatID(auditoriumID, seat.Label) {
		return errors.New("seat map contains a noncanonical seat")
	}
	if !normalizedCoordinate(seat.X) || !normalizedCoordinate(seat.Y) {
		return fmt.Errorf("seat %s position must be finite and normalized to 0..1", seat.Label)
	}
	if _, duplicate := seenIDs[seat.ID]; duplicate {
		return errors.New("seat map contains a duplicate seat id")
	}
	if _, duplicate := seenLabels[seat.Label]; duplicate {
		return errors.New("seat map contains a duplicate seat label")
	}
	seenIDs[seat.ID] = struct{}{}
	seenLabels[seat.Label] = struct{}{}
	return nil
}

func validateLayoutGroups(layout *contracts.SeatMapLayout) error {
	for _, zone := range layout.Zones {
		if zone.Capacity < 0 || !normalizedBounds(zone.MinX, zone.MaxX, zone.MinY, zone.MaxY) {
			return errors.New("seat map contains an invalid zone")
		}
	}
	for _, block := range layout.Blocks {
		if !normalizedBounds(block.MinX, block.MaxX, block.MinY, block.MaxY) {
			return errors.New("seat map contains an invalid block")
		}
	}
	return nil
}

func sortLayout(layout *contracts.SeatMapLayout) {
	sort.Slice(layout.Seats, func(i, j int) bool { return layout.Seats[i].Label < layout.Seats[j].Label })
	sort.Slice(layout.Zones, func(i, j int) bool {
		if layout.Zones[i].Code == layout.Zones[j].Code {
			return layout.Zones[i].Name < layout.Zones[j].Name
		}
		return layout.Zones[i].Code < layout.Zones[j].Code
	})
	sort.Slice(layout.Blocks, func(i, j int) bool {
		if layout.Blocks[i].Code == layout.Blocks[j].Code {
			return layout.Blocks[i].Name < layout.Blocks[j].Name
		}
		return layout.Blocks[i].Code < layout.Blocks[j].Code
	})
}

func normalizedCoordinate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func normalizedBounds(minX, maxX, minY, maxY float64) bool {
	return normalizedCoordinate(minX) && normalizedCoordinate(maxX) &&
		normalizedCoordinate(minY) && normalizedCoordinate(maxY) && minX <= maxX && minY <= maxY
}

// ValidateObservationTime checks whether a non-zero observation timestamp is
// within the catalog provider's accepted future tolerance.
func ValidateObservationTime(observedAt, now time.Time) error {
	if !observedAt.IsZero() && observedAt.After(now.Add(MaximumObservationClockSkew)) {
		return errors.New("catalog observation is in the future")
	}
	return nil
}
