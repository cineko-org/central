// Package planning owns the date projection and lane selection rules for
// shared theater observations.
package planning

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"
)

const (
	dateLayout         = time.DateOnly
	maximumWeekdayDays = 365
)

// TaskDataLaneKey names the private task-data field used to persist lane
// ownership without adding a database column.
const TaskDataLaneKey = "_cinekoLane"

// TaskDataHotFingerprintKey names the private task-data field used to detect
// a changed active-demand projection without adding a database column.
const TaskDataHotFingerprintKey = "_cinekoHotFingerprint"

// Lane identifies the purpose of an observation assignment.
type Lane string

const (
	LaneIdle     Lane = "idle"
	LaneHot      Lane = "hot"
	LaneBaseline Lane = "baseline"
)

// MonitorTarget contains only the schedule fields needed to project active
// user demand onto theater-local calendar dates.
type MonitorTarget struct {
	TargetDates       []string `json:"targetDates"`
	TargetWeekdays    []int    `json:"targetWeekdays"`
	SearchHorizonDays int      `json:"searchHorizonDays"`
}

// Input describes one theater's current demand and baseline target horizon.
type Input struct {
	Now                      time.Time
	Location                 *time.Location
	TargetDateMode           string
	ExplicitTargetDates      []string
	HorizonDays              int
	HotTargets               []MonitorTarget
	HotTargetFingerprint     string
	NextRunAt                time.Time
	LastHotFinishedAt        time.Time
	LastHotTargetDates       []string
	LastHotTargetFingerprint string
	HotMinimumInterval       time.Duration
	LastBaselineFinishedAt   time.Time
	BaselineMaximumInterval  time.Duration
	LastBaselineTargetDate   string
}

// Result is the next assignment's lane and target dates.
type Result struct {
	Lane                 Lane
	TargetDates          []string
	HotTargetFingerprint string
}

// Build chooses changed or initial hot demand first, then one owed baseline
// date after a successful hot run, before recurring hot work. The baseline
// cursor keeps ordinary horizon coverage bounded and fair.
func Build(input Input) (Result, error) {
	if input.Location == nil || input.Now.IsZero() {
		return Result{}, errors.New("observation planning clock and location are required")
	}
	baselineDates, err := baselineDates(input)
	if err != nil {
		return Result{}, err
	}
	hotDates, err := projectHotDates(input.HotTargets, input.Now.In(input.Location), input.Location)
	if err != nil {
		return Result{}, err
	}
	if len(hotDates) > 0 {
		fingerprint := input.HotTargetFingerprint
		if fingerprint == "" {
			fingerprint = Fingerprint(input.HotTargets)
		}
		if hotChanged(input.LastHotTargetDates, hotDates, input.LastHotTargetFingerprint, fingerprint) {
			return Result{Lane: LaneHot, TargetDates: hotDates, HotTargetFingerprint: fingerprint}, nil
		}
		// A baseline completed before the latest hot run is owed one bounded
		// date before recurring hot work is allowed to claim the next slot.
		// This closes the fairness gap when a reconcile tick is longer than
		// the hot interval and therefore misses the nominal slack window.
		if baselineDue(input, true) && baselinePendingAfterHot(input) {
			return baselinePlan(baselineDates, input.LastBaselineTargetDate)
		}
		if hotDue(input) {
			return Result{Lane: LaneHot, TargetDates: hotDates, HotTargetFingerprint: fingerprint}, nil
		}
		if !baselineDue(input, true) {
			return Result{Lane: LaneIdle}, nil
		}
		return baselinePlan(baselineDates, input.LastBaselineTargetDate)
	}
	if !baselineDue(input, false) {
		return Result{Lane: LaneIdle}, nil
	}
	return baselinePlan(baselineDates, input.LastBaselineTargetDate)
}

func hotChanged(previous, current []string, previousFingerprint, currentFingerprint string) bool {
	if previousFingerprint != "" && currentFingerprint != "" {
		return previousFingerprint != currentFingerprint
	}
	if len(previous) != len(current) {
		return true
	}
	for index := range current {
		if previous[index] != current[index] {
			return true
		}
	}
	return false
}

// Fingerprint returns a stable hash of the active monitor projection. It
// ignores ordering inside target arrays so equivalent demand remains in the
// same hot lane, while any date/weekday/horizon change preempts baseline work.
func Fingerprint(targets []MonitorTarget) string {
	type canonicalTarget struct {
		TargetDates       []string `json:"targetDates"`
		TargetWeekdays    []int    `json:"targetWeekdays"`
		SearchHorizonDays int      `json:"searchHorizonDays"`
	}
	canonical := make([]canonicalTarget, 0, len(targets))
	for _, target := range targets {
		dates := append([]string(nil), target.TargetDates...)
		weekdays := append([]int(nil), target.TargetWeekdays...)
		sort.Strings(dates)
		slices.Sort(weekdays)
		canonical = append(canonical, canonicalTarget{
			TargetDates: dates, TargetWeekdays: weekdays, SearchHorizonDays: target.SearchHorizonDays,
		})
	}
	sort.Slice(canonical, func(left, right int) bool {
		leftJSON, _ := json.Marshal(canonical[left])
		rightJSON, _ := json.Marshal(canonical[right])
		return string(leftJSON) < string(rightJSON)
	})
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func baselinePlan(dates []string, cursor string) (Result, error) {
	date, ok := nextBaselineDate(dates, cursor)
	if !ok {
		return Result{}, errors.New("observation baseline has no target dates")
	}
	return Result{Lane: LaneBaseline, TargetDates: []string{date}}, nil
}

func baselinePendingAfterHot(input Input) bool {
	return !input.LastHotFinishedAt.IsZero() &&
		(input.LastBaselineFinishedAt.IsZero() || input.LastBaselineFinishedAt.Before(input.LastHotFinishedAt))
}

func hotDue(input Input) bool {
	if input.LastHotFinishedAt.IsZero() || input.HotMinimumInterval <= 0 {
		return true
	}
	return !input.Now.Before(input.LastHotFinishedAt.Add(input.HotMinimumInterval))
}

func baselineDue(input Input, hasHotTargets bool) bool {
	if !hasHotTargets {
		return input.NextRunAt.IsZero() || !input.NextRunAt.After(input.Now)
	}
	if baselinePendingAfterHot(input) {
		return true
	}
	if input.LastBaselineFinishedAt.IsZero() || input.BaselineMaximumInterval <= 0 {
		return true
	}
	return !input.Now.Before(input.LastBaselineFinishedAt.Add(input.BaselineMaximumInterval))
}

func nextBaselineDate(dates []string, cursor string) (string, bool) {
	if len(dates) == 0 {
		return "", false
	}
	if cursor == "" {
		return dates[0], true
	}
	for _, date := range dates {
		if date > cursor {
			return date, true
		}
	}
	return dates[0], true
}

func baselineDates(input Input) ([]string, error) {
	switch input.TargetDateMode {
	case "explicit":
		if len(input.ExplicitTargetDates) == 0 {
			return nil, errors.New("explicit observation policy has no target dates")
		}
		dates, err := uniqueDates(input.ExplicitTargetDates)
		if err != nil {
			return nil, err
		}
		sort.Strings(dates)
		return dates, nil
	case "rolling":
		if input.HorizonDays < 1 || input.HorizonDays > 90 {
			return nil, errors.New("rolling observation horizon is outside 1..90")
		}
		start := input.Now.In(input.Location)
		dates := make([]string, input.HorizonDays)
		for index := range dates {
			dates[index] = start.AddDate(0, 0, index).Format(dateLayout)
		}
		return dates, nil
	default:
		return nil, fmt.Errorf("unsupported observation target date mode %q", input.TargetDateMode)
	}
}

func projectHotDates(targets []MonitorTarget, now time.Time, location *time.Location) ([]string, error) {
	if location == nil {
		return nil, errors.New("observation planning location is required")
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	seen := make(map[string]struct{})
	for _, target := range targets {
		for _, value := range target.TargetDates {
			parsed, err := time.ParseInLocation(dateLayout, value, location)
			if err != nil {
				return nil, fmt.Errorf("invalid monitor target date %q: %w", value, err)
			}
			if !parsed.Before(start) {
				seen[parsed.Format(dateLayout)] = struct{}{}
			}
		}
		if len(target.TargetWeekdays) == 0 {
			continue
		}
		if target.SearchHorizonDays < 1 || target.SearchHorizonDays > maximumWeekdayDays {
			return nil, fmt.Errorf("monitor weekday horizon must be between 1 and %d days", maximumWeekdayDays)
		}
		weekdays := make(map[time.Weekday]struct{}, len(target.TargetWeekdays))
		for _, value := range target.TargetWeekdays {
			weekday := time.Weekday(value)
			if weekday < time.Sunday || weekday > time.Saturday {
				return nil, fmt.Errorf("invalid monitor target weekday %d", value)
			}
			weekdays[weekday] = struct{}{}
		}
		for offset := 0; offset < target.SearchHorizonDays; offset++ {
			date := start.AddDate(0, 0, offset)
			if _, wanted := weekdays[date.Weekday()]; wanted {
				seen[date.Format(dateLayout)] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

// DecodeMonitorTargets decodes the projection emitted by the catalog adapter.
// Strict decoding keeps a changed database projection from silently widening
// the planner's input contract.
func DecodeMonitorTargets(payload []byte) ([]MonitorTarget, error) {
	var targets []MonitorTarget
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&targets); err != nil {
		return nil, fmt.Errorf("decode active monitor target projection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return nil, errors.New("active monitor target projection contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read active monitor target projection: %w", err)
	}
	return targets, nil
}

func uniqueDates(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, err := time.Parse(dateLayout, value); err != nil {
			return nil, fmt.Errorf("invalid target date %q: %w", value, err)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate target date %q", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
