package central

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceWaitForAssignmentOptionalRepository(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	service, err := NewService(newMemoryRepository(), Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	if err := service.WaitForAssignment(context.Background(), Probe{ID: "probe_memory"}); err != nil {
		t.Fatalf("WaitForAssignment(memory repository) = %v", err)
	}
}

func TestServiceWaitForAssignmentDelegatesWithHeartbeatCutoff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	waitErr := errors.New("assignment wait failed")
	repository := &waitableMemoryRepository{memoryRepository: newMemoryRepository(), waitErr: waitErr}
	service, err := NewService(repository, Config{
		EnrollmentToken:   "enroll",
		ProbeHeartbeatTTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	probe := Probe{ID: "probe_wait"}
	if err := service.WaitForAssignment(context.Background(), probe); !errors.Is(err, waitErr) {
		t.Fatalf("WaitForAssignment(waiter) = %v, want %v", err, waitErr)
	}
	if repository.waitProbeID != probe.ID {
		t.Fatalf("wait probe id = %q, want %q", repository.waitProbeID, probe.ID)
	}
	wantCutoff := now.Add(-2 * time.Minute)
	if !repository.waitCutoff.Equal(wantCutoff) {
		t.Fatalf("wait heartbeat cutoff = %s, want %s", repository.waitCutoff, wantCutoff)
	}
}

type waitableMemoryRepository struct {
	*memoryRepository
	waitErr     error
	waitProbeID string
	waitCutoff  time.Time
}

func (repository *waitableMemoryRepository) WaitForAssignment(
	_ context.Context,
	probeID string,
	cutoff time.Time,
) error {
	repository.waitProbeID = probeID
	repository.waitCutoff = cutoff
	return repository.waitErr
}
