package postgres

import (
	"context"
	"testing"
	"time"
)

func TestPostgresClientEventNotifyReplayAndRetention(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	store, err := Open(t.Context(), testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const userID = "event_stream_integration"
	_, _ = store.pool.Exec(t.Context(), `DELETE FROM client_users WHERE id = $1`, userID)
	t.Cleanup(func() { _, _ = store.pool.Exec(context.Background(), `DELETE FROM client_users WHERE id = $1`, userID) })
	now := time.Now().UTC()
	if _, err := store.pool.Exec(t.Context(), `
		INSERT INTO client_users (id, display_name, created_at, updated_at) VALUES ($1, 'Event', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	wake := make(chan error, 1)
	waitContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { wake <- store.WaitClientEvents(waitContext, userID, 0, 1) }()
	if _, err := store.pool.Exec(t.Context(), `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ('event_stream_1', $1, 'monitor.updated', 'monitors', 'monitor', 1, '{}', $2)
	`, userID, now.Add(-181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := <-wake; err != nil {
		t.Fatal(err)
	}
	page, err := store.ClientEventPage(t.Context(), userID, 0, 100)
	if err != nil || len(page.Events) != 1 || page.Latest != page.Events[0].Sequence {
		t.Fatalf("event page = %+v, %v", page, err)
	}
	deleted, err := store.DeleteExpiredClientEvents(t.Context(), now.Add(-180*24*time.Hour), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("retention = %d, %v", deleted, err)
	}
	pruned, err := store.ClientEventPage(t.Context(), userID, 0, 100)
	if err != nil || pruned.PrunedThrough != page.Latest || len(pruned.Events) != 0 {
		t.Fatalf("pruned page = %+v, %v", pruned, err)
	}
}
