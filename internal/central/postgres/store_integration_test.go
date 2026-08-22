package postgres

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/bootstrap"
	"github.com/cineko-org/central/internal/central/reconcile"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	"github.com/cineko-org/central/internal/support/numeric"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		providerID     = "provider_client_plane_integration"
		theaterID      = "theater_client_plane_integration"
		auditoriumID   = "auditorium_client_plane_integration"
		movieID        = "movie_client_plane_integration"
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
		cleanupClientResourceCatalog(t, store, providerID, []string{theaterID}, []string{auditoriumID}, []string{movieID})
	}
	cleanup()
	t.Cleanup(cleanup)
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID)

	service, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Now().UTC()
	release := integrationClientRelease(publishedAt, "darwin", "arm64", "https://download.example/client.zip", "Cineko.app/Contents/MacOS/Cineko")
	browserRelease := integrationBrowserRelease(publishedAt, "darwin", "arm64", "https://download.example/browser.zip", "chromium/Chromium")
	playwrightRelease := integrationPlaywrightRelease(publishedAt, "darwin", "arm64", "https://download.example/driver.zip", "driver/playwright")
	launcherRelease := integrationLauncherRelease(publishedAt, "darwin", "arm64", "https://download.example/launcher.zip", "cineko-launcher")
	clientReleases := []*releasepb.ClientRelease{release,
		integrationClientRelease(publishedAt, "linux", "amd64", "https://download.example/linux/client.zip", "cineko-client"),
		integrationClientRelease(publishedAt, "windows", "amd64", "https://download.example/windows/client.zip", "cineko-client.exe"),
	}
	browserReleases := []*releasepb.BrowserRelease{browserRelease,
		integrationBrowserRelease(publishedAt, "linux", "amd64", "https://download.example/linux/browser.zip", "cineko-browser"),
		integrationBrowserRelease(publishedAt, "windows", "amd64", "https://download.example/windows/browser.zip", "cineko-browser.exe"),
	}
	playwrightReleases := []*releasepb.PlaywrightRelease{playwrightRelease,
		integrationPlaywrightRelease(publishedAt, "linux", "amd64", "https://download.example/linux/driver.zip", "driver/playwright"),
		integrationPlaywrightRelease(publishedAt, "windows", "amd64", "https://download.example/windows/driver.zip", "driver/playwright.exe"),
	}
	launcherReleases := []*releasepb.LauncherRelease{launcherRelease,
		integrationLauncherRelease(publishedAt, "linux", "amd64", "https://download.example/linux/launcher.zip", "cineko-launcher"),
		integrationLauncherRelease(publishedAt, "windows", "amd64", "https://download.example/windows/launcher.zip", "cineko-launcher.exe"),
	}
	if err := service.BootstrapReleaseRegistry(
		ctx,
		releaseSetClients(clientReleases),
		releaseSetBrowsers(browserReleases),
		releaseSetPlaywright(playwrightReleases),
		releaseSetLaunchers(launcherReleases),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.Provision(ctx, []central.ClientCredentialSeed{{
		UserID: userID, DisplayName: "Integration User", AccessToken: accessToken,
	}}); err != nil {
		t.Fatal(err)
	}
	exchange := &clientpb.TokenExchangeRequest{}
	exchange.SetUserId(userID)
	exchange.SetAccessToken(accessToken)
	auth, err := service.Exchange(ctx, exchange)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, auth.GetAccessToken())
	if err != nil || principal.UserID != userID {
		t.Fatalf("authenticated client = %+v, %v", principal, err)
	}
	refresh := &clientpb.TokenRefreshRequest{}
	refresh.SetRefreshToken(auth.GetRefreshToken())
	refreshed, err := service.Refresh(ctx, refresh)
	if err != nil || refreshed.GetUser().GetId() != userID || refreshed.GetAccessToken() == auth.GetAccessToken() ||
		refreshed.GetRefreshToken() == auth.GetRefreshToken() {
		t.Fatalf("refreshed client session = %+v, %v", refreshed, err)
	}
	if _, err := service.Authenticate(ctx, auth.GetAccessToken()); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("revoked access token error = %v", err)
	}
	refreshReplay := &clientpb.TokenRefreshRequest{}
	refreshReplay.SetRefreshToken(auth.GetRefreshToken())
	if _, err := service.Refresh(ctx, refreshReplay); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("replayed refresh token error = %v", err)
	}
	principal, err = service.Authenticate(ctx, refreshed.GetAccessToken())
	if err != nil || principal.UserID != userID {
		t.Fatalf("authenticated refreshed client = %+v, %v", principal, err)
	}
	deviceRequest := &clientpb.Device{}
	deviceRequest.SetInstallationId(installationID)
	deviceRequest.SetDeviceId("device_integration")
	deviceRequest.SetPlatform("darwin")
	deviceRequest.SetArchitecture("arm64")
	deviceRequest.SetAppVersion("1.0.0")
	device, err := service.UpsertDevice(ctx, principal, deviceRequest)
	if err != nil || device.GetUserId() != userID {
		t.Fatalf("client device = %+v, %v", device, err)
	}
	launchContext := &clientpb.LaunchContext{}
	launchContext.SetInstallationId(installationID)
	launchContext.SetDeviceId(device.GetDeviceId())
	launchContext.SetReleaseGeneration(service.ReleaseGeneration())
	launchContext.SetClientVersion(release.GetVersion())
	launchContext.SetArtifactSha256(release.GetArtifact().GetSha256())
	launchContext.SetBrowserRevision(browserRelease.GetRevision())
	launchContext.SetBrowserArtifactSha256(browserRelease.GetArtifact().GetSha256())
	launchContext.SetPlaywrightVersion(playwrightRelease.GetVersion())
	launchContext.SetPlaywrightArtifactSha256(playwrightRelease.GetArtifact().GetSha256())
	launchRequest := &clientpb.LaunchTicketRequest{}
	launchRequest.SetContext(launchContext)
	launchRequest.SetNonce("launcher_nonce_integration")
	launch, err := service.IssueLaunchTicket(ctx, principal, launchRequest)
	if err != nil || launch.GetLaunchTicket() == "" {
		t.Fatalf("launch ticket = %+v, %v", launch, err)
	}
	launchExchange := &clientpb.SessionExchangeRequest{}
	launchExchange.SetLaunchTicket(launch.GetLaunchTicket())
	launchExchange.SetClientNonce("client_nonce_integration")
	launched, err := service.ExchangeLaunchTicket(ctx, launchExchange)
	if err != nil || launched.GetUser().GetId() != userID {
		t.Fatalf("launched client session = %+v, %v", launched, err)
	}
	launchReplay := &clientpb.SessionExchangeRequest{}
	launchReplay.SetLaunchTicket(launch.GetLaunchTicket())
	launchReplay.SetClientNonce("client_nonce_replay")
	if _, err := service.ExchangeLaunchTicket(ctx, launchReplay); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("replayed launch ticket error = %v", err)
	}
	clientBootstrap, err := service.Bootstrap(ctx, principal, installationID)
	if err != nil || clientBootstrap.GetDevice() == nil || clientBootstrap.GetUser().GetId() != userID {
		t.Fatalf("client bootstrap = %+v, %v", clientBootstrap, err)
	}

	presetResource := storeIntegrationNamedPresetResource(userID, "preset_integration", "IMAX", theaterID, auditoriumID)
	created, err := service.PutResource(ctx, principal, "presets", "preset_integration", presetResource, nil, "create_preset")
	if err != nil || created.GetIdentity().GetRevision() != 1 {
		t.Fatalf("create client resource = %+v, %v", created, err)
	}
	replayedResource := storeIntegrationNamedPresetResource(userID, "preset_integration", "Ignored", theaterID, auditoriumID)
	replayed, err := service.PutResource(ctx, principal, "presets", "preset_integration", replayedResource, nil, "create_preset")
	if err != nil || replayed.GetIdentity().GetRevision() != created.GetIdentity().GetRevision() || replayed.GetPreset().GetName() != "IMAX" {
		t.Fatalf("replay client resource = %+v, %v", replayed, err)
	}
	if _, err := service.PutResource(ctx, principal, "monitors", "other", storeIntegrationMonitorResource(userID, "other", "preset_integration", movieID), nil, "create_preset"); !errors.Is(err, central.ErrIdempotencyConflict) {
		t.Fatalf("reused client command error = %v", err)
	}
	revision := created.GetIdentity().GetRevision()
	updatedResource := storeIntegrationNamedPresetResource(userID, "preset_integration", "IMAX center", theaterID, auditoriumID)
	updated, err := service.PutResource(ctx, principal, "presets", created.GetIdentity().GetId(), updatedResource, &revision, "update_preset")
	if err != nil || updated.GetIdentity().GetRevision() != 2 {
		t.Fatalf("update client resource = %+v, %v", updated, err)
	}
	revisions, err := store.ClientResourceRevisions(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var latestPresetEvent int64
	if err := store.pool.QueryRow(ctx, `
		SELECT MAX(sequence) FROM client_events
		WHERE user_id = $1 AND resource_kind = 'presets'
	`, userID).Scan(&latestPresetEvent); err != nil {
		t.Fatal(err)
	}
	if revisions["presets"] != latestPresetEvent {
		t.Fatalf("preset change cursor = %d, want event sequence %d", revisions["presets"], latestPresetEvent)
	}
	if _, err := service.PutResource(ctx, principal, "presets", created.GetIdentity().GetId(), storeIntegrationNamedPresetResource(userID, "preset_integration", "Stale", theaterID, auditoriumID), &revision, "stale_update"); !errors.Is(err, central.ErrRevisionConflict) {
		t.Fatalf("stale client revision error = %v", err)
	}
	resources, err := service.ListResources(ctx, principal, "presets")
	if err != nil || len(resources) != 1 || resources[0].GetIdentity().GetRevision() != 2 {
		t.Fatalf("list client resources = %+v, %v", resources, err)
	}
	events, err := service.Events(ctx, principal, 0, 10)
	if err != nil || len(events) != 2 || events[0].GetSequence() >= events[1].GetSequence() {
		t.Fatalf("client events = %+v, %v", events, err)
	}
	concurrentCreateErrors := make(chan error, 2)
	var concurrentCreates sync.WaitGroup
	for index := range 2 {
		concurrentCreates.Add(1)
		go func(index int) {
			defer concurrentCreates.Done()
			settingsResource := storeIntegrationSettingsResource()
			_, createErr := service.PutResource(ctx, principal, "settings", "settings", settingsResource, nil, fmt.Sprintf("create_concurrent_settings_%d", index))
			concurrentCreateErrors <- createErr
		}(index)
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
	revision = updated.GetIdentity().GetRevision()
	deleted, err := service.DeleteResource(ctx, principal, "presets", created.GetIdentity().GetId(), &revision, "delete_preset")
	if err != nil || deleted.GetIdentity().GetRevision() != 3 {
		t.Fatalf("delete client resource = %+v, %v", deleted, err)
	}
	var deletedPayload []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT payload FROM client_events
		WHERE user_id = $1 AND event_type = 'presets.deleted' AND resource_id = $2
	`, userID, created.GetIdentity().GetId()).Scan(&deletedPayload); err != nil {
		t.Fatal(err)
	}
	if string(deletedPayload) != `{}` {
		t.Fatalf("deleted client event payload = %s, want {}", deletedPayload)
	}
	if _, err := service.DeleteResource(ctx, principal, "presets", created.GetIdentity().GetId(), &revision, "delete_preset"); err != nil {
		t.Fatalf("replay client resource deletion = %v", err)
	}
	if _, err := service.GetResource(ctx, principal, "presets", created.GetIdentity().GetId()); !errors.Is(err, central.ErrNotFound) {
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
		onlineID           = "probe_admin_delete_online"
		offlineID          = "probe_admin_delete_offline"
		historyID          = "probe_admin_delete_history"
		assignmentID       = "assignment_admin_delete_history"
		leasedAssignmentID = "assignment_admin_delete_leased"
	)
	probeIDs := []string{onlineID, offlineID, historyID}
	cleanup := func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM assignment_attempts WHERE assignment_id = $1`, assignmentID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM observation_assignments WHERE id = $1`, leasedAssignmentID)
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
			$1, 'cgv.schedule.capture', 'theater_admin_delete', 'cgv', 'admin-delete',
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
	if err := store.DeleteAdminProbe(ctx, historyID); err != nil {
		t.Fatalf("delete Probe with history = %v", err)
	}
	var attempts int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM assignment_attempts WHERE assignment_id = $1 AND probe_id = $2
	`, assignmentID, historyID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("historical Probe attempts = %d, want 1", attempts)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE probe_runtimes SET status = 'offline' WHERE id = $1`, onlineID); err != nil {
		t.Fatal(err)
	}
	leaseTokenHash := sha256.Sum256([]byte("lease_" + onlineID))
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates, locale, time_zone, egress_policy_id,
			status, not_before, deadline, probe_id, lease_token_hash, lease_expires_at,
			created_at, updated_at
		) VALUES (
			$1, 'cgv.schedule.capture', 'theater_admin_delete_leased', 'cgv', 'admin-delete-leased',
			'서울', '관리 시험관', ARRAY['2026-08-20'::date], 'ko-KR', 'Asia/Seoul', 'scan_default',
			'leased', $2, $3, $4, $5, $3, $2, $3
		)
	`, leasedAssignmentID, now.Add(-time.Minute), now.Add(time.Minute), onlineID, leaseTokenHash[:]); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAdminProbe(ctx, onlineID); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("delete Probe with active assignment = %v", err)
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
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_users WHERE id = $1`, issue.GetUser().GetId())
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_pin_attempts`)
	})
	users, err := pinService.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, user := range users {
		found = found || user.GetUser().GetId() == issue.GetUser().GetId() && user.GetPinActive()
	}
	if !found {
		t.Fatalf("created PIN user missing from %+v", users)
	}
	request := &clientpb.PinExchangeRequest{}
	request.SetPin(issue.GetPin())
	request.SetInstallationId("install_pin_integration")
	request.SetDeviceId("device_pin_integration")
	auth, err := pinService.Exchange(ctx, request, "198.51.100.40")
	if err != nil || auth.GetUser().GetId() != issue.GetUser().GetId() {
		t.Fatalf("PIN exchange = %+v, %v", auth, err)
	}
	principal, err := clientService.Authenticate(ctx, auth.GetAccessToken())
	if err != nil || principal.UserID != issue.GetUser().GetId() {
		t.Fatalf("PIN session principal = %+v, %v", principal, err)
	}
	rotated, err := pinService.Rotate(ctx, issue.GetUser().GetId())
	if err != nil || rotated.GetPin() == issue.GetPin() {
		t.Fatalf("rotated PIN = %+v, %v", rotated, err)
	}
	if _, err := pinService.Exchange(ctx, request, "198.51.100.41"); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("old PIN exchange error = %v", err)
	}
	request.SetPin(rotated.GetPin())
	if _, err := pinService.Exchange(ctx, request, "198.51.100.41"); err != nil {
		t.Fatalf("rotated PIN exchange = %v", err)
	}
	request.SetPin("999999")
	for attempt := 1; attempt <= central.ClientPINFailureLimit; attempt++ {
		_, err := pinService.Exchange(ctx, request, "198.51.100.42")
		if attempt < central.ClientPINFailureLimit && !errors.Is(err, central.ErrUnauthorized) {
			t.Fatalf("failed PIN attempt %d = %v", attempt, err)
		}
		if attempt == central.ClientPINFailureLimit && !errors.Is(err, central.ErrRateLimited) {
			t.Fatalf("rate-limited PIN attempt = %v", err)
		}
	}
	request.SetPin(rotated.GetPin())
	if _, err := pinService.Exchange(ctx, request, "198.51.100.42"); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("blocked correct PIN exchange = %v", err)
	}
	request.SetInstallationId("install_pin_persistent_source")
	request.SetDeviceId("device_pin_persistent_source")
	request.SetPin("999999")
	for attempt := 1; attempt < central.ClientPINFailureLimit; attempt++ {
		if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); !errors.Is(err, central.ErrUnauthorized) {
			t.Fatalf("persistent source failed PIN attempt %d = %v", attempt, err)
		}
	}
	request.SetPin(rotated.GetPin())
	if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); err != nil {
		t.Fatalf("valid PIN before source limit = %v", err)
	}
	request.SetInstallationId("install_pin_rotated_device")
	request.SetDeviceId("device_pin_rotated_device")
	request.SetPin("999999")
	if _, err := pinService.Exchange(ctx, request, "198.51.100.43"); !errors.Is(err, central.ErrRateLimited) {
		t.Fatalf("device rotation reset source-wide PIN limit: %v", err)
	}
	settings := storeIntegrationSettingsResource()
	if _, err := clientService.PutResource(
		ctx,
		principal,
		"settings",
		settings.GetIdentity().GetId(),
		settings,
		nil,
		"create_settings_before_user_delete",
	); err != nil {
		t.Fatal(err)
	}
	if err := pinService.DeleteUser(ctx, issue.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := clientService.Authenticate(ctx, auth.GetAccessToken()); !errors.Is(err, central.ErrUnauthorized) {
		t.Fatalf("deleted user session error = %v", err)
	}
	var userCount, resourceCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM client_users WHERE id = $1`, issue.GetUser().GetId()).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM client_resources
		WHERE user_id = $1
	`, issue.GetUser().GetId()).Scan(&resourceCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 || resourceCount != 0 {
		t.Fatalf("deleted user data remains: users=%d resources=%d", userCount, resourceCount)
	}
	if err := pinService.DeleteUser(ctx, issue.GetUser().GetId()); !errors.Is(err, central.ErrNotFound) {
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
		userID       = "user_execution_integration"
		accessToken  = "execution-client-token-0123456789abcdef"
		providerID   = "provider_execution_integration"
		theaterID    = "theater_execution_integration"
		auditoriumID = "auditorium_execution_integration"
		movieID      = "movie_execution"
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
		cleanupClientResourceCatalog(t, store, providerID, []string{theaterID}, []string{auditoriumID}, []string{movieID})
	}
	cleanup()
	t.Cleanup(cleanup)
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID)
	service, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Provision(ctx, []central.ClientCredentialSeed{{
		UserID: userID, DisplayName: "Execution User", AccessToken: accessToken,
	}}); err != nil {
		t.Fatal(err)
	}
	exchange := &clientpb.TokenExchangeRequest{}
	exchange.SetUserId(userID)
	exchange.SetAccessToken(accessToken)
	auth, err := service.Exchange(ctx, exchange)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, auth.GetAccessToken())
	if err != nil {
		t.Fatal(err)
	}
	for _, deviceValues := range [][2]string{{"execution_install_a", "execution_device_a"}, {"execution_install_b", "execution_device_b"}} {
		device := &clientpb.Device{}
		device.SetInstallationId(deviceValues[0])
		device.SetDeviceId(deviceValues[1])
		device.SetPlatform("darwin")
		device.SetArchitecture("arm64")
		device.SetAppVersion("1.0.0")
		if _, err := service.UpsertDevice(ctx, principal, device); err != nil {
			t.Fatal(err)
		}
	}
	preset := &clientpb.Preset{}
	preset.SetId("execution_preset")
	preset.SetUserId(userID)
	preset.SetName("IMAX")
	preset.SetTheaterId(theaterID)
	preset.SetAuditoriumId(auditoriumID)
	preset.SetSeatCount(2)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	showtimeStart := time.Now().UTC().Add(2 * time.Hour)
	targetDate := showtimeStart.In(time.FixedZone("KST", 9*60*60)).Format(time.DateOnly)
	targetDateMessage := localDateMessage(targetDate)
	earliest := &commonpb.LocalTime{}
	earliest.SetHour(0)
	earliest.SetMinute(0)
	latest := &commonpb.LocalTime{}
	latest.SetHour(23)
	latest.SetMinute(59)
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	monitor := &clientpb.Monitor{}
	monitor.SetId("execution_monitor")
	monitor.SetUserId(userID)
	monitor.SetPresetId(preset.GetId())
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle("Execution Movie")
	monitor.SetTargetDates([]*commonpb.LocalDate{targetDateMessage})
	monitor.SetEarliestTime(earliest)
	monitor.SetLatestTime(latest)
	monitor.SetSearchHorizonDays(14)
	monitor.SetState(state)
	for _, resource := range []struct {
		kind string
		id   string
		data *clientpb.Resource
	}{{"presets", preset.GetId(), storeIntegrationResource(preset.GetId(), preset, nil)}, {"monitors", monitor.GetId(), storeIntegrationResource(monitor.GetId(), nil, monitor)}} {
		if _, err := service.PutResource(ctx, principal, resource.kind, resource.id, resource.data, nil, "create_"+resource.id); err != nil {
			t.Fatal(err)
		}
	}
	observedAt := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	showtime := executionIntegrationShowtime(
		providerID,
		preset.GetTheaterId(),
		monitor.GetMovieId(),
		preset.GetAuditoriumId(),
		showtimeStart,
	)
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID, showtime)
	capture := &observationpb.Capture{}
	capture.SetTargetDate(targetDateMessage)
	capture.SetComplete(true)
	capture.SetObservedAt(timestamppb.New(observedAt))
	capture.SetShowtimes([]*catalogpb.Showtime{showtime})
	completed := &observationpb.Completed{}
	completed.SetCaptures([]*observationpb.Capture{capture})
	result := &observationpb.AssignmentResult{}
	result.SetCompleted(completed)
	commit := central.ResultCommit{
		CommittedAt: observedAt,
		Result:      result,
	}
	for range 2 {
		tx, beginErr := store.pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := enqueueClientExecutions(ctx, tx, commit, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
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
		t.Fatalf("queued commands after duplicate observations = %d, %v", queuedCommands, err)
	}
	// A catalog refresh can retire the referenced rows before a Client claims
	// the command. The immutable command payload must keep the execution viable.
	if _, err := store.pool.Exec(ctx, `DELETE FROM showtimes WHERE id = $1`, showtime.GetId()); err != nil {
		t.Fatal(err)
	}
	claimA := &executionpb.ClaimRequest{}
	claimA.SetInstallationId("execution_install_a")
	firstResponse, err := service.ClaimExecution(ctx, principal, claimA)
	first := firstResponse.GetCommand()
	if err != nil || first == nil || first.GetMonitorId() != monitor.GetId() || first.GetPayload().GetShowtime().GetId() != showtime.GetId() {
		t.Fatalf("first execution claim = %+v, %v", first, err)
	}
	claimB := &executionpb.ClaimRequest{}
	claimB.SetInstallationId("execution_install_b")
	emptyResponse, err := service.ClaimExecution(ctx, principal, claimB)
	if err != nil || emptyResponse.GetNoCommand() == nil {
		t.Fatalf("concurrent execution claim = %+v, %v", emptyResponse, err)
	}
	heartbeat := &executionpb.HeartbeatRequest{}
	heartbeat.SetCommandId(first.GetId())
	heartbeat.SetLeaseToken(first.GetLeaseToken())
	if _, err := service.HeartbeatExecution(ctx, principal, heartbeat); err != nil {
		t.Fatal(err)
	}
	failFirst := &executionpb.ResultRequest{}
	failFirst.SetCommandId(first.GetId())
	failFirst.SetLeaseToken(first.GetLeaseToken())
	retryFirst := &executionpb.RetryRequested{}
	retryFirst.SetReasonCode("booking_preparation_failed")
	failFirst.SetRetryRequested(retryFirst)
	if err := service.CompleteExecution(ctx, principal, failFirst); err != nil {
		t.Fatal(err)
	}
	secondResponse, err := service.ClaimExecution(ctx, principal, claimB)
	second := secondResponse.GetCommand()
	if err != nil || second == nil || second.GetAttempt() != 2 || second.GetInstallationId() != "execution_install_b" {
		t.Fatalf("second execution claim = %+v, %v", second, err)
	}
	failSecond := &executionpb.ResultRequest{}
	failSecond.SetCommandId(second.GetId())
	failSecond.SetLeaseToken(second.GetLeaseToken())
	failedSecond := &executionpb.Failed{}
	failedSecond.SetReasonCode(executionReasonPreferredSeatsUnavailable)
	failSecond.SetFailed(failedSecond)
	if err := service.CompleteExecution(ctx, principal, failSecond); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteExecution(ctx, principal, failSecond); !errors.Is(err, central.ErrLeaseExpired) {
		t.Fatalf("replayed execution completion error = %v", err)
	}
	commit.CommittedAt = time.Now().UTC()
	commit.Result.GetCompleted().GetCaptures()[0].SetObservedAt(timestamppb.New(commit.CommittedAt))
	tx, beginErr := store.pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if err := enqueueClientExecutions(ctx, tx, commit, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	emptyResponse, err = service.ClaimExecution(ctx, principal, claimA)
	if err != nil || emptyResponse.GetNoCommand() == nil {
		t.Fatalf("availability cooldown claim = %+v, %v", emptyResponse, err)
	}
	commit.CommittedAt = commit.CommittedAt.Add(31 * time.Second)
	commit.Result.GetCompleted().GetCaptures()[0].SetObservedAt(timestamppb.New(commit.CommittedAt))
	for _, value := range commit.Result.GetCompleted().GetCaptures()[0].GetShowtimes() {
		value.SetAvailableSeats(1)
	}
	tx, beginErr = store.pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if err := enqueueClientExecutions(ctx, tx, commit, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	thirdResponse, err := service.ClaimExecution(ctx, principal, claimA)
	third := thirdResponse.GetCommand()
	if err != nil || third == nil || third.GetAttempt() != 1 {
		t.Fatalf("availability rearmed execution claim = %+v, %v", third, err)
	}
	completeThird := &executionpb.ResultRequest{}
	completeThird.SetCommandId(third.GetId())
	completeThird.SetLeaseToken(third.GetLeaseToken())
	completeThird.SetCompleted(&executionpb.Completed{})
	if err := service.CompleteExecution(ctx, principal, completeThird); err != nil {
		t.Fatal(err)
	}

	terminalShowtime := proto.CloneOf(showtime)
	terminalShowtime.SetId("show_execution_terminal")
	terminalShowtime.SetSourceKey("show_execution_terminal")
	terminalStart := showtimeStart.Add(10 * time.Minute)
	terminalShowtime.SetStartsAt(timestamppb.New(terminalStart))
	terminalShowtime.SetEndsAt(timestamppb.New(terminalStart.Add(150 * time.Minute)))
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID, terminalShowtime)
	terminalCapture := &observationpb.Capture{}
	terminalCapture.SetTargetDate(targetDateMessage)
	terminalCapture.SetComplete(true)
	terminalCapture.SetObservedAt(timestamppb.New(commit.CommittedAt.Add(time.Second)))
	terminalCapture.SetShowtimes([]*catalogpb.Showtime{terminalShowtime})
	terminalCompleted := &observationpb.Completed{}
	terminalCompleted.SetCaptures([]*observationpb.Capture{terminalCapture})
	terminalResult := &observationpb.AssignmentResult{}
	terminalResult.SetCompleted(terminalCompleted)
	tx, beginErr = store.pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if err := enqueueClientExecutions(ctx, tx, central.ResultCommit{
		CommittedAt: terminalCapture.GetObservedAt().AsTime(), Result: terminalResult,
	}, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	terminalResponse, err := service.ClaimExecution(ctx, principal, claimA)
	terminalCommand := terminalResponse.GetCommand()
	if err != nil || terminalCommand == nil {
		t.Fatalf("terminal execution claim = %+v, %v", terminalResponse, err)
	}
	terminalFailure := &executionpb.ResultRequest{}
	terminalFailure.SetCommandId(terminalCommand.GetId())
	terminalFailure.SetLeaseToken(terminalCommand.GetLeaseToken())
	authenticationFailure := &executionpb.Failed{}
	authenticationFailure.SetReasonCode("authentication_required")
	terminalFailure.SetFailed(authenticationFailure)
	if err := service.CompleteExecution(ctx, principal, terminalFailure); err != nil {
		t.Fatal(err)
	}
	terminalMonitor, err := service.GetResource(ctx, principal, "monitors", monitor.GetId())
	if err != nil || terminalMonitor.GetMonitor().GetState().GetFailed().GetReason() != "authentication_required" {
		t.Fatalf("terminal monitor = %+v, %v", terminalMonitor, err)
	}

	rearmedMonitor := proto.CloneOf(terminalMonitor)
	rearmedState := &clientpb.MonitorState{}
	rearmedState.SetPending(&clientpb.MonitorPending{})
	rearmedMonitor.GetMonitor().SetState(rearmedState)
	rearmedMonitor.GetMonitor().SetUpdatedAt(timestamppb.Now())
	terminalRevision := terminalMonitor.GetIdentity().GetRevision()
	rearmedMonitor, err = service.PutResource(
		ctx, principal, "monitors", monitor.GetId(), rearmedMonitor,
		&terminalRevision, "rearm_monitor_for_lease_loss_test",
	)
	if err != nil {
		t.Fatal(err)
	}
	leaseLossShowtime := proto.CloneOf(terminalShowtime)
	leaseLossShowtime.SetId("show_execution_lease_loss")
	leaseLossShowtime.SetSourceKey("show_execution_lease_loss")
	leaseLossStart := terminalStart.Add(5 * time.Minute)
	leaseLossShowtime.SetStartsAt(timestamppb.New(leaseLossStart))
	leaseLossShowtime.SetEndsAt(timestamppb.New(leaseLossStart.Add(150 * time.Minute)))
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID, leaseLossShowtime)
	leaseLossCapture := &observationpb.Capture{}
	leaseLossCapture.SetTargetDate(targetDateMessage)
	leaseLossCapture.SetComplete(true)
	leaseLossCapture.SetObservedAt(timestamppb.Now())
	leaseLossCapture.SetShowtimes([]*catalogpb.Showtime{leaseLossShowtime})
	leaseLossCompleted := &observationpb.Completed{}
	leaseLossCompleted.SetCaptures([]*observationpb.Capture{leaseLossCapture})
	leaseLossResult := &observationpb.AssignmentResult{}
	leaseLossResult.SetCompleted(leaseLossCompleted)
	tx, beginErr = store.pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if err := enqueueClientExecutions(ctx, tx, central.ResultCommit{
		CommittedAt: leaseLossCapture.GetObservedAt().AsTime(), Result: leaseLossResult,
	}, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	leaseLossResponse, err := service.ClaimExecution(ctx, principal, claimA)
	leaseLossCommand := leaseLossResponse.GetCommand()
	if err != nil || leaseLossCommand == nil {
		t.Fatalf("lease-loss execution claim = %+v, %v", leaseLossResponse, err)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE client_execution_commands SET lease_expires_at = $2 WHERE id = $1
	`, leaseLossCommand.GetId(), time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	leaseLossEmpty, err := service.ClaimExecution(ctx, principal, claimB)
	if err != nil || leaseLossEmpty.GetNoCommand() == nil {
		t.Fatalf("claim after ambiguous lease loss = %+v, %v", leaseLossEmpty, err)
	}
	leaseLossMonitor, err := service.GetResource(ctx, principal, "monitors", monitor.GetId())
	if err != nil || leaseLossMonitor.GetMonitor().GetState().GetPaymentUnknown() == nil {
		t.Fatalf("lease-loss monitor = %+v, %v", leaseLossMonitor, err)
	}
	var leaseLossStatus, leaseLossReason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, reason_code FROM client_execution_commands WHERE id = $1
	`, leaseLossCommand.GetId()).Scan(&leaseLossStatus, &leaseLossReason); err != nil ||
		leaseLossStatus != "failed" || leaseLossReason != "execution_lease_lost" {
		t.Fatalf("lease-loss command = %q/%q, %v", leaseLossStatus, leaseLossReason, err)
	}

	rearmedMonitor = proto.CloneOf(leaseLossMonitor)
	rearmedState = &clientpb.MonitorState{}
	rearmedState.SetPending(&clientpb.MonitorPending{})
	rearmedMonitor.GetMonitor().SetState(rearmedState)
	rearmedMonitor.GetMonitor().SetUpdatedAt(timestamppb.Now())
	leaseLossRevision := leaseLossMonitor.GetIdentity().GetRevision()
	rearmedMonitor, err = service.PutResource(
		ctx, principal, "monitors", monitor.GetId(), rearmedMonitor,
		&leaseLossRevision, "rearm_monitor_for_delete_test",
	)
	if err != nil {
		t.Fatal(err)
	}
	deleteShowtime := proto.CloneOf(terminalShowtime)
	deleteShowtime.SetId("show_execution_delete")
	deleteShowtime.SetSourceKey("show_execution_delete")
	deleteStart := terminalStart.Add(10 * time.Minute)
	deleteShowtime.SetStartsAt(timestamppb.New(deleteStart))
	deleteShowtime.SetEndsAt(timestamppb.New(deleteStart.Add(150 * time.Minute)))
	seedClientResourceCatalog(t, store, providerID, theaterID, auditoriumID, movieID, deleteShowtime)
	deleteCapture := &observationpb.Capture{}
	deleteCapture.SetTargetDate(targetDateMessage)
	deleteCapture.SetComplete(true)
	deleteCapture.SetObservedAt(timestamppb.Now())
	deleteCapture.SetShowtimes([]*catalogpb.Showtime{deleteShowtime})
	deleteCompleted := &observationpb.Completed{}
	deleteCompleted.SetCaptures([]*observationpb.Capture{deleteCapture})
	deleteResult := &observationpb.AssignmentResult{}
	deleteResult.SetCompleted(deleteCompleted)
	tx, beginErr = store.pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if err := enqueueClientExecutions(ctx, tx, central.ResultCommit{
		CommittedAt: deleteCapture.GetObservedAt().AsTime(), Result: deleteResult,
	}, preset.GetTheaterId(), "Asia/Seoul"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	deleteClaimResponse, err := service.ClaimExecution(ctx, principal, claimA)
	deleteCommand := deleteClaimResponse.GetCommand()
	if err != nil || deleteCommand == nil {
		t.Fatalf("delete execution claim = %+v, %v", deleteClaimResponse, err)
	}
	deleteRevision := rearmedMonitor.GetIdentity().GetRevision()
	if _, err := service.DeleteResource(
		ctx, principal, "monitors", monitor.GetId(), &deleteRevision, "delete_monitor_with_execution",
	); err != nil {
		t.Fatal(err)
	}
	deleteHeartbeat := &executionpb.HeartbeatRequest{}
	deleteHeartbeat.SetCommandId(deleteCommand.GetId())
	deleteHeartbeat.SetLeaseToken(deleteCommand.GetLeaseToken())
	if _, err := service.HeartbeatExecution(ctx, principal, deleteHeartbeat); !errors.Is(err, central.ErrLeaseExpired) {
		t.Fatalf("deleted monitor execution heartbeat = %v", err)
	}
	var deleteStatus, deleteReason string
	if err := store.pool.QueryRow(ctx, `
		SELECT status, reason_code FROM client_execution_commands WHERE id = $1
	`, deleteCommand.GetId()).Scan(&deleteStatus, &deleteReason); err != nil ||
		deleteStatus != "failed" || deleteReason != "monitor_deleted" {
		t.Fatalf("deleted monitor command = %q/%q, %v", deleteStatus, deleteReason, err)
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
	capability := &observationpb.Capability{}
	capability.SetScheduleCapture(&observationpb.ScheduleCapture{})
	kind := &probepb.ProbeKind{}
	kind.SetClient(&probepb.ClientProbe{})
	runtime := &commonpb.Runtime{}
	runtime.SetComponentVersion("1.0.0")
	runtime.SetBrowserRevision("1228")
	runtime.SetPlatform("darwin")
	runtime.SetArchitecture("arm64")
	registration := &probepb.RegisterRequest{}
	registration.SetInstallationId(installationID)
	registration.SetKind(kind)
	registration.SetNetworkHint("203.0.113.10:443")
	registration.SetCapabilities([]*observationpb.Capability{capability})
	registration.SetMaxConcurrency(1)
	registration.SetRuntime(runtime)
	ticket, err := signer.Issue(bootstrap.Claims{
		UserID: userID, TicketID: ticketID, InstallationID: installationID, DeviceID: deviceID,
		Kind: "client", Capabilities: []string{"cgv.schedule.capture"},
		MaxConcurrency: 1, RuntimeVersion: runtime.GetComponentVersion(), BrowserRevision: runtime.GetBrowserRevision(),
		Platform: runtime.GetPlatform(), Architecture: runtime.GetArchitecture(),
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
	`, registered.GetProbeId()).Scan(&storedUserID, &storedDeviceID); err != nil {
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
	theaterID := catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", theaterSourceKey)

	cleanupIntegrationRows(t, store, probeID, assignmentID)
	t.Cleanup(func() { cleanupIntegrationRows(t, store, probeID, assignmentID) })

	registered, err := store.RegisterProbe(ctx, central.Probe{
		ID: probeID, InstallationID: installationID, Kind: "container", NetworkID: "net_integration",
		Capabilities: []string{"cgv.schedule.capture"}, MaxConcurrency: 1,
		Runtime:   storeIntegrationRuntime("integration", "integration", "linux", "amd64"),
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
	if _, err := store.HeartbeatProbe(ctx, probeID, storeIntegrationHeartbeat(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(catalogdomain.ProviderCGV)
	theater.SetSourceKey(theaterSourceKey)
	theater.SetRegion("서울")
	theater.SetName("통합 시험관")
	task := storeIntegrationScheduleTask(theater, "2026-08-20", "ko-KR", "Asia/Seoul")
	taskData, err := protojson.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO observation_assignments (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_dates,
			locale, time_zone, egress_policy_id, status, not_before, deadline, created_at, updated_at, task_data
		) VALUES ($1, 'cgv.schedule.capture', $2, $3, $4, '서울', '통합 시험관',
			ARRAY['2026-08-20'::date], 'ko-KR', 'Asia/Seoul', 'scan_default', 'queued', $5, $6, $5, $5, $7::jsonb)
	`, assignmentID, theaterID, catalogdomain.ProviderCGV, theaterSourceKey,
		now.Add(-time.Minute), now.Add(time.Hour), taskData); err != nil {
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
	if assignment.ID != assignmentID || assignment.Task.GetSchedule().GetTheater().GetId() != theaterID {
		t.Fatalf("claimed assignment = %+v", assignment)
	}
	if err := store.HeartbeatAssignment(
		ctx, assignmentID, probeID, leaseHash, now.Add(time.Second), now.Add(2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}

	result := integrationAssignmentResult(assignment.Task.GetSchedule().GetTheater(), "2026-08-20", now)
	payload, err := protojson.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest := sha256.Sum256(payload)
	commit := central.ResultCommit{
		AssignmentID: assignmentID, ProbeID: probeID, LeaseHash: leaseHash,
		PayloadHash: hex.EncodeToString(payloadDigest[:]), Result: result,
		CommittedAt: now.Add(11 * time.Second),
	}
	receipt, err := store.CommitResult(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.CommitResult(ctx, commit)
	if err != nil || repeated.GetAssignmentId() != receipt.GetAssignmentId() ||
		repeated.GetRunId() != receipt.GetRunId() || repeated.GetContentHash() != receipt.GetContentHash() ||
		receipt.GetAccepted() == nil || repeated.GetDuplicate() == nil {
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
	var observedMovieID string
	if err := store.pool.QueryRow(ctx, `
		SELECT movie_id FROM showtime_observations WHERE assignment_id = $1
	`, assignmentID).Scan(&observedMovieID); err != nil {
		t.Fatal(err)
	}
	if expected := result.GetCompleted().GetCaptures()[0].GetShowtimes()[0].GetMovie().GetId(); observedMovieID != expected {
		t.Fatalf("observation movie ID = %q, want %q", observedMovieID, expected)
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
	if !report.GetLeader() || report.GetCreatedAssignments() != 1 {
		t.Fatalf("initial reconcile report = %+v", report)
	}
	retryAssignment := assignmentForPolicy(t, store, policyIDs[0])
	if retryAssignment.Status != "queued" || len(retryAssignment.Task.GetSchedule().GetTargetDates()) != 1 {
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
	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], storeIntegrationHeartbeat(), time.Now().UTC()); err != nil {
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

	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], storeIntegrationHeartbeat(), time.Now().UTC()); err != nil {
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

	if _, err := store.HeartbeatProbe(ctx, probeIDs[0], storeIntegrationHeartbeat(), time.Now().UTC()); err != nil {
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
			) VALUES ($1, $2, 'cgv.schedule.capture', $3, 'cgv', $4, '서울', '용산아이파크몰',
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
	if value.GetSnapshotCount() != 4 || value.GetShowtimeObservations() != 3 || len(value.GetOpeningPatterns()) != 1 ||
		len(value.GetDemandPatterns()) != 1 || !value.GetLastObservedAt().AsTime().Equal(observedTimes[3]) {
		t.Fatalf("schedule intelligence summary = %+v", value)
	}
	opening := value.GetOpeningPatterns()[0]
	if opening.GetSampleSize() != 1 || opening.GetTypicalOpenTime() != "09:05" ||
		opening.GetTypicalPrecisionMinutes() != 10 || opening.GetAuditoriumId() != "imax" {
		t.Fatalf("opening pattern = %+v", opening)
	}
	demand := value.GetDemandPatterns()[0]
	if demand.GetOccurrenceCount() != 1 || demand.GetTypicalFirstHourSellThrough() != 60 ||
		demand.GetTypicalHalfSoldMinutes() != 60 || demand.GetTypicalSoldOutMinutes() != 120 {
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
	if report.GetCreatedAssignments() != 2 {
		t.Fatalf("created assignments = %d, want 2", report.GetCreatedAssignments())
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
	const (
		userID  = "user_lane_priority"
		movieID = "movie_lane_priority"
	)
	auditoriumIDs := []string{
		"auditorium_lane_demand",
		"auditorium_lane_cancellation",
		"auditorium_lane_baseline",
	}
	theaterIDs := []string{
		catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", "theater_lane_demand"),
		catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", "theater_lane_cancellation"),
		catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", "theater_lane_baseline"),
	}
	cleanup := func() {
		if _, cleanupErr := store.pool.Exec(ctx, `DELETE FROM client_events WHERE user_id = $1`, userID); cleanupErr != nil {
			t.Errorf("delete lane events: %v", cleanupErr)
		}
		if _, cleanupErr := store.pool.Exec(ctx, `DELETE FROM client_commands WHERE user_id = $1`, userID); cleanupErr != nil {
			t.Errorf("delete lane commands: %v", cleanupErr)
		}
		if _, cleanupErr := store.pool.Exec(ctx, `DELETE FROM client_resources WHERE user_id = $1`, userID); cleanupErr != nil {
			t.Errorf("delete lane resources: %v", cleanupErr)
		}
		cleanupClientResourceCatalog(
			t,
			store,
			catalogdomain.ProviderCGV,
			theaterIDs,
			auditoriumIDs,
			[]string{movieID},
		)
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
		horizon_days = CASE WHEN id = $4 THEN 21 ELSE horizon_days END,
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
	for index, theaterID := range []string{demandTheaterID, cancellationTheaterID, baselineTheaterID} {
		seedClientResourceCatalog(
			t,
			store,
			catalogdomain.ProviderCGV,
			theaterID,
			auditoriumIDs[index],
			movieID,
		)
	}
	clientService, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	principal := central.ClientPrincipal{UserID: userID}
	resources := []struct {
		kind string
		id   string
		body *clientpb.Resource
	}{
		{
			kind: "presets",
			id:   "preset_lane",
			body: storeIntegrationNamedPresetResource(
				userID,
				"preset_lane",
				"lane",
				demandTheaterID,
				auditoriumIDs[0],
			),
		},
		{
			kind: "presets",
			id:   "preset_cancellation",
			body: storeIntegrationNamedPresetResource(
				userID,
				"preset_cancellation",
				"cancellation",
				cancellationTheaterID,
				auditoriumIDs[1],
			),
		},
		{
			kind: "presets",
			id:   "preset_triggered",
			body: storeIntegrationNamedPresetResource(
				userID,
				"preset_triggered",
				"triggered",
				baselineTheaterID,
				auditoriumIDs[2],
			),
		},
		{
			kind: "monitors",
			id:   "monitor_lane",
			body: storeIntegrationTypedMonitorResource(
				userID,
				"monitor_lane",
				"preset_lane",
				movieID,
				"pending",
			),
		},
		{
			kind: "monitors",
			id:   "monitor_cancellation",
			body: storeIntegrationTypedMonitorResource(
				userID,
				"monitor_cancellation",
				"preset_cancellation",
				movieID,
				"pending",
			),
		},
		{
			kind: "monitors",
			id:   "monitor_triggered",
			body: storeIntegrationTypedMonitorResource(
				userID,
				"monitor_triggered",
				"preset_triggered",
				movieID,
				"triggered",
			),
		},
	}
	for _, resource := range resources {
		if _, err := clientService.PutResource(
			ctx,
			principal,
			resource.kind,
			resource.id,
			resource.body,
			nil,
			"create_"+resource.id,
		); err != nil {
			t.Fatalf("create %s/%s: %v", resource.kind, resource.id, err)
		}
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
	if due[3].ID != policyIDs[3] || due[3].Priority != 10 {
		t.Fatalf("baseline policy = %+v", due[3])
	}
	if due[0].MinimumInterval != 2*time.Second || due[0].MaximumInterval != 5*time.Second ||
		due[1].MinimumInterval != 15*time.Second || due[1].MaximumInterval != 30*time.Second ||
		due[2].MinimumInterval != 30*time.Second || due[2].MaximumInterval != 45*time.Second ||
		due[3].MinimumInterval != 5*time.Minute || due[3].MaximumInterval != 15*time.Minute ||
		due[3].HorizonDays != 14 {
		t.Fatalf("automatic observation cadence = %+v", due[:3])
	}
	if due[0].Theater.GetId() != demandTheaterID || due[1].Theater.GetId() != burstTheaterID ||
		due[2].Theater.GetId() != cancellationTheaterID {
		t.Fatalf("unexpected lane theaters: demand=%q burst=%q", due[0].Theater.GetId(), due[1].Theater.GetId())
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
		theater := &catalogpb.Theater{}
		theater.SetId("system-catalog")
		theater.SetProviderId(catalogdomain.ProviderCGV)
		theater.SetSourceKey("__catalog__")
		theater.SetRegion("system")
		theater.SetName("CGV catalog")
		catalogTask := &observationpb.CatalogTask{}
		catalogTask.SetTheater(theater)
		catalogTask.SetTargetDates([]*commonpb.LocalDate{localDateMessage(now.Format(time.DateOnly))})
		catalogTask.SetLocale("ko-KR")
		catalogTask.SetTimeZone("Asia/Seoul")
		egress := &commonpb.EgressPolicy{}
		egress.SetManagedScan(&commonpb.ManagedScanEgress{})
		task := &observationpb.AssignmentTask{}
		task.SetCatalog(catalogTask)
		task.SetEgress(egress)
		return repository.CreateAssignment(ctx, reconcile.NewAssignment{
			ID: assignmentID, Priority: 100, Status: "queued", NotBefore: now,
			Deadline: now.Add(time.Minute), CreatedAt: now,
			Task: task,
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

func TestPostgresCatalogRefreshRequiresCatalogAssignmentCompletion(t *testing.T) {
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

	fixtureID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	providerID := "catalog_refresh_integration_" + fixtureID
	sourceKey := "catalog-refresh-theater-" + fixtureID
	provider := &catalogpb.Provider{}
	provider.SetId(providerID)
	provider.SetName("Catalog refresh integration")
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(providerID, "theater", sourceKey))
	theater.SetProviderId(providerID)
	theater.SetSourceKey(sourceKey)
	theater.SetRegion("Seoul")
	theater.SetName("Catalog refresh theater")
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetObservedAt(timestamppb.New(time.Now().UTC()))
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM theaters WHERE provider_id = $1`, snapshot.GetProvider().GetId())
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM providers WHERE id = $1`, snapshot.GetProvider().GetId())
	})
	if err := store.RequestCatalogRefresh(ctx, snapshot.GetObservedAt().AsTime()); err != nil {
		t.Fatal(err)
	}
	firstGeneration, err := store.UpsertCatalogSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	replayedGeneration, err := store.UpsertCatalogSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if replayedGeneration != firstGeneration {
		t.Fatalf("identical catalog replay changed generation: first=%d replay=%d", firstGeneration, replayedGeneration)
	}
	var requestedAt *time.Time
	if err := store.pool.QueryRow(ctx, `SELECT refresh_requested_at FROM catalog_state WHERE id = 1`).Scan(&requestedAt); err != nil {
		t.Fatal(err)
	}
	if requestedAt == nil {
		t.Fatal("partial catalog write cleared the pending full refresh")
	}
	empty := proto.CloneOf(snapshot)
	empty.SetTheaters(nil)
	invalidCompleted := &observationpb.Completed{}
	invalidCompleted.SetCatalog(empty)
	invalidResult := &observationpb.AssignmentResult{}
	invalidResult.SetCompleted(invalidCompleted)
	invalidTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeCatalogResult(ctx, invalidTx, invalidResult); !errors.Is(err, central.ErrInvalid) {
		_ = invalidTx.Rollback(ctx)
		t.Fatalf("provider-only full catalog result = %v", err)
	}
	if err := invalidTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT refresh_requested_at FROM catalog_state WHERE id = 1`).Scan(&requestedAt); err != nil {
		t.Fatal(err)
	}
	if requestedAt == nil {
		t.Fatal("invalid provider-only catalog result cleared the pending refresh")
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	completed := &observationpb.Completed{}
	completed.SetCatalog(snapshot)
	result := &observationpb.AssignmentResult{}
	result.SetCompleted(completed)
	if err := storeCatalogResult(ctx, tx, result); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT refresh_requested_at FROM catalog_state WHERE id = 1`).Scan(&requestedAt); err != nil {
		t.Fatal(err)
	}
	if requestedAt != nil {
		t.Fatalf("completed catalog assignment left refresh pending at %v", requestedAt)
	}
}

func TestPostgresCatalogRetainsMovieHistoryOutsideClientProjection(t *testing.T) {
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
	fixtureID := fmt.Sprintf("%d", now.UnixNano())
	providerID := "catalog_history_integration_" + fixtureID
	theater := &catalogpb.Theater{}
	theater.SetId(catalogdomain.CatalogID(providerID, "theater", "theater"))
	theater.SetProviderId(providerID)
	theater.SetSourceKey("theater")
	theater.SetRegion("Seoul")
	theater.SetName("History theater")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(catalogdomain.CatalogID(providerID, "auditorium", "auditorium"))
	auditorium.SetTheaterId(theater.GetId())
	auditorium.SetSourceKey("auditorium")
	auditorium.SetName("History auditorium")
	auditorium.SetScreenTypes([]string{"STANDARD"})
	auditorium.SetCapacity(100)
	pastMovie := &catalogpb.Movie{}
	pastMovie.SetId(catalogdomain.CatalogID(providerID, "movie", "past"))
	pastMovie.SetProviderId(providerID)
	pastMovie.SetSourceKey("past")
	pastMovie.SetTitle("Past movie")
	futureMovie := &catalogpb.Movie{}
	futureMovie.SetId(catalogdomain.CatalogID(providerID, "movie", "future"))
	futureMovie.SetProviderId(providerID)
	futureMovie.SetSourceKey("future")
	futureMovie.SetTitle("Future movie")
	pastShowtime := &catalogpb.Showtime{}
	pastShowtime.SetId(catalogdomain.CatalogID(providerID, "showtime", "past"))
	pastShowtime.SetProviderId(providerID)
	pastShowtime.SetSourceKey("past")
	pastShowtime.SetTheaterId(theater.GetId())
	pastShowtime.SetMovie(pastMovie)
	pastShowtime.SetAuditorium(auditorium)
	pastShowtime.SetStartsAt(timestamppb.New(now.Add(-2 * time.Hour)))
	pastShowtime.SetEndsAt(timestamppb.New(now.Add(-time.Hour)))
	futureShowtime := &catalogpb.Showtime{}
	futureShowtime.SetId(catalogdomain.CatalogID(providerID, "showtime", "future"))
	futureShowtime.SetProviderId(providerID)
	futureShowtime.SetSourceKey("future")
	futureShowtime.SetTheaterId(theater.GetId())
	futureShowtime.SetMovie(futureMovie)
	futureShowtime.SetAuditorium(auditorium)
	futureShowtime.SetStartsAt(timestamppb.New(now.Add(time.Hour)))
	futureShowtime.SetEndsAt(timestamppb.New(now.Add(2 * time.Hour)))
	provider := &catalogpb.Provider{}
	provider.SetId(providerID)
	provider.SetName("Catalog history integration")
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetMovies([]*catalogpb.Movie{pastMovie, futureMovie})
	snapshot.SetAuditoriums([]*catalogpb.Auditorium{auditorium})
	snapshot.SetShowtimes([]*catalogpb.Showtime{pastShowtime, futureShowtime})
	snapshot.SetObservedAt(timestamppb.New(now))
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM showtimes WHERE provider_id = $1`, providerID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM auditoriums WHERE theater_id = $1`, theater.GetId())
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM movies WHERE provider_id = $1`, providerID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM theaters WHERE provider_id = $1`, providerID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM providers WHERE id = $1`, providerID)
	})
	if _, err := store.UpsertCatalogSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}

	var retainedMovies int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM movies WHERE provider_id = $1`, providerID).Scan(&retainedMovies); err != nil {
		t.Fatal(err)
	}
	if retainedMovies != 2 {
		t.Fatalf("retained movie rows = %d, want 2", retainedMovies)
	}
	catalog, err := store.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, movie := range catalog.GetMovies() {
		if movie.GetId() == pastMovie.GetId() {
			t.Fatal("historical movie leaked into Client catalog")
		}
	}
	if !slices.ContainsFunc(catalog.GetMovies(), func(movie *catalogpb.Movie) bool { return movie.GetId() == futureMovie.GetId() }) {
		t.Fatal("future movie missing from Client catalog")
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
		Capabilities: []string{"cgv.schedule.capture"}, MaxConcurrency: 1,
		Runtime:   storeIntegrationRuntime("1.0.0", "2000", "linux", "amd64"),
		TokenHash: tokenHash, TokenExpiresAt: now.Add(time.Hour), Status: "online", Health: "healthy",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HeartbeatProbe(context.Background(), probeID, storeIntegrationHeartbeat(), now); err != nil {
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
	theaterID := catalogdomain.CatalogID(catalogdomain.ProviderCGV, "theater", theaterSourceKey)
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO observation_policies (
			id, task_kind, theater_id, theater_provider_id, theater_source_key,
			theater_region, theater_name, target_date_mode, target_dates,
			horizon_days, locale, time_zone, egress_policy_id, priority, min_interval_seconds,
			max_interval_seconds, execution_window_seconds, next_run_at, created_at, updated_at
		) VALUES (
			$1, 'cgv.schedule.capture', $2, $3, $4, '서울', '통합 시험관', 'rolling', '{}',
			2, 'ko-KR', 'Asia/Seoul', 'scan_default', 50, 60, 61, 120, $5, $6, $6
		)
	`, policyID, theaterID, catalogdomain.ProviderCGV, theaterSourceKey, nextRunAt, now); err != nil {
		t.Fatal(err)
	}
	return theaterID
}

func assignmentForPolicy(t *testing.T, store *Store, policyID string) central.Assignment {
	t.Helper()
	assignment, err := scanAssignment(store.pool.QueryRow(context.Background(), `
		SELECT id, status, not_before, deadline, probe_id, lease_expires_at,
			created_at, updated_at, task_data
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

func storeIntegrationResourceIdentity(id string) *commonpb.ResourceIdentity {
	identity := &commonpb.ResourceIdentity{}
	identity.SetId(id)
	return identity
}

func storeIntegrationNamedPresetResource(
	userID string,
	id string,
	name string,
	theaterID string,
	auditoriumID string,
) *clientpb.Resource {
	preset := &clientpb.Preset{}
	preset.SetId(id)
	preset.SetUserId(userID)
	preset.SetName(name)
	preset.SetTheaterId(theaterID)
	preset.SetAuditoriumId(auditoriumID)
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	resource := &clientpb.Resource{}
	resource.SetIdentity(storeIntegrationResourceIdentity(id))
	resource.SetPreset(preset)
	return resource
}

func storeIntegrationMonitorResource(userID, id, presetID, movieID string) *clientpb.Resource {
	return storeIntegrationTypedMonitorResource(userID, id, presetID, movieID, "pending")
}

func storeIntegrationTypedMonitorResource(
	userID string,
	id string,
	presetID string,
	movieID string,
	stateName string,
) *clientpb.Resource {
	targetDate := &commonpb.LocalDate{}
	targetDate.SetYear(2026)
	targetDate.SetMonth(8)
	targetDate.SetDay(20)
	state := &clientpb.MonitorState{}
	switch stateName {
	case "pending":
		state.SetPending(&clientpb.MonitorPending{})
	case "triggered":
		state.SetTriggered(&clientpb.MonitorTriggered{})
	default:
		panic("unsupported integration monitor state: " + stateName)
	}
	monitor := &clientpb.Monitor{}
	monitor.SetId(id)
	monitor.SetUserId(userID)
	monitor.SetPresetId(presetID)
	monitor.SetMovieId(movieID)
	monitor.SetMovieTitle("Movie")
	monitor.SetTargetDates([]*commonpb.LocalDate{targetDate})
	monitor.SetSearchHorizonDays(14)
	monitor.SetState(state)
	resource := &clientpb.Resource{}
	resource.SetIdentity(storeIntegrationResourceIdentity(id))
	resource.SetMonitor(monitor)
	return resource
}

func seedClientResourceCatalog(
	t *testing.T,
	store *Store,
	providerID string,
	theaterID string,
	auditoriumID string,
	movieID string,
	showtimes ...*catalogpb.Showtime,
) {
	t.Helper()
	provider := &catalogpb.Provider{}
	provider.SetId(providerID)
	providerName := "Integration provider"
	if providerID == catalogdomain.ProviderCGV {
		providerName = "CGV"
	}
	provider.SetName(providerName)
	theater := &catalogpb.Theater{}
	theater.SetId(theaterID)
	theater.SetProviderId(providerID)
	theater.SetSourceKey(theaterID)
	theater.SetRegion("Seoul")
	theater.SetName("Integration theater")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(theaterID)
	auditorium.SetSourceKey(auditoriumID)
	auditorium.SetName("Integration auditorium")
	auditorium.SetScreenTypes([]string{"STANDARD"})
	auditorium.SetCapacity(100)
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(providerID)
	movie.SetSourceKey(movieID)
	movie.SetTitle("Integration movie")
	snapshot := &catalogpb.CatalogSnapshot{}
	snapshot.SetProvider(provider)
	snapshot.SetTheaters([]*catalogpb.Theater{theater})
	snapshot.SetMovies([]*catalogpb.Movie{movie})
	snapshot.SetAuditoriums([]*catalogpb.Auditorium{auditorium})
	snapshot.SetShowtimes(showtimes)
	snapshot.SetObservedAt(timestamppb.Now())
	if _, err := store.UpsertCatalogSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("seed Client resource catalog: %v", err)
	}
}

func cleanupClientResourceCatalog(
	t *testing.T,
	store *Store,
	providerID string,
	theaterIDs []string,
	auditoriumIDs []string,
	movieIDs []string,
) {
	t.Helper()
	ctx := context.Background()
	for _, fixture := range []struct {
		query string
		ids   []string
	}{
		{`DELETE FROM showtimes WHERE theater_id = ANY($1)`, theaterIDs},
		{`DELETE FROM auditoriums WHERE id = ANY($1)`, auditoriumIDs},
		{`DELETE FROM movies WHERE id = ANY($1)`, movieIDs},
		{`DELETE FROM theaters WHERE id = ANY($1)`, theaterIDs},
	} {
		if len(fixture.ids) == 0 {
			continue
		}
		if _, err := store.pool.Exec(ctx, fixture.query, fixture.ids); err != nil {
			t.Errorf("clean Client resource catalog fixture: %v", err)
		}
	}
	if providerID == "" || providerID == catalogdomain.ProviderCGV {
		return
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, providerID); err != nil {
		t.Errorf("clean Client resource provider fixture: %v", err)
	}
}

func storeIntegrationSettingsResource() *clientpb.Resource {
	resource := &clientpb.Resource{}
	resource.SetIdentity(storeIntegrationResourceIdentity("settings"))
	resource.SetSettings(&clientpb.Settings{})
	return resource
}

func storeIntegrationResource(id string, preset *clientpb.Preset, monitor *clientpb.Monitor) *clientpb.Resource {
	resource := &clientpb.Resource{}
	resource.SetIdentity(storeIntegrationResourceIdentity(id))
	if preset != nil {
		resource.SetPreset(preset)
	}
	if monitor != nil {
		resource.SetMonitor(monitor)
	}
	return resource
}

func localDateMessage(value string) *commonpb.LocalDate {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return &commonpb.LocalDate{}
	}
	date := &commonpb.LocalDate{}
	date.SetYear(numeric.ClampInt32(parsed.Year()))
	date.SetMonth(numeric.ClampInt32(int(parsed.Month())))
	date.SetDay(numeric.ClampInt32(parsed.Day()))
	return date
}

func executionIntegrationShowtime(
	providerID string,
	theaterID string,
	movieID string,
	auditoriumID string,
	startsAt time.Time,
) *catalogpb.Showtime {
	movie := &catalogpb.Movie{}
	movie.SetId(movieID)
	movie.SetProviderId(providerID)
	movie.SetSourceKey(movieID)
	movie.SetTitle("Execution Movie")
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(auditoriumID)
	auditorium.SetTheaterId(theaterID)
	auditorium.SetSourceKey(auditoriumID)
	auditorium.SetName("IMAX관")
	auditorium.SetScreenTypes([]string{"IMAX"})
	auditorium.SetCapacity(624)
	showtime := &catalogpb.Showtime{}
	showtime.SetId("show_execution")
	showtime.SetProviderId(providerID)
	showtime.SetSourceKey("show_execution")
	showtime.SetTheaterId(theaterID)
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetStartsAt(timestamppb.New(startsAt))
	showtime.SetEndsAt(timestamppb.New(startsAt.Add(150 * time.Minute)))
	showtime.SetAvailableSeats(300)
	showtime.SetCapacity(624)
	return showtime
}

func storeIntegrationRuntime(version, browserRevision, platform, architecture string) *commonpb.Runtime {
	runtime := &commonpb.Runtime{}
	runtime.SetComponentVersion(version)
	runtime.SetBrowserRevision(browserRevision)
	runtime.SetPlatform(platform)
	runtime.SetArchitecture(architecture)
	return runtime
}

func storeIntegrationHeartbeat() *probepb.HeartbeatRequest {
	capability := &observationpb.Capability{}
	capability.SetScheduleCapture(&observationpb.ScheduleCapture{})
	health := &probepb.ProbeHealth{}
	health.SetHealthy(&probepb.Healthy{})
	heartbeat := &probepb.HeartbeatRequest{}
	heartbeat.SetAvailableCapabilities([]*observationpb.Capability{capability})
	heartbeat.SetAvailableSlots(1)
	heartbeat.SetHealth(health)
	return heartbeat
}

func storeIntegrationScheduleTask(theater *catalogpb.Theater, targetDate, locale, timeZone string) *observationpb.AssignmentTask {
	schedule := &observationpb.ScheduleTask{}
	schedule.SetTheater(theater)
	schedule.SetTargetDates([]*commonpb.LocalDate{localDateMessage(targetDate)})
	schedule.SetLocale(locale)
	schedule.SetTimeZone(timeZone)
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task := &observationpb.AssignmentTask{}
	task.SetSchedule(schedule)
	task.SetEgress(egress)
	return task
}

func integrationResultCommit(
	t *testing.T,
	assignment central.Assignment,
	probeID string,
	leaseHash [32]byte,
) central.ResultCommit {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	schedule := assignment.Task.GetSchedule()
	targetDate := schedule.GetTargetDates()[0]
	theater := schedule.GetTheater()
	result := integrationAssignmentResult(theater, localDateString(targetDate), now)
	result.SetRunId("run_reconcile")
	result.SetStartedAt(timestamppb.New(now.Add(-2 * time.Second)))
	result.SetFinishedAt(timestamppb.New(now.Add(-time.Second)))
	payload, err := protojson.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return central.ResultCommit{
		AssignmentID: assignment.ID, ProbeID: probeID, LeaseHash: leaseHash,
		PayloadHash: hex.EncodeToString(digest[:]), Result: result, CommittedAt: now,
	}
}

func integrationAssignmentResult(theater *catalogpb.Theater, targetDate string, now time.Time) *observationpb.AssignmentResult {
	capture := &observationpb.Capture{}
	capture.SetTargetDate(localDateMessage(targetDate))
	capture.SetComplete(true)
	capture.SetObservedAt(timestamppb.New(now.Add(-time.Second)))
	capture.SetShowtimes([]*catalogpb.Showtime{integrationShowtime(theater, now)})
	completed := &observationpb.Completed{}
	completed.SetCaptures([]*observationpb.Capture{capture})
	result := &observationpb.AssignmentResult{}
	result.SetCompleted(completed)
	return result
}

func integrationShowtime(theater *catalogpb.Theater, now time.Time) *catalogpb.Showtime {
	movieSourceKey := "00001234"
	movie := &catalogpb.Movie{}
	movie.SetId(catalogdomain.CatalogID(theater.GetProviderId(), "movie", movieSourceKey))
	movie.SetProviderId(theater.GetProviderId())
	movie.SetSourceKey(movieSourceKey)
	movie.SetTitle("통합 시험 영화")
	auditoriumSourceKey := theater.GetSourceKey() + "/0007"
	auditorium := &catalogpb.Auditorium{}
	auditorium.SetId(catalogdomain.CatalogID(theater.GetProviderId(), "auditorium", auditoriumSourceKey))
	auditorium.SetTheaterId(theater.GetId())
	auditorium.SetSourceKey(auditoriumSourceKey)
	auditorium.SetName("IMAX관")
	auditorium.SetScreenTypes([]string{"IMAX"})
	auditorium.SetCapacity(624)
	showtimeSourceKey := theater.GetSourceKey() + "/2026-08-20/0007/0003"
	showtime := &catalogpb.Showtime{}
	showtime.SetId(catalogdomain.CatalogID(theater.GetProviderId(), "showtime", showtimeSourceKey))
	showtime.SetProviderId(theater.GetProviderId())
	showtime.SetSourceKey(showtimeSourceKey)
	showtime.SetTheaterId(theater.GetId())
	showtime.SetMovie(movie)
	showtime.SetAuditorium(auditorium)
	showtime.SetStartsAt(timestamppb.New(now.Add(24 * time.Hour)))
	showtime.SetEndsAt(timestamppb.New(now.Add(26 * time.Hour)))
	showtime.SetAvailableSeats(500)
	showtime.SetCapacity(624)
	return showtime
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
