package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MaximumObservationClockSkew is the future tolerance for provider observations.
const MaximumObservationClockSkew = 5 * time.Minute

// NormalizeSnapshot canonicalizes catalog identities and verifies relationships.
func NormalizeSnapshot(snapshot *catalogpb.CatalogSnapshot) error {
	if snapshot == nil || snapshot.GetProvider() == nil {
		return errors.New("catalog snapshot and provider are required")
	}
	provider := snapshot.GetProvider()
	provider.SetId(strings.TrimSpace(provider.GetId()))
	provider.SetName(strings.TrimSpace(provider.GetName()))
	if provider.GetId() == "" || provider.GetName() == "" {
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

// NormalizeSeatMap canonicalizes a layout and derives its stable snapshot identity.
func NormalizeSeatMap(snapshot *seatmappb.Snapshot, now time.Time) error {
	if snapshot == nil || snapshot.GetLayout() == nil {
		return errors.New("seat map snapshot and layout are required")
	}
	if snapshot.GetObservedAt() == nil {
		snapshot.SetObservedAt(timestamppb.New(now.UTC()))
	}
	if err := snapshot.GetObservedAt().CheckValid(); err != nil {
		return errors.New("seat map observation time is invalid")
	}
	if snapshot.GetObservedAt().AsTime().After(now.Add(MaximumObservationClockSkew)) {
		return errors.New("seat map observation is in the future")
	}
	auditoriumID := strings.TrimSpace(snapshot.GetAuditoriumId())
	claimedID := strings.TrimSpace(snapshot.GetId())
	if auditoriumID == "" || snapshot.GetCapacity() < 1 || len(snapshot.GetLayout().GetSeats()) != int(snapshot.GetCapacity()) {
		return errors.New("seat map identity, capacity, and seat count are required")
	}
	snapshot.SetAuditoriumId(auditoriumID)
	if err := normalizeLayout(snapshot.GetLayout(), auditoriumID); err != nil {
		return err
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(snapshot.GetLayout())
	if err != nil {
		return fmt.Errorf("marshal normalized seat map: %w", err)
	}
	digest := sha256.Sum256(canonical)
	layoutHash := hex.EncodeToString(digest[:])
	snapshot.SetLayoutHash(layoutHash)
	snapshot.SetId(SeatMapVersionID(auditoriumID, layoutHash))
	if claimedID != "" && claimedID != snapshot.GetId() {
		return errors.New("seat map snapshot id is not canonical")
	}
	return nil
}

func normalizeTheaters(snapshot *catalogpb.CatalogSnapshot, seen map[string]struct{}) (map[string]*catalogpb.Theater, error) {
	result := make(map[string]*catalogpb.Theater, len(snapshot.GetTheaters()))
	for _, theater := range snapshot.GetTheaters() {
		if theater == nil {
			return nil, errors.New("catalog theater is required")
		}
		theater.SetProviderId(strings.TrimSpace(theater.GetProviderId()))
		theater.SetSourceKey(strings.TrimSpace(theater.GetSourceKey()))
		theater.SetRegion(strings.TrimSpace(theater.GetRegion()))
		theater.SetName(strings.TrimSpace(theater.GetName()))
		theater.SetId(CatalogID(snapshot.GetProvider().GetId(), "theater", theater.GetSourceKey()))
		if theater.GetProviderId() != snapshot.GetProvider().GetId() || theater.GetSourceKey() == "" || theater.GetRegion() == "" || theater.GetName() == "" {
			return nil, errors.New("catalog theater is incomplete")
		}
		if err := rememberID(seen, theater.GetId()); err != nil {
			return nil, err
		}
		result[theater.GetId()] = theater
	}
	return result, nil
}

func normalizeMovies(snapshot *catalogpb.CatalogSnapshot, seen map[string]struct{}) (map[string]*catalogpb.Movie, error) {
	result := make(map[string]*catalogpb.Movie, len(snapshot.GetMovies()))
	for _, movie := range snapshot.GetMovies() {
		if movie == nil {
			return nil, errors.New("catalog movie is required")
		}
		movie.SetProviderId(strings.TrimSpace(movie.GetProviderId()))
		movie.SetSourceKey(strings.TrimSpace(movie.GetSourceKey()))
		movie.SetTitle(strings.TrimSpace(movie.GetTitle()))
		movie.SetPosterUrl(strings.TrimSpace(movie.GetPosterUrl()))
		movie.SetId(CatalogID(snapshot.GetProvider().GetId(), "movie", movie.GetSourceKey()))
		if movie.GetProviderId() != snapshot.GetProvider().GetId() || movie.GetSourceKey() == "" || movie.GetTitle() == "" {
			return nil, errors.New("catalog movie is incomplete")
		}
		if err := rememberID(seen, movie.GetId()); err != nil {
			return nil, err
		}
		result[movie.GetId()] = movie
	}
	return result, nil
}

func normalizeAuditoriums(snapshot *catalogpb.CatalogSnapshot, theaters map[string]*catalogpb.Theater, seen map[string]struct{}) (map[string]*catalogpb.Auditorium, error) {
	result := make(map[string]*catalogpb.Auditorium, len(snapshot.GetAuditoriums()))
	for _, auditorium := range snapshot.GetAuditoriums() {
		if auditorium == nil {
			return nil, errors.New("catalog auditorium is required")
		}
		auditorium.SetTheaterId(strings.TrimSpace(auditorium.GetTheaterId()))
		auditorium.SetSourceKey(strings.TrimSpace(auditorium.GetSourceKey()))
		auditorium.SetName(strings.TrimSpace(auditorium.GetName()))
		auditorium.SetScreenTypes(normalizedStrings(auditorium.GetScreenTypes()))
		auditorium.SetId(CatalogID(snapshot.GetProvider().GetId(), "auditorium", auditorium.GetSourceKey()))
		if theaters[auditorium.GetTheaterId()] == nil || auditorium.GetSourceKey() == "" || auditorium.GetName() == "" || auditorium.GetCapacity() < 0 {
			return nil, errors.New("catalog auditorium is incomplete")
		}
		if err := rememberID(seen, auditorium.GetId()); err != nil {
			return nil, err
		}
		result[auditorium.GetId()] = auditorium
	}
	return result, nil
}

//nolint:gocyclo,cyclop // Cross-entity identity checks are intentionally explicit at the catalog contract boundary.
func normalizeShowtimes(snapshot *catalogpb.CatalogSnapshot, theaters map[string]*catalogpb.Theater, movies map[string]*catalogpb.Movie, auditoriums map[string]*catalogpb.Auditorium, seen map[string]struct{}) error {
	for _, showtime := range snapshot.GetShowtimes() {
		if showtime == nil || showtime.GetMovie() == nil || showtime.GetAuditorium() == nil {
			return errors.New("catalog showtime is required")
		}
		showtime.SetProviderId(strings.TrimSpace(showtime.GetProviderId()))
		showtime.SetSourceKey(strings.TrimSpace(showtime.GetSourceKey()))
		showtime.SetTheaterId(strings.TrimSpace(showtime.GetTheaterId()))
		showtime.SetId(CatalogID(snapshot.GetProvider().GetId(), "showtime", showtime.GetSourceKey()))
		movie := movies[strings.TrimSpace(showtime.GetMovie().GetId())]
		auditorium := auditoriums[strings.TrimSpace(showtime.GetAuditorium().GetId())]
		startsAt, endsAt := showtime.GetStartsAt(), showtime.GetEndsAt()
		if showtime.GetProviderId() != snapshot.GetProvider().GetId() || showtime.GetSourceKey() == "" || theaters[showtime.GetTheaterId()] == nil || movie == nil || auditorium == nil || auditorium.GetTheaterId() != showtime.GetTheaterId() || !validLocalDate(showtime.GetScheduleDate()) || startsAt == nil || endsAt == nil || startsAt.CheckValid() != nil || endsAt.CheckValid() != nil || !endsAt.AsTime().After(startsAt.AsTime()) {
			return errors.New("catalog showtime is incomplete")
		}
		showtime.SetMovie(movie)
		showtime.SetAuditorium(auditorium)
		if err := rememberID(seen, showtime.GetId()); err != nil {
			return err
		}
	}
	return nil
}

// validLocalDate rejects normalized-looking values that time.Date would roll
// into a different civil day.
func validLocalDate(value *commonpb.LocalDate) bool {
	if value == nil || value.GetYear() < 1 || value.GetMonth() < 1 || value.GetMonth() > 12 || value.GetDay() < 1 || value.GetDay() > 31 {
		return false
	}
	date := time.Date(int(value.GetYear()), time.Month(value.GetMonth()), int(value.GetDay()), 0, 0, 0, 0, time.UTC)
	return date.Year() == int(value.GetYear()) && date.Month() == time.Month(value.GetMonth()) && date.Day() == int(value.GetDay())
}

//nolint:gocyclo,cyclop // Seat geometry validation keeps every contract invariant visible in one ordered pass.
func normalizeLayout(layout *seatmappb.Layout, auditoriumID string) error {
	seenIDs := make(map[string]struct{}, len(layout.GetSeats()))
	seenLabels := make(map[string]struct{}, len(layout.GetSeats()))
	for _, seat := range layout.GetSeats() {
		if seat == nil {
			return errors.New("seat map contains an empty seat")
		}
		seat.SetAuditoriumId(strings.TrimSpace(seat.GetAuditoriumId()))
		seat.SetLabel(strings.TrimSpace(seat.GetLabel()))
		seat.SetRow(strings.TrimSpace(seat.GetRow()))
		seat.SetNumber(seat.GetNumber())
		seat.SetX(seat.GetX())
		seat.SetY(seat.GetY())
		seat.SetType(strings.TrimSpace(seat.GetType()))
		seat.SetZoneName(strings.TrimSpace(seat.GetZoneName()))
		seat.SetZoneKind(strings.TrimSpace(seat.GetZoneKind()))
		seat.SetSaleFormCode(strings.TrimSpace(seat.GetSaleFormCode()))
		seat.SetSaleFormName(strings.TrimSpace(seat.GetSaleFormName()))
		seat.SetLeftAisle(seat.GetLeftAisle())
		seat.SetRightAisle(seat.GetRightAisle())
		seat.SetFeatures(normalizedStrings(seat.GetFeatures()))
		seat.SetSourceLabel(strings.TrimSpace(seat.GetSourceLabel()))
		seat.SetSourceSeatKindCode(strings.TrimSpace(seat.GetSourceSeatKindCode()))
		seat.SetSourceSeatKindName(strings.TrimSpace(seat.GetSourceSeatKindName()))
		seat.SetSourceClasses(normalizedStrings(seat.GetSourceClasses()))
		canonicalLabel := seat.GetRow() + strconv.Itoa(int(seat.GetNumber()))
		if seat.GetAuditoriumId() != auditoriumID || seat.GetRow() == "" || seat.GetRow() != strings.ToUpper(seat.GetRow()) || seat.GetNumber() < 1 || seat.GetLabel() != canonicalLabel || seat.GetType() == "" || seat.GetId() != SeatID(auditoriumID, seat.GetLabel()) {
			return errors.New("seat map contains a noncanonical seat")
		}
		if !normalizedCoordinate(seat.GetX()) || !normalizedCoordinate(seat.GetY()) {
			return fmt.Errorf("seat %s position must be finite and normalized to 0..1", seat.GetLabel())
		}
		if _, duplicate := seenIDs[seat.GetId()]; duplicate {
			return errors.New("seat map contains a duplicate seat id")
		}
		if _, duplicate := seenLabels[seat.GetLabel()]; duplicate {
			return errors.New("seat map contains a duplicate seat label")
		}
		seenIDs[seat.GetId()] = struct{}{}
		seenLabels[seat.GetLabel()] = struct{}{}
	}
	for _, zone := range layout.GetZones() {
		if zone == nil || zone.GetCapacity() < 0 || !normalizedBounds(zone.GetMinX(), zone.GetMaxX(), zone.GetMinY(), zone.GetMaxY()) {
			return errors.New("seat map contains an invalid zone")
		}
		zone.SetCode(strings.TrimSpace(zone.GetCode()))
		zone.SetName(strings.TrimSpace(zone.GetName()))
		zone.SetKindCode(strings.TrimSpace(zone.GetKindCode()))
		zone.SetKindName(strings.TrimSpace(zone.GetKindName()))
		zone.SetMinX(zone.GetMinX())
		zone.SetMaxX(zone.GetMaxX())
		zone.SetMinY(zone.GetMinY())
		zone.SetMaxY(zone.GetMaxY())
		zone.SetCapacity(zone.GetCapacity())
	}
	for _, block := range layout.GetBlocks() {
		if block == nil || !normalizedBounds(block.GetMinX(), block.GetMaxX(), block.GetMinY(), block.GetMaxY()) {
			return errors.New("seat map contains an invalid block")
		}
		block.SetCode(strings.TrimSpace(block.GetCode()))
		block.SetName(strings.TrimSpace(block.GetName()))
		block.SetKindCode(strings.TrimSpace(block.GetKindCode()))
		block.SetKindName(strings.TrimSpace(block.GetKindName()))
		block.SetMinX(block.GetMinX())
		block.SetMaxX(block.GetMaxX())
		block.SetMinY(block.GetMinY())
		block.SetMaxY(block.GetMaxY())
	}
	sort.Slice(layout.GetSeats(), func(i, j int) bool { return layout.GetSeats()[i].GetLabel() < layout.GetSeats()[j].GetLabel() })
	sort.Slice(layout.GetZones(), func(i, j int) bool {
		if layout.GetZones()[i].GetCode() == layout.GetZones()[j].GetCode() {
			return layout.GetZones()[i].GetName() < layout.GetZones()[j].GetName()
		}
		return layout.GetZones()[i].GetCode() < layout.GetZones()[j].GetCode()
	})
	sort.Slice(layout.GetBlocks(), func(i, j int) bool {
		if layout.GetBlocks()[i].GetCode() == layout.GetBlocks()[j].GetCode() {
			return layout.GetBlocks()[i].GetName() < layout.GetBlocks()[j].GetName()
		}
		return layout.GetBlocks()[i].GetCode() < layout.GetBlocks()[j].GetCode()
	})
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

func normalizedCoordinate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func normalizedBounds(minX, maxX, minY, maxY float64) bool {
	return normalizedCoordinate(minX) && normalizedCoordinate(maxX) && normalizedCoordinate(minY) && normalizedCoordinate(maxY) && minX <= maxX && minY <= maxY
}

// ValidateObservationTime rejects missing and implausibly future observations.
func ValidateObservationTime(observedAt, now time.Time) error {
	if observedAt.IsZero() {
		return errors.New("catalog observation time is required")
	}
	if observedAt.After(now.Add(MaximumObservationClockSkew)) {
		return errors.New("catalog observation is in the future")
	}
	return nil
}
