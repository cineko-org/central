package releases

import (
	"sort"
	"strings"

	contracts "github.com/cineko-org/contracts/v3"

	"golang.org/x/mod/semver"
)

// CurrentRuntime selects the newest client stack compatible with the active
// launcher, Playwright, and browser releases for one desktop target.
func CurrentRuntime(
	catalog Catalog,
	channel string,
	platform string,
	arch string,
) (contracts.RuntimeRelease, bool) {
	prefix := ReleaseKey(channel, platform, arch) + "/"
	candidates := make([]contracts.ClientRelease, 0, len(catalog.Clients))
	for key, client := range catalog.Clients {
		if strings.HasPrefix(key, prefix) {
			candidates = append(candidates, client)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return semver.Compare(CanonicalVersion(candidates[left].Version), CanonicalVersion(candidates[right].Version)) > 0
	})
	launcher, launcherExists := CurrentLauncher(catalog, channel, platform, arch)
	if !launcherExists {
		return contracts.RuntimeRelease{}, false
	}
	for _, client := range candidates {
		if semver.Compare(CanonicalVersion(launcher.Version), CanonicalVersion(client.MinimumLauncherVersion)) < 0 {
			continue
		}
		playwright, playwrightExists := catalog.Playwright[ComponentKey(
			channel, platform, arch, client.PlaywrightVersion,
		)]
		if !playwrightExists {
			continue
		}
		browser, browserExists := CompatibleBrowser(
			catalog, channel, platform, arch, client.MinimumBrowserRevision, playwright.Version,
		)
		if browserExists {
			return contracts.RuntimeRelease{Client: client, Browser: browser, Playwright: playwright}, true
		}
	}
	return contracts.RuntimeRelease{}, false
}

// CompatibleBrowser selects the newest browser revision satisfying both the
// client minimum revision and the Playwright compatibility list.
func CompatibleBrowser(
	catalog Catalog,
	channel string,
	platform string,
	arch string,
	minimumRevision string,
	playwrightVersion string,
) (contracts.BrowserRelease, bool) {
	if !IsNumericRevision(minimumRevision) {
		return contracts.BrowserRelease{}, false
	}
	prefix := ReleaseKey(channel, platform, arch) + "/"
	var selected contracts.BrowserRelease
	for key, release := range catalog.Browsers {
		if !strings.HasPrefix(key, prefix) || !IsNumericRevision(release.Revision) ||
			CompareNumericRevision(release.Revision, minimumRevision) < 0 ||
			!contains(release.CompatiblePlaywrightVersions, playwrightVersion) {
			continue
		}
		if selected.Revision == "" || CompareNumericRevision(release.Revision, selected.Revision) > 0 {
			selected = release
		}
	}
	return selected, selected.Revision != ""
}

// CurrentLauncher selects the newest launcher release for one desktop target.
func CurrentLauncher(
	catalog Catalog,
	channel string,
	platform string,
	arch string,
) (contracts.LauncherRelease, bool) {
	prefix := ReleaseKey(channel, platform, arch) + "/"
	var selected contracts.LauncherRelease
	for key, candidate := range catalog.Launchers {
		if strings.HasPrefix(key, prefix) && (selected.Version == "" ||
			semver.Compare(CanonicalVersion(candidate.Version), CanonicalVersion(selected.Version)) > 0) {
			selected = candidate
		}
	}
	return selected, selected.Version != ""
}

// CurrentProbe selects the newest Probe release for one channel.
func CurrentProbe(catalog Catalog, channel string) (contracts.ProbeRelease, bool) {
	prefix := strings.TrimSpace(channel) + "/"
	var selected contracts.ProbeRelease
	for key, release := range catalog.Probes {
		if strings.HasPrefix(key, prefix) && (selected.Version == "" ||
			semver.Compare(CanonicalVersion(release.Version), CanonicalVersion(selected.Version)) > 0) {
			selected = release
		}
	}
	return selected, selected.Version != ""
}

// RuntimeMatchesLaunchRequest verifies that every launch artifact matches the
// runtime selected from the current release catalog.
func RuntimeMatchesLaunchRequest(
	runtime contracts.RuntimeRelease,
	request contracts.LaunchTicketRequest,
) bool {
	return runtime.Client.Version == request.ClientVersion &&
		runtime.Client.Artifact.SHA256 == request.ArtifactSHA256 &&
		runtime.Client.Protocol == request.Protocol &&
		runtime.Browser.Revision == request.BrowserRevision &&
		runtime.Browser.Artifact.SHA256 == request.BrowserArtifactSHA256 &&
		runtime.Playwright.Version == request.PlaywrightVersion &&
		runtime.Playwright.Artifact.SHA256 == request.PlaywrightArtifactSHA256
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
