package central

import (
	"context"
	"errors"
	"testing"
	"time"
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
	result.Status = "failed"
	result.Captures = nil

	receipt, err := service.CommitResult(
		context.Background(), Probe{ID: "probe"}, "assignment", "lease", result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "failed" || repository.commit.Result.Status != "failed" ||
		repository.commit.CommittedAt != now || len(repository.commit.Payload) == 0 ||
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
) (ResultReceipt, error) {
	repository.commit = commit
	if repository.err != nil {
		return ResultReceipt{}, repository.err
	}
	return ResultReceipt{
		AssignmentID: commit.AssignmentID,
		RunID:        commit.Result.RunID,
		ContentHash:  commit.PayloadHash,
		Status:       commit.Result.Status,
	}, nil
}
