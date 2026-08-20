package postgres

import (
	"context"
	"strings"
	"testing"

	centralapi "github.com/cineko-org/central/internal/central/api"
)

// TestPostgresUpdatesObservationPolicy exercises the production UPDATE
// signature so PostgreSQL parameter inference cannot drift unnoticed.
func TestPostgresUpdatesObservationPolicy(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		providerID = "provider_observation_update_integration"
		theaterID  = "theater_observation_update_integration"
	)
	cleanup := func() {
		for _, query := range []struct {
			statement string
			argument  string
		}{
			{`DELETE FROM observation_policies WHERE theater_id = $1`, theaterID},
			{`DELETE FROM theaters WHERE id = $1`, theaterID},
			{`DELETE FROM providers WHERE id = $1`, providerID},
		} {
			if _, cleanupErr := store.pool.Exec(ctx, query.statement, query.argument); cleanupErr != nil {
				t.Errorf("clean observation policy fixture: %v", cleanupErr)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	digest := strings.Repeat("a", 64)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, 'Integration provider', $2, now(), now(), now())
	`, providerID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO theaters (
			id, provider_id, source_key, region, name, active, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($2, $1, 'source-observation-update', 'Seoul', 'Integration theater', true, $3, now(), now(), now())
	`, providerID, theaterID, digest); err != nil {
		t.Fatal(err)
	}

	input := centralapi.AdminObservationPolicyInput{
		TheaterID: theaterID, Enabled: true, HorizonDays: 14, Priority: 50,
		BaselineMinSeconds: 300, BaselineMaxSeconds: 900,
		DemandMinSeconds: 30, DemandMaxSeconds: 45,
		BurstMinSeconds: 15, BurstMaxSeconds: 30, BurstDurationSeconds: 1800,
		Locale: "ko-KR", TimeZone: "Asia/Seoul", EgressPolicyID: "scan_default",
	}
	created, err := store.CreateAdminObservationPolicy(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	input.HorizonDays = 7
	updated, err := store.UpdateAdminObservationPolicy(ctx, created.ID, created.Revision, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.HorizonDays != 7 || updated.Revision != created.Revision+1 {
		t.Fatalf("updated policy = %+v", updated)
	}
}
