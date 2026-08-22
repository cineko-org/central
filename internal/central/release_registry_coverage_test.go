package central

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	"google.golang.org/protobuf/proto"
)

func TestReleaseRegistryBootstrapPublishAndViewUseGeneratedProto(t *testing.T) {
	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BootstrapReleaseRegistry(
		t.Context(), clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet(),
	); err != nil {
		t.Fatal(err)
	}
	if service.ReleaseGeneration() != 1 || repository.generation != 1 || len(repository.records) != 13 {
		t.Fatalf("bootstrapped registry = generation %d, records %d", service.ReleaseGeneration(), len(repository.records))
	}
	registry, err := service.ReleaseRegistry(t.Context())
	if err != nil || registry.GetGeneration() != 1 || len(registry.GetClients().GetReleases()) != 3 ||
		len(registry.GetProbes().GetReleases()) != 1 {
		t.Fatalf("ReleaseRegistry() = %+v, %v", registry, err)
	}
	if err := service.BootstrapReleaseRegistry(
		t.Context(), clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet(),
	); err != nil || repository.generation != 1 {
		t.Fatalf("idempotent bootstrap generation = %d, error = %v", repository.generation, err)
	}
	probe := proto.CloneOf(validProbeRelease())
	probe.SetVersion("1.1.0")
	probe.SetImageDigest("sha256:" + strings.Repeat("9", 64))
	set := &releasepb.ProbeReleaseSet{}
	set.SetReleases([]*releasepb.ProbeRelease{probe})
	generation, inserted, err := service.PublishReleaseSet(t.Context(), "probe", set)
	if err != nil || !inserted || generation != 1 {
		t.Fatalf("probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	generation, inserted, err = service.PublishReleaseSet(t.Context(), "probe", set)
	if err != nil || inserted || generation != 1 {
		t.Fatalf("idempotent probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
}

func TestReleaseSnapshotsAndFingerprintUseGeneratedProto(t *testing.T) {
	fallback, _ := newClientServiceHarness(t)
	if err := configureValidRuntime(fallback, validClientRelease()); err != nil {
		t.Fatal(err)
	}
	if err := fallback.ConfigureProbeReleases([]*releasepb.ProbeRelease{validProbeRelease()}); err != nil {
		t.Fatal(err)
	}
	runtime, generation, err := fallback.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "darwin", "arm64")
	if err != nil || runtime.GetClient().GetVersion() == "" || generation != 0 {
		t.Fatalf("runtime fallback = %+v, %d, %v", runtime, generation, err)
	}
	launcher, generation, err := fallback.CurrentLauncherReleaseSnapshot(t.Context(), "stable", "darwin", "arm64")
	if err != nil || launcher.GetVersion() == "" || generation != 0 {
		t.Fatalf("launcher fallback = %+v, %d, %v", launcher, generation, err)
	}
	probe, generation, err := fallback.CurrentProbeReleaseSnapshot(t.Context(), "stable")
	if err != nil || probe.GetVersion() == "" || generation != 0 {
		t.Fatalf("probe fallback = %+v, %d, %v", probe, generation, err)
	}

	records := completeReleaseRegistryRecords(t)
	fingerprint, err := ActiveDesktopManifestFingerprint(records)
	if err != nil || len(fingerprint) != 64 {
		t.Fatalf("fingerprint = %q, %v", fingerprint, err)
	}
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{}, records: records, generation: 11,
	}
	service, _ := NewClientService(repository, time.Hour)
	runtime, generation, err = service.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "darwin", "arm64")
	if err != nil || runtime.GetClient().GetVersion() == "" || generation != 11 {
		t.Fatalf("runtime repository = %+v, %d, %v", runtime, generation, err)
	}
	if missing, generation, err := service.CurrentRuntimeReleaseSnapshot(
		t.Context(), "stable", "linux", "arm64",
	); !errors.Is(err, ErrNotFound) || generation != 11 || missing != nil {
		t.Fatalf("missing runtime = %+v, %d, %v", missing, generation, err)
	}
}

func TestReleaseRegistryRejectsInvalidProtoSetsAndStoredPayloads(t *testing.T) {
	emptyClients := &releasepb.ClientReleaseSet{}
	if _, err := releaseRecords("client", emptyClients); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty release set = %v", err)
	}
	partialClients := &releasepb.ClientReleaseSet{}
	partialClients.SetReleases([]*releasepb.ClientRelease{validClientRelease()})
	if _, err := releaseRecords("client", partialClients); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial release set = %v", err)
	}
	mismatched := clientReleaseSet()
	mismatched.GetReleases()[1].SetVersion("1.0.1")
	if _, err := releaseRecords("client", mismatched); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed release identity = %v", err)
	}
	duplicate := clientReleaseSet()
	duplicate.GetReleases()[1] = duplicate.GetReleases()[0]
	if _, err := releaseRecords("client", duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate desktop target = %v", err)
	}
	twoProbes := &releasepb.ProbeReleaseSet{}
	twoProbes.SetReleases([]*releasepb.ProbeRelease{validProbeRelease(), validProbeRelease()})
	if _, err := releaseRecords("probe", twoProbes); !errors.Is(err, ErrInvalid) {
		t.Fatalf("multiple probe images = %v", err)
	}
	if _, err := releaseRecords("client", browserReleaseSet()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched component = %v", err)
	}
	if _, err := releaseRecord("client", &releasepb.BrowserRelease{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched record = %v", err)
	}

	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records:              []ReleaseRecord{{Kind: "client", Payload: []byte("{broken")}},
	}
	service, _ := NewClientService(repository, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("invalid stored proto payload accepted")
	}
	repository.records = []ReleaseRecord{{Kind: "unknown", Payload: []byte("{}")}}
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("unknown stored release kind accepted")
	}
	records, err := releaseRecords("client", clientReleaseSet())
	if err != nil {
		t.Fatal(err)
	}
	records[0].Channel = "mismatched"
	repository.records = records
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("stored identity mismatch accepted")
	}
}

func TestCanonicalReleaseValidationUsesOnlyLatestProtoFields(t *testing.T) {
	client := proto.CloneOf(validClientRelease())
	client.SetVersion("1.0.0+build")
	if err := validateCanonicalClientRelease(client); err == nil {
		t.Fatal("client build metadata accepted")
	}
	client = proto.CloneOf(validClientRelease())
	client.SetProbeBootstrapPublicKeys(map[string]string{
		" primary ": "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	})
	if err := validateCanonicalClientRelease(client); err == nil {
		t.Fatal("padded client key id accepted")
	}
	browser := proto.CloneOf(validBrowserRelease())
	browser.SetRevision("01234")
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("padded browser revision accepted")
	}
	browser = proto.CloneOf(validBrowserRelease())
	browser.SetCompatiblePlaywrightVersions([]string{"2.0.0", "1.61.1"})
	if err := validateCanonicalBrowserRelease(browser); err == nil {
		t.Fatal("unsorted browser compatibility accepted")
	}
	playwright := proto.CloneOf(validPlaywrightRelease())
	playwright.SetPublishedAt(nil)
	if err := validateCanonicalPlaywrightRelease(playwright); err == nil {
		t.Fatal("missing Playwright timestamp accepted")
	}
	launcher := proto.CloneOf(validLauncherRelease())
	launcher.SetVersion("1.0.0+build")
	if err := validateCanonicalLauncherRelease(launcher); err == nil {
		t.Fatal("launcher build metadata accepted")
	}
	probe := proto.CloneOf(validProbeRelease())
	probe.SetImageDigest("sha256:" + strings.Repeat("A", 64))
	if err := validateCanonicalProbeRelease(probe); err == nil {
		t.Fatal("uppercase probe digest accepted")
	}
}

func TestReleaseRegistryRepositoryFailures(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.BootstrapReleaseRegistry(t.Context(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("bootstrap without repository succeeded")
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", clientReleaseSet()); err == nil {
		t.Fatal("publish without repository succeeded")
	}
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{}, insertErr: errInjectedClient,
	}
	service, _ = NewClientService(repository, time.Hour)
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", clientReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("publish repository error = %v", err)
	}
	repository.insertErr = nil
	repository.listErr = errInjectedClient
	if err := service.LoadReleaseRegistry(t.Context()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("load repository error = %v", err)
	}
}

func clientReleaseSet() *releasepb.ClientReleaseSet {
	set := &releasepb.ClientReleaseSet{}
	set.SetReleases(validClientReleaseSet())
	return set
}

func browserReleaseSet() *releasepb.BrowserReleaseSet {
	set := &releasepb.BrowserReleaseSet{}
	set.SetReleases(validBrowserReleaseSet())
	return set
}

func playwrightReleaseSet() *releasepb.PlaywrightReleaseSet {
	set := &releasepb.PlaywrightReleaseSet{}
	set.SetReleases(validPlaywrightReleaseSet())
	return set
}

func launcherReleaseSet() *releasepb.LauncherReleaseSet {
	set := &releasepb.LauncherReleaseSet{}
	set.SetReleases(validLauncherReleaseSet())
	return set
}

func probeReleaseSet() *releasepb.ProbeReleaseSet {
	set := &releasepb.ProbeReleaseSet{}
	set.SetReleases([]*releasepb.ProbeRelease{validProbeRelease()})
	return set
}

func completeReleaseRegistryRecords(t *testing.T) []ReleaseRecord {
	t.Helper()
	sets := []struct {
		kind string
		set  proto.Message
	}{
		{kind: "client", set: clientReleaseSet()},
		{kind: "browser", set: browserReleaseSet()},
		{kind: "playwright", set: playwrightReleaseSet()},
		{kind: "launcher", set: launcherReleaseSet()},
		{kind: "probe", set: probeReleaseSet()},
	}
	records := make([]ReleaseRecord, 0, 13)
	for _, set := range sets {
		values, err := releaseRecords(set.kind, set.set)
		if err != nil {
			t.Fatalf("releaseRecords(%s) = %v", set.kind, err)
		}
		records = append(records, values...)
	}
	return records
}

type releaseRepositoryFake struct {
	*clientRepositoryFake
	records    []ReleaseRecord
	generation int64
	insertErr  error
	listErr    error
}

func (repository *releaseRepositoryFake) ListReleases(context.Context) ([]ReleaseRecord, int64, error) {
	if repository.listErr != nil {
		return nil, 0, repository.listErr
	}
	return append([]ReleaseRecord(nil), repository.records...), repository.generation, nil
}

func (repository *releaseRepositoryFake) CurrentReleaseGeneration(context.Context) (int64, error) {
	if repository.listErr != nil {
		return 0, repository.listErr
	}
	return repository.generation, nil
}

func (repository *releaseRepositoryFake) InsertReleaseSet(
	_ context.Context,
	records []ReleaseRecord,
) (int64, bool, error) {
	if repository.insertErr != nil {
		return 0, false, repository.insertErr
	}
	identity := records[0]
	existing := make([]ReleaseRecord, 0, len(records))
	for _, stored := range repository.records {
		if stored.Kind == identity.Kind && stored.Channel == identity.Channel && stored.Version == identity.Version {
			existing = append(existing, stored)
		}
	}
	if len(existing) > 0 {
		if len(existing) != len(records) {
			return 0, false, ErrConflict
		}
		for _, record := range records {
			matched := false
			for _, stored := range existing {
				matched = matched || stored.Platform == record.Platform && stored.Arch == record.Arch &&
					bytes.Equal(stored.Payload, record.Payload)
			}
			if !matched {
				return 0, false, ErrConflict
			}
		}
		return repository.generation, false, nil
	}
	before := append([]ReleaseRecord(nil), repository.records...)
	repository.records = append(repository.records, records...)
	beforeFingerprint, beforeErr := ActiveDesktopManifestFingerprint(before)
	afterFingerprint, afterErr := ActiveDesktopManifestFingerprint(repository.records)
	if afterErr == nil && (beforeErr != nil || beforeFingerprint != afterFingerprint) {
		repository.generation++
	}
	return repository.generation, true, nil
}

var _ ReleaseRepository = (*releaseRepositoryFake)(nil)
