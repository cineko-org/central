package postgres

import (
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain"
)

func TestExecutionTargetMatchesCanonicalProbeShowtime(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, location)
	target := executionTarget{
		monitor: domain.MonitorJob{
			MovieID: "movie-1", Movie: "명탐정 코난-하이웨이의 타천사", TargetDates: []string{"2026-08-12"},
			EarliestTime: "19:00", LatestTime: "21:00",
		},
		preset: domain.Preset{AuditoriumID: "d90f7eaef3a2a67b6d2e81e8"},
	}
	showtime := central.Showtime{
		ID:         "9238317d2a1589ed7c5d3241",
		Movie:      central.Movie{ID: "movie-1", Title: "명탐정 코난-하이웨이의 타천사"},
		Auditorium: central.Auditorium{ID: "d90f7eaef3a2a67b6d2e81e8", Name: "6관 (Laser)"},
		StartsAt:   time.Date(2026, 8, 12, 19, 45, 0, 0, location),
	}
	if !executionTargetMatches(target, "2026-08-12", showtime, now, location) {
		t.Fatal("canonical Probe showtime did not match the stored Client target")
	}

	tests := []struct {
		name   string
		mutate func(*central.Showtime)
		date   string
	}{
		{name: "auditorium", mutate: func(value *central.Showtime) { value.Auditorium.ID = "other" }, date: "2026-08-12"},
		{name: "movie identity", mutate: func(value *central.Showtime) { value.Movie.ID = "other-movie" }, date: "2026-08-12"},
		{name: "date", mutate: func(value *central.Showtime) { value.StartsAt = value.StartsAt.AddDate(0, 0, 1) }, date: "2026-08-13"},
		{name: "time", mutate: func(value *central.Showtime) { value.StartsAt = value.StartsAt.Add(2 * time.Hour) }, date: "2026-08-12"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := showtime
			test.mutate(&value)
			if executionTargetMatches(target, test.date, value, now, location) {
				t.Fatal("mismatched showtime was accepted")
			}
		})
	}
	value := showtime
	value.Movie.Title = "제목이 바뀐 영화"
	if !executionTargetMatches(target, "2026-08-12", value, now, location) {
		t.Fatal("showtime title change with the same movie identity was rejected")
	}
	target.monitor.EarliestTime, target.monitor.LatestTime = "21:00", "06:00"
	showtime.StartsAt = time.Date(2026, 8, 12, 1, 0, 0, 0, location)
	if !executionTargetMatches(target, "2026-08-12", showtime, now, location) {
		t.Fatal("cross-midnight showtime did not match the stored Client target")
	}
	// CGV may report Friday's scan day for a Saturday 01:00 show. Matching uses
	// the actual Seoul calendar date, not the provider grouping date.
	target.monitor.TargetDates = []string{"2026-08-22"}
	showtime.StartsAt = time.Date(2026, 8, 22, 1, 0, 0, 0, location)
	if !executionTargetMatches(target, "2026-08-21", showtime, now, location) {
		t.Fatal("Saturday monitor did not match a Friday-grouped Saturday 01:00 showtime")
	}
	target.monitor.TargetDates = []string{"2026-08-21"}
	if executionTargetMatches(target, "2026-08-21", showtime, now, location) {
		t.Fatal("Friday monitor matched a Saturday 01:00 showtime")
	}
}
