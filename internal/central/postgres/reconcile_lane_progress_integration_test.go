package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central/reconcile"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPostgresDuePoliciesIgnoreUnsuccessfulLaneProgress(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const policyID = "policy_lane_progress_outcomes"
	cleanupReconcileRows(t, store, []string{policyID}, nil)
	t.Cleanup(func() { cleanupReconcileRows(t, store, []string{policyID}, nil) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	theaterID := seedIntegrationPolicy(t, store, policyID, "theater_lane_progress", now.Add(-time.Minute))

	insertLaneProgressAssignment(t, store, laneProgressAssignment{
		ID:         "assignment_lane_progress_hot_completed_old",
		PolicyID:   policyID,
		TheaterID:  theaterID,
		Status:     "completed",
		Lane:       "hot",
		FinishedAt: now.Add(-time.Second),
		TargetDate: "2026-08-20",
	})
	for index, outcome := range []string{"partial", "failed", "missed"} {
		insertLaneProgressAssignment(t, store, laneProgressAssignment{
			ID:         "assignment_lane_progress_hot_" + outcome,
			PolicyID:   policyID,
			TheaterID:  theaterID,
			Status:     outcome,
			Lane:       "hot",
			FinishedAt: now.Add(time.Duration(index+1) * time.Second),
			TargetDate: "2026-08-20",
		})
		insertLaneProgressAssignment(t, store, laneProgressAssignment{
			ID:         "assignment_lane_progress_baseline_" + outcome,
			PolicyID:   policyID,
			TheaterID:  theaterID,
			Status:     outcome,
			Lane:       "baseline",
			FinishedAt: now.Add(time.Duration(index+3) * time.Second),
			TargetDate: "2026-08-14",
		})
	}

	var policies []reconcile.Policy
	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		var queryErr error
		policies, queryErr = repository.DuePolicies(ctx, now, 1)
		return queryErr
	})
	if err != nil || !leader {
		t.Fatalf("read lane progress: leader=%t error=%v", leader, err)
	}
	if len(policies) != 1 {
		t.Fatalf("due policies = %d, want 1", len(policies))
	}
	policy := policies[0]
	if !policy.LastHotFinishedAt.IsZero() || len(policy.LastHotTargetDates) != 0 || policy.LastHotTargetFingerprint != "" {
		t.Fatalf("unsuccessful hot progress was persisted: %+v", policy)
	}
	if !policy.LastBaselineFinishedAt.IsZero() || policy.LastBaselineTargetDate != "" {
		t.Fatalf("unsuccessful baseline progress was persisted: %+v", policy)
	}

	// A newer failed attempt must not hide the most recent successful
	// baseline cursor. The planner can therefore retry the same next date.
	insertLaneProgressAssignment(t, store, laneProgressAssignment{
		ID:         "assignment_lane_progress_baseline_completed",
		PolicyID:   policyID,
		TheaterID:  theaterID,
		Status:     "completed",
		Lane:       "baseline",
		FinishedAt: now.Add(10 * time.Second),
		TargetDate: "2026-08-14",
	})
	insertLaneProgressAssignment(t, store, laneProgressAssignment{
		ID:         "assignment_lane_progress_baseline_failed_newer",
		PolicyID:   policyID,
		TheaterID:  theaterID,
		Status:     "failed",
		Lane:       "baseline",
		FinishedAt: now.Add(20 * time.Second),
		TargetDate: "2026-08-15",
	})
	leader, err = store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		var queryErr error
		policies, queryErr = repository.DuePolicies(ctx, now, 1)
		return queryErr
	})
	if err != nil || !leader {
		t.Fatalf("read successful lane progress: leader=%t error=%v", leader, err)
	}
	if len(policies) != 1 {
		t.Fatalf("successful lane progress policies = %+v", policies)
	}
	policy = policies[0]
	if policy.LastBaselineTargetDate != "2026-08-14" || !policy.LastBaselineFinishedAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("successful baseline was hidden by failure: %+v", policies)
	}
}

func TestPostgresPreemptQueuedBaselineMakesFuturePolicyDue(t *testing.T) {
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
		policyID     = "policy_preempt_future_next_run"
		assignmentID = "assignment_preempt_future_next_run"
	)
	cleanupReconcileRows(t, store, []string{policyID}, nil)
	t.Cleanup(func() { cleanupReconcileRows(t, store, []string{policyID}, nil) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	theaterID := seedIntegrationPolicy(t, store, policyID, "theater_preempt_future", now.Add(time.Hour))
	insertLaneProgressAssignment(t, store, laneProgressAssignment{
		ID:         assignmentID,
		PolicyID:   policyID,
		TheaterID:  theaterID,
		Status:     "queued",
		Lane:       "baseline",
		FinishedAt: time.Time{},
		TargetDate: "2026-08-14",
	})

	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		return repository.PreemptQueuedBaseline(ctx, policyID, now)
	})
	if err != nil || !leader {
		t.Fatalf("preempt future policy: leader=%t error=%v", leader, err)
	}
	var status, reason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, terminal_reason FROM observation_assignments WHERE id = $1
	`, assignmentID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "missed" || reason != "hot_demand_preempted" {
		t.Fatalf("preempted assignment = status %q reason %q", status, reason)
	}
	var nextRunAt time.Time
	if err := store.pool.QueryRow(ctx, `
		SELECT next_run_at FROM observation_policies WHERE id = $1
	`, policyID).Scan(&nextRunAt); err != nil {
		t.Fatal(err)
	}
	if nextRunAt.After(now) {
		t.Fatalf("preempted policy remains scheduled in the future: %s", nextRunAt)
	}
}

type laneProgressAssignment struct {
	ID         string
	PolicyID   string
	TheaterID  string
	Status     string
	Lane       string
	FinishedAt time.Time
	TargetDate string
}

func insertLaneProgressAssignment(t *testing.T, store *Store, assignment laneProgressAssignment) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	theater := &catalogpb.Theater{}
	theater.SetId(assignment.TheaterID)
	theater.SetProviderId("cgv")
	theater.SetSourceKey("lane-progress")
	theater.SetRegion("서울")
	theater.SetName("레인 진행 시험관")
	taskData, err := protojson.Marshal(storeIntegrationScheduleTask(
		theater, assignment.TargetDate, "ko-KR", "Asia/Seoul",
	))
	if err != nil {
		t.Fatal(err)
	}
	var finishedAt *time.Time
	if !assignment.FinishedAt.IsZero() {
		finishedAt = &assignment.FinishedAt
	}
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO observation_assignments (
			id, task_kind, policy_id, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, lane, not_before, deadline, finished_at, created_at, updated_at, task_data
		) VALUES (
			$1, 'cgv.schedule.capture', $2, $3, 'cgv', 'lane-progress',
			'서울', '레인 진행 시험관', ARRAY[$4::date], 'ko-KR', 'Asia/Seoul', 'scan_default',
			$5, $6, $7, $8, $9, $10, $10, $11::jsonb
		)
	`, assignment.ID, assignment.PolicyID, assignment.TheaterID, assignment.TargetDate,
		assignment.Status, assignment.Lane, now.Add(-time.Minute), now.Add(time.Minute), finishedAt, now, taskData); err != nil {
		t.Fatal(err)
	}
}
