package postgres

import (
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestExecutionTargetMatchesCanonicalProbeShowtime(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, location)
	monitor := &clientpb.Monitor{}
	monitor.SetMovieId("movie_cgv_123")
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(12)
	monitor.SetTargetDates([]*commonpb.LocalDate{date})
	earliest := &commonpb.LocalTime{}
	earliest.SetHour(19)
	earliest.SetMinute(0)
	monitor.SetEarliestTime(earliest)
	latest := &commonpb.LocalTime{}
	latest.SetHour(21)
	latest.SetMinute(0)
	monitor.SetLatestTime(latest)
	preset := &clientpb.Preset{}
	preset.SetAuditoriumId("d90f7eaef3a2a67b6d2e81e8")
	target := executionTarget{
		monitor: monitor,
		preset:  preset,
	}
	movie := &catalogpb.Movie{}
	movie.SetId("movie_cgv_123")
	movie.SetTitle("명탐정 코난-하이웨이의 타천사")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId("d90f7eaef3a2a67b6d2e81e8")
	auditorium.SetName("6관 (Laser)")
	showtime := &catalogpb.Showtime{}
	showtime.SetId("9238317d2a1589ed7c5d3241")
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetStartsAt(timestamppb.New(time.Date(2026, 8, 12, 19, 45, 0, 0, location)))
	if !executionTargetMatches(target, "2026-08-12", showtime, now, location) {
		t.Fatal("canonical Probe showtime did not match the stored Client target")
	}

	tests := []struct {
		name   string
		mutate func(*catalogpb.Showtime)
		date   string
		want   bool
	}{
		{name: "auditorium", mutate: func(value *catalogpb.Showtime) { value.GetAuditorium().SetId("other") }, date: "2026-08-12", want: false},
		{name: "movie title snapshot", mutate: func(value *catalogpb.Showtime) { value.GetMovie().SetTitle("다른 영화") }, date: "2026-08-12", want: true},
		{name: "movie identity", mutate: func(value *catalogpb.Showtime) { value.GetMovie().SetId("other") }, date: "2026-08-12", want: false},
		{name: "missing movie identity", mutate: func(value *catalogpb.Showtime) { value.GetMovie().SetId("") }, date: "2026-08-12", want: false},
		{name: "date", mutate: func(*catalogpb.Showtime) {}, date: "2026-08-13", want: false},
		{name: "time", mutate: func(value *catalogpb.Showtime) {
			value.SetStartsAt(timestamppb.New(value.GetStartsAt().AsTime().Add(2 * time.Hour)))
		}, date: "2026-08-12", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := proto.CloneOf(showtime)
			test.mutate(value)
			if got := executionTargetMatches(target, test.date, value, now, location); got != test.want {
				t.Fatalf("executionTargetMatches() = %t, want %t", got, test.want)
			}
		})
	}
}
