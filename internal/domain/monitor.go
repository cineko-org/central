package domain

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

const DefaultSearchHorizonDays = 14

type MonitorMode string

const (
	MonitorModeOpening      MonitorMode = "opening"
	MonitorModeCancellation MonitorMode = "cancellation"
)

type MonitorStatus string

const (
	MonitorPending   MonitorStatus = "pending"
	MonitorRunning   MonitorStatus = "running"
	MonitorTriggered MonitorStatus = "triggered"
	MonitorBooked    MonitorStatus = "booked"
	MonitorFailed    MonitorStatus = "failed"
	MonitorStopped   MonitorStatus = "stopped"
)

type MonitorJob struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	PresetID string `json:"presetId"`
	// MovieID is the canonical catalog identity. Movie title is retained only
	// for display and is never authoritative for execution matching.
	MovieID           string        `json:"movieId"`
	Mode              MonitorMode   `json:"mode"`
	Movie             string        `json:"movie"`
	TargetDates       []string      `json:"targetDates"`
	TargetWeekdays    []int         `json:"targetWeekdays"`
	SearchHorizonDays int           `json:"searchHorizonDays"`
	EarliestTime      string        `json:"earliestTime"`
	LatestTime        string        `json:"latestTime"`
	PollInterval      time.Duration `json:"pollInterval"`
	PollIntervalMax   time.Duration `json:"pollIntervalMax"`
	Status            MonitorStatus `json:"status"`
	LastCheckedAt     *time.Time    `json:"lastCheckedAt"`
	LastError         string        `json:"lastError"`
	ReservationID     string        `json:"reservationId"`
	CreatedAt         time.Time     `json:"createdAt"`
	UpdatedAt         time.Time     `json:"updatedAt"`
}

func (job MonitorJob) Validate() error {
	if job.ID == "" || job.UserID == "" || job.PresetID == "" {
		return errors.New("monitor id, user id, and preset id are required")
	}
	if strings.TrimSpace(job.MovieID) == "" || strings.TrimSpace(job.Movie) == "" ||
		len(job.TargetDates)+len(job.TargetWeekdays) == 0 {
		return errors.New("monitor movie id, movie title, and at least one target date or weekday are required")
	}
	if err := job.validateMode(); err != nil {
		return err
	}
	if job.PollInterval < 2*time.Second {
		return errors.New("poll interval must be at least 2 seconds")
	}
	if job.EffectivePollIntervalMax() <= job.PollInterval {
		return errors.New("maximum poll interval must be greater than minimum poll interval")
	}
	if err := validateTargetDates(job.TargetDates); err != nil {
		return err
	}
	if err := validateTargetWeekdays(job.TargetWeekdays, job.SearchHorizonDays); err != nil {
		return err
	}
	return validateTimeWindow(job.EarliestTime, job.LatestTime)
}

func (job MonitorJob) validateMode() error {
	if mode := job.EffectiveMode(); mode != MonitorModeOpening && mode != MonitorModeCancellation {
		return fmt.Errorf("invalid monitor mode %q", job.Mode)
	}
	if job.EffectiveMode() == MonitorModeCancellation && len(job.TargetWeekdays) > 0 {
		return errors.New("cancellation-seat monitors require exact target dates")
	}
	return nil
}

func validateTargetDates(dates []string) error {
	for _, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("invalid target date %q: %w", date, err)
		}
	}
	return nil
}

func validateTargetWeekdays(weekdays []int, horizon int) error {
	seen := make(map[int]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		if weekday < int(time.Sunday) || weekday > int(time.Saturday) {
			return fmt.Errorf("invalid target weekday %d", weekday)
		}
		if _, duplicate := seen[weekday]; duplicate {
			return fmt.Errorf("duplicate target weekday %d", weekday)
		}
		seen[weekday] = struct{}{}
	}
	if len(weekdays) > 0 && (horizon < 1 || horizon > DefaultSearchHorizonDays) {
		return fmt.Errorf("weekday search horizon must be between 1 and %d days", DefaultSearchHorizonDays)
	}
	return nil
}

func validateTimeWindow(earliest, latest string) error {
	for name, value := range map[string]string{"earliest time": earliest, "latest time": latest} {
		if value == "" {
			continue
		}
		if _, ok := parseClockMinutes(value); !ok {
			return fmt.Errorf("invalid %s %q: expected HH:MM", name, value)
		}
	}
	if earliest != "" && latest != "" && earliest == latest {
		return errors.New("time window cannot be empty")
	}
	return nil
}

// TimeWindowContains reports whether a showtime start belongs to the monitor's
// half-open time window [earliest, latest). A missing bound is unbounded. When
// both bounds exist and earliest is later than latest, the window crosses
// midnight and is the union [earliest, 24:00) + [00:00, latest).
func TimeWindowContains(showtime, earliest, latest string) bool {
	minute, ok := parseClockMinutes(showtime)
	if !ok {
		return false
	}
	start, hasStart := parseClockMinutes(earliest)
	end, hasEnd := parseClockMinutes(latest)
	if (earliest != "" && !hasStart) || (latest != "" && !hasEnd) {
		return false
	}
	if !hasStart && !hasEnd {
		return true
	}
	if hasStart && !hasEnd {
		return minute >= start
	}
	if !hasStart && hasEnd {
		return minute < end
	}
	if start < end {
		return minute >= start && minute < end
	}
	if start > end {
		return minute >= start || minute < end
	}
	// Equal bounds are an empty half-open interval.
	return false
}

func parseClockMinutes(value string) (int, bool) {
	if len(value) != len("15:04") || value[2] != ':' {
		return 0, false
	}
	for index, character := range value {
		if index == 2 {
			continue
		}
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func (job MonitorJob) ResolveTargetDates(now time.Time) []string {
	seen := make(map[string]struct{}, len(job.TargetDates)+job.SearchHorizonDays)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, value := range job.TargetDates {
		parsed, err := time.ParseInLocation("2006-01-02", value, now.Location())
		if err == nil && !parsed.Before(today) {
			seen[value] = struct{}{}
		}
	}
	if len(job.TargetWeekdays) > 0 {
		for offset := 0; offset < job.SearchHorizonDays; offset++ {
			candidate := today.AddDate(0, 0, offset)
			if slices.Contains(job.TargetWeekdays, int(candidate.Weekday())) {
				seen[candidate.Format("2006-01-02")] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (job MonitorJob) EffectiveMode() MonitorMode {
	if job.Mode == "" {
		return MonitorModeOpening
	}
	return job.Mode
}

func (job MonitorJob) EffectivePollIntervalMax() time.Duration {
	if job.PollIntervalMax > 0 {
		return job.PollIntervalMax
	}
	return job.PollInterval + job.PollInterval/5
}

func (job MonitorJob) Expired(now time.Time) bool {
	if len(job.TargetWeekdays) > 0 || len(job.TargetDates) == 0 {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, value := range job.TargetDates {
		parsed, err := time.ParseInLocation("2006-01-02", value, now.Location())
		if err == nil && !parsed.Before(today) {
			return false
		}
	}
	return true
}

func (job *MonitorJob) Transition(status MonitorStatus, now time.Time) {
	job.Status = status
	job.UpdatedAt = now
}

func (job *MonitorJob) RecordCheck(now time.Time, err error) {
	job.LastCheckedAt = &now
	job.UpdatedAt = now
	if err == nil {
		job.LastError = ""
		return
	}
	job.LastError = err.Error()
}
