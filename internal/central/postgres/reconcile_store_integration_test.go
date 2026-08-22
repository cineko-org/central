package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSeatMapBackfillTargetDoesNotRequireStoredShowtime(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const (
		providerID   = "provider_seat_map_without_showtime"
		theaterID    = "theater_seat_map_without_showtime"
		auditoriumID = "auditorium_seat_map_without_showtime"
	)
	cleanup := func() {
		_, _ = store.pool.Exec(t.Context(), `DELETE FROM auditoriums WHERE id = $1`, auditoriumID)
		_, _ = store.pool.Exec(t.Context(), `DELETE FROM theaters WHERE id = $1`, theaterID)
		_, _ = store.pool.Exec(t.Context(), `DELETE FROM providers WHERE id = $1`, providerID)
	}
	cleanup()
	t.Cleanup(cleanup)
	now := time.Date(2026, 8, 22, 4, 0, 0, 0, time.UTC)
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, 'CGV', $2, $3, $3, $3)
	`, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '0056', '서울', '용산아이파크몰', $3, $4, $4, $4)
	`, theaterID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, screen_types, capacity, content_hash,
			first_seen_at, last_seen_at, updated_at, seat_map_requested_at
		) VALUES ($1, $2, '0056/0007', 'IMAX관', ARRAY['IMAX'], 624, $3, $4, $4, $4, $4)
	`, auditoriumID, theaterID, hash, now); err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(t.Context()) })
	target, err := (&cycleStore{tx: tx}).SeatMapBackfillTarget(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil || !target.Requested || target.Task.GetSeatMap().GetShowtime() != nil ||
		len(target.Task.GetSeatMap().GetTargetDates()) != seatMapBackfillHorizonDays {
		t.Fatalf("seat-map backfill target = %+v", target)
	}
}

func TestSeatMapBackfillWithoutShowtimeCommitsCapturedLayout(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const (
		providerID   = "provider_seat_map_commit_without_showtime"
		theaterID    = "theater_seat_map_commit_without_showtime"
		auditoriumID = "auditorium_seat_map_commit_without_showtime"
		probeID      = "probe_seat_map_commit_without_showtime"
		assignmentID = "assignment_seat_map_commit_without_showtime"
	)
	cleanup := func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM assignment_attempts WHERE assignment_id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM assignment_eligible_probes WHERE assignment_id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM observation_assignments WHERE id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `UPDATE auditoriums SET current_seat_map_version_id = NULL WHERE id = $1`, auditoriumID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM seat_map_versions WHERE auditorium_id = $1`, auditoriumID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM probe_runtimes WHERE id = $1`, probeID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM auditoriums WHERE id = $1`, auditoriumID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM theaters WHERE id = $1`, theaterID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM providers WHERE id = $1`, providerID)
	}
	cleanup()
	t.Cleanup(cleanup)
	now := time.Now().UTC().Truncate(time.Microsecond)
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, 'CGV', $2, $3, $3, $3)
	`, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, '0056', '서울', '용산아이파크몰', $3, $4, $4, $4)
	`, theaterID, providerID, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, screen_types, capacity, content_hash,
			first_seen_at, last_seen_at, updated_at, seat_map_requested_at
		) VALUES ($1, $2, '0056/0007', 'IMAX관', ARRAY['IMAX'], 1, $3, $4, $4, $4, $4)
	`, auditoriumID, theaterID, hash, now); err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("token_" + probeID))
	if _, err := store.RegisterProbe(ctx, central.Probe{
		ID: probeID, InstallationID: "install_" + probeID, Kind: "container", NetworkID: "net_" + probeID,
		Capabilities: []string{probedomain.CapabilityCGVSeatMapCapture}, MaxConcurrency: 1,
		Runtime: storeIntegrationRuntime("1.0.0", "2000", "linux", "amd64"), TokenHash: tokenHash,
		TokenExpiresAt: now.Add(time.Hour), Status: "online", Health: "healthy", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	capability := &observationpb.Capability{}
	capability.SetSeatMapCapture(&observationpb.SeatMapCapture{})
	health := &probepb.ProbeHealth{}
	health.SetHealthy(&probepb.Healthy{})
	heartbeat := &probepb.HeartbeatRequest{}
	heartbeat.SetAvailableCapabilities([]*observationpb.Capability{capability})
	heartbeat.SetAvailableSlots(1)
	heartbeat.SetHealth(health)
	if _, err := store.HeartbeatProbe(ctx, probeID, heartbeat, now); err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cycle := &cycleStore{tx: tx}
	target, err := cycle.SeatMapBackfillTarget(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil || target.Task.GetSeatMap().GetShowtime() != nil {
		t.Fatalf("seat-map target = %+v", target)
	}
	if err := cycle.CreateAssignment(ctx, reconcile.NewAssignment{
		ID: assignmentID, Task: target.Task, Priority: 95, Status: "queued", NotBefore: now,
		Deadline: now.Add(10 * time.Minute), CreatedAt: now,
		Candidates: []reconcile.CandidateProbe{{ID: probeID, NetworkID: "net_" + probeID}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	leaseHash := sha256.Sum256([]byte("lease_" + assignmentID))
	claimed, err := store.ClaimAssignment(ctx, probeID, leaseHash, now, now.Add(time.Minute), now.Add(-time.Minute))
	if err != nil || claimed.ID != assignmentID {
		t.Fatalf("claimed assignment = %+v, %v", claimed, err)
	}
	seat := &seatmappb.Seat{}
	seat.SetId(catalogdomain.SeatID(auditoriumID, "A1"))
	seat.SetAuditoriumId(auditoriumID)
	seat.SetLabel("A1")
	seat.SetRow("A")
	seat.SetNumber(1)
	seat.SetX(0.5)
	seat.SetY(0.5)
	seat.SetType("premium")
	seat.SetZoneName("중앙")
	seat.SetZoneKind("preferred")
	seat.SetSaleFormCode("imax")
	seat.SetSaleFormName("IMAX")
	seat.SetLeftAisle(true)
	seat.SetFeatures([]string{"aisle", "premium"})
	seat.SetSourceLabel("A01")
	seat.SetSourceSeatKindCode("P")
	seat.SetSourceSeatKindName("Premium")
	seat.SetSourceClasses([]string{"seat", "seat-premium"})
	zone := &seatmappb.LayoutZone{}
	zone.SetCode("center")
	zone.SetName("중앙")
	zone.SetKindCode("preferred")
	zone.SetKindName("선호")
	zone.SetMinX(0.25)
	zone.SetMaxX(0.75)
	zone.SetMinY(0.25)
	zone.SetMaxY(0.75)
	zone.SetCapacity(1)
	block := &seatmappb.LayoutBlock{}
	block.SetCode("main")
	block.SetName("Main")
	block.SetKindCode("standard")
	block.SetKindName("Standard")
	block.SetMinX(0)
	block.SetMaxX(1)
	block.SetMinY(0)
	block.SetMaxY(1)
	layout := &seatmappb.Layout{}
	layout.SetSeats([]*seatmappb.Seat{seat})
	layout.SetZones([]*seatmappb.LayoutZone{zone})
	layout.SetBlocks([]*seatmappb.LayoutBlock{block})
	snapshot := &seatmappb.Snapshot{}
	snapshot.SetAuditoriumId(auditoriumID)
	snapshot.SetCapacity(1)
	snapshot.SetLayout(layout)
	snapshot.SetObservedAt(timestamppb.New(now))
	if err := catalogdomain.NormalizeSeatMap(snapshot, now); err != nil {
		t.Fatal(err)
	}
	completed := &observationpb.Completed{}
	completed.SetSeatMap(snapshot)
	result := &observationpb.AssignmentResult{}
	result.SetRunId("run_" + assignmentID)
	result.SetStartedAt(timestamppb.New(now.Add(-time.Second)))
	result.SetFinishedAt(timestamppb.New(now))
	result.SetCompleted(completed)
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	receipt, err := store.CommitResult(ctx, central.ResultCommit{
		AssignmentID: assignmentID, ProbeID: probeID, LeaseHash: leaseHash,
		PayloadHash: hex.EncodeToString(digest[:]), Result: result, CommittedAt: now,
	})
	if err != nil || receipt.GetAccepted() == nil {
		t.Fatalf("seat-map result receipt = %+v, %v", receipt, err)
	}
	stored, err := store.SeatMap(ctx, auditoriumID)
	if err != nil || stored.GetLayoutHash() != snapshot.GetLayoutHash() || !proto.Equal(stored.GetLayout(), snapshot.GetLayout()) {
		t.Fatalf("stored seat map = %+v, %v", stored, err)
	}
	var seatCount, featureCount, sourceClassCount, zoneCount, blockCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM seat_map_seats WHERE version_id = $1),
			(SELECT count(*) FROM seat_map_seat_features WHERE version_id = $1),
			(SELECT count(*) FROM seat_map_seat_source_classes WHERE version_id = $1),
			(SELECT count(*) FROM seat_map_zones WHERE version_id = $1),
			(SELECT count(*) FROM seat_map_blocks WHERE version_id = $1)
	`, snapshot.GetId()).Scan(&seatCount, &featureCount, &sourceClassCount, &zoneCount, &blockCount); err != nil {
		t.Fatal(err)
	}
	if seatCount != 1 || featureCount != 2 || sourceClassCount != 2 || zoneCount != 1 || blockCount != 1 {
		t.Fatalf(
			"normalized layout counts = seats %d, features %d, source classes %d, zones %d, blocks %d",
			seatCount, featureCount, sourceClassCount, zoneCount, blockCount,
		)
	}
}

func TestSeatAvailabilityTargetRestrictsToCGVProvider(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		userID            = "user_seat_availability_provider_filter"
		otherProviderID   = "provider_seat_availability_other"
		otherTheaterID    = "theater_seat_availability_other"
		otherAuditoriumID = "auditorium_seat_availability_other"
		otherMovieID      = "movie_seat_availability_other"
		otherShowtimeID   = "showtime_seat_availability_other"
		otherPresetID     = "preset_seat_availability_other"
		otherMonitorID    = "monitor_seat_availability_other"
		cgvTheaterID      = "theater_seat_availability_cgv"
		cgvAuditoriumID   = "auditorium_seat_availability_cgv"
		cgvMovieID        = "movie_seat_availability_cgv"
		cgvShowtimeID     = "showtime_seat_availability_cgv"
		cgvPresetID       = "preset_seat_availability_cgv"
		cgvMonitorID      = "monitor_seat_availability_cgv"
	)
	cleanup := func() {
		cleanupClientResourceUser(t, store, userID)
		cleanupClientResourceCatalog(t, store, otherProviderID,
			[]string{otherTheaterID}, []string{otherAuditoriumID}, []string{otherMovieID})
		cleanupClientResourceCatalog(t, store, catalogdomain.ProviderCGV,
			[]string{cgvTheaterID}, []string{cgvAuditoriumID}, []string{cgvMovieID})
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC().Truncate(time.Second)
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	scheduleDate := now.Add(2 * time.Hour).In(location).Format(time.DateOnly)
	otherShowtime := seatAvailabilityTargetShowtime(
		otherShowtimeID, otherProviderID, otherTheaterID, otherMovieID, otherAuditoriumID,
		now.Add(time.Hour), scheduleDate,
	)
	cgvShowtime := seatAvailabilityTargetShowtime(
		cgvShowtimeID, catalogdomain.ProviderCGV, cgvTheaterID, cgvMovieID, cgvAuditoriumID,
		now.Add(2*time.Hour), scheduleDate,
	)
	seedClientResourceCatalog(t, store, otherProviderID, otherTheaterID, otherAuditoriumID, otherMovieID, otherShowtime)
	seedClientResourceCatalog(t, store, catalogdomain.ProviderCGV, cgvTheaterID, cgvAuditoriumID, cgvMovieID, cgvShowtime)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, 'Seat availability provider filter', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		providerID, theaterID, auditoriumID, movieID, presetID, monitorID string
		showtimeID                                                        string
	}{{
		providerID: otherProviderID, theaterID: otherTheaterID, auditoriumID: otherAuditoriumID,
		movieID: otherMovieID, presetID: otherPresetID, monitorID: otherMonitorID,
		showtimeID: otherShowtimeID,
	}, {
		providerID: catalogdomain.ProviderCGV, theaterID: cgvTheaterID, auditoriumID: cgvAuditoriumID,
		movieID: cgvMovieID, presetID: cgvPresetID, monitorID: cgvMonitorID,
		showtimeID: cgvShowtimeID,
	}} {
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO client_resources (user_id, kind, id, revision, created_at, updated_at)
			VALUES ($1, 'presets', $2, 1, $3, $3),
			       ($1, 'monitors', $4, 1, $3, $3)
		`, userID, target.presetID, now, target.monitorID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO client_presets (
				user_id, resource_kind, id, name, theater_id, auditorium_id, seat_count,
				has_seat_preference, together, avoid_edges, preset_created_at, preset_updated_at
			) VALUES ($1, 'presets', $2, 'Provider filter preset', $3, $4, 1, false, false, false, $5, $5)
		`, userID, target.presetID, target.theaterID, target.auditoriumID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO client_monitors (
				user_id, resource_kind, id, preset_id, movie_id, movie_title,
				search_horizon_days, state, monitor_created_at, monitor_updated_at
			) VALUES ($1, 'monitors', $2, $3, $4, 'Provider filter movie', 14, 'pending', $5, $5)
		`, userID, target.monitorID, target.presetID, target.movieID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO client_monitor_target_dates (user_id, monitor_id, position, target_date)
			VALUES ($1, $2, 0, $3::date)
		`, userID, target.monitorID, scheduleDate); err != nil {
			t.Fatal(err)
		}
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	target, err := (&cycleStore{tx: tx}).SeatAvailabilityTarget(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil || target.Task.GetSeatAvailability() == nil ||
		target.Task.GetSeatAvailability().GetShowtime().GetId() != cgvShowtimeID ||
		target.Task.GetSeatAvailability().GetShowtime().GetProviderId() != catalogdomain.ProviderCGV {
		t.Fatalf("seat-availability target = %+v", target)
	}
}

func cleanupClientResourceUser(t *testing.T, store *Store, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `DELETE FROM client_resources WHERE user_id = $1`, userID); err != nil {
		t.Errorf("clean Client resource user resources: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM client_users WHERE id = $1`, userID); err != nil {
		t.Errorf("clean Client resource user: %v", err)
	}
}

func seatAvailabilityTargetShowtime(
	id, providerID, theaterID, movieID, auditoriumID string,
	startsAt time.Time, scheduleDate string,
) *catalogpb.Showtime {
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(providerID)
	movie.SetSourceKey(movieID)
	movie.SetTitle("Provider filter movie")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(theaterID)
	auditorium.SetSourceKey(auditoriumID)
	auditorium.SetName("Provider filter auditorium")
	auditorium.SetScreenTypes([]string{"STANDARD"})
	auditorium.SetCapacity(100)
	showtime := &catalogpb.Showtime{}
	showtime.SetId(id)
	showtime.SetProviderId(providerID)
	showtime.SetSourceKey(id)
	showtime.SetTheaterId(theaterID)
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetScheduleDate(localDateMessage(scheduleDate))
	showtime.SetStartsAt(timestamppb.New(startsAt))
	showtime.SetEndsAt(timestamppb.New(startsAt.Add(90 * time.Minute)))
	showtime.SetAvailableSeats(50)
	showtime.SetCapacity(100)
	return showtime
}
