package central

import (
	"strings"
	"testing"

	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReleaseConfigurationUsesGeneratedContracts(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if err := service.ConfigureReleases(validClientReleaseSet()); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureBrowserReleases(validBrowserReleaseSet()); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigurePlaywrightReleases(validPlaywrightReleaseSet()); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureLauncherReleases(validLauncherReleaseSet()); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureProbeReleases([]*releasepb.ProbeRelease{validProbeRelease()}); err != nil {
		t.Fatal(err)
	}

	runtime, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64")
	if err != nil || runtime.GetClient().GetVersion() != "1.0.0" || runtime.GetBrowser().GetRevision() != "1234" {
		t.Fatalf("CurrentRuntimeRelease() = %+v, %v", runtime, err)
	}
	launcher, err := service.CurrentLauncherRelease("stable", "darwin", "arm64")
	if err != nil || launcher.GetVersion() != "1.0.0" {
		t.Fatalf("CurrentLauncherRelease() = %+v, %v", launcher, err)
	}
	probe, err := service.CurrentProbeRelease("stable")
	if err != nil || probe.GetVersion() != "1.0.0" {
		t.Fatalf("CurrentProbeRelease() = %+v, %v", probe, err)
	}
}

func TestReleaseConfigurationRejectsInvalidGeneratedContracts(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	invalidClient := proto.CloneOf(validClientRelease())
	invalidClient.SetChannel("beta")
	if err := service.ConfigureReleases([]*releasepb.ClientRelease{invalidClient}); err == nil {
		t.Fatal("invalid Client release accepted")
	}
	if err := service.ConfigureReleases([]*releasepb.ClientRelease{validClientRelease(), validClientRelease()}); err == nil {
		t.Fatal("duplicate Client release accepted")
	}
	invalidBrowser := proto.CloneOf(validBrowserRelease())
	invalidBrowser.SetRevision("invalid")
	if err := service.ConfigureBrowserReleases([]*releasepb.BrowserRelease{invalidBrowser}); err == nil {
		t.Fatal("invalid Browser release accepted")
	}
	invalidPlaywright := proto.CloneOf(validPlaywrightRelease())
	invalidPlaywright.SetVersion("invalid")
	if err := service.ConfigurePlaywrightReleases([]*releasepb.PlaywrightRelease{invalidPlaywright}); err == nil {
		t.Fatal("invalid Playwright release accepted")
	}
	invalidLauncher := proto.CloneOf(validLauncherRelease())
	invalidLauncher.SetLauncher(nil)
	if err := service.ConfigureLauncherReleases([]*releasepb.LauncherRelease{invalidLauncher}); err == nil {
		t.Fatal("invalid Launcher release accepted")
	}
	invalidProbe := proto.CloneOf(validProbeRelease())
	invalidProbe.SetImage("https://registry.example.com/cineko-probe:latest")
	if err := service.ConfigureProbeReleases([]*releasepb.ProbeRelease{invalidProbe}); err == nil {
		t.Fatal("invalid Probe release accepted")
	}
}

func validClientRelease() *releasepb.ClientRelease {
	release := &releasepb.ClientRelease{}
	release.SetChannel("stable")
	release.SetPlatform("darwin")
	release.SetArchitecture("arm64")
	release.SetVersion("1.0.0")
	release.SetMinimumLauncherVersion("1.0.0")
	release.SetMinimumBrowserRevision("1234")
	release.SetPlaywrightVersion("1.61.1")
	release.SetArtifact(releaseArtifact("https://download.example/client.zip", "Cineko.app/Contents/MacOS/Cineko", "0"))
	release.SetProbeBootstrapPublicKeys(map[string]string{"primary": "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----\n"})
	release.SetPublishedAt(timestamppb.New(clientTestTime))
	return release
}

func validBrowserRelease() *releasepb.BrowserRelease {
	release := &releasepb.BrowserRelease{}
	release.SetChannel("stable")
	release.SetPlatform("darwin")
	release.SetArchitecture("arm64")
	release.SetRevision("1234")
	release.SetCompatiblePlaywrightVersions([]string{"1.61.1"})
	release.SetArtifact(releaseArtifact("https://download.example/browser.zip", "chromium/Chromium", "1"))
	release.SetPublishedAt(timestamppb.New(clientTestTime))
	return release
}

func validPlaywrightRelease() *releasepb.PlaywrightRelease {
	release := &releasepb.PlaywrightRelease{}
	release.SetChannel("stable")
	release.SetPlatform("darwin")
	release.SetArchitecture("arm64")
	release.SetVersion("1.61.1")
	release.SetArtifact(releaseArtifact("https://download.example/driver.zip", "driver/playwright", "2"))
	release.SetPublishedAt(timestamppb.New(clientTestTime))
	return release
}

func validLauncherRelease() *releasepb.LauncherRelease {
	release := &releasepb.LauncherRelease{}
	release.SetChannel("stable")
	release.SetPlatform("darwin")
	release.SetArchitecture("arm64")
	release.SetVersion("1.0.0")
	release.SetLauncher(releaseArtifact("https://download.example/launcher.zip", "Cineko Launcher.app/Contents/MacOS/Cineko Launcher", "3"))
	release.SetPublishedAt(timestamppb.New(clientTestTime))
	return release
}

func validProbeRelease() *releasepb.ProbeRelease {
	release := &releasepb.ProbeRelease{}
	release.SetChannel("stable")
	release.SetVersion("1.0.0")
	release.SetBrowserRevision("1234")
	release.SetImage("registry.example.com/example/cineko-probe")
	release.SetImageDigest("sha256:" + strings.Repeat("8", 64))
	release.SetPublishedAt(timestamppb.New(clientTestTime))
	return release
}

func releaseArtifact(url, executable, digit string) *releasepb.Artifact {
	artifact := &releasepb.Artifact{}
	artifact.SetUrl(url)
	artifact.SetSize(1)
	artifact.SetSha256(strings.Repeat(digit, 64))
	artifact.SetExecutable(executable)
	return artifact
}

func validClientReleaseSet() []*releasepb.ClientRelease {
	return []*releasepb.ClientRelease{
		validClientRelease(),
		clientReleaseForTarget(validClientRelease(), "linux", "amd64", "linux-client"),
		clientReleaseForTarget(validClientRelease(), "windows", "amd64", "windows-client.exe"),
	}
}

func clientReleaseForTarget(base *releasepb.ClientRelease, platform, arch, executable string) *releasepb.ClientRelease {
	release := proto.CloneOf(base)
	release.SetPlatform(platform)
	release.SetArchitecture(arch)
	release.GetArtifact().SetUrl("https://download.example/" + platform + "/client.zip")
	release.GetArtifact().SetExecutable(executable)
	return release
}

func validBrowserReleaseSet() []*releasepb.BrowserRelease {
	return []*releasepb.BrowserRelease{
		validBrowserRelease(),
		browserReleaseForTarget(validBrowserRelease(), "linux", "amd64", "chromium/chrome"),
		browserReleaseForTarget(validBrowserRelease(), "windows", "amd64", "chromium/chrome.exe"),
	}
}

func browserReleaseForTarget(base *releasepb.BrowserRelease, platform, arch, executable string) *releasepb.BrowserRelease {
	release := proto.CloneOf(base)
	release.SetPlatform(platform)
	release.SetArchitecture(arch)
	release.GetArtifact().SetUrl("https://download.example/" + platform + "/browser.zip")
	release.GetArtifact().SetExecutable(executable)
	return release
}

func validPlaywrightReleaseSet() []*releasepb.PlaywrightRelease {
	return []*releasepb.PlaywrightRelease{
		validPlaywrightRelease(),
		playwrightReleaseForTarget(validPlaywrightRelease(), "linux", "amd64", "playwright"),
		playwrightReleaseForTarget(validPlaywrightRelease(), "windows", "amd64", "playwright.exe"),
	}
}

func playwrightReleaseForTarget(base *releasepb.PlaywrightRelease, platform, arch, executable string) *releasepb.PlaywrightRelease {
	release := proto.CloneOf(base)
	release.SetPlatform(platform)
	release.SetArchitecture(arch)
	release.GetArtifact().SetUrl("https://download.example/" + platform + "/playwright.zip")
	release.GetArtifact().SetExecutable(executable)
	return release
}

func validLauncherReleaseSet() []*releasepb.LauncherRelease {
	return []*releasepb.LauncherRelease{
		validLauncherRelease(),
		launcherReleaseForTarget(validLauncherRelease(), "linux", "amd64", "launcher"),
		launcherReleaseForTarget(validLauncherRelease(), "windows", "amd64", "launcher.exe"),
	}
}

func launcherReleaseForTarget(base *releasepb.LauncherRelease, platform, arch, executable string) *releasepb.LauncherRelease {
	release := proto.CloneOf(base)
	release.SetPlatform(platform)
	release.SetArchitecture(arch)
	release.GetLauncher().SetUrl("https://download.example/" + platform + "/launcher.zip")
	release.GetLauncher().SetExecutable(executable)
	return release
}

func configureValidRuntime(service *ClientService, client *releasepb.ClientRelease) error {
	if err := service.ConfigureReleases([]*releasepb.ClientRelease{client}); err != nil {
		return err
	}
	if err := service.ConfigureBrowserReleases([]*releasepb.BrowserRelease{validBrowserRelease()}); err != nil {
		return err
	}
	if err := service.ConfigurePlaywrightReleases([]*releasepb.PlaywrightRelease{validPlaywrightRelease()}); err != nil {
		return err
	}
	return service.ConfigureLauncherReleases([]*releasepb.LauncherRelease{validLauncherRelease()})
}
