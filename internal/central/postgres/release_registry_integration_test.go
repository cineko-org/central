package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
)

func TestPostgresReleaseRegistry(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	const marker = "registry-integration"
	cleanup := func() {
		if _, cleanupErr := store.pool.Exec(context.Background(), `
			DELETE FROM release_components
			WHERE payload->'payload'->>'integrationMarker' = $1
				OR payload->>'integrationMarker' = $1
				OR version IN ('91.0.0', '91.61.1', '991234')
		`, marker); cleanupErr != nil {
			t.Errorf("release registry cleanup: %v", cleanupErr)
		}
		records, _, cleanupErr := store.ListReleases(context.Background())
		if cleanupErr != nil {
			t.Errorf("list release registry after cleanup: %v", cleanupErr)
			return
		}
		fingerprint, cleanupErr := central.ActiveDesktopManifestFingerprint(records)
		if cleanupErr != nil {
			t.Errorf("resolve release registry after cleanup: %v", cleanupErr)
			return
		}
		if _, cleanupErr = store.pool.Exec(context.Background(), `
			UPDATE desktop_release_registry_state
			SET active_manifest_sha256 = $1, updated_at = now()
			WHERE singleton = true
		`, fingerprint); cleanupErr != nil {
			t.Errorf("repair release registry cleanup state: %v", cleanupErr)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	_, initialGeneration, err := store.ListReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service, err := central.NewClientService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	clients := integrationClientReleaseSet(publishedAt)
	browsers := integrationBrowserReleaseSet(publishedAt)
	playwright := integrationPlaywrightReleaseSet(publishedAt)
	launchers := integrationLauncherReleaseSet(publishedAt)
	probe := integrationProbeRelease(publishedAt)

	generation, inserted, err := service.PublishReleaseSet(ctx, "probe", []central.ProbeRelease{probe})
	if err != nil || !inserted || generation != initialGeneration {
		t.Fatalf("Probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	for _, publication := range []struct {
		kind    string
		release any
		want    int64
	}{
		{kind: "client", release: clients, want: initialGeneration},
		{kind: "browser", release: browsers, want: initialGeneration},
		{kind: "playwright", release: playwright, want: initialGeneration},
		{kind: "launcher", release: launchers, want: initialGeneration + 1},
	} {
		generation, inserted, err = service.PublishReleaseSet(ctx, publication.kind, publication.release)
		if err != nil || !inserted || generation != publication.want {
			t.Fatalf("%s publish = generation %d, inserted %v, error %v", publication.kind, generation, inserted, err)
		}
	}
	generation, inserted, err = service.PublishReleaseSet(ctx, "launcher", launchers)
	if err != nil || inserted || generation != initialGeneration+1 {
		t.Fatalf("idempotent Launcher publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	storedRecords, _, err := store.ListReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyEquivalent := make([]central.ReleaseRecord, 0, len(launchers))
	for _, record := range storedRecords {
		if record.Kind != "launcher" || record.Channel != "stable" || record.Version != launchers[0].Version {
			continue
		}
		if record.SchemaVersion != 2 {
			t.Fatalf("stored Launcher schema version = %d", record.SchemaVersion)
		}
		var envelope struct {
			SchemaVersion int             `json:"schemaVersion"`
			Payload       json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(record.Payload, &envelope); err != nil || envelope.SchemaVersion != 2 || len(envelope.Payload) == 0 {
			t.Fatalf("stored Launcher envelope = %s, %+v, %v", record.Payload, envelope, err)
		}
		record.SchemaVersion = 1
		record.Payload = envelope.Payload
		legacyEquivalent = append(legacyEquivalent, record)
	}
	if len(legacyEquivalent) != len(launchers) {
		t.Fatalf("stored Launcher records = %d", len(legacyEquivalent))
	}
	if _, _, err := store.InsertReleaseSet(ctx, legacyEquivalent); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("cross-schema immutable identity = %v", err)
	}
	conflict := append([]central.LauncherRelease(nil), launchers...)
	conflict[0].Launcher.SHA256 = strings.Repeat("f", 64)
	if _, _, err := service.PublishReleaseSet(ctx, "launcher", conflict); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("immutable identity conflict = %v", err)
	}
	runtimeRelease, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err != nil || runtimeRelease.Client.Version != clients[0].Version ||
		runtimeRelease.Browser.Revision != browsers[0].Revision || runtimeRelease.Playwright.Version != playwright[0].Version {
		t.Fatalf("current runtime = %+v, %v", runtimeRelease, err)
	}
	if current, currentErr := service.CurrentLauncherRelease("stable", "darwin", "arm64"); currentErr != nil || current.Version != launchers[0].Version {
		t.Fatalf("current Launcher = %+v, %v", current, currentErr)
	}
	if current, currentErr := service.CurrentProbeRelease("stable"); currentErr != nil || current.Version != probe.Version {
		t.Fatalf("current Probe = %+v, %v", current, currentErr)
	}

	_, finalGeneration, err := store.ListReleases(ctx)
	if err != nil || finalGeneration != initialGeneration+1 {
		t.Fatalf("active manifest generation = %d, error %v", finalGeneration, err)
	}
}

func integrationClientRelease(publishedAt time.Time) central.ClientRelease {
	return central.ClientRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "91.0.0",
		MinimumLauncherVersion: "91.0.0", MinimumBrowserRevision: "991234",
		PlaywrightVersion: "91.61.1", Protocol: central.ProtocolVersion,
		Artifact: integrationArtifact("client", strings.Repeat("1", 64)),
		ProbeBootstrapPublicKeys: map[string]string{
			"primary": "-----BEGIN PUBLIC KEY-----\nintegration\n-----END PUBLIC KEY-----\n",
		},
		PublishedAt: publishedAt,
	}
}

func integrationClientReleaseSet(publishedAt time.Time) []central.ClientRelease {
	base := integrationClientRelease(publishedAt)
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://downloads.example.com/cineko/releases/client/linux/artifact.zip", "client"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://downloads.example.com/cineko/releases/client/windows/artifact.zip", "client.exe"
	return []central.ClientRelease{base, linux, windows}
}

func integrationBrowserRelease(publishedAt time.Time) central.BrowserRelease {
	return central.BrowserRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Revision: "991234",
		CompatiblePlaywrightVersions: []string{"91.61.1"},
		Artifact:                     integrationArtifact("browser", strings.Repeat("2", 64)),
		PublishedAt:                  publishedAt,
	}
}

func integrationBrowserReleaseSet(publishedAt time.Time) []central.BrowserRelease {
	base := integrationBrowserRelease(publishedAt)
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://downloads.example.com/cineko/releases/browser/linux/artifact.zip", "chrome"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://downloads.example.com/cineko/releases/browser/windows/artifact.zip", "chrome.exe"
	return []central.BrowserRelease{base, linux, windows}
}

func integrationPlaywrightRelease(publishedAt time.Time) central.PlaywrightRelease {
	return central.PlaywrightRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "91.61.1",
		Artifact: integrationArtifact("playwright", strings.Repeat("3", 64)), PublishedAt: publishedAt,
	}
}

func integrationPlaywrightReleaseSet(publishedAt time.Time) []central.PlaywrightRelease {
	base := integrationPlaywrightRelease(publishedAt)
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://downloads.example.com/cineko/releases/playwright/linux/artifact.zip", "playwright"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://downloads.example.com/cineko/releases/playwright/windows/artifact.zip", "playwright.exe"
	return []central.PlaywrightRelease{base, linux, windows}
}

func integrationLauncherRelease(publishedAt time.Time) central.LauncherRelease {
	return central.LauncherRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "91.0.0",
		Protocol: central.ProtocolVersion,
		Launcher: integrationArtifact("launcher", strings.Repeat("4", 64)), PublishedAt: publishedAt,
	}
}

func integrationLauncherReleaseSet(publishedAt time.Time) []central.LauncherRelease {
	base := integrationLauncherRelease(publishedAt)
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Launcher.URL, linux.Launcher.Executable = "https://downloads.example.com/cineko/releases/launcher/linux/artifact.zip", "cineko-launcher"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Launcher.URL, windows.Launcher.Executable = "https://downloads.example.com/cineko/releases/launcher/windows/artifact.zip", "cineko-launcher.exe"
	return []central.LauncherRelease{base, linux, windows}
}

func integrationProbeRelease(publishedAt time.Time) central.ProbeRelease {
	return central.ProbeRelease{
		Channel: "stable", Version: "91.0.0", Protocol: central.ProtocolVersion, BrowserRevision: "991234",
		Image: "registry.example.com/example/cineko-probe", ImageDigest: "sha256:" + strings.Repeat("5", 64),
		PublishedAt: publishedAt,
	}
}

func integrationArtifact(component string, digest string) central.ReleaseArtifact {
	return central.ReleaseArtifact{
		URL:  "https://downloads.example.com/cineko/releases/" + component + "/artifact.zip",
		Size: 1, SHA256: digest, Executable: component,
	}
}
