package postgres

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPostgresMigratedSchemaMatchesContractDocumentation(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	store, err := Open(t.Context(), testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve schema contract test path")
	}
	contractPath := filepath.Join(filepath.Dir(filename), "../../../docs/schema-contract.md")
	// #nosec G304 -- the path is fixed relative to this repository-owned test file.
	document, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.pool.Query(t.Context(), `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		tables[table] = append(tables[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 60 {
		t.Fatalf("migrated schema tables = %d, want 60", len(tables))
	}
	contract := string(document)
	for table, columns := range tables {
		row := schemaDocumentationRow(t, contract, table)
		for _, column := range columns {
			if !strings.Contains(row, "`"+column) {
				t.Errorf("schema contract table %s does not document migrated column %s", table, column)
			}
		}
	}
}

func TestSeatMapCollectionStateMigrationEnforcesLifecycleShape(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	store, err := Open(t.Context(), testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	var columns int
	if err := store.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'seat_map_collection_states'
	`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 12 {
		t.Fatalf("seat-map collection state columns = %d, want 12", columns)
	}
	var checks int
	if err := store.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'seat_map_collection_states'::regclass
			AND contype = 'c'
	`).Scan(&checks); err != nil {
		t.Fatal(err)
	}
	if checks < 10 {
		t.Fatalf("seat-map collection state checks = %d, want at least 10", checks)
	}

	tx, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(t.Context()) })

	now := time.Now().UTC()
	suffix := now.UnixNano()
	providerID := fmt.Sprintf("seat-map-shape-provider-%d", suffix)
	theaterID := fmt.Sprintf("seat-map-shape-theater-%d", suffix)
	movieID := fmt.Sprintf("seat-map-shape-movie-%d", suffix)
	auditoriumID := fmt.Sprintf("seat-map-shape-auditorium-%d", suffix)
	showtimeID := fmt.Sprintf("seat-map-shape-showtime-%d", suffix)
	assignmentID := fmt.Sprintf("seat-map-collection-shape-%d", suffix)
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, 'CGV', $2, $3, $3, $3)
	`, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '9999', '서울', '계약 검증 극장', $3, $4, $4, $4)
	`, theaterID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO movies (
			id, provider_id, source_key, title, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '99999999', '계약 검증 영화', $3, $4, $4, $4)
	`, movieID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, capacity, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '9999/0001', '1관', 1, $3, $4, $4, $4)
	`, auditoriumID, theaterID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO showtimes (
			id, provider_id, source_key, theater_id, movie_id, auditorium_id, schedule_date,
			starts_at, ends_at, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '9999/99999999/2099-01-01/1200', $3, $4, $5, '2099-01-01',
			$6::timestamptz + interval '1 hour', $6::timestamptz + interval '2 hours', $7, $6, $6, $6)
	`, showtimeID, providerID, theaterID, movieID, auditoriumID, now, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, status, not_before, deadline,
			created_at, updated_at, auditorium_id, showtime_id
		) VALUES ($1, 'cgv.seat-map.capture', '', '', '', '{}', 'ko-KR', 'Asia/Seoul',
			'', 'queued', $2::timestamptz, $2::timestamptz + interval '10 minutes',
			$2::timestamptz, $2::timestamptz, $3, $4)
	`, assignmentID, now, auditoriumID, showtimeID); err != nil {
		t.Fatal(err)
	}
	insert := func(state, reason string, assignmentID, stateShowtimeID string, nextAttempt any) {
		t.Helper()
		if _, err := tx.Exec(t.Context(), `
			INSERT INTO seat_map_collection_states (
				auditorium_id, state, trigger_kind, priority, assignment_id, showtime_id,
				reason_code, requested_at, next_attempt_at, updated_at
			) VALUES ($1, $2, 'operator_request', 80, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $6)
		`, auditoriumID, state, assignmentID, stateShowtimeID, reason, now, nextAttempt); err != nil {
			t.Fatalf("accepted %s state: %v", state, err)
		}
		if _, err := tx.Exec(t.Context(), `DELETE FROM seat_map_collection_states WHERE auditorium_id = $1`, auditoriumID); err != nil {
			t.Fatal(err)
		}
	}
	insert("queued", "", "", showtimeID, nil)
	insert("waiting_for_showtime", "showtime_not_discovered", "", "", nil)
	insert("retry_scheduled", "provider_transport_failed", "", showtimeID, now.Add(time.Minute))
	insert("blocked", "provider_transport_failed", "", showtimeID, nil)
	if assignmentID != "" {
		insert("collecting", "", assignmentID, showtimeID, nil)
	}

	assertRejected := func(name, state, reason, assignmentID, stateShowtimeID string, nextAttempt any) {
		t.Helper()
		if _, err := tx.Exec(t.Context(), `SAVEPOINT seat_map_shape_check`); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(t.Context(), `
			INSERT INTO seat_map_collection_states (
				auditorium_id, state, trigger_kind, priority, assignment_id, showtime_id,
				reason_code, requested_at, next_attempt_at, updated_at
			) VALUES ($1, $2, 'operator_request', 80, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $6)
		`, auditoriumID, state, assignmentID, stateShowtimeID, reason, now, nextAttempt)
		if err == nil {
			t.Fatalf("invalid %s state was accepted", name)
		}
		if _, rollbackErr := tx.Exec(t.Context(), `ROLLBACK TO SAVEPOINT seat_map_shape_check`); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if _, releaseErr := tx.Exec(t.Context(), `RELEASE SAVEPOINT seat_map_shape_check`); releaseErr != nil {
			t.Fatal(releaseErr)
		}
	}
	assertRejected("queued without showtime", "queued", "", "", "", nil)
	assertRejected("waiting without reason", "waiting_for_showtime", "", "", "", nil)
	assertRejected("retry without deadline", "retry_scheduled", "timeout", "", showtimeID, nil)
	assertRejected("retry without reason", "retry_scheduled", "", "", showtimeID, now.Add(time.Minute))
	assertRejected("blocked with waiting reason", "blocked", "showtime_not_discovered", "", showtimeID, nil)
	if assignmentID != "" {
		assertRejected("collecting without assignment", "collecting", "", "", showtimeID, nil)
	}
}

func schemaDocumentationRow(t *testing.T, document, table string) string {
	t.Helper()
	prefix := "| `" + table + "` |"
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("schema contract does not document migrated table %s", table)
	return ""
}
