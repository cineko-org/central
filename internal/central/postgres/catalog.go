package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"

	"github.com/jackc/pgx/v5"
)

func (store *Store) AuthorizeCatalogWrite(
	ctx context.Context,
	userID string,
	installationID string,
	capability string,
) error {
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

func (store *Store) Catalog(ctx context.Context) (contracts.CatalogIndex, error) {
	var result contracts.CatalogIndex
	if err := store.pool.QueryRow(ctx, `SELECT generation FROM catalog_state WHERE id = 1`).Scan(&result.Generation); err != nil {
		return contracts.CatalogIndex{}, fmt.Errorf("read catalog generation: %w", err)
	}
	var err error
	if result.Providers, err = store.listProviders(ctx); err != nil {
		return contracts.CatalogIndex{}, err
	}
	if result.Theaters, err = store.listTheaters(ctx); err != nil {
		return contracts.CatalogIndex{}, err
	}
	if result.Movies, err = store.listMovies(ctx); err != nil {
		return contracts.CatalogIndex{}, err
	}
	if result.Auditoriums, err = store.listAuditoriums(ctx); err != nil {
		return contracts.CatalogIndex{}, err
	}
	if result.Showtimes, err = store.listShowtimes(ctx); err != nil {
		return contracts.CatalogIndex{}, err
	}
	return result, nil
}

func (store *Store) CatalogRefreshStatus(
	ctx context.Context,
	now time.Time,
	heartbeatCutoff time.Time,
) (central.CatalogRefreshStatus, error) {
	var status central.CatalogRefreshStatus
	err := store.pool.QueryRow(ctx, `
		SELECT
			NOT EXISTS (SELECT 1 FROM theaters WHERE active),
			state.refresh_requested_at,
			EXISTS (
				SELECT 1 FROM observation_assignments
				WHERE task_kind = $1 AND status IN ('queued', 'leased', 'retry_pending')
			),
			(
				SELECT count(*) FROM probe_runtimes
				WHERE status = 'online' AND NOT draining AND health = 'healthy'
					AND COALESCE(last_heartbeat_at, updated_at) >= $2
					AND token_expires_at > $3 AND $1 = ANY(available_capabilities)
			),
			COALESCE(last_assignment.status, ''),
			last_assignment.updated_at
		FROM catalog_state AS state
		LEFT JOIN LATERAL (
			SELECT status, updated_at FROM observation_assignments
			WHERE task_kind = $1 ORDER BY updated_at DESC LIMIT 1
		) AS last_assignment ON true
		WHERE state.id = 1
	`, contracts.CapabilityCGVCatalogCapture, heartbeatCutoff, now).Scan(
		&status.CatalogEmpty, &status.RequestedAt, &status.Active, &status.EligibleProbes,
		&status.LastStatus, &status.LastAttemptedAt,
	)
	if err != nil {
		return central.CatalogRefreshStatus{}, fmt.Errorf("read catalog refresh status: %w", err)
	}
	return status, nil
}

func (store *Store) RequestCatalogRefresh(ctx context.Context, requestedAt time.Time) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE catalog_state
		SET refresh_requested_at = COALESCE(refresh_requested_at, $1)
		WHERE id = 1
	`, requestedAt)
	if err != nil {
		return fmt.Errorf("request catalog refresh: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrNotFound
	}
	return nil
}

func (store *Store) UpsertCatalogSnapshot(
	ctx context.Context,
	snapshot contracts.CatalogSnapshot,
) (int64, error) {
	return store.catalogTransaction(ctx, "catalog snapshot", func(tx pgx.Tx) (int64, error) {
		return upsertCatalogSnapshotTx(ctx, tx, snapshot)
	})
}

func (store *Store) catalogTransaction(
	ctx context.Context,
	label string,
	mutate func(pgx.Tx) (int64, error),
) (int64, error) {
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

func upsertCatalogSnapshotTx(
	ctx context.Context,
	tx pgx.Tx,
	snapshot contracts.CatalogSnapshot,
) (int64, error) {
	generation, err := lockCatalogGeneration(ctx, tx)
	if err != nil {
		return 0, err
	}
	changed, err := upsertProvider(ctx, tx, snapshot.Provider, snapshot.ObservedAt)
	if err != nil {
		return 0, err
	}
	groups := []catalogMutationResult{
		upsertCatalogEntities(ctx, tx, snapshot.Theaters, snapshot.ObservedAt, upsertTheater),
		upsertCatalogEntities(ctx, tx, snapshot.Movies, snapshot.ObservedAt, upsertMovie),
		upsertCatalogEntities(ctx, tx, snapshot.Auditoriums, snapshot.ObservedAt, upsertAuditorium),
		upsertCatalogEntities(ctx, tx, snapshot.Showtimes, snapshot.ObservedAt, upsertShowtime),
	}
	for _, group := range groups {
		if group.err != nil {
			return 0, group.err
		}
		changed = changed || group.changed
	}
	if !changed {
		return generation, nil
	}
	return incrementCatalogGeneration(ctx, tx, snapshot.ObservedAt)
}

func completeCatalogRefresh(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `UPDATE catalog_state SET refresh_requested_at = NULL WHERE id = 1`); err != nil {
		return fmt.Errorf("complete catalog refresh: %w", err)
	}
	return nil
}

type catalogMutationResult struct {
	changed bool
	err     error
}

func upsertCatalogEntities[T any](
	ctx context.Context,
	tx pgx.Tx,
	values []T,
	observedAt time.Time,
	upsert func(context.Context, pgx.Tx, T, time.Time) (bool, error),
) catalogMutationResult {
	changed := false
	for _, value := range values {
		entityChanged, err := upsert(ctx, tx, value, observedAt)
		if err != nil {
			return catalogMutationResult{err: err}
		}
		changed = changed || entityChanged
	}
	return catalogMutationResult{changed: changed}
}

func (store *Store) PutSeatMapVersion(
	ctx context.Context,
	version contracts.SeatMapVersion,
) (int64, error) {
	return store.catalogTransaction(ctx, "seat map version", func(tx pgx.Tx) (int64, error) {
		return putSeatMapVersionTx(ctx, tx, version)
	})
}

func putSeatMapVersionTx(
	ctx context.Context,
	tx pgx.Tx,
	version contracts.SeatMapVersion,
) (int64, error) {
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
		WHERE auditorium.id = $1
		FOR UPDATE OF auditorium
	`, version.AuditoriumID).Scan(&currentID, &currentObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, central.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock seat map auditorium: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO seat_map_versions (
			id, auditorium_id, layout_hash, capacity, layout,
			observed_at, first_seen_at, last_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $6, $6)
		ON CONFLICT (auditorium_id, layout_hash) DO UPDATE SET
			last_seen_at = GREATEST(seat_map_versions.last_seen_at, EXCLUDED.last_seen_at)
	`, version.ID, version.AuditoriumID, version.LayoutHash, version.Capacity,
		version.Layout, version.ObservedAt); err != nil {
		return 0, fmt.Errorf("upsert seat map version: %w", err)
	}
	if currentID == version.ID {
		if _, err := tx.Exec(ctx, `
			UPDATE auditoriums SET seat_map_requested_at = NULL WHERE id = $1
		`, version.AuditoriumID); err != nil {
			return 0, fmt.Errorf("complete unchanged seat map request: %w", err)
		}
		return generation, nil
	}
	if currentObservedAt != nil && !version.ObservedAt.After(*currentObservedAt) {
		return generation, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auditoriums
		SET current_seat_map_version_id = $2, capacity = $3, seat_map_requested_at = NULL, updated_at = $4
		WHERE id = $1
	`, version.AuditoriumID, version.ID, version.Capacity, version.ObservedAt); err != nil {
		return 0, fmt.Errorf("activate seat map version: %w", err)
	}
	generation, err = incrementCatalogGeneration(ctx, tx, version.ObservedAt)
	if err != nil {
		return 0, err
	}
	return generation, nil
}

func (store *Store) SeatMapVersion(
	ctx context.Context,
	auditoriumID string,
) (contracts.SeatMapVersion, error) {
	var version contracts.SeatMapVersion
	err := store.pool.QueryRow(ctx, `
		SELECT version.id, version.auditorium_id, version.layout_hash, version.capacity,
			version.layout, version.observed_at
		FROM auditoriums AS auditorium
		JOIN seat_map_versions AS version ON version.id = auditorium.current_seat_map_version_id
		WHERE auditorium.id = $1
	`, auditoriumID).Scan(
		&version.ID, &version.AuditoriumID, &version.LayoutHash, &version.Capacity,
		&version.Layout, &version.ObservedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.SeatMapVersion{}, central.ErrNotFound
	}
	if err != nil {
		return contracts.SeatMapVersion{}, fmt.Errorf("read seat map version: %w", err)
	}
	return version, nil
}

func (store *Store) RequestSeatMapBackfill(
	ctx context.Context,
	auditoriumID string,
	requestedAt time.Time,
) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE auditoriums
		SET seat_map_requested_at = COALESCE(seat_map_requested_at, $2)
		WHERE id = $1 AND active
	`, auditoriumID, requestedAt)
	if err != nil {
		return fmt.Errorf("request seat-map backfill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auditoriums WHERE id = $1 AND active)`, auditoriumID).Scan(&exists); err != nil {
			return fmt.Errorf("inspect seat-map backfill target: %w", err)
		}
		if !exists {
			return central.ErrNotFound
		}
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
		UPDATE catalog_state SET generation = generation + 1, updated_at = $1
		WHERE id = 1 RETURNING generation
	`, observedAt).Scan(&generation); err != nil {
		return 0, fmt.Errorf("increment catalog generation: %w", err)
	}
	return generation, nil
}

func upsertProvider(ctx context.Context, tx pgx.Tx, provider contracts.Provider, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(
		ctx, tx, "provider", provider.ID, provider, observedAt,
		`SELECT content_hash, updated_at FROM providers WHERE id = $1 FOR UPDATE`, `
		INSERT INTO providers (id, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $4, $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(providers.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN providers.content_hash <> EXCLUDED.content_hash
				THEN EXCLUDED.updated_at ELSE providers.updated_at END
		WHERE EXCLUDED.updated_at >= providers.updated_at
	`, provider.ID, provider.Name)
}

func upsertTheater(ctx context.Context, tx pgx.Tx, theater contracts.Theater, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(
		ctx, tx, "theater", theater.ID, theater, observedAt,
		`SELECT content_hash, updated_at FROM theaters WHERE id = $1 FOR UPDATE`, `
		INSERT INTO theaters (id, provider_id, source_key, region, name, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id,
			source_key = EXCLUDED.source_key, region = EXCLUDED.region, name = EXCLUDED.name, active = true,
			content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(theaters.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN theaters.content_hash <> EXCLUDED.content_hash
				THEN EXCLUDED.updated_at ELSE theaters.updated_at END
		WHERE EXCLUDED.updated_at >= theaters.updated_at
	`, theater.ID, theater.ProviderID, theater.SourceKey, theater.Region, theater.Name)
}

func upsertMovie(ctx context.Context, tx pgx.Tx, movie contracts.Movie, observedAt time.Time) (bool, error) {
	return mutateCatalogEntity(
		ctx, tx, "movie", movie.ID, movie, observedAt,
		`SELECT content_hash, updated_at FROM movies WHERE id = $1 FOR UPDATE`, `
		INSERT INTO movies (id, provider_id, source_key, title, poster_url, content_hash, first_seen_at, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id,
			source_key = EXCLUDED.source_key, title = EXCLUDED.title, poster_url = EXCLUDED.poster_url, active = true,
			content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(movies.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN movies.content_hash <> EXCLUDED.content_hash
				THEN EXCLUDED.updated_at ELSE movies.updated_at END
		WHERE EXCLUDED.updated_at >= movies.updated_at
	`, movie.ID, movie.ProviderID, movie.SourceKey, movie.Title, movie.PosterURL)
}

func upsertAuditorium(
	ctx context.Context,
	tx pgx.Tx,
	auditorium contracts.Auditorium,
	observedAt time.Time,
) (bool, error) {
	return mutateCatalogEntity(
		ctx, tx, "auditorium", auditorium.ID, auditorium, observedAt,
		`SELECT content_hash, updated_at FROM auditoriums WHERE id = $1 FOR UPDATE`, `
		INSERT INTO auditoriums (
			id, theater_id, source_key, name, screen_types, capacity, content_hash,
			first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8)
		ON CONFLICT (id) DO UPDATE SET theater_id = EXCLUDED.theater_id,
			source_key = EXCLUDED.source_key, name = EXCLUDED.name, screen_types = EXCLUDED.screen_types,
			capacity = EXCLUDED.capacity, active = true,
			content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(auditoriums.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN auditoriums.content_hash <> EXCLUDED.content_hash
				THEN EXCLUDED.updated_at ELSE auditoriums.updated_at END
		WHERE EXCLUDED.updated_at >= auditoriums.updated_at
	`, auditorium.ID, auditorium.TheaterID, auditorium.SourceKey, auditorium.Name,
		auditorium.ScreenTypes, auditorium.Capacity)
}

func upsertShowtime(ctx context.Context, tx pgx.Tx, showtime contracts.Showtime, observedAt time.Time) (bool, error) {
	identity := struct {
		ID, ProviderID, SourceKey, TheaterID, MovieID, AuditoriumID string
		StartsAt, EndsAt                                            time.Time
	}{
		showtime.ID, showtime.ProviderID, showtime.SourceKey, showtime.TheaterID,
		showtime.Movie.ID, showtime.Auditorium.ID, showtime.StartsAt.UTC(), showtime.EndsAt.UTC(),
	}
	return mutateCatalogEntity(
		ctx, tx, "showtime", showtime.ID, identity, observedAt,
		`SELECT content_hash, updated_at FROM showtimes WHERE id = $1 FOR UPDATE`, `
		INSERT INTO showtimes (
			id, provider_id, source_key, theater_id, movie_id, auditorium_id,
			starts_at, ends_at, content_hash, first_seen_at, last_seen_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $10)
		ON CONFLICT (id) DO UPDATE SET provider_id = EXCLUDED.provider_id,
			source_key = EXCLUDED.source_key, theater_id = EXCLUDED.theater_id,
			movie_id = EXCLUDED.movie_id, auditorium_id = EXCLUDED.auditorium_id,
			starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at,
			active = true, content_hash = EXCLUDED.content_hash,
			last_seen_at = GREATEST(showtimes.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = CASE WHEN showtimes.content_hash <> EXCLUDED.content_hash
				THEN EXCLUDED.updated_at ELSE showtimes.updated_at END
		WHERE EXCLUDED.updated_at >= showtimes.updated_at
	`, showtime.ID, showtime.ProviderID, showtime.SourceKey, showtime.TheaterID,
		showtime.Movie.ID, showtime.Auditorium.ID, showtime.StartsAt, showtime.EndsAt)
}

func mutateCatalogEntity(
	ctx context.Context,
	tx pgx.Tx,
	entity string,
	id string,
	value any,
	observedAt time.Time,
	stateQuery string,
	mutationQuery string,
	arguments ...any,
) (bool, error) {
	hash := catalogContentHash(value)
	changed, err := catalogEntityChanged(ctx, tx, stateQuery, id, hash, observedAt)
	if err != nil {
		return false, err
	}
	arguments = append(arguments, hash, observedAt)
	_, err = tx.Exec(ctx, mutationQuery, arguments...)
	return changed, wrapCatalogMutation(entity, err)
}

func catalogEntityChanged(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	id string,
	want string,
	observedAt time.Time,
) (bool, error) {
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

func catalogContentHash(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func wrapCatalogMutation(entity string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("upsert catalog %s: %w", entity, err)
}

func (store *Store) listProviders(ctx context.Context) ([]contracts.Provider, error) {
	return queryCatalogRows(ctx, store.pool, "provider", `
		SELECT id, name FROM providers ORDER BY name, id
	`, func(rows pgx.Rows, value *contracts.Provider) error {
		return rows.Scan(&value.ID, &value.Name)
	})
}

func (store *Store) listTheaters(ctx context.Context) ([]contracts.Theater, error) {
	return queryCatalogRows(ctx, store.pool, "theater", `
		SELECT id, provider_id, source_key, region, name
		FROM theaters WHERE active ORDER BY region, name, id
	`, func(rows pgx.Rows, value *contracts.Theater) error {
		return rows.Scan(&value.ID, &value.ProviderID, &value.SourceKey, &value.Region, &value.Name)
	})
}

func (store *Store) listMovies(ctx context.Context) ([]contracts.Movie, error) {
	return queryCatalogRows(ctx, store.pool, "movie", `
		SELECT id, provider_id, source_key, title, poster_url
		FROM movies WHERE active ORDER BY title, id
	`, func(rows pgx.Rows, value *contracts.Movie) error {
		return rows.Scan(&value.ID, &value.ProviderID, &value.SourceKey, &value.Title, &value.PosterURL)
	})
}

func (store *Store) listAuditoriums(ctx context.Context) ([]contracts.Auditorium, error) {
	return queryCatalogRows(ctx, store.pool, "auditorium", `
		SELECT id, theater_id, source_key, name, screen_types, capacity,
			COALESCE(current_seat_map_version_id, '')
		FROM auditoriums WHERE active ORDER BY theater_id, name, id
	`, func(rows pgx.Rows, value *contracts.Auditorium) error {
		return rows.Scan(
			&value.ID, &value.TheaterID, &value.SourceKey, &value.Name, &value.ScreenTypes, &value.Capacity,
			&value.SeatMapVersion,
		)
	})
}

func (store *Store) listShowtimes(ctx context.Context) ([]contracts.Showtime, error) {
	return queryCatalogRows(ctx, store.pool, "showtime", `
		SELECT showtime.id, showtime.provider_id, showtime.source_key, showtime.theater_id,
			movie.id, movie.provider_id, movie.source_key, movie.title, movie.poster_url,
			auditorium.id, auditorium.theater_id, auditorium.source_key, auditorium.name,
			auditorium.screen_types, auditorium.capacity,
			COALESCE(auditorium.current_seat_map_version_id, ''), showtime.starts_at, showtime.ends_at
		FROM showtimes AS showtime
		JOIN movies AS movie ON movie.id = showtime.movie_id
		JOIN auditoriums AS auditorium ON auditorium.id = showtime.auditorium_id
		WHERE showtime.active AND showtime.ends_at >= now() - interval '6 hours'
		ORDER BY showtime.starts_at, showtime.id
	`, func(rows pgx.Rows, value *contracts.Showtime) error {
		return rows.Scan(
			&value.ID, &value.ProviderID, &value.SourceKey, &value.TheaterID,
			&value.Movie.ID, &value.Movie.ProviderID, &value.Movie.SourceKey,
			&value.Movie.Title, &value.Movie.PosterURL,
			&value.Auditorium.ID, &value.Auditorium.TheaterID, &value.Auditorium.SourceKey,
			&value.Auditorium.Name, &value.Auditorium.ScreenTypes, &value.Auditorium.Capacity,
			&value.Auditorium.SeatMapVersion,
			&value.StartsAt, &value.EndsAt,
		)
	})
}

type catalogRowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryCatalogRows[T any](
	ctx context.Context,
	querier catalogRowsQuerier,
	entity string,
	query string,
	scan func(pgx.Rows, *T) error,
) ([]T, error) {
	rows, err := querier.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list catalog %ss: %w", entity, err)
	}
	defer rows.Close()
	result := make([]T, 0)
	for rows.Next() {
		var value T
		if err := scan(rows, &value); err != nil {
			return nil, fmt.Errorf("scan catalog %s: %w", entity, err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog %ss: %w", entity, err)
	}
	return result, nil
}
