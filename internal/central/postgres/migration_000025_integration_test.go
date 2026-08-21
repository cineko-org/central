package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestMigration000025ConvertsLegacyAssignmentTaskProtoJSON(t *testing.T) {
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
	defer connection.Release()
	const testSchema = "migration_000025_test"
	if _, err := connection.Exec(ctx, `DROP SCHEMA IF EXISTS `+testSchema+` CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `CREATE SCHEMA `+testSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+testSchema+` CASCADE`)
	}()
	if _, err := connection.Exec(ctx, `SET search_path TO `+testSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		t.Fatal(err)
	}
	defer unlockMigrations(connection)
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS cineko_schema_migrations (
			version bigint PRIMARY KEY,
			checksum text NOT NULL DEFAULT '',
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		ALTER TABLE cineko_schema_migrations
			ADD COLUMN IF NOT EXISTS checksum text NOT NULL DEFAULT ''
	`); err != nil {
		t.Fatal(err)
	}

	allMigrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var migration25 migration
	for _, item := range allMigrations {
		switch {
		case item.version <= 24:
			if err := applyMigration(ctx, connection, item); err != nil {
				t.Fatalf("apply migration %d (%s): %v", item.version, item.name, err)
			}
		case item.version == 25:
			migration25 = item
		}
	}
	if migration25.version != 25 {
		t.Fatal("migration 000025 was not loaded")
	}
	var applied int
	var highest int64
	if err := connection.QueryRow(ctx, `
		SELECT count(*), max(version) FROM cineko_schema_migrations
	`).Scan(&applied, &highest); err != nil {
		t.Fatal(err)
	}
	if applied != 24 || highest != 24 {
		t.Fatalf("migration baseline = count %d, highest %d; want 24/24", applied, highest)
	}

	insertLegacyMigrationFixture(t, connection, ctx)
	if err := applyMigration(ctx, connection, migration25); err != nil {
		t.Fatalf("apply migration 25 (%s): %v", migration25.name, err)
	}
	if err := connection.QueryRow(ctx, `
		SELECT count(*), max(version) FROM cineko_schema_migrations
	`).Scan(&applied, &highest); err != nil {
		t.Fatal(err)
	}
	if applied != 25 || highest != 25 {
		t.Fatalf("migration result = count %d, highest %d; want 25/25", applied, highest)
	}

	var capabilities, availableCapabilities []string
	if err := connection.QueryRow(ctx, `
		SELECT capabilities, available_capabilities
		FROM probe_runtimes WHERE id = 'probe_legacy_proto'
	`).Scan(&capabilities, &availableCapabilities); err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"cgv.schedule.capture", "cgv.catalog.capture", "cgv.seat-map.capture", "custom.capture",
	}; !slices.Equal(capabilities, want) {
		t.Fatalf("probe capabilities = %v, want %v", capabilities, want)
	}
	if want := []string{"cgv.schedule.capture", "cgv.seat-map.capture"}; !slices.Equal(availableCapabilities, want) {
		t.Fatalf("available probe capabilities = %v, want %v", availableCapabilities, want)
	}

	var policyKinds, assignmentKinds []string
	if err := scanTextArray(ctx, connection, `
		SELECT array_agg(task_kind ORDER BY id) FROM observation_policies
	`, &policyKinds); err != nil {
		t.Fatal(err)
	}
	if err := scanTextArray(ctx, connection, `
		SELECT array_agg(task_kind ORDER BY id) FROM observation_assignments
	`, &assignmentKinds); err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"cgv.catalog.capture", "cgv.schedule.capture", "cgv.seat-map.capture"}
	if !slices.Equal(policyKinds, wantKinds) {
		t.Fatalf("observation policy task kinds = %v, want %v", policyKinds, wantKinds)
	}
	if !slices.Equal(assignmentKinds, wantKinds) {
		t.Fatalf("assignment task kinds = %v, want %v", assignmentKinds, wantKinds)
	}

	rows, err := connection.Query(ctx, `
		SELECT id, task_kind, task_data, lane, hot_target_fingerprint,
			result_payload
		FROM observation_assignments ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantJSON := map[string]string{
		"assignment_legacy_catalog":  `{"egress":{"managedScan":{}},"catalog":{"theater":{"id":"theater_catalog","providerId":"cgv","sourceKey":"catalog-source","region":"KR-11","name":"Catalog Theater"},"targetDates":[{"year":2026,"month":8,"day":24}],"locale":"ko-KR","timeZone":"Asia/Seoul"}}`,
		"assignment_legacy_schedule": `{"egress":{"managedScan":{}},"schedule":{"theater":{"id":"theater_schedule","providerId":"cgv","sourceKey":"schedule-source","region":"KR-11","name":"Schedule Theater"},"targetDates":[{"year":2026,"month":8,"day":22},{"year":2026,"month":8,"day":23}],"locale":"ko-KR","timeZone":"Asia/Seoul"}}`,
		"assignment_legacy_seat_map": `{"egress":{"managedScan":{}},"seatMap":{"theater":{"id":"theater_seat_map","providerId":"cgv","sourceKey":"seat-map-source","region":"KR-11","name":"Seat Map Theater"},"auditorium":{"id":"auditorium_seat_map","theaterId":"theater_seat_map","sourceKey":"auditorium-source","name":"IMAX","screenTypes":["IMAX"],"capacity":300,"currentLayoutHash":"layout-v1"},"showtime":{"id":"showtime_seat_map","providerId":"cgv","sourceKey":"showtime-source","theaterId":"theater_seat_map","movie":{"id":"movie_seat_map","providerId":"cgv","sourceKey":"movie-source","title":"Seat Map Movie"},"auditorium":{"id":"auditorium_seat_map","theaterId":"theater_seat_map","sourceKey":"auditorium-source","name":"IMAX","screenTypes":["IMAX"],"capacity":300,"currentLayoutHash":"layout-v1"},"startsAt":"2026-08-22T12:00:00Z","endsAt":"2026-08-22T14:00:00Z","availableSeats":120,"capacity":300,"soldOut":true},"locale":"ko-KR","timeZone":"Asia/Seoul"}}`,
	}
	seen := 0
	for rows.Next() {
		var (
			id, taskKind, lane, hotFingerprint string
			raw, resultPayload                 []byte
		)
		if err := rows.Scan(&id, &taskKind, &raw, &lane, &hotFingerprint, &resultPayload); err != nil {
			t.Fatal(err)
		}
		seen++
		if taskKind != map[string]string{
			"assignment_legacy_catalog":  "cgv.catalog.capture",
			"assignment_legacy_schedule": "cgv.schedule.capture",
			"assignment_legacy_seat_map": "cgv.seat-map.capture",
		}[id] {
			t.Fatalf("assignment %s task kind = %q", id, taskKind)
		}
		var task observationpb.AssignmentTask
		if err := (protojson.UnmarshalOptions{}).Unmarshal(raw, &task); err != nil {
			t.Fatalf("assignment %s is not strict current AssignmentTask ProtoJSON: %v; raw=%s", id, err, raw)
		}
		encoded, err := (protojson.MarshalOptions{}).Marshal(&task)
		if err != nil {
			t.Fatalf("marshal assignment %s: %v", id, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, encoded); err != nil {
			t.Fatalf("compact assignment %s ProtoJSON: %v", id, err)
		}
		if compact.String() != wantJSON[id] {
			t.Fatalf("assignment %s ProtoJSON = %s, want %s", id, compact.String(), wantJSON[id])
		}
		if id == "assignment_legacy_schedule" {
			if lane != "hot" || hotFingerprint != "legacy-hot-fingerprint" {
				t.Fatalf("schedule lane metadata = %q/%q", lane, hotFingerprint)
			}
		} else if lane != "baseline" || hotFingerprint != "" {
			t.Fatalf("assignment %s lane metadata = %q/%q", id, lane, hotFingerprint)
		}
		if resultPayload != nil {
			t.Fatalf("assignment %s retained legacy result_payload = %s", id, resultPayload)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(wantJSON) {
		t.Fatalf("converted assignments = %d, want %d", seen, len(wantJSON))
	}

	var attemptPayload []byte
	if err := connection.QueryRow(ctx, `
		SELECT result_payload FROM assignment_attempts
		WHERE assignment_id = 'assignment_legacy_schedule'
	`).Scan(&attemptPayload); err != nil {
		t.Fatal(err)
	}
	if attemptPayload != nil {
		t.Fatalf("assignment attempt retained legacy result_payload = %s", attemptPayload)
	}
}

func insertLegacyMigrationFixture(t *testing.T, connection *pgxpool.Conn, ctx context.Context) {
	t.Helper()
	// The fixture is inserted against the schema immediately after migration
	// 000024, so these values exercise the exact versioned legacy boundary.
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	if _, err := connection.Exec(ctx, `
		INSERT INTO probe_runtimes (
			id, installation_id, kind, network_id, network_hint, capabilities,
			max_concurrency, runtime_version, browser_revision, platform, architecture,
			token_hash, token_expires_at, status, draining, available_slots, health,
			reason_code, created_at, updated_at, owner_user_id, device_id, available_capabilities
		) VALUES (
			'probe_legacy_proto', 'installation_legacy_proto', 'container', 'network_legacy_proto',
			'', $1, 1, '1.0.0', 'browser-legacy', 'linux', 'amd64',
			$2, $3, 'online', false, 1, 'healthy', '', $4, $4, '', '', $5
		)
	`, []string{
		"cgv.schedule.capture.v2", "cgv.catalog.capture.v1", "cgv.seat-map.capture.v1", "custom.capture",
	}, []byte("legacy-probe-token"), now.Add(time.Hour), now, []string{
		"cgv.schedule.capture.v2", "cgv.seat-map.capture.v1",
	}); err != nil {
		t.Fatalf("insert legacy Probe fixture: %v", err)
	}
	for _, policy := range []struct {
		id, taskKind, theaterID, theaterName, targetDate string
	}{
		{"policy_legacy_catalog", "cgv.catalog.capture.v1", "theater_catalog", "Catalog Theater", "2026-08-24"},
		{"policy_legacy_schedule", "cgv.schedule.capture.v2", "theater_schedule", "Schedule Theater", "2026-08-22"},
		{"policy_legacy_seat_map", "cgv.seat-map.capture.v1", "theater_seat_map", "Seat Map Theater", "2026-08-25"},
	} {
		if _, err := connection.Exec(ctx, `
			INSERT INTO observation_policies (
				id, enabled, revision, task_kind, theater_id, theater_region, theater_name,
				target_date_mode, target_dates, locale, time_zone, egress_policy_id,
				priority, min_interval_seconds, max_interval_seconds, execution_window_seconds,
				last_error_code, created_at, updated_at
			) VALUES ($1, true, 1, $2, $3, 'KR-11', $4, 'explicit', ARRAY[$5::date],
				'ko-KR', 'Asia/Seoul', 'scan_default', 50, 60, 120, 3600, '', $6, $6)
		`, policy.id, policy.taskKind, policy.theaterID, policy.theaterName, policy.targetDate, now); err != nil {
			t.Fatalf("insert legacy policy %s: %v", policy.id, err)
		}
	}
	legacySchedule := `{"kind":"cgv.schedule.capture.v2","theater":{"id":"theater_schedule","providerId":"cgv","sourceKey":"schedule-source","region":"KR-11","name":"Schedule Theater"},"targetDates":["2026-08-22","2026-08-23"],"locale":"ko-KR","timeZone":"Asia/Seoul","egressPolicyId":"scan_default","_cinekoLane":"hot","_cinekoHotFingerprint":"legacy-hot-fingerprint"}`
	legacyCatalog := `{"kind":"cgv.catalog.capture.v1","theater":{"id":"theater_catalog","providerId":"cgv","sourceKey":"catalog-source","region":"KR-11","name":"Catalog Theater"},"targetDates":["2026-08-24"],"locale":"ko-KR","timeZone":"Asia/Seoul","egressPolicyId":"scan_default"}`
	legacySeatMap := `{"kind":"cgv.seat-map.capture.v1","theater":{"id":"theater_seat_map","providerId":"cgv","sourceKey":"seat-map-source","region":"KR-11","name":"Seat Map Theater"},"auditorium":{"id":"auditorium_seat_map","theaterId":"theater_seat_map","sourceKey":"auditorium-source","name":"IMAX","screenTypes":["IMAX"],"capacity":300,"seatMapVersion":"layout-v1"},"showtime":{"id":"showtime_seat_map","providerId":"cgv","sourceKey":"showtime-source","theaterId":"theater_seat_map","movie":{"id":"movie_seat_map","providerId":"cgv","sourceKey":"movie-source","title":"Seat Map Movie"},"auditorium":{"id":"auditorium_seat_map","theaterId":"theater_seat_map","sourceKey":"auditorium-source","name":"IMAX","screenTypes":["IMAX"],"capacity":300,"seatMapVersion":"layout-v1"},"startsAt":"2026-08-22T12:00:00Z","endsAt":"2026-08-22T14:00:00Z","availableSeats":120,"capacity":300,"soldOut":true},"locale":"ko-KR","timeZone":"Asia/Seoul","egressPolicyId":"scan_default"}`
	insertLegacyAssignment(t, connection, ctx, "assignment_legacy_catalog", "cgv.catalog.capture.v1", "theater_catalog", "Catalog Theater", "catalog-source", "2026-08-24", "", legacyCatalog, now)
	insertLegacyAssignment(t, connection, ctx, "assignment_legacy_schedule", "cgv.schedule.capture.v2", "theater_schedule", "Schedule Theater", "schedule-source", "2026-08-22", "2026-08-23", legacySchedule, now)
	insertLegacyAssignment(t, connection, ctx, "assignment_legacy_seat_map", "cgv.seat-map.capture.v1", "theater_seat_map", "Seat Map Theater", "seat-map-source", "2026-08-25", "", legacySeatMap, now)
	if _, err := connection.Exec(ctx, `
		INSERT INTO assignment_attempts (
			assignment_id, probe_id, attempt, started_at, finished_at, status, error_code,
			network_id, run_id, result_hash, result_payload
		) VALUES (
			'assignment_legacy_schedule', 'probe_legacy_proto', 1, $1, $1, 'completed', '',
			'network_legacy_proto', 'legacy-run', repeat('a', 64), $2
		)
	`, now, `{"runId":"legacy-run","status":"completed","captures":[]}`); err != nil {
		t.Fatalf("insert legacy assignment attempt: %v", err)
	}
}

func insertLegacyAssignment(
	t *testing.T,
	connection *pgxpool.Conn,
	ctx context.Context,
	id, taskKind, theaterID, theaterName, theaterSourceKey, firstDate, secondDate, taskData string,
	now time.Time,
) {
	t.Helper()
	if _, err := connection.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_region, theater_name, theater_provider_id, theater_source_key, target_dates,
			locale, time_zone, egress_policy_id, status, not_before, deadline,
			result_payload, created_at, updated_at, task_data
		) VALUES (
			$1, $2, $3, 'KR-11', $4, 'cgv', $5,
			ARRAY[$6::date] || CASE WHEN $7::text <> '' THEN ARRAY[$7::date] ELSE '{}'::date[] END,
			'ko-KR', 'Asia/Seoul', 'scan_default', 'queued', $8::timestamptz, $8::timestamptz + interval '1 hour',
			$9::jsonb, $8::timestamptz, $8::timestamptz, $10::jsonb
		)
	`, id, taskKind, theaterID, theaterName, theaterSourceKey, firstDate, secondDate, now, `{"runId":"legacy-assignment","status":"completed"}`, taskData); err != nil {
		t.Fatalf("insert legacy assignment %s: %v", id, err)
	}
}

func scanTextArray(ctx context.Context, connection *pgxpool.Conn, query string, target *[]string) error {
	return connection.QueryRow(ctx, query).Scan(target)
}
