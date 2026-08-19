package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
)

func (store *Store) BootstrapAdminCredentials(
	ctx context.Context,
	credentials []central.AdminCredential,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin admin credential bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE admin_credentials IN EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock admin credentials: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM admin_credentials`).Scan(&count); err != nil {
		return fmt.Errorf("count admin credentials: %w", err)
	}
	if count > 0 {
		return tx.Commit(ctx)
	}
	if len(credentials) == 0 {
		return errors.New("initial admin credentials are required when no admin exists")
	}
	now := time.Now().UTC()
	for _, credential := range credentials {
		if _, err := tx.Exec(ctx, `
				INSERT INTO admin_credentials (user_id, display_name, password_hash, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $4)
			`, credential.UserID, credential.DisplayName, credential.PasswordHash, now); err != nil {
			return fmt.Errorf("insert admin credential %q: %w", credential.UserID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin credential bootstrap: %w", err)
	}
	return nil
}

func (store *Store) FindAdminCredential(
	ctx context.Context,
	userID string,
) (central.AdminCredential, error) {
	var credential central.AdminCredential
	err := store.pool.QueryRow(ctx, `
		SELECT user_id, display_name, password_hash
		FROM admin_credentials
		WHERE user_id = $1
	`, userID).Scan(&credential.UserID, &credential.DisplayName, &credential.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.AdminCredential{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.AdminCredential{}, fmt.Errorf("authenticate admin credential: %w", err)
	}
	return credential, nil
}
