package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	if err != nil || len(page.Events) != 1 || page.Latest != page.Events[0].GetSequence() {
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
	const (
		userID       = "event_mutation_wake_integration"
		providerID   = "provider_event_wake"
		theaterID    = "theater"
		auditoriumID = "auditorium"
		movieID      = "movie_event_wake"
	)
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
	cleanupClientResourceCatalog(t, store, providerID, []string{theaterID}, []string{auditoriumID}, []string{movieID})
	t.Cleanup(func() {
		cleanupClientResourceCatalog(t, store, providerID, []string{theaterID}, []string{auditoriumID}, []string{movieID})
	})
	t.Cleanup(func() { cleanup(context.Background()) })
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID)
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

	preset := &clientpb.Preset{}
	preset.SetId("event_wake_preset")
	preset.SetUserId(userID)
	preset.SetName("Wake")
	preset.SetTheaterId(theaterID)
	preset.SetAuditoriumId(auditoriumID)
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	assertWake("resource", func() error {
		identity := &commonpb.ResourceIdentity{}
		identity.SetId("settings")
		resource := &clientpb.Resource{}
		resource.SetIdentity(identity)
		resource.SetSettings(&clientpb.Settings{})
		_, err := store.PutClientResource(t.Context(), central.ResourceMutation{
			UserID: userID, Kind: "settings", ID: "settings", Resource: resource,
			CommandID: "event_wake_resource", Now: now.Add(time.Second),
		})
		return err
	})
	assertWake("configuration", func() error {
		identity := &commonpb.ResourceIdentity{}
		identity.SetId("event_wake_preset")
		resource := &clientpb.Resource{}
		resource.SetIdentity(identity)
		resource.SetPreset(preset)
		_, err := store.PutClientResource(t.Context(), central.ResourceMutation{
			UserID: userID, Kind: "presets", ID: "event_wake_preset", Resource: resource,
			CommandID: "event_wake_configuration", Now: now.Add(2 * time.Second),
		})
		return err
	})

	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetTitle("Wake")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetName("Auditorium")
	showtime := &catalogpb.Showtime{}
	showtime.SetId("event_wake_showtime")
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetStartsAt(timestamppb.New(now.Add(24 * time.Hour)))
	showtime.SetEndsAt(timestamppb.New(now.Add(26 * time.Hour)))
	showtime.SetAvailableSeats(1)
	showtime.SetCapacity(1)
	monitor := &clientpb.Monitor{}
	monitor.SetId("event_wake_monitor")
	monitor.SetUserId(userID)
	monitor.SetPresetId("event_wake_preset")
	monitor.SetMovieId(movie.GetId())
	monitor.SetSearchHorizonDays(14)
	monitor.SetState(&clientpb.MonitorState{})
	monitor.GetState().SetPending(&clientpb.MonitorPending{})
	monitorIdentity := &commonpb.ResourceIdentity{}
	monitorIdentity.SetId(monitor.GetId())
	monitorResource := &clientpb.Resource{}
	monitorResource.SetIdentity(monitorIdentity)
	monitorResource.SetMonitor(monitor)
	assertWake("monitor created", func() error {
		var putErr error
		monitorResource, putErr = store.PutClientResource(t.Context(), central.ResourceMutation{
			UserID: userID, Kind: "monitors", ID: monitor.GetId(), Resource: monitorResource,
			CommandID: "event_wake_monitor_created", Now: now.Add(3 * time.Second),
		})
		return putErr
	})
	target := executionTarget{userID: userID, monitor: monitor, preset: preset}
	assertWake("execution created", func() error {
		tx, beginErr := store.pool.Begin(t.Context())
		if beginErr != nil {
			return beginErr
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err := insertExecutionCommand(t.Context(), tx, target, showtime, now, now.Add(4*time.Second), false); err != nil {
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
	`, userID, now.Add(5*time.Second)).Scan(&commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(t.Context(), `
		UPDATE client_monitors SET state = 'failed', state_reason = 'integration_failure'
		WHERE user_id = $1 AND id = 'event_wake_monitor'
	`, userID); err != nil {
		t.Fatal(err)
	}
	revision := monitorResource.GetIdentity().GetRevision()
	assertWake("execution monitor retry", func() error {
		_, putErr := store.PutClientResource(t.Context(), central.ResourceMutation{
			UserID: userID, Kind: "monitors", ID: monitor.GetId(), Resource: monitorResource,
			ExpectedRevision: &revision, CommandID: "event_wake_monitor_retry",
			Now: now.Add(6 * time.Second),
		})
		return putErr
	})
	var status string
	if err := store.pool.QueryRow(t.Context(), `
		SELECT status FROM client_execution_commands WHERE id = $1
	`, commandID).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("monitor retry command status = %q, %v", status, err)
	}
	var payload []byte
	if err := store.pool.QueryRow(t.Context(), `
		SELECT payload FROM client_events
		WHERE user_id = $1 AND event_type = 'execution.ready' AND resource_id = $2
		ORDER BY sequence DESC LIMIT 1
	`, userID, commandID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	ready := &clientpb.ExecutionReady{}
	if err := protojson.Unmarshal(payload, ready); err != nil || ready.GetReason() != "explicit_monitor_retry" {
		t.Fatalf("monitor retry event = %+v, %v", ready, err)
	}
}
