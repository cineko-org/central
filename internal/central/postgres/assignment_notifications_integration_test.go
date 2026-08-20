package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestPostgresAssignmentNotificationWakesWaitingProbe(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		probeID      = "probe_assignment_notification"
		assignmentID = "assignment_notification"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupIntegrationRows(t, store, probeID, assignmentID)
	t.Cleanup(func() { cleanupIntegrationRows(t, store, probeID, assignmentID) })
	registerIntegrationProbe(t, store, probeID, "install_"+probeID, now)

	// With no durable assignment, the listener must remain blocked until its
	// bounded context expires rather than polling and returning early.
	noAssignmentContext, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = store.WaitForAssignment(noAssignmentContext, probeID, now.Add(-time.Minute))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty assignment wait error = %v, want deadline", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		waitResult <- store.WaitForAssignment(waitContext, probeID, now.Add(-time.Minute))
	}()
	// Give LISTEN and the durable pre-check time to complete before the
	// transaction below emits its post-commit notification.
	time.Sleep(100 * time.Millisecond)
	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		return repository.CreateAssignment(ctx, reconcile.NewAssignment{
			ID: assignmentID, Priority: 100, Status: "queued", NotBefore: now,
			Deadline: now.Add(time.Minute), CreatedAt: now,
			Task: central.AssignmentTask{
				Kind: contracts.CapabilityCGVScheduleCapture,
				Theater: central.Theater{
					ID:         contracts.CatalogID(contracts.ProviderCGV, "theater", "0056"),
					ProviderID: contracts.ProviderCGV, SourceKey: "0056", Region: "서울", Name: "용산아이파크몰",
				},
				Locale: "ko-KR", TimeZone: "Asia/Seoul", EgressPolicyID: "scan_default",
			},
			Candidates: []reconcile.CandidateProbe{{ID: probeID, NetworkID: "net_" + probeID}},
		})
	})
	if err != nil || !leader {
		t.Fatalf("create notified assignment: leader=%t error=%v", leader, err)
	}
	select {
	case err := <-waitResult:
		if err != nil {
			t.Fatalf("notification wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment notification did not wake listener")
	}
}
