package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (store *Store) ProvisionClientCredential(
	ctx context.Context,
	user central.ClientUser,
	tokenHash [32]byte,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin client credential provision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = EXCLUDED.updated_at
	`, user.ID, user.DisplayName, user.UpdatedAt); err != nil {
		return fmt.Errorf("provision client user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_credentials (user_id, token_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash, revoked_at = NULL, updated_at = EXCLUDED.updated_at
	`, user.ID, tokenHash[:], user.UpdatedAt); err != nil {
		return fmt.Errorf("provision client access token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit client credential provision: %w", err)
	}
	return nil
}

func (store *Store) ExchangeClientCredential(
	ctx context.Context,
	userID string,
	tokenHash [32]byte,
	_ time.Time,
) (central.ClientUser, error) {
	var user central.ClientUser
	var storedHash []byte
	err := store.pool.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at, credentials.token_hash
		FROM client_users AS users
		JOIN client_credentials AS credentials ON credentials.user_id = users.id
		WHERE users.id = $1 AND credentials.revoked_at IS NULL
	`, userID).Scan(&user.ID, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientUser{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("exchange client credential: %w", err)
	}
	if len(storedHash) != len(tokenHash) || subtle.ConstantTimeCompare(storedHash, tokenHash[:]) != 1 {
		return central.ClientUser{}, central.ErrUnauthorized
	}
	return user, nil
}

func (store *Store) CreateClientSession(ctx context.Context, session central.ClientSession) error {
	if _, err := store.pool.Exec(ctx, `
		WITH expired AS (
			DELETE FROM client_sessions WHERE refresh_expires_at <= $7 OR revoked_at IS NOT NULL
		)
		INSERT INTO client_sessions (
			id, user_id, token_hash, expires_at, refresh_token_hash, refresh_expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, session.UserID, session.TokenHash[:], session.ExpiresAt,
		session.RefreshTokenHash[:], session.RefreshExpiresAt, session.CreatedAt); err != nil {
		return fmt.Errorf("create client session: %w", err)
	}
	return nil
}

func (store *Store) RotateClientSession(
	ctx context.Context,
	refreshTokenHash [32]byte,
	session central.ClientSession,
	now time.Time,
) (central.ClientUser, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("begin client session rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var user central.ClientUser
	var storedHash []byte
	err = tx.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at, sessions.refresh_token_hash
		FROM client_sessions AS sessions
		JOIN client_users AS users ON users.id = sessions.user_id
		WHERE sessions.refresh_token_hash = $1
			AND sessions.refresh_expires_at > $2
			AND sessions.revoked_at IS NULL
		FOR UPDATE OF sessions
	`, refreshTokenHash[:], now).Scan(
		&user.ID, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt, &storedHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientUser{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("lock client refresh session: %w", err)
	}
	if len(storedHash) != len(refreshTokenHash) ||
		subtle.ConstantTimeCompare(storedHash, refreshTokenHash[:]) != 1 {
		return central.ClientUser{}, central.ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_sessions SET revoked_at = $2 WHERE refresh_token_hash = $1
	`, refreshTokenHash[:], now); err != nil {
		return central.ClientUser{}, fmt.Errorf("revoke client refresh session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_sessions (
			id, user_id, token_hash, expires_at, refresh_token_hash, refresh_expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, user.ID, session.TokenHash[:], session.ExpiresAt,
		session.RefreshTokenHash[:], session.RefreshExpiresAt, session.CreatedAt); err != nil {
		return central.ClientUser{}, fmt.Errorf("create rotated client session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ClientUser{}, fmt.Errorf("commit client session rotation: %w", err)
	}
	return user, nil
}

func (store *Store) RevokeClientSession(ctx context.Context, sessionID string, now time.Time) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE client_sessions SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL
	`, sessionID, now)
	if err != nil {
		return fmt.Errorf("revoke client session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrUnauthorized
	}
	return nil
}

func (store *Store) CreateLaunchTicket(ctx context.Context, ticket central.LaunchTicket) error {
	tag, err := store.pool.Exec(ctx, `
		WITH expired AS (
			DELETE FROM client_launch_tickets WHERE expires_at <= $16 OR consumed_at IS NOT NULL
		)
		INSERT INTO client_launch_tickets (
			id, user_id, installation_id, device_id, release_generation, client_version, artifact_sha256,
			protocol, browser_revision, browser_artifact_sha256, playwright_version, playwright_artifact_sha256,
			launcher_nonce, token_hash, expires_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		FROM desktop_release_registry_state
		WHERE singleton = true AND generation = $5
		FOR SHARE
	`, ticket.ID, ticket.UserID, ticket.InstallationID, ticket.DeviceID, ticket.ReleaseGeneration,
		ticket.ClientVersion, ticket.ArtifactSHA256, ticket.Protocol, ticket.BrowserRevision,
		ticket.BrowserArtifactSHA256, ticket.PlaywrightVersion, ticket.PlaywrightArtifactSHA256,
		ticket.LauncherNonce, ticket.TokenHash[:], ticket.ExpiresAt, ticket.CreatedAt)
	if err != nil {
		return fmt.Errorf("create client launch ticket: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return central.ErrStaleRelease
	}
	return nil
}

func (store *Store) ExchangeLaunchTicket(
	ctx context.Context,
	tokenHash [32]byte,
	clientNonce string,
	releaseGeneration int64,
	session central.ClientSession,
	now time.Time,
) (central.LaunchedClient, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.LaunchedClient{}, fmt.Errorf("begin client launch ticket exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT generation FROM desktop_release_registry_state WHERE singleton = true FOR SHARE
	`).Scan(&currentGeneration); err != nil {
		return central.LaunchedClient{}, fmt.Errorf("lock desktop release generation: %w", err)
	}
	if currentGeneration != releaseGeneration {
		return central.LaunchedClient{}, central.ErrStaleRelease
	}
	var launched central.LaunchedClient
	var storedHash []byte
	err = tx.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at,
			tickets.installation_id, tickets.device_id, tickets.release_generation, tickets.client_version,
			tickets.artifact_sha256, tickets.protocol, tickets.browser_revision, tickets.browser_artifact_sha256,
			tickets.playwright_version, tickets.playwright_artifact_sha256, tickets.token_hash
		FROM client_launch_tickets AS tickets
		JOIN client_users AS users ON users.id = tickets.user_id
		WHERE tickets.token_hash = $1 AND tickets.expires_at > $2 AND tickets.consumed_at IS NULL
		FOR UPDATE OF tickets
	`, tokenHash[:], now).Scan(
		&launched.User.ID, &launched.User.DisplayName, &launched.User.CreatedAt, &launched.User.UpdatedAt,
		&launched.Context.InstallationID, &launched.Context.DeviceID, &launched.Context.ReleaseGeneration,
		&launched.Context.ClientVersion,
		&launched.Context.ArtifactSHA256, &launched.Context.Protocol, &launched.Context.BrowserRevision,
		&launched.Context.BrowserArtifactSHA256, &launched.Context.PlaywrightVersion,
		&launched.Context.PlaywrightArtifactSHA256, &storedHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.LaunchedClient{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.LaunchedClient{}, fmt.Errorf("lock client launch ticket: %w", err)
	}
	if len(storedHash) != len(tokenHash) || subtle.ConstantTimeCompare(storedHash, tokenHash[:]) != 1 {
		return central.LaunchedClient{}, central.ErrUnauthorized
	}
	if launched.Context.ReleaseGeneration != releaseGeneration {
		return central.LaunchedClient{}, central.ErrStaleRelease
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_launch_tickets SET consumed_at = $2, client_nonce = $3 WHERE token_hash = $1
	`, tokenHash[:], now, clientNonce); err != nil {
		return central.LaunchedClient{}, fmt.Errorf("consume client launch ticket: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_sessions (
			id, user_id, token_hash, expires_at, refresh_token_hash, refresh_expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, launched.User.ID, session.TokenHash[:], session.ExpiresAt,
		session.RefreshTokenHash[:], session.RefreshExpiresAt, session.CreatedAt); err != nil {
		return central.LaunchedClient{}, fmt.Errorf("create launched client session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.LaunchedClient{}, fmt.Errorf("commit client launch ticket exchange: %w", err)
	}
	return launched, nil
}

func (store *Store) AuthenticateClientSession(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (central.ClientPrincipal, error) {
	var principal central.ClientPrincipal
	var storedHash []byte
	err := store.pool.QueryRow(ctx, `
		SELECT id, user_id, token_hash FROM client_sessions
		WHERE token_hash = $1 AND expires_at > $2 AND revoked_at IS NULL
	`, tokenHash[:], now).Scan(&principal.SessionID, &principal.UserID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientPrincipal{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.ClientPrincipal{}, fmt.Errorf("authenticate client session: %w", err)
	}
	if len(storedHash) != len(tokenHash) || subtle.ConstantTimeCompare(storedHash, tokenHash[:]) != 1 {
		return central.ClientPrincipal{}, central.ErrUnauthorized
	}
	return principal, nil
}

func (store *Store) UpsertClientDevice(
	ctx context.Context,
	device central.ClientDevice,
) (central.ClientDevice, error) {
	err := store.pool.QueryRow(ctx, `
		INSERT INTO client_devices (
			installation_id, user_id, device_id, platform, architecture, app_version,
			last_seen_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (installation_id) DO UPDATE SET
			device_id = EXCLUDED.device_id,
			platform = EXCLUDED.platform,
			architecture = EXCLUDED.architecture,
			app_version = EXCLUDED.app_version,
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
		WHERE client_devices.user_id = EXCLUDED.user_id
		RETURNING installation_id, user_id, device_id, platform, architecture, app_version,
			last_seen_at, created_at, updated_at
	`, device.InstallationID, device.UserID, device.DeviceID, device.Platform, device.Arch,
		device.AppVersion, device.LastSeenAt, device.CreatedAt, device.UpdatedAt).Scan(
		&device.InstallationID, &device.UserID, &device.DeviceID, &device.Platform, &device.Arch,
		&device.AppVersion, &device.LastSeenAt, &device.CreatedAt, &device.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientDevice{}, central.ErrUnauthorized
	}
	if err != nil {
		return central.ClientDevice{}, fmt.Errorf("upsert client device: %w", err)
	}
	return device, nil
}

func (store *Store) GetClientDevice(
	ctx context.Context,
	userID string,
	installationID string,
) (central.ClientDevice, error) {
	var device central.ClientDevice
	err := store.pool.QueryRow(ctx, `
		SELECT installation_id, user_id, device_id, platform, architecture, app_version,
			last_seen_at, created_at, updated_at
		FROM client_devices WHERE user_id = $1 AND installation_id = $2
	`, userID, installationID).Scan(
		&device.InstallationID, &device.UserID, &device.DeviceID, &device.Platform, &device.Arch,
		&device.AppVersion, &device.LastSeenAt, &device.CreatedAt, &device.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientDevice{}, central.ErrNotFound
	}
	if err != nil {
		return central.ClientDevice{}, fmt.Errorf("get client device: %w", err)
	}
	return device, nil
}

func (store *Store) GetClientUser(ctx context.Context, userID string) (central.ClientUser, error) {
	var user central.ClientUser
	err := store.pool.QueryRow(ctx, `
		SELECT id, display_name, created_at, updated_at FROM client_users WHERE id = $1
	`, userID).Scan(&user.ID, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientUser{}, central.ErrNotFound
	}
	if err != nil {
		return central.ClientUser{}, fmt.Errorf("get client user: %w", err)
	}
	return user, nil
}

func (store *Store) ClientResourceRevisions(ctx context.Context, userID string) (map[string]int64, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT resource_kind, MAX(sequence) FROM client_events
		WHERE user_id = $1 GROUP BY resource_kind
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list client resource revisions: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var kind string
		var revision int64
		if err := rows.Scan(&kind, &revision); err != nil {
			return nil, fmt.Errorf("scan client resource revision: %w", err)
		}
		result[kind] = revision
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate client resource revisions: %w", err)
	}
	return result, nil
}

func (store *Store) ListClientResources(
	ctx context.Context,
	userID string,
	kind string,
) ([]central.ClientResource, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT kind, id, user_id, revision, payload, created_at, updated_at
		FROM client_resources
		WHERE user_id = $1 AND kind = $2 AND deleted_at IS NULL
		ORDER BY updated_at DESC, id
	`, userID, kind)
	if err != nil {
		return nil, fmt.Errorf("list client resources: %w", err)
	}
	defer rows.Close()
	return collectClientResources(rows)
}

func collectClientResources(rows pgx.Rows) ([]central.ClientResource, error) {
	resources := make([]central.ClientResource, 0)
	for rows.Next() {
		resource, err := scanClientResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate client resources: %w", err)
	}
	return resources, nil
}

func (store *Store) GetClientResource(
	ctx context.Context,
	userID string,
	kind string,
	id string,
) (central.ClientResource, error) {
	resource, err := scanClientResource(store.pool.QueryRow(ctx, `
		SELECT kind, id, user_id, revision, payload, created_at, updated_at
		FROM client_resources
		WHERE user_id = $1 AND kind = $2 AND id = $3 AND deleted_at IS NULL
	`, userID, kind, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientResource{}, central.ErrNotFound
	}
	if err != nil {
		return central.ClientResource{}, fmt.Errorf("get client resource: %w", err)
	}
	return resource, nil
}

func (store *Store) PutClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
) (central.ClientResource, error) {
	return store.mutateClientResource(ctx, mutation, false)
}

func (store *Store) DeleteClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
) (central.ClientResource, error) {
	return store.mutateClientResource(ctx, mutation, true)
}

func (store *Store) mutateClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
	deleting bool,
) (central.ClientResource, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return central.ClientResource{}, fmt.Errorf("begin client resource mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockClientUser(ctx, tx, mutation.UserID); err != nil {
		return central.ClientResource{}, err
	}
	operation := clientMutationOperation(deleting)
	if resource, handled, err := replayClientCommand(ctx, tx, mutation, operation); handled || err != nil {
		return resource, err
	}
	resource, create, err := applyClientResourceMutation(ctx, tx, mutation, operation, deleting)
	if err != nil {
		return central.ClientResource{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if create && isConcurrentClientResourceCreate(err) {
			return central.ClientResource{}, central.ErrRevisionConflict
		}
		return central.ClientResource{}, fmt.Errorf("commit client resource mutation: %w", err)
	}
	return resource, nil
}

func applyClientResourceMutation(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
	deleting bool,
) (central.ClientResource, bool, error) {
	resource, create, err := prepareClientMutation(ctx, tx, mutation, deleting)
	if err != nil {
		return central.ClientResource{}, false, err
	}
	if err := writeClientResource(ctx, tx, resource, create, deleting); err != nil {
		if create && isConcurrentClientResourceCreate(err) {
			return central.ClientResource{}, false, central.ErrRevisionConflict
		}
		return central.ClientResource{}, false, err
	}
	eventType := mutation.Kind + ".updated.v1"
	if deleting {
		eventType = mutation.Kind + ".deleted.v1"
	}
	if err := recordClientMutation(ctx, tx, mutation, operation, eventType, resource); err != nil {
		return central.ClientResource{}, false, err
	}
	return resource, create, nil
}

func isConcurrentClientResourceCreate(err error) bool {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return false
	}
	return databaseError.Code == "23505" || databaseError.Code == "40001"
}

func clientMutationOperation(deleting bool) string {
	if deleting {
		return "delete"
	}
	return "put"
}

func prepareClientMutation(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	deleting bool,
) (central.ClientResource, bool, error) {
	current, deleted, err := lockClientResource(ctx, tx, mutation.UserID, mutation.Kind, mutation.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return central.ClientResource{}, false, err
		}
		if deleting {
			return central.ClientResource{}, false, central.ErrNotFound
		}
		if mutation.ExpectedRevision != nil {
			return central.ClientResource{}, false, central.ErrRevisionConflict
		}
		return central.ClientResource{
			Kind: mutation.Kind, ID: mutation.ID, UserID: mutation.UserID,
			Revision: 1, Data: mutation.Data, CreatedAt: mutation.Now, UpdatedAt: mutation.Now,
		}, true, nil
	}
	if deleted {
		return central.ClientResource{}, false, central.ErrNotFound
	}
	if mutation.ExpectedRevision == nil {
		return central.ClientResource{}, false, central.ErrRevisionConflict
	}
	if *mutation.ExpectedRevision != current.Revision {
		return central.ClientResource{}, false, central.ErrRevisionConflict
	}
	current.Revision++
	current.UpdatedAt = mutation.Now
	if !deleting {
		current.Data = mutation.Data
	}
	return current, false, nil
}

func writeClientResource(
	ctx context.Context,
	tx pgx.Tx,
	resource central.ClientResource,
	create bool,
	deleting bool,
) error {
	if create {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
			VALUES ($1, $2, $3, 1, $4, $5, $5)
		`, resource.UserID, resource.Kind, resource.ID, resource.Data, resource.CreatedAt); err != nil {
			return fmt.Errorf("create client resource: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_resources SET revision = $4, payload = $5, updated_at = $6, deleted_at = $7
		WHERE user_id = $1 AND kind = $2 AND id = $3
	`, resource.UserID, resource.Kind, resource.ID, resource.Revision, resource.Data,
		resource.UpdatedAt, nullableDeletion(deleting, resource.UpdatedAt)); err != nil {
		return fmt.Errorf("update client resource: %w", err)
	}
	return nil
}

func replayClientCommand(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
) (central.ClientResource, bool, error) {
	var storedOperation, kind, id string
	var revision int64
	err := tx.QueryRow(ctx, `
		SELECT operation, resource_kind, resource_id, result_revision
		FROM client_commands WHERE user_id = $1 AND command_id = $2
	`, mutation.UserID, mutation.CommandID).Scan(&storedOperation, &kind, &id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return central.ClientResource{}, false, nil
	}
	if err != nil {
		return central.ClientResource{}, true, fmt.Errorf("read client command: %w", err)
	}
	if storedOperation != operation || kind != mutation.Kind || id != mutation.ID {
		return central.ClientResource{}, true, central.ErrIdempotencyConflict
	}
	resource, _, err := lockClientResource(ctx, tx, mutation.UserID, kind, id)
	if err != nil {
		return central.ClientResource{}, true, fmt.Errorf("replay client command: %w", err)
	}
	if resource.Revision != revision {
		return central.ClientResource{}, true, central.ErrIdempotencyConflict
	}
	return resource, true, nil
}

func lockClientResource(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	kind string,
	id string,
) (central.ClientResource, bool, error) {
	var resource central.ClientResource
	var deletedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT kind, id, user_id, revision, payload, created_at, updated_at, deleted_at
		FROM client_resources WHERE user_id = $1 AND kind = $2 AND id = $3 FOR UPDATE
	`, userID, kind, id).Scan(
		&resource.Kind, &resource.ID, &resource.UserID, &resource.Revision,
		&resource.Data, &resource.CreatedAt, &resource.UpdatedAt, &deletedAt,
	)
	return resource, deletedAt != nil, err
}

func recordClientMutation(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
	eventType string,
	resource central.ClientResource,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_commands (
			user_id, command_id, operation, resource_kind, resource_id, result_revision, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, mutation.UserID, mutation.CommandID, operation, mutation.Kind, mutation.ID,
		resource.Revision, mutation.Now); err != nil {
		return fmt.Errorf("record client command: %w", err)
	}
	eventID := clientEventID(mutation.UserID, mutation.CommandID)
	payload := resource.Data
	if operation == "delete" {
		payload = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventID, mutation.UserID, eventType, mutation.Kind, mutation.ID,
		resource.Revision, payload, mutation.Now); err != nil {
		return fmt.Errorf("record client event: %w", err)
	}
	return nil
}

func (store *Store) ClientEventPage(
	ctx context.Context,
	userID string,
	after int64,
	limit int,
) (central.ClientEventPage, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return central.ClientEventPage{}, fmt.Errorf("begin client event page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page := central.ClientEventPage{}
	if err := tx.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT pruned_through FROM client_event_cursors WHERE user_id = $1), 0),
			GREATEST(
				COALESCE((SELECT MAX(sequence) FROM client_events WHERE user_id = $1), 0),
				COALESCE((SELECT pruned_through FROM client_event_cursors WHERE user_id = $1), 0)
			),
			(SELECT generation FROM desktop_release_registry_state WHERE singleton = true)
	`, userID).Scan(&page.PrunedThrough, &page.Latest, &page.ReleaseGeneration); err != nil {
		return central.ClientEventPage{}, fmt.Errorf("read client event stream state: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT sequence, id, event_type, occurred_at, resource_kind, resource_id,
			resource_revision, payload
		FROM client_events
		WHERE user_id = $1 AND sequence > $2
		ORDER BY sequence LIMIT $3
	`, userID, after, limit)
	if err != nil {
		return central.ClientEventPage{}, fmt.Errorf("list client events: %w", err)
	}
	defer rows.Close()
	page.Events = make([]central.ClientEvent, 0)
	for rows.Next() {
		var event central.ClientEvent
		if err := rows.Scan(
			&event.Sequence, &event.ID, &event.Type, &event.OccurredAt,
			&event.Resource.Kind, &event.Resource.ID, &event.Resource.Revision, &event.Data,
		); err != nil {
			return central.ClientEventPage{}, fmt.Errorf("scan client event: %w", err)
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return central.ClientEventPage{}, fmt.Errorf("iterate client events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ClientEventPage{}, fmt.Errorf("commit client event page: %w", err)
	}
	return page, nil
}

func (store *Store) ListClientEvents(
	ctx context.Context,
	userID string,
	after int64,
	limit int,
) ([]central.ClientEvent, error) {
	page, err := store.ClientEventPage(ctx, userID, after, limit)
	return page.Events, err
}

type resourceScanner interface {
	Scan(...any) error
}

func scanClientResource(scanner resourceScanner) (central.ClientResource, error) {
	var resource central.ClientResource
	err := scanner.Scan(
		&resource.Kind, &resource.ID, &resource.UserID, &resource.Revision,
		&resource.Data, &resource.CreatedAt, &resource.UpdatedAt,
	)
	return resource, err
}

func nullableDeletion(deleting bool, now time.Time) *time.Time {
	if deleting {
		return &now
	}
	return nil
}

func clientEventID(userID string, commandID string) string {
	digest := sha256.Sum256([]byte(userID + "\x00" + commandID))
	return "event_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}
