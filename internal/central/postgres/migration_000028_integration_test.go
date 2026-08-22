package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMigration000028NormalizesLatestClientResourceProtoJSON(t *testing.T) {
	connection, migration28 := newMigration000028Schema(t, "normalized")
	ctx := t.Context()
	const (
		userID        = "user_migration_28"
		providerID    = "provider_migration_28"
		theaterID     = "theater_migration_28"
		auditoriumID  = "auditorium_migration_28"
		movieID       = "movie_migration_28"
		showtimeID    = "showtime_migration_28"
		presetID      = "preset_migration_28"
		monitorID     = "monitor_migration_28"
		reservationID = "reservation_migration_28"
	)
	now := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	showtimeStart := now.Add(2 * time.Hour)
	seedMigration000028Catalog(t, connection, ctx, now, providerID, theaterID, auditoriumID, movieID, showtimeID)
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, 'Migration 28 User', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ('user_migration_28_empty_settings', 'Empty Settings User', $1, $1)
	`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES ('user_migration_28_empty_settings', 'settings', 'settings', 1, '{}'::jsonb, $1, $1)
	`, now); err != nil {
		t.Fatal(err)
	}

	settings := migration000028Settings()
	preset := migration000028Preset(userID, presetID, theaterID, auditoriumID, now)
	monitor := migration000028Monitor(userID, monitorID, presetID, movieID, now)
	showtime := migration000028Showtime(showtimeID, providerID, theaterID, movieID, auditoriumID, showtimeStart)
	reservation := migration000028Reservation(userID, reservationID, monitorID, showtime, now)
	operation := migration000028ExternalOperation(userID, "operation_migration_28", monitorID, reservationID, now)
	event := migration000028AppEvent(userID, "event_migration_28", now)
	resources := []struct {
		kind string
		id   string
		body proto.Message
	}{
		{kind: "settings", id: "settings", body: settings},
		{kind: "presets", id: presetID, body: preset},
		{kind: "monitors", id: monitorID, body: monitor},
		{kind: "reservations", id: reservationID, body: reservation},
		{kind: "external-operations", id: operation.GetId(), body: operation},
		{kind: "app-events", id: event.GetId(), body: event},
	}
	for _, resource := range resources {
		payload, err := protojson.Marshal(resource.body)
		if err != nil {
			t.Fatalf("marshal %s: %v", resource.kind, err)
		}
		if _, err := connection.Exec(ctx, `
			INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
			VALUES ($1, $2, $3, 1, $4::jsonb, $5, $5)
		`, userID, resource.kind, resource.id, payload, now); err != nil {
			t.Fatalf("insert %s: %v", resource.kind, err)
		}
	}

	assignmentTask := migration000028AssignmentTask(theaterID, auditoriumID)
	assignmentPayload, err := protojson.Marshal(assignmentTask)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_region, theater_name, theater_provider_id,
			theater_source_key, target_dates, locale, time_zone, egress_policy_id, status,
			not_before, deadline, created_at, updated_at, task_data
		) VALUES (
			'assignment_migration_28', 'cgv.seat-map.capture', $1, 'KR-11', 'Migration Theater',
			$2, 'theater-28', ARRAY['2026-08-23'::date], 'ko-KR', 'Asia/Seoul', 'scan_default',
			'queued', $3::timestamptz, $3::timestamptz + interval '1 hour', $3::timestamptz, $3::timestamptz, $4::jsonb
		)
	`, theaterID, providerID, now, assignmentPayload); err != nil {
		t.Fatal(err)
	}

	executionPayload := &executionpb.Payload{}
	executionPayload.SetShowtime(showtime)
	executionObservedAt := now.Add(30 * time.Minute)
	executionPayload.SetObservedAt(timestamppb.New(executionObservedAt))
	executionJSON, err := protojson.Marshal(executionPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_execution_commands (
			id, user_id, monitor_id, showtime_id, starts_at, payload, status,
			attempt_count, reason_code, created_at, updated_at
		) VALUES (
			'execution_migration_28', $1, $2, $3, $4, $5::jsonb, 'queued', 0, '', $6, $6
		)
	`, userID, monitorID, showtimeID, showtimeStart, executionJSON, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision,
			payload, occurred_at
		) VALUES
			('event_payload_migration_28', $1, 'settings.updated', 'settings', 'settings', 1, $2::jsonb, $3),
			('event_legacy_preset_migration_28', $1, 'presets.updated', 'presets', $4, 1, $5::jsonb, $3),
			('event_preset_deleted_migration_28', $1, 'presets.deleted', 'presets', $4, 2, '{"legacy":"ignored"}'::jsonb, $3)
	`, userID, mustProtoJSON(t, settings), now, presetID, mustProtoJSON(t, preset)); err != nil {
		t.Fatal(err)
	}

	if err := applyMigration(ctx, connection, migration28); err != nil {
		t.Fatalf("apply migration 000028 (%s): %v", migration28.name, err)
	}
	assertMigration000028Normalized(t, connection, ctx, userID, reservationID, executionObservedAt, auditoriumID)
	if err := applyMigration(ctx, connection, migration28); err != nil {
		t.Fatalf("reapply migration 000028 (%s): %v", migration28.name, err)
	}
	var emptyNetworkMode *string
	if err := connection.QueryRow(ctx, `
		SELECT network_mode FROM client_settings
		WHERE user_id = 'user_migration_28_empty_settings' AND id = 'settings'
	`).Scan(&emptyNetworkMode); err != nil {
		t.Fatal(err)
	}
	if emptyNetworkMode != nil {
		t.Fatalf("empty Settings network_mode = %q, want NULL", *emptyNetworkMode)
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

func mustProtoJSON(t *testing.T, message proto.Message) []byte {
	t.Helper()
	payload, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
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

func seedMigration000028Catalog(
	t *testing.T,
	connection *pgxpool.Conn,
	ctx context.Context,
	now time.Time,
	providerID, theaterID, auditoriumID, movieID, showtimeID string,
) {
	t.Helper()
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := connection.Exec(ctx, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, 'CGV', $2, $3, $3, $3)
	`, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, 'theater-source-28', 'KR-11', 'Migration Theater', $3, $4, $4, $4)
	`, theaterID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, screen_types, capacity, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, 'auditorium-source-28', 'IMAX', ARRAY['IMAX', 'PREMIUM'], 300, $3, $4, $4, $4)
	`, auditoriumID, theaterID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO movies (
			id, provider_id, source_key, title, poster_url, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, 'movie-source-28', 'Migration Movie', 'https://movie.example/poster', $3, $4, $4, $4)
	`, movieID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	showtime := migration000028Showtime(showtimeID, providerID, theaterID, movieID, auditoriumID, now.Add(2*time.Hour))
	if _, err := connection.Exec(ctx, `
		INSERT INTO showtimes (
			id, provider_id, source_key, theater_id, movie_id, auditorium_id,
			starts_at, ends_at, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $10)
	`, showtime.GetId(), providerID, showtime.GetSourceKey(), theaterID, movieID, auditoriumID,
		showtime.GetStartsAt().AsTime(), showtime.GetEndsAt().AsTime(), hash, now); err != nil {
		t.Fatal(err)
	}
}

func migration000028Settings() *clientpb.Settings {
	proxy := &clientpb.ProxyNetwork{}
	proxy.SetUrls([]string{"https://proxy-a.example", "https://proxy-b.example"})
	proxy.SetUsername("proxy-user")
	proxy.SetPassword("proxy-secret")
	proxy.SetHasPassword(true)
	network := &clientpb.NetworkSettings{}
	network.SetProxy(proxy)
	webhookA := &clientpb.WebhookTarget{}
	webhookA.SetId("webhook-a")
	webhookA.SetName("A")
	webhookA.SetUrl("https://hooks.example/a")
	webhookA.SetSecret("secret-a")
	webhookA.SetEventKinds([]string{"reservation.booked", "reservation.cancelled"})
	webhookA.SetEnabled(true)
	webhookA.SetHasSecret(true)
	webhookB := &clientpb.WebhookTarget{}
	webhookB.SetId("webhook-b")
	webhookB.SetName("B")
	webhookB.SetUrl("https://hooks.example/b")
	webhookB.SetEventKinds([]string{"monitor.failed"})
	webhookB.SetEnabled(false)
	settings := &clientpb.Settings{}
	settings.SetNetwork(network)
	settings.SetWebhooks([]*clientpb.WebhookTarget{webhookA, webhookB})
	return settings
}

func migration000028Preset(userID, id, theaterID, auditoriumID string, now time.Time) *clientpb.Preset {
	zone := &clientpb.SeatZone{}
	zone.SetName("center")
	zone.SetMinX(0.1)
	zone.SetMaxX(0.9)
	zone.SetMinY(0.2)
	zone.SetMaxY(0.8)
	zone.SetWeight(3)
	preference := &clientpb.SeatPreference{}
	preference.SetExplicitSeats([]string{"A1", "A2"})
	preference.SetPreferredRows([]string{"A", "B"})
	preference.SetPreferredZones([]*clientpb.SeatZone{zone})
	preference.SetPreferredTypes([]string{"IMAX", "PREMIUM"})
	preference.SetTogether(true)
	preset := &clientpb.Preset{}
	preset.SetId(id)
	preset.SetUserId(userID)
	preset.SetName("Migration Preset")
	preset.SetTheaterId(theaterID)
	preset.SetAuditoriumId(auditoriumID)
	preset.SetSeatCount(2)
	preset.SetSeatPreference(preference)
	preset.SetCreatedAt(timestamppb.New(now.Add(-time.Hour)))
	preset.SetUpdatedAt(timestamppb.New(now))
	return preset
}

func migration000028Monitor(userID, id, presetID, movieID string, now time.Time) *clientpb.Monitor {
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	earliest := &commonpb.LocalTime{}
	earliest.SetHour(9)
	earliest.SetMinute(30)
	latest := &commonpb.LocalTime{}
	latest.SetHour(23)
	latest.SetMinute(45)
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(23)
	monitor := &clientpb.Monitor{}
	monitor.SetId(id)
	monitor.SetUserId(userID)
	monitor.SetPresetId(presetID)
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle("Migration Movie")
	monitor.SetTargetDates([]*commonpb.LocalDate{date})
	monitor.SetTargetWeekdays([]int32{1, 3})
	monitor.SetSearchHorizonDays(7)
	monitor.SetEarliestTime(earliest)
	monitor.SetLatestTime(latest)
	monitor.SetState(state)
	monitor.SetCreatedAt(timestamppb.New(now.Add(-time.Hour)))
	monitor.SetUpdatedAt(timestamppb.New(now))
	return monitor
}

func migration000028Showtime(id, providerID, theaterID, movieID, auditoriumID string, startsAt time.Time) *catalogpb.Showtime {
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(providerID)
	movie.SetSourceKey("movie-source-28")
	movie.SetTitle("Migration Movie")
	movie.SetPosterUrl("https://movie.example/poster")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(theaterID)
	auditorium.SetSourceKey("auditorium-source-28")
	auditorium.SetName("IMAX")
	auditorium.SetScreenTypes([]string{"IMAX", "PREMIUM"})
	auditorium.SetCapacity(300)
	auditorium.SetCurrentLayoutHash("layout-hash-28")
	showtime := &catalogpb.Showtime{}
	showtime.SetId(id)
	showtime.SetProviderId(providerID)
	showtime.SetSourceKey("showtime-source-28")
	showtime.SetTheaterId(theaterID)
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	scheduleDate := &commonpb.LocalDate{}
	scheduleDate.SetYear(numeric.ClampInt32(startsAt.Year()))
	scheduleDate.SetMonth(numeric.ClampInt32(int(startsAt.Month())))
	scheduleDate.SetDay(numeric.ClampInt32(startsAt.Day()))
	showtime.SetScheduleDate(scheduleDate)
	showtime.SetStartsAt(timestamppb.New(startsAt))
	showtime.SetEndsAt(timestamppb.New(startsAt.Add(2 * time.Hour)))
	showtime.SetAvailableSeats(120)
	showtime.SetCapacity(300)
	showtime.SetSoldOut(false)
	return showtime
}

func migration000028Reservation(userID, id, monitorID string, showtime *catalogpb.Showtime, now time.Time) *clientpb.Reservation {
	state := &clientpb.Reservation{}
	state.SetId(id)
	state.SetUserId(userID)
	state.SetMonitorId(monitorID)
	state.SetBookingNumber("booking-28")
	state.SetSeatLabels([]string{"A1", "A2"})
	state.SetTotalPrice("24000")
	state.SetBookedAt(timestamppb.New(now))
	state.SetRefundAmount("0")
	state.SetBooked(&clientpb.ReservationBooked{})
	state.SetShowtime(showtime)
	return state
}

func migration000028ExternalOperation(userID, id, monitorID, reservationID string, now time.Time) *clientpb.ExternalOperation {
	operation := &clientpb.ExternalOperation{}
	operation.SetId(id)
	operation.SetUserId(userID)
	operation.SetMonitorId(monitorID)
	operation.SetReservationId(reservationID)
	operation.SetRefundAmount("0")
	operation.SetLastError("")
	operation.SetCreatedAt(timestamppb.New(now))
	operation.SetUpdatedAt(timestamppb.New(now))
	operation.SetCancellation(&clientpb.CancellationOperation{})
	operation.SetConfirmed(&clientpb.OperationConfirmed{})
	return operation
}

func migration000028AppEvent(userID, id string, now time.Time) *clientpb.AppEvent {
	event := &clientpb.AppEvent{}
	event.SetId(id)
	event.SetUserId(userID)
	event.SetKind("reservation.booked")
	event.SetMessage("Reservation booked")
	event.SetCreatedAt(timestamppb.New(now))
	event.SetReadAt(timestamppb.New(now.Add(time.Minute)))
	event.SetSuccess(&clientpb.EventSuccess{})
	return event
}

func migration000028AssignmentTask(theaterID, auditoriumID string) *observationpb.AssignmentTask {
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId("provider_migration_28")
	theater.SetSourceKey("theater-source-28")
	theater.SetRegion("KR-11")
	theater.SetName("Migration Theater")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(theaterID)
	auditorium.SetSourceKey("auditorium-source-28")
	auditorium.SetName("IMAX")
	auditorium.SetScreenTypes([]string{"IMAX"})
	auditorium.SetCapacity(300)
	auditorium.SetCurrentLayoutHash("layout-hash-28")
	seatMap := &observationpb.SeatMapTask{}
	seatMap.SetTheater(theater)
	seatMap.SetAuditorium(auditorium)
	seatMap.SetLocale("ko-KR")
	seatMap.SetTimeZone("Asia/Seoul")
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task := &observationpb.AssignmentTask{}
	task.SetEgress(egress)
	task.SetSeatMap(seatMap)
	return task
}

func assertMigration000028Normalized(
	t *testing.T,
	connection *pgxpool.Conn,
	ctx context.Context,
	userID, reservationID string,
	executionObservedAt time.Time,
	wantAuditoriumID string,
) {
	t.Helper()
	var payloadExists bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'client_resources' AND column_name = 'payload'
		)
	`).Scan(&payloadExists); err != nil {
		t.Fatal(err)
	}
	if payloadExists {
		t.Fatal("client_resources.payload remains after migration")
	}
	for _, table := range []string{
		"client_settings", "client_presets", "client_monitors", "client_reservations",
		"client_external_operations", "client_app_events",
	} {
		var count int
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, userID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s rows = %d, want 1", table, count)
		}
	}
	var urls []string
	if err := connection.QueryRow(ctx, `
		SELECT array_agg(url ORDER BY position)
		FROM client_setting_proxy_urls WHERE user_id = $1 AND settings_id = 'settings'
	`, userID).Scan(&urls); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "https://proxy-a.example" || urls[1] != "https://proxy-b.example" {
		t.Fatalf("proxy URL order = %v", urls)
	}
	var webhookIDs []string
	if err := connection.QueryRow(ctx, `
		SELECT array_agg(id ORDER BY position)
		FROM client_setting_webhooks WHERE user_id = $1 AND settings_id = 'settings'
	`, userID).Scan(&webhookIDs); err != nil {
		t.Fatal(err)
	}
	if len(webhookIDs) != 2 || webhookIDs[0] != "webhook-a" || webhookIDs[1] != "webhook-b" {
		t.Fatalf("webhook order = %v", webhookIDs)
	}
	var hasPreference bool
	var presetCreated, presetUpdated time.Time
	if err := connection.QueryRow(ctx, `
		SELECT has_seat_preference, preset_created_at, preset_updated_at
		FROM client_presets WHERE user_id = $1 AND id = 'preset_migration_28'
	`, userID).Scan(&hasPreference, &presetCreated, &presetUpdated); err != nil {
		t.Fatal(err)
	}
	if !hasPreference || presetCreated.IsZero() || presetUpdated.IsZero() {
		t.Fatalf("preset presence/timestamps = %t/%v/%v", hasPreference, presetCreated, presetUpdated)
	}
	var state string
	var horizon, earliest, latest int
	if err := connection.QueryRow(ctx, `
		SELECT state, search_horizon_days, earliest_minute, latest_minute
		FROM client_monitors WHERE user_id = $1 AND id = 'monitor_migration_28'
	`, userID).Scan(&state, &horizon, &earliest, &latest); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || horizon != 7 || earliest != 9*60+30 || latest != 23*60+45 {
		t.Fatalf("monitor normalized values = %q/%d/%d/%d", state, horizon, earliest, latest)
	}
	var legacyColumns int
	if err := connection.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'client_monitors'
			AND column_name IN ('mode', 'poll_interval_nanos', 'maximum_poll_interval_nanos')
	`).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if legacyColumns != 0 {
		t.Fatalf("client_monitors retained %d legacy mode/cadence columns", legacyColumns)
	}
	var snapshotMovieProvider, snapshotMovieSource, layoutHash string
	var scheduleDate time.Time
	var available, capacity int
	if err := connection.QueryRow(ctx, `
		SELECT movie_provider_id, movie_source_key, auditorium_layout_hash, schedule_date,
			available_seats, capacity
		FROM client_reservation_showtimes WHERE user_id = $1 AND reservation_id = $2
	`, userID, reservationID).Scan(
		&snapshotMovieProvider, &snapshotMovieSource, &layoutHash, &scheduleDate, &available, &capacity,
	); err != nil {
		t.Fatal(err)
	}
	if snapshotMovieProvider != "provider_migration_28" || snapshotMovieSource != "movie-source-28" ||
		layoutHash != "layout-hash-28" || scheduleDate.Format(time.DateOnly) != "2026-08-22" ||
		available != 120 || capacity != 300 {
		t.Fatalf("reservation snapshot = %q/%q/%q/%s/%d/%d", snapshotMovieProvider,
			snapshotMovieSource, layoutHash, scheduleDate.Format(time.DateOnly), available, capacity)
	}
	var assignmentAuditorium string
	if err := connection.QueryRow(ctx, `
		SELECT auditorium_id FROM observation_assignments WHERE id = 'assignment_migration_28'
	`).Scan(&assignmentAuditorium); err != nil {
		t.Fatal(err)
	}
	if assignmentAuditorium != wantAuditoriumID {
		t.Fatalf("assignment auditorium_id = %q, want %q", assignmentAuditorium, wantAuditoriumID)
	}
	var observedAt time.Time
	if err := connection.QueryRow(ctx, `
		SELECT observed_at FROM client_execution_commands WHERE id = 'execution_migration_28'
	`).Scan(&observedAt); err != nil {
		t.Fatal(err)
	}
	if !observedAt.Equal(executionObservedAt) {
		t.Fatalf("execution observed_at = %v, want %v", observedAt, executionObservedAt)
	}
	var executionPayloadExists, eventPayloadExists bool
	if err := connection.QueryRow(ctx, `
		SELECT
			(SELECT payload IS NOT NULL FROM client_execution_commands WHERE id = 'execution_migration_28'),
			(SELECT payload IS NOT NULL FROM client_events WHERE id = 'event_payload_migration_28')
	`).Scan(&executionPayloadExists, &eventPayloadExists); err != nil {
		t.Fatal(err)
	}
	if !executionPayloadExists || !eventPayloadExists {
		t.Fatalf("retained payloads = execution:%t events:%t", executionPayloadExists, eventPayloadExists)
	}
	var deletedPayload []byte
	if err := connection.QueryRow(ctx, `
		SELECT payload FROM client_events WHERE id = 'event_preset_deleted_migration_28'
	`).Scan(&deletedPayload); err != nil {
		t.Fatal(err)
	}
	if string(deletedPayload) != `{}` {
		t.Fatalf("deleted event payload = %s, want {}", deletedPayload)
	}
}
