package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	"github.com/cineko-org/central/internal/domain"
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
	_, _ = store.pool.Exec(t.Context(), `DELETE FROM client_events WHERE user_id = $1`, userID)
	_, _ = store.pool.Exec(t.Context(), `DELETE FROM client_users WHERE id = $1`, userID)
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_events WHERE user_id = $1`, userID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_users WHERE id = $1`, userID)
	})
	now := time.Now().UTC()
	if _, err := store.pool.Exec(t.Context(), `
		INSERT INTO client_users (id, display_name, created_at, updated_at) VALUES ($1, 'Event', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	var releaseGeneration int64
	if err := store.pool.QueryRow(t.Context(), `
		SELECT generation FROM desktop_release_registry_state WHERE singleton = true
	`).Scan(&releaseGeneration); err != nil {
		t.Fatal(err)
	}
	wake := make(chan error, 1)
	waitContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { wake <- store.WaitClientEvents(waitContext, userID, 0, releaseGeneration) }()
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
	if _, err := store.pool.Exec(t.Context(), `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ('event_stream_2', $1, 'monitor.updated', 'monitors', 'monitor', 2, '{}', $2)
	`, userID, now.Add(-181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var cycleDeleted int64
	leader, err := store.RunLeaderCycle(t.Context(), func(repository reconcile.CycleRepository) error {
		var retentionErr error
		cycleDeleted, retentionErr = repository.DeleteExpiredClientEvents(
			t.Context(), now.Add(-180*24*time.Hour), 100,
		)
		return retentionErr
	})
	if err != nil || !leader || cycleDeleted != 1 {
		t.Fatalf("leader retention = leader:%t deleted:%d error:%v", leader, cycleDeleted, err)
	}
}

func TestPostgresClientMutationPathsWakeEventStream(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	store, err := Open(t.Context(), testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const userID = "event_mutation_wake_integration"
	cleanup := func(ctx context.Context) {
		for _, statement := range []string{
			`DELETE FROM client_events WHERE user_id = $1`,
			`DELETE FROM client_commands WHERE user_id = $1`,
			`DELETE FROM client_resources WHERE user_id = $1`,
			`DELETE FROM client_execution_commands WHERE user_id = $1`,
			`DELETE FROM client_users WHERE id = $1`,
		} {
			if _, cleanupErr := store.pool.Exec(ctx, statement, userID); cleanupErr != nil {
				t.Errorf("Client mutation wake cleanup: %v", cleanupErr)
			}
		}
	}
	cleanup(t.Context())
	t.Cleanup(func() { cleanup(context.Background()) })
	now := time.Now().UTC()
	if _, err := store.pool.Exec(t.Context(), `
		INSERT INTO client_users (id, display_name, created_at, updated_at) VALUES ($1, 'Event Wake', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}

	assertWake := func(name string, mutate func() error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			page, pageErr := store.ClientEventPage(t.Context(), userID, 0, 1)
			if pageErr != nil {
				t.Fatal(pageErr)
			}
			waitContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			wake := make(chan error, 1)
			go func() {
				wake <- store.WaitClientEvents(waitContext, userID, page.Latest, page.ReleaseGeneration)
			}()
			if err := mutate(); err != nil {
				t.Fatal(err)
			}
			if err := <-wake; err != nil {
				t.Fatalf("event stream was not notified after committed mutation: %v", err)
			}
		})
	}

	assertWake("resource", func() error {
		_, err := store.PutClientResource(t.Context(), central.ResourceMutation{
			UserID: userID, Kind: "settings", ID: "settings", Data: json.RawMessage(`{}`),
			CommandID: "event_wake_resource", Now: now.Add(time.Second),
		})
		return err
	})
	assertWake("configuration", func() error {
		page, pageErr := store.ClientEventPage(t.Context(), userID, 0, 1)
		if pageErr != nil {
			return pageErr
		}
		payload := json.RawMessage(`{"id":"event_wake_preset","userId":"` + userID + `","name":"Wake","theaterId":"theater","auditoriumId":"auditorium","seatCount":1,"seatPreference":{}}`)
		_, err := store.ReplaceClientConfiguration(t.Context(), central.ConfigurationReplacement{
			UserID: userID, ExpectedRevision: page.Latest,
			Resources: []central.ConfigurationResource{{Kind: "presets", ID: "event_wake_preset", Data: payload}},
			CommandID: "event_wake_configuration", PayloadSHA256: "event-wake-configuration",
			Now: now.Add(2 * time.Second),
		})
		return err
	})

	showtime := central.Showtime{
		ID: "event_wake_showtime", StartsAt: now.Add(24 * time.Hour),
		Movie: central.Movie{Title: "Wake"}, Auditorium: central.Auditorium{ID: "auditorium", Name: "Auditorium"},
		AvailableSeats: 1, Capacity: 1,
	}
	target := executionTarget{userID: userID, monitor: domain.MonitorJob{ID: "event_wake_monitor"}}
	assertWake("execution created", func() error {
		tx, beginErr := store.pool.Begin(t.Context())
		if beginErr != nil {
			return beginErr
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err := insertExecutionCommand(t.Context(), tx, target, showtime, now, now.Add(3*time.Second)); err != nil {
			return err
		}
		return tx.Commit(t.Context())
	})
	var commandID string
	if err := store.pool.QueryRow(t.Context(), `
		UPDATE client_execution_commands SET status = 'failed', attempt_count = 3,
			reason_code = 'integration_failure', completed_at = $2, updated_at = $2
		WHERE user_id = $1 AND monitor_id = 'event_wake_monitor'
		RETURNING id
	`, userID, now.Add(4*time.Second)).Scan(&commandID); err != nil {
		t.Fatal(err)
	}
	assertWake("execution explicit retry", func() error {
		return store.RetryClientExecution(t.Context(), userID, commandID, now.Add(5*time.Second))
	})
}
