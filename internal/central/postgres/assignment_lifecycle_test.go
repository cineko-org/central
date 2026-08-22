package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
)

func TestAssignmentAuthorityEndsAtTargetDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	leaseHash := sha256.Sum256([]byte("lease"))

	for _, test := range []struct {
		name         string
		leaseExpires time.Time
		deadline     time.Time
		want         error
	}{
		{name: "active", leaseExpires: now.Add(time.Minute), deadline: now.Add(time.Hour)},
		{name: "lease equality", leaseExpires: now, deadline: now.Add(time.Hour), want: central.ErrLeaseExpired},
		{name: "deadline equality", leaseExpires: now.Add(time.Minute), deadline: now, want: central.ErrLeaseExpired},
		{name: "past deadline", leaseExpires: now.Add(time.Minute), deadline: now.Add(-time.Nanosecond), want: central.ErrLeaseExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := authorizeAssignmentHeartbeat(
				leaseHash[:], test.leaseExpires, test.deadline, leaseHash, now,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("authorization error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResultAuthorityEndsAtTargetDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	leaseHash := sha256.Sum256([]byte("lease"))
	commit := central.ResultCommit{ProbeID: "probe", LeaseHash: leaseHash, CommittedAt: now}
	state := assignmentResultState{
		status: "leased", probeID: "probe", storedLease: leaseHash[:],
		leaseExpiresAt: timePointer(now.Add(time.Minute)), deadline: now,
	}
	if err := authorizeResultCommit(state, commit); !errors.Is(err, central.ErrLeaseExpired) {
		t.Fatalf("result authorization error = %v", err)
	}
	state.deadline = now.Add(time.Nanosecond)
	if err := authorizeResultCommit(state, commit); err != nil {
		t.Fatalf("active result authorization error = %v", err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestPostgresFailedResultIsReassignedBeforeTerminalFailure(t *testing.T) {
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
		policyID   = "policy_retryable_result"
		probeOne   = "probe_retryable_result_1"
		probeAlias = "probe_retryable_result_same_network"
		probeTwo   = "probe_retryable_result_2"
	)
	probeIDs := []string{probeOne, probeAlias, probeTwo}
	cleanupReconcileRows(t, store, []string{policyID}, probeIDs)
	t.Cleanup(func() { cleanupReconcileRows(t, store, []string{policyID}, probeIDs) })
	now := time.Now().UTC()
	for _, probeID := range probeIDs {
		registerIntegrationProbe(t, store, probeID, "install_"+probeID, now)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE probe_runtimes SET network_id = $2 WHERE id = $1
	`, probeAlias, "net_"+probeOne); err != nil {
		t.Fatal(err)
	}
	seedIntegrationPolicy(t, store, policyID, "9101", now.Add(-time.Second))
	engine, err := newAssignmentTestReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assignment := assignmentForPolicy(t, store, policyID)
	keepOnlyAssignmentCandidates(t, store, assignment.ID, probeIDs)
	if _, err := store.pool.Exec(ctx, `UPDATE probe_runtimes SET network_id = 'net_moved' WHERE id = $1`, probeTwo); err != nil {
		t.Fatal(err)
	}
	claimNow := time.Now().UTC()
	movedLease := sha256.Sum256([]byte("lease_retryable_result_moved"))
	if _, err := store.ClaimAssignment(
		ctx, probeTwo, movedLease, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	); !errors.Is(err, central.ErrNoAssignment) {
		t.Fatalf("changed-network eligibility error = %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE probe_runtimes SET network_id = $2 WHERE id = $1`, probeTwo, "net_"+probeTwo); err != nil {
		t.Fatal(err)
	}
	leaseOne := sha256.Sum256([]byte("lease_retryable_result_1"))
	claimNow = time.Now().UTC()
	claimed, err := store.ClaimAssignment(
		ctx, probeOne, leaseOne, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	)
	if err != nil || claimed.ID != assignment.ID {
		t.Fatalf("first claim = %+v, %v", claimed, err)
	}
	failure := integrationResultCommit(t, claimed, probeOne, leaseOne)
	failure.Result.SetRunId("run_retryable_failure")
	failed := &observationpb.Failed{}
	failureReason := &collectionpb.FailureReason{}
	failureReason.SetProviderTransportFailed(&collectionpb.ProviderTransportFailed{})
	failed.SetReason(failureReason)
	failure.Result.SetFailed(failed)
	refreshCommitPayload(t, &failure)
	failedReceipt, err := store.CommitResult(ctx, failure)
	if err != nil || failedReceipt.GetAccepted() == nil {
		t.Fatalf("failed result receipt = %+v, %v", failedReceipt, err)
	}
	var status, terminalReason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, terminal_reason FROM observation_assignments WHERE id = $1
	`, assignment.ID).Scan(&status, &terminalReason); err != nil {
		t.Fatal(err)
	}
	if status != "retry_pending" || terminalReason != "provider_transport_failed" {
		t.Fatalf("retryable result state = %q, %q", status, terminalReason)
	}
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	makeAssignmentClaimable(t, store, assignment.ID)
	claimNow = time.Now().UTC()
	aliasLease := sha256.Sum256([]byte("lease_retryable_result_same_network"))
	if _, err := store.ClaimAssignment(
		ctx, probeAlias, aliasLease, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	); !errors.Is(err, central.ErrNoAssignment) {
		t.Fatalf("same-network retry claim error = %v", err)
	}
	leaseTwo := sha256.Sum256([]byte("lease_retryable_result_2"))
	claimNow = time.Now().UTC()
	claimed, err = store.ClaimAssignment(
		ctx, probeTwo, leaseTwo, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	)
	if err != nil || claimed.ID != assignment.ID {
		t.Fatalf("second claim = %+v, %v", claimed, err)
	}
	if repeated, err := store.CommitResult(ctx, failure); err != nil || repeated.GetDuplicate() == nil || repeated.GetRunId() != failedReceipt.GetRunId() {
		t.Fatalf("failed attempt replay = %+v, %v; want %+v", repeated, err, failedReceipt)
	}
	success := integrationResultCommit(t, claimed, probeTwo, leaseTwo)
	success.Result.SetRunId("run_retryable_success")
	refreshCommitPayload(t, &success)
	if receipt, err := store.CommitResult(ctx, success); err != nil || receipt.GetAccepted() == nil {
		t.Fatalf("successful retry receipt = %+v, %v", receipt, err)
	}
	if repeated, err := store.CommitResult(ctx, failure); err != nil || repeated.GetDuplicate() == nil || repeated.GetRunId() != failedReceipt.GetRunId() {
		t.Fatalf("failed attempt replay after completion = %+v, %v; want %+v", repeated, err, failedReceipt)
	}
}

func TestPostgresFailedResultExhaustsOneAttemptPerEligibleNetwork(t *testing.T) {
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
		policyID   = "policy_retryable_network_exhausted"
		probeOne   = "probe_retryable_network_1"
		probeAlias = "probe_retryable_network_alias"
	)
	probeIDs := []string{probeOne, probeAlias}
	cleanupReconcileRows(t, store, []string{policyID}, probeIDs)
	t.Cleanup(func() { cleanupReconcileRows(t, store, []string{policyID}, probeIDs) })
	now := time.Now().UTC()
	for _, probeID := range probeIDs {
		registerIntegrationProbe(t, store, probeID, "install_"+probeID, now)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE probe_runtimes SET network_id = $2 WHERE id = $1
	`, probeAlias, "net_"+probeOne); err != nil {
		t.Fatal(err)
	}
	seedIntegrationPolicy(t, store, policyID, "9102", now.Add(-time.Second))
	engine, err := newAssignmentTestReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assignment := assignmentForPolicy(t, store, policyID)
	keepOnlyAssignmentCandidates(t, store, assignment.ID, probeIDs)
	leaseHash := sha256.Sum256([]byte("lease_retryable_network_1"))
	claimNow := time.Now().UTC()
	claimed, err := store.ClaimAssignment(
		ctx, probeOne, leaseHash, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	)
	if err != nil || claimed.ID != assignment.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	failure := integrationResultCommit(t, claimed, probeOne, leaseHash)
	failure.Result.SetRunId("run_retryable_network_failure")
	failed := &observationpb.Failed{}
	failureReason := &collectionpb.FailureReason{}
	failureReason.SetProviderTransportFailed(&collectionpb.ProviderTransportFailed{})
	failed.SetReason(failureReason)
	failure.Result.SetFailed(failed)
	refreshCommitPayload(t, &failure)
	if _, err := store.CommitResult(ctx, failure); err != nil {
		t.Fatal(err)
	}
	report, err := engine.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.GetFailedAssignments() != 1 || report.GetRequeuedAssignments() != 0 {
		t.Fatalf("report = %+v", report)
	}
	var status, terminalReason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, terminal_reason FROM observation_assignments WHERE id = $1
	`, assignment.ID).Scan(&status, &terminalReason); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || terminalReason != "eligible_probes_exhausted" {
		t.Fatalf("terminal assignment = %q, %q", status, terminalReason)
	}
}

func TestPostgresNonRetryableFailedResultIsTerminalAndAudited(t *testing.T) {
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
		policyID = "policy_nonretryable_result"
		probeID  = "probe_nonretryable_result"
	)
	cleanupReconcileRows(t, store, []string{policyID}, []string{probeID})
	t.Cleanup(func() { cleanupReconcileRows(t, store, []string{policyID}, []string{probeID}) })
	now := time.Now().UTC()
	registerIntegrationProbe(t, store, probeID, "install_"+probeID, now)
	seedIntegrationPolicy(t, store, policyID, "9103", now.Add(-time.Second))
	engine, err := newAssignmentTestReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assignment := assignmentForPolicy(t, store, policyID)
	keepOnlyAssignmentCandidates(t, store, assignment.ID, []string{probeID})
	leaseHash := sha256.Sum256([]byte("lease_nonretryable_result"))
	claimNow := time.Now().UTC()
	claimed, err := store.ClaimAssignment(
		ctx, probeID, leaseHash, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
	)
	if err != nil || claimed.ID != assignment.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	failure := integrationResultCommit(t, claimed, probeID, leaseHash)
	failure.Result.SetRunId("run_nonretryable_failure")
	failed := &observationpb.Failed{}
	failureReason := &collectionpb.FailureReason{}
	failureReason.SetInvalidResult(&collectionpb.InvalidResult{})
	failed.SetReason(failureReason)
	failure.Result.SetFailed(failed)
	refreshCommitPayload(t, &failure)
	receipt, err := store.CommitResult(ctx, failure)
	if err != nil || receipt.GetAccepted() == nil {
		t.Fatalf("non-retryable result receipt = %+v, %v", receipt, err)
	}

	var status, terminalReason, completedBy, runID, resultHash string
	var resultPayload []byte
	var startedAt, finishedAt *time.Time
	if err := store.pool.QueryRow(ctx, `
		SELECT status, terminal_reason, completed_by_probe_id, run_id, result_hash, result_payload,
			started_at, finished_at
		FROM observation_assignments WHERE id = $1
	`, assignment.ID).Scan(
		&status, &terminalReason, &completedBy, &runID, &resultHash, &resultPayload, &startedAt, &finishedAt,
	); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || terminalReason != "invalid_result" || completedBy != probeID ||
		runID != failure.Result.GetRunId() || resultHash != failure.PayloadHash || len(resultPayload) == 0 ||
		startedAt == nil || finishedAt == nil {
		t.Fatalf("non-retryable assignment audit = status %q reason %q completedBy %q run %q hash %q payload %d started %v finished %v",
			status, terminalReason, completedBy, runID, resultHash, len(resultPayload), startedAt, finishedAt)
	}

	var attemptStatus string
	if err := store.pool.QueryRow(ctx, `
		SELECT status FROM assignment_attempts WHERE assignment_id = $1 AND probe_id = $2
	`, assignment.ID, probeID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != "failed" {
		t.Fatalf("non-retryable attempt status = %q", attemptStatus)
	}
}

func keepOnlyAssignmentCandidates(t *testing.T, store *Store, assignmentID string, probeIDs []string) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		DELETE FROM assignment_eligible_probes
		WHERE assignment_id = $1 AND NOT (probe_id = ANY($2))
	`, assignmentID, probeIDs); err != nil {
		t.Fatal(err)
	}
}

func newAssignmentTestReconciler(store *Store) (*reconcile.Engine, error) {
	return reconcile.New(store, reconcile.Config{
		TickInterval: time.Hour, ProbeHeartbeatTTL: time.Minute, OfflineRetention: 24 * time.Hour,
		RetryMinimum: time.Millisecond, RetryMaximum: time.Millisecond, BatchSize: 100,
	})
}

func refreshCommitPayload(t *testing.T, commit *central.ResultCommit) {
	t.Helper()
	payload, err := json.Marshal(commit.Result)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	commit.PayloadHash = hex.EncodeToString(digest[:])
}
