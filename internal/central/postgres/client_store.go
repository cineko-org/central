package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/domain/clientresources"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func scanClientUser(row rowScanner) (*clientpb.User, error) {
	return scanClientUserWithHash(row, nil)
}

func clientUser(id, displayName string, createdAt, updatedAt time.Time) *clientpb.User {
	user := &clientpb.User{}
	user.SetId(id)
	user.SetDisplayName(displayName)
	user.SetCreatedAt(timestamppb.New(createdAt))
	user.SetUpdatedAt(timestamppb.New(updatedAt))
	return user
}

func scanClientUserWithHash(row rowScanner, tokenHash *[]byte) (*clientpb.User, error) {
	var id, displayName string
	var createdAt, updatedAt time.Time
	targets := []any{&id, &displayName, &createdAt, &updatedAt}
	if tokenHash != nil {
		targets = append(targets, tokenHash)
	}
	if err := row.Scan(targets...); err != nil {
		return nil, err
	}
	return clientUser(id, displayName, createdAt, updatedAt), nil
}

func scanClientDevice(row rowScanner) (*clientpb.Device, error) {
	var installationID, userID, deviceID, platform, architecture, appVersion string
	var lastSeenAt, createdAt, updatedAt time.Time
	if err := row.Scan(
		&installationID, &userID, &deviceID, &platform, &architecture, &appVersion,
		&lastSeenAt, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	device := &clientpb.Device{}
	device.SetInstallationId(installationID)
	device.SetUserId(userID)
	device.SetDeviceId(deviceID)
	device.SetPlatform(platform)
	device.SetArchitecture(architecture)
	device.SetAppVersion(appVersion)
	device.SetLastSeenAt(timestamppb.New(lastSeenAt))
	device.SetCreatedAt(timestamppb.New(createdAt))
	device.SetUpdatedAt(timestamppb.New(updatedAt))
	return device, nil
}

func (store *Store) ProvisionClientCredential(
	ctx context.Context,
	user *clientpb.User,
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
	`, user.GetId(), user.GetDisplayName(), user.GetUpdatedAt().AsTime()); err != nil {
		return fmt.Errorf("provision client user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_credentials (user_id, token_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash, revoked_at = NULL, updated_at = EXCLUDED.updated_at
	`, user.GetId(), tokenHash[:], user.GetUpdatedAt().AsTime()); err != nil {
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
) (*clientpb.User, error) {
	var storedHash []byte
	row := store.pool.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at, credentials.token_hash
		FROM client_users AS users
		JOIN client_credentials AS credentials ON credentials.user_id = users.id
		WHERE users.id = $1 AND credentials.revoked_at IS NULL
	`, userID)
	user, err := scanClientUserWithHash(row, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("exchange client credential: %w", err)
	}
	if len(storedHash) != len(tokenHash) || subtle.ConstantTimeCompare(storedHash, tokenHash[:]) != 1 {
		return nil, central.ErrUnauthorized
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
) (*clientpb.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin client session rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storedHash []byte
	row := tx.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at, sessions.refresh_token_hash
		FROM client_sessions AS sessions
		JOIN client_users AS users ON users.id = sessions.user_id
		WHERE sessions.refresh_token_hash = $1
			AND sessions.refresh_expires_at > $2
			AND sessions.revoked_at IS NULL
		FOR UPDATE OF sessions
	`, refreshTokenHash[:], now)
	user, err := scanClientUserWithHash(row, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("lock client refresh session: %w", err)
	}
	if len(storedHash) != len(refreshTokenHash) ||
		subtle.ConstantTimeCompare(storedHash, refreshTokenHash[:]) != 1 {
		return nil, central.ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_sessions SET revoked_at = $2 WHERE refresh_token_hash = $1
	`, refreshTokenHash[:], now); err != nil {
		return nil, fmt.Errorf("revoke client refresh session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_sessions (
			id, user_id, token_hash, expires_at, refresh_token_hash, refresh_expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, user.GetId(), session.TokenHash[:], session.ExpiresAt,
		session.RefreshTokenHash[:], session.RefreshExpiresAt, session.CreatedAt); err != nil {
		return nil, fmt.Errorf("create rotated client session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit client session rotation: %w", err)
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
			DELETE FROM client_launch_tickets WHERE expires_at <= $15 OR consumed_at IS NOT NULL
		)
		INSERT INTO client_launch_tickets (
			id, user_id, installation_id, device_id, release_generation, client_version, artifact_sha256,
			browser_revision, browser_artifact_sha256, playwright_version, playwright_artifact_sha256,
			launcher_nonce, token_hash, expires_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		FROM desktop_release_registry_state
		WHERE singleton = true AND generation = $5
		FOR SHARE
	`, ticket.ID, ticket.UserID, ticket.InstallationID, ticket.DeviceID, ticket.ReleaseGeneration,
		ticket.ClientVersion, ticket.ArtifactSHA256, ticket.BrowserRevision,
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
	launched.Context = &clientpb.LaunchContext{}
	var userID, displayName string
	var userCreatedAt, userUpdatedAt time.Time
	var installationID, deviceID, clientVersion, artifactSHA256 string
	var browserRevision, browserArtifactSHA256, playwrightVersion, playwrightArtifactSHA256 string
	var launchGeneration int64
	var storedHash []byte
	err = tx.QueryRow(ctx, `
		SELECT users.id, users.display_name, users.created_at, users.updated_at,
			tickets.installation_id, tickets.device_id, tickets.release_generation, tickets.client_version,
			tickets.artifact_sha256, tickets.browser_revision, tickets.browser_artifact_sha256,
			tickets.playwright_version, tickets.playwright_artifact_sha256, tickets.token_hash
		FROM client_launch_tickets AS tickets
		JOIN client_users AS users ON users.id = tickets.user_id
		WHERE tickets.token_hash = $1 AND tickets.expires_at > $2 AND tickets.consumed_at IS NULL
		FOR UPDATE OF tickets
	`, tokenHash[:], now).Scan(
		&userID, &displayName, &userCreatedAt, &userUpdatedAt,
		&installationID, &deviceID, &launchGeneration, &clientVersion,
		&artifactSHA256, &browserRevision, &browserArtifactSHA256, &playwrightVersion,
		&playwrightArtifactSHA256, &storedHash,
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
	launched.User = clientUser(userID, displayName, userCreatedAt, userUpdatedAt)
	launched.Context.SetInstallationId(installationID)
	launched.Context.SetDeviceId(deviceID)
	launched.Context.SetReleaseGeneration(launchGeneration)
	launched.Context.SetClientVersion(clientVersion)
	launched.Context.SetArtifactSha256(artifactSHA256)
	launched.Context.SetBrowserRevision(browserRevision)
	launched.Context.SetBrowserArtifactSha256(browserArtifactSHA256)
	launched.Context.SetPlaywrightVersion(playwrightVersion)
	launched.Context.SetPlaywrightArtifactSha256(playwrightArtifactSHA256)
	if launched.Context.GetReleaseGeneration() != releaseGeneration {
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
	`, session.ID, launched.User.GetId(), session.TokenHash[:], session.ExpiresAt,
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
	device *clientpb.Device,
) (*clientpb.Device, error) {
	row := store.pool.QueryRow(ctx, `
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
	`, device.GetInstallationId(), device.GetUserId(), device.GetDeviceId(), device.GetPlatform(), device.GetArchitecture(),
		device.GetAppVersion(), device.GetLastSeenAt().AsTime(), device.GetCreatedAt().AsTime(), device.GetUpdatedAt().AsTime())
	stored, err := scanClientDevice(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("upsert client device: %w", err)
	}
	return stored, nil
}

func (store *Store) GetClientDevice(
	ctx context.Context,
	userID string,
	installationID string,
) (*clientpb.Device, error) {
	row := store.pool.QueryRow(ctx, `
		SELECT installation_id, user_id, device_id, platform, architecture, app_version,
			last_seen_at, created_at, updated_at
		FROM client_devices WHERE user_id = $1 AND installation_id = $2
	`, userID, installationID)
	device, err := scanClientDevice(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get client device: %w", err)
	}
	return device, nil
}

func (store *Store) GetClientUser(ctx context.Context, userID string) (*clientpb.User, error) {
	user, err := scanClientUser(store.pool.QueryRow(ctx, `
		SELECT id, display_name, created_at, updated_at FROM client_users WHERE id = $1
	`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get client user: %w", err)
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
) ([]*clientpb.Resource, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("begin client resource list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT kind, id, user_id, revision, created_at, updated_at
		FROM client_resources
		WHERE user_id = $1 AND kind = $2 AND deleted_at IS NULL
		ORDER BY updated_at DESC, id
	`, userID, kind)
	if err != nil {
		return nil, fmt.Errorf("list client resources: %w", err)
	}
	defer rows.Close()
	stored, err := collectStoredClientResources(rows)
	if err != nil {
		return nil, err
	}
	resources := make([]*clientpb.Resource, 0, len(stored))
	for index := range stored {
		if err := loadClientResourceBody(ctx, tx, &stored[index]); err != nil {
			return nil, fmt.Errorf("load client resource body: %w", err)
		}
		resource, err := stored[index].proto()
		if err != nil {
			return nil, fmt.Errorf("build client resource: %w", err)
		}
		resources = append(resources, resource)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit client resource list: %w", err)
	}
	return resources, nil
}

func collectStoredClientResources(rows pgx.Rows) ([]storedClientResource, error) {
	resources := make([]storedClientResource, 0)
	for rows.Next() {
		stored, err := scanStoredClientResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, stored)
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
) (*clientpb.Resource, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("begin client resource get: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := scanStoredClientResource(tx.QueryRow(ctx, `
		SELECT kind, id, user_id, revision, created_at, updated_at
		FROM client_resources
		WHERE user_id = $1 AND kind = $2 AND id = $3 AND deleted_at IS NULL
	`, userID, kind, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get client resource: %w", err)
	}
	if err := loadClientResourceBody(ctx, tx, &stored); err != nil {
		return nil, fmt.Errorf("load client resource body: %w", err)
	}
	resource, err := stored.proto()
	if err != nil {
		return nil, fmt.Errorf("build client resource: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit client resource get: %w", err)
	}
	return resource, nil
}

func (store *Store) PutClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
) (*clientpb.Resource, error) {
	return store.mutateClientResource(ctx, mutation, false)
}

func (store *Store) DeleteClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
) (*clientpb.Resource, error) {
	return store.mutateClientResource(ctx, mutation, true)
}

func (store *Store) mutateClientResource(
	ctx context.Context,
	mutation central.ResourceMutation,
	deleting bool,
) (*clientpb.Resource, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin client resource mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockClientUser(ctx, tx, mutation.UserID); err != nil {
		return nil, err
	}
	operation := clientMutationOperation(deleting)
	if resource, handled, err := replayClientCommand(ctx, tx, mutation, operation); handled || err != nil {
		if err != nil {
			return nil, err
		}
		return resource.proto()
	}
	retryCommandID, rearmExecution, err := prepareMonitorExecutionRearm(ctx, tx, mutation, deleting)
	if err != nil {
		return nil, err
	}
	if err := invalidateDeletedMonitorExecutions(ctx, tx, mutation, deleting); err != nil {
		return nil, err
	}
	resource, create, err := applyClientResourceMutation(ctx, tx, mutation, operation, deleting)
	if err != nil {
		return nil, err
	}
	if rearmExecution {
		if err := rearmMonitorExecution(ctx, tx, mutation, retryCommandID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if create && isConcurrentClientResourceCreate(err) {
			return nil, central.ErrRevisionConflict
		}
		return nil, fmt.Errorf("commit client resource mutation: %w", err)
	}
	return resource.proto()
}

// invalidateDeletedMonitorExecutions keeps non-Monitor mutations out of the
// execution lifecycle while preserving the command-before-resource lock order.
func invalidateDeletedMonitorExecutions(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	deleting bool,
) error {
	if !deleting || mutation.Kind != "monitors" {
		return nil
	}
	return invalidateMonitorExecutions(ctx, tx, mutation)
}

// prepareMonitorExecutionRearm locks the execution command before the Monitor
// resource. Execution completion uses the same lock order, preventing a retry
// mutation from deadlocking with a result arriving from another installation.
func prepareMonitorExecutionRearm(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	deleting bool,
) (string, bool, error) {
	if deleting || mutation.Kind != "monitors" ||
		mutation.Resource.GetMonitor().GetState().GetPending() == nil {
		return "", false, nil
	}
	var commandID string
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM client_execution_commands
		WHERE user_id = $1 AND monitor_id = $2
			AND status IN ('failed', 'completed') AND starts_at > $3
		ORDER BY created_at DESC, id
		LIMIT 1 FOR UPDATE
	`, mutation.UserID, mutation.ID, mutation.Now).Scan(&commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lock monitor execution retry candidate: %w", err)
	}
	current, deleted, err := lockClientResource(ctx, tx, mutation.UserID, mutation.Kind, mutation.ID)
	if errors.Is(err, pgx.ErrNoRows) || deleted {
		return commandID, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lock monitor for execution retry: %w", err)
	}
	state := current.body.GetMonitor().GetState()
	return commandID, state.GetTriggered() != nil || state.GetPaymentUnknown() != nil ||
		state.GetFailed() != nil || state.GetStopped() != nil, nil
}

// rearmMonitorExecution applies the user's explicit Monitor retry to the most
// recent future execution. If no command exists, the pending Monitor simply
// resumes shared discovery and a later observation will create one.
func rearmMonitorExecution(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	commandID string,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'queued', leased_installation_id = NULL, last_installation_id = NULL,
			lease_token_hash = NULL, lease_expires_at = NULL, attempt_count = 0,
			reason_code = '', completed_at = NULL, updated_at = $3
		WHERE id = $1 AND user_id = $2 AND status IN ('failed', 'completed')
	`, commandID, mutation.UserID, mutation.Now)
	if err != nil {
		return fmt.Errorf("rearm monitor execution: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("rearm monitor execution %q: command state changed", commandID)
	}
	return recordExecutionReadyEvent(
		ctx, tx, mutation.UserID, commandID, mutation.ID,
		"explicit_monitor_retry", "explicit-monitor-retry:"+mutation.CommandID, mutation.Now,
	)
}

// invalidateMonitorExecutions locks commands before the monitor row. Execution
// completion uses the same order, so delete and completion cannot deadlock.
// A later revision failure rolls this update back with the resource mutation.
func invalidateMonitorExecutions(ctx context.Context, tx pgx.Tx, mutation central.ResourceMutation) error {
	if _, err := tx.Exec(ctx, `
		UPDATE client_execution_commands
		SET status = 'failed', leased_installation_id = NULL,
			last_installation_id = COALESCE(leased_installation_id, last_installation_id),
			lease_token_hash = NULL, lease_expires_at = NULL,
			reason_code = 'monitor_deleted', completed_at = $3, updated_at = $3
		WHERE user_id = $1 AND monitor_id = $2 AND status IN ('queued', 'leased')
	`, mutation.UserID, mutation.ID, mutation.Now); err != nil {
		return fmt.Errorf("invalidate deleted monitor executions: %w", err)
	}
	return nil
}

func lockClientUser(ctx context.Context, tx pgx.Tx, userID string) error {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM client_users WHERE id = $1 FOR UPDATE`, userID).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return central.ErrUnauthorized
	} else if err != nil {
		return fmt.Errorf("lock client user: %w", err)
	}
	return nil
}

func applyClientResourceMutation(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
	deleting bool,
) (storedClientResource, bool, error) {
	resource, create, err := prepareClientMutation(ctx, tx, mutation, deleting)
	if err != nil {
		return storedClientResource{}, false, err
	}
	if err := writeClientResource(ctx, tx, resource, create, deleting); err != nil {
		if create && isConcurrentClientResourceCreate(err) {
			return storedClientResource{}, false, central.ErrRevisionConflict
		}
		return storedClientResource{}, false, err
	}
	eventType := mutation.Kind + ".updated"
	if deleting {
		eventType = mutation.Kind + ".deleted"
	}
	if err := recordClientMutation(ctx, tx, mutation, operation, eventType, resource); err != nil {
		return storedClientResource{}, false, err
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
) (storedClientResource, bool, error) {
	current, deleted, err := lockClientResource(ctx, tx, mutation.UserID, mutation.Kind, mutation.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return storedClientResource{}, false, err
		}
		if deleting {
			return storedClientResource{}, false, central.ErrNotFound
		}
		if mutation.ExpectedRevision != nil {
			return storedClientResource{}, false, central.ErrRevisionConflict
		}
		return storedClientResource{
			kind: mutation.Kind, id: mutation.ID, userID: mutation.UserID,
			revision: 1, body: cloneClientResource(mutation.Resource), createdAt: mutation.Now, updatedAt: mutation.Now,
		}, true, nil
	}
	if deleted {
		return storedClientResource{}, false, central.ErrNotFound
	}
	if mutation.ExpectedRevision == nil {
		return storedClientResource{}, false, central.ErrRevisionConflict
	}
	if *mutation.ExpectedRevision != current.revision {
		return storedClientResource{}, false, central.ErrRevisionConflict
	}
	current.revision++
	current.updatedAt = mutation.Now
	if !deleting {
		current.body = cloneClientResource(mutation.Resource)
	}
	return current, false, nil
}

func writeClientResource(
	ctx context.Context,
	tx pgx.Tx,
	resource storedClientResource,
	create bool,
	deleting bool,
) error {
	if create {
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_resources (user_id, kind, id, revision, created_at, updated_at)
			VALUES ($1, $2, $3, 1, $4, $4)
		`, resource.userID, resource.kind, resource.id, resource.createdAt); err != nil {
			return fmt.Errorf("create client resource: %w", err)
		}
		return writeClientResourceBody(ctx, tx, resource)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE client_resources SET revision = $4, updated_at = $5, deleted_at = $6
		WHERE user_id = $1 AND kind = $2 AND id = $3
	`, resource.userID, resource.kind, resource.id, resource.revision,
		resource.updatedAt, nullableDeletion(deleting, resource.updatedAt)); err != nil {
		return fmt.Errorf("update client resource: %w", err)
	}
	if !deleting {
		return writeClientResourceBody(ctx, tx, resource)
	}
	return nil
}

func replayClientCommand(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
) (storedClientResource, bool, error) {
	var storedOperation, kind, id string
	var revision int64
	err := tx.QueryRow(ctx, `
		SELECT operation, resource_kind, resource_id, result_revision
		FROM client_commands WHERE user_id = $1 AND command_id = $2
	`, mutation.UserID, mutation.CommandID).Scan(&storedOperation, &kind, &id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedClientResource{}, false, nil
	}
	if err != nil {
		return storedClientResource{}, true, fmt.Errorf("read client command: %w", err)
	}
	if storedOperation != operation || kind != mutation.Kind || id != mutation.ID {
		return storedClientResource{}, true, central.ErrIdempotencyConflict
	}
	resource, _, err := lockClientResource(ctx, tx, mutation.UserID, kind, id)
	if err != nil {
		return storedClientResource{}, true, fmt.Errorf("replay client command: %w", err)
	}
	if resource.revision != revision {
		return storedClientResource{}, true, central.ErrIdempotencyConflict
	}
	return resource, true, nil
}

func lockClientResource(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	kind string,
	id string,
) (storedClientResource, bool, error) {
	var resource storedClientResource
	var deletedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT kind, id, user_id, revision, created_at, updated_at, deleted_at
		FROM client_resources WHERE user_id = $1 AND kind = $2 AND id = $3 FOR UPDATE
	`, userID, kind, id).Scan(
		&resource.kind, &resource.id, &resource.userID, &resource.revision,
		&resource.createdAt, &resource.updatedAt, &deletedAt,
	)
	if err != nil {
		return resource, deletedAt != nil, err
	}
	if err := loadClientResourceBody(ctx, tx, &resource); err != nil {
		return storedClientResource{}, false, fmt.Errorf("load locked client resource body: %w", err)
	}
	return resource, deletedAt != nil, nil
}

func recordClientMutation(
	ctx context.Context,
	tx pgx.Tx,
	mutation central.ResourceMutation,
	operation string,
	eventType string,
	resource storedClientResource,
) error {
	payload := []byte("{}")
	if !strings.HasSuffix(eventType, ".deleted") {
		var err error
		payload, err = resourcePayload(resource)
		if err != nil {
			return fmt.Errorf("encode client event resource: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_commands (
			user_id, command_id, operation, resource_kind, resource_id, result_revision, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, mutation.UserID, mutation.CommandID, operation, mutation.Kind, mutation.ID,
		resource.revision, mutation.Now); err != nil {
		return fmt.Errorf("record client command: %w", err)
	}
	eventID := clientEventID(mutation.UserID, mutation.CommandID)
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventID, mutation.UserID, eventType, mutation.Kind, mutation.ID,
		resource.revision, payload, mutation.Now); err != nil {
		return fmt.Errorf("record client event: %w", err)
	}
	return nil
}

func (store *Store) ClientEventPage(
	ctx context.Context,
	userID string,
	after int64,
	limit int,
) (central.ClientEventBatch, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return central.ClientEventBatch{}, fmt.Errorf("begin client event page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page := central.ClientEventBatch{}
	if err := tx.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT pruned_through FROM client_event_cursors WHERE user_id = $1), 0),
			GREATEST(
				COALESCE((SELECT MAX(sequence) FROM client_events WHERE user_id = $1), 0),
				COALESCE((SELECT pruned_through FROM client_event_cursors WHERE user_id = $1), 0)
			),
			(SELECT generation FROM desktop_release_registry_state WHERE singleton = true)
	`, userID).Scan(&page.PrunedThrough, &page.Latest, &page.ReleaseGeneration); err != nil {
		return central.ClientEventBatch{}, fmt.Errorf("read client event stream state: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT sequence, id, event_type, occurred_at, resource_kind, resource_id,
			resource_revision, payload
		FROM client_events
		WHERE user_id = $1 AND sequence > $2
		ORDER BY sequence LIMIT $3
	`, userID, after, limit)
	if err != nil {
		return central.ClientEventBatch{}, fmt.Errorf("list client events: %w", err)
	}
	defer rows.Close()
	page.Events = make([]*clientpb.ClientEvent, 0)
	for rows.Next() {
		var sequence, revision int64
		var id, eventType, kind, resourceID string
		var occurredAt time.Time
		var payload []byte
		if err := rows.Scan(
			&sequence, &id, &eventType, &occurredAt, &kind, &resourceID, &revision, &payload,
		); err != nil {
			return central.ClientEventBatch{}, fmt.Errorf("scan client event: %w", err)
		}
		event := &clientpb.ClientEvent{}
		event.SetSequence(sequence)
		event.SetId(id)
		event.SetOccurredAt(timestamppb.New(occurredAt))
		switch {
		case strings.HasSuffix(eventType, ".deleted"):
			kindMessage, err := clientresources.KindMessage(kind)
			if err != nil {
				return central.ClientEventBatch{}, fmt.Errorf("decode deleted client event resource: %w", err)
			}
			deleted := &clientpb.DeletedResource{}
			deleted.SetId(resourceID)
			deleted.SetRevision(revision)
			deleted.SetKind(kindMessage)
			event.SetDeleted(deleted)
		case eventType == "execution.ready":
			ready := &clientpb.ExecutionReady{}
			if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, ready); err != nil {
				return central.ClientEventBatch{}, fmt.Errorf("decode execution-ready client event: %w", err)
			}
			event.SetExecutionReady(ready)
		default:
			resource, err := clientresources.DecodeEventResource(kind, resourceID, revision, payload)
			if err != nil {
				return central.ClientEventBatch{}, fmt.Errorf("decode client event resource: %w", err)
			}
			event.SetUpserted(resource)
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return central.ClientEventBatch{}, fmt.Errorf("iterate client events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return central.ClientEventBatch{}, fmt.Errorf("commit client event page: %w", err)
	}
	return page, nil
}

func (store *Store) ListClientEvents(
	ctx context.Context,
	userID string,
	after int64,
	limit int,
) ([]*clientpb.ClientEvent, error) {
	page, err := store.ClientEventPage(ctx, userID, after, limit)
	return page.Events, err
}

type resourceScanner interface {
	Scan(...any) error
}

type storedClientResource struct {
	kind      string
	id        string
	userID    string
	revision  int64
	body      *clientpb.Resource
	createdAt time.Time
	updatedAt time.Time
}

func (resource storedClientResource) proto() (*clientpb.Resource, error) {
	if resource.body == nil || clientresources.Kind(resource.body) != resource.kind {
		return nil, fmt.Errorf("missing normalized %s resource body", resource.kind)
	}
	result := cloneClientResource(resource.body)
	identity := &commonpb.ResourceIdentity{}
	identity.SetId(resource.id)
	identity.SetRevision(resource.revision)
	identity.SetCreatedAt(timestamppb.New(resource.createdAt))
	identity.SetUpdatedAt(timestamppb.New(resource.updatedAt))
	result.SetIdentity(identity)
	return result, nil
}

func scanStoredClientResource(scanner resourceScanner) (storedClientResource, error) {
	var resource storedClientResource
	err := scanner.Scan(
		&resource.kind, &resource.id, &resource.userID, &resource.revision,
		&resource.createdAt, &resource.updatedAt,
	)
	return resource, err
}

func cloneClientResource(resource *clientpb.Resource) *clientpb.Resource {
	if resource == nil {
		return nil
	}
	return proto.CloneOf(resource)
}

func resourcePayload(resource storedClientResource) ([]byte, error) {
	message, err := resource.proto()
	if err != nil {
		return nil, err
	}
	return clientresources.Payload(message)
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
