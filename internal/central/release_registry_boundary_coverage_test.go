package central

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReleaseSnapshotBoundaries(t *testing.T) {
	t.Parallel()

	fallback, _ := newClientServiceHarness(t)
	fallback.releaseGeneration.Store(7)
	if generation, err := fallback.CurrentReleaseGeneration(t.Context()); err != nil || generation != 7 {
		t.Fatalf("CurrentReleaseGeneration(fallback) = %d, %v", generation, err)
	}
	if err := configureValidRuntime(fallback, validClientRelease()); err != nil {
		t.Fatal(err)
	}
	if err := fallback.ConfigureProbeReleases([]ProbeRelease{validProbeRelease()}); err != nil {
		t.Fatal(err)
	}
	if release, generation, err := fallback.CurrentRuntimeReleaseSnapshot(
		t.Context(), "stable", "darwin", "arm64",
	); err != nil || release.Client.Version == "" || generation != 1 {
		t.Fatalf("CurrentRuntimeReleaseSnapshot(fallback) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := fallback.CurrentLauncherReleaseSnapshot(
		t.Context(), "stable", "darwin", "arm64",
	); err != nil || release.Version == "" || generation != 1 {
		t.Fatalf("CurrentLauncherReleaseSnapshot(fallback) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := fallback.CurrentProbeReleaseSnapshot(
		t.Context(), "stable",
	); err != nil || release.Version == "" || generation != 1 {
		t.Fatalf("CurrentProbeReleaseSnapshot(fallback) = %+v, %d, %v", release, generation, err)
	}
	if _, _, err := fallback.currentReleaseCatalog(t.Context()); err == nil {
		t.Fatal("currentReleaseCatalog() accepted a missing repository")
	}

	records := completeReleaseRegistryRecords(t)
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{}, records: records, generation: 11,
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if release, generation, err := service.CurrentRuntimeReleaseSnapshot(
		t.Context(), "stable", "darwin", "arm64",
	); err != nil || release.Client.Version == "" || generation != 11 {
		t.Fatalf("CurrentRuntimeReleaseSnapshot(repository) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := service.CurrentLauncherReleaseSnapshot(
		t.Context(), "stable", "linux", "amd64",
	); err != nil || release.Version == "" || generation != 11 {
		t.Fatalf("CurrentLauncherReleaseSnapshot(repository) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := service.CurrentProbeReleaseSnapshot(
		t.Context(), "stable",
	); err != nil || release.Version == "" || generation != 11 {
		t.Fatalf("CurrentProbeReleaseSnapshot(repository) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := service.CurrentRuntimeReleaseSnapshot(
		t.Context(), "stable", "linux", "arm64",
	); !errors.Is(err, ErrNotFound) || generation != 11 || release.Client.Version != "" {
		t.Fatalf("CurrentRuntimeReleaseSnapshot(missing target) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := service.CurrentLauncherReleaseSnapshot(
		t.Context(), "stable", "linux", "arm64",
	); !errors.Is(err, ErrNotFound) || generation != 11 || release.Version != "" {
		t.Fatalf("CurrentLauncherReleaseSnapshot(missing target) = %+v, %d, %v", release, generation, err)
	}
	if release, generation, err := service.CurrentProbeReleaseSnapshot(
		t.Context(), "beta",
	); !errors.Is(err, ErrNotFound) || generation != 11 || release.Version != "" {
		t.Fatalf("CurrentProbeReleaseSnapshot(missing channel) = %+v, %d, %v", release, generation, err)
	}

	repository.listErr = errInjectedClient
	if _, _, err := service.CurrentLauncherReleaseSnapshot(
		t.Context(), "stable", "darwin", "arm64",
	); !errors.Is(err, errInjectedClient) {
		t.Fatalf("CurrentLauncherReleaseSnapshot(list error) = %v", err)
	}
	repository.listErr = nil
	repository.records = []ReleaseRecord{{Kind: "unknown", Payload: json.RawMessage(`{}`)}}
	if _, _, err := service.CurrentProbeReleaseSnapshot(t.Context(), "stable"); err == nil {
		t.Fatal("CurrentProbeReleaseSnapshot() accepted corrupt inventory")
	}
	duplicateRecords := append([]ReleaseRecord(nil), records...)
	duplicateRecords = append(duplicateRecords, records[0])
	repository.records = duplicateRecords
	if _, _, err := service.currentReleaseCatalog(t.Context()); err == nil {
		t.Fatal("currentReleaseCatalog() accepted duplicate inventory")
	}
}

func TestReleaseFingerprintAndStoredPayloadErrorBoundaries(t *testing.T) {
	t.Parallel()
	records := completeReleaseRegistryRecords(t)
	if fingerprint, err := ActiveDesktopManifestFingerprint(records); err != nil || len(fingerprint) != 64 {
		t.Fatalf("ActiveDesktopManifestFingerprint() = %q, %v", fingerprint, err)
	}
	if _, err := ActiveDesktopManifestFingerprint(
		[]ReleaseRecord{{Kind: "unknown", Payload: json.RawMessage(`{}`)}},
	); err == nil {
		t.Fatal("fingerprinted an unknown stored component")
	}
	if _, err := ActiveDesktopManifestFingerprint(append(records, records[0])); err == nil {
		t.Fatal("fingerprinted duplicate stored inventory")
	}

	snapshot := releaseRegistrySnapshot{}
	if err := snapshot.add(ReleaseRecord{
		Kind: "client", SchemaVersion: legacyReleasePayloadSchemaVersion, Payload: json.RawMessage(`[]`),
	}); err == nil {
		t.Fatal("decoded a non-object Client payload")
	}
	if _, err := decodeStoredReleasePayload(ReleaseRecord{
		SchemaVersion: currentReleasePayloadSchemaVersion, Payload: json.RawMessage(`{`),
	}); err == nil {
		t.Fatal("decoded malformed release envelope JSON")
	}
}

func TestCanonicalReleaseValidationBoundaries(t *testing.T) {
	t.Parallel()

	client := validClientRelease()
	client.Protocol = 0
	if err := validateCanonicalClientRelease(client); err == nil {
		t.Fatal("canonical Client validation accepted invalid base release")
	}
	client = validClientRelease()
	client.Version = "1.0.0+build"
	if err := validateCanonicalClientRelease(client); err == nil {
		t.Fatal("canonical Client validation accepted build metadata")
	}
	client = validClientRelease()
	client.ProbeBootstrapPublicKeys = map[string]string{
		" primary ": "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	}
	if err := validateCanonicalClientRelease(client); err == nil {
		t.Fatal("canonical Client validation accepted padded key ID")
	}

	browser := validBrowserRelease()
	browser.CompatiblePlaywrightVersions = nil
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("canonical Browser validation accepted invalid base release")
	}
	browser = validBrowserRelease()
	browser.Revision = "01234"
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("canonical Browser validation accepted padded revision")
	}
	browser = validBrowserRelease()
	browser.CompatiblePlaywrightVersions = []string{"2.0.0", "1.61.1"}
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("canonical Browser validation accepted unsorted compatibility")
	}
	browser = validBrowserRelease()
	browser.CompatiblePlaywrightVersions = []string{"1.61.1+build"}
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("canonical Browser validation accepted noncanonical compatibility")
	}

	playwright := validPlaywrightRelease()
	playwright.PublishedAt = time.Time{}
	if err := validateCanonicalPlaywrightRelease(playwright); err == nil {
		t.Fatal("canonical Playwright validation accepted invalid base release")
	}
	playwright = validPlaywrightRelease()
	playwright.Version = "1.61.1+build"
	if err := validateCanonicalPlaywrightRelease(playwright); err == nil {
		t.Fatal("canonical Playwright validation accepted build metadata")
	}

	launcher := validLauncherRelease()
	launcher.Protocol = 0
	if err := validateCanonicalLauncherRelease(launcher); err == nil {
		t.Fatal("canonical Launcher validation accepted invalid base release")
	}
	launcher = validLauncherRelease()
	launcher.Version = "1.0.0+build"
	if err := validateCanonicalLauncherRelease(launcher); err == nil {
		t.Fatal("canonical Launcher validation accepted build metadata")
	}

	probe := validProbeRelease()
	probe.Protocol = 0
	if err := validateCanonicalProbeRelease(probe); err == nil {
		t.Fatal("canonical Probe validation accepted invalid base release")
	}
	probe = validProbeRelease()
	probe.ImageDigest = "sha256:" + strings.Repeat("A", 64)
	if err := validateCanonicalProbeRelease(probe); err == nil {
		t.Fatal("canonical Probe validation accepted uppercase digest")
	}

	for _, revision := range []string{"", "01234", "12x"} {
		if canonicalNumericRevision(revision) {
			t.Fatalf("canonicalNumericRevision(%q) = true", revision)
		}
	}
	if !canonicalNumericRevision("1234") {
		t.Fatal("canonicalNumericRevision(1234) = false")
	}
}

func TestConfigureAvailableReleasesSuccess(t *testing.T) {
	t.Parallel()
	service, _ := newClientServiceHarness(t)
	if err := service.configureAvailableReleases(
		[]ClientRelease{validClientRelease()},
		[]BrowserRelease{validBrowserRelease()},
		[]PlaywrightRelease{validPlaywrightRelease()},
		[]LauncherRelease{validLauncherRelease()},
		[]ProbeRelease{validProbeRelease()},
		99,
	); err != nil {
		t.Fatal(err)
	}
	if len(service.clients) != 1 || len(service.browsers) != 1 || len(service.playwright) != 1 ||
		len(service.launchers) != 1 || len(service.probes) != 1 || service.ReleaseGeneration() != 0 {
		t.Fatalf("configured catalog = %+v, generation %d", service.releaseCatalog(), service.ReleaseGeneration())
	}
}

func completeReleaseRegistryRecords(t *testing.T) []ReleaseRecord {
	t.Helper()
	sets := []struct {
		kind     string
		releases any
	}{
		{kind: "client", releases: validClientReleaseSet()},
		{kind: "browser", releases: validBrowserReleaseSet()},
		{kind: "playwright", releases: validPlaywrightReleaseSet()},
		{kind: "launcher", releases: validLauncherReleaseSet()},
		{kind: "probe", releases: []ProbeRelease{validProbeRelease()}},
	}
	records := make([]ReleaseRecord, 0, 13)
	for _, set := range sets {
		setRecords, err := releaseRecords(set.kind, set.releases)
		if err != nil {
			t.Fatalf("releaseRecords(%s) = %v", set.kind, err)
		}
		records = append(records, setRecords...)
	}
	return records
}
