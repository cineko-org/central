package central

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	"golang.org/x/mod/semver"
)

type ReleaseRecord struct {
	Kind          string
	Channel       string
	Platform      string
	Arch          string
	Version       string
	SchemaVersion int
	Payload       json.RawMessage
	PublishedAt   time.Time
}

const (
	legacyReleasePayloadSchemaVersion  = 1
	currentReleasePayloadSchemaVersion = 2
)

type releasePayloadEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Payload       json.RawMessage `json:"payload"`
}

type ReleaseRepository interface {
	ListReleases(context.Context) ([]ReleaseRecord, int64, error)
	CurrentReleaseGeneration(context.Context) (int64, error)
	InsertReleaseSet(context.Context, []ReleaseRecord) (int64, bool, error)
}

type activeDesktopManifest struct {
	ResolverVersion int                           `json:"resolverVersion"`
	Targets         []activeDesktopTargetManifest `json:"targets"`
}

type activeDesktopTargetManifest struct {
	Platform string           `json:"platform"`
	Arch     string           `json:"arch"`
	Launcher *LauncherRelease `json:"launcher"`
	Runtime  *RuntimeRelease  `json:"runtime"`
}

type ReleaseRegistry struct {
	Generation int64                     `json:"generation"`
	Components ReleaseRegistryComponents `json:"components"`
}

type ReleaseRegistryComponents struct {
	Launcher   []LauncherRelease   `json:"launcher"`
	Client     []ClientRelease     `json:"client"`
	Browser    []BrowserRelease    `json:"browser"`
	Playwright []PlaywrightRelease `json:"playwright"`
	Probe      []ProbeRelease      `json:"probe"`
}

func (service *ClientService) ReleaseRegistry(ctx context.Context) (ReleaseRegistry, error) {
	if service.releaseRepository == nil {
		return ReleaseRegistry{}, errors.New("release repository is unavailable")
	}
	records, generation, err := service.releaseRepository.ListReleases(ctx)
	if err != nil {
		return ReleaseRegistry{}, err
	}
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return ReleaseRegistry{}, err
	}
	return ReleaseRegistry{
		Generation: generation,
		Components: ReleaseRegistryComponents{
			Launcher: snapshot.launchers, Client: snapshot.clients, Browser: snapshot.browsers,
			Playwright: snapshot.playwright, Probe: snapshot.probes,
		},
	}, nil
}

func (service *ClientService) CurrentReleaseGeneration(ctx context.Context) (int64, error) {
	if service.releaseRepository == nil {
		return service.ReleaseGeneration(), nil
	}
	return service.releaseRepository.CurrentReleaseGeneration(ctx)
}

func (service *ClientService) CurrentRuntimeReleaseSnapshot(
	ctx context.Context,
	channel string,
	platform string,
	arch string,
) (RuntimeRelease, int64, error) {
	return currentDesktopReleaseSnapshot(
		ctx,
		service,
		channel,
		platform,
		arch,
		service.CurrentRuntimeRelease,
		releasepolicy.CurrentRuntime,
	)
}

func (service *ClientService) CurrentLauncherReleaseSnapshot(
	ctx context.Context,
	channel string,
	platform string,
	arch string,
) (LauncherRelease, int64, error) {
	return currentDesktopReleaseSnapshot(
		ctx,
		service,
		channel,
		platform,
		arch,
		service.CurrentLauncherRelease,
		releasepolicy.CurrentLauncher,
	)
}

func (service *ClientService) CurrentProbeReleaseSnapshot(
	ctx context.Context,
	channel string,
) (ProbeRelease, int64, error) {
	return currentChannelReleaseSnapshot(
		ctx,
		service,
		channel,
		service.CurrentProbeRelease,
		releasepolicy.CurrentProbe,
	)
}

func currentDesktopReleaseSnapshot[Release any](
	ctx context.Context,
	service *ClientService,
	channel string,
	platform string,
	arch string,
	fallback func(string, string, string) (Release, error),
	resolve func(releasepolicy.Catalog, string, string, string) (Release, bool),
) (Release, int64, error) {
	if service.releaseRepository == nil {
		release, err := fallback(channel, platform, arch)
		return release, service.ReleaseGeneration(), err
	}
	catalog, generation, err := service.currentReleaseCatalog(ctx)
	if err != nil {
		var zero Release
		return zero, 0, err
	}
	release, ok := resolve(catalog, channel, platform, arch)
	if !ok {
		var zero Release
		return zero, generation, ErrNotFound
	}
	return release, generation, nil
}

func currentChannelReleaseSnapshot[Release any](
	ctx context.Context,
	service *ClientService,
	channel string,
	fallback func(string) (Release, error),
	resolve func(releasepolicy.Catalog, string) (Release, bool),
) (Release, int64, error) {
	if service.releaseRepository == nil {
		release, err := fallback(channel)
		return release, service.ReleaseGeneration(), err
	}
	catalog, generation, err := service.currentReleaseCatalog(ctx)
	if err != nil {
		var zero Release
		return zero, 0, err
	}
	release, ok := resolve(catalog, channel)
	if !ok {
		var zero Release
		return zero, generation, ErrNotFound
	}
	return release, generation, nil
}

func (service *ClientService) currentReleaseCatalog(ctx context.Context) (releasepolicy.Catalog, int64, error) {
	if service.releaseRepository == nil {
		return releasepolicy.Catalog{}, 0, errors.New("release repository is unavailable")
	}
	records, generation, err := service.releaseRepository.ListReleases(ctx)
	if err != nil {
		return releasepolicy.Catalog{}, 0, err
	}
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return releasepolicy.Catalog{}, 0, err
	}
	catalog, err := configuredReleaseCatalog(snapshot)
	if err != nil {
		return releasepolicy.Catalog{}, 0, err
	}
	return catalog, generation, nil
}

func (service *ClientService) LoadReleaseRegistry(ctx context.Context) error {
	if service.releaseRepository == nil {
		return errors.New("release repository is unavailable")
	}
	records, generation, err := service.releaseRepository.ListReleases(ctx)
	if err != nil {
		return err
	}
	return service.applyReleaseRegistry(records, generation)
}

func (service *ClientService) applyReleaseRegistry(records []ReleaseRecord, generation int64) error {
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return err
	}
	catalog, err := configuredReleaseCatalog(snapshot)
	if err != nil {
		return err
	}
	service.applyReleaseCatalog(catalog, generation)
	return nil
}

func decodeReleaseRegistry(records []ReleaseRecord) (releaseRegistrySnapshot, error) {
	snapshot := releaseRegistrySnapshot{
		clients: make([]ClientRelease, 0), browsers: make([]BrowserRelease, 0),
		playwright: make([]PlaywrightRelease, 0), launchers: make([]LauncherRelease, 0),
		probes: make([]ProbeRelease, 0),
	}
	for _, record := range records {
		if err := snapshot.add(record); err != nil {
			return releaseRegistrySnapshot{}, err
		}
	}
	if err := validateStoredReleaseSets(records); err != nil {
		return releaseRegistrySnapshot{}, err
	}
	return snapshot, nil
}

// ActiveDesktopManifestFingerprint resolves the same Launcher and Runtime
// manifests served to desktop clients and hashes their canonical aggregate.
func ActiveDesktopManifestFingerprint(records []ReleaseRecord) (string, error) {
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return "", err
	}
	catalog, err := configuredReleaseCatalog(snapshot)
	if err != nil {
		return "", err
	}
	manifest := activeDesktopManifest{
		ResolverVersion: releasepolicy.ActiveDesktopResolverVersion,
		Targets:         make([]activeDesktopTargetManifest, 0, len(releasepolicy.SupportedDesktopTargets())),
	}
	for _, target := range releasepolicy.SupportedDesktopTargets() {
		resolved := activeDesktopTargetManifest{Platform: target.Platform, Arch: target.Arch}
		if launcher, exists := releasepolicy.CurrentLauncher(
			catalog, "stable", target.Platform, target.Arch,
		); exists {
			resolved.Launcher = &launcher
		}
		if runtime, exists := releasepolicy.CurrentRuntime(
			catalog, "stable", target.Platform, target.Arch,
		); exists {
			resolved.Runtime = &runtime
		}
		manifest.Targets = append(manifest.Targets, resolved)
	}
	encoded, err := json.Marshal(manifest)
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), err
}

type releaseRegistrySnapshot struct {
	clients    []ClientRelease
	browsers   []BrowserRelease
	playwright []PlaywrightRelease
	launchers  []LauncherRelease
	probes     []ProbeRelease
}

func configuredReleaseCatalog(snapshot releaseRegistrySnapshot) (releasepolicy.Catalog, error) {
	candidate := ClientService{
		clients: make(map[string]ClientRelease), browsers: make(map[string]BrowserRelease),
		playwright: make(map[string]PlaywrightRelease), launchers: make(map[string]LauncherRelease),
		probes: make(map[string]ProbeRelease),
	}
	if len(snapshot.clients) > 0 {
		if err := candidate.ConfigureReleases(snapshot.clients); err != nil {
			return releasepolicy.Catalog{}, err
		}
	}
	if len(snapshot.browsers) > 0 {
		if err := candidate.ConfigureBrowserReleases(snapshot.browsers); err != nil {
			return releasepolicy.Catalog{}, err
		}
	}
	if len(snapshot.playwright) > 0 {
		if err := candidate.ConfigurePlaywrightReleases(snapshot.playwright); err != nil {
			return releasepolicy.Catalog{}, err
		}
	}
	if len(snapshot.launchers) > 0 {
		if err := candidate.ConfigureLauncherReleases(snapshot.launchers); err != nil {
			return releasepolicy.Catalog{}, err
		}
	}
	if len(snapshot.probes) > 0 {
		if err := candidate.ConfigureProbeReleases(snapshot.probes); err != nil {
			return releasepolicy.Catalog{}, err
		}
	}
	return releasepolicy.Catalog{
		Clients: candidate.clients, Browsers: candidate.browsers, Playwright: candidate.playwright,
		Launchers: candidate.launchers, Probes: candidate.probes,
	}, nil
}

func (snapshot *releaseRegistrySnapshot) add(record ReleaseRecord) error {
	switch record.Kind {
	case "client":
		return appendStoredRelease(record, &snapshot.clients)
	case "browser":
		return appendStoredRelease(record, &snapshot.browsers)
	case "playwright":
		return appendStoredRelease(record, &snapshot.playwright)
	case "launcher":
		return appendStoredRelease(record, &snapshot.launchers)
	case "probe":
		return appendStoredRelease(record, &snapshot.probes)
	default:
		return fmt.Errorf("stored release has unsupported kind %q", record.Kind)
	}
}

func appendStoredRelease[Release any](record ReleaseRecord, releases *[]Release) error {
	payload, err := decodeStoredReleasePayload(record)
	if err != nil {
		return fmt.Errorf("decode stored %s release envelope: %w", record.Kind, err)
	}
	var release Release
	if err := json.Unmarshal(payload, &release); err != nil {
		return fmt.Errorf("decode stored %s release: %w", record.Kind, err)
	}
	expected, err := releaseRecord(record.Kind, release)
	if err != nil {
		return fmt.Errorf("validate stored %s release: %w", record.Kind, err)
	}
	if expected.Kind != record.Kind || expected.Channel != record.Channel || expected.Platform != record.Platform ||
		expected.Arch != record.Arch || expected.Version != record.Version {
		return fmt.Errorf("stored %s release identity does not match its payload", record.Kind)
	}
	*releases = append(*releases, release)
	return nil
}

func decodeStoredReleasePayload(record ReleaseRecord) (json.RawMessage, error) {
	switch record.SchemaVersion {
	case 0, legacyReleasePayloadSchemaVersion:
		if !json.Valid(record.Payload) {
			return nil, errors.New("legacy release payload is invalid JSON")
		}
		return record.Payload, nil
	case currentReleasePayloadSchemaVersion:
		var envelope releasePayloadEnvelope
		if err := json.Unmarshal(record.Payload, &envelope); err != nil {
			return nil, err
		}
		if envelope.SchemaVersion != record.SchemaVersion {
			return nil, fmt.Errorf(
				"release payload schema version %d does not match record schema version %d",
				envelope.SchemaVersion,
				record.SchemaVersion,
			)
		}
		if len(envelope.Payload) == 0 || !json.Valid(envelope.Payload) {
			return nil, errors.New("release payload envelope has invalid payload")
		}
		return envelope.Payload, nil
	default:
		return nil, fmt.Errorf("unsupported release payload schema version %d", record.SchemaVersion)
	}
}

func validateStoredReleaseSets(records []ReleaseRecord) error {
	type releaseSet struct {
		kind    string
		targets map[string]struct{}
	}
	sets := make(map[string]releaseSet)
	for _, record := range records {
		identity := record.Kind + "/" + record.Channel + "/" + record.Version
		set := sets[identity]
		if set.targets == nil {
			set = releaseSet{kind: record.Kind, targets: make(map[string]struct{})}
		}
		set.targets[record.Platform+"/"+record.Arch] = struct{}{}
		sets[identity] = set
	}
	for identity, set := range sets {
		if set.kind == "probe" {
			if len(set.targets) == 1 {
				if _, valid := set.targets["/"]; valid {
					continue
				}
			}
		} else if releasepolicy.CompleteDesktopTargetSet(set.targets) {
			continue
		}
		return fmt.Errorf("stored release set %s is incomplete", identity)
	}
	return nil
}

func (service *ClientService) BootstrapReleaseRegistry(
	ctx context.Context,
	clients []ClientRelease,
	browsers []BrowserRelease,
	playwright []PlaywrightRelease,
	launchers []LauncherRelease,
	probes []ProbeRelease,
) error {
	if service.releaseRepository == nil {
		return errors.New("release repository is unavailable")
	}
	records, generation, err := service.releaseRepository.ListReleases(ctx)
	if err != nil {
		return err
	}
	if err := service.applyReleaseRegistry(records, generation); err != nil {
		return err
	}
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.Kind] = struct{}{}
	}
	seeds := []struct {
		kind     string
		releases any
		present  bool
	}{
		{kind: "client", releases: clients, present: len(clients) > 0},
		{kind: "browser", releases: browsers, present: len(browsers) > 0},
		{kind: "playwright", releases: playwright, present: len(playwright) > 0},
		{kind: "launcher", releases: launchers, present: len(launchers) > 0},
		{kind: "probe", releases: probes, present: len(probes) > 0},
	}
	for _, seed := range seeds {
		if _, found := existing[seed.kind]; found || !seed.present {
			continue
		}
		if _, _, err := service.PublishReleaseSet(ctx, seed.kind, seed.releases); err != nil {
			return err
		}
	}
	return service.LoadReleaseRegistry(ctx)
}

func (service *ClientService) PublishReleaseSet(
	ctx context.Context,
	kind string,
	releases any,
) (int64, bool, error) {
	service.releasePublishMu.Lock()
	defer service.releasePublishMu.Unlock()
	records, err := releaseRecords(kind, releases)
	if err != nil {
		return 0, false, err
	}
	if service.releaseRepository == nil {
		return 0, false, errors.New("release repository is unavailable")
	}
	generation, inserted, err := service.releaseRepository.InsertReleaseSet(ctx, records)
	if err != nil {
		return 0, false, err
	}
	if err := service.LoadReleaseRegistry(ctx); err != nil {
		return 0, false, err
	}
	return generation, inserted, nil
}

func releaseRecords(kind string, releases any) ([]ReleaseRecord, error) {
	values, validType := releaseValues(releases)
	if !validType || len(values) == 0 {
		return nil, ErrInvalid
	}
	records, err := buildReleaseRecords(kind, values)
	if err != nil {
		return nil, err
	}
	if err := validateReleaseSet(kind, records); err != nil {
		return nil, err
	}
	return records, nil
}

func releaseValues(releases any) ([]any, bool) {
	switch typed := releases.(type) {
	case []ClientRelease:
		return toAny(typed), true
	case []BrowserRelease:
		return toAny(typed), true
	case []PlaywrightRelease:
		return toAny(typed), true
	case []LauncherRelease:
		return toAny(typed), true
	case []ProbeRelease:
		return toAny(typed), true
	default:
		return nil, false
	}
}

func buildReleaseRecords(kind string, values []any) ([]ReleaseRecord, error) {
	if kind == "probe" && len(values) != 1 {
		return nil, fmt.Errorf("%w: probe release set must contain one image", ErrInvalid)
	}
	records := make([]ReleaseRecord, len(values))
	for index, value := range values {
		record, err := releaseRecord(kind, value)
		if err != nil {
			return nil, err
		}
		records[index] = record
	}
	return records, nil
}

func validateReleaseSet(kind string, records []ReleaseRecord) error {
	identity := records[0]
	targets := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.Kind != identity.Kind || record.Channel != identity.Channel || record.Version != identity.Version {
			return fmt.Errorf("%w: release set identity must be uniform", ErrInvalid)
		}
		target := record.Platform + "/" + record.Arch
		if _, duplicate := targets[target]; duplicate {
			return fmt.Errorf("%w: duplicate release set platform %s", ErrInvalid, target)
		}
		targets[target] = struct{}{}
	}
	if kind != "probe" && !releasepolicy.CompleteDesktopTargetSet(targets) {
		return fmt.Errorf("%w: desktop release set must contain every supported target", ErrInvalid)
	}
	return nil
}

func toAny[Value any](values []Value) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func (service *ClientService) ReleaseGeneration() int64 {
	return service.releaseGeneration.Load()
}

func releaseRecord(kind string, release any) (ReleaseRecord, error) {
	var record ReleaseRecord
	var validationError error
	switch value := release.(type) {
	case ClientRelease:
		validationError = validateCanonicalClientRelease(value)
		record = ReleaseRecord{Kind: "client", Channel: value.Channel, Platform: value.Platform, Arch: value.Arch, Version: value.Version, PublishedAt: value.PublishedAt}
	case BrowserRelease:
		validationError = validateCanonicalBrowserRelease(value)
		record = ReleaseRecord{Kind: "browser", Channel: value.Channel, Platform: value.Platform, Arch: value.Arch, Version: value.Revision, PublishedAt: value.PublishedAt}
	case PlaywrightRelease:
		validationError = validateCanonicalPlaywrightRelease(value)
		record = ReleaseRecord{Kind: "playwright", Channel: value.Channel, Platform: value.Platform, Arch: value.Arch, Version: value.Version, PublishedAt: value.PublishedAt}
	case LauncherRelease:
		validationError = validateCanonicalLauncherRelease(value)
		record = ReleaseRecord{Kind: "launcher", Channel: value.Channel, Platform: value.Platform, Arch: value.Arch, Version: value.Version, PublishedAt: value.PublishedAt}
	case ProbeRelease:
		validationError = validateCanonicalProbeRelease(value)
		record = ReleaseRecord{Kind: "probe", Channel: value.Channel, Version: value.Version, PublishedAt: value.PublishedAt}
	default:
		return ReleaseRecord{}, ErrInvalid
	}
	if record.Kind != kind || validationError != nil {
		return ReleaseRecord{}, fmt.Errorf("%w: invalid %s release", ErrInvalid, kind)
	}
	payload, err := json.Marshal(release)
	if err != nil {
		return ReleaseRecord{}, err
	}
	envelope, err := json.Marshal(releasePayloadEnvelope{
		SchemaVersion: currentReleasePayloadSchemaVersion,
		Payload:       payload,
	})
	record.SchemaVersion = currentReleasePayloadSchemaVersion
	record.Payload = envelope
	return record, err
}

func validateCanonicalClientRelease(release ClientRelease) error {
	if err := validateClientRelease(release); err != nil {
		return err
	}
	if !canonicalReleaseTarget(release.Channel, release.Platform, release.Arch) ||
		!canonicalSemanticVersion(release.Version) || !canonicalSemanticVersion(release.MinimumLauncherVersion) ||
		!canonicalNumericRevision(release.MinimumBrowserRevision) ||
		!canonicalSemanticVersion(release.PlaywrightVersion) || !canonicalReleaseArtifact(release.Artifact) {
		return errors.New("client release fields must use canonical formatting")
	}
	for keyID := range release.ProbeBootstrapPublicKeys {
		if keyID != strings.TrimSpace(keyID) {
			return errors.New("client Probe key IDs must use canonical formatting")
		}
	}
	return nil
}

func validateCanonicalBrowserRelease(release BrowserRelease) error {
	if err := validateBrowserRelease(release); err != nil {
		return err
	}
	if !canonicalReleaseTarget(release.Channel, release.Platform, release.Arch) ||
		!canonicalNumericRevision(release.Revision) || !canonicalReleaseArtifact(release.Artifact) {
		return errors.New("browser release fields must use canonical formatting")
	}
	if !sort.StringsAreSorted(release.CompatiblePlaywrightVersions) {
		return errors.New("browser compatible Playwright versions must be sorted")
	}
	for _, version := range release.CompatiblePlaywrightVersions {
		if !canonicalSemanticVersion(version) {
			return errors.New("browser compatible Playwright versions must use canonical formatting")
		}
	}
	return nil
}

func validateCanonicalPlaywrightRelease(release PlaywrightRelease) error {
	if err := validatePlaywrightRelease(release); err != nil {
		return err
	}
	return validateCanonicalDesktopArtifactRelease(
		release.Channel, release.Platform, release.Arch, release.Version, release.Artifact, "playwright",
	)
}

func validateCanonicalLauncherRelease(release LauncherRelease) error {
	if err := validateLauncherRelease(release); err != nil {
		return err
	}
	return validateCanonicalDesktopArtifactRelease(
		release.Channel, release.Platform, release.Arch, release.Version, release.Launcher, "launcher",
	)
}

func validateCanonicalDesktopArtifactRelease(
	channel string,
	platform string,
	arch string,
	version string,
	artifact ReleaseArtifact,
	label string,
) error {
	if !canonicalReleaseTarget(channel, platform, arch) ||
		!canonicalSemanticVersion(version) || !canonicalReleaseArtifact(artifact) {
		return fmt.Errorf("%s release fields must use canonical formatting", label)
	}
	return nil
}

func validateCanonicalProbeRelease(release ProbeRelease) error {
	if err := validateProbeRelease(release); err != nil {
		return err
	}
	if release.Channel != "stable" || !canonicalSemanticVersion(release.Version) ||
		!canonicalNumericRevision(release.BrowserRevision) || release.Image != strings.TrimSpace(release.Image) ||
		release.ImageDigest != strings.ToLower(strings.TrimSpace(release.ImageDigest)) {
		return errors.New("probe release fields must use canonical formatting")
	}
	return nil
}

func canonicalReleaseTarget(channel string, platform string, arch string) bool {
	return channel == "stable" && channel == strings.TrimSpace(channel) &&
		platform == strings.TrimSpace(platform) && arch == strings.TrimSpace(arch)
}

func canonicalSemanticVersion(value string) bool {
	prefixed := "v" + value
	return semver.IsValid(prefixed) && semver.Canonical(prefixed) == prefixed
}

func canonicalNumericRevision(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func canonicalReleaseArtifact(artifact ReleaseArtifact) bool {
	return artifact.URL == strings.TrimSpace(artifact.URL) &&
		artifact.SHA256 == strings.ToLower(strings.TrimSpace(artifact.SHA256)) &&
		artifact.Executable == strings.TrimSpace(artifact.Executable)
}

func (service *ClientService) configureAvailableReleases(
	clients []ClientRelease,
	browsers []BrowserRelease,
	playwright []PlaywrightRelease,
	launchers []LauncherRelease,
	probes []ProbeRelease,
	_ int64,
) error {
	catalog, err := configuredReleaseCatalog(releaseRegistrySnapshot{
		clients: clients, browsers: browsers, playwright: playwright, launchers: launchers, probes: probes,
	})
	if err != nil {
		return err
	}
	service.applyReleaseCatalog(catalog, 0)
	return nil
}

func (service *ClientService) applyReleaseCatalog(catalog releasepolicy.Catalog, generation int64) {
	service.releaseMu.Lock()
	service.clients = catalog.Clients
	service.browsers = catalog.Browsers
	service.playwright = catalog.Playwright
	service.launchers = catalog.Launchers
	service.probes = catalog.Probes
	service.releaseGeneration.Store(generation)
	service.releaseMu.Unlock()
}
