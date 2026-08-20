package releases

import (
	"testing"

	contracts "github.com/cineko-org/contracts/v3"
)

func TestSelectionRequiresCompatibleRuntimeComponents(t *testing.T) {
	catalog := Catalog{
		Clients: map[string]contracts.ClientRelease{
			ComponentKey("stable", "linux", "amd64", "1.0.0"): {
				Version: "1.0.0", MinimumLauncherVersion: "1.0.0", MinimumBrowserRevision: "200",
				PlaywrightVersion: "1.1.0",
			},
			ComponentKey("stable", "linux", "amd64", "2.0.0"): {
				Version: "2.0.0", MinimumLauncherVersion: "2.0.0", MinimumBrowserRevision: "300",
				PlaywrightVersion: "1.2.0",
			},
		},
		Launchers: map[string]contracts.LauncherRelease{
			ComponentKey("stable", "linux", "amd64", "1.5.0"): {Version: "1.5.0"},
		},
		Playwright: map[string]contracts.PlaywrightRelease{
			ComponentKey("stable", "linux", "amd64", "1.1.0"): {Version: "1.1.0"},
		},
		Browsers: map[string]contracts.BrowserRelease{
			ComponentKey("stable", "linux", "amd64", "300"): {
				Revision: "300", CompatiblePlaywrightVersions: []string{"1.1.0"},
			},
		},
	}

	selected, ok := CurrentRuntime(catalog, "stable", "linux", "amd64")
	if !ok || selected.Client.Version != "1.0.0" || selected.Browser.Revision != "300" {
		t.Fatalf("CurrentRuntime() = %+v, %t", selected, ok)
	}
	if _, ok := CurrentRuntime(catalog, "stable", "windows", "amd64"); ok {
		t.Fatal("CurrentRuntime() selected a missing target")
	}
}

func TestSelectionReturnsNewestComponent(t *testing.T) {
	catalog := Catalog{
		Launchers: map[string]contracts.LauncherRelease{
			ComponentKey("stable", "darwin", "arm64", "1.0.0"): {Version: "1.0.0"},
			ComponentKey("stable", "darwin", "arm64", "1.2.0"): {Version: "1.2.0"},
		},
		Probes: map[string]contracts.ProbeRelease{
			"stable/1.0.0": {Version: "1.0.0"},
			"stable/1.3.0": {Version: "1.3.0"},
		},
	}
	launcher, ok := CurrentLauncher(catalog, "stable", "darwin", "arm64")
	if !ok || launcher.Version != "1.2.0" {
		t.Fatalf("CurrentLauncher() = %+v, %t", launcher, ok)
	}
	probe, ok := CurrentProbe(catalog, "stable")
	if !ok || probe.Version != "1.3.0" {
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
