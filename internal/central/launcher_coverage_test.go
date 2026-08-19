package central

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientReleaseConfiguration(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.ConfigureReleases(nil); err == nil {
		t.Fatal("empty client releases accepted")
	}
	invalid := validClientRelease()
	invalid.Channel = "beta"
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("invalid client release accepted")
	}
	invalid = validClientRelease()
	invalid.Artifact.URL = "http://download.example/client.zip"
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("insecure client artifact accepted")
	}
	invalid = validClientRelease()
	invalid.Artifact.SHA256 = "bad"
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("invalid artifact digest accepted")
	}
	invalid = validClientRelease()
	invalid.Artifact.Executable = "../client"
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("escaping artifact executable accepted")
	}
	invalid = validClientRelease()
	invalid.ProbeBootstrapPublicKeys = nil
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("missing Probe public keyring accepted")
	}
	invalid = validClientRelease()
	invalid.ProbeBootstrapPublicKeys = map[string]string{"": "not a public key"}
	if err := service.ConfigureReleases([]ClientRelease{invalid}); err == nil {
		t.Fatal("invalid Probe public keyring accepted")
	}
	release := validClientRelease()
	if err := service.ConfigureReleases([]ClientRelease{release, release}); err == nil {
		t.Fatal("duplicate client release accepted")
	}
	if err := service.ConfigureReleases([]ClientRelease{release}); err != nil {
		t.Fatal(err)
	}
	if !supportedPlatform("windows", "amd64") || !supportedPlatform("linux", "amd64") ||
		supportedPlatform("linux", "arm64") {
		t.Fatal("supported platform matrix is incorrect")
	}
}

func TestValidSHA256RejectsNonHexDigest(t *testing.T) {
	t.Parallel()
	for _, digest := range []string{strings.Repeat("g", 64), "short", strings.Repeat("A", 64)} {
		if validSHA256(digest) {
			t.Fatalf("invalid SHA-256 %q accepted", digest)
		}
	}
}

func TestBrowserAndPlaywrightReleaseConfiguration(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.ConfigureBrowserReleases(nil); err == nil {
		t.Fatal("empty browser releases accepted")
	}
	browser := validBrowserRelease()
	invalidBrowser := browser
	invalidBrowser.CompatiblePlaywrightVersions = []string{"1.61.1", "1.61.1"}
	if err := service.ConfigureBrowserReleases([]BrowserRelease{invalidBrowser}); err == nil {
		t.Fatal("duplicate browser compatibility accepted")
	}
	if err := service.ConfigureBrowserReleases([]BrowserRelease{browser, browser}); err == nil {
		t.Fatal("duplicate browser release accepted")
	}
	newerBrowser := browser
	newerBrowser.Revision = "1235"
	newerBrowser.Artifact.SHA256 = strings.Repeat("4", 64)
	if err := service.ConfigureBrowserReleases([]BrowserRelease{browser, newerBrowser}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigurePlaywrightReleases(nil); err == nil {
		t.Fatal("empty Playwright releases accepted")
	}
	playwright := validPlaywrightRelease()
	invalidPlaywright := playwright
	invalidPlaywright.Artifact.SHA256 = "bad"
	if err := service.ConfigurePlaywrightReleases([]PlaywrightRelease{invalidPlaywright}); err == nil {
		t.Fatal("invalid Playwright artifact accepted")
	}
	if err := service.ConfigurePlaywrightReleases([]PlaywrightRelease{playwright, playwright}); err == nil {
		t.Fatal("duplicate Playwright release accepted")
	}
	if err := service.ConfigurePlaywrightReleases([]PlaywrightRelease{playwright}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime without client = %v", err)
	}
	if err := service.ConfigureReleases([]ClientRelease{validClientRelease()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureLauncherReleases([]LauncherRelease{validLauncherRelease()}); err != nil {
		t.Fatal(err)
	}
	runtimeRelease, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err != nil || runtimeRelease.Browser.Revision != newerBrowser.Revision ||
		runtimeRelease.Playwright.Version != playwright.Version {
		t.Fatalf("CurrentRuntimeRelease() = %+v, %v", runtimeRelease, err)
	}
	newestIncompatible := validClientRelease()
	newestIncompatible.Version = "2.0.0"
	newestIncompatible.PlaywrightVersion = "2.0.0"
	newestIncompatible.Artifact.SHA256 = strings.Repeat("5", 64)
	if err := service.ConfigureReleases([]ClientRelease{validClientRelease(), newestIncompatible}); err != nil {
		t.Fatal(err)
	}
	runtimeRelease, err = service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err != nil || runtimeRelease.Client.Version != "1.0.0" {
		t.Fatalf("compatible runtime fallback = %+v, %v", runtimeRelease, err)
	}
	incompatibleBrowser := validBrowserRelease()
	incompatibleBrowser.Revision = "1236"
	incompatibleBrowser.CompatiblePlaywrightVersions = []string{"2.0.0"}
	incompatibleBrowser.Artifact.SHA256 = strings.Repeat("a", 64)
	if err := service.ConfigureBrowserReleases([]BrowserRelease{validBrowserRelease(), incompatibleBrowser}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.compatibleBrowserRelease("stable", "darwin", "arm64", "1234", "9.9.9"); ok {
		t.Fatal("incompatible browser selected")
	}
}

func TestLauncherAndProbeReleaseConfiguration(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.ConfigureLauncherReleases(nil); err == nil {
		t.Fatal("empty Launcher releases accepted")
	}
	launcher := validLauncherRelease()
	invalidLauncher := launcher
	invalidLauncher.Version = "invalid"
	if err := service.ConfigureLauncherReleases([]LauncherRelease{invalidLauncher}); err == nil {
		t.Fatal("invalid Launcher release accepted")
	}
	if err := service.ConfigureLauncherReleases([]LauncherRelease{launcher, launcher}); err == nil {
		t.Fatal("duplicate Launcher release accepted")
	}
	newerLauncher := launcher
	newerLauncher.Version = "2.0.0"
	newerLauncher.Launcher.SHA256 = strings.Repeat("6", 64)
	if err := service.ConfigureLauncherReleases([]LauncherRelease{launcher, newerLauncher}); err != nil {
		t.Fatal(err)
	}
	selectedLauncher, err := service.CurrentLauncherRelease(" stable ", "darwin", "arm64")
	if err != nil || selectedLauncher.Version != "2.0.0" {
		t.Fatalf("CurrentLauncherRelease() = %+v, %v", selectedLauncher, err)
	}
	if _, err := service.CurrentLauncherRelease("stable", "linux", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Launcher release = %v", err)
	}

	if err := service.ConfigureProbeReleases(nil); err == nil {
		t.Fatal("empty Probe releases accepted")
	}
	probe := validProbeRelease()
	invalidProbe := probe
	invalidProbe.ImageDigest = "bad"
	if err := service.ConfigureProbeReleases([]ProbeRelease{invalidProbe}); err == nil {
		t.Fatal("invalid Probe release accepted")
	}
	if err := service.ConfigureProbeReleases([]ProbeRelease{probe, probe}); err == nil {
		t.Fatal("duplicate Probe release accepted")
	}
	newerProbe := probe
	newerProbe.Version = "1.1.0"
	newerProbe.ImageDigest = "sha256:" + strings.Repeat("7", 64)
	if err := service.ConfigureProbeReleases([]ProbeRelease{probe, newerProbe}); err != nil {
		t.Fatal(err)
	}
	selectedProbe, err := service.CurrentProbeRelease(" stable ")
	if err != nil || selectedProbe.Version != "1.1.0" {
		t.Fatalf("CurrentProbeRelease() = %+v, %v", selectedProbe, err)
	}
	if _, err := service.CurrentProbeRelease("beta"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Probe release = %v", err)
	}
}

func TestClientLaunchTicketLifecycle(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	release := validClientRelease()
	if err := configureValidRuntime(service, release); err != nil {
		t.Fatal(err)
	}
	repository.device = ClientDevice{
		InstallationID: "install", UserID: "user", DeviceID: "device", Platform: "darwin", Arch: "arm64",
	}
	principal := ClientPrincipal{UserID: "user", SessionID: "session"}
	request := validLaunchTicketRequest(release)
	if _, err := service.IssueLaunchTicket(context.Background(), principal, LaunchTicketRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("IssueLaunchTicket(invalid) = %v", err)
	}
	repository.fail = "device"
	if _, err := service.IssueLaunchTicket(context.Background(), principal, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("IssueLaunchTicket(device error) = %v", err)
	}
	repository.fail = ""
	repository.device.DeviceID = "other"
	if _, err := service.IssueLaunchTicket(context.Background(), principal, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("IssueLaunchTicket(device mismatch) = %v", err)
	}
	repository.device.DeviceID = "device"
	mismatch := request
	mismatch.BrowserArtifactSHA256 = strings.Repeat("f", 64)
	if _, err := service.IssueLaunchTicket(context.Background(), principal, mismatch); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("IssueLaunchTicket(release mismatch) = %v", err)
	}
	stale := request
	stale.ReleaseGeneration++
	if _, err := service.IssueLaunchTicket(context.Background(), principal, stale); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("IssueLaunchTicket(stale generation) = %v", err)
	}
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.IssueLaunchTicket(context.Background(), principal, request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("IssueLaunchTicket(token error) = %v", err)
	}
	calls := 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, errInjectedClient
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := service.IssueLaunchTicket(context.Background(), principal, request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("IssueLaunchTicket(id error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "create-launch-ticket"
	if _, err := service.IssueLaunchTicket(context.Background(), principal, request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("IssueLaunchTicket(repository error) = %v", err)
	}
	repository.fail = ""
	issued, err := service.IssueLaunchTicket(context.Background(), principal, request)
	if err != nil || issued.LaunchTicket == "" || !issued.ExpiresAt.Equal(clientTestTime.Add(time.Minute)) ||
		repository.ticket.UserID != "user" {
		t.Fatalf("IssueLaunchTicket() = %+v, %+v, %v", issued, repository.ticket, err)
	}

	if _, err := service.ExchangeLaunchTicket(context.Background(), ClientSessionExchangeRequest{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ExchangeLaunchTicket(invalid) = %v", err)
	}
	service.random = func([]byte) (int, error) { return 0, errInjectedClient }
	if _, err := service.ExchangeLaunchTicket(context.Background(), ClientSessionExchangeRequest{
		LaunchTicket: issued.LaunchTicket, ClientNonce: strings.Repeat("n", 16),
	}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("ExchangeLaunchTicket(token error) = %v", err)
	}
	service.random = deterministicClientRandom
	repository.fail = "exchange-launch-ticket"
	if _, err := service.ExchangeLaunchTicket(context.Background(), ClientSessionExchangeRequest{
		LaunchTicket: issued.LaunchTicket, ClientNonce: strings.Repeat("n", 16),
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ExchangeLaunchTicket(repository error) = %v", err)
	}
	repository.fail = "exchange-stale-release"
	if _, err := service.ExchangeLaunchTicket(context.Background(), ClientSessionExchangeRequest{
		LaunchTicket: issued.LaunchTicket, ClientNonce: strings.Repeat("n", 16),
	}); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("ExchangeLaunchTicket(stale release) = %v", err)
	}
	repository.fail = ""
	auth, err := service.ExchangeLaunchTicket(context.Background(), ClientSessionExchangeRequest{
		LaunchTicket: " " + issued.LaunchTicket + " ", ClientNonce: strings.Repeat("n", 16),
	})
	if err != nil || auth.User.ID != "user" || auth.AccessToken == "" || auth.RefreshToken == "" {
		t.Fatalf("ExchangeLaunchTicket() = %+v, %v", auth, err)
	}
}

func TestClientProbeBootstrapAuthorization(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	release := validClientRelease()
	if err := configureValidRuntime(service, release); err != nil {
		t.Fatal(err)
	}
	repository.device = ClientDevice{
		InstallationID: "install", UserID: "user", DeviceID: "device", Platform: "darwin", Arch: "arm64",
	}
	principal := ClientPrincipal{UserID: "user"}
	request := ProbeBootstrapTicketRequest{
		InstallationID: " install ", DeviceID: " device ",
		Capabilities: []string{"cgv.schedule.capture.v2"}, MaxConcurrency: 1,
		Runtime: Runtime{
			Version: release.Version, Protocol: release.Protocol, BrowserRevision: validBrowserRelease().Revision,
			Platform: release.Platform, Arch: release.Arch,
		},
	}
	if _, err := service.AuthorizeProbeBootstrap(context.Background(), principal, ProbeBootstrapTicketRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("AuthorizeProbeBootstrap(invalid) = %v", err)
	}
	repository.fail = "device"
	if _, err := service.AuthorizeProbeBootstrap(context.Background(), principal, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthorizeProbeBootstrap(device error) = %v", err)
	}
	repository.fail = ""
	repository.device.DeviceID = "other"
	if _, err := service.AuthorizeProbeBootstrap(context.Background(), principal, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthorizeProbeBootstrap(device mismatch) = %v", err)
	}
	repository.device.DeviceID = "device"
	mismatch := request
	mismatch.Runtime.Protocol++
	if _, err := service.AuthorizeProbeBootstrap(context.Background(), principal, mismatch); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthorizeProbeBootstrap(runtime mismatch) = %v", err)
	}
	device, err := service.AuthorizeProbeBootstrap(context.Background(), principal, request)
	if err != nil || device.DeviceID != "device" {
		t.Fatalf("AuthorizeProbeBootstrap() = %+v, %v", device, err)
	}
}

func TestClientProbeCapabilityValidation(t *testing.T) {
	valid := [][]string{
		{"cgv.schedule.capture.v2"},
		{"cgv.catalog.capture.v1", "cgv.schedule.capture.v2"},
		{"cgv.catalog.capture.v1", "cgv.schedule.capture.v2", "cgv.seat-map.capture.v1"},
	}
	for _, capabilities := range valid {
		if !validClientProbeCapabilities(capabilities) {
			t.Fatalf("valid capabilities rejected: %v", capabilities)
		}
	}
	invalid := [][]string{
		nil,
		{"cgv.catalog.capture.v1"},
		{"cgv.schedule.capture.v2", "cgv.schedule.capture.v2"},
		{"unknown", "cgv.schedule.capture.v2"},
		{"cgv.catalog.capture.v1", "cgv.schedule.capture.v2", "unknown"},
	}
	for _, capabilities := range invalid {
		if validClientProbeCapabilities(capabilities) {
			t.Fatalf("invalid capabilities accepted: %v", capabilities)
		}
	}
}

func validClientRelease() ClientRelease {
	return ClientRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0",
		MinimumLauncherVersion: "1.0.0", MinimumBrowserRevision: "1234",
		PlaywrightVersion: "1.61.1", Protocol: ProtocolVersion,
		Artifact: ReleaseArtifact{
			URL: "https://download.example/client.zip", Size: 1,
			SHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "Cineko.app/Contents/MacOS/Cineko",
		},
		ProbeBootstrapPublicKeys: map[string]string{
			"primary": "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----\n",
		},
		PublishedAt: clientTestTime,
	}
}

func validBrowserRelease() BrowserRelease {
	return BrowserRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Revision: "1234",
		CompatiblePlaywrightVersions: []string{"1.61.1"},
		Artifact: ReleaseArtifact{
			URL: "https://download.example/browser.zip", Size: 1,
			SHA256:     "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "chromium/Chromium",
		},
		PublishedAt: clientTestTime,
	}
}

func validPlaywrightRelease() PlaywrightRelease {
	return PlaywrightRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.61.1",
		Artifact: ReleaseArtifact{
			URL: "https://download.example/driver.zip", Size: 1,
			SHA256:     "2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Executable: "driver/playwright",
		},
		PublishedAt: clientTestTime,
	}
}

func validLauncherRelease() LauncherRelease {
	return LauncherRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0", Protocol: ProtocolVersion,
		Launcher: ReleaseArtifact{
			URL: "https://download.example/launcher.zip", Size: 1,
			SHA256: strings.Repeat("3", 64), Executable: "Cineko Launcher.app/Contents/MacOS/Cineko Launcher",
		},
		PublishedAt: clientTestTime,
	}
}

func validProbeRelease() ProbeRelease {
	return ProbeRelease{
		Channel: "stable", Version: "1.0.0", Protocol: ProtocolVersion, BrowserRevision: "1234",
		Image: "registry.example.com/example/cineko-probe", ImageDigest: "sha256:" + strings.Repeat("8", 64),
		PublishedAt: clientTestTime,
	}
}

func validClientReleaseSet() []ClientRelease {
	base := validClientRelease()
	return []ClientRelease{
		base,
		clientReleaseForTarget(base, "linux", "amd64", "linux-client"),
		clientReleaseForTarget(base, "windows", "amd64", "windows-client.exe"),
	}
}

func clientReleaseForTarget(base ClientRelease, platform, arch, executable string) ClientRelease {
	base.Platform, base.Arch = platform, arch
	base.Artifact.URL = "https://download.example/" + platform + "/client.zip"
	base.Artifact.Executable = executable
	return base
}

func validBrowserReleaseSet() []BrowserRelease {
	base := validBrowserRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://download.example/linux/browser.zip", "chromium/chrome"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://download.example/windows/browser.zip", "chromium/chrome.exe"
	return []BrowserRelease{base, linux, windows}
}

func validPlaywrightReleaseSet() []PlaywrightRelease {
	base := validPlaywrightRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://download.example/linux/playwright.zip", "playwright"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://download.example/windows/playwright.zip", "playwright.exe"
	return []PlaywrightRelease{base, linux, windows}
}

func validLauncherReleaseSet() []LauncherRelease {
	base := validLauncherRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Launcher.URL, linux.Launcher.Executable = "https://download.example/linux/launcher.zip", "cineko-launcher"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Launcher.URL, windows.Launcher.Executable = "https://download.example/windows/launcher.zip", "cineko-launcher.exe"
	return []LauncherRelease{base, linux, windows}
}

func configureValidRuntime(service *ClientService, client ClientRelease) error {
	if err := service.ConfigureReleases([]ClientRelease{client}); err != nil {
		return err
	}
	if err := service.ConfigureBrowserReleases([]BrowserRelease{validBrowserRelease()}); err != nil {
		return err
	}
	if err := service.ConfigurePlaywrightReleases([]PlaywrightRelease{validPlaywrightRelease()}); err != nil {
		return err
	}
	if err := service.ConfigureLauncherReleases([]LauncherRelease{validLauncherRelease()}); err != nil {
		return err
	}
	service.releaseGeneration.Store(1)
	return nil
}

func validLaunchTicketRequest(release ClientRelease) LaunchTicketRequest {
	return LaunchTicketRequest{
		InstallationID: " install ", DeviceID: " device ", ReleaseGeneration: 1,
		ClientVersion:  release.Version,
		ArtifactSHA256: strings.ToUpper(release.Artifact.SHA256), Protocol: release.Protocol,
		BrowserRevision:          validBrowserRelease().Revision,
		BrowserArtifactSHA256:    strings.ToUpper(validBrowserRelease().Artifact.SHA256),
		PlaywrightVersion:        validPlaywrightRelease().Version,
		PlaywrightArtifactSHA256: strings.ToUpper(validPlaywrightRelease().Artifact.SHA256),
		Nonce:                    strings.Repeat("l", 16),
	}
}

func TestReleaseValidationFailures(t *testing.T) {
	client := validClientRelease()
	client.MinimumBrowserRevision = "bad"
	if err := validateClientRelease(client); err == nil {
		t.Fatal("invalid minimum browser revision accepted")
	}

	browser := validBrowserRelease()
	browser.CompatiblePlaywrightVersions = []string{"bad"}
	if err := validateBrowserRelease(browser); err == nil {
		t.Fatal("invalid browser compatibility accepted")
	}
	browser = validBrowserRelease()
	browser.Artifact.SHA256 = "bad"
	if err := validateBrowserRelease(browser); err == nil {
		t.Fatal("invalid browser artifact accepted")
	}
	if containsString([]string{"one"}, "two") {
		t.Fatal("unexpected string match")
	}

	playwright := validPlaywrightRelease()
	playwright.Version = ""
	if err := validatePlaywrightRelease(playwright); err == nil {
		t.Fatal("empty Playwright version accepted")
	}
	playwright = validPlaywrightRelease()
	playwright.Artifact.SHA256 = "bad"
	if err := validatePlaywrightRelease(playwright); err == nil {
		t.Fatal("invalid Playwright artifact accepted")
	}

	launcher := validLauncherRelease()
	launcher.Version = ""
	if err := validateLauncherRelease(launcher); err == nil {
		t.Fatal("empty Launcher version accepted")
	}
	launcher = validLauncherRelease()
	launcher.Launcher.SHA256 = "bad"
	if err := validateLauncherRelease(launcher); err == nil {
		t.Fatal("invalid Launcher artifact accepted")
	}

	probe := validProbeRelease()
	probe.Image = "invalid"
	if err := validateProbeRelease(probe); err == nil {
		t.Fatal("invalid Probe image accepted")
	}
}
