package postgres

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/bootstrap"
	"github.com/cineko-org/central/internal/central/reconcile"
	"github.com/cineko-org/central/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestPostgresClientPlaneLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		userID         = "user_client_plane_integration"
		installationID = "install_client_plane_integration"
		accessToken    = "0123456789abcdef0123456789abcdef"
	)
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM client_events WHERE user_id = $1`,
			`DELETE FROM client_commands WHERE user_id = $1`,
			`DELETE FROM client_resources WHERE user_id = $1`,
			`DELETE FROM client_launch_tickets WHERE user_id = $1`,
			`DELETE FROM client_devices WHERE user_id = $1`,
			`DELETE FROM client_sessions WHERE user_id = $1`,
			`DELETE FROM client_credentials WHERE user_id = $1`,
			`DELETE FROM client_users WHERE id = $1`,
		} {
			if _, cleanupErr := store.pool.Exec(context.Background(), statement, userID); cleanupErr != nil {
				t.Errorf("client plane cleanup: %v", cleanupErr)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	service, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	release := central.ClientRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0",
		MinimumLauncherVersion: "1.0.0", MinimumBrowserRevision: "1234",
		PlaywrightVersion: "1.61.1", Protocol: central.ProtocolVersion,
		Artifact: central.ReleaseArtifact{
			URL: "https://download.example/client.zip", Size: 1,
			SHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "Cineko.app/Contents/MacOS/Cineko",
		},
		ProbeBootstrapPublicKeys: map[string]string{
			"primary": "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----\n",
		},
		PublishedAt: time.Now().UTC(),
	}
	browserRelease := central.BrowserRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Revision: "1234",
		CompatiblePlaywrightVersions: []string{"1.61.1"},
		Artifact: central.ReleaseArtifact{
			URL: "https://download.example/browser.zip", Size: 1,
			SHA256:     "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "chromium/Chromium",
		},
		PublishedAt: time.Now().UTC(),
	}
	playwrightRelease := central.PlaywrightRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.61.1",
		Artifact: central.ReleaseArtifact{
			URL: "https://download.example/driver.zip", Size: 1,
			SHA256:     "2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "driver/playwright",
		},
		PublishedAt: time.Now().UTC(),
	}
	launcherRelease := central.LauncherRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0",
		Protocol: central.ProtocolVersion,
		Launcher: central.ReleaseArtifact{
			URL: "https://download.example/launcher.zip", Size: 1,
			SHA256: strings.Repeat("3", 64), Executable: "cineko-launcher",
		},
		PublishedAt: time.Now().UTC(),
	}
	clientReleases := make([]central.ClientRelease, 1, 3)
	clientReleases[0] = release
	browserReleases := make([]central.BrowserRelease, 1, 3)
	browserReleases[0] = browserRelease
	playwrightReleases := make([]central.PlaywrightRelease, 1, 3)
	playwrightReleases[0] = playwrightRelease
	launcherReleases := make([]central.LauncherRelease, 1, 3)
	launcherReleases[0] = launcherRelease
	for _, target := range []struct {
		platform     string
		architecture string
		executable   string
	}{
		{platform: "linux", architecture: "amd64", executable: "cineko-client"},
		{platform: "windows", architecture: "amd64", executable: "cineko-client.exe"},
	} {
		clientTarget := release
		clientTarget.Platform, clientTarget.Arch = target.platform, target.architecture
		clientTarget.Artifact.URL = "https://download.example/" + target.platform + "/client.zip"
		clientTarget.Artifact.Executable = target.executable
		clientReleases = append(clientReleases, clientTarget)

		browserTarget := browserRelease
		browserTarget.Platform, browserTarget.Arch = target.platform, target.architecture
		browserTarget.Artifact.URL = "https://download.example/" + target.platform + "/browser.zip"
		browserTarget.Artifact.Executable = target.executable
		browserReleases = append(browserReleases, browserTarget)

		playwrightTarget := playwrightRelease
		playwrightTarget.Platform, playwrightTarget.Arch = target.platform, target.architecture
		playwrightTarget.Artifact.URL = "https://download.example/" + target.platform + "/driver.zip"
		playwrightTarget.Artifact.Executable = target.executable
		playwrightReleases = append(playwrightReleases, playwrightTarget)

		launcherTarget := launcherRelease
		launcherTarget.Platform, launcherTarget.Arch = target.platform, target.architecture
		launcherTarget.Launcher.URL = "https://download.example/" + target.platform + "/launcher.zip"
		launcherTarget.Launcher.Executable = target.executable
		launcherReleases = append(launcherReleases, launcherTarget)
	}
	if err := service.BootstrapReleaseRegistry(
		ctx,
		clientReleases,
		browserReleases,
		playwrightReleases,
		launcherReleases,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.Provision(ctx, []central.ClientCredentialSeed{{
		UserID: userID, DisplayName: "Integration User", AccessToken: accessToken,
	}}); err != nil {
		t.Fatal(err)
	}
	auth, err := service.Exchange(ctx, central.AuthExchangeRequest{UserID: userID, AccessToken: accessToken})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, auth.AccessToken)
	if err != nil || principal.UserID != userID {
		t.Fatalf("authenticated client = %+v, %v", principal, err)
	}
	refreshed, err := service.Refresh(ctx, central.AuthRefreshRequest{RefreshToken: auth.RefreshToken})
	if err != nil || refreshed.User.ID != userID || refreshed.AccessToken == auth.AccessToken ||
		refreshed.RefreshToken == auth.RefreshToken {
		t.Fatalf("refreshed client session = %+v, %v", refreshed, err)
	}
	if _, err := service.Authenticate(ctx, auth.AccessToken); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("revoked access token error = %v", err)
	}
	if _, err := service.Refresh(ctx, central.AuthRefreshRequest{RefreshToken: auth.RefreshToken}); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("replayed refresh token error = %v", err)
	}
	principal, err = service.Authenticate(ctx, refreshed.AccessToken)
	if err != nil || principal.UserID != userID {
		t.Fatalf("authenticated refreshed client = %+v, %v", principal, err)
	}
	device, err := service.UpsertDevice(ctx, principal, central.ClientDevice{
		InstallationID: installationID, DeviceID: "device_integration", Platform: "darwin",
		Arch: "arm64", AppVersion: "1.0.0",
	})
	if err != nil || device.UserID != userID {
		t.Fatalf("client device = %+v, %v", device, err)
	}
	launch, err := service.IssueLaunchTicket(ctx, principal, central.LaunchTicketRequest{
		InstallationID: installationID, DeviceID: device.DeviceID,
		ReleaseGeneration: service.ReleaseGeneration(), ClientVersion: release.Version,
		ArtifactSHA256: release.Artifact.SHA256, Protocol: release.Protocol,
		BrowserRevision: browserRelease.Revision, BrowserArtifactSHA256: browserRelease.Artifact.SHA256,
		PlaywrightVersion:        playwrightRelease.Version,
		PlaywrightArtifactSHA256: playwrightRelease.Artifact.SHA256, Nonce: "launcher_nonce_integration",
	})
	if err != nil || launch.LaunchTicket == "" {
		t.Fatalf("launch ticket = %+v, %v", launch, err)
	}
	launched, err := service.ExchangeLaunchTicket(ctx, central.ClientSessionExchangeRequest{
		LaunchTicket: launch.LaunchTicket, ClientNonce: "client_nonce_integration",
	})
	if err != nil || launched.User.ID != userID {
		t.Fatalf("launched client session = %+v, %v", launched, err)
	}
	if _, err := service.ExchangeLaunchTicket(ctx, central.ClientSessionExchangeRequest{
		LaunchTicket: launch.LaunchTicket, ClientNonce: "client_nonce_replay",
	}); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("replayed launch ticket error = %v", err)
	}
	clientBootstrap, err := service.Bootstrap(ctx, principal, installationID)
	if err != nil || clientBootstrap.Device == nil || clientBootstrap.User.ID != userID {
		t.Fatalf("client bootstrap = %+v, %v", clientBootstrap, err)
	}

	presetPayload := json.RawMessage(`{"id":"preset_integration","userId":"` + userID + `","name":"IMAX","theaterId":"0013","auditoriumId":"imax","seatCount":1,"seatPreference":{}}`)
	created, err := service.PutResource(
		ctx, principal, "presets", "preset_integration", presetPayload, nil, "create_preset",
	)
	if err != nil || created.Revision != 1 {
		t.Fatalf("create client resource = %+v, %v", created, err)
	}
	replayed, err := service.PutResource(
		ctx, principal, "presets", "preset_integration", json.RawMessage(`{"id":"preset_integration","userId":"`+userID+`","name":"Ignored","theaterId":"0013","auditoriumId":"imax","seatCount":1,"seatPreference":{}}`), nil, "create_preset",
	)
	var replayedData struct {
		Name string `json:"name"`
	}
	decodeErr := json.Unmarshal(replayed.Data, &replayedData)
	if err != nil || decodeErr != nil || replayed.Revision != created.Revision || replayedData.Name != "IMAX" {
		t.Fatalf("replay client resource = %+v, %v", replayed, err)
	}
	if _, err := service.PutResource(
		ctx, principal, "monitors", "other", json.RawMessage(`{"id":"other","userId":"`+userID+`","presetId":"preset_integration","movie":"Movie","targetDates":["2026-08-20"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"pending"}`), nil, "create_preset",
	); !errors.Is(err, central.ErrIdempotencyConflict) {
		t.Fatalf("reused client command error = %v", err)
	}
	revision := created.Revision
	updated, err := service.PutResource(
		ctx, principal, "presets", created.ID, json.RawMessage(`{"id":"preset_integration","userId":"`+userID+`","name":"IMAX center","theaterId":"0013","auditoriumId":"imax","seatCount":1,"seatPreference":{}}`), &revision, "update_preset",
	)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update client resource = %+v, %v", updated, err)
	}
	if _, err := service.PutResource(
		ctx, principal, "presets", created.ID, json.RawMessage(`{"id":"preset_integration","userId":"`+userID+`","name":"Stale","theaterId":"0013","auditoriumId":"imax","seatCount":1,"seatPreference":{}}`), &revision, "stale_update",
	); !errors.Is(err, central.ErrRevisionConflict) {
		t.Fatalf("stale client revision error = %v", err)
	}
	resources, err := service.ListResources(ctx, principal, "presets")
	if err != nil || len(resources) != 1 || resources[0].Revision != 2 {
		t.Fatalf("list client resources = %+v, %v", resources, err)
	}
	events, err := service.Events(ctx, principal, 0, 10)
	if err != nil || len(events) != 2 || events[0].Sequence >= events[1].Sequence {
		t.Fatalf("client events = %+v, %v", events, err)
	}
	concurrentCreateErrors := make(chan error, 2)
	var concurrentCreates sync.WaitGroup
	for index, payload := range []string{`{"source":"a"}`, `{"source":"b"}`} {
		concurrentCreates.Add(1)
		go func(index int, payload string) {
			defer concurrentCreates.Done()
			_, createErr := service.PutResource(
				ctx, principal, "settings", "settings", json.RawMessage(payload), nil,
				fmt.Sprintf("create_concurrent_settings_%d", index),
			)
			concurrentCreateErrors <- createErr
		}(index, payload)
	}
	concurrentCreates.Wait()
	close(concurrentCreateErrors)
	createdCount, conflictCount := 0, 0
	for createErr := range concurrentCreateErrors {
		switch {
		case createErr == nil:
			createdCount++
		case errors.Is(createErr, central.ErrRevisionConflict):
			conflictCount++
		default:
			t.Fatalf("concurrent create error = %v", createErr)
		}
	}
	if createdCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent create results = created %d, conflicts %d", createdCount, conflictCount)
	}
	revision = updated.Revision
	deleted, err := service.DeleteResource(ctx, principal, "presets", created.ID, &revision, "delete_preset")
	if err != nil || deleted.Revision != 3 {
		t.Fatalf("delete client resource = %+v, %v", deleted, err)
	}
	if _, err := service.DeleteResource(ctx, principal, "presets", created.ID, &revision, "delete_preset"); err != nil {
		t.Fatalf("replay client resource deletion = %v", err)
	}
	if _, err := service.GetResource(ctx, principal, "presets", created.ID); !errors.Is(err, central.ErrNotFound) {
		t.Fatalf("get deleted client resource error = %v", err)
	}
}

func TestPostgresAdminSessionLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	now := time.Now().UTC()
	_, _ = store.pool.Exec(ctx, `DELETE FROM admin_credentials`)
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM admin_credentials`)
	})
	credential := central.AdminCredential{
		UserID: "integration-admin", DisplayName: "Integration Admin",
		PasswordHash: strings.Repeat("x", 96),
	}
	if err := store.BootstrapAdminCredentials(ctx, []central.AdminCredential{credential}); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapAdminCredentials(ctx, []central.AdminCredential{{
		UserID: "ignored-admin", DisplayName: "Ignored", PasswordHash: credential.PasswordHash,
	}}); err != nil {
		t.Fatal(err)
	}
	loadedCredential, err := store.FindAdminCredential(ctx, credential.UserID)
	if err != nil || loadedCredential.DisplayName != credential.DisplayName {
		t.Fatalf("admin credential = %+v, %v", loadedCredential, err)
	}
	if _, err := store.FindAdminCredential(ctx, "ignored-admin"); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("ignored bootstrap credential error = %v", err)
	}
	tokenHash := sha256.Sum256([]byte("admin-session-integration"))
	_, _ = store.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash[:])
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash[:])
	})
	session := central.AdminSession{
		TokenHash: tokenHash, UserID: "integration-admin", DisplayName: "Integration Admin",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := store.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.AuthenticateAdminSession(ctx, tokenHash, now)
	if err != nil || loaded.UserID != session.UserID || loaded.DisplayName != session.DisplayName {
		t.Fatalf("admin session = %+v, %v", loaded, err)
	}
	if err := store.RevokeAdminSession(ctx, tokenHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAdminSession(ctx, tokenHash, now.Add(time.Minute)); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("revoked admin session error = %v", err)
	}
}

func TestPostgresAdminProbeDeletion(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		onlineID     = "probe_admin_delete_online"
		offlineID    = "probe_admin_delete_offline"
		historyID    = "probe_admin_delete_history"
		assignmentID = "assignment_admin_delete_history"
	)
	probeIDs := []string{onlineID, offlineID, historyID}
	cleanup := func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM assignment_attempts WHERE assignment_id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM observation_assignments WHERE id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM probe_runtimes WHERE id = ANY($1)`, probeIDs)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, probeID := range probeIDs {
		registerIntegrationProbe(t, store, probeID, "install_"+probeID, now)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE probe_runtimes SET status = 'offline', available_slots = 0
		WHERE id = ANY($1)
	`, []string{offlineID, historyID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, not_before, deadline, finished_at, created_at, updated_at
		) VALUES (
			$1, 'cgv.schedule.capture.v2', 'theater_admin_delete', 'cgv', 'admin-delete',
			'서울', '관리 시험관', ARRAY['2026-08-20'::date], 'ko-KR', 'Asia/Seoul', 'scan_default',
			'completed', $2, $3, $3, $2, $3
		)
	`, assignmentID, now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO assignment_attempts (
			assignment_id, probe_id, attempt, started_at, finished_at, status, network_id
		) VALUES ($1, $2, 1, $3, $4, 'completed', $5)
	`, assignmentID, historyID, now.Add(-time.Minute), now, "net_"+historyID); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteAdminProbe(ctx, onlineID); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("delete online Probe = %v", err)
	}
	if err := store.DeleteAdminProbe(ctx, historyID); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("delete Probe with history = %v", err)
	}
	if err := store.DeleteAdminProbe(ctx, offlineID); err != nil {
		t.Fatalf("delete offline Probe: %v", err)
	}
	if err := store.DeleteAdminProbe(ctx, offlineID); !errors.Is(err, central.ErrNotFound) {
		t.Fatalf("delete missing Probe = %v", err)
	}
}

func TestPostgresClientPINLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	_, _ = store.pool.Exec(ctx, `DELETE FROM client_pin_attempts`)
	clientService, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pinService, err := central.NewPINService(store, clientService, "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := pinService.CreateUser(ctx, "PIN Integration User")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_users WHERE id = $1`, issue.User.ID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_pin_attempts`)
	})
	users, err := pinService.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, user := range users {
		found = found || user.User.ID == issue.User.ID && user.PINActive
	}
	if !found {
		t.Fatalf("created PIN user missing from %+v", users)
	}
	request := central.ClientPINExchangeRequest{
		PIN: issue.PIN, InstallationID: "install_pin_integration", DeviceID: "device_pin_integration",
	}
	auth, err := pinService.Exchange(ctx, request, "198.51.100.40")
	if err != nil || auth.User.ID != issue.User.ID {
		t.Fatalf("PIN exchange = %+v, %v", auth, err)
	}
	principal, err := clientService.Authenticate(ctx, auth.AccessToken)
	if err != nil || principal.UserID != issue.User.ID {
		t.Fatalf("PIN session principal = %+v, %v", principal, err)
	}
	rotated, err := pinService.Rotate(ctx, issue.User.ID)
	if err != nil || rotated.PIN == issue.PIN {
		t.Fatalf("rotated PIN = %+v, %v", rotated, err)
	}
	if _, err := pinService.Exchange(ctx, request, "198.51.100.41"); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("old PIN exchange error = %v", err)
	}
	request.PIN = rotated.PIN
	if _, err := pinService.Exchange(ctx, request, "198.51.100.41"); err != nil {
		t.Fatalf("rotated PIN exchange = %v", err)
	}
	request.PIN = "999999"
	for attempt := 1; attempt <= central.ClientPINFailureLimit; attempt++ {
		_, err := pinService.Exchange(ctx, request, "198.51.100.42")
		if attempt < central.ClientPINFailureLimit && !errors.Is(err, central.ErrUnauthorized) {
			t.Fatalf("failed PIN attempt %d = %v", attempt, err)
		}
		if attempt == central.ClientPINFailureLimit && !errors.Is(err, central.ErrRateLimited) {
			t.Fatalf("rate-limited PIN attempt = %v", err)
		}
	}
	request.PIN = rotated.PIN
	if _, err := pinService.Exchange(ctx, request, "198.51.100.42"); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("blocked correct PIN exchange = %v", err)
	}
	request.InstallationID = "install_pin_persistent_source"
	request.DeviceID = "device_pin_persistent_source"
	request.PIN = "999999"
	for attempt := 1; attempt < central.ClientPINFailureLimit; attempt++ {
		if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); !errors.Is(err, central.ErrUnauthorized) {
			t.Fatalf("persistent source failed PIN attempt %d = %v", attempt, err)
		}
	}
	request.PIN = rotated.PIN
	if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); err != nil {
		t.Fatalf("valid PIN before source limit = %v", err)
	}
	request.InstallationID = "install_pin_rotated_device"
	request.DeviceID = "device_pin_rotated_device"
	request.PIN = "999999"
	if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("device rotation reset source-wide PIN limit: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO client_resources (
			user_id, kind, id, revision, payload, created_at, updated_at
		) VALUES ($1, 'monitors', 'monitor_delete_with_user', 1, '{}'::jsonb, now(), now())
	`, issue.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := pinService.DeleteUser(ctx, issue.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := clientService.Authenticate(ctx, auth.AccessToken); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("deleted user session error = %v", err)
	}
	var userCount, monitorCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM client_users WHERE id = $1`, issue.User.ID).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM client_resources
		WHERE user_id = $1 AND kind = 'monitors'
	`, issue.User.ID).Scan(&monitorCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 || monitorCount != 0 {
		t.Fatalf("deleted user data remains: users=%d monitors=%d", userCount, monitorCount)
	}
	if err := pinService.DeleteUser(ctx, issue.User.ID); !errors.Is(err, central.ErrNotFound) {
		t.Fatalf("delete missing user = %v", err)
	}
}

func TestPostgresAvailabilityExecutionLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const (
		userID      = "user_execution_integration"
		accessToken = "execution-client-token-0123456789abcdef"
	)
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM client_execution_commands WHERE user_id = $1`,
			`DELETE FROM client_events WHERE user_id = $1`,
			`DELETE FROM client_commands WHERE user_id = $1`,
			`DELETE FROM client_resources WHERE user_id = $1`,
			`DELETE FROM client_launch_tickets WHERE user_id = $1`,
			`DELETE FROM client_sessions WHERE user_id = $1`,
			`DELETE FROM client_devices WHERE user_id = $1`,
			`DELETE FROM client_credentials WHERE user_id = $1`,
			`DELETE FROM client_users WHERE id = $1`,
		} {
			if _, cleanupErr := store.pool.Exec(context.Background(), statement, userID); cleanupErr != nil {
				t.Errorf("execution cleanup: %v", cleanupErr)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	service, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Provision(ctx, []central.ClientCredentialSeed{{
		UserID: userID, DisplayName: "Execution User", AccessToken: accessToken,
	}}); err != nil {
		t.Fatal(err)
	}
	auth, err := service.Exchange(ctx, central.AuthExchangeRequest{UserID: userID, AccessToken: accessToken})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, auth.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range []central.ClientDevice{
		{InstallationID: "execution_install_a", DeviceID: "execution_device_a", Platform: "darwin", Arch: "arm64", AppVersion: "1.0.0"},
		{InstallationID: "execution_install_b", DeviceID: "execution_device_b", Platform: "darwin", Arch: "arm64", AppVersion: "1.0.0"},
	} {
		if _, err := service.UpsertDevice(ctx, principal, device); err != nil {
			t.Fatal(err)
		}
	}
	preset := domain.Preset{
		ID: "execution_preset", UserID: userID, Name: "IMAX", TheaterID: "0013",
		AuditoriumID: "imax", SeatCount: 2,
	}
	monitor := domain.MonitorJob{
		ID: "execution_monitor", UserID: userID, PresetID: preset.ID,
		Mode: domain.MonitorModeOpening, Movie: "Execution Movie",
		TargetDates: []string{"2026-08-20"}, EarliestTime: "18:00", LatestTime: "22:00",
		PollInterval: 2 * time.Second, PollIntervalMax: 3 * time.Second, Status: domain.MonitorPending,
	}
	for _, resource := range []struct {
		kind string
		id   string
		data any
	}{{"presets", preset.ID, preset}, {"monitors", monitor.ID, monitor}} {
		payload, marshalErr := json.Marshal(resource.data)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, err := service.PutResource(
			ctx, principal, resource.kind, resource.id, payload, nil, "create_"+resource.id,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES
			($1, 'presets', 'corrupt_execution_preset', 1,
			 '{"id":"corrupt_execution_preset","userId":"foreign_user","name":"Poison","theaterId":"0013","auditoriumId":"imax","seatCount":1,"seatPreference":{}}', now(), now()),
			($1, 'monitors', 'corrupt_execution_monitor', 1,
			 '{"id":"corrupt_execution_monitor","userId":"foreign_user","presetId":"corrupt_execution_preset","movie":"Execution Movie","targetDates":["2026-08-20"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"pending"}', now(), now())
	`, userID); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	showtime := central.Showtime{
		ID: "show_execution", Movie: central.Movie{Title: monitor.Movie},
		Auditorium:     central.Auditorium{ID: preset.AuditoriumID, Name: "IMAX관"},
		StartsAt:       time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
		EndsAt:         time.Date(2026, 8, 20, 12, 30, 0, 0, time.UTC),
		AvailableSeats: 300, Capacity: 624,
	}
	commit := central.ResultCommit{
		CommittedAt: observedAt,
		Result: central.AssignmentResult{Captures: []central.Capture{{
			TargetDate: "2026-08-20", Complete: true, ObservedAt: observedAt,
			Showtimes: []central.Showtime{showtime},
		}}},
	}
	for range 2 {
		tx, beginErr := store.pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := enqueueClientExecutions(ctx, tx, commit, preset.TheaterID, "Asia/Seoul"); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var queuedCommands int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM client_execution_commands WHERE user_id = $1
	`, userID).Scan(&queuedCommands); err != nil || queuedCommands != 1 {
		t.Fatalf("queued commands after corrupt target quarantine = %d, %v", queuedCommands, err)
	}
	first, err := service.ClaimExecution(ctx, principal, central.ExecutionClaimRequest{
		InstallationID: "execution_install_a",
	})
	if err != nil || first == nil || first.MonitorID != monitor.ID || first.Payload.Showtime.ID != showtime.ID {
		t.Fatalf("first execution claim = %+v, %v", first, err)
	}
	if empty, err := service.ClaimExecution(ctx, principal, central.ExecutionClaimRequest{
		InstallationID: "execution_install_b",
	}); err != nil || empty != nil {
		t.Fatalf("concurrent execution claim = %+v, %v", empty, err)
	}
	if _, err := service.HeartbeatExecution(ctx, principal, first.ID, central.ExecutionHeartbeatRequest{
		LeaseToken: first.LeaseToken,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteExecution(ctx, principal, first.ID, central.ExecutionResultRequest{
		LeaseToken: first.LeaseToken, Status: "failed", ReasonCode: "test_retry",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := service.ClaimExecution(ctx, principal, central.ExecutionClaimRequest{
		InstallationID: "execution_install_b",
	})
	if err != nil || second == nil || second.Attempt != 2 || second.InstallationID != "execution_install_b" {
		t.Fatalf("second execution claim = %+v, %v", second, err)
	}
	if err := service.CompleteExecution(ctx, principal, second.ID, central.ExecutionResultRequest{
		LeaseToken: second.LeaseToken, Status: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteExecution(ctx, principal, second.ID, central.ExecutionResultRequest{
		LeaseToken: second.LeaseToken, Status: "completed",
	}); !errors.Is(err, central.ErrLeaseExpired) {
		t.Fatalf("replayed execution completion error = %v", err)
	}
	if empty, err := service.ClaimExecution(ctx, principal, central.ExecutionClaimRequest{
		InstallationID: "execution_install_a",
	}); err != nil || empty != nil {
		t.Fatalf("terminal execution claim = %+v, %v", empty, err)
	}
}

func TestPostgresClientProbeBootstrapLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const (
		installationID = "install_client_integration"
		deviceID       = "device_client_integration"
		userID         = "user_client_integration"
		ticketID       = "ticket_client_integration"
	)
	cleanup := func() {
		if _, cleanupErr := store.pool.Exec(
			context.Background(), `DELETE FROM probe_runtimes WHERE installation_id = $1`, installationID,
		); cleanupErr != nil {
			t.Errorf("client Probe cleanup: %v", cleanupErr)
		}
		if _, cleanupErr := store.pool.Exec(
			context.Background(), `DELETE FROM consumed_probe_bootstrap_tickets WHERE ticket_id = $1`, ticketID,
		); cleanupErr != nil {
			t.Errorf("client bootstrap cleanup: %v", cleanupErr)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := bootstrap.NewSigner("cineko-central", "cineko-probe", "integration", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := bootstrap.NewVerifier(
		"cineko-central", "cineko-probe", map[string]*ecdsa.PublicKey{"integration": &privateKey.PublicKey},
		15*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := central.NewService(store, central.Config{
		EnrollmentToken: "container-only", ClientAuthorizer: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := central.RegisterProbeRequest{
		InstallationID: installationID, Kind: "client", Capabilities: []string{"cgv.schedule.capture.v2"},
		MaxConcurrency: 1,
		Runtime: central.Runtime{
			Version: "1.0.0", Protocol: central.ProtocolVersion, BrowserRevision: "1228",
			Platform: "darwin", Arch: "arm64",
		},
	}
	ticket, err := signer.Issue(bootstrap.Claims{
		UserID: userID, TicketID: ticketID, InstallationID: installationID, DeviceID: deviceID,
		Kind: "client", Capabilities: registration.Capabilities,
		MaxConcurrency: 1, Runtime: registration.Runtime,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterProbe(ctx, registration, "203.0.113.10:443", ticket)
	if err != nil {
		t.Fatal(err)
	}
	var storedUserID, storedDeviceID string
	if err := store.pool.QueryRow(ctx, `
		SELECT owner_user_id, device_id FROM probe_runtimes WHERE id = $1
	`, registered.ProbeID).Scan(&storedUserID, &storedDeviceID); err != nil {
		t.Fatal(err)
	}
	if storedUserID != userID || storedDeviceID != deviceID {
		t.Fatalf("stored client identity = %q, %q", storedUserID, storedDeviceID)
	}
	if _, err := service.RegisterProbe(
		ctx, registration, "203.0.113.10:443", ticket,
	); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("replayed client bootstrap ticket error = %v", err)
	}
}

func TestPostgresProbeLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := Migrate(ctx, store.pool); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	probeID := "probe_integration"
	installationID := "install_integration"
	assignmentID := "assignment_integration"
	accessToken := "cpt_integration"
	accessHash := sha256.Sum256([]byte(accessToken))
	leaseToken := "lease_integration"
	leaseHash := sha256.Sum256([]byte(leaseToken))
	theaterSourceKey := "theater_integration"
	theaterID := contracts.CatalogID(contracts.ProviderCGV, "theater", theaterSourceKey)

	cleanupIntegrationRows(t, store, probeID, assignmentID)
	t.Cleanup(func() { cleanupIntegrationRows(t, store, probeID, assignmentID) })

	registered, err := store.RegisterProbe(ctx, central.Probe{
		ID: probeID, InstallationID: installationID, Kind: "container", NetworkID: "net_integration",
		Capabilities: []string{"cgv.schedule.capture.v2"}, MaxConcurrency: 1,
		Runtime: central.Runtime{
			Version: "integration", Protocol: central.ProtocolVersion, BrowserRevision: "integration",
			Platform: "linux", Arch: "amd64",
		},
		TokenHash: accessHash, TokenExpiresAt: now.Add(time.Hour), Status: "online", Health: "healthy",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if registered.ID != probeID {
		t.Fatalf("registered probe id = %q", registered.ID)
	}
	probe, err := store.AuthenticateProbe(ctx, probeID, accessHash, now)
	if err != nil {
		t.Fatal(err)
	}
	if probe.InstallationID != installationID {
		t.Fatalf("authenticated installation = %q", probe.InstallationID)
	}
	if _, err := store.HeartbeatProbe(ctx, probeID, central.ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, status, not_before, deadline, created_at, updated_at
		) VALUES ($1, 'cgv.schedule.capture.v2', $2, $3, $4, '서울', '통합 시험관',
			ARRAY['2026-08-20'::date], 'ko-KR', 'Asia/Seoul', 'scan_default', 'queued', $5, $6, $5, $5)
	`, assignmentID, theaterID, contracts.ProviderCGV, theaterSourceKey,
		now.Add(-time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO assignment_eligible_probes (assignment_id, probe_id, network_id, eligible_at)
		VALUES ($1, $2, 'net_integration', $3)
	`, assignmentID, probeID, now); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.ClaimAssignment(
		ctx, probeID, leaseHash, now, now.Add(time.Minute), now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ID != assignmentID || assignment.Task.Theater.ID != theaterID {
		t.Fatalf("claimed assignment = %+v", assignment)
	}
	if err := store.HeartbeatAssignment(
		ctx, assignmentID, probeID, leaseHash, now.Add(time.Second), now.Add(2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}

	result := central.AssignmentResult{
		RunID: "run_integration", Status: "completed", StartedAt: now, FinishedAt: now.Add(10 * time.Second),
		Captures: []central.Capture{{
			TargetDate: "2026-08-20", Complete: true, ObservedAt: now.Add(9 * time.Second),
			Showtimes: []central.Showtime{integrationShowtime(assignment.Task.Theater, now)},
		}},
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest := sha256.Sum256(payload)
	commit := central.ResultCommit{
		AssignmentID: assignmentID, ProbeID: probeID, LeaseHash: leaseHash,
		PayloadHash: hex.EncodeToString(payloadDigest[:]), Payload: payload, Result: result,
		CommittedAt: now.Add(11 * time.Second),
	}
	receipt, err := store.CommitResult(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.CommitResult(ctx, commit)
	if err != nil || repeated != receipt {
		t.Fatalf("repeated receipt = %+v, %v; want %+v", repeated, err, receipt)
	}
	commit.PayloadHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := store.CommitResult(ctx, commit); !errors.Is(err, central.ErrIdempotencyConflict) {
		t.Fatalf("conflicting commit error = %v", err)
	}

	var captureCount, showtimeCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM schedule_captures WHERE assignment_id = $1`, assignmentID).
		Scan(&captureCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM showtime_observations WHERE assignment_id = $1`, assignmentID).
		Scan(&showtimeCount); err != nil {
		t.Fatal(err)
	}
	if captureCount != 1 || showtimeCount != 1 {
		t.Fatalf("stored rows: captures=%d showtimes=%d", captureCount, showtimeCount)
	}
}

func TestPostgresReconcilerLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	policyIDs := []string{"policy_retry", "policy_failed", "policy_missed", "policy_returned"}
	probeIDs := []string{"probe_reconcile_1", "probe_reconcile_2"}
	cleanupReconcileRows(t, store, policyIDs, probeIDs)
	t.Cleanup(func() { cleanupReconcileRows(t, store, policyIDs, probeIDs) })

	now := time.Now().UTC().Truncate(time.Microsecond)
	registerIntegrationProbe(t, store, probeIDs[0], "install_reconcile_1", now)
	registerIntegrationProbe(t, store, probeIDs[1], "install_reconcile_2", now)
	engine, err := reconcile.New(store, reconcile.Config{
		TickInterval: time.Second, ProbeHeartbeatTTL: time.Minute, OfflineRetention: 24 * time.Hour,
		RetryMinimum: time.Millisecond, RetryMaximum: time.Millisecond, BatchSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	seedIntegrationPolicy(t, store, policyIDs[0], "theater_retry", now.Add(-time.Minute))
	report, err := engine.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Leader || report.CreatedAssignments != 1 {
		t.Fatalf("initial reconcile report = %+v", report)
	}
	retryAssignment := assignmentForPolicy(t, store, policyIDs[0])
	if retryAssignment.Status != "queued" || len(retryAssignment.Task.TargetDates) != 2 {
		t.Fatalf("scheduled assignment = %+v", retryAssignment)
	}
	var eligibleCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM assignment_eligible_probes WHERE assignment_id = $1
	`, retryAssignment.ID).Scan(&eligibleCount); err != nil {
		t.Fatal(err)
	}
	if eligibleCount != 2 {
		t.Fatalf("eligible probe count = %d", eligibleCount)
	}

	leaseOne := sha256.Sum256([]byte("lease_reconcile_1"))
	claimedOne, err := store.ClaimAssignment(
		ctx, probeIDs[0], leaseOne, time.Now().UTC(), time.Now().UTC().Add(time.Minute), time.Now().UTC().Add(-time.Minute),
	)
	if err != nil || claimedOne.ID != retryAssignment.ID {
		t.Fatalf("first claim = %+v, %v", claimedOne, err)
	}
	expireAssignmentLease(t, store, retryAssignment.ID)
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	makeAssignmentClaimable(t, store, retryAssignment.ID)
	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], central.ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAssignment(
		ctx, probeIDs[0], leaseOne, time.Now().UTC(), time.Now().UTC().Add(time.Minute), time.Now().UTC().Add(-time.Minute),
	); !errors.Is(err, central.ErrNoAssignment) {
		t.Fatalf("same-probe retry error = %v", err)
	}
	leaseTwo := sha256.Sum256([]byte("lease_reconcile_2"))
	claimedTwo, err := store.ClaimAssignment(
		ctx, probeIDs[1], leaseTwo, time.Now().UTC(), time.Now().UTC().Add(time.Minute), time.Now().UTC().Add(-time.Minute),
	)
	if err != nil || claimedTwo.ID != retryAssignment.ID {
		t.Fatalf("second claim = %+v, %v", claimedTwo, err)
	}
	commit := integrationResultCommit(t, retryAssignment, probeIDs[1], leaseTwo)
	if _, err := store.CommitResult(ctx, commit); err != nil {
		t.Fatal(err)
	}
	replay := commit
	replay.ProbeID = probeIDs[0]
	replay.LeaseHash = leaseOne
	if _, err := store.CommitResult(ctx, replay); !errors.Is(err, central.ErrIdempotencyConflict) {
		t.Fatalf("cross-probe replay error = %v", err)
	}
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertPolicyOutcome(t, store, policyIDs[0], reconcile.OutcomeCompleted)

	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], central.ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seedIntegrationPolicy(t, store, policyIDs[1], "theater_failed", time.Now().UTC().Add(-time.Minute))
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	failedAssignment := assignmentForPolicy(t, store, policyIDs[1])
	claimAndExpire(t, store, engine, failedAssignment.ID, probeIDs[0], "failed_lease_1")
	makeAssignmentClaimable(t, store, failedAssignment.ID)
	claimAndExpire(t, store, engine, failedAssignment.ID, probeIDs[1], "failed_lease_2")
	var failedStatus, failedReason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, terminal_reason FROM observation_assignments WHERE id = $1
	`, failedAssignment.ID).Scan(&failedStatus, &failedReason); err != nil {
		t.Fatal(err)
	}
	if failedStatus != reconcile.OutcomeFailed || failedReason != "eligible_probes_exhausted" {
		t.Fatalf("failed assignment = %s, %s", failedStatus, failedReason)
	}
	assertPolicyOutcome(t, store, policyIDs[1], reconcile.OutcomeFailed)

	if err := store.DisconnectProbe(ctx, probeIDs[0], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.DisconnectProbe(ctx, probeIDs[1], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seedIntegrationPolicy(t, store, policyIDs[2], "theater_missed", time.Now().UTC().Add(-time.Minute))
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	missedAssignment := assignmentForPolicy(t, store, policyIDs[2])
	if missedAssignment.Status != reconcile.OutcomeMissed {
		t.Fatalf("missed assignment = %+v", missedAssignment)
	}
	assertPolicyOutcome(t, store, policyIDs[2], reconcile.OutcomeMissed)

	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], central.ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seedIntegrationPolicy(t, store, policyIDs[3], "theater_returned", time.Now().UTC().Add(-time.Minute))
	if _, err := engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	returnedAssignment := assignmentForPolicy(t, store, policyIDs[3])
	if returnedAssignment.Status != "queued" {
		t.Fatalf("returned-probe assignment = %+v", returnedAssignment)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM assignment_eligible_probes WHERE assignment_id = $1
	`, returnedAssignment.ID).Scan(&eligibleCount); err != nil {
		t.Fatal(err)
	}
	if eligibleCount != 1 {
		t.Fatalf("returned eligible probe count = %d", eligibleCount)
	}
}

func TestPostgresScheduleIntelligenceProjection(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const (
		policyID         = "policy_intelligence_integration"
		theaterSourceKey = "theater_intelligence_integration"
	)
	assignmentIDs := []string{
		"assignment_intelligence_0", "assignment_intelligence_1",
		"assignment_intelligence_2", "assignment_intelligence_3",
	}
	hashes := []string{
		fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2),
		fmt.Sprintf("%064x", 3), fmt.Sprintf("%064x", 4),
	}
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM showtime_observations WHERE assignment_id = ANY($1)`,
			`DELETE FROM schedule_captures WHERE assignment_id = ANY($1)`,
			`DELETE FROM observation_assignments WHERE id = ANY($1)`,
		} {
			if _, cleanupErr := store.pool.Exec(context.Background(), statement, assignmentIDs); cleanupErr != nil {
				t.Errorf("intelligence observation cleanup: %v", cleanupErr)
			}
		}
		if _, cleanupErr := store.pool.Exec(context.Background(),
			`DELETE FROM observation_payloads WHERE content_hash = ANY($1)`, hashes); cleanupErr != nil {
			t.Errorf("intelligence payload cleanup: %v", cleanupErr)
		}
		if _, cleanupErr := store.pool.Exec(context.Background(),
			`DELETE FROM observation_policies WHERE id = $1`, policyID); cleanupErr != nil {
			t.Errorf("intelligence policy cleanup: %v", cleanupErr)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	theaterID := seedIntegrationPolicy(t, store, policyID, theaterSourceKey, base)

	observedTimes := []time.Time{base, base.Add(10 * time.Minute), base.Add(70 * time.Minute), base.Add(130 * time.Minute)}
	availableSeats := []int{-1, 100, 40, 0}
	for index, assignmentID := range assignmentIDs {
		runID := fmt.Sprintf("run_intelligence_%d", index)
		hash := hashes[index]
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO observation_assignments (
				id, policy_id, task_kind, theater_id, theater_provider_id, theater_source_key,
				theater_region, theater_name, target_dates,
				locale, time_zone, egress_policy_id, status, not_before, deadline,
				run_id, created_at, updated_at
			) VALUES ($1, $2, 'cgv.schedule.capture.v2', $3, 'cgv', $4, '서울', '용산아이파크몰',
				ARRAY['2026-08-20'::date], 'ko-KR', 'Asia/Seoul', 'scan_default', 'completed',
				$5, $6, $7, $5, $5)
		`, assignmentID, policyID, theaterID, theaterSourceKey,
			observedTimes[index], observedTimes[index].Add(time.Hour), runID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO observation_payloads (content_hash, payload, created_at)
			VALUES ($1, '{}', $2)
		`, hash, observedTimes[index]); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO schedule_captures (
				assignment_id, run_id, target_date, observed_at, complete, content_hash, created_at
			) VALUES ($1, $2, '2026-08-20', $3, true, $4, $3)
		`, assignmentID, runID, observedTimes[index], hash); err != nil {
			t.Fatal(err)
		}
		if availableSeats[index] < 0 {
			continue
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO showtime_observations (
				assignment_id, run_id, target_date, source_key, theater_id,
				auditorium_id, auditorium_name, screen_types, movie_title, poster_url,
				starts_at, ends_at, available_seats, capacity, sold_out, observed_at
			) VALUES ($1, $2, '2026-08-20', 'show_intelligence', $3,
				'imax', 'IMAX관', ARRAY['IMAX'], '통합 시험 영화', '',
				'2026-08-20T10:00:00Z', '2026-08-20T12:30:00Z', $4, 100, $5, $6)
		`, assignmentID, runID, theaterID, availableSeats[index], availableSeats[index] == 0, observedTimes[index]); err != nil {
			t.Fatal(err)
		}
	}

	value, err := store.AdminObservationIntelligence(ctx, time.FixedZone("KST", 9*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if value.SnapshotCount != 4 || value.ShowtimeObservations != 3 || len(value.OpeningPatterns) != 1 ||
		len(value.DemandPatterns) != 1 || !value.LastObservedAt.Equal(observedTimes[3]) {
		t.Fatalf("schedule intelligence summary = %+v", value)
	}
	opening := value.OpeningPatterns[0]
	if opening.SampleSize != 1 || opening.TypicalOpenTime != "09:05" ||
		opening.TypicalPrecisionMins != 10 || opening.AuditoriumID != "imax" {
		t.Fatalf("opening pattern = %+v", opening)
	}
	demand := value.DemandPatterns[0]
	if demand.OccurrenceCount != 1 || demand.TypicalFirstHourSellThrough != 60 ||
		demand.TypicalHalfSoldMinutes != 60 || demand.TypicalSoldOutMinutes != 120 {
		t.Fatalf("demand pattern = %+v", demand)
	}
}

func TestPostgresReconcilerLeadershipIsExclusive(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		leader, err := store.RunLeaderCycle(ctx, func(reconcile.CycleRepository) error {
			close(entered)
			<-release
			return nil
		})
		if err == nil && !leader {
			err = errors.New("first cycle did not become leader")
		}
		firstResult <- err
	}()
	<-entered
	secondLeader, err := store.RunLeaderCycle(ctx, func(reconcile.CycleRepository) error {
		return errors.New("follower callback must not run")
	})
	if err != nil || secondLeader {
		t.Fatalf("second cycle = leader %t, error %v", secondLeader, err)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := Migrate(ctx, store.pool); err != nil {
				errorsFound <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var missingChecksums int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM cineko_schema_migrations WHERE checksum = ''
	`).Scan(&missingChecksums); err != nil {
		t.Fatal(err)
	}
	if missingChecksums != 0 {
		t.Fatalf("migrations without checksum = %d", missingChecksums)
	}
}

func TestPostgresConcurrentClaimRespectsProbeCapacity(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	policyIDs := []string{"policy_capacity_1", "policy_capacity_2"}
	probeIDs := []string{"probe_capacity"}
	cleanupReconcileRows(t, store, policyIDs, probeIDs)
	t.Cleanup(func() { cleanupReconcileRows(t, store, policyIDs, probeIDs) })

	now := time.Now().UTC().Truncate(time.Microsecond)
	registerIntegrationProbe(t, store, probeIDs[0], "install_capacity", now)
	seedIntegrationPolicy(t, store, policyIDs[0], "theater_capacity_1", now.Add(-time.Minute))
	seedIntegrationPolicy(t, store, policyIDs[1], "theater_capacity_2", now.Add(-time.Minute))
	engine, err := reconcile.New(store, reconcile.Config{
		TickInterval: time.Second, ProbeHeartbeatTTL: time.Minute, OfflineRetention: 24 * time.Hour,
		RetryMinimum: time.Millisecond, RetryMaximum: time.Millisecond, BatchSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.CreatedAssignments != 2 {
		t.Fatalf("created assignments = %d, want 2", report.CreatedAssignments)
	}
	claimNow := time.Now().UTC()

	type claimResult struct {
		assignment central.Assignment
		err        error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for index := range 2 {
		leaseHash := sha256.Sum256([]byte{byte(index + 1)})
		go func() {
			<-start
			assignment, claimErr := store.ClaimAssignment(
				ctx, probeIDs[0], leaseHash, claimNow, claimNow.Add(time.Minute), claimNow.Add(-time.Minute),
			)
			results <- claimResult{assignment: assignment, err: claimErr}
		}()
	}
	close(start)

	claimed := 0
	noAssignment := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			claimed++
		case errors.Is(result.err, central.ErrNoAssignment):
			noAssignment++
		default:
			t.Fatalf("concurrent claim error = %v", result.err)
		}
	}
	if claimed != 1 || noAssignment != 1 {
		t.Fatalf("concurrent claims: claimed=%d no_assignment=%d", claimed, noAssignment)
	}

	var availableSlots, leasedAssignments, attempts int
	if err := store.pool.QueryRow(ctx, `
		SELECT available_slots FROM probe_runtimes WHERE id = $1
	`, probeIDs[0]).Scan(&availableSlots); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM observation_assignments
		WHERE policy_id = ANY($1) AND status = 'leased'
	`, policyIDs).Scan(&leasedAssignments); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM assignment_attempts
		WHERE assignment_id IN (SELECT id FROM observation_assignments WHERE policy_id = ANY($1))
	`, policyIDs).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if availableSlots != 0 || leasedAssignments != 1 || attempts != 1 {
		t.Fatalf(
			"capacity state: slots=%d leased=%d attempts=%d",
			availableSlots, leasedAssignments, attempts,
		)
	}
}

func TestPostgresDuePoliciesKeepBookingDemandAheadOfChangeBurst(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	policyIDs := []string{"policy_lane_demand", "policy_lane_burst", "policy_lane_cancellation", "policy_lane_baseline"}
	const userID = "user_lane_priority"
	cleanup := func() {
		if _, cleanupErr := store.pool.Exec(ctx, `DELETE FROM client_resources WHERE user_id = $1`, userID); cleanupErr != nil {
			t.Errorf("delete lane resources: %v", cleanupErr)
		}
		if _, cleanupErr := store.pool.Exec(ctx, `DELETE FROM client_users WHERE id = $1`, userID); cleanupErr != nil {
			t.Errorf("delete lane user: %v", cleanupErr)
		}
		cleanupReconcileRows(t, store, policyIDs, nil)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC().Truncate(time.Microsecond)
	demandTheaterID := seedIntegrationPolicy(t, store, policyIDs[0], "theater_lane_demand", now.Add(-time.Minute))
	burstTheaterID := seedIntegrationPolicy(t, store, policyIDs[1], "theater_lane_burst", now.Add(-time.Minute))
	cancellationTheaterID := seedIntegrationPolicy(t, store, policyIDs[2], "theater_lane_cancellation", now.Add(-time.Minute))
	baselineTheaterID := seedIntegrationPolicy(t, store, policyIDs[3], "theater_lane_baseline", now.Add(-time.Minute))
	if _, err := store.pool.Exec(ctx, `
		UPDATE observation_policies
		SET priority = CASE id
			WHEN $1 THEN 0
			WHEN $2 THEN 0
			WHEN $3 THEN 0
			WHEN $4 THEN 100
		END,
		burst_until = CASE WHEN id = $2 THEN $5::timestamptz ELSE NULL END
		WHERE id = ANY($6)
	`, policyIDs[0], policyIDs[1], policyIDs[2], policyIDs[3], now.Add(time.Hour), policyIDs); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO client_users (id, display_name, created_at, updated_at)
		VALUES ($1, 'lane test', $2, $2)
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	presetPayload := fmt.Sprintf(`{"id":"preset_lane","userId":%q,"name":"lane","theaterId":%q,"auditoriumId":"imax","seatCount":1,"seatPreference":{}}`, userID, demandTheaterID)
	monitorPayload := fmt.Sprintf(`{"id":"monitor_lane","userId":%q,"presetId":"preset_lane","movie":"Movie","targetDates":["2026-08-20"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"pending","mode":"opening"}`, userID)
	cancellationPresetPayload := fmt.Sprintf(`{"id":"preset_cancellation","userId":%q,"name":"cancellation","theaterId":%q,"auditoriumId":"imax","seatCount":1,"seatPreference":{}}`, userID, cancellationTheaterID)
	cancellationPayload := fmt.Sprintf(`{"id":"monitor_cancellation","userId":%q,"presetId":"preset_cancellation","movie":"Movie","targetDates":["2026-08-20"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"pending","mode":"cancellation"}`, userID)
	triggeredPayload := fmt.Sprintf(`{"id":"monitor_triggered","userId":%q,"presetId":"preset_triggered","movie":"Movie","targetDates":["2026-08-20"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"triggered","mode":"opening"}`, userID)
	triggeredPresetPayload := fmt.Sprintf(`{"id":"preset_triggered","userId":%q,"name":"triggered","theaterId":%q,"auditoriumId":"imax","seatCount":1,"seatPreference":{}}`, userID, baselineTheaterID)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO client_resources (user_id, kind, id, revision, payload, created_at, updated_at)
		VALUES
			($1, 'presets', 'preset_lane', 1, $2, $8, $8),
			($1, 'monitors', 'monitor_lane', 1, $3, $8, $8),
			($1, 'presets', 'preset_cancellation', 1, $4, $8, $8),
			($1, 'monitors', 'monitor_cancellation', 1, $5, $8, $8),
			($1, 'presets', 'preset_triggered', 1, $6, $8, $8),
			($1, 'monitors', 'monitor_triggered', 1, $7, $8, $8)
	`, userID, presetPayload, monitorPayload, cancellationPresetPayload, cancellationPayload,
		triggeredPresetPayload, triggeredPayload, now); err != nil {
		t.Fatal(err)
	}

	var due []reconcile.Policy
	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		var dueErr error
		due, dueErr = repository.DuePolicies(ctx, now, 10)
		return dueErr
	})
	if err != nil || !leader {
		t.Fatalf("read due policy lanes: leader=%t error=%v", leader, err)
	}
	if len(due) != 4 {
		t.Fatalf("due policies = %d, want 4", len(due))
	}
	if due[0].ID != policyIDs[0] || due[0].Priority != 90 {
		t.Fatalf("booking demand policy = %+v", due[0])
	}
	if due[1].ID != policyIDs[1] || due[1].Priority != 60 {
		t.Fatalf("change burst policy = %+v", due[1])
	}
	if due[2].ID != policyIDs[2] || due[2].Priority != 40 {
		t.Fatalf("cancellation demand policy = %+v", due[2])
	}
	if due[3].ID != policyIDs[3] || due[3].Priority != 33 {
		t.Fatalf("baseline policy = %+v", due[3])
	}
	if due[0].Theater.ID != demandTheaterID || due[1].Theater.ID != burstTheaterID ||
		due[2].Theater.ID != cancellationTheaterID {
		t.Fatalf("unexpected lane theaters: demand=%q burst=%q", due[0].Theater.ID, due[1].Theater.ID)
	}
}

func TestPostgresSystemAssignmentDoesNotRequirePolicy(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	const assignmentID = "assignment_system_catalog_integration"
	cleanup := func() {
		if _, cleanupErr := store.pool.Exec(context.Background(),
			`DELETE FROM observation_assignments WHERE id = $1`, assignmentID); cleanupErr != nil {
			t.Errorf("system assignment cleanup: %v", cleanupErr)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	now := time.Now().UTC()
	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		return repository.CreateAssignment(ctx, reconcile.NewAssignment{
			ID: assignmentID, Priority: 100, Status: "queued", NotBefore: now,
			Deadline: now.Add(time.Minute), CreatedAt: now,
			Task: contracts.AssignmentTask{
				Kind: contracts.CapabilityCGVCatalogCapture,
				Theater: contracts.Theater{
					ID: "system-catalog", ProviderID: contracts.ProviderCGV,
					SourceKey: "__catalog__", Region: "system", Name: "CGV catalog",
				},
				Locale: "ko-KR", TimeZone: "Asia/Seoul", EgressPolicyID: "scan_default",
			},
		})
	})
	if err != nil || !leader {
		t.Fatalf("create system assignment: leader=%t error=%v", leader, err)
	}
	var policyID *string
	if err := store.pool.QueryRow(ctx,
		`SELECT policy_id FROM observation_assignments WHERE id = $1`, assignmentID).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if policyID != nil {
		t.Fatalf("system assignment policy = %q", *policyID)
	}
}

func TestPostgresReconcilerCycleRollsBackAtomically(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	policyIDs := []string{"policy_rollback"}
	cleanupReconcileRows(t, store, policyIDs, nil)
	t.Cleanup(func() { cleanupReconcileRows(t, store, policyIDs, nil) })
	seedIntegrationPolicy(t, store, policyIDs[0], "theater_rollback", time.Now().UTC().Add(time.Hour))

	wantErr := errors.New("rollback integration cycle")
	leader, err := store.RunLeaderCycle(ctx, func(repository reconcile.CycleRepository) error {
		if suspendErr := repository.SuspendPolicy(ctx, policyIDs[0], "injected_failure", time.Now().UTC()); suspendErr != nil {
			return suspendErr
		}
		return wantErr
	})
	if !leader || !errors.Is(err, wantErr) {
		t.Fatalf("rollback cycle: leader=%t error=%v", leader, err)
	}
	var enabled bool
	var errorCode string
	if err := store.pool.QueryRow(ctx, `
		SELECT enabled, last_error_code FROM observation_policies WHERE id = $1
	`, policyIDs[0]).Scan(&enabled, &errorCode); err != nil {
		t.Fatal(err)
	}
	if !enabled || errorCode != "" {
		t.Fatalf("policy changed despite rollback: enabled=%t error=%q", enabled, errorCode)
	}
}

func TestPostgresMigrationRejectsChecksumDrift(t *testing.T) {
	databaseURL := testDatabaseURL
	if databaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	migrationSet, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrationSet) == 0 {
		t.Fatal("no embedded Central migrations")
	}
	first := migrationSet[0]
	if _, err := store.pool.Exec(ctx, `
		UPDATE cineko_schema_migrations SET checksum = 'drifted' WHERE version = $1
	`, first.version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.pool.Exec(context.Background(), `
			UPDATE cineko_schema_migrations SET checksum = $2 WHERE version = $1
		`, first.version, first.checksum); err != nil {
			t.Errorf("restore migration checksum: %v", err)
		}
	})
	if err := Migrate(ctx, store.pool); err == nil {
		t.Fatal("migration with checksum drift unexpectedly succeeded")
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE cineko_schema_migrations SET checksum = $2 WHERE version = $1
	`, first.version, first.checksum); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, store.pool); err != nil {
		t.Fatalf("migration after checksum restore: %v", err)
	}
}

func registerIntegrationProbe(t *testing.T, store *Store, probeID, installationID string, now time.Time) {
	t.Helper()
	tokenHash := sha256.Sum256([]byte("token_" + probeID))
	if _, err := store.RegisterProbe(context.Background(), central.Probe{
		ID: probeID, InstallationID: installationID, Kind: "container", NetworkID: "net_" + probeID,
		Capabilities: []string{"cgv.schedule.capture.v2"}, MaxConcurrency: 1,
		Runtime: central.Runtime{
			Version: "1.0.0", Protocol: central.ProtocolVersion, BrowserRevision: "2000",
			Platform: "linux", Arch: "amd64",
		},
		TokenHash: tokenHash, TokenExpiresAt: now.Add(time.Hour), Status: "online", Health: "healthy",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HeartbeatProbe(context.Background(), probeID, central.ProbeHeartbeatRequest{
		AvailableSlots: 1, Health: "healthy",
	}, now); err != nil {
		t.Fatal(err)
	}
}

func seedIntegrationPolicy(
	t *testing.T,
	store *Store,
	policyID string,
	theaterSourceKey string,
	nextRunAt time.Time,
) string {
	t.Helper()
	now := time.Now().UTC()
	theaterID := contracts.CatalogID(contracts.ProviderCGV, "theater", theaterSourceKey)
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO observation_policies (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_date_mode, target_dates,
			horizon_days, locale, time_zone, egress_policy_id, priority, min_interval_seconds,
			max_interval_seconds, execution_window_seconds, next_run_at, created_at, updated_at
		) VALUES (
			$1, 'cgv.schedule.capture.v2', $2, $3, $4, '서울', '통합 시험관', 'rolling', '{}',
			2, 'ko-KR', 'Asia/Seoul', 'scan_default', 50, 60, 61, 120, $5, $6, $6
		)
	`, policyID, theaterID, contracts.ProviderCGV, theaterSourceKey, nextRunAt, now); err != nil {
		t.Fatal(err)
	}
	return theaterID
}

func assignmentForPolicy(t *testing.T, store *Store, policyID string) central.Assignment {
	t.Helper()
	assignment, err := scanAssignment(store.pool.QueryRow(context.Background(), `
		SELECT id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates::text[],
			locale, time_zone, egress_policy_id, status, not_before, deadline,
			COALESCE(probe_id, ''), lease_expires_at, created_at, updated_at, task_data
		FROM observation_assignments
		WHERE policy_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, policyID))
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

func expireAssignmentLease(t *testing.T, store *Store, assignmentID string) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		UPDATE observation_assignments SET lease_expires_at = $2 WHERE id = $1 AND status = 'leased'
	`, assignmentID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
}

func makeAssignmentClaimable(t *testing.T, store *Store, assignmentID string) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		UPDATE observation_assignments SET not_before = $2 WHERE id = $1 AND status = 'queued'
	`, assignmentID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
}

func claimAndExpire(
	t *testing.T,
	store *Store,
	engine *reconcile.Engine,
	assignmentID string,
	probeID string,
	leaseValue string,
) {
	t.Helper()
	now := time.Now().UTC()
	leaseHash := sha256.Sum256([]byte(leaseValue))
	assignment, err := store.ClaimAssignment(
		context.Background(), probeID, leaseHash, now, now.Add(time.Minute), now.Add(-time.Minute),
	)
	if err != nil || assignment.ID != assignmentID {
		t.Fatalf("claim %s with %s = %+v, %v", assignmentID, probeID, assignment, err)
	}
	expireAssignmentLease(t, store, assignmentID)
	if _, err := engine.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func integrationResultCommit(
	t *testing.T,
	assignment central.Assignment,
	probeID string,
	leaseHash [32]byte,
) central.ResultCommit {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	targetDate := assignment.Task.TargetDates[0]
	result := central.AssignmentResult{
		RunID: "run_reconcile", Status: reconcile.OutcomeCompleted,
		StartedAt: now.Add(-2 * time.Second), FinishedAt: now.Add(-time.Second),
		Captures: []central.Capture{{
			TargetDate: targetDate, Complete: true, ObservedAt: now.Add(-time.Second),
			Showtimes: []central.Showtime{integrationShowtime(assignment.Task.Theater, now)},
		}},
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return central.ResultCommit{
		AssignmentID: assignment.ID, ProbeID: probeID, LeaseHash: leaseHash,
		PayloadHash: hex.EncodeToString(digest[:]), Payload: payload, Result: result, CommittedAt: now,
	}
}

func integrationShowtime(theater central.Theater, now time.Time) central.Showtime {
	movieSourceKey := "movie_integration"
	movie := central.Movie{
		ID:         contracts.CatalogID(theater.ProviderID, "movie", movieSourceKey),
		ProviderID: theater.ProviderID, SourceKey: movieSourceKey, Title: "통합 시험 영화",
	}
	auditoriumSourceKey := theater.SourceKey + "/imax"
	auditorium := central.Auditorium{
		ID:        contracts.CatalogID(theater.ProviderID, "auditorium", auditoriumSourceKey),
		TheaterID: theater.ID, SourceKey: auditoriumSourceKey, Name: "IMAX관",
		ScreenTypes: []string{"IMAX"}, Capacity: 624,
	}
	showtimeSourceKey := theater.SourceKey + "/showtime_integration"
	return central.Showtime{
		ID:         contracts.CatalogID(theater.ProviderID, "showtime", showtimeSourceKey),
		ProviderID: theater.ProviderID, SourceKey: showtimeSourceKey, TheaterID: theater.ID,
		Movie: movie, Auditorium: auditorium,
		StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(26 * time.Hour),
		AvailableSeats: 500, Capacity: 624,
	}
}

func assertPolicyOutcome(t *testing.T, store *Store, policyID, expected string) {
	t.Helper()
	var outcome string
	var nextRunAt *time.Time
	if err := store.pool.QueryRow(context.Background(), `
		SELECT COALESCE(last_outcome, ''), next_run_at FROM observation_policies WHERE id = $1
	`, policyID).Scan(&outcome, &nextRunAt); err != nil {
		t.Fatal(err)
	}
	if outcome != expected || nextRunAt == nil || !nextRunAt.After(time.Now().UTC()) {
		t.Fatalf("policy %s outcome = %q, next = %v", policyID, outcome, nextRunAt)
	}
}

func cleanupReconcileRows(t *testing.T, store *Store, policyIDs, probeIDs []string) {
	t.Helper()
	ctx := context.Background()
	rows, err := store.pool.Query(ctx, `
		SELECT capture.content_hash
		FROM schedule_captures AS capture
		JOIN observation_assignments AS assignment ON assignment.id = capture.assignment_id
		WHERE assignment.policy_id = ANY($1)
	`, policyIDs)
	if err != nil {
		t.Errorf("read reconcile payload hashes: %v", err)
		return
	}
	var payloadHashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Errorf("scan reconcile payload hash: %v", err)
			rows.Close()
			return
		}
		payloadHashes = append(payloadHashes, hash)
	}
	rows.Close()
	statements := []string{
		`DELETE FROM showtime_observations WHERE assignment_id IN (SELECT id FROM observation_assignments WHERE policy_id = ANY($1))`,
		`DELETE FROM schedule_captures WHERE assignment_id IN (SELECT id FROM observation_assignments WHERE policy_id = ANY($1))`,
		`DELETE FROM assignment_eligible_probes WHERE assignment_id IN (SELECT id FROM observation_assignments WHERE policy_id = ANY($1))`,
		`DELETE FROM assignment_attempts WHERE assignment_id IN (SELECT id FROM observation_assignments WHERE policy_id = ANY($1))`,
		`DELETE FROM observation_assignments WHERE policy_id = ANY($1)`,
		`DELETE FROM observation_policies WHERE id = ANY($1)`,
	}
	for _, statement := range statements {
		if _, err := store.pool.Exec(ctx, statement, policyIDs); err != nil {
			t.Errorf("reconcile cleanup: %v", err)
		}
	}
	for _, hash := range payloadHashes {
		if _, err := store.pool.Exec(ctx, `DELETE FROM observation_payloads WHERE content_hash = $1`, hash); err != nil {
			t.Errorf("reconcile payload cleanup: %v", err)
		}
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM probe_runtimes WHERE id = ANY($1)`, probeIDs); err != nil {
		t.Errorf("reconcile probe cleanup: %v", err)
	}
}

func cleanupIntegrationRows(t *testing.T, store *Store, probeID, assignmentID string) {
	t.Helper()
	ctx := context.Background()
	rows, err := store.pool.Query(ctx, `SELECT content_hash FROM schedule_captures WHERE assignment_id = $1`, assignmentID)
	if err != nil {
		t.Errorf("read integration payload hashes: %v", err)
		return
	}
	var payloadHashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Errorf("scan integration payload hash: %v", err)
			rows.Close()
			return
		}
		payloadHashes = append(payloadHashes, hash)
	}
	rows.Close()
	statements := []string{
		`DELETE FROM showtime_observations WHERE assignment_id = $1`,
		`DELETE FROM schedule_captures WHERE assignment_id = $1`,
		`DELETE FROM assignment_eligible_probes WHERE assignment_id = $1`,
		`DELETE FROM assignment_attempts WHERE assignment_id = $1`,
		`DELETE FROM observation_assignments WHERE id = $1`,
	}
	for _, statement := range statements {
		if _, err := store.pool.Exec(ctx, statement, assignmentID); err != nil {
			t.Errorf("integration cleanup: %v", err)
		}
	}
	for _, hash := range payloadHashes {
		if _, err := store.pool.Exec(ctx, `DELETE FROM observation_payloads WHERE content_hash = $1`, hash); err != nil {
			t.Errorf("integration payload cleanup: %v", err)
		}
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM probe_runtimes WHERE id = $1`, probeID); err != nil {
		t.Errorf("integration probe cleanup: %v", err)
	}
}
