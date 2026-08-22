package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration000028DropsPreCutoverAppEvents(t *testing.T) {
	connection, migration28 := newMigration000028Schema(t, "drops_pre_cutover_app_events")
	ctx := t.Context()
	now := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	const (
		userID            = "user_migration_28_app_events"
		preCutoverEventID = "app_event_migration_28_pre_cutover"
		currentEventID    = "app_event_migration_28_current"
	)
	preCutoverPayload := []byte(`{"id":"app_event_migration_28_pre_cutover","userId":"user_migration_28_app_events","kind":"monitor.failed","message":"Monitor failed","createdAt":"2026-08-22T05:00:00Z","tone":"error"}`)
	currentPayload := []byte(`{"id":"app_event_migration_28_current","userId":"user_migration_28_app_events","kind":"monitor.ready","message":"Monitor ready","createdAt":"2026-08-22T05:00:00Z","success":{}}`)
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, 'App Event Migration User', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES
			($1, 'app-events', $2, 1, $3::jsonb, $5, $5),
			($1, 'app-events', $4, 1, $6::jsonb, $5, $5)
	`, userID, preCutoverEventID, preCutoverPayload, currentEventID, now, currentPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision,
			payload, occurred_at
		) VALUES
			(
				'client_event_migration_28_pre_cutover_updated', $1, 'app-events.updated.v1',
				'app-events', $2, 1, $3::jsonb, $4
			),
			(
				'client_event_migration_28_pre_cutover_deleted', $1, 'app-events.deleted.v1',
				'app-events', $2, 2, '{}'::jsonb, $4
			),
			(
				'client_event_migration_28_pre_cutover_payload', $1, 'app-events.updated',
				'app-events', $2, 3, $3::jsonb, $4
			),
			(
				'client_event_migration_28_pre_cutover_settings', $1, 'settings.updated.v1',
				'settings', 'settings', 1, '{}'::jsonb, $4
			),
			(
				'client_event_migration_28_current', $1, 'app-events.updated',
				'app-events', $5, 1, $6::jsonb, $4
			)
	`, userID, preCutoverEventID, preCutoverPayload, now, currentEventID, currentPayload); err != nil {
		t.Fatal(err)
	}

	if err := applyMigration(ctx, connection, migration28); err != nil {
		t.Fatalf("apply migration 000028 (%s): %v", migration28.name, err)
	}
	var preCutoverResources, preCutoverRows, preCutoverEvents int
	if err := connection.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM client_resources WHERE user_id = $1 AND id = $2),
			(SELECT count(*) FROM client_app_events WHERE user_id = $1 AND id = $2),
			(SELECT count(*) FROM client_events WHERE id LIKE 'client_event_migration_28_pre_cutover_%')
	`, userID, preCutoverEventID).Scan(&preCutoverResources, &preCutoverRows, &preCutoverEvents); err != nil {
		t.Fatal(err)
	}
	if preCutoverResources != 0 || preCutoverRows != 0 || preCutoverEvents != 0 {
		t.Fatalf("pre-cutover rows retained: resources=%d app_events=%d events=%d",
			preCutoverResources, preCutoverRows, preCutoverEvents)
	}

	var tone, eventType string
	var hasSuccessMember, successMemberIsEmpty bool
	if err := connection.QueryRow(ctx, `
		SELECT app_event.tone, event.event_type,
			event.payload ? 'success', event.payload->'success' = '{}'::jsonb
		FROM client_app_events AS app_event
		JOIN client_events AS event
		  ON event.user_id = app_event.user_id AND event.resource_id = app_event.id
		WHERE app_event.user_id = $1 AND app_event.id = $2
	`, userID, currentEventID).Scan(&tone, &eventType, &hasSuccessMember, &successMemberIsEmpty); err != nil {
		t.Fatal(err)
	}
	if tone != "success" || eventType != "app-events.updated" || !hasSuccessMember || !successMemberIsEmpty {
		t.Fatalf("retained current app event = tone:%q type:%q success:%t empty:%t",
			tone, eventType, hasSuccessMember, successMemberIsEmpty)
	}
}

func TestMigration000028RejectsMalformedClientResourceProtoJSON(t *testing.T) {
	connection, migration28 := newMigration000028Schema(t, "rejects")
	ctx := t.Context()
	now := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ('user_migration_28_invalid', 'Invalid Migration 28 User', $1, $1)
	`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES (
			'user_migration_28_invalid', 'monitors', 'monitor_invalid', 1,
			'{"id":"monitor_invalid","userId":"user_migration_28_invalid","legacyStatus":"pending"}'::jsonb,
			$1, $1
		)
	`, now); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, connection, migration28); err == nil {
		t.Fatal("malformed latest ProtoJSON migration unexpectedly succeeded")
	}
	var applied bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM cineko_schema_migrations WHERE version = 28)
	`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("failed migration 000028 was recorded")
	}
	var payloadExists bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'client_resources' AND column_name = 'payload'
		)
	`).Scan(&payloadExists); err != nil {
		t.Fatal(err)
	}
	if !payloadExists {
		t.Fatal("failed migration removed client_resources.payload")
	}
}

func TestMigration000028RejectsMalformedClientEventProtoJSON(t *testing.T) {
	connection, migration28 := newMigration000028Schema(t, "rejects_event")
	ctx := t.Context()
	now := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ('user_migration_28_invalid_event', 'Invalid Event User', $1, $1)
	`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES ('user_migration_28_invalid_event', 'settings', 'settings', 1, '{}'::jsonb, $1, $1)
	`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES (
			'event_invalid_payload_migration_28', 'user_migration_28_invalid_event',
			'settings.updated', 'settings', 'settings', 1,
			'{"network":{},"legacyStatus":"pending"}'::jsonb, $1
		)
	`, now); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, connection, migration28); err == nil {
		t.Fatal("malformed historical client event migration unexpectedly succeeded")
	}
	var applied bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM cineko_schema_migrations WHERE version = 28)
	`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("failed migration 000028 was recorded for malformed client event")
	}
	var payloadExists bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'client_resources' AND column_name = 'payload'
		)
	`).Scan(&payloadExists); err != nil {
		t.Fatal(err)
	}
	if !payloadExists {
		t.Fatal("failed event migration removed client_resources.payload")
	}
}

func newMigration000028Schema(t *testing.T, suffix string) (*pgxpool.Conn, migration) {
	t.Helper()
	if os.Getenv("CINEKO_CENTRAL_INTEGRATION") != "1" || os.Getenv("CINEKO_CENTRAL_TEST_DATABASE_URL") != "" {
		t.Skip("requires the repository-managed ephemeral PostgreSQL testcontainer")
	}
	ctx := t.Context()
	config, err := pgxpool.ParseConfig(testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Release)
	testSchema := "migration_000028_" + suffix
	if _, err := connection.Exec(ctx, `DROP SCHEMA IF EXISTS `+testSchema+` CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `CREATE SCHEMA `+testSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = connection.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+testSchema+` CASCADE`)
	})
	if _, err := connection.Exec(ctx, `SET search_path TO `+testSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockMigrations(connection) })
	if _, err := connection.Exec(ctx, `
		CREATE TABLE cineko_schema_migrations (
			version bigint PRIMARY KEY,
			checksum text NOT NULL DEFAULT '',
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var migration28 migration
	for _, item := range migrations {
		if item.version <= 27 {
			if err := applyMigration(ctx, connection, item); err != nil {
				t.Fatalf("apply migration %d (%s): %v", item.version, item.name, err)
			}
		}
		if item.version == 28 {
			migration28 = item
		}
	}
	if migration28.version != 28 {
		t.Fatal("migration 000028 was not loaded")
	}
	return connection, migration28
}
