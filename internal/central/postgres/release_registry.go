package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
)

func (store *Store) ListReleases(ctx context.Context) ([]central.ReleaseRecord, int64, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("begin release registry read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var generation int64
	if err := tx.QueryRow(ctx, `
		SELECT generation FROM desktop_release_registry_state WHERE singleton = true
	`).Scan(&generation); err != nil {
		return nil, 0, fmt.Errorf("read release generation: %w", err)
	}
	records, err := listReleaseRecords(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit release registry read: %w", err)
	}
	return records, generation, nil
}

func (store *Store) CurrentReleaseGeneration(ctx context.Context) (int64, error) {
	var generation int64
	if err := store.pool.QueryRow(ctx, `
		SELECT generation FROM desktop_release_registry_state WHERE singleton = true
	`).Scan(&generation); err != nil {
		return 0, fmt.Errorf("read desktop release generation: %w", err)
	}
	return generation, nil
}

func (store *Store) InsertReleaseSet(
	ctx context.Context,
	records []central.ReleaseRecord,
) (int64, bool, error) {
	if len(records) == 0 {
		return 0, false, central.ErrInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, false, fmt.Errorf("begin release publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	generation, storedRecords, beforeFingerprint, err := loadReleasePublishState(ctx, tx)
	if err != nil {
		return 0, false, err
	}
	idempotent, err := releaseSetIsIdempotent(ctx, tx, records)
	if err != nil {
		return 0, false, err
	}
	if idempotent {
		if err := tx.Commit(ctx); err != nil {
			return 0, false, fmt.Errorf("commit idempotent release publish: %w", err)
		}
		return generation, false, nil
	}
	generation, err = publishReleaseRecords(
		ctx, tx, records, storedRecords, generation, beforeFingerprint,
	)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("commit release publish: %w", err)
	}
	return generation, true, nil
}

func loadReleasePublishState(
	ctx context.Context,
	tx pgx.Tx,
) (int64, []central.ReleaseRecord, string, error) {
	generation, storedFingerprint, err := lockedDesktopState(ctx, tx)
	if err != nil {
		return 0, nil, "", err
	}
	storedRecords, err := listReleaseRecords(ctx, tx)
	if err != nil {
		return 0, nil, "", err
	}
	beforeFingerprint, err := central.ActiveDesktopManifestFingerprint(storedRecords)
	if err != nil {
		return 0, nil, "", fmt.Errorf("resolve current active desktop manifest: %w", err)
	}
	if beforeFingerprint != storedFingerprint {
		if err := synchronizeDesktopFingerprint(ctx, tx, generation, beforeFingerprint); err != nil {
			return 0, nil, "", err
		}
	}
	return generation, storedRecords, beforeFingerprint, nil
}

// synchronizeDesktopFingerprint refreshes derived registry state after the
// generated release contract changes without treating unchanged releases as a publication.
func synchronizeDesktopFingerprint(
	ctx context.Context,
	tx pgx.Tx,
	generation int64,
	fingerprint string,
) error {
	result, err := tx.Exec(ctx, `
		UPDATE desktop_release_registry_state
		SET active_manifest_sha256 = $2, updated_at = now()
		WHERE singleton = true AND generation = $1
	`, generation, fingerprint)
	if err != nil {
		return fmt.Errorf("synchronize active desktop manifest fingerprint: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("synchronize active desktop manifest fingerprint: expected one registry state row")
	}
	return nil
}

func releaseSetIsIdempotent(ctx context.Context, tx pgx.Tx, records []central.ReleaseRecord) (bool, error) {
	existing, err := releaseSetSize(ctx, tx, records[0])
	if err != nil || existing == 0 {
		return false, err
	}
	identical, err := identicalReleaseSet(ctx, tx, records, existing)
	if err != nil {
		return false, err
	}
	if !identical {
		return false, central.ErrConflict
	}
	return true, nil
}

func publishReleaseRecords(
	ctx context.Context,
	tx pgx.Tx,
	records []central.ReleaseRecord,
	storedRecords []central.ReleaseRecord,
	generation int64,
	beforeFingerprint string,
) (int64, error) {
	if err := insertReleaseSet(ctx, tx, records); err != nil {
		return 0, err
	}
	afterFingerprint, err := central.ActiveDesktopManifestFingerprint(append(storedRecords, records...))
	if err != nil {
		return 0, fmt.Errorf("resolve updated active desktop manifest: %w", err)
	}
	if afterFingerprint == beforeFingerprint {
		return generation, nil
	}
	return advanceDesktopGeneration(ctx, tx, generation, afterFingerprint)
}

func lockedDesktopState(ctx context.Context, tx pgx.Tx) (int64, string, error) {
	var generation int64
	var fingerprint string
	if err := tx.QueryRow(ctx, `
		SELECT generation, active_manifest_sha256
		FROM desktop_release_registry_state WHERE singleton = true FOR UPDATE
	`).Scan(&generation, &fingerprint); err != nil {
		return 0, "", fmt.Errorf("lock desktop release state: %w", err)
	}
	return generation, fingerprint, nil
}

func releaseSetSize(ctx context.Context, tx pgx.Tx, identity central.ReleaseRecord) (int64, error) {
	var count int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM release_components WHERE kind = $1 AND channel = $2 AND version = $3
	`, identity.Kind, identity.Channel, identity.Version).Scan(&count); err != nil {
		return 0, fmt.Errorf("count existing release set: %w", err)
	}
	return count, nil
}

func identicalReleaseSet(
	ctx context.Context,
	tx pgx.Tx,
	records []central.ReleaseRecord,
	existing int64,
) (bool, error) {
	if existing != int64(len(records)) {
		return false, nil
	}
	for _, record := range records {
		var equal bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM release_components
				WHERE kind = $1 AND channel = $2 AND platform = $3 AND architecture = $4
					AND version = $5 AND payload = $6::jsonb
			)
		`, record.Kind, record.Channel, record.Platform, record.Arch, record.Version,
			record.Payload).Scan(&equal); err != nil {
			return false, fmt.Errorf("compare existing release set: %w", err)
		}
		if !equal {
			return false, nil
		}
	}
	return true, nil
}

func insertReleaseSet(ctx context.Context, tx pgx.Tx, records []central.ReleaseRecord) error {
	for _, record := range records {
		if _, err := tx.Exec(ctx, `
			INSERT INTO release_components (
				kind, channel, platform, architecture, version, payload, published_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, record.Kind, record.Channel, record.Platform, record.Arch, record.Version,
			record.Payload, record.PublishedAt); err != nil {
			return fmt.Errorf("insert release component: %w", err)
		}
	}
	return nil
}

func listReleaseRecords(ctx context.Context, queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]central.ReleaseRecord, error) {
	rows, err := queryer.Query(ctx, `
		SELECT kind, channel, platform, architecture, version, payload, published_at
		FROM release_components
		ORDER BY kind, channel, platform, architecture, published_at, version
	`)
	if err != nil {
		return nil, fmt.Errorf("list release components: %w", err)
	}
	records, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (central.ReleaseRecord, error) {
		var record central.ReleaseRecord
		err := row.Scan(
			&record.Kind, &record.Channel, &record.Platform, &record.Arch,
			&record.Version, &record.Payload, &record.PublishedAt,
		)
		return record, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan release components: %w", err)
	}
	return records, nil
}

func advanceDesktopGeneration(
	ctx context.Context,
	tx pgx.Tx,
	current int64,
	fingerprint string,
) (int64, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE desktop_release_registry_state
		SET generation = generation + 1, active_manifest_sha256 = $2, updated_at = now()
		WHERE singleton = true AND generation = $1
		RETURNING generation
	`, current, fingerprint).Scan(&generation); err != nil {
		return 0, fmt.Errorf("update release generation: %w", err)
	}
	return generation, nil
}
