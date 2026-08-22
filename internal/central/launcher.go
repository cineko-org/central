package central

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	probedomain "github.com/cineko-org/central/internal/domain/probe"
	releasepolicy "github.com/cineko-org/central/internal/domain/releases"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"

	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const DefaultLaunchTicketTTL = time.Minute

type LaunchedClient struct {
	User    *clientpb.User
	Context *clientpb.LaunchContext
}

type LaunchTicket struct {
	ID                       string
	UserID                   string
	InstallationID           string
	DeviceID                 string
	ReleaseGeneration        int64
	ClientVersion            string
	ArtifactSHA256           string
	BrowserRevision          string
	BrowserArtifactSHA256    string
	PlaywrightVersion        string
	PlaywrightArtifactSHA256 string
	LauncherNonce            string
	TokenHash                [32]byte
	ExpiresAt                time.Time
	CreatedAt                time.Time
}

func (service *ClientService) ConfigureReleases(releases []*releasepb.ClientRelease) error {
	configured, err := configureVersionedReleaseMap(
		releases, "client", normalizeClientRelease, validateClientRelease, clientReleaseIdentity,
	)
	if err != nil {
		return err
	}
	service.releaseMu.Lock()
	service.clients = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) ConfigureBrowserReleases(releases []*releasepb.BrowserRelease) error {
	configured, err := configureVersionedReleaseMap(
		releases, "browser", normalizeBrowserRelease, validateBrowserRelease, browserReleaseIdentity,
	)
	if err != nil {
		return err
	}
	service.releaseMu.Lock()
	service.browsers = configured
	service.releaseMu.Unlock()
	return nil
}

func (service *ClientService) ConfigurePlaywrightReleases(releases []*releasepb.PlaywrightRelease) error {
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

func (service *ClientService) CurrentRuntimeRelease(channel, platform, arch string) (*releasepb.RuntimeRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	if release, ok := releasepolicy.CurrentRuntime(service.releaseCatalog(), channel, platform, arch); ok {
		return release, nil
	}
	return nil, ErrNotFound
}

func (service *ClientService) ConfigureLauncherReleases(releases []*releasepb.LauncherRelease) error {
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

func normalizePlaywrightRelease(release **releasepb.PlaywrightRelease) {
	*release = clonePlaywrightRelease(*release)
	normalizeDesktopReleaseIdentity(
		(*release).GetChannel(), (*release).GetPlatform(), (*release).GetArchitecture(), (*release).GetVersion(),
		(*release).SetChannel, (*release).SetPlatform, (*release).SetArchitecture, (*release).SetVersion,
	)
}

func playwrightReleaseIdentity(release *releasepb.PlaywrightRelease) string {
	return releasepolicy.ComponentKey(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetVersion(),
	)
}

func normalizeLauncherRelease(release **releasepb.LauncherRelease) {
	*release = cloneLauncherRelease(*release)
	normalizeDesktopReleaseIdentity(
		(*release).GetChannel(), (*release).GetPlatform(), (*release).GetArchitecture(), (*release).GetVersion(),
		(*release).SetChannel, (*release).SetPlatform, (*release).SetArchitecture, (*release).SetVersion,
	)
}

func normalizeDesktopReleaseIdentity(
	channel, platform, architecture, version string,
	setChannel, setPlatform, setArchitecture, setVersion func(string),
) {
	setChannel(strings.TrimSpace(channel))
	setPlatform(strings.TrimSpace(platform))
	setArchitecture(strings.TrimSpace(architecture))
	setVersion(strings.TrimSpace(version))
}

func launcherReleaseIdentity(release *releasepb.LauncherRelease) string {
	return releasepolicy.ComponentKey(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetVersion(),
	)
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

func (service *ClientService) CurrentLauncherRelease(channel, platform, arch string) (*releasepb.LauncherRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	if release, ok := releasepolicy.CurrentLauncher(service.releaseCatalog(), channel, platform, arch); ok {
		return release, nil
	}
	return nil, ErrNotFound
}

func (service *ClientService) ConfigureProbeReleases(releases []*releasepb.ProbeRelease) error {
	if len(releases) == 0 {
		return errors.New("at least one Probe release is required")
	}
	configured := make(map[string]*releasepb.ProbeRelease, len(releases))
	for index := range releases {
		release := cloneProbeRelease(releases[index])
		release.SetChannel(strings.TrimSpace(release.GetChannel()))
		release.SetVersion(strings.TrimSpace(release.GetVersion()))
		release.SetBrowserRevision(strings.TrimSpace(release.GetBrowserRevision()))
		release.SetImage(strings.TrimSpace(release.GetImage()))
		release.SetImageDigest(strings.ToLower(strings.TrimSpace(release.GetImageDigest())))
		if err := validateProbeRelease(release); err != nil {
			return fmt.Errorf("Probe release %d: %w", index, err)
		}
		identity := release.GetChannel() + "/" + release.GetVersion()
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

func (service *ClientService) CurrentProbeRelease(channel string) (*releasepb.ProbeRelease, error) {
	service.releaseMu.RLock()
	defer service.releaseMu.RUnlock()
	if release, ok := releasepolicy.CurrentProbe(service.releaseCatalog(), channel); ok {
		return release, nil
	}
	return nil, ErrNotFound
}

func (service *ClientService) releaseCatalog() releasepolicy.Catalog {
	return releasepolicy.Catalog{
		Clients: service.clients, Browsers: service.browsers, Playwright: service.playwright,
		Launchers: service.launchers, Probes: service.probes,
	}
}

func (service *ClientService) IssueLaunchTicket(
	ctx context.Context,
	principal ClientPrincipal,
	request *clientpb.LaunchTicketRequest,
) (*clientpb.LaunchTicketResponse, error) {
	request = normalizeLaunchTicketRequest(request)
	if !launchTicketRequestValid(request) {
		return nil, ErrInvalid
	}
	launchContext := request.GetContext()
	device, err := service.repository.GetClientDevice(ctx, principal.UserID, launchContext.GetInstallationId())
	if err != nil || device.GetDeviceId() != launchContext.GetDeviceId() {
		return nil, ErrUnauthorized
	}
	runtimeRelease, generation, err := service.CurrentRuntimeReleaseSnapshot(
		ctx, "stable", device.GetPlatform(), device.GetArchitecture(),
	)
	if err != nil || launchContext.GetReleaseGeneration() != generation ||
		!releasepolicy.RuntimeMatchesLaunchRequest(runtimeRelease, launchContext) {
		return nil, ErrStaleRelease
	}
	token, tokenHash, err := service.secret("clt_")
	if err != nil {
		return nil, err
	}
	ticketID, _, err := service.secret("launch_")
	if err != nil {
		return nil, err
	}
	now := service.clock().UTC()
	ticket := LaunchTicket{
		ID: ticketID, UserID: principal.UserID, InstallationID: launchContext.GetInstallationId(),
		DeviceID: launchContext.GetDeviceId(), ReleaseGeneration: launchContext.GetReleaseGeneration(),
		ClientVersion:   launchContext.GetClientVersion(),
		ArtifactSHA256:  launchContext.GetArtifactSha256(),
		BrowserRevision: launchContext.GetBrowserRevision(), BrowserArtifactSHA256: launchContext.GetBrowserArtifactSha256(),
		PlaywrightVersion: launchContext.GetPlaywrightVersion(), PlaywrightArtifactSHA256: launchContext.GetPlaywrightArtifactSha256(),
		LauncherNonce: request.GetNonce(),
		TokenHash:     tokenHash, ExpiresAt: now.Add(DefaultLaunchTicketTTL), CreatedAt: now,
	}
	if err := service.repository.CreateLaunchTicket(ctx, ticket); err != nil {
		return nil, err
	}
	response := &clientpb.LaunchTicketResponse{}
	response.SetLaunchTicket(token)
	response.SetExpiresAt(timestamppb.New(ticket.ExpiresAt))
	return response, nil
}

func normalizeLaunchTicketRequest(request *clientpb.LaunchTicketRequest) *clientpb.LaunchTicketRequest {
	if request == nil {
		return nil
	}
	request = proto.CloneOf(request)
	request.SetNonce(strings.TrimSpace(request.GetNonce()))
	context := request.GetContext()
	if context == nil {
		return request
	}
	context.SetInstallationId(strings.TrimSpace(context.GetInstallationId()))
	context.SetDeviceId(strings.TrimSpace(context.GetDeviceId()))
	context.SetClientVersion(strings.TrimSpace(context.GetClientVersion()))
	context.SetArtifactSha256(strings.ToLower(strings.TrimSpace(context.GetArtifactSha256())))
	context.SetBrowserRevision(strings.TrimSpace(context.GetBrowserRevision()))
	context.SetBrowserArtifactSha256(strings.ToLower(strings.TrimSpace(context.GetBrowserArtifactSha256())))
	context.SetPlaywrightVersion(strings.TrimSpace(context.GetPlaywrightVersion()))
	context.SetPlaywrightArtifactSha256(strings.ToLower(strings.TrimSpace(context.GetPlaywrightArtifactSha256())))
	return request
}

func launchTicketRequestValid(request *clientpb.LaunchTicketRequest) bool {
	context := request.GetContext()
	return context != nil && context.GetInstallationId() != "" && context.GetDeviceId() != "" &&
		context.GetReleaseGeneration() > 0 && context.GetClientVersion() != "" &&
		validSHA256(context.GetArtifactSha256()) && context.GetBrowserRevision() != "" &&
		validSHA256(context.GetBrowserArtifactSha256()) && context.GetPlaywrightVersion() != "" &&
		validSHA256(context.GetPlaywrightArtifactSha256()) && len(request.GetNonce()) >= 16
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	// DecodeString always yields 32 bytes after the exact 64-character check.
	_, err := hex.DecodeString(value)
	return err == nil
}

func (service *ClientService) ExchangeLaunchTicket(
	ctx context.Context,
	request *clientpb.SessionExchangeRequest,
) (*clientpb.AuthenticationResponse, error) {
	launchTicket := strings.TrimSpace(request.GetLaunchTicket())
	clientNonce := strings.TrimSpace(request.GetClientNonce())
	if launchTicket == "" || len(clientNonce) < 16 {
		return nil, ErrUnauthorized
	}
	now := service.clock().UTC()
	response, session, err := service.issueSession(&clientpb.User{}, now)
	if err != nil {
		return nil, err
	}
	generation, err := service.CurrentReleaseGeneration(ctx)
	if err != nil {
		return nil, err
	}
	launched, err := service.repository.ExchangeLaunchTicket(
		ctx, sha256.Sum256([]byte(launchTicket)), clientNonce,
		generation, session, now,
	)
	if err != nil {
		if errors.Is(err, ErrStaleRelease) {
			return nil, ErrStaleRelease
		}
		return nil, ErrUnauthorized
	}
	response.SetUser(launched.User)
	response.SetLaunch(launched.Context)
	return response, nil
}

func (service *ClientService) AuthorizeProbeBootstrap(
	ctx context.Context,
	principal ClientPrincipal,
	request *clientpb.ProbeBootstrapTicketRequest,
) (*clientpb.Device, error) {
	installationID := strings.TrimSpace(request.GetInstallationId())
	deviceID := strings.TrimSpace(request.GetDeviceId())
	if installationID == "" || deviceID == "" || request.GetMaxConcurrency() != 1 ||
		!validClientProbeCapabilities(request.GetCapabilities()) {
		return nil, ErrInvalid
	}
	device, err := service.repository.GetClientDevice(ctx, principal.UserID, installationID)
	if err != nil || device.GetDeviceId() != deviceID {
		return nil, ErrUnauthorized
	}
	release, _, err := service.CurrentRuntimeReleaseSnapshot(ctx, "stable", device.GetPlatform(), device.GetArchitecture())
	if err != nil || !runtimeMatchesRelease(request.GetRuntime(), release) {
		return nil, ErrUnauthorized
	}
	return device, nil
}

func validClientProbeCapabilities(values []*observationpb.Capability) bool {
	if len(values) < 1 || len(values) > 4 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		capability := capabilityName(value)
		if !probedomain.IsSupportedCapability(capability) {
			return false
		}
		seen[capability] = struct{}{}
	}
	_, schedule := seen[probedomain.CapabilityCGVScheduleCapture]
	return schedule && len(seen) == len(values)
}

func runtimeMatchesRelease(runtime *commonpb.Runtime, release *releasepb.RuntimeRelease) bool {
	return runtime != nil && release != nil &&
		runtime.GetComponentVersion() == release.GetClient().GetVersion() &&
		runtime.GetBrowserRevision() == release.GetBrowser().GetRevision() &&
		runtime.GetPlatform() == release.GetClient().GetPlatform() &&
		runtime.GetArchitecture() == release.GetClient().GetArchitecture()
}

func capabilityName(capability *observationpb.Capability) string {
	switch {
	case capability.GetScheduleCapture() != nil:
		return probedomain.CapabilityCGVScheduleCapture
	case capability.GetCatalogCapture() != nil:
		return probedomain.CapabilityCGVCatalogCapture
	case capability.GetSeatMapCapture() != nil:
		return probedomain.CapabilityCGVSeatMapCapture
	case capability.GetSeatAvailabilityCapture() != nil:
		return probedomain.CapabilityCGVSeatAvailabilityCapture
	default:
		return ""
	}
}

func validateClientRelease(release *releasepb.ClientRelease) error {
	if err := validateClientReleaseIdentity(release); err != nil {
		return err
	}
	if err := validateClientReleaseVersions(release); err != nil {
		return err
	}
	if err := validateReleaseArtifact(release.GetArtifact()); err != nil {
		return fmt.Errorf("client artifact: %w", err)
	}
	return validateProbeBootstrapKeyring(release.GetProbeBootstrapPublicKeys())
}

func validateClientReleaseIdentity(release *releasepb.ClientRelease) error {
	if release.GetChannel() != "stable" ||
		!releasepolicy.IsSupportedDesktopTarget(release.GetPlatform(), release.GetArchitecture()) ||
		release.GetVersion() == "" || release.GetMinimumLauncherVersion() == "" ||
		release.GetMinimumBrowserRevision() == "" || release.GetPlaywrightVersion() == "" ||
		!validPublishedAt(release.GetPublishedAt()) {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	return nil
}

func validateClientReleaseVersions(release *releasepb.ClientRelease) error {
	if !semver.IsValid(releasepolicy.CanonicalVersion(release.GetVersion())) ||
		!semver.IsValid(releasepolicy.CanonicalVersion(release.GetMinimumLauncherVersion())) ||
		!semver.IsValid(releasepolicy.CanonicalVersion(release.GetPlaywrightVersion())) {
		return errors.New("client, minimum Launcher, and Playwright versions must be semantic versions")
	}
	if !releasepolicy.IsNumericRevision(release.GetMinimumBrowserRevision()) {
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

func validateBrowserRelease(release *releasepb.BrowserRelease) error {
	if release.GetChannel() != "stable" ||
		!releasepolicy.IsSupportedDesktopTarget(release.GetPlatform(), release.GetArchitecture()) ||
		!releasepolicy.IsNumericRevision(release.GetRevision()) || !validPublishedAt(release.GetPublishedAt()) ||
		len(release.GetCompatiblePlaywrightVersions()) == 0 {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	seen := make(map[string]struct{}, len(release.GetCompatiblePlaywrightVersions()))
	for _, version := range release.GetCompatiblePlaywrightVersions() {
		if !semver.IsValid(releasepolicy.CanonicalVersion(version)) {
			return errors.New("compatible Playwright versions must not be empty")
		}
		if _, duplicate := seen[version]; duplicate {
			return errors.New("compatible Playwright versions must be unique")
		}
		seen[version] = struct{}{}
	}
	if err := validateReleaseArtifact(release.GetArtifact()); err != nil {
		return fmt.Errorf("browser artifact: %w", err)
	}
	return nil
}

func validatePlaywrightRelease(release *releasepb.PlaywrightRelease) error {
	return validateDesktopComponentRelease(
		"playwright", release.GetChannel(), release.GetPlatform(), release.GetArchitecture(),
		release.GetVersion(), release.GetPublishedAt(), release.GetArtifact(),
	)
}

func validateLauncherRelease(release *releasepb.LauncherRelease) error {
	return validateDesktopComponentRelease(
		"launcher", release.GetChannel(), release.GetPlatform(), release.GetArchitecture(),
		release.GetVersion(), release.GetPublishedAt(), release.GetLauncher(),
	)
}

func validateDesktopComponentRelease(
	component, channel, platform, architecture, version string,
	publishedAt *timestamppb.Timestamp,
	artifact *releasepb.Artifact,
) error {
	if channel != "stable" || !releasepolicy.IsSupportedDesktopTarget(platform, architecture) ||
		version == "" || !validPublishedAt(publishedAt) {
		return errors.New("release identity and publishedAt are required")
	}
	if !semver.IsValid(releasepolicy.CanonicalVersion(version)) {
		return fmt.Errorf("%s version must be a semantic version", component)
	}
	if err := validateReleaseArtifact(artifact); err != nil {
		return fmt.Errorf("%s artifact: %w", component, err)
	}
	return nil
}

func validateProbeRelease(release *releasepb.ProbeRelease) error {
	if release.GetChannel() != "stable" || !semver.IsValid(releasepolicy.CanonicalVersion(release.GetVersion())) ||
		!releasepolicy.IsNumericRevision(release.GetBrowserRevision()) ||
		!validPublishedAt(release.GetPublishedAt()) {
		return errors.New("release identity, compatibility, and publishedAt are required")
	}
	image := release.GetImage()
	lastSlash := strings.LastIndex(image, "/")
	if image == "" || strings.ContainsAny(image, " \t\r\n@") ||
		strings.Contains(image, "://") || lastSlash <= 0 ||
		strings.Contains(image[lastSlash+1:], ":") {
		return errors.New("Probe image must be an untagged OCI registry repository")
	}
	digest := strings.TrimPrefix(release.GetImageDigest(), "sha256:")
	decoded, err := hex.DecodeString(digest)
	if !strings.HasPrefix(release.GetImageDigest(), "sha256:") || err != nil || len(decoded) != sha256.Size {
		return errors.New("Probe imageDigest must be a sha256 OCI digest")
	}
	return nil
}

func validateReleaseArtifact(artifact *releasepb.Artifact) error {
	if artifact == nil {
		return errors.New("artifact is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(artifact.GetUrl()))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || artifact.GetSize() <= 0 {
		return errors.New("HTTPS URL without credentials/fragment and positive size are required")
	}
	digest, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(artifact.GetSha256())))
	if err != nil || len(digest) != sha256.Size {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	executable := strings.TrimSpace(artifact.GetExecutable())
	if executable == "" || path.IsAbs(executable) || path.Clean(executable) != executable ||
		strings.HasPrefix(executable, "../") {
		return errors.New("executable must be a clean relative archive path")
	}
	return nil
}

func validPublishedAt(value *timestamppb.Timestamp) bool {
	return value != nil && value.IsValid() && !value.AsTime().IsZero()
}

func normalizeClientRelease(release **releasepb.ClientRelease) {
	*release = cloneClientRelease(*release)
	(*release).SetChannel(strings.TrimSpace((*release).GetChannel()))
	(*release).SetPlatform(strings.TrimSpace((*release).GetPlatform()))
	(*release).SetArchitecture(strings.TrimSpace((*release).GetArchitecture()))
	(*release).SetVersion(strings.TrimSpace((*release).GetVersion()))
	(*release).SetMinimumLauncherVersion(strings.TrimSpace((*release).GetMinimumLauncherVersion()))
	(*release).SetMinimumBrowserRevision(strings.TrimSpace((*release).GetMinimumBrowserRevision()))
	(*release).SetPlaywrightVersion(strings.TrimSpace((*release).GetPlaywrightVersion()))
}

func clientReleaseIdentity(release *releasepb.ClientRelease) string {
	return releasepolicy.ComponentKey(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetVersion(),
	)
}

func normalizeBrowserRelease(release **releasepb.BrowserRelease) {
	*release = cloneBrowserRelease(*release)
	(*release).SetChannel(strings.TrimSpace((*release).GetChannel()))
	(*release).SetPlatform(strings.TrimSpace((*release).GetPlatform()))
	(*release).SetArchitecture(strings.TrimSpace((*release).GetArchitecture()))
	(*release).SetRevision(strings.TrimSpace((*release).GetRevision()))
	versions := (*release).GetCompatiblePlaywrightVersions()
	for index := range versions {
		versions[index] = strings.TrimSpace(versions[index])
	}
	(*release).SetCompatiblePlaywrightVersions(versions)
}

func browserReleaseIdentity(release *releasepb.BrowserRelease) string {
	return releasepolicy.ComponentKey(
		release.GetChannel(), release.GetPlatform(), release.GetArchitecture(), release.GetRevision(),
	)
}

func cloneClientRelease(value *releasepb.ClientRelease) *releasepb.ClientRelease {
	if value == nil {
		return &releasepb.ClientRelease{}
	}
	return proto.CloneOf(value)
}

func cloneBrowserRelease(value *releasepb.BrowserRelease) *releasepb.BrowserRelease {
	if value == nil {
		return &releasepb.BrowserRelease{}
	}
	return proto.CloneOf(value)
}

func clonePlaywrightRelease(value *releasepb.PlaywrightRelease) *releasepb.PlaywrightRelease {
	if value == nil {
		return &releasepb.PlaywrightRelease{}
	}
	return proto.CloneOf(value)
}

func cloneLauncherRelease(value *releasepb.LauncherRelease) *releasepb.LauncherRelease {
	if value == nil {
		return &releasepb.LauncherRelease{}
	}
	return proto.CloneOf(value)
}

func cloneProbeRelease(value *releasepb.ProbeRelease) *releasepb.ProbeRelease {
	if value == nil {
		return &releasepb.ProbeRelease{}
	}
	return proto.CloneOf(value)
}
