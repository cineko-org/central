package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
)

const ProviderCGV = "cgv"

// The catalog Proto carries provider identity in a typed oneof. PostgreSQL
// keeps a compact source_key for indexing, so these helpers are the only
// boundary adapter between the two representations. There is deliberately no
// legacy source_key field fallback.
func TheaterSourceKey(theater *catalogpb.Theater) (string, bool) {
	if theater == nil || theater.GetIdentity() == nil || theater.GetIdentity().GetCgv() == nil {
		return "", false
	}
	value := strings.TrimSpace(theater.GetIdentity().GetCgv().GetSiteNo())
	return value, value != ""
}

func MovieSourceKey(movie *catalogpb.Movie) (string, bool) {
	if movie == nil || movie.GetIdentity() == nil || movie.GetIdentity().GetCgv() == nil {
		return "", false
	}
	value := strings.TrimSpace(movie.GetIdentity().GetCgv().GetMovieNo())
	return value, value != ""
}

func AuditoriumSourceKey(auditorium *catalogpb.Auditorium) (string, bool) {
	if auditorium == nil || auditorium.GetIdentity() == nil || auditorium.GetIdentity().GetCgv() == nil {
		return "", false
	}
	identity := auditorium.GetIdentity().GetCgv()
	siteNo, screenNo := strings.TrimSpace(identity.GetSiteNo()), strings.TrimSpace(identity.GetScreenNo())
	if siteNo == "" || screenNo == "" {
		return "", false
	}
	return siteNo + "/" + screenNo, true
}

func ShowtimeSourceKey(showtime *catalogpb.Showtime) (string, bool) {
	if showtime == nil || showtime.GetIdentity() == nil || showtime.GetIdentity().GetCgv() == nil {
		return "", false
	}
	identity := showtime.GetIdentity().GetCgv()
	siteNo, screenNo, sequence := strings.TrimSpace(identity.GetSiteNo()), strings.TrimSpace(identity.GetScreenNo()), strings.TrimSpace(identity.GetSequence())
	date := identity.GetScheduleDate()
	if siteNo == "" || screenNo == "" || sequence == "" || !validIdentityDate(date) {
		return "", false
	}
	return siteNo + "/" + formatIdentityDate(date) + "/" + screenNo + "/" + sequence, true
}

func SetTheaterSourceKey(theater *catalogpb.Theater, value string) bool {
	value = strings.TrimSpace(value)
	if theater == nil || value == "" {
		return false
	}
	identity := &catalogpb.TheaterIdentity{}
	identity.SetCgv((&catalogpb.CgvTheaterIdentity_builder{SiteNo: &value}).Build())
	theater.SetIdentity(identity)
	return true
}

func SetMovieSourceKey(movie *catalogpb.Movie, value string) bool {
	value = strings.TrimSpace(value)
	if movie == nil || value == "" {
		return false
	}
	identity := &catalogpb.MovieIdentity{}
	identity.SetCgv((&catalogpb.CgvMovieIdentity_builder{MovieNo: &value}).Build())
	movie.SetIdentity(identity)
	return true
}

func SetAuditoriumSourceKey(auditorium *catalogpb.Auditorium, value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if auditorium == nil || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	identity := &catalogpb.AuditoriumIdentity{}
	identity.SetCgv((&catalogpb.CgvAuditoriumIdentity_builder{SiteNo: &parts[0], ScreenNo: &parts[1]}).Build())
	auditorium.SetIdentity(identity)
	return true
}

func SetShowtimeSourceKey(showtime *catalogpb.Showtime, value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if showtime == nil || len(parts) != 4 || parts[0] == "" || parts[2] == "" || parts[3] == "" {
		return false
	}
	date, ok := parseIdentityDate(parts[1])
	if !ok {
		return false
	}
	identity := &catalogpb.ShowtimeIdentity{}
	identity.SetCgv((&catalogpb.CgvShowtimeIdentity_builder{
		SiteNo: &parts[0], ScheduleDate: date, ScreenNo: &parts[2], Sequence: &parts[3],
	}).Build())
	showtime.SetIdentity(identity)
	return true
}

func validIdentityDate(value *commonpb.LocalDate) bool {
	if value == nil || value.GetYear() < 1 || value.GetMonth() < 1 || value.GetMonth() > 12 || value.GetDay() < 1 || value.GetDay() > 31 {
		return false
	}
	return formatIdentityDate(value) != ""
}

func formatIdentityDate(value *commonpb.LocalDate) string {
	if !validIdentityDateFields(value) {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", value.GetYear(), value.GetMonth(), value.GetDay())
}

func validIdentityDateFields(value *commonpb.LocalDate) bool {
	if value == nil || value.GetYear() < 1 || value.GetMonth() < 1 || value.GetMonth() > 12 || value.GetDay() < 1 || value.GetDay() > 31 {
		return false
	}
	return true
}

func parseIdentityDate(value string) (*commonpb.LocalDate, bool) {
	var year, month, day int32
	if _, err := fmt.Sscanf(value, "%04d-%02d-%02d", &year, &month, &day); err != nil || len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return nil, false
	}
	date := &commonpb.LocalDate{}
	date.SetYear(year)
	date.SetMonth(month)
	date.SetDay(day)
	return date, validIdentityDateFields(date) && formatIdentityDate(date) == value
}

// CatalogID derives a stable provider-scoped identity from the upstream key.
func CatalogID(providerID, kind, sourceKey string) string {
	normalized := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(providerID), strings.TrimSpace(kind), strings.TrimSpace(sourceKey),
	}, "\x00"))
	digest := sha256.Sum256([]byte(normalized))
	return strings.TrimSpace(kind) + "_" + hex.EncodeToString(digest[:16])
}

// SeatMapVersionID derives the immutable identity of a normalized layout.
func SeatMapVersionID(auditoriumID, layoutHash string) string {
	return CatalogID("catalog", "seat-map", strings.TrimSpace(auditoriumID)+"\x00"+strings.TrimSpace(layoutHash))
}

// SeatID derives the stable identity of a labeled seat in an auditorium.
func SeatID(auditoriumID, label string) string {
	return CatalogID("catalog", "seat", strings.TrimSpace(auditoriumID)+"\x00"+strings.ToUpper(strings.TrimSpace(label)))
}
