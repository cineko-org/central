package central

import (
	"errors"
	"testing"
	"time"

	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
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
	mismatched.GetCompleted().GetSchedule().GetCaptures()[0].SetTargetDate(date)
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
	validDate := &commonpb.LocalDate{}
	validDate.SetYear(2024)
	validDate.SetMonth(2)
	validDate.SetDay(29)
	if !validLocalDate(validDate) {
		t.Fatal("valid leap-day local date was rejected")
	}
	invalidCalendarDate := proto.CloneOf(validDate)
	invalidCalendarDate.SetDay(30)
	if validLocalDate(invalidCalendarDate) {
		t.Fatal("invalid calendar date was accepted")
	}
}

func TestCommitResultLiveSeatAndEncodingBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }

	missingLayout := validResult(now)
	missingLayout.GetCompleted().SetLiveSeat((&seatmappb.LiveSeatObservation_builder{
		Availability: &seatmappb.AvailabilitySnapshot{},
	}).Build())
	if _, err := service.CommitResult(t.Context(), Probe{}, "assignment", "lease", missingLayout); !errors.Is(err, ErrInvalid) {
		t.Fatalf("live seat without layout error = %v", err)
	}

	invalidAvailability := validResult(now)
	invalidAvailability.GetCompleted().SetLiveSeat((&seatmappb.LiveSeatObservation_builder{
		Layout:       seatMapSnapshot("auditorium", 1),
		Availability: &seatmappb.AvailabilitySnapshot{},
	}).Build())
	if _, err := service.CommitResult(t.Context(), Probe{}, "assignment", "lease", invalidAvailability); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid live availability error = %v", err)
	}

	invalidUTF8 := validResult(now)
	invalidUTF8.SetRunId(string([]byte{0xff}))
	if _, err := service.CommitResult(t.Context(), Probe{}, "assignment", "lease", invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 assignment result was encoded")
	}
}

func TestValidateResultOutcomeBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	base := func() *observationpb.AssignmentResult {
		result := &observationpb.AssignmentResult{}
		result.SetRunId("run")
		result.SetStartedAt(timestamppb.New(now))
		result.SetFinishedAt(timestamppb.New(now.Add(time.Second)))
		return result
	}

	invalidDeferred := base()
	invalidDeferred.SetDeferred(&observationpb.Deferred{})
	if err := validateResult(invalidDeferred); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deferred result without reason error = %v", err)
	}
	reason := &collectionpb.DeferredReason{}
	reason.SetNoBookableShowtime(&collectionpb.NoBookableShowtime{})
	deferred := &observationpb.Deferred{}
	deferred.SetReason(reason)
	validDeferred := base()
	validDeferred.SetDeferred(deferred)
	if err := validateResult(validDeferred); err != nil {
		t.Fatalf("valid deferred result error = %v", err)
	}

	emptyCompleted := base()
	emptyCompleted.SetCompleted(&observationpb.Completed{})
	if err := validateResult(emptyCompleted); !errors.Is(err, ErrInvalid) {
		t.Fatalf("completed result without payload error = %v", err)
	}
}
