package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	probeSet := &releasepb.ProbeReleaseSet{}
	probeSet.SetReleases([]*releasepb.ProbeRelease{probe})
	generation, inserted, err := service.PublishReleaseSet(ctx, "probe", probeSet)
	if err != nil || !inserted || generation != initialGeneration {
		t.Fatalf("Probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	for _, publication := range []struct {
		kind    string
		release proto.Message
		want    int64
	}{
		{kind: "client", release: releaseSetClients(clients), want: initialGeneration},
		{kind: "browser", release: releaseSetBrowsers(browsers), want: initialGeneration},
		{kind: "playwright", release: releaseSetPlaywright(playwright), want: initialGeneration},
		{kind: "launcher", release: releaseSetLaunchers(launchers), want: initialGeneration + 1},
	} {
		generation, inserted, err = service.PublishReleaseSet(ctx, publication.kind, publication.release)
		if err != nil || !inserted || generation != publication.want {
			t.Fatalf("%s publish = generation %d, inserted %v, error %v", publication.kind, generation, inserted, err)
		}
	}
	generation, inserted, err = service.PublishReleaseSet(ctx, "launcher", releaseSetLaunchers(launchers))
	if err != nil || inserted || generation != initialGeneration+1 {
		t.Fatalf("idempotent Launcher publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	storedRecords, _, err := store.ListReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	launcherRecords := make([]central.ReleaseRecord, 0, len(launchers))
	for _, record := range storedRecords {
		if record.Kind != "launcher" || record.Channel != "stable" || record.Version != launchers[0].GetVersion() {
			continue
		}
		launcherRecords = append(launcherRecords, record)
	}
	if len(launcherRecords) != len(launchers) {
		t.Fatalf("stored Launcher records = %d", len(launcherRecords))
	}
	if replayGeneration, replayInserted, replayErr := store.InsertReleaseSet(ctx, launcherRecords); replayErr != nil || replayInserted || replayGeneration != initialGeneration+1 {
		t.Fatalf("idempotent immutable identity = generation %d, inserted %v, error %v", replayGeneration, replayInserted, replayErr)
	}
	conflict := proto.CloneOf(launchers[0])
	conflict.GetLauncher().SetSha256(strings.Repeat("f", 64))
	conflictSet := &releasepb.LauncherReleaseSet{}
	conflictSet.SetReleases([]*releasepb.LauncherRelease{
		conflict, launchers[1], launchers[2],
	})
	if _, _, err := service.PublishReleaseSet(ctx, "launcher", conflictSet); !errors.Is(err, central.ErrConflict) {
		t.Fatalf("immutable identity conflict = %v", err)
	}
	runtimeRelease, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err != nil || runtimeRelease.GetClient().GetVersion() != clients[0].GetVersion() ||
		runtimeRelease.GetBrowser().GetRevision() != browsers[0].GetRevision() || runtimeRelease.GetPlaywright().GetVersion() != playwright[0].GetVersion() {
		t.Fatalf("current runtime = %+v, %v", runtimeRelease, err)
	}
	if current, currentErr := service.CurrentLauncherRelease("stable", "darwin", "arm64"); currentErr != nil || current.GetVersion() != launchers[0].GetVersion() {
		t.Fatalf("current Launcher = %+v, %v", current, currentErr)
	}
	if current, currentErr := service.CurrentProbeRelease("stable"); currentErr != nil || current.GetVersion() != probe.GetVersion() {
		t.Fatalf("current Probe = %+v, %v", current, currentErr)
	}

	_, finalGeneration, err := store.ListReleases(ctx)
	if err != nil || finalGeneration != initialGeneration+1 {
		t.Fatalf("active manifest generation = %d, error %v", finalGeneration, err)
	}
}

func integrationClientRelease(publishedAt time.Time, platform, architecture, url, executable string) *releasepb.ClientRelease {
	result := &releasepb.ClientRelease{}
	result.SetChannel("stable")
	result.SetPlatform(platform)
	result.SetArchitecture(architecture)
	result.SetVersion("91.0.0")
	result.SetMinimumLauncherVersion("91.0.0")
	result.SetMinimumBrowserRevision("991234")
	result.SetPlaywrightVersion("91.61.1")
	result.SetArtifact(integrationArtifact(strings.Repeat("1", 64), url, executable))
	result.SetProbeBootstrapPublicKeys(map[string]string{
		"primary": "-----BEGIN PUBLIC KEY-----\nintegration\n-----END PUBLIC KEY-----\n",
	})
	result.SetPublishedAt(timestamppb.New(publishedAt))
	return result
}

func integrationClientReleaseSet(publishedAt time.Time) []*releasepb.ClientRelease {
	return []*releasepb.ClientRelease{
		integrationClientRelease(publishedAt, "darwin", "arm64", "https://downloads.example.com/cineko/releases/client/artifact.zip", "client"),
		integrationClientRelease(publishedAt, "linux", "amd64", "https://downloads.example.com/cineko/releases/client/linux/artifact.zip", "client"),
		integrationClientRelease(publishedAt, "windows", "amd64", "https://downloads.example.com/cineko/releases/client/windows/artifact.zip", "client.exe"),
	}
}

func integrationBrowserRelease(publishedAt time.Time, platform, architecture, url, executable string) *releasepb.BrowserRelease {
	result := &releasepb.BrowserRelease{}
	result.SetChannel("stable")
	result.SetPlatform(platform)
	result.SetArchitecture(architecture)
	result.SetRevision("991234")
	result.SetCompatiblePlaywrightVersions([]string{"91.61.1"})
	result.SetArtifact(integrationArtifact(strings.Repeat("2", 64), url, executable))
	result.SetPublishedAt(timestamppb.New(publishedAt))
	return result
}

func integrationBrowserReleaseSet(publishedAt time.Time) []*releasepb.BrowserRelease {
	return []*releasepb.BrowserRelease{
		integrationBrowserRelease(publishedAt, "darwin", "arm64", "https://downloads.example.com/cineko/releases/browser/artifact.zip", "chrome"),
		integrationBrowserRelease(publishedAt, "linux", "amd64", "https://downloads.example.com/cineko/releases/browser/linux/artifact.zip", "chrome"),
		integrationBrowserRelease(publishedAt, "windows", "amd64", "https://downloads.example.com/cineko/releases/browser/windows/artifact.zip", "chrome.exe"),
	}
}

func integrationPlaywrightRelease(publishedAt time.Time, platform, architecture, url, executable string) *releasepb.PlaywrightRelease {
	result := &releasepb.PlaywrightRelease{}
	result.SetChannel("stable")
	result.SetPlatform(platform)
	result.SetArchitecture(architecture)
	result.SetVersion("91.61.1")
	result.SetArtifact(integrationArtifact(strings.Repeat("3", 64), url, executable))
	result.SetPublishedAt(timestamppb.New(publishedAt))
	return result
}

func integrationPlaywrightReleaseSet(publishedAt time.Time) []*releasepb.PlaywrightRelease {
	return []*releasepb.PlaywrightRelease{
		integrationPlaywrightRelease(publishedAt, "darwin", "arm64", "https://downloads.example.com/cineko/releases/playwright/artifact.zip", "playwright"),
		integrationPlaywrightRelease(publishedAt, "linux", "amd64", "https://downloads.example.com/cineko/releases/playwright/linux/artifact.zip", "playwright"),
		integrationPlaywrightRelease(publishedAt, "windows", "amd64", "https://downloads.example.com/cineko/releases/playwright/windows/artifact.zip", "playwright.exe"),
	}
}

func integrationLauncherRelease(publishedAt time.Time, platform, architecture, url, executable string) *releasepb.LauncherRelease {
	result := &releasepb.LauncherRelease{}
	result.SetChannel("stable")
	result.SetPlatform(platform)
	result.SetArchitecture(architecture)
	result.SetVersion("91.0.0")
	result.SetLauncher(integrationArtifact(strings.Repeat("4", 64), url, executable))
	result.SetPublishedAt(timestamppb.New(publishedAt))
	return result
}

func integrationLauncherReleaseSet(publishedAt time.Time) []*releasepb.LauncherRelease {
	return []*releasepb.LauncherRelease{
		integrationLauncherRelease(publishedAt, "darwin", "arm64", "https://downloads.example.com/cineko/releases/launcher/artifact.zip", "cineko-launcher"),
		integrationLauncherRelease(publishedAt, "linux", "amd64", "https://downloads.example.com/cineko/releases/launcher/linux/artifact.zip", "cineko-launcher"),
		integrationLauncherRelease(publishedAt, "windows", "amd64", "https://downloads.example.com/cineko/releases/launcher/windows/artifact.zip", "cineko-launcher.exe"),
	}
}

func integrationProbeRelease(publishedAt time.Time) *releasepb.ProbeRelease {
	result := &releasepb.ProbeRelease{}
	result.SetChannel("stable")
	result.SetVersion("91.0.0")
	result.SetBrowserRevision("991234")
	result.SetImage("registry.example.com/example/cineko-probe")
	result.SetImageDigest("sha256:" + strings.Repeat("5", 64))
	result.SetPublishedAt(timestamppb.New(publishedAt))
	return result
}

func integrationArtifact(digest, url, executable string) *releasepb.Artifact {
	result := &releasepb.Artifact{}
	result.SetUrl(url)
	result.SetSize(1)
	result.SetSha256(digest)
	result.SetExecutable(executable)
	return result
}

func releaseSetClients(releases []*releasepb.ClientRelease) *releasepb.ClientReleaseSet {
	result := &releasepb.ClientReleaseSet{}
	result.SetReleases(releases)
	return result
}

func releaseSetBrowsers(releases []*releasepb.BrowserRelease) *releasepb.BrowserReleaseSet {
	result := &releasepb.BrowserReleaseSet{}
	result.SetReleases(releases)
	return result
}

func releaseSetPlaywright(releases []*releasepb.PlaywrightRelease) *releasepb.PlaywrightReleaseSet {
	result := &releasepb.PlaywrightReleaseSet{}
	result.SetReleases(releases)
	return result
}

func releaseSetLaunchers(releases []*releasepb.LauncherRelease) *releasepb.LauncherReleaseSet {
	result := &releasepb.LauncherReleaseSet{}
	result.SetReleases(releases)
	return result
}
