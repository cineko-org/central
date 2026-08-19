package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
)

func (store *Store) CreateAdminSession(ctx context.Context, session central.AdminSession) error {
	_, err := store.pool.Exec(ctx, `
		WITH expired AS (
			DELETE FROM admin_sessions WHERE expires_at <= $5
		)
		INSERT INTO admin_sessions (
			token_hash, user_id, display_name, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`, session.TokenHash[:], session.UserID, session.DisplayName, session.ExpiresAt, session.CreatedAt)
	if err != nil {
		return fmt.Errorf("create admin session: %w", err)
	}
	return nil
}

func (store *Store) AuthenticateAdminSession(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (central.AdminSession, error) {
	var session central.AdminSession
	var storedHash []byte
	err := store.pool.QueryRow(ctx, `
		SELECT token_hash, user_id, display_name, expires_at, created_at
		FROM admin_sessions
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
	`, tokenHash[:], now).Scan(
		&storedHash, &session.UserID, &session.DisplayName, &session.ExpiresAt, &session.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.AdminSession{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.AdminSession{}, fmt.Errorf("authenticate admin session: %w", err)
	}
	copy(session.TokenHash[:], storedHash)
	return session, nil
}

func (store *Store) RevokeAdminSession(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) error {
	_, err := store.pool.Exec(ctx, `
		UPDATE admin_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE token_hash = $1
	`, tokenHash[:], now)
	if err != nil {
		return fmt.Errorf("revoke admin session: %w", err)
	}
	return nil
}
