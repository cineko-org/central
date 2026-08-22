package central

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ReleaseRecord is the database identity and serialized latest-Proto payload for one release.
type ReleaseRecord struct {
	Kind        string
	Channel     string
	Platform    string
	Arch        string
	Version     string
	Payload     []byte
	PublishedAt time.Time
}

type ReleaseRepository interface {
	ListReleases(context.Context) ([]ReleaseRecord, int64, error)
	CurrentReleaseGeneration(context.Context) (int64, error)
	InsertReleaseSet(context.Context, []ReleaseRecord) (int64, bool, error)
}

func (service *ClientService) ReleaseRegistry(ctx context.Context) (*releasepb.Registry, error) {
	if service.releaseRepository == nil {
		return nil, errors.New("release repository is unavailable")
	}
	records, generation, err := service.releaseRepository.ListReleases(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return nil, err
	}
	return snapshot.registry(generation), nil
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
) (*releasepb.RuntimeRelease, int64, error) {
	return currentDesktopReleaseSnapshot(
		ctx, service, channel, platform, arch, service.CurrentRuntimeRelease, releasepolicy.CurrentRuntime,
	)
}

func (service *ClientService) CurrentLauncherReleaseSnapshot(
	ctx context.Context,
	channel string,
	platform string,
	arch string,
) (*releasepb.LauncherRelease, int64, error) {
	return currentDesktopReleaseSnapshot(
		ctx, service, channel, platform, arch, service.CurrentLauncherRelease, releasepolicy.CurrentLauncher,
	)
}

func (service *ClientService) CurrentProbeReleaseSnapshot(
	ctx context.Context,
	channel string,
) (*releasepb.ProbeRelease, int64, error) {
	return currentChannelReleaseSnapshot(
		ctx, service, channel, service.CurrentProbeRelease, releasepolicy.CurrentProbe,
	)
}

func currentDesktopReleaseSnapshot[Release proto.Message](
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

func currentChannelReleaseSnapshot[Release proto.Message](
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

type releaseRegistrySnapshot struct {
	clients    []*releasepb.ClientRelease
	browsers   []*releasepb.BrowserRelease
	playwright []*releasepb.PlaywrightRelease
	launchers  []*releasepb.LauncherRelease
	probes     []*releasepb.ProbeRelease
}

func (snapshot releaseRegistrySnapshot) registry(generation int64) *releasepb.Registry {
	clients := &releasepb.ClientReleaseSet{}
	clients.SetReleases(snapshot.clients)
	browsers := &releasepb.BrowserReleaseSet{}
	browsers.SetReleases(snapshot.browsers)
	playwright := &releasepb.PlaywrightReleaseSet{}
	playwright.SetReleases(snapshot.playwright)
	launchers := &releasepb.LauncherReleaseSet{}
	launchers.SetReleases(snapshot.launchers)
	probes := &releasepb.ProbeReleaseSet{}
	probes.SetReleases(snapshot.probes)
	registry := &releasepb.Registry{}
	registry.SetGeneration(generation)
	registry.SetClients(clients)
	registry.SetBrowsers(browsers)
	registry.SetPlaywright(playwright)
	registry.SetLaunchers(launchers)
	registry.SetProbes(probes)
	return registry
}

func decodeReleaseRegistry(records []ReleaseRecord) (releaseRegistrySnapshot, error) {
	snapshot := releaseRegistrySnapshot{}
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

func (snapshot *releaseRegistrySnapshot) add(record ReleaseRecord) error {
	message, err := newReleaseMessage(record.Kind)
	if err != nil {
		return err
	}
	if err := protojson.Unmarshal(record.Payload, message); err != nil {
		return fmt.Errorf("decode stored %s release: %w", record.Kind, err)
	}
	expected, err := releaseRecord(record.Kind, message)
	if err != nil {
		return fmt.Errorf("validate stored %s release: %w", record.Kind, err)
	}
	if expected.Channel != record.Channel || expected.Platform != record.Platform ||
		expected.Arch != record.Arch || expected.Version != record.Version {
		return fmt.Errorf("stored %s release identity does not match its payload", record.Kind)
	}
	switch release := message.(type) {
	case *releasepb.ClientRelease:
		snapshot.clients = append(snapshot.clients, release)
	case *releasepb.BrowserRelease:
		snapshot.browsers = append(snapshot.browsers, release)
	case *releasepb.PlaywrightRelease:
		snapshot.playwright = append(snapshot.playwright, release)
	case *releasepb.LauncherRelease:
		snapshot.launchers = append(snapshot.launchers, release)
	case *releasepb.ProbeRelease:
		snapshot.probes = append(snapshot.probes, release)
	}
	return nil
}

func newReleaseMessage(kind string) (proto.Message, error) {
	switch kind {
	case "client":
		return &releasepb.ClientRelease{}, nil
	case "browser":
		return &releasepb.BrowserRelease{}, nil
	case "playwright":
		return &releasepb.PlaywrightRelease{}, nil
	case "launcher":
		return &releasepb.LauncherRelease{}, nil
	case "probe":
		return &releasepb.ProbeRelease{}, nil
	default:
		return nil, fmt.Errorf("stored release has unsupported kind %q", kind)
	}
}

func configuredReleaseCatalog(snapshot releaseRegistrySnapshot) (releasepolicy.Catalog, error) {
	candidate := ClientService{
		clients: make(map[string]*releasepb.ClientRelease), browsers: make(map[string]*releasepb.BrowserRelease),
		playwright: make(map[string]*releasepb.PlaywrightRelease), launchers: make(map[string]*releasepb.LauncherRelease),
		probes: make(map[string]*releasepb.ProbeRelease),
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
	return candidate.releaseCatalog(), nil
}

// ActiveDesktopManifestFingerprint hashes the releases actually selected for every desktop target.
func ActiveDesktopManifestFingerprint(records []ReleaseRecord) (string, error) {
	snapshot, err := decodeReleaseRegistry(records)
	if err != nil {
		return "", err
	}
	catalog, err := configuredReleaseCatalog(snapshot)
	if err != nil {
		return "", err
	}
	selected := releaseRegistrySnapshot{}
	for _, target := range releasepolicy.SupportedDesktopTargets() {
		if launcher, exists := releasepolicy.CurrentLauncher(catalog, "stable", target.Platform, target.Arch); exists {
			selected.launchers = append(selected.launchers, launcher)
		}
		if runtime, exists := releasepolicy.CurrentRuntime(catalog, "stable", target.Platform, target.Arch); exists {
			selected.clients = append(selected.clients, runtime.GetClient())
			selected.browsers = append(selected.browsers, runtime.GetBrowser())
			selected.playwright = append(selected.playwright, runtime.GetPlaywright())
		}
	}
	// The registry is composed exclusively from generated messages, so deterministic
	// marshaling cannot fail on an unsupported handwritten Proto implementation.
	encoded, _ := proto.MarshalOptions{Deterministic: true}.Marshal(selected.registry(0))
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
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
		if set.kind == "probe" && len(set.targets) == 1 {
			if _, valid := set.targets["/"]; valid {
				continue
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
	clients *releasepb.ClientReleaseSet,
	browsers *releasepb.BrowserReleaseSet,
	playwright *releasepb.PlaywrightReleaseSet,
	launchers *releasepb.LauncherReleaseSet,
	probes *releasepb.ProbeReleaseSet,
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
		kind string
		set  proto.Message
		has  bool
	}{
		{kind: "client", set: clients, has: len(clients.GetReleases()) > 0},
		{kind: "browser", set: browsers, has: len(browsers.GetReleases()) > 0},
		{kind: "playwright", set: playwright, has: len(playwright.GetReleases()) > 0},
		{kind: "launcher", set: launchers, has: len(launchers.GetReleases()) > 0},
		{kind: "probe", set: probes, has: len(probes.GetReleases()) > 0},
	}
	for _, seed := range seeds {
		if _, found := existing[seed.kind]; found || !seed.has {
			continue
		}
		if _, _, err := service.PublishReleaseSet(ctx, seed.kind, seed.set); err != nil {
			return err
		}
	}
	return service.LoadReleaseRegistry(ctx)
}

func (service *ClientService) PublishReleaseSet(
	ctx context.Context,
	kind string,
	set proto.Message,
) (int64, bool, error) {
	service.releasePublishMu.Lock()
	defer service.releasePublishMu.Unlock()
	records, err := releaseRecords(kind, set)
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

func releaseRecords(kind string, set proto.Message) ([]ReleaseRecord, error) {
	values, err := releaseMessages(set)
	if err != nil || len(values) == 0 {
		return nil, ErrInvalid
	}
	if kind == "probe" && len(values) != 1 {
		return nil, fmt.Errorf("%w: probe release set must contain one image", ErrInvalid)
	}
	records := make([]ReleaseRecord, len(values))
	for index, value := range values {
		record, recordErr := releaseRecord(kind, value)
		if recordErr != nil {
			return nil, recordErr
		}
		records[index] = record
	}
	if err := validateReleaseSet(kind, records); err != nil {
		return nil, err
	}
	return records, nil
}

func releaseMessages(set proto.Message) ([]proto.Message, error) {
	switch typed := set.(type) {
	case *releasepb.ClientReleaseSet:
		return messages(typed.GetReleases()), nil
	case *releasepb.BrowserReleaseSet:
		return messages(typed.GetReleases()), nil
	case *releasepb.PlaywrightReleaseSet:
		return messages(typed.GetReleases()), nil
	case *releasepb.LauncherReleaseSet:
		return messages(typed.GetReleases()), nil
	case *releasepb.ProbeReleaseSet:
		return messages(typed.GetReleases()), nil
	default:
		return nil, ErrInvalid
	}
}

func messages[Message proto.Message](values []Message) []proto.Message {
	result := make([]proto.Message, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func releaseRecord(kind string, release proto.Message) (ReleaseRecord, error) {
	var record ReleaseRecord
	var validationError error
	switch value := release.(type) {
	case *releasepb.ClientRelease:
		validationError = validateCanonicalClientRelease(value)
		record = ReleaseRecord{Kind: "client", Channel: value.GetChannel(), Platform: value.GetPlatform(), Arch: value.GetArchitecture(), Version: value.GetVersion(), PublishedAt: value.GetPublishedAt().AsTime()}
	case *releasepb.BrowserRelease:
		validationError = validateCanonicalBrowserRelease(value)
		record = ReleaseRecord{Kind: "browser", Channel: value.GetChannel(), Platform: value.GetPlatform(), Arch: value.GetArchitecture(), Version: value.GetRevision(), PublishedAt: value.GetPublishedAt().AsTime()}
	case *releasepb.PlaywrightRelease:
		validationError = validateCanonicalPlaywrightRelease(value)
		record = ReleaseRecord{Kind: "playwright", Channel: value.GetChannel(), Platform: value.GetPlatform(), Arch: value.GetArchitecture(), Version: value.GetVersion(), PublishedAt: value.GetPublishedAt().AsTime()}
	case *releasepb.LauncherRelease:
		validationError = validateCanonicalLauncherRelease(value)
		record = ReleaseRecord{Kind: "launcher", Channel: value.GetChannel(), Platform: value.GetPlatform(), Arch: value.GetArchitecture(), Version: value.GetVersion(), PublishedAt: value.GetPublishedAt().AsTime()}
	case *releasepb.ProbeRelease:
		validationError = validateCanonicalProbeRelease(value)
		record = ReleaseRecord{Kind: "probe", Channel: value.GetChannel(), Version: value.GetVersion(), PublishedAt: value.GetPublishedAt().AsTime()}
	default:
		return ReleaseRecord{}, ErrInvalid
	}
	if record.Kind != kind || validationError != nil {
		return ReleaseRecord{}, fmt.Errorf("%w: invalid %s release", ErrInvalid, kind)
	}
	payload, err := protojson.Marshal(release)
	if err != nil {
		return ReleaseRecord{}, err
	}
	record.Payload = payload
	return record, nil
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

func validateCanonicalClientRelease(release *releasepb.ClientRelease) error {
	if err := validateClientRelease(release); err != nil {
		return err
	}
	if !canonicalReleaseTarget(release.GetChannel(), release.GetPlatform(), release.GetArchitecture()) ||
		!canonicalSemanticVersion(release.GetVersion()) ||
		!canonicalSemanticVersion(release.GetMinimumLauncherVersion()) ||
		!canonicalNumericRevision(release.GetMinimumBrowserRevision()) ||
		!canonicalSemanticVersion(release.GetPlaywrightVersion()) ||
		!canonicalReleaseArtifact(release.GetArtifact()) {
		return errors.New("client release fields must use canonical formatting")
	}
	for keyID := range release.GetProbeBootstrapPublicKeys() {
		if keyID != strings.TrimSpace(keyID) {
			return errors.New("client Probe key IDs must use canonical formatting")
		}
	}
	return nil
}

func validateCanonicalBrowserRelease(release *releasepb.BrowserRelease) error {
	if err := validateBrowserRelease(release); err != nil {
		return err
	}
	if !canonicalReleaseTarget(release.GetChannel(), release.GetPlatform(), release.GetArchitecture()) ||
		!canonicalNumericRevision(release.GetRevision()) || !canonicalReleaseArtifact(release.GetArtifact()) {
		return errors.New("browser release fields must use canonical formatting")
	}
	versions := release.GetCompatiblePlaywrightVersions()
	if !sort.StringsAreSorted(versions) {
		return errors.New("browser compatible Playwright versions must be sorted")
	}
	for _, version := range versions {
		if !canonicalSemanticVersion(version) {
			return errors.New("browser compatible Playwright versions must use canonical formatting")
		}
	}
	return nil
}

func validateCanonicalPlaywrightRelease(release *releasepb.PlaywrightRelease) error {
	if err := validatePlaywrightRelease(release); err != nil {
		return err
	}
	return validateCanonicalDesktopArtifactRelease(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetVersion(),
		release.GetArtifact(), "playwright",
	)
}

func validateCanonicalLauncherRelease(release *releasepb.LauncherRelease) error {
	if err := validateLauncherRelease(release); err != nil {
		return err
	}
	return validateCanonicalDesktopArtifactRelease(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetVersion(),
		release.GetLauncher(), "launcher",
	)
}

func validateCanonicalDesktopArtifactRelease(
	channel string,
	platform string,
	arch string,
	version string,
	artifact *releasepb.Artifact,
	label string,
) error {
	if !canonicalReleaseTarget(channel, platform, arch) ||
		!canonicalSemanticVersion(version) || !canonicalReleaseArtifact(artifact) {
		return fmt.Errorf("%s release fields must use canonical formatting", label)
	}
	return nil
}

func validateCanonicalProbeRelease(release *releasepb.ProbeRelease) error {
	if err := validateProbeRelease(release); err != nil {
		return err
	}
	if release.GetChannel() != "stable" || !canonicalSemanticVersion(release.GetVersion()) ||
		!canonicalNumericRevision(release.GetBrowserRevision()) ||
		release.GetImage() != strings.TrimSpace(release.GetImage()) ||
		release.GetImageDigest() != strings.ToLower(strings.TrimSpace(release.GetImageDigest())) {
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

func canonicalReleaseArtifact(artifact *releasepb.Artifact) bool {
	return artifact != nil && artifact.GetUrl() == strings.TrimSpace(artifact.GetUrl()) &&
		artifact.GetSha256() == strings.ToLower(strings.TrimSpace(artifact.GetSha256())) &&
		artifact.GetExecutable() == strings.TrimSpace(artifact.GetExecutable())
}

func (service *ClientService) ReleaseGeneration() int64 {
	return service.releaseGeneration.Load()
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
