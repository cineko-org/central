package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/cineko-org/central/internal/central"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (store *Store) SnapshotClientConfiguration(
	ctx context.Context,
	userID string,
	kinds []string,
) (central.ConfigurationSnapshot, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return central.ConfigurationSnapshot{}, fmt.Errorf("begin configuration snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshot central.ConfigurationSnapshot
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0) FROM client_events WHERE user_id = $1
	`, userID).Scan(&snapshot.Revision); err != nil {
		return central.ConfigurationSnapshot{}, fmt.Errorf("read configuration revision: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT kind, id, payload FROM client_resources
		WHERE user_id = $1 AND kind = ANY($2) AND deleted_at IS NULL
		ORDER BY kind, id
	`, userID, kinds)
	if err != nil {
		return central.ConfigurationSnapshot{}, fmt.Errorf("list configuration resources: %w", err)
	}
	for rows.Next() {
		var resource central.ConfigurationResource
		if err := rows.Scan(&resource.Kind, &resource.ID, &resource.Data); err != nil {
			rows.Close()
			return central.ConfigurationSnapshot{}, fmt.Errorf("scan configuration resource: %w", err)
		}
		snapshot.Resources = append(snapshot.Resources, resource)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return central.ConfigurationSnapshot{}, fmt.Errorf("iterate configuration resources: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return central.ConfigurationSnapshot{}, fmt.Errorf("commit configuration snapshot: %w", err)
	}
	return snapshot, nil
}

func (store *Store) ReplaceClientConfiguration(
	ctx context.Context,
	replacement central.ConfigurationReplacement,
) (int64, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, fmt.Errorf("begin configuration replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockClientUser(ctx, tx, replacement.UserID); err != nil {
		return 0, err
	}
	commandIdentity := strconv.FormatInt(replacement.ExpectedRevision, 10) + ":" + replacement.PayloadSHA256
	if revision, handled, err := replayConfigurationCommand(ctx, tx, replacement, commandIdentity); handled || err != nil {
		return revision, err
	}
	currentRevision, err := clientConfigurationRevision(ctx, tx, replacement.UserID)
	if err != nil {
		return 0, err
	}
	if currentRevision != replacement.ExpectedRevision {
		return 0, central.ErrRevisionConflict
	}

	if err := replaceConfigurationResources(ctx, tx, replacement); err != nil {
		return 0, err
	}
	resultRevision, err := clientConfigurationRevision(ctx, tx, replacement.UserID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_commands (
			user_id, command_id, operation, resource_kind, resource_id, result_revision, created_at
		) VALUES ($1, $2, 'put', 'configuration', $3, $4, $5)
	`, replacement.UserID, replacement.CommandID, commandIdentity, resultRevision, replacement.Now); err != nil {
		return 0, normalizeConfigurationConflict(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, normalizeConfigurationConflict(err)
	}
	return resultRevision, nil
}

func replaceConfigurationResources(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
) error {
	desired := make(map[string]central.ConfigurationResource, len(replacement.Resources))
	for _, resource := range replacement.Resources {
		desired[configurationResourceKey(resource.Kind, resource.ID)] = resource
	}
	current, err := lockPortableConfigurationResources(ctx, tx, replacement.UserID)
	if err != nil {
		return err
	}
	keys := configurationResourceKeys(current, desired)
	for _, key := range keys {
		stored, rowExists := current[key]
		after, wanted := desired[key]
		if err := replaceConfigurationResource(
			ctx, tx, replacement, stored, rowExists, after, wanted,
		); err != nil {
			return err
		}
	}
	return nil
}

func configurationResourceKeys(
	current map[string]storedConfigurationResource,
	desired map[string]central.ConfigurationResource,
) []string {
	keys := make([]string, 0, len(current)+len(desired))
	for key := range current {
		keys = append(keys, key)
	}
	for key := range desired {
		if _, exists := current[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func replaceConfigurationResource(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
	stored storedConfigurationResource,
	rowExists bool,
	after central.ConfigurationResource,
	wanted bool,
) error {
	before := stored.resource
	existed := rowExists && !stored.deleted
	if existed && wanted && jsonEqualBytes(before.Data, after.Data) {
		return nil
	}
	if wanted {
		return upsertConfigurationResource(ctx, tx, replacement, before, after, !rowExists)
	}
	if existed {
		return deleteConfigurationResource(ctx, tx, replacement, before)
	}
	return nil
}

func upsertConfigurationResource(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
	before central.ClientResource,
	after central.ConfigurationResource,
	create bool,
) error {
	resource := central.ClientResource{
		Kind: after.Kind, ID: after.ID, UserID: replacement.UserID,
		Revision: 1, Data: after.Data, CreatedAt: replacement.Now, UpdatedAt: replacement.Now,
	}
	if !create {
		resource.CreatedAt = before.CreatedAt
		resource.Revision = before.Revision + 1
	}
	if err := writeClientResource(ctx, tx, resource, create, false); err != nil {
		return normalizeConfigurationConflict(err)
	}
	return recordConfigurationEvent(ctx, tx, replacement, "updated", resource)
}

func deleteConfigurationResource(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
	resource central.ClientResource,
) error {
	resource.Revision++
	resource.UpdatedAt = replacement.Now
	if err := writeClientResource(ctx, tx, resource, false, true); err != nil {
		return err
	}
	return recordConfigurationEvent(ctx, tx, replacement, "deleted", resource)
}

func configurationResourceKey(kind string, id string) string {
	return kind + "\x00" + id
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

func clientConfigurationRevision(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM client_events WHERE user_id = $1`, userID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read configuration revision: %w", err)
	}
	return revision, nil
}

func replayConfigurationCommand(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
	identity string,
) (int64, bool, error) {
	var operation, kind, resourceID string
	var revision int64
	err := tx.QueryRow(ctx, `
		SELECT operation, resource_kind, resource_id, result_revision
		FROM client_commands WHERE user_id = $1 AND command_id = $2
	`, replacement.UserID, replacement.CommandID).Scan(&operation, &kind, &resourceID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, true, fmt.Errorf("read configuration command: %w", err)
	}
	if operation != "put" || kind != "configuration" || resourceID != identity {
		return 0, true, central.ErrIdempotencyConflict
	}
	return revision, true, nil
}

func lockPortableConfigurationResources(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
) (map[string]storedConfigurationResource, error) {
	rows, err := tx.Query(ctx, `
		SELECT kind, id, user_id, revision, payload, created_at, updated_at, deleted_at
		FROM client_resources
		WHERE user_id = $1 AND kind = ANY($2)
		ORDER BY kind, id FOR UPDATE
	`, userID, central.PortableConfigurationKinds())
	if err != nil {
		return nil, fmt.Errorf("lock configuration resources: %w", err)
	}
	defer rows.Close()
	resources := make(map[string]storedConfigurationResource)
	for rows.Next() {
		var resource central.ClientResource
		var deletedAt *time.Time
		if err := rows.Scan(
			&resource.Kind, &resource.ID, &resource.UserID, &resource.Revision,
			&resource.Data, &resource.CreatedAt, &resource.UpdatedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan configuration resource: %w", err)
		}
		resources[configurationResourceKey(resource.Kind, resource.ID)] = storedConfigurationResource{
			resource: resource, deleted: deletedAt != nil,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate locked configuration resources: %w", err)
	}
	return resources, nil
}

type storedConfigurationResource struct {
	resource central.ClientResource
	deleted  bool
}

func recordConfigurationEvent(
	ctx context.Context,
	tx pgx.Tx,
	replacement central.ConfigurationReplacement,
	operation string,
	resource central.ClientResource,
) error {
	payload := resource.Data
	if operation == "deleted" {
		payload = json.RawMessage(`{}`)
	}
	eventID := clientEventID(replacement.UserID, replacement.CommandID+"\x00"+operation+"\x00"+resource.Kind+"\x00"+resource.ID)
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_events (
			id, user_id, event_type, resource_kind, resource_id, resource_revision, payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventID, replacement.UserID, resource.Kind+"."+operation+".v1", resource.Kind,
		resource.ID, resource.Revision, payload, replacement.Now); err != nil {
		return fmt.Errorf("record configuration event: %w", err)
	}
	return nil
}

func jsonEqualBytes(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(left, right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func normalizeConfigurationConflict(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && (databaseError.Code == "23505" || databaseError.Code == "40001") {
		return central.ErrRevisionConflict
	}
	return err
}
