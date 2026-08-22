package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	"github.com/cineko-org/central/internal/support/numeric"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (store *Store) AuthorizeCatalogWrite(ctx context.Context, userID, installationID, capability string) error {
	var authorized bool
	if err := store.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM probe_runtimes
			WHERE owner_user_id = $1 AND installation_id = $2 AND kind = 'client'
				AND status = 'online' AND NOT draining AND health = 'healthy'
				AND COALESCE(last_heartbeat_at, updated_at) >= now() - ($4::bigint * interval '1 second')
				AND token_expires_at > now() AND $3 = ANY(available_capabilities)
		)
	`, userID, installationID, capability, int64(central.DefaultProbeHeartbeatTTL/time.Second)).Scan(&authorized); err != nil {
		return fmt.Errorf("authorize Client catalog write: %w", err)
	}
	if !authorized {
		return central.ErrUnauthorized
	}
	return nil
}

func (store *Store) Catalog(ctx context.Context) (*catalogpb.CatalogIndex, error) {
	var generation int64
	if err := store.pool.QueryRow(ctx, `SELECT generation FROM catalog_state WHERE id = 1`).Scan(&generation); err != nil {
		return nil, fmt.Errorf("read catalog generation: %w", err)
	}
	providers, err := store.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	theaters, err := store.listTheaters(ctx)
	if err != nil {
		return nil, err
	}
	movies, err := store.listMovies(ctx)
	if err != nil {
		return nil, err
	}
	auditoriums, err := store.listAuditoriums(ctx)
	if err != nil {
		return nil, err
	}
	showtimes, err := store.listShowtimes(ctx)
	if err != nil {
		return nil, err
	}
	result := catalogpb.CatalogIndex_builder{
		Providers: providers, Theaters: theaters, Movies: movies,
		Auditoriums: auditoriums, Showtimes: showtimes,
	}.Build()
	result.SetGeneration(generation)
	return result, nil
}

func (store *Store) CatalogRefreshStatus(ctx context.Context, now, heartbeatCutoff time.Time) (*adminpb.CatalogRefreshStatus, error) {
	var catalogEmpty, active bool
	var requestedAt, lastAttemptedAt *time.Time
	var eligibleProbes int32
	var lastStatus string
	err := store.pool.QueryRow(ctx, `
		SELECT NOT EXISTS (SELECT 1 FROM theaters WHERE active), state.refresh_requested_at,
			EXISTS (SELECT 1 FROM observation_assignments WHERE task_kind = $1 AND status IN ('queued', 'leased', 'retry_pending')),
			(SELECT count(*) FROM probe_runtimes WHERE status = 'online' AND NOT draining AND health = 'healthy'
				AND COALESCE(last_heartbeat_at, updated_at) >= $2 AND token_expires_at > $3 AND $1 = ANY(available_capabilities)),
			COALESCE(last_assignment.status, ''), last_assignment.updated_at
		FROM catalog_state AS state
		LEFT JOIN LATERAL (
			SELECT status, updated_at FROM observation_assignments
			WHERE task_kind = $1 ORDER BY updated_at DESC LIMIT 1
		) AS last_assignment ON true
		WHERE state.id = 1
	`, probedomain.CapabilityCGVCatalogCapture, heartbeatCutoff, now).Scan(
		&catalogEmpty, &requestedAt, &active, &eligibleProbes, &lastStatus, &lastAttemptedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("read catalog refresh status: %w", err)
	}
	status := &adminpb.CatalogRefreshStatus{}
	status.SetCatalogEmpty(catalogEmpty)
	status.SetActive(active)
	status.SetEligibleProbes(eligibleProbes)
	status.SetLastStatus(lastStatus)
	status.SetQueued(&adminpb.CatalogRefreshQueued{})
	if requestedAt != nil {
		status.SetRequestedAt(timestamppb.New(*requestedAt))
	}
	if lastAttemptedAt != nil {
		status.SetLastAttemptedAt(timestamppb.New(*lastAttemptedAt))
	}
	return status, nil
}

func (store *Store) RequestCatalogRefresh(ctx context.Context, requestedAt time.Time) error {
	tag, err := store.pool.Exec(ctx, `UPDATE catalog_state SET refresh_requested_at = COALESCE(refresh_requested_at, $1) WHERE id = 1`, requestedAt)
	if err != nil {
		return fmt.Errorf("request catalog refresh: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrNotFound
	}
	return nil
}

func (store *Store) UpsertCatalogSnapshot(ctx context.Context, snapshot *catalogpb.CatalogSnapshot) (int64, error) {
	return store.catalogTransaction(ctx, "catalog snapshot", func(tx pgx.Tx) (int64, error) {
		return upsertCatalogSnapshotTx(ctx, tx, snapshot)
	})
}

func (store *Store) PutSeatMap(ctx context.Context, snapshot *seatmappb.Snapshot) (int64, error) {
	return store.catalogTransaction(ctx, "seat map", func(tx pgx.Tx) (int64, error) {
		return putSeatMapTx(ctx, tx, snapshot)
	})
}

func (store *Store) catalogTransaction(ctx context.Context, label string, mutate func(pgx.Tx) (int64, error)) (int64, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, fmt.Errorf("begin %s: %w", label, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	generation, err := mutate(tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit %s: %w", label, err)
	}
	return generation, nil
}

//nolint:gocyclo,cyclop // The transaction deliberately keeps every catalog Proto collection in one atomic update.
func upsertCatalogSnapshotTx(ctx context.Context, tx pgx.Tx, snapshot *catalogpb.CatalogSnapshot) (int64, error) {
	generation, err := lockCatalogGeneration(ctx, tx)
	if err != nil {
		return 0, err
	}
	observedAt := snapshot.GetObservedAt().AsTime()
	changed, err := upsertProvider(ctx, tx, snapshot.GetProvider(), observedAt)
	if err != nil {
		return 0, err
	}
	for _, theater := range snapshot.GetTheaters() {
		entityChanged, err := upsertTheater(ctx, tx, theater, observedAt)
		if err != nil {
			return 0, err
		}
		changed = changed || entityChanged
	}
	for index, movie := range snapshot.GetMovies() {
		entityChanged, err := upsertMovie(ctx, tx, movie, index+1, observedAt)
		if err != nil {
			return 0, err
		}
		changed = changed || entityChanged
	}
	for _, auditorium := range snapshot.GetAuditoriums() {
		entityChanged, err := upsertAuditorium(ctx, tx, auditorium, observedAt)
		if err != nil {
			return 0, err
		}
		changed = changed || entityChanged
	}
	for _, showtime := range snapshot.GetShowtimes() {
		entityChanged, err := upsertShowtime(ctx, tx, showtime, observedAt)
		if err != nil {
			return 0, err
		}
		changed = changed || entityChanged
	}
	if !changed {
		return generation, nil
	}
	return incrementCatalogGeneration(ctx, tx, observedAt)
}

func putSeatMapTx(ctx context.Context, tx pgx.Tx, snapshot *seatmappb.Snapshot) (int64, error) {
	generation, err := lockCatalogGeneration(ctx, tx)
	if err != nil {
		return 0, err
	}
	var currentID string
	var currentObservedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(auditorium.current_seat_map_version_id, ''), version.observed_at
		FROM auditoriums AS auditorium
		LEFT JOIN seat_map_versions AS version ON version.id = auditorium.current_seat_map_version_id
		WHERE auditorium.id = $1 FOR UPDATE OF auditorium
	`, snapshot.GetAuditoriumId()).Scan(&currentID, &currentObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, central.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock seat map auditorium: %w", err)
	}
	observedAt := snapshot.GetObservedAt().AsTime()
	var storedID string
	err = tx.QueryRow(ctx, `
		SELECT id FROM seat_map_versions WHERE auditorium_id = $1 AND layout_hash = $2
	`, snapshot.GetAuditoriumId(), snapshot.GetLayoutHash()).Scan(&storedID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO seat_map_versions (
				id, auditorium_id, layout_hash, capacity, observed_at, first_seen_at, last_seen_at
			) VALUES ($1, $2, $3, $4, $5, $5, $5)
		`, snapshot.GetId(), snapshot.GetAuditoriumId(), snapshot.GetLayoutHash(), snapshot.GetCapacity(), observedAt); err != nil {
			return 0, fmt.Errorf("insert seat-map version: %w", err)
		}
		if err := storeSeatMapLayout(ctx, tx, snapshot); err != nil {
			return 0, err
		}
	case err != nil:
		return 0, fmt.Errorf("read seat-map version: %w", err)
	case storedID != snapshot.GetId():
		return 0, errors.New("stored seat-map version identity is inconsistent")
	default:
		if _, err := tx.Exec(ctx, `
			UPDATE seat_map_versions SET last_seen_at = GREATEST(last_seen_at, $2) WHERE id = $1
		`, storedID, observedAt); err != nil {
			return 0, fmt.Errorf("refresh seat-map version: %w", err)
		}
	}
	if currentID == snapshot.GetId() {
		_, err := tx.Exec(ctx, `UPDATE auditoriums SET seat_map_requested_at = NULL WHERE id = $1`, snapshot.GetAuditoriumId())
		return generation, err
	}
	if currentObservedAt != nil && !observedAt.After(*currentObservedAt) {
		return generation, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auditoriums SET current_seat_map_version_id = $2, capacity = $3,
			seat_map_requested_at = NULL, updated_at = $4 WHERE id = $1
	`, snapshot.GetAuditoriumId(), snapshot.GetId(), snapshot.GetCapacity(), observedAt); err != nil {
		return 0, fmt.Errorf("activate seat map: %w", err)
	}
	return incrementCatalogGeneration(ctx, tx, observedAt)
}

func (store *Store) SeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Snapshot, error) {
	var id, storedAuditoriumID, layoutHash string
	var capacity int32
	var observedAt time.Time
	err := store.pool.QueryRow(ctx, `
		SELECT version.id, version.auditorium_id, version.layout_hash, version.capacity, version.observed_at
		FROM auditoriums AS auditorium
		JOIN seat_map_versions AS version ON version.id = auditorium.current_seat_map_version_id
		WHERE auditorium.id = $1
	`, auditoriumID).Scan(&id, &storedAuditoriumID, &layoutHash, &capacity, &observedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read seat map: %w", err)
	}
	layout, err := readSeatMapLayout(ctx, store.pool, id, storedAuditoriumID)
	if err != nil {
		return nil, err
	}
	result := seatmappb.Snapshot_builder{Layout: layout, ObservedAt: timestamppb.New(observedAt)}.Build()
	result.SetId(id)
	result.SetAuditoriumId(storedAuditoriumID)
	result.SetLayoutHash(layoutHash)
	result.SetCapacity(capacity)
	return result, nil
}

func (store *Store) RequestSeatMapBackfill(ctx context.Context, auditoriumID string, requestedAt time.Time) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE auditoriums SET seat_map_requested_at = COALESCE(seat_map_requested_at, $2)
		WHERE id = $1 AND active
	`, auditoriumID, requestedAt)
	if err != nil {
		return fmt.Errorf("request seat-map backfill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return central.ErrNotFound
	}
	return nil
}

func lockCatalogGeneration(ctx context.Context, tx pgx.Tx) (int64, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `SELECT generation FROM catalog_state WHERE id = 1 FOR UPDATE`).Scan(&generation); err != nil {
		return 0, fmt.Errorf("lock catalog generation: %w", err)
	}
	return generation, nil
}

func incrementCatalogGeneration(ctx context.Context, tx pgx.Tx, observedAt time.Time) (int64, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE catalog_state SET generation = generation + 1, updated_at = $1 WHERE id = 1 RETURNING generation
	`, observedAt).Scan(&generation); err != nil {
		return 0, fmt.Errorf("increment catalog generation: %w", err)
	}
	return generation, nil
}

func upsertProvider(ctx context.Context, tx pgx.Tx, provider *catalogpb.Provider, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(ctx, tx, "provider", provider.GetId(), provider, observedAt,
		`SELECT content_hash, updated_at FROM providers WHERE id = $1 FOR UPDATE`, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $4, $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(providers.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN providers.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.updated_at ELSE providers.updated_at END
		WHERE EXCLUDED.updated_at >= providers.updated_at`, provider.GetId(), provider.GetName())
}

func upsertTheater(ctx context.Context, tx pgx.Tx, theater *catalogpb.Theater, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(ctx, tx, "theater", theater.GetId(), theater, observedAt,
		`SELECT content_hash, updated_at FROM theaters WHERE id = $1 FOR UPDATE`, `
		INSERT INTO theaters (id, provider_id, source_key, region, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id, source_key = EXCLUDED.source_key,
			region = EXCLUDED.region, name = EXCLUDED.name, active = true, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(theaters.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN theaters.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.updated_at ELSE theaters.updated_at END
		WHERE EXCLUDED.updated_at >= theaters.updated_at`, theater.GetId(), theater.GetProviderId(), theater.GetSourceKey(), theater.GetRegion(), theater.GetName())
}

func upsertMovie(ctx context.Context, tx pgx.Tx, movie *catalogpb.Movie, displayOrder int, observedAt time.Time) (bool, error) {
	hashInput := proto.Clone(movie)
	return mutateCatalogEntity(ctx, tx, "movie", movie.GetId(), hashInput, observedAt,
		`SELECT content_hash, updated_at FROM movies WHERE id = $1 FOR UPDATE`, `
		INSERT INTO movies (id, provider_id, source_key, title, poster_url, display_order, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id, source_key = EXCLUDED.source_key,
			title = EXCLUDED.title, poster_url = EXCLUDED.poster_url, display_order = EXCLUDED.display_order,
			active = true, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(movies.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN movies.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.updated_at ELSE movies.updated_at END
		WHERE EXCLUDED.updated_at >= movies.updated_at`, movie.GetId(), movie.GetProviderId(), movie.GetSourceKey(), movie.GetTitle(), movie.GetPosterUrl(), displayOrder)
}

func upsertAuditorium(ctx context.Context, tx pgx.Tx, auditorium *catalogpb.Auditorium, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(ctx, tx, "auditorium", auditorium.GetId(), auditorium, observedAt,
		`SELECT content_hash, updated_at FROM auditoriums WHERE id = $1 FOR UPDATE`, `
		INSERT INTO auditoriums (id, theater_id, source_key, name, screen_types, capacity, content_hash, first_seen_at, last_seen_at, updated_at, seat_map_requested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8, $8)
		ON CONFLICT (id) DO UPDATE SET theater_id = EXCLUDED.theater_id, source_key = EXCLUDED.source_key,
			name = EXCLUDED.name, screen_types = EXCLUDED.screen_types, capacity = EXCLUDED.capacity, active = true,
			seat_map_requested_at = CASE WHEN auditoriums.current_seat_map_version_id IS NULL
				OR auditoriums.capacity IS DISTINCT FROM EXCLUDED.capacity
				OR auditoriums.screen_types IS DISTINCT FROM EXCLUDED.screen_types
				THEN COALESCE(auditoriums.seat_map_requested_at, EXCLUDED.seat_map_requested_at)
				ELSE auditoriums.seat_map_requested_at END,
			content_hash = EXCLUDED.content_hash, last_seen_at = GREATEST(auditoriums.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN auditoriums.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.updated_at ELSE auditoriums.updated_at END
		WHERE EXCLUDED.updated_at >= auditoriums.updated_at`, auditorium.GetId(), auditorium.GetTheaterId(), auditorium.GetSourceKey(), auditorium.GetName(), auditorium.GetScreenTypes(), auditorium.GetCapacity())
}

func upsertShowtime(ctx context.Context, tx pgx.Tx, showtime *catalogpb.Showtime, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(ctx, tx, "showtime", showtime.GetId(), showtime, observedAt,
		`SELECT content_hash, updated_at FROM showtimes WHERE id = $1 FOR UPDATE`, `
		INSERT INTO showtimes (id, provider_id, source_key, theater_id, movie_id, auditorium_id, schedule_date, starts_at, ends_at, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, $11)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id, source_key = EXCLUDED.source_key,
			theater_id = EXCLUDED.theater_id, movie_id = EXCLUDED.movie_id, auditorium_id = EXCLUDED.auditorium_id,
			schedule_date = EXCLUDED.schedule_date, starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, active = true, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(showtimes.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN showtimes.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.updated_at ELSE showtimes.updated_at END
		WHERE EXCLUDED.updated_at >= showtimes.updated_at`, showtime.GetId(), showtime.GetProviderId(), showtime.GetSourceKey(), showtime.GetTheaterId(), showtime.GetMovie().GetId(), showtime.GetAuditorium().GetId(), localDateString(showtime.GetScheduleDate()), showtime.GetStartsAt().AsTime(), showtime.GetEndsAt().AsTime())
}

func mutateCatalogEntity(ctx context.Context, tx pgx.Tx, entity, id string, value proto.Message, observedAt time.Time, stateQuery, mutationQuery string, arguments ...any) (bool, error) {
	hash, err := catalogContentHash(value)
	if err != nil {
		return false, fmt.Errorf("hash catalog %s: %w", entity, err)
	}
	changed, err := catalogEntityChanged(ctx, tx, stateQuery, id, hash, observedAt)
	if err != nil {
		return false, err
	}
	arguments = append(arguments, hash, observedAt)
	_, err = tx.Exec(ctx, mutationQuery, arguments...)
	if err != nil {
		return false, fmt.Errorf("upsert catalog %s: %w", entity, err)
	}
	return changed, nil
}

func catalogContentHash(value proto.Message) (string, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func catalogEntityChanged(ctx context.Context, tx pgx.Tx, query, id, want string, observedAt time.Time) (bool, error) {
	var current string
	var updatedAt time.Time
	err := tx.QueryRow(ctx, query, id).Scan(&current, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read catalog entity hash: %w", err)
	}
	return !observedAt.Before(updatedAt) && current != want, nil
}

func (store *Store) listProviders(ctx context.Context) ([]*catalogpb.Provider, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name FROM providers ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list catalog providers: %w", err)
	}
	defer rows.Close()
	result := make([]*catalogpb.Provider, 0)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		value := &catalogpb.Provider{}
		value.SetId(id)
		value.SetName(name)
		result = append(result, value)
	}
	return result, rows.Err()
}

//nolint:dupl // Separate generated Proto row mappings make schema-to-contract fields directly auditable.
func (store *Store) listTheaters(ctx context.Context) ([]*catalogpb.Theater, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, provider_id, source_key, region, name FROM theaters WHERE active ORDER BY region, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list catalog theaters: %w", err)
	}
	defer rows.Close()
	result := make([]*catalogpb.Theater, 0)
	for rows.Next() {
		var id, providerID, sourceKey, region, name string
		if err := rows.Scan(&id, &providerID, &sourceKey, &region, &name); err != nil {
			return nil, err
		}
		value := &catalogpb.Theater{}
		value.SetId(id)
		value.SetProviderId(providerID)
		value.SetSourceKey(sourceKey)
		value.SetRegion(region)
		value.SetName(name)
		result = append(result, value)
	}
	return result, rows.Err()
}

//nolint:dupl // Separate generated Proto row mappings make schema-to-contract fields directly auditable.
func (store *Store) listMovies(ctx context.Context) ([]*catalogpb.Movie, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url
		FROM movies AS movie WHERE movie.active AND EXISTS (
			SELECT 1 FROM showtimes AS showtime WHERE showtime.movie_id = movie.id AND showtime.active AND showtime.starts_at > now()
		) ORDER BY movie.display_order, movie.title, movie.id`)
	if err != nil {
		return nil, fmt.Errorf("list catalog movies: %w", err)
	}
	defer rows.Close()
	result := make([]*catalogpb.Movie, 0)
	for rows.Next() {
		var id, providerID, sourceKey, title, posterURL string
		if err := rows.Scan(&id, &providerID, &sourceKey, &title, &posterURL); err != nil {
			return nil, err
		}
		value := &catalogpb.Movie{}
		value.SetId(id)
		value.SetProviderId(providerID)
		value.SetSourceKey(sourceKey)
		value.SetTitle(title)
		value.SetPosterUrl(posterURL)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) listAuditoriums(ctx context.Context) ([]*catalogpb.Auditorium, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, theater_id, source_key, name, screen_types, capacity, COALESCE(current_seat_map_version_id, '') FROM auditoriums WHERE active ORDER BY theater_id, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list catalog auditoriums: %w", err)
	}
	defer rows.Close()
	result := make([]*catalogpb.Auditorium, 0)
	for rows.Next() {
		var id, theaterID, sourceKey, name, layoutHash string
		var screenTypes []string
		var capacity int32
		if err := rows.Scan(&id, &theaterID, &sourceKey, &name, &screenTypes, &capacity, &layoutHash); err != nil {
			return nil, err
		}
		value := catalogpb.Auditorium_builder{ScreenTypes: screenTypes}.Build()
		value.SetId(id)
		value.SetTheaterId(theaterID)
		value.SetSourceKey(sourceKey)
		value.SetName(name)
		value.SetCapacity(capacity)
		value.SetCurrentLayoutHash(layoutHash)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) listShowtimes(ctx context.Context) ([]*catalogpb.Showtime, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT showtime.id, showtime.provider_id, showtime.source_key, showtime.theater_id,
			movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url,
			auditorium.id, auditorium.theater_id, auditorium.source_key, auditorium.name,
			auditorium.screen_types, auditorium.capacity, COALESCE(auditorium.current_seat_map_version_id, ''),
			showtime.schedule_date::text, showtime.starts_at, showtime.ends_at
		FROM showtimes AS showtime JOIN movies AS movie ON movie.id = showtime.movie_id
		JOIN auditoriums AS auditorium ON auditorium.id = showtime.auditorium_id
		WHERE showtime.active AND showtime.starts_at > now() ORDER BY showtime.starts_at, showtime.id`)
	if err != nil {
		return nil, fmt.Errorf("list catalog showtimes: %w", err)
	}
	defer rows.Close()
	result := make([]*catalogpb.Showtime, 0)
	for rows.Next() {
		var id, providerID, sourceKey, theaterID string
		var movieID, movieProviderID, movieSourceKey, movieTitle, moviePosterURL string
		var auditoriumID, auditoriumTheaterID, auditoriumSourceKey, auditoriumName, layoutHash string
		var screenTypes []string
		var capacity int32
		var scheduleDate string
		var startsAt, endsAt time.Time
		if err := rows.Scan(&id, &providerID, &sourceKey, &theaterID, &movieID, &movieProviderID, &movieSourceKey, &movieTitle, &moviePosterURL, &auditoriumID, &auditoriumTheaterID, &auditoriumSourceKey, &auditoriumName, &screenTypes, &capacity, &layoutHash, &scheduleDate, &startsAt, &endsAt); err != nil {
			return nil, err
		}
		parsedScheduleDate, err := time.Parse(time.DateOnly, scheduleDate)
		if err != nil {
			return nil, fmt.Errorf("parse catalog showtime schedule date: %w", err)
		}
		movie := &catalogpb.Movie{}
		movie.SetId(movieID)
		movie.SetProviderId(movieProviderID)
		movie.SetSourceKey(movieSourceKey)
		movie.SetTitle(movieTitle)
		movie.SetPosterUrl(moviePosterURL)
		auditorium := catalogpb.Auditorium_builder{ScreenTypes: screenTypes}.Build()
		auditorium.SetId(auditoriumID)
		auditorium.SetTheaterId(auditoriumTheaterID)
		auditorium.SetSourceKey(auditoriumSourceKey)
		auditorium.SetName(auditoriumName)
		auditorium.SetCapacity(capacity)
		auditorium.SetCurrentLayoutHash(layoutHash)
		value := catalogpb.Showtime_builder{Movie: movie, Auditorium: auditorium, StartsAt: timestamppb.New(startsAt), EndsAt: timestamppb.New(endsAt)}.Build()
		value.SetId(id)
		value.SetProviderId(providerID)
		value.SetSourceKey(sourceKey)
		value.SetTheaterId(theaterID)
		value.SetScheduleDate(catalogLocalDate(parsedScheduleDate))
		result = append(result, value)
	}
	return result, rows.Err()
}

func catalogLocalDate(value time.Time) *commonpb.LocalDate {
	date := &commonpb.LocalDate{}
	date.SetYear(numeric.ClampInt32(value.Year()))
	date.SetMonth(numeric.ClampInt32(int(value.Month())))
	date.SetDay(numeric.ClampInt32(value.Day()))
	return date
}
