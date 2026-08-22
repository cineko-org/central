package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestMigration000033CutsLegacyCatalogWorkAndEnforcesProviderGlobalTarget(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	connection, migration32 := newMigration000032Schema(t)
	ctx := t.Context()
	if err := applyMigration(ctx, connection, migration32); err != nil {
		t.Fatalf("apply migration 000032: %v", err)
	}
	migration33 := loadedMigration(t, 33)
	now := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	seedStatements := []string{
		`INSERT INTO probe_runtimes (
			id, installation_id, kind, network_id, capabilities, max_concurrency,
			runtime_version, browser_revision, platform, architecture, token_hash,
			token_expires_at, status, available_slots, health, reason_code,
			created_at, updated_at, available_capabilities
		) VALUES (
			'legacy_catalog_probe', 'legacy_catalog_installation', 'container', 'legacy_network',
			ARRAY['cgv.catalog.capture'], 1, '1.0.0', 'browser', 'linux', 'amd64',
			'legacy-catalog-token', $1::timestamptz + interval '1 hour', 'online', 1,
			'healthy', '', $1, $1, ARRAY['cgv.catalog.capture']
		)`,
		`INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, not_before, deadline, created_at, updated_at, task_data
		) VALUES (
			'legacy_catalog_assignment', 'cgv.catalog.capture', 'pseudo_theater', 'cgv',
			'__catalog__', 'system', 'CGV catalog', '{}', 'ko-KR', 'Asia/Seoul',
			'scan_default', 'queued', $1, $1::timestamptz + interval '10 minutes', $1, $1,
			'{"catalog":{"theater":{"id":"pseudo_theater"}}}'::jsonb
		)`,
		`INSERT INTO assignment_eligible_probes (assignment_id, probe_id, network_id, eligible_at)
		VALUES ('legacy_catalog_assignment', 'legacy_catalog_probe', 'legacy_network', $1)`,
		`INSERT INTO assignment_attempts (
			assignment_id, probe_id, attempt, started_at, finished_at, status, error_code
		) VALUES (
			'legacy_catalog_assignment', 'legacy_catalog_probe', 1, $1, $1, 'failed', 'legacy_contract'
		)`,
	}
	for _, statement := range seedStatements {
		if _, err := connection.Exec(ctx, statement, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMigration(ctx, connection, migration33); err != nil {
		t.Fatalf("apply migration 000033: %v", err)
	}
	for _, table := range []string{"assignment_attempts", "assignment_eligible_probes", "observation_assignments"} {
		var count int
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d legacy catalog rows", table, count)
		}
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, not_before, deadline, created_at, updated_at, task_data
		) VALUES (
			'provider_catalog_assignment', 'cgv.catalog.capture', '', 'cgv', '', '', '', '{}',
			'ko-KR', 'Asia/Seoul', 'scan_default', 'queued', $1,
			$1::timestamptz + interval '10 minutes', $1, $1,
			'{"catalog":{"providerId":"cgv","locale":"ko-KR","timeZone":"Asia/Seoul"}}'::jsonb
		)
	`, now); err != nil {
		t.Fatalf("insert provider-global catalog assignment: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, not_before, deadline, created_at, updated_at
		) VALUES (
			'invalid_schedule_assignment', 'cgv.schedule.capture', '', 'cgv', '', '', '', '{}',
			'ko-KR', 'Asia/Seoul', 'scan_default', 'queued', $1,
			$1::timestamptz + interval '10 minutes', $1, $1
		)
	`, now); err == nil {
		t.Fatal("theater-scoped assignment accepted an empty theater target")
	}
	var indexDefinition string
	if err := connection.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'observation_assignments_active_target_idx'
	`).Scan(&indexDefinition); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"task_kind", "theater_provider_id", "theater_id"} {
		if !strings.Contains(indexDefinition, column) {
			t.Fatalf("active target index is missing %s: %s", column, indexDefinition)
		}
	}
}

func loadedMigration(t *testing.T, version int64) migration {
	t.Helper()
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.version == version {
			return item
		}
	}
	t.Fatalf("migration %06d was not loaded", version)
	return migration{}
}
