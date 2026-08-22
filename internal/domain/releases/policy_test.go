package releases

import (
	"testing"

	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
)

func TestSelectionRequiresCompatibleRuntimeComponents(t *testing.T) {
	catalog := Catalog{
		Clients: map[string]*releasepb.ClientRelease{
			ComponentKey("stable", "linux", "amd64", "1.0.0"): clientRelease("1.0.0", "1.0.0", "200", "1.1.0"),
			ComponentKey("stable", "linux", "amd64", "2.0.0"): clientRelease("2.0.0", "2.0.0", "300", "1.2.0"),
		},
		Launchers: map[string]*releasepb.LauncherRelease{
			ComponentKey("stable", "linux", "amd64", "1.5.0"): launcherRelease("1.5.0"),
		},
		Playwright: map[string]*releasepb.PlaywrightRelease{
			ComponentKey("stable", "linux", "amd64", "1.1.0"): playwrightRelease("1.1.0"),
		},
		Browsers: map[string]*releasepb.BrowserRelease{
			ComponentKey("stable", "linux", "amd64", "300"): browserRelease("300", "1.1.0"),
		},
	}

	selected, ok := CurrentRuntime(catalog, "stable", "linux", "amd64")
	if !ok || selected.GetClient().GetVersion() != "1.0.0" || selected.GetBrowser().GetRevision() != "300" {
		t.Fatalf("CurrentRuntime() = %+v, %t", selected, ok)
	}
	if _, ok := CurrentRuntime(catalog, "stable", "windows", "amd64"); ok {
		t.Fatal("CurrentRuntime() selected a missing target")
	}
}

func TestSelectionReturnsNewestComponent(t *testing.T) {
	catalog := Catalog{
		Launchers: map[string]*releasepb.LauncherRelease{
			ComponentKey("stable", "darwin", "arm64", "1.0.0"): launcherRelease("1.0.0"),
			ComponentKey("stable", "darwin", "arm64", "1.2.0"): launcherRelease("1.2.0"),
		},
		Probes: map[string]*releasepb.ProbeRelease{
			"stable/1.0.0": probeRelease("1.0.0"),
			"stable/1.3.0": probeRelease("1.3.0"),
		},
	}
	launcher, ok := CurrentLauncher(catalog, "stable", "darwin", "arm64")
	if !ok || launcher.GetVersion() != "1.2.0" {
		t.Fatalf("CurrentLauncher() = %+v, %t", launcher, ok)
	}
	probe, ok := CurrentProbe(catalog, "stable")
	if !ok || probe.GetVersion() != "1.3.0" {
		t.Fatalf("CurrentProbe() = %+v, %t", probe, ok)
	}
}

func TestReleaseTargetAndVersionPolicy(t *testing.T) {
	if !IsSupportedDesktopTarget(" linux ", "amd64") || IsSupportedDesktopTarget("linux", "arm64") {
		t.Fatal("desktop target policy is incorrect")
	}
	if !CompleteDesktopTargetSet(map[string]struct{}{
		"darwin/arm64": {}, "linux/amd64": {}, "windows/amd64": {},
	}) {
		t.Fatal("complete desktop target set was rejected")
	}
	if CompleteDesktopTargetSet(map[string]struct{}{
		"darwin/arm64": {}, "linux/amd64": {}, "unsupported/amd64": {},
	}) {
		t.Fatal("unsupported target set was accepted")
	}
	if CanonicalVersion(" v1.2.0 ") != "v1.2.0" || CompareNumericRevision("2001", "2000") <= 0 ||
		!IsNumericRevision("2001") {
		t.Fatal("release version policy is incorrect")
	}
	for _, value := range []string{"", " ", "-1", "+1", "1.2"} {
		if IsNumericRevision(value) {
			t.Fatalf("IsNumericRevision(%q) accepted a non-digit revision", value)
		}
	}
	if !IsNumericRevision(" 2001 ") {
		t.Fatal("IsNumericRevision() rejected normalized whitespace")
	}
}

func clientRelease(version, minimumLauncher, minimumBrowser, playwright string) *releasepb.ClientRelease {
	release := &releasepb.ClientRelease{}
	release.SetVersion(version)
	release.SetMinimumLauncherVersion(minimumLauncher)
	release.SetMinimumBrowserRevision(minimumBrowser)
	release.SetPlaywrightVersion(playwright)
	return release
}

func launcherRelease(version string) *releasepb.LauncherRelease {
	release := &releasepb.LauncherRelease{}
	release.SetVersion(version)
	return release
}

func playwrightRelease(version string) *releasepb.PlaywrightRelease {
	release := &releasepb.PlaywrightRelease{}
	release.SetVersion(version)
	return release
}

func browserRelease(revision string, playwrightVersions ...string) *releasepb.BrowserRelease {
	release := &releasepb.BrowserRelease{}
	release.SetRevision(revision)
	release.SetCompatiblePlaywrightVersions(playwrightVersions)
	return release
}

func probeRelease(version string) *releasepb.ProbeRelease {
	release := &releasepb.ProbeRelease{}
	release.SetVersion(version)
	return release
}
