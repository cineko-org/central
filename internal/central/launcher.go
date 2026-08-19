package central

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"

	"golang.org/x/mod/semver"
)

const DefaultLaunchTicketTTL = time.Minute

type ReleaseArtifact = contracts.ReleaseArtifact

type ClientRelease = contracts.ClientRelease

type BrowserRelease = contracts.BrowserRelease

type PlaywrightRelease = contracts.PlaywrightRelease

type RuntimeRelease = contracts.RuntimeRelease

type LauncherRelease = contracts.LauncherRelease

type ProbeRelease = contracts.ProbeRelease

type LaunchTicketRequest = contracts.LaunchTicketRequest

type LaunchTicketResponse = contracts.LaunchTicketResponse

type ClientSessionExchangeRequest = contracts.ClientSessionExchangeRequest

type ClientLaunchContext = contracts.ClientLaunchContext

type LaunchedClient struct {
	User    ClientUser
	Context ClientLaunchContext
}

type LaunchTicket struct {
	ID                       string
	UserID                   string
	InstallationID           string
	DeviceID                 string
	ReleaseGeneration        int64
	ClientVersion            string
	ArtifactSHA256           string
	Protocol                 int
	BrowserRevision          string
	BrowserArtifactSHA256    string
	PlaywrightVersion        string
	PlaywrightArtifactSHA256 string
	LauncherNonce            string
	TokenHash                [32]byte
	ExpiresAt                time.Time
	CreatedAt                time.Time
}

type ProbeBootstrapTicketRequest = contracts.ProbeBootstrapTicketRequest

type ProbeBootstrapTicketResponse = contracts.ProbeBootstrapTicketResponse

func (service *ClientService) ConfigureReleases(releases []ClientRelease) error {
	if len(releases) == 0 {
		return errors.New("at least one client release is required")
	}
	configured := make(map[string]ClientRelease, len(releases))
	for index := range releases {
		release := releases[index]
		release.Channel = strings.TrimSpace(release.Channel)
		release.Platform = strings.TrimSpace(release.Platform)
		release.Arch = strings.TrimSpace(release.Arch)
		release.Version = strings.TrimSpace(release.Version)
		release.MinimumLauncherVersion = strings.TrimSpace(release.MinimumLauncherVersion)
		release.MinimumBrowserRevision = strings.TrimSpace(release.MinimumBrowserRevision)
		release.PlaywrightVersion = strings.TrimSpace(release.PlaywrightVersion)
		if err := validateClientRelease(release); err != nil {
			return fmt.Errorf("client release %d: %w", index, err)
		}
		identity := componentReleaseKey(release.Channel, release.Platform, release.Arch, release.Version)
		if _, duplicate := configured[identity]; duplicate {
			return fmt.Errorf("duplicate client release for %s", identity)
		}
		configured[identity] = release
	}
	service.releaseMu.Lock()
	service.clients = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) ConfigureBrowserReleases(releases []BrowserRelease) error {
	if len(releases) == 0 {
		return errors.New("at least one browser release is required")
	}
	configured := make(map[string]BrowserRelease, len(releases))
	for index := range releases {
		release := releases[index]
		release.Channel = strings.TrimSpace(release.Channel)
		release.Platform = strings.TrimSpace(release.Platform)
		release.Arch = strings.TrimSpace(release.Arch)
		release.Revision = strings.TrimSpace(release.Revision)
		for versionIndex := range release.CompatiblePlaywrightVersions {
			release.CompatiblePlaywrightVersions[versionIndex] = strings.TrimSpace(
				release.CompatiblePlaywrightVersions[versionIndex],
			)
		}
		if err := validateBrowserRelease(release); err != nil {
			return fmt.Errorf("browser release %d: %w", index, err)
		}
		key := componentReleaseKey(release.Channel, release.Platform, release.Arch, release.Revision)
		if _, duplicate := configured[key]; duplicate {
			return fmt.Errorf("duplicate browser release for %s", key)
		}
		configured[key] = release
	}
	service.releaseMu.Lock()
	service.browsers = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) ConfigurePlaywrightReleases(releases []PlaywrightRelease) error {
	configured, err := configureVersionedReleaseMap(
		releases,
		"playwright",
		normalizePlaywrightRelease,
		validatePlaywrightRelease,
		playwrightReleaseIdentity,
	)
	if err != nil {
		return err
	}
	service.releaseMu.Lock()
	service.playwright = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) CurrentRuntimeRelease(channel, platform, arch string) (RuntimeRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	return currentRuntimeReleaseFromCatalog(service.releaseCatalog(), channel, platform, arch)
}

func currentRuntimeReleaseFromCatalog(
	catalog releaseCatalog,
	channel string,
	platform string,
	arch string,
) (RuntimeRelease, error) {
	release, exists := selectCurrentRuntimeRelease(catalog, channel, platform, arch)
	if !exists {
		return RuntimeRelease{}, ErrNotFound
	}
	return release, nil
}

func selectCurrentRuntimeRelease(
	catalog releaseCatalog,
	channel string,
	platform string,
	arch string,
) (RuntimeRelease, bool) {
	prefix := releaseKey(channel, platform, arch) + "/"
	candidates := make([]ClientRelease, 0, len(catalog.clients))
	for key, client := range catalog.clients {
		if strings.HasPrefix(key, prefix) {
			candidates = append(candidates, client)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return semver.Compare(canonicalVersion(candidates[left].Version), canonicalVersion(candidates[right].Version)) > 0
	})
	launcher, launcherExists := selectCurrentLauncherRelease(catalog, channel, platform, arch)
	if !launcherExists {
		return RuntimeRelease{}, false
	}
	for _, client := range candidates {
		if semver.Compare(canonicalVersion(launcher.Version), canonicalVersion(client.MinimumLauncherVersion)) < 0 {
			continue
		}
		playwright, playwrightExists := catalog.playwright[componentReleaseKey(
			channel, platform, arch, client.PlaywrightVersion,
		)]
		if !playwrightExists {
			continue
		}
		browser, browserExists := compatibleBrowserReleaseFromCatalog(
			catalog,
			channel, platform, arch, client.MinimumBrowserRevision, playwright.Version,
		)
		if browserExists {
			return RuntimeRelease{Client: client, Browser: browser, Playwright: playwright}, true
		}
	}
	return RuntimeRelease{}, false
}

func (service *ClientService) compatibleBrowserRelease(
	channel string,
	platform string,
	arch string,
	minimumRevision string,
	playwrightVersion string,
) (BrowserRelease, bool) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	return compatibleBrowserReleaseFromCatalog(
		service.releaseCatalog(), channel, platform, arch, minimumRevision, playwrightVersion,
	)
}

func compatibleBrowserReleaseFromCatalog(
	catalog releaseCatalog,
	channel string,
	platform string,
	arch string,
	minimumRevision string,
	playwrightVersion string,
) (BrowserRelease, bool) {
	prefix := releaseKey(channel, platform, arch) + "/"
	var selected BrowserRelease
	for key, release := range catalog.browsers {
		if !strings.HasPrefix(key, prefix) || compareNumericRevision(release.Revision, minimumRevision) < 0 ||
			!containsString(release.CompatiblePlaywrightVersions, playwrightVersion) {
			continue
		}
		if selected.Revision == "" || compareNumericRevision(release.Revision, selected.Revision) > 0 {
			selected = release
		}
	}
	return selected, selected.Revision != ""
}

func (service *ClientService) ConfigureLauncherReleases(releases []LauncherRelease) error {
	configured, err := configureVersionedReleaseMap(
		releases,
		"launcher",
		normalizeLauncherRelease,
		validateLauncherRelease,
		launcherReleaseIdentity,
	)
	if err != nil {
		return err
	}
	service.releaseMu.Lock()
	service.launchers = configured
	service.releaseMu.Unlock()
	return nil
}

func normalizePlaywrightRelease(release *PlaywrightRelease) {
	release.Channel = strings.TrimSpace(release.Channel)
	release.Platform = strings.TrimSpace(release.Platform)
	release.Arch = strings.TrimSpace(release.Arch)
	release.Version = strings.TrimSpace(release.Version)
}

func playwrightReleaseIdentity(release PlaywrightRelease) string {
	return componentReleaseKey(release.Channel, release.Platform, release.Arch, release.Version)
}

func normalizeLauncherRelease(release *LauncherRelease) {
	release.Channel = strings.TrimSpace(release.Channel)
	release.Platform = strings.TrimSpace(release.Platform)
	release.Arch = strings.TrimSpace(release.Arch)
	release.Version = strings.TrimSpace(release.Version)
}

func launcherReleaseIdentity(release LauncherRelease) string {
	return componentReleaseKey(release.Channel, release.Platform, release.Arch, release.Version)
}

func configureVersionedReleaseMap[Release any](
	releases []Release,
	label string,
	normalize func(*Release),
	validate func(Release) error,
	identity func(Release) string,
) (map[string]Release, error) {
	if len(releases) == 0 {
		return nil, fmt.Errorf("at least one %s release is required", label)
	}
	configured := make(map[string]Release, len(releases))
	for index := range releases {
		release := releases[index]
		normalize(&release)
		if err := validate(release); err != nil {
			return nil, fmt.Errorf("%s release %d: %w", label, index, err)
		}
		key := identity(release)
		if _, duplicate := configured[key]; duplicate {
			return nil, fmt.Errorf("duplicate %s release for %s", label, key)
		}
		configured[key] = release
	}
	return configured, nil
}

func (service *ClientService) CurrentLauncherRelease(channel, platform, arch string) (LauncherRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	return currentLauncherReleaseFromCatalog(service.releaseCatalog(), channel, platform, arch)
}

func currentLauncherReleaseFromCatalog(
	catalog releaseCatalog,
	channel string,
	platform string,
	arch string,
) (LauncherRelease, error) {
	release, exists := selectCurrentLauncherRelease(catalog, channel, platform, arch)
	if !exists {
		return LauncherRelease{}, ErrNotFound
	}
	return release, nil
}

func selectCurrentLauncherRelease(
	catalog releaseCatalog,
	channel string,
	platform string,
	arch string,
) (LauncherRelease, bool) {
	prefix := releaseKey(channel, platform, arch) + "/"
	var release LauncherRelease
	for key, candidate := range catalog.launchers {
		if strings.HasPrefix(key, prefix) && (release.Version == "" ||
			semver.Compare(canonicalVersion(candidate.Version), canonicalVersion(release.Version)) > 0) {
			release = candidate
		}
	}
	exists := release.Version != ""
	return release, exists
}

func (service *ClientService) ConfigureProbeReleases(releases []ProbeRelease) error {
	if len(releases) == 0 {
		return errors.New("at least one Probe release is required")
	}
	configured := make(map[string]ProbeRelease, len(releases))
	for index := range releases {
		release := releases[index]
		release.Channel = strings.TrimSpace(release.Channel)
		release.Version = strings.TrimSpace(release.Version)
		release.BrowserRevision = strings.TrimSpace(release.BrowserRevision)
		release.Image = strings.TrimSpace(release.Image)
		release.ImageDigest = strings.ToLower(strings.TrimSpace(release.ImageDigest))
		if err := validateProbeRelease(release); err != nil {
			return fmt.Errorf("Probe release %d: %w", index, err)
		}
		identity := release.Channel + "/" + release.Version
		if _, duplicate := configured[identity]; duplicate {
			return fmt.Errorf("duplicate Probe release for %s", identity)
		}
		configured[identity] = release
	}
	service.releaseMu.Lock()
	service.probes = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) CurrentProbeRelease(channel string) (ProbeRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	return currentProbeReleaseFromCatalog(service.releaseCatalog(), channel)
}

func currentProbeReleaseFromCatalog(catalog releaseCatalog, channel string) (ProbeRelease, error) {
	prefix := strings.TrimSpace(channel) + "/"
	var selected ProbeRelease
	for key, release := range catalog.probes {
		if strings.HasPrefix(key, prefix) && (selected.Version == "" ||
			semver.Compare(canonicalVersion(release.Version), canonicalVersion(selected.Version)) > 0) {
			selected = release
		}
	}
	if selected.Version == "" {
		return ProbeRelease{}, ErrNotFound
	}
	return selected, nil
}

func (service *ClientService) releaseCatalog() releaseCatalog {
	return releaseCatalog{
		clients: service.clients, browsers: service.browsers, playwright: service.playwright,
		launchers: service.launchers, probes: service.probes,
	}
}

func (service *ClientService) IssueLaunchTicket(
	ctx context.Context,
	principal ClientPrincipal,
	request LaunchTicketRequest,
) (LaunchTicketResponse, error) {
	request = normalizeLaunchTicketRequest(request)
	if !launchTicketRequestValid(request) {
		return LaunchTicketResponse{}, ErrInvalid
	}
	device, err := service.repository.GetClientDevice(ctx, principal.UserID, request.InstallationID)
	if err != nil || device.DeviceID != request.DeviceID {
		return LaunchTicketResponse{}, ErrUnauthorized
	}
	runtimeRelease, generation, err := service.CurrentRuntimeReleaseSnapshot(
		ctx, "stable", device.Platform, device.Arch,
	)
	if err != nil || request.ReleaseGeneration != generation || !runtimeMatchesLaunchRequest(runtimeRelease, request) {
		return LaunchTicketResponse{}, ErrStaleRelease
	}
	token, tokenHash, err := service.secret("clt_")
	if err != nil {
		return LaunchTicketResponse{}, err
	}
	ticketID, _, err := service.secret("launch_")
	if err != nil {
		return LaunchTicketResponse{}, err
	}
	now := service.clock().UTC()
	ticket := LaunchTicket{
		ID: ticketID, UserID: principal.UserID, InstallationID: request.InstallationID,
		DeviceID: request.DeviceID, ReleaseGeneration: request.ReleaseGeneration,
		ClientVersion:  request.ClientVersion,
		ArtifactSHA256: request.ArtifactSHA256, Protocol: request.Protocol,
		BrowserRevision: request.BrowserRevision, BrowserArtifactSHA256: request.BrowserArtifactSHA256,
		PlaywrightVersion: request.PlaywrightVersion, PlaywrightArtifactSHA256: request.PlaywrightArtifactSHA256,
		LauncherNonce: request.Nonce,
		TokenHash:     tokenHash, ExpiresAt: now.Add(DefaultLaunchTicketTTL), CreatedAt: now,
	}
	if err := service.repository.CreateLaunchTicket(ctx, ticket); err != nil {
		return LaunchTicketResponse{}, err
	}
	return LaunchTicketResponse{LaunchTicket: token, ExpiresAt: ticket.ExpiresAt}, nil
}

func normalizeLaunchTicketRequest(request LaunchTicketRequest) LaunchTicketRequest {
	request.InstallationID = strings.TrimSpace(request.InstallationID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.ClientVersion = strings.TrimSpace(request.ClientVersion)
	request.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(request.ArtifactSHA256))
	request.BrowserRevision = strings.TrimSpace(request.BrowserRevision)
	request.BrowserArtifactSHA256 = strings.ToLower(strings.TrimSpace(request.BrowserArtifactSHA256))
	request.PlaywrightVersion = strings.TrimSpace(request.PlaywrightVersion)
	request.PlaywrightArtifactSHA256 = strings.ToLower(strings.TrimSpace(request.PlaywrightArtifactSHA256))
	request.Nonce = strings.TrimSpace(request.Nonce)
	return request
}

func launchTicketRequestValid(request LaunchTicketRequest) bool {
	return request.InstallationID != "" && request.DeviceID != "" && request.ReleaseGeneration > 0 &&
		request.ClientVersion != "" && validSHA256(request.ArtifactSHA256) && request.Protocol == ProtocolVersion &&
		request.BrowserRevision != "" && validSHA256(request.BrowserArtifactSHA256) &&
		request.PlaywrightVersion != "" && validSHA256(request.PlaywrightArtifactSHA256) && len(request.Nonce) >= 16
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	// DecodeString always yields 32 bytes after the exact 64-character check.
	_, err := hex.DecodeString(value)
	return err == nil
}

func runtimeMatchesLaunchRequest(runtimeRelease RuntimeRelease, request LaunchTicketRequest) bool {
	return runtimeRelease.Client.Version == request.ClientVersion &&
		runtimeRelease.Client.Artifact.SHA256 == request.ArtifactSHA256 &&
		runtimeRelease.Client.Protocol == request.Protocol &&
		runtimeRelease.Browser.Revision == request.BrowserRevision &&
		runtimeRelease.Browser.Artifact.SHA256 == request.BrowserArtifactSHA256 &&
		runtimeRelease.Playwright.Version == request.PlaywrightVersion &&
		runtimeRelease.Playwright.Artifact.SHA256 == request.PlaywrightArtifactSHA256
}

func (service *ClientService) ExchangeLaunchTicket(
	ctx context.Context,
	request ClientSessionExchangeRequest,
) (AuthExchangeResponse, error) {
	request.LaunchTicket = strings.TrimSpace(request.LaunchTicket)
	request.ClientNonce = strings.TrimSpace(request.ClientNonce)
	if request.LaunchTicket == "" || len(request.ClientNonce) < 16 {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	response, session, err := service.issueSession(ClientUser{}, now)
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	generation, err := service.CurrentReleaseGeneration(ctx)
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	launched, err := service.repository.ExchangeLaunchTicket(
		ctx, sha256.Sum256([]byte(request.LaunchTicket)), request.ClientNonce,
		generation, session, now,
	)
	if err != nil {
		if errors.Is(err, ErrStaleRelease) {
			return AuthExchangeResponse{}, ErrStaleRelease
		}
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	response.User = launched.User
	response.Launch = &launched.Context
	return response, nil
}

func (service *ClientService) AuthorizeProbeBootstrap(
	ctx context.Context,
	principal ClientPrincipal,
	request ProbeBootstrapTicketRequest,
) (ClientDevice, error) {
	request.InstallationID = strings.TrimSpace(request.InstallationID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	if request.InstallationID == "" || request.DeviceID == "" || request.MaxConcurrency != 1 ||
		!validClientProbeCapabilities(request.Capabilities) {
		return ClientDevice{}, ErrInvalid
	}
	device, err := service.repository.GetClientDevice(ctx, principal.UserID, request.InstallationID)
	if err != nil || device.DeviceID != request.DeviceID {
		return ClientDevice{}, ErrUnauthorized
	}
	release, _, err := service.CurrentRuntimeReleaseSnapshot(ctx, "stable", device.Platform, device.Arch)
	if err != nil || request.Runtime != (Runtime{
		Version: release.Client.Version, Protocol: release.Client.Protocol, BrowserRevision: release.Browser.Revision,
		Platform: release.Client.Platform, Arch: release.Client.Arch,
	}) {
		return ClientDevice{}, ErrUnauthorized
	}
	return device, nil
}

func validClientProbeCapabilities(values []string) bool {
	if len(values) < 1 || len(values) > 3 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !contracts.IsSupportedCapability(value) {
			return false
		}
		seen[value] = struct{}{}
	}
	_, schedule := seen[contracts.CapabilityCGVScheduleCapture]
	return schedule && len(seen) == len(values)
}

func validateClientRelease(release ClientRelease) error {
	if err := validateClientReleaseIdentity(release); err != nil {
		return err
	}
	if err := validateClientReleaseVersions(release); err != nil {
		return err
	}
	if err := validateReleaseArtifact(release.Artifact); err != nil {
		return fmt.Errorf("client artifact: %w", err)
	}
	return validateProbeBootstrapKeyring(release.ProbeBootstrapPublicKeys)
}

func validateClientReleaseIdentity(release ClientRelease) error {
	if release.Channel != "stable" || !supportedPlatform(release.Platform, release.Arch) ||
		release.Version == "" || release.MinimumLauncherVersion == "" ||
		release.MinimumBrowserRevision == "" || release.PlaywrightVersion == "" ||
		release.Protocol != ProtocolVersion || release.PublishedAt.IsZero() {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	return nil
}

func validateClientReleaseVersions(release ClientRelease) error {
	if !semver.IsValid(canonicalVersion(release.Version)) ||
		!semver.IsValid(canonicalVersion(release.MinimumLauncherVersion)) ||
		!semver.IsValid(canonicalVersion(release.PlaywrightVersion)) {
		return errors.New("client, minimum Launcher, and Playwright versions must be semantic versions")
	}
	if !isNumericRevision(release.MinimumBrowserRevision) {
		return errors.New("minimum browser revision must be numeric")
	}
	return nil
}

func validateProbeBootstrapKeyring(keyring map[string]string) error {
	if len(keyring) == 0 {
		return errors.New("at least one Probe bootstrap public key is required")
	}
	for keyID, contents := range keyring {
		if strings.TrimSpace(keyID) == "" || len(contents) > 8<<10 ||
			!strings.Contains(contents, "-----BEGIN PUBLIC KEY-----") {
			return errors.New("Probe bootstrap public keyring is invalid")
		}
	}
	return nil
}

func validateBrowserRelease(release BrowserRelease) error {
	if release.Channel != "stable" || !supportedPlatform(release.Platform, release.Arch) ||
		!isNumericRevision(release.Revision) || release.PublishedAt.IsZero() ||
		len(release.CompatiblePlaywrightVersions) == 0 {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	seen := make(map[string]struct{}, len(release.CompatiblePlaywrightVersions))
	for _, version := range release.CompatiblePlaywrightVersions {
		if !semver.IsValid(canonicalVersion(version)) {
			return errors.New("compatible Playwright versions must not be empty")
		}
		if _, duplicate := seen[version]; duplicate {
			return errors.New("compatible Playwright versions must be unique")
		}
		seen[version] = struct{}{}
	}
	if err := validateReleaseArtifact(release.Artifact); err != nil {
		return fmt.Errorf("browser artifact: %w", err)
	}
	return nil
}

func validatePlaywrightRelease(release PlaywrightRelease) error {
	if release.Channel != "stable" || !supportedPlatform(release.Platform, release.Arch) ||
		release.Version == "" || release.PublishedAt.IsZero() {
		return errors.New("release identity and publishedAt are required")
	}
	if !semver.IsValid(canonicalVersion(release.Version)) {
		return errors.New("playwright version must be a semantic version")
	}
	if err := validateReleaseArtifact(release.Artifact); err != nil {
		return fmt.Errorf("playwright artifact: %w", err)
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func validateLauncherRelease(release LauncherRelease) error {
	if release.Channel != "stable" || !supportedPlatform(release.Platform, release.Arch) ||
		release.Version == "" || release.Protocol != ProtocolVersion || release.PublishedAt.IsZero() {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	if !semver.IsValid(canonicalVersion(release.Version)) {
		return errors.New("launcher version must be a semantic version")
	}
	if err := validateReleaseArtifact(release.Launcher); err != nil {
		return fmt.Errorf("launcher artifact: %w", err)
	}
	return nil
}

func validateProbeRelease(release ProbeRelease) error {
	if release.Channel != "stable" || !semver.IsValid(canonicalVersion(release.Version)) ||
		release.Protocol != ProtocolVersion || !isNumericRevision(release.BrowserRevision) ||
		release.PublishedAt.IsZero() {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	lastSlash := strings.LastIndex(release.Image, "/")
	if release.Image == "" || strings.ContainsAny(release.Image, " \t\r\n@") ||
		strings.Contains(release.Image, "://") || lastSlash <= 0 ||
		strings.Contains(release.Image[lastSlash+1:], ":") {
		return errors.New("Probe image must be an untagged OCI registry repository")
	}
	digest := strings.TrimPrefix(release.ImageDigest, "sha256:")
	decoded, err := hex.DecodeString(digest)
	if !strings.HasPrefix(release.ImageDigest, "sha256:") || err != nil || len(decoded) != sha256.Size {
		return errors.New("Probe imageDigest must be a sha256 OCI digest")
	}
	return nil
}

func validateReleaseArtifact(artifact ReleaseArtifact) error {
	parsed, err := url.Parse(strings.TrimSpace(artifact.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || artifact.Size <= 0 {
		return errors.New("HTTPS URL without credentials/fragment and positive size are required")
	}
	digest, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(artifact.SHA256)))
	if err != nil || len(digest) != sha256.Size {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	executable := strings.TrimSpace(artifact.Executable)
	if executable == "" || path.IsAbs(executable) || path.Clean(executable) != executable ||
		strings.HasPrefix(executable, "../") {
		return errors.New("executable must be a clean relative archive path")
	}
	return nil
}

func supportedPlatform(platform, arch string) bool {
	_, supported := supportedDesktopReleaseTargets[platform+"/"+arch]
	return supported
}

var supportedDesktopReleaseTargets = map[string]struct{}{
	"darwin/arm64":  {},
	"linux/amd64":   {},
	"windows/amd64": {},
}

func releaseKey(channel, platform, arch string) string {
	return strings.TrimSpace(channel) + "/" + strings.TrimSpace(platform) + "/" + strings.TrimSpace(arch)
}

func componentReleaseKey(channel, platform, arch, version string) string {
	return releaseKey(channel, platform, arch) + "/" + strings.TrimSpace(version)
}
