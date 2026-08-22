package central

import (
	"context"
	"errors"
	"testing"
	"time"

	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
)

func TestServiceCommitsFailedAttemptForRepositoryRetryDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	repository := &assignmentCommitRecorder{memoryRepository: newMemoryRepository()}
	service, err := NewService(repository, Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	result := validResult(now)
	failed := &observationpb.Failed{}
	failed.SetReasonCode("provider_error")
	result.SetFailed(failed)

	receipt, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.GetRunId() != result.GetRunId() || repository.commit.Result.GetFailed() == nil ||
		repository.commit.CommittedAt != now ||
		repository.commit.PayloadHash == "" {
		t.Fatalf("receipt = %+v, commit = %+v", receipt, repository.commit)
	}

	repository.err = ErrLeaseExpired
	if _, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", result,
	); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("deadline rejection = %v", err)
	}
}

type assignmentCommitRecorder struct {
	*memoryRepository
	commit ResultCommit
	err    error
}

func (repository *assignmentCommitRecorder) CommitResult(
	_ context.Context,
	commit ResultCommit,
) (*observationpb.ResultReceipt, error) {
	repository.commit = commit
	if repository.err != nil {
		return nil, repository.err
	}
	receipt := &observationpb.ResultReceipt{}
	receipt.SetAssignmentId(commit.AssignmentID)
	receipt.SetRunId(commit.Result.GetRunId())
	receipt.SetContentHash(commit.PayloadHash)
	return receipt, nil
}
