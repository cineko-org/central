package domain

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/cineko-org/central/internal/support/numeric"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
)

const DefaultSearchHorizonDays = 14

// ValidateMonitor enforces Central's domain invariants on the canonical Proto monitor.
func ValidateMonitor(monitor *clientpb.Monitor) error {
	if monitor == nil {
		return errors.New("monitor is required")
	}
	if monitor.GetId() == "" || monitor.GetUserId() == "" || monitor.GetPresetId() == "" {
		return errors.New("monitor id, user id, and preset id are required")
	}
	if monitor.GetMovieId() == "" || len(monitor.GetTargetDates())+len(monitor.GetTargetWeekdays()) == 0 {
		return errors.New("monitor movie id and at least one target date or weekday are required")
	}
	horizon := int(monitor.GetSearchHorizonDays())
	if horizon < 1 || horizon > DefaultSearchHorizonDays {
		return fmt.Errorf("search horizon must be between 1 and %d days", DefaultSearchHorizonDays)
	}
	if err := validateTargetDates(monitor.GetTargetDates()); err != nil {
		return err
	}
	if err := validateTargetWeekdays(monitor.GetTargetWeekdays()); err != nil {
		return err
	}
	return validateTimeWindow(monitor.GetEarliestTime(), monitor.GetLatestTime())
}

func validateTargetDates(dates []*commonpb.LocalDate) error {
	seen := make(map[string]struct{}, len(dates))
	for _, date := range dates {
		parsed, err := localDate(date, time.UTC)
		if err != nil {
			return err
		}
		key := parsed.Format(time.DateOnly)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate target date %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateTargetWeekdays(weekdays []int32) error {
	seen := make(map[int32]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		if weekday < int32(time.Sunday) || weekday > int32(time.Saturday) {
			return fmt.Errorf("invalid target weekday %d", weekday)
		}
		if _, duplicate := seen[weekday]; duplicate {
			return fmt.Errorf("duplicate target weekday %d", weekday)
		}
		seen[weekday] = struct{}{}
	}
	return nil
}

func validateTimeWindow(earliest, latest *commonpb.LocalTime) error {
	start, startSet, startValid := localMinutes(earliest)
	end, endSet, endValid := localMinutes(latest)
	if !startValid || !endValid {
		return errors.New("monitor time window is invalid")
	}
	if startSet && endSet && start == end {
		return errors.New("time window cannot be empty")
	}
	return nil
}

// MonitorMatchesSchedule applies exact dates, weekdays, and overnight time windows.
func MonitorMatchesSchedule(
	monitor *clientpb.Monitor,
	start time.Time,
	now time.Time,
	location *time.Location,
) bool {
	if monitor == nil || location == nil || start.IsZero() {
		return false
	}
	civilDate := start.In(location).Format(time.DateOnly)
	return MonitorMatchesScheduleDate(monitor, civilDate, start, now, location)
}

// MonitorMatchesScheduleDate applies a provider-owned schedule date when it is
// available. Providers can publish a showtime after midnight while retaining
// the prior schedule date; using the civil start date in that case would miss a
// valid Client target.
func MonitorMatchesScheduleDate(
	monitor *clientpb.Monitor,
	targetDate string,
	start time.Time,
	now time.Time,
	location *time.Location,
) bool {
	if monitor == nil || location == nil || start.IsZero() || targetDate == "" {
		return false
	}
	return slices.Contains(MonitorTargetDates(monitor, now.In(location)), targetDate) &&
		monitorMatchesTimeWindow(monitor, start, location)
}

func monitorMatchesTimeWindow(monitor *clientpb.Monitor, start time.Time, location *time.Location) bool {
	earliest, hasEarliest, earliestValid := localMinutes(monitor.GetEarliestTime())
	latest, hasLatest, latestValid := localMinutes(monitor.GetLatestTime())
	if !earliestValid || !latestValid {
		return false
	}
	if !hasEarliest && !hasLatest {
		return true
	}
	localStart := start.In(location)
	minutes := localStart.Hour()*60 + localStart.Minute()
	if !hasEarliest {
		return minutes < latest
	}
	if !hasLatest {
		return minutes >= earliest
	}
	if earliest < latest {
		return minutes >= earliest && minutes < latest
	}
	return minutes >= earliest || minutes < latest
}

// MonitorTargetDates resolves explicit dates and weekday rules into local calendar dates.
func MonitorTargetDates(monitor *clientpb.Monitor, now time.Time) []string {
	if monitor == nil {
		return nil
	}
	horizon := int(monitor.GetSearchHorizonDays())
	seen := make(map[string]struct{}, len(monitor.GetTargetDates())+horizon)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, value := range monitor.GetTargetDates() {
		parsed, err := localDate(value, now.Location())
		if err == nil && !parsed.Before(today) {
			seen[parsed.Format(time.DateOnly)] = struct{}{}
		}
	}
	for offset := 0; offset < horizon && len(monitor.GetTargetWeekdays()) > 0; offset++ {
		candidate := today.AddDate(0, 0, offset)
		if slices.Contains(monitor.GetTargetWeekdays(), numeric.ClampInt32(int(candidate.Weekday()))) {
			seen[candidate.Format(time.DateOnly)] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// MonitorExpired reports whether every explicit date is before today.
func MonitorExpired(monitor *clientpb.Monitor, now time.Time) bool {
	if monitor == nil || len(monitor.GetTargetWeekdays()) > 0 || len(monitor.GetTargetDates()) == 0 {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, value := range monitor.GetTargetDates() {
		parsed, err := localDate(value, now.Location())
		if err == nil && !parsed.Before(today) {
			return false
		}
	}
	return true
}

func localDate(value *commonpb.LocalDate, location *time.Location) (time.Time, error) {
	if value == nil || location == nil {
		return time.Time{}, errors.New("target date is required")
	}
	parsed := time.Date(int(value.GetYear()), time.Month(value.GetMonth()), int(value.GetDay()), 0, 0, 0, 0, location)
	if parsed.Year() != int(value.GetYear()) || numeric.ClampInt32(int(parsed.Month())) != value.GetMonth() || numeric.ClampInt32(parsed.Day()) != value.GetDay() {
		return time.Time{}, fmt.Errorf("invalid target date %04d-%02d-%02d", value.GetYear(), value.GetMonth(), value.GetDay())
	}
	return parsed, nil
}

func localMinutes(value *commonpb.LocalTime) (minutes int, set bool, valid bool) {
	if value == nil {
		return 0, false, true
	}
	if value.GetHour() < 0 || value.GetHour() > 23 || value.GetMinute() < 0 || value.GetMinute() > 59 {
		return 0, true, false
	}
	return int(value.GetHour()*60 + value.GetMinute()), true, true
}
