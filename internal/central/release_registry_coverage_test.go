package central

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReleaseRegistryBootstrapAndPublish(t *testing.T) {
	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	changedClients := validClientReleaseSet()
	changedClients[0].Artifact.SHA256 = strings.Repeat("f", 64)
	if err := service.BootstrapReleaseRegistry(
		t.Context(),
		changedClients,
		validBrowserReleaseSet(),
		validPlaywrightReleaseSet(),
		validLauncherReleaseSet(),
		[]ProbeRelease{validProbeRelease()},
	); err != nil {
		t.Fatal(err)
	}
	if service.ReleaseGeneration() != 1 || repository.generation != 1 || len(repository.records) != 13 {
		t.Fatalf("bootstrapped registry = generation %d, records %d", service.ReleaseGeneration(), len(repository.records))
	}
	if err := service.BootstrapReleaseRegistry(
		t.Context(),
		validClientReleaseSet(),
		validBrowserReleaseSet(),
		validPlaywrightReleaseSet(),
		validLauncherReleaseSet(),
		[]ProbeRelease{validProbeRelease()},
	); err != nil || repository.generation != 1 {
		t.Fatalf("idempotent bootstrap = generation %d, error %v", repository.generation, err)
	}
	newProbe := validProbeRelease()
	newProbe.Version = "1.1.0"
	newProbe.ImageDigest = "sha256:" + strings.Repeat("9", 64)
	generation, inserted, err := service.PublishReleaseSet(t.Context(), "probe", []ProbeRelease{newProbe})
	if err != nil || !inserted || generation != 1 || service.ReleaseGeneration() != 1 {
		t.Fatalf("Probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
	generation, inserted, err = service.PublishReleaseSet(t.Context(), "probe", []ProbeRelease{newProbe})
	if err != nil || inserted || generation != 1 {
		t.Fatalf("idempotent Probe publish = generation %d, inserted %v, error %v", generation, inserted, err)
	}
}

func TestReleaseRegistryView(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if _, err := service.ReleaseRegistry(t.Context()); err == nil {
		t.Fatal("viewed release registry without a repository")
	}

	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		generation:           9,
		listErr:              errInjectedClient,
	}
	service, _ = NewClientService(repository, time.Hour)
	if _, err := service.ReleaseRegistry(t.Context()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("registry view list error = %v", err)
	}

	repository.listErr = nil
	repository.records = []ReleaseRecord{{Kind: "unknown", Payload: json.RawMessage(`{}`)}}
	if _, err := service.ReleaseRegistry(t.Context()); err == nil {
		t.Fatal("viewed invalid stored release registry")
	}

	repository.records = nil
	registry, err := service.ReleaseRegistry(t.Context())
	if err != nil || registry.Generation != 9 || registry.Components.Client == nil ||
		registry.Components.Browser == nil || registry.Components.Playwright == nil ||
		registry.Components.Launcher == nil || registry.Components.Probe == nil {
		t.Fatalf("empty release registry view = %+v, %v", registry, err)
	}
}

func TestReleaseRegistryFailures(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.BootstrapReleaseRegistry(t.Context(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("bootstrapped registry without a repository")
	}
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("loaded registry without a repository")
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", validClientReleaseSet()); err == nil {
		t.Fatal("published release without a repository")
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "unknown", validClientReleaseSet()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown release kind = %v", err)
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", struct{}{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported release type = %v", err)
	}
	invalidTime := validClientRelease()
	invalidTime.PublishedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	invalidSet := validClientReleaseSet()
	for index := range invalidSet {
		invalidSet[index].PublishedAt = invalidTime.PublishedAt
	}
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", invalidSet); err == nil {
		t.Fatal("unencodable release accepted")
	}

	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, insertErr: errInjectedClient}
	service, _ = NewClientService(repository, time.Hour)
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", validClientReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("publish repository error = %v", err)
	}
	repository.insertErr = nil
	repository.listErr = errInjectedClient
	if _, _, err := service.PublishReleaseSet(t.Context(), "client", validClientReleaseSet()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("publish reload error = %v", err)
	}
	if err := service.LoadReleaseRegistry(t.Context()); !errors.Is(err, errInjectedClient) {
		t.Fatalf("registry list error = %v", err)
	}
	repository = &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records:              []ReleaseRecord{{Kind: "unknown", Payload: json.RawMessage(`{}`)}},
	}
	service, _ = NewClientService(repository, time.Hour)
	if err := service.BootstrapReleaseRegistry(t.Context(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("bootstrapped over invalid stored inventory")
	}
}

func TestReleaseSetValidation(t *testing.T) {
	if _, err := releaseRecords("client", []ClientRelease{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty release set = %v", err)
	}
	if _, err := releaseRecords("client", []ClientRelease{validClientRelease()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial desktop release set = %v", err)
	}
	mismatched := validClientReleaseSet()
	mismatched[1].Version = "1.0.1"
	if _, err := releaseRecords("client", mismatched); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed identity release set = %v", err)
	}
	duplicate := validClientReleaseSet()
	duplicate[1] = duplicate[0]
	if _, err := releaseRecords("client", duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate target release set = %v", err)
	}
	if _, err := releaseRecords("probe", []ProbeRelease{validProbeRelease(), validProbeRelease()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("multiple Probe images = %v", err)
	}
	if _, err := releaseRecords("client", []BrowserRelease{validBrowserRelease()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched component type = %v", err)
	}
	if _, err := releaseRecord("client", struct{}{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported release record = %v", err)
	}
	if completeDesktopTargetSet(map[string]struct{}{
		"darwin/arm64": {}, "linux/amd64": {}, "unsupported/amd64": {},
	}) {
		t.Fatal("unsupported target set accepted")
	}
}

func TestReleaseRegistryStoredPayloadValidation(t *testing.T) {
	for _, kind := range []string{"client", "browser", "playwright", "launcher", "probe"} {
		t.Run(kind, func(t *testing.T) {
			repository := &releaseRepositoryFake{
				clientRepositoryFake: &clientRepositoryFake{},
				records:              []ReleaseRecord{{Kind: kind, Payload: []byte(`{"broken":`)}},
			}
			service, _ := NewClientService(repository, time.Hour)
			if err := service.LoadReleaseRegistry(t.Context()); err == nil {
				t.Fatalf("invalid stored %s release accepted", kind)
			}
		})
	}
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records:              []ReleaseRecord{{Kind: "unknown", Payload: []byte(`{}`)}},
	}
	service, _ := NewClientService(repository, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("unknown stored release kind accepted")
	}
	partialRelease := validClientRelease()
	partialPayload, err := json.Marshal(partialRelease)
	if err != nil {
		t.Fatal(err)
	}
	partial := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records: []ReleaseRecord{{
			Kind: "client", Channel: "stable", Platform: "darwin", Arch: "arm64",
			Version: "1.0.0", Payload: partialPayload,
		}},
	}
	service, _ = NewClientService(partial, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("partial stored release set accepted")
	}

	clientSet := validClientReleaseSet()
	matchingRecords, err := releaseRecords("client", clientSet)
	if err != nil {
		t.Fatal(err)
	}
	matchingRecords[0].Channel = "mismatched"
	mismatched := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, records: matchingRecords}
	service, _ = NewClientService(mismatched, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("stored release identity mismatch accepted")
	}
	validRecords, err := releaseRecords("client", clientSet)
	if err != nil {
		t.Fatal(err)
	}
	duplicateRecords := append([]ReleaseRecord(nil), validRecords...)
	duplicateRecords = append(duplicateRecords, validRecords[0])
	duplicate := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, records: duplicateRecords}
	service, _ = NewClientService(duplicate, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("duplicate stored release accepted")
	}
}

func TestReleasePayloadEnvelopeAndLegacyDecode(t *testing.T) {
	release := validClientRelease()
	record, err := releaseRecord("client", release)
	if err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != currentReleasePayloadSchemaVersion {
		t.Fatalf("new release schema version = %d", record.SchemaVersion)
	}
	decoded, err := decodeStoredReleasePayload(record)
	if err != nil {
		t.Fatal(err)
	}
	var restored ClientRelease
	if err := json.Unmarshal(decoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Version != release.Version || restored.Artifact.SHA256 != release.Artifact.SHA256 {
		t.Fatalf("restored release = %+v", restored)
	}

	legacyPayload, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := decodeStoredReleasePayload(ReleaseRecord{
		SchemaVersion: legacyReleasePayloadSchemaVersion,
		Payload:       legacyPayload,
	})
	if err != nil || !bytes.Equal(legacy, legacyPayload) {
		t.Fatalf("legacy payload = %s, %v", legacy, err)
	}

	for name, invalid := range map[string]ReleaseRecord{
		"future": {SchemaVersion: currentReleasePayloadSchemaVersion + 1, Payload: record.Payload},
		"mismatch": {
			SchemaVersion: currentReleasePayloadSchemaVersion,
			Payload:       json.RawMessage(`{"schemaVersion":1,"payload":{}}`),
		},
		"missing payload": {
			SchemaVersion: currentReleasePayloadSchemaVersion,
			Payload:       json.RawMessage(`{"schemaVersion":2}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStoredReleasePayload(invalid); err == nil {
				t.Fatal("invalid stored release envelope accepted")
			}
		})
	}

	legacyRecords, err := releaseRecords("client", validClientReleaseSet())
	if err != nil {
		t.Fatal(err)
	}
	for index := range legacyRecords {
		legacyRecords[index].Payload, err = decodeStoredReleasePayload(legacyRecords[index])
		if err != nil {
			t.Fatal(err)
		}
		legacyRecords[index].SchemaVersion = legacyReleasePayloadSchemaVersion
	}
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records:              legacyRecords,
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.LoadReleaseRegistry(t.Context()); err != nil {
		t.Fatalf("load legacy raw release registry: %v", err)
	}
	current, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err == nil || current.Client.Version != "" {
		t.Fatalf("legacy registry without companion runtime unexpectedly resolved: %+v, %v", current, err)
	}
	if len(service.clients) != len(legacyRecords) {
		t.Fatalf("legacy Client releases loaded = %d", len(service.clients))
	}
}

func TestConfigureAvailableReleasesRejectsInvalidComponent(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	invalidClient := validClientRelease()
	invalidClient.Version = "bad"
	invalidBrowser := validBrowserRelease()
	invalidBrowser.Revision = "bad"
	invalidPlaywright := validPlaywrightRelease()
	invalidPlaywright.Version = "bad"
	invalidLauncher := validLauncherRelease()
	invalidLauncher.Version = "bad"
	invalidProbe := validProbeRelease()
	invalidProbe.Version = "bad"
	for name, configure := range map[string]func() error{
		"client": func() error {
			return service.configureAvailableReleases([]ClientRelease{invalidClient}, nil, nil, nil, nil, 0)
		},
		"browser": func() error {
			return service.configureAvailableReleases(nil, []BrowserRelease{invalidBrowser}, nil, nil, nil, 0)
		},
		"playwright": func() error {
			return service.configureAvailableReleases(nil, nil, []PlaywrightRelease{invalidPlaywright}, nil, nil, 0)
		},
		"launcher": func() error {
			return service.configureAvailableReleases(nil, nil, nil, []LauncherRelease{invalidLauncher}, nil, 0)
		},
		"probe": func() error {
			return service.configureAvailableReleases(nil, nil, nil, nil, []ProbeRelease{invalidProbe}, 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := configure(); err == nil {
				t.Fatalf("invalid %s release accepted", name)
			}
		})
	}
}

func TestLoadReleaseRegistryRejectsInvalidDecodedRelease(t *testing.T) {
	invalid := validClientRelease()
	invalid.Version = "bad"
	payload, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		records:              []ReleaseRecord{{Kind: "client", Payload: payload}},
	}
	service, _ := NewClientService(repository, time.Hour)
	if err := service.LoadReleaseRegistry(t.Context()); err == nil {
		t.Fatal("invalid decoded release accepted")
	}
}

func TestBootstrapReleaseRegistryStopsAtPublishFailure(t *testing.T) {
	client := validClientReleaseSet()
	browser := validBrowserReleaseSet()
	playwright := validPlaywrightReleaseSet()
	launcher := validLauncherReleaseSet()
	probe := []ProbeRelease{validProbeRelease()}
	tests := []struct {
		name       string
		clients    []ClientRelease
		browsers   []BrowserRelease
		playwright []PlaywrightRelease
		launchers  []LauncherRelease
		probes     []ProbeRelease
	}{
		{name: "client", clients: client},
		{name: "browser", browsers: browser},
		{name: "playwright", playwright: playwright},
		{name: "launcher", launchers: launcher},
		{name: "probe", probes: probe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, insertErr: errInjectedClient}
			service, _ := NewClientService(repository, time.Hour)
			err := service.BootstrapReleaseRegistry(
				t.Context(), test.clients, test.browsers, test.playwright, test.launchers, test.probes,
			)
			if !errors.Is(err, errInjectedClient) {
				t.Fatalf("bootstrap %s error = %v", test.name, err)
			}
		})
	}
	repository := &releaseRepositoryFake{clientRepositoryFake: &clientRepositoryFake{}, listErr: errInjectedClient}
	service, _ := NewClientService(repository, time.Hour)
	if err := service.BootstrapReleaseRegistry(t.Context(), nil, nil, nil, nil, nil); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap final load error = %v", err)
	}
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
					stored.SchemaVersion == record.SchemaVersion && bytes.Equal(stored.Payload, record.Payload)
			}
			if !matched {
				return 0, false, ErrConflict
			}
		}
		return repository.generation, false, nil
	}
	repository.records = append(repository.records, records...)
	before := repository.records[:len(repository.records)-len(records)]
	beforeFingerprint, beforeErr := ActiveDesktopManifestFingerprint(before)
	afterFingerprint, afterErr := ActiveDesktopManifestFingerprint(repository.records)
	if beforeErr != nil || afterErr != nil {
		if beforeErr != nil {
			return 0, false, beforeErr
		}
		return 0, false, afterErr
	}
	if beforeFingerprint != afterFingerprint {
		repository.generation++
	}
	return repository.generation, true, nil
}
