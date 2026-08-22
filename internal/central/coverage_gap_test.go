package central

import (
	"errors"
	"testing"
	"time"

	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateResultSeatAvailabilityAndCaptureDateBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	completed := &observationpb.Completed{}
	completed.SetLiveSeat((&seatmappb.LiveSeatObservation_builder{
		Layout: &seatmappb.Snapshot{}, Availability: &seatmappb.AvailabilitySnapshot{},
	}).Build())
	result := &observationpb.AssignmentResult{}
	result.SetRunId("availability_run")
	result.SetStartedAt(timestamppb.New(now))
	result.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
	result.SetCompleted(completed)
	if err := validateResult(result); err != nil {
		t.Fatalf("seat-availability result rejected: %v", err)
	}

	mismatched := validResult(now)
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(21)
	mismatched.GetCompleted().GetSchedule().GetCaptures()[0].GetShowtimes()[0].GetIdentity().GetCgv().SetScheduleDate(date)
	if err := validateResult(mismatched); err == nil {
		t.Fatal("capture with mismatched showtime date was accepted")
	}

	invalidDate := validResult(now)
	invalidDate.GetCompleted().GetSchedule().GetCaptures()[0].GetShowtimes()[0].GetIdentity().GetCgv().ClearScheduleDate()
	if err := validateResult(invalidDate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid local date error = %v, want ErrInvalid", err)
	}

	invalidYear := proto.CloneOf(date)
	invalidYear.SetYear(0)
	if validLocalDate(invalidYear) {
		t.Fatal("local date with year zero was accepted")
	}
}
