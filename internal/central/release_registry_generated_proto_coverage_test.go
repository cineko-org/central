package central

import (
	"errors"
	"testing"
	"time"

	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestGeneratedProtoReleaseRegistryRepositoryBoundaries(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if _, err := service.ReleaseRegistry(t.Context()); err == nil {
		t.Fatal("ReleaseRegistry without repository succeeded")
	}
	if generation, err := service.CurrentReleaseGeneration(t.Context()); err != nil || generation != 0 {
		t.Fatalf("fallback release generation = %d, %v", generation, err)
	}
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("LoadReleaseRegistry without repository succeeded")
	}
	if _, _, err := service.currentReleaseCatalog(t.Context()); err == nil {
		t.Fatal("current release catalog without repository succeeded")
	}

	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, generation: 7}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.listErr = errInjectedClient
	for name, call := range map[string]func() error{
		"registry":   func() error { _, err := service.ReleaseRegistry(t.Context()); return err },
		"generation": func() error { _, err := service.CurrentReleaseGeneration(t.Context()); return err },
		"load":       func() error { return service.LoadReleaseRegistry(t.Context()) },
		"catalog":    func() error { _, _, err := service.currentReleaseCatalog(t.Context()); return err },
		"runtime snapshot": func() error {
			_, _, err := service.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "darwin", "arm64")
			return err
		},
		"launcher snapshot": func() error {
			_, _, err := service.CurrentLauncherReleaseSnapshot(t.Context(), "stable", "darwin", "arm64")
			return err
		},
		"Probe snapshot": func() error {
			_, _, err := service.CurrentProbeReleaseSnapshot(t.Context(), "stable")
			return err
		},
	} {
		if !errors.Is(call(), errInjectedClient) {
			t.Fatalf("%s repository error was not returned", name)
		}
	}
	repository.listErr = nil
	repository.records = []ReleaseRecord{{Kind: "client", Payload: []byte("{broken")}}
	if _, err := service.ReleaseRegistry(t.Context()); err == nil {
		t.Fatal("invalid registry payload accepted")
	}
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("invalid registry load accepted")
	}
	if _, _, err := service.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "darwin", "arm64"); err == nil {
		t.Fatal("invalid registry runtime snapshot accepted")
	}

	repository.records = completeReleaseRegistryRecords(t)
	registry, err := service.ReleaseRegistry(t.Context())
	if err != nil || registry.GetGeneration() != 7 {
		t.Fatalf("valid registry = %+v, %v", registry, err)
	}
	if _, _, err := service.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "linux", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing runtime snapshot = %v", err)
	}
	if _, _, err := service.CurrentLauncherReleaseSnapshot(t.Context(), "stable", "linux", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing launcher snapshot = %v", err)
	}
	if _, _, err := service.CurrentProbeReleaseSnapshot(t.Context(), "beta"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Probe snapshot = %v", err)
	}
	if probe, generation, err := service.CurrentProbeReleaseSnapshot(t.Context(), "stable"); err != nil || probe.GetVersion() == "" || generation != 7 {
		t.Fatalf("current Probe snapshot = %+v, %d, %v", probe, generation, err)
	}
	if err := service.LoadReleaseRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedProtoReleaseRegistrySetAndCanonicalBoundaries(t *testing.T) {
	if _, err := releaseMessages(&releasepb.Registry{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown release set = %v", err)
	}
	sets := []proto.Message{clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet()}
	for _, set := range sets {
		messages, err := releaseMessages(set)
		if err != nil || len(messages) == 0 {
			t.Fatalf("releaseMessages(%T) = %v, %d", set, err, len(messages))
		}
	}
	if _, err := releaseRecord("client", &releasepb.Registry{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown release record = %v", err)
	}
	if err := validateCanonicalClientRelease(validClientRelease()); err != nil {
		t.Fatalf("valid canonical client = %v", err)
	}
	if err := validateCanonicalBrowserRelease(validBrowserRelease()); err != nil {
		t.Fatalf("valid canonical browser = %v", err)
	}
	if err := validateCanonicalLauncherRelease(validLauncherRelease()); err != nil {
		t.Fatalf("valid canonical launcher = %v", err)
	}
	if err := validateCanonicalProbeRelease(validProbeRelease()); err != nil {
		t.Fatalf("valid canonical Probe = %v", err)
	}

	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{}); err != nil {
		t.Fatalf("empty configured catalog = %v", err)
	}
	if fingerprint, err := ActiveDesktopManifestFingerprint(nil); err != nil || fingerprint == "" {
		t.Fatalf("empty desktop registry fingerprint = %q, %v", fingerprint, err)
	}
	if err := validateStoredReleaseSets([]ReleaseRecord{{Kind: "probe", Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0"}}); err == nil {
		t.Fatal("invalid Probe release target set accepted")
	}
	if err := validateStoredReleaseSets([]ReleaseRecord{{Kind: "client", Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0"}}); err == nil {
		t.Fatal("incomplete desktop release set accepted")
	}
	clientRecords, err := releaseRecords("client", clientReleaseSet())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReleaseRegistry(clientRecords[:1]); err == nil {
		t.Fatal("incomplete decoded registry accepted")
	}
	duplicateRecords := append(append([]ReleaseRecord(nil), clientRecords...), clientRecords[0])
	if _, err := ActiveDesktopManifestFingerprint(clientRecords[:1]); err == nil {
		t.Fatal("incomplete desktop fingerprint registry accepted")
	}
	if _, err := ActiveDesktopManifestFingerprint(duplicateRecords); err == nil {
		t.Fatal("duplicate desktop fingerprint registry accepted")
	}
	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{clients: []*releasepb.ClientRelease{{}}}); err == nil {
		t.Fatal("invalid client catalog accepted")
	}
	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{browsers: []*releasepb.BrowserRelease{{}}}); err == nil {
		t.Fatal("invalid browser catalog accepted")
	}
	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{playwright: []*releasepb.PlaywrightRelease{{}}}); err == nil {
		t.Fatal("invalid Playwright catalog accepted")
	}
	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{launchers: []*releasepb.LauncherRelease{{}}}); err == nil {
		t.Fatal("invalid launcher catalog accepted")
	}
	if _, err := configuredReleaseCatalog(releaseRegistrySnapshot{probes: []*releasepb.ProbeRelease{{}}}); err == nil {
		t.Fatal("invalid Probe catalog accepted")
	}
	duplicateRepository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, records: duplicateRecords}
	duplicateService, err := NewClientService(duplicateRepository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := duplicateService.CurrentRuntimeReleaseSnapshot(t.Context(), "stable", "darwin", "arm64"); err == nil {
		t.Fatal("duplicate catalog runtime accepted")
	}
	if err := duplicateService.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("duplicate catalog load accepted")
	}
	if err := duplicateService.applyReleaseRegistry(duplicateRecords, 1); err == nil {
		t.Fatal("duplicate catalog apply accepted")
	}

	invalidProbe := proto.CloneOf(validProbeRelease())
	invalidProbe.SetImage("registry.example.com/" + string([]byte{0xff}))
	if _, err := releaseRecord("probe", invalidProbe); err == nil {
		t.Fatal("invalid ProtoJSON Probe payload accepted")
	}
	invalidClient := proto.CloneOf(validClientRelease())
	invalidClient.SetVersion("invalid")
	if validateCanonicalClientRelease(invalidClient) == nil {
		t.Fatal("invalid canonical client accepted")
	}
	invalidBrowser := proto.CloneOf(validBrowserRelease())
	invalidBrowser.SetCompatiblePlaywrightVersions([]string{"invalid"})
	if validateCanonicalBrowserRelease(invalidBrowser) == nil {
		t.Fatal("invalid canonical browser accepted")
	}
	canonicalBrowser := proto.CloneOf(validBrowserRelease())
	canonicalBrowser.SetCompatiblePlaywrightVersions([]string{"v1.61.1"})
	if validateCanonicalBrowserRelease(canonicalBrowser) == nil {
		t.Fatal("non-canonical compatible browser version accepted")
	}
	invalidStoredBrowserPayload, err := protojson.Marshal(canonicalBrowser)
	if err != nil {
		t.Fatal(err)
	}
	invalidStoredBrowser := ReleaseRecord{
		Kind: "browser", Channel: canonicalBrowser.GetChannel(), Platform: canonicalBrowser.GetPlatform(),
		Arch: canonicalBrowser.GetArchitecture(), Version: canonicalBrowser.GetRevision(),
		Payload: invalidStoredBrowserPayload,
	}
	var invalidStoredSnapshot releaseRegistrySnapshot
	if err := invalidStoredSnapshot.add(invalidStoredBrowser); err == nil {
		t.Fatal("non-canonical stored browser accepted")
	}
	invalidLauncher := proto.CloneOf(validLauncherRelease())
	invalidLauncher.SetVersion("invalid")
	if validateCanonicalLauncherRelease(invalidLauncher) == nil {
		t.Fatal("invalid canonical launcher accepted")
	}
	invalidCanonicalProbe := proto.CloneOf(validProbeRelease())
	invalidCanonicalProbe.SetVersion("invalid")
	if validateCanonicalProbeRelease(invalidCanonicalProbe) == nil {
		t.Fatal("invalid canonical Probe accepted")
	}
}

func TestGeneratedProtoReleaseRegistryBootstrapAndPublishFailures(t *testing.T) {
	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.listErr = errInjectedClient
	if err := service.BootstrapReleaseRegistry(t.Context(), clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap list error = %v", err)
	}
	repository.listErr = nil
	repository.records = []ReleaseRecord{{Kind: "client", Payload: []byte("{broken")}}
	if err := service.BootstrapReleaseRegistry(t.Context(), clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet()); err == nil {
		t.Fatal("bootstrap invalid existing registry accepted")
	}
	repository.records = nil
	if err := service.BootstrapReleaseRegistry(t.Context(), nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("empty bootstrap seeds = %v", err)
	}
	repository.records = nil
	repository.listErr = nil
	repository.insertErr = nil
	repository.generation = 1
	repository.insertErr = errInjectedClient
	if err := service.BootstrapReleaseRegistry(t.Context(), clientReleaseSet(), browserReleaseSet(), playwrightReleaseSet(), launcherReleaseSet(), probeReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap publish error = %v", err)
	}
	repository.insertErr = nil
	repository.records = nil
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", &releasepb.ClientReleaseSet{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid publish set = %v", err)
	}
	repository.listErr = errInjectedClient
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", clientReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("publish load error = %v", err)
	}
	if len(repository.records) == 0 {
		t.Fatal("publish did not insert before load failure")
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", clientReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("publish repeated load error = %v", err)
	}
}
