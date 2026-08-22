package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
)

func TestClaimAssignmentWaitsForRepositoryWakeupBeforeRetry(t *testing.T) {
	const accessToken = "probe-access-token"
	now := time.Now().UTC()
	base := &apiRepository{probe: central.Probe{
		ID:             "probe_wait",
		TokenHash:      sha256.Sum256([]byte(accessToken)),
		TokenExpiresAt: now.Add(time.Hour),
	}}
	repository := &delayedClaimRepository{
		apiRepository: base,
		waitStarted:   make(chan struct{}),
		release:       make(chan struct{}),
		assignment: central.Assignment{
			ID: "assignment_wait", Status: "leased", NotBefore: now.Add(-time.Minute),
			Deadline: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute),
			Task: validAPIClaimTask(now),
		},
	}
	service, err := central.NewService(repository, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}

	response := make(chan int, 1)
	go func() {
		recorder := request(t, server.Handler(), http.MethodPost,
			"/v1/probes/probe_wait/assignments:claim", nil,
			map[string]string{"Authorization": "Bearer " + accessToken})
		response <- recorder.Code
	}()
	select {
	case <-repository.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("claim did not enter repository wait")
	}
	close(repository.release)
	select {
	case status := <-response:
		if status != http.StatusOK {
			t.Fatalf("claim status = %d, want %d", status, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("claim did not retry after repository wakeup")
	}
	if repository.claimCalls != 2 {
		t.Fatalf("claim calls = %d, want durable recheck before and after wait", repository.claimCalls)
	}
}

func validAPIClaimTask(now time.Time) *observationpb.AssignmentTask {
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", "0056"))
	theater.SetProviderId(catalogdomain.ProviderCGV)
	catalogdomain.SetTheaterSourceKey(theater, "0056")
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	targetDate := &commonpb.LocalDate{}
	targetDate.SetYear(numeric.ClampInt32(now.Year()))
	targetDate.SetMonth(numeric.ClampInt32(int(now.Month())))
	targetDate.SetDay(numeric.ClampInt32(now.Day()))
	schedule := &observationpb.ScheduleTask{}
	schedule.SetTheater(theater)
	schedule.SetTargetDates([]*commonpb.LocalDate{targetDate})
	schedule.SetLocale("ko-KR")
	schedule.SetTimeZone("Asia/Seoul")
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task := &observationpb.AssignmentTask{}
	task.SetSchedule(schedule)
	task.SetEgress(egress)
	return task
}

type delayedClaimRepository struct {
	*apiRepository
	waitStarted chan struct{}
	release     chan struct{}
	assignment  central.Assignment
	claimCalls  int
}

func (repository *delayedClaimRepository) ClaimAssignment(
	context.Context,
	string,
	[32]byte,
	time.Time,
	time.Time,
	time.Time,
) (central.Assignment, error) {
	repository.claimCalls++
	if repository.claimCalls == 1 {
		return central.Assignment{}, central.ErrNoAssignment
	}
	return repository.assignment, nil
}

func (repository *delayedClaimRepository) WaitForAssignment(ctx context.Context, _ string, _ time.Time) error {
	close(repository.waitStarted)
	select {
	case <-repository.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
