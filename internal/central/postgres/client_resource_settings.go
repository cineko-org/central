package postgres

import (
	"context"
	"fmt"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"

	"github.com/jackc/pgx/v5"
)

func loadClientSettings(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var mode *string
	var username, password string
	var hasPassword bool
	if err := queryer.QueryRow(ctx, `
		SELECT network_mode, proxy_username, proxy_password, proxy_has_password
		FROM client_settings WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(&mode, &username, &password, &hasPassword); err != nil {
		return nil, fmt.Errorf("read normalized Client settings: %w", err)
	}
	network := &clientpb.NetworkSettings{}
	switch {
	case mode == nil:
	case *mode == "direct":
		network.SetDirect(&clientpb.DirectNetwork{})
	case *mode == "proxy":
		proxy := &clientpb.ProxyNetwork{}
		proxy.SetUsername(username)
		proxy.SetPassword(password)
		proxy.SetHasPassword(hasPassword)
		urls, err := loadOrderedStrings(ctx, queryer, `
			SELECT url FROM client_setting_proxy_urls
			WHERE user_id = $1 AND settings_id = $2 ORDER BY position
		`, userID, id)
		if err != nil {
			return nil, fmt.Errorf("read Client proxy URLs: %w", err)
		}
		proxy.SetUrls(urls)
		network.SetProxy(proxy)
	default:
		return nil, fmt.Errorf("unknown normalized Client network mode %q", *mode)
	}
	settings := &clientpb.Settings{}
	if mode != nil {
		settings.SetNetwork(network)
	}
	rows, err := queryer.Query(ctx, `
		SELECT position, id, name, url, secret, enabled, has_secret
		FROM client_setting_webhooks
		WHERE user_id = $1 AND settings_id = $2 ORDER BY position
	`, userID, id)
	if err != nil {
		return nil, fmt.Errorf("list Client setting webhooks: %w", err)
	}
	type webhookRecord struct {
		position int32
		value    *clientpb.WebhookTarget
	}
	records := make([]webhookRecord, 0)
	for rows.Next() {
		var record webhookRecord
		var webhookID, name, url, secret string
		var enabled, hasSecret bool
		if err := rows.Scan(&record.position, &webhookID, &name, &url, &secret, &enabled, &hasSecret); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan Client setting webhook: %w", err)
		}
		record.value = &clientpb.WebhookTarget{}
		record.value.SetId(webhookID)
		record.value.SetName(name)
		record.value.SetUrl(url)
		record.value.SetSecret(secret)
		record.value.SetEnabled(enabled)
		record.value.SetHasSecret(hasSecret)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate Client setting webhooks: %w", err)
	}
	rows.Close()
	webhooks := make([]*clientpb.WebhookTarget, 0, len(records))
	for _, record := range records {
		eventKinds, err := loadOrderedStrings(ctx, queryer, `
			SELECT event_kind FROM client_setting_webhook_event_kinds
			WHERE user_id = $1 AND settings_id = $2 AND webhook_position = $3
			ORDER BY position
		`, userID, id, record.position)
		if err != nil {
			return nil, fmt.Errorf("read Client webhook event kinds: %w", err)
		}
		record.value.SetEventKinds(eventKinds)
		webhooks = append(webhooks, record.value)
	}
	settings.SetWebhooks(webhooks)
	resource := &clientpb.Resource{}
	resource.SetSettings(settings)
	return resource, nil
}

func writeClientSettings(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	settings := resource.body.GetSettings()
	if settings == nil {
		return fmt.Errorf("client settings are required")
	}
	mode, username, password, hasPassword, err := clientSettingsValues(settings)
	if err != nil {
		return err
	}
	if err := upsertClientSettings(ctx, tx, resource, mode, username, password, hasPassword); err != nil {
		return err
	}
	if err := replaceClientProxyURLs(ctx, tx, resource, settings, mode); err != nil {
		return err
	}
	return replaceClientSettingWebhooks(ctx, tx, resource, settings)
}

func clientSettingsValues(settings *clientpb.Settings) (any, string, string, bool, error) {
	var mode any
	username, password := "", ""
	hasPassword := false
	switch {
	case settings.GetNetwork() == nil:
		mode = nil
	case settings.GetNetwork().HasDirect():
		mode = "direct"
	case settings.GetNetwork().HasProxy():
		mode = "proxy"
		proxy := settings.GetNetwork().GetProxy()
		username = proxy.GetUsername()
		password = proxy.GetPassword()
		hasPassword = proxy.GetHasPassword()
	default:
		return nil, "", "", false, fmt.Errorf("client settings network mode is required")
	}
	return mode, username, password, hasPassword, nil
}

func upsertClientSettings(
	ctx context.Context,
	tx pgx.Tx,
	resource storedClientResource,
	mode any,
	username, password string,
	hasPassword bool,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_settings (
			user_id, resource_kind, id, network_mode, proxy_username, proxy_password, proxy_has_password
		) VALUES ($1, 'settings', $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, id) DO UPDATE SET
			network_mode = EXCLUDED.network_mode,
			proxy_username = EXCLUDED.proxy_username,
			proxy_password = EXCLUDED.proxy_password,
			proxy_has_password = EXCLUDED.proxy_has_password
	`, resource.userID, resource.id, mode, username, password, hasPassword); err != nil {
		return fmt.Errorf("write normalized client settings: %w", err)
	}
	return nil
}

func replaceClientProxyURLs(
	ctx context.Context,
	tx pgx.Tx,
	resource storedClientResource,
	settings *clientpb.Settings,
	mode any,
) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_setting_proxy_urls WHERE user_id = $1 AND settings_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear Client proxy URLs: %w", err)
	}
	if mode == "proxy" {
		for position, url := range settings.GetNetwork().GetProxy().GetUrls() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO client_setting_proxy_urls (user_id, settings_id, position, url)
				VALUES ($1, $2, $3, $4)
			`, resource.userID, resource.id, position, url); err != nil {
				return fmt.Errorf("write Client proxy URL: %w", err)
			}
		}
	}
	return nil
}

func replaceClientSettingWebhooks(
	ctx context.Context,
	tx pgx.Tx,
	resource storedClientResource,
	settings *clientpb.Settings,
) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_setting_webhooks WHERE user_id = $1 AND settings_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear Client setting webhooks: %w", err)
	}
	for webhookPosition, webhook := range settings.GetWebhooks() {
		if webhook == nil {
			return fmt.Errorf("client setting webhook %d is required", webhookPosition)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_setting_webhooks (
				user_id, settings_id, position, id, name, url, secret, enabled, has_secret
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, resource.userID, resource.id, webhookPosition, webhook.GetId(), webhook.GetName(),
			webhook.GetUrl(), webhook.GetSecret(), webhook.GetEnabled(), webhook.GetHasSecret()); err != nil {
			return fmt.Errorf("write client setting webhook: %w", err)
		}
		for eventPosition, eventKind := range webhook.GetEventKinds() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO client_setting_webhook_event_kinds (
					user_id, settings_id, webhook_position, position, event_kind
				) VALUES ($1, $2, $3, $4, $5)
			`, resource.userID, resource.id, webhookPosition, eventPosition, eventKind); err != nil {
				return fmt.Errorf("write Client webhook event kind: %w", err)
			}
		}
	}
	return nil
}

func loadOrderedStrings(
	ctx context.Context,
	queryer clientResourceQueryer,
	query string,
	arguments ...any,
) ([]string, error) {
	rows, err := queryer.Query(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
