package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration000032HardCutsLegacyWorkloadAndPreservesControlPlane(t *testing.T) {
	if os.Getenv("CINEKO_CENTRAL_INTEGRATION") != "1" || os.Getenv("CINEKO_CENTRAL_TEST_DATABASE_URL") != "" {
		t.Skip("requires the repository-managed ephemeral PostgreSQL testcontainer")
	}
	ctx := t.Context()
	connection, migration32 := newMigration000032Schema(t)
	now := time.Date(2026, time.August, 23, 1, 0, 0, 0, time.UTC)
	seedMigration000032CutoverState(t, connection, ctx, now)

	if err := applyMigration(ctx, connection, migration32); err != nil {
		t.Fatalf("apply migration 000032 (%s): %v", migration32.name, err)
	}

	for _, table := range []string{
		"client_execution_commands", "client_events", "client_commands", "client_event_cursors",
		"showtime_observations", "schedule_captures", "assignment_attempts",
		"assignment_eligible_probes", "observation_assignments", "observation_policies",
		"observation_payloads", "showtimes", "seat_availability_snapshots", "seat_map_versions",
		"auditoriums", "movies", "theaters", "providers", "seat_map_collection_states",
	} {
		assertMigration000032TableCount(t, connection, ctx, table, 0)
	}
	var nonSettings int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM client_resources WHERE kind <> 'settings'`).Scan(&nonSettings); err != nil {
		t.Fatal(err)
	}
	if nonSettings != 0 {
		t.Fatalf("non-settings resources after cutover = %d", nonSettings)
	}

	for _, table := range []string{
		"client_users", "client_credentials", "client_sessions", "client_devices",
		"client_resources", "client_settings", "probe_runtimes", "desktop_release_registry_state",
	} {
		assertMigration000032TableCount(t, connection, ctx, table, 1)
	}
	var generation int64
	var refreshRequestedAt *time.Time
	if err := connection.QueryRow(ctx, `
		SELECT generation, refresh_requested_at FROM catalog_state WHERE id = 1
	`).Scan(&generation, &refreshRequestedAt); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || refreshRequestedAt == nil || refreshRequestedAt.IsZero() {
		t.Fatalf("catalog reset = generation %d, requested_at %v", generation, refreshRequestedAt)
	}
}

func newMigration000032Schema(t *testing.T) (*pgxpool.Conn, migration) {
	t.Helper()
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
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Release)
	const schema = "migration_000032_hard_cutover"
	if _, err := connection.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = connection.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
	if _, err := connection.Exec(ctx, `SET search_path TO `+schema); err != nil {
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
	var migration32 migration
	for _, item := range migrations {
		if item.version <= 31 {
			if err := applyMigration(ctx, connection, item); err != nil {
				t.Fatalf("apply migration %d (%s): %v", item.version, item.name, err)
			}
		}
		if item.version == 32 {
			migration32 = item
		}
	}
	if migration32.version != 32 {
		t.Fatal("migration 000032 was not loaded")
	}
	return connection, migration32
}

func seedMigration000032CutoverState(t *testing.T, connection *pgxpool.Conn, ctx context.Context, now time.Time) {
	t.Helper()
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	statements := []string{
		`INSERT INTO client_users (id, display_name, created_at, updated_at)
		 VALUES ('cutover_user', 'Cutover User', $1, $1)`,
		`INSERT INTO client_credentials (user_id, token_hash, created_at, updated_at)
		 VALUES ('cutover_user', 'credential', $1, $1)`,
		`INSERT INTO client_sessions (
			id, user_id, token_hash, expires_at, refresh_token_hash, refresh_expires_at, created_at
		 ) VALUES ('cutover_session', 'cutover_user', 'session', $1::timestamptz + interval '1 hour',
			'refresh-session', $1::timestamptz + interval '2 hours', $1)`,
		`INSERT INTO client_devices (
			installation_id, user_id, device_id, platform, architecture, app_version,
			last_seen_at, created_at, updated_at
		 ) VALUES ('cutover_installation', 'cutover_user', 'cutover_device', 'darwin', 'arm64',
			'1.0.0', $1, $1, $1)`,
		`INSERT INTO client_resources (user_id, kind, id, revision, created_at, updated_at)
		 VALUES ('cutover_user', 'settings', 'settings', 1, $1, $1),
		        ('cutover_user', 'app-events', 'legacy_event', 1, $1, $1)`,
		`INSERT INTO client_settings (user_id, resource_kind, id)
		 VALUES ('cutover_user', 'settings', 'settings')`,
		`INSERT INTO probe_runtimes (
			id, installation_id, kind, network_id, capabilities, max_concurrency,
			runtime_version, browser_revision, platform, architecture, token_hash,
			token_expires_at, status, available_slots, health, reason_code,
			created_at, updated_at, available_capabilities
		 ) VALUES ('cutover_probe', 'cutover_probe_installation', 'container', 'cutover_network',
			ARRAY['cgv.catalog.capture'], 1, '1.0.0', 'browser', 'linux', 'amd64', 'probe-token',
			$1::timestamptz + interval '1 hour', 'online', 1, 'healthy', '', $1, $1,
			ARRAY['cgv.catalog.capture'])`,
		`INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		 VALUES ('legacy_provider', 'Legacy Provider', $2, $1, $1, $1)`,
		`INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash, first_seen_at, last_seen_at, updated_at
		 ) VALUES ('legacy_theater', 'legacy_provider', 'display-derived', 'Seoul', 'Legacy Theater',
			$2, $1, $1, $1)`,
		`INSERT INTO movies (
			id, provider_id, source_key, title, content_hash, first_seen_at, last_seen_at, updated_at
		 ) VALUES ('legacy_movie', 'legacy_provider', 'display-derived', 'Legacy Movie', $2, $1, $1, $1)`,
		`INSERT INTO auditoriums (
			id, theater_id, source_key, name, capacity, content_hash, first_seen_at, last_seen_at, updated_at
		 ) VALUES ('legacy_auditorium', 'legacy_theater', 'display-derived', 'Legacy Auditorium', 1,
			$2, $1, $1, $1)`,
		`INSERT INTO showtimes (
			id, provider_id, source_key, theater_id, movie_id, auditorium_id, schedule_date,
			starts_at, ends_at, content_hash, first_seen_at, last_seen_at, updated_at
		 ) VALUES ('legacy_showtime', 'legacy_provider', 'display-derived', 'legacy_theater',
			'legacy_movie', 'legacy_auditorium', '2026-08-23', $1::timestamptz + interval '1 hour',
			$1::timestamptz + interval '2 hours', $2, $1, $1, $1)`,
		`INSERT INTO seat_map_versions (
			id, auditorium_id, layout_hash, capacity, observed_at, first_seen_at, last_seen_at
		 ) VALUES ('legacy_layout', 'legacy_auditorium', $2, 1, $1, $1, $1)`,
		`UPDATE auditoriums SET current_seat_map_version_id = 'legacy_layout'
		 WHERE id = 'legacy_auditorium'`,
		`INSERT INTO seat_availability_snapshots (
			id, showtime_id, auditorium_id, layout_hash, content_hash, observed_at, created_at
		 ) VALUES ('legacy_availability', 'legacy_showtime', 'legacy_auditorium', $2, $2, $1, $1)`,
		`INSERT INTO observation_policies (
			id, task_kind, theater_id, theater_region, theater_name, target_date_mode,
			target_dates, locale, time_zone, egress_policy_id, priority, min_interval_seconds,
			max_interval_seconds, execution_window_seconds, created_at, updated_at
		 ) VALUES ('legacy_policy', 'cgv.schedule.capture', 'legacy_theater', 'Seoul', 'Legacy Theater',
			'rolling', '{}', 'ko-KR', 'Asia/Seoul', 'scan_default', 50, 60, 120, 3600, $1, $1)`,
		`INSERT INTO observation_assignments (
			id, policy_id, task_kind, theater_id, theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, status, not_before, deadline, created_at, updated_at
		 ) VALUES ('legacy_assignment', 'legacy_policy', 'cgv.schedule.capture', 'legacy_theater',
			'Seoul', 'Legacy Theater', '{}', 'ko-KR', 'Asia/Seoul', 'scan_default', 'queued',
			$1, $1::timestamptz + interval '1 hour', $1, $1)`,
		`INSERT INTO assignment_attempts (
			assignment_id, probe_id, attempt, started_at, finished_at, status, error_code
		 ) VALUES ('legacy_assignment', 'cutover_probe', 1, $1, $1, 'failed', 'legacy')`,
		`INSERT INTO assignment_eligible_probes (assignment_id, probe_id, network_id, eligible_at)
		 VALUES ('legacy_assignment', 'cutover_probe', 'cutover_network', $1)`,
		`INSERT INTO observation_payloads (content_hash, payload, created_at)
		 VALUES ($2, '{}'::jsonb, $1)`,
		`UPDATE catalog_state SET generation = 23, refresh_requested_at = NULL, updated_at = $1 WHERE id = 1`,
	}
	for _, statement := range statements {
		var arguments []any
		if strings.Contains(statement, "$1") {
			arguments = append(arguments, now)
		}
		if strings.Contains(statement, "$2") {
			arguments = append(arguments, hash)
		}
		if _, err := connection.Exec(ctx, statement, arguments...); err != nil {
			t.Fatalf("seed migration 000032 cutover state: %v\n%s", err, statement)
		}
	}
}

func assertMigration000032TableCount(
	t *testing.T,
	connection *pgxpool.Conn,
	ctx context.Context,
	table string,
	want int,
) {
	t.Helper()
	var got int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}
