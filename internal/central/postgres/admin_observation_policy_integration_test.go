package postgres

import (
	"context"
	"strings"
	"testing"

	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
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

	input := &adminpb.ObservationPolicyInput{}
	input.SetTheaterId(theaterID)
	input.SetEnabled(true)
	input.SetHorizonDays(14)
	input.SetPriority(50)
	input.SetBaselineMinSeconds(300)
	input.SetBaselineMaxSeconds(900)
	input.SetDemandMinSeconds(30)
	input.SetDemandMaxSeconds(45)
	input.SetBurstMinSeconds(15)
	input.SetBurstMaxSeconds(30)
	input.SetBurstDurationSeconds(1800)
	input.SetLocale("ko-KR")
	input.SetTimeZone("Asia/Seoul")
	input.SetEgressPolicyId("scan_default")
	created, err := store.CreateAdminObservationPolicy(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	input.SetHorizonDays(7)
	updated, err := store.UpdateAdminObservationPolicy(ctx, created.GetId(), created.GetRevision(), input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GetInput().GetHorizonDays() != 7 || updated.GetRevision() != created.GetRevision()+1 {
		t.Fatalf("updated policy = %+v", updated)
	}
}
