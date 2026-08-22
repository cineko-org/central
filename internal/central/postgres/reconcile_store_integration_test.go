package postgres

import (
	"testing"
	"time"
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
