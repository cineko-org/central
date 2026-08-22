package postgres

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration000027NormalizesExistingSeatMapLayouts(t *testing.T) {
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
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	const testSchema = "migration_000027_test"
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
		CREATE TABLE cineko_schema_migrations (
			version bigint PRIMARY KEY,
			checksum text NOT NULL DEFAULT '',
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatal(err)
	}
	allMigrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var migration27 migration
	for _, item := range allMigrations {
		switch {
		case item.version <= 26:
			if err := applyMigration(ctx, connection, item); err != nil {
				t.Fatalf("apply migration %d (%s): %v", item.version, item.name, err)
			}
		case item.version == 27:
			migration27 = item
		}
	}
	if migration27.version != 27 {
		t.Fatal("migration 000027 was not loaded")
	}
	now := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := connection.Exec(ctx, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ('provider_migration_27', 'CGV', $1, $2, $2, $2)
	`, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ('theater_migration_27', 'provider_migration_27', '0056', '서울', '용산아이파크몰', $1, $2, $2, $2)
	`, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, screen_types, capacity, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ('auditorium_migration_27', 'theater_migration_27', '0056/0007', 'IMAX관', ARRAY['IMAX'], 1, $1, $2, $2, $2)
	`, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO seat_map_versions (
			id, auditorium_id, layout_hash, capacity, layout, observed_at, first_seen_at, last_seen_at
		) VALUES (
			'version_migration_27', 'auditorium_migration_27', $1, 1,
			$3::jsonb, $2, $2, $2
		)
	`, hash, now, `{
		"seats":[{
			"id":"seat_migration_27","auditoriumId":"auditorium_migration_27",
			"label":"A1","row":"A","number":1,"x":0.5,"y":0.5,"type":"premium",
			"zoneName":"Center","zoneKind":"preferred","saleFormCode":"imax","saleFormName":"IMAX",
			"leftAisle":true,"features":["aisle","premium"],"sourceLabel":"A01",
			"sourceSeatKindCode":"P","sourceSeatKindName":"Premium","sourceClasses":["seat","seat-premium"]
		}],
		"zones":[{
			"code":"center","name":"Center","kindCode":"preferred","kindName":"Preferred",
			"minX":0.25,"maxX":0.75,"minY":0.25,"maxY":0.75,"capacity":1
		}],
		"blocks":[{
			"code":"main","name":"Main","kindCode":"standard","kindName":"Standard",
			"minX":0,"maxX":1,"minY":0,"maxY":1
		}]
	}`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		UPDATE auditoriums SET current_seat_map_version_id = 'version_migration_27'
		WHERE id = 'auditorium_migration_27'
	`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, connection, migration27); err != nil {
		t.Fatalf("apply migration 27 (%s): %v", migration27.name, err)
	}
	var layoutColumnExists bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'seat_map_versions' AND column_name = 'layout'
		)
	`, testSchema).Scan(&layoutColumnExists); err != nil {
		t.Fatal(err)
	}
	if layoutColumnExists {
		t.Fatal("seat_map_versions.layout was not removed")
	}
	var label, rowLabel, seatType, zoneName, sourceLabel string
	var features, sourceClasses []string
	var leftAisle bool
	if err := connection.QueryRow(ctx, `
		SELECT seat.label, seat.row_label, seat.seat_type, seat.zone_name, seat.left_aisle,
			ARRAY(
				SELECT feature.feature FROM seat_map_seat_features AS feature
				WHERE feature.version_id = seat.version_id AND feature.seat_id = seat.seat_id
				ORDER BY feature.position
			),
			seat.source_label,
			ARRAY(
				SELECT source_class.source_class FROM seat_map_seat_source_classes AS source_class
				WHERE source_class.version_id = seat.version_id AND source_class.seat_id = seat.seat_id
				ORDER BY source_class.position
			)
		FROM seat_map_seats AS seat WHERE version_id = 'version_migration_27'
	`).Scan(&label, &rowLabel, &seatType, &zoneName, &leftAisle, &features, &sourceLabel, &sourceClasses); err != nil {
		t.Fatal(err)
	}
	if label != "A1" || rowLabel != "A" || seatType != "premium" || zoneName != "Center" || !leftAisle || sourceLabel != "A01" ||
		!slices.Equal(features, []string{"aisle", "premium"}) || !slices.Equal(sourceClasses, []string{"seat", "seat-premium"}) {
		t.Fatalf("normalized seat = %q/%q/%q/%q/%t/%v/%q/%v", label, rowLabel, seatType, zoneName, leftAisle, features, sourceLabel, sourceClasses)
	}
	var zones, blocks int
	if err := connection.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM seat_map_zones WHERE version_id = 'version_migration_27'),
			(SELECT count(*) FROM seat_map_blocks WHERE version_id = 'version_migration_27')
	`).Scan(&zones, &blocks); err != nil {
		t.Fatal(err)
	}
	if zones != 1 || blocks != 1 {
		t.Fatalf("normalized layout children = zones %d, blocks %d", zones, blocks)
	}
}
