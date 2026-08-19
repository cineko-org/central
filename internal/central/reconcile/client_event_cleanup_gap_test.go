package reconcile

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReconcilePropagatesClientEventCleanupFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	cycle := newMemoryCycle()
	cycle.failAt = "delete_client_events"
	engine := newTestEngine(t, &memoryRepository{cycle: cycle, leader: true}, now)

	_, err := engine.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "delete expired Client events") {
		t.Fatalf("Client event cleanup error = %v", err)
	}
}
