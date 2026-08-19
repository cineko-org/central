package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLockKey int64 = 0x43494E454B4F

type migration struct {
	version  int64
	name     string
	contents []byte
	checksum string
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire Central migration connection: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("lock Central migrations: %w", err)
	}
	defer unlockMigrations(connection)

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS cineko_schema_migrations (
			version bigint PRIMARY KEY,
			checksum text NOT NULL DEFAULT '',
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		ALTER TABLE cineko_schema_migrations
			ADD COLUMN IF NOT EXISTS checksum text NOT NULL DEFAULT ''
	`); err != nil {
		return fmt.Errorf("initialize Central migrations: %w", err)
	}
	migrationSet, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, item := range migrationSet {
		if err := applyMigration(ctx, connection, item); err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read Central migrations: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	seenVersions := make(map[int64]string)
	loaded := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		versionText, _, _ := strings.Cut(entry.Name(), "_")
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse Central migration %q: %w", entry.Name(), err)
		}
		if previous, duplicate := seenVersions[version]; duplicate {
			return nil, fmt.Errorf("duplicate Central migration version %d in %q and %q", version, previous, entry.Name())
		}
		seenVersions[version] = entry.Name()
		contents, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read Central migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		loaded = append(loaded, migration{
			version: version, name: entry.Name(), contents: contents, checksum: hex.EncodeToString(digest[:]),
		})
	}
	return loaded, nil
}

func applyMigration(ctx context.Context, connection *pgxpool.Conn, item migration) error {
	var storedChecksum string
	err := connection.QueryRow(ctx, `
		SELECT checksum FROM cineko_schema_migrations WHERE version = $1
	`, item.version).Scan(&storedChecksum)
	switch {
	case err == nil && storedChecksum == "":
		if _, err := connection.Exec(ctx, `
			UPDATE cineko_schema_migrations SET checksum = $2 WHERE version = $1
		`, item.version, item.checksum); err != nil {
			return fmt.Errorf("backfill Central migration %d checksum: %w", item.version, err)
		}
		return nil
	case err == nil && storedChecksum != item.checksum:
		return fmt.Errorf("central migration %d checksum mismatch", item.version)
	case err == nil:
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("check Central migration %d: %w", item.version, err)
	}

	tx, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin Central migration %d: %w", item.version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, string(item.contents)); err != nil {
		return fmt.Errorf("apply Central migration %d: %w", item.version, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cineko_schema_migrations(version, checksum) VALUES ($1, $2)
	`, item.version, item.checksum); err != nil {
		return fmt.Errorf("record Central migration %d: %w", item.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Central migration %d: %w", item.version, err)
	}
	return nil
}

func unlockMigrations(connection *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = connection.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrationLockKey)
}
