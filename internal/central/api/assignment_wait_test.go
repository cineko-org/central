package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
)

func TestClaimAssignmentWaitsForRepositoryWakeupBeforeRetry(t *testing.T) {
	const accessToken = "probe-access-token"
	base := &apiRepository{probe: central.Probe{
		ID:             "probe_wait",
		TokenHash:      sha256.Sum256([]byte(accessToken)),
		TokenExpiresAt: time.Now().Add(time.Hour),
	}}
	repository := &delayedClaimRepository{
		apiRepository: base,
		waitStarted:   make(chan struct{}),
		release:       make(chan struct{}),
		assignment: central.Assignment{
			ID: "assignment_wait", Status: "leased", LeaseExpiresAt: time.Now().Add(time.Minute),
			Task: observationpb.AssignmentTask_builder{
				Schedule: observationpb.ScheduleTask_builder{
					Theater: catalogpb.Theater_builder{Id: stringPointer("cgv:theater:0056")}.Build(),
				}.Build(),
			}.Build(),
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
