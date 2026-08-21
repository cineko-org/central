package releases

import (
	"sort"
	"strings"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"golang.org/x/mod/semver"
)

func CurrentRuntime(catalog Catalog, channel, platform, arch string) (*releasepb.RuntimeRelease, bool) {
	prefix := ReleaseKey(channel, platform, arch) + "/"
	candidates := make([]*releasepb.ClientRelease, 0, len(catalog.Clients))
	for key, client := range catalog.Clients {
		if strings.HasPrefix(key, prefix) {
			candidates = append(candidates, client)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return semver.Compare(CanonicalVersion(candidates[left].GetVersion()), CanonicalVersion(candidates[right].GetVersion())) > 0
	})
	launcher, launcherExists := CurrentLauncher(catalog, channel, platform, arch)
	if !launcherExists {
		return nil, false
	}
	for _, client := range candidates {
		if semver.Compare(CanonicalVersion(launcher.GetVersion()), CanonicalVersion(client.GetMinimumLauncherVersion())) < 0 {
			continue
		}
		playwright, exists := catalog.Playwright[ComponentKey(channel, platform, arch, client.GetPlaywrightVersion())]
		if !exists {
			continue
		}
		browser, exists := CompatibleBrowser(catalog, channel, platform, arch, client.GetMinimumBrowserRevision(), playwright.GetVersion())
		if exists {
			runtime := &releasepb.RuntimeRelease{}
			runtime.SetClient(client)
			runtime.SetBrowser(browser)
			runtime.SetPlaywright(playwright)
			return runtime, true
		}
	}
	return nil, false
}

func CompatibleBrowser(catalog Catalog, channel, platform, arch, minimumRevision, playwrightVersion string) (*releasepb.BrowserRelease, bool) {
	if !IsNumericRevision(minimumRevision) {
		return nil, false
	}
	prefix := ReleaseKey(channel, platform, arch) + "/"
	var selected *releasepb.BrowserRelease
	for key, release := range catalog.Browsers {
		if !strings.HasPrefix(key, prefix) || !IsNumericRevision(release.GetRevision()) ||
			CompareNumericRevision(release.GetRevision(), minimumRevision) < 0 ||
			!contains(release.GetCompatiblePlaywrightVersions(), playwrightVersion) {
			continue
		}
		if selected == nil || CompareNumericRevision(release.GetRevision(), selected.GetRevision()) > 0 {
			selected = release
		}
	}
	return selected, selected != nil
}

func CurrentLauncher(catalog Catalog, channel, platform, arch string) (*releasepb.LauncherRelease, bool) {
	prefix := ReleaseKey(channel, platform, arch) + "/"
	var selected *releasepb.LauncherRelease
	for key, candidate := range catalog.Launchers {
		if strings.HasPrefix(key, prefix) && (selected == nil ||
			semver.Compare(CanonicalVersion(candidate.GetVersion()), CanonicalVersion(selected.GetVersion())) > 0) {
			selected = candidate
		}
	}
	return selected, selected != nil
}

func CurrentProbe(catalog Catalog, channel string) (*releasepb.ProbeRelease, bool) {
	prefix := strings.TrimSpace(channel) + "/"
	var selected *releasepb.ProbeRelease
	for key, release := range catalog.Probes {
		if strings.HasPrefix(key, prefix) && (selected == nil ||
			semver.Compare(CanonicalVersion(release.GetVersion()), CanonicalVersion(selected.GetVersion())) > 0) {
			selected = release
		}
	}
	return selected, selected != nil
}

func RuntimeMatchesLaunchRequest(runtime *releasepb.RuntimeRelease, request *clientpb.LaunchContext) bool {
	return runtime.GetClient().GetVersion() == request.GetClientVersion() &&
		runtime.GetClient().GetArtifact().GetSha256() == request.GetArtifactSha256() &&
		runtime.GetBrowser().GetRevision() == request.GetBrowserRevision() &&
		runtime.GetBrowser().GetArtifact().GetSha256() == request.GetBrowserArtifactSha256() &&
		runtime.GetPlaywright().GetVersion() == request.GetPlaywrightVersion() &&
		runtime.GetPlaywright().GetArtifact().GetSha256() == request.GetPlaywrightArtifactSha256()
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
