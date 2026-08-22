package releases

import (
	"slices"
	"strings"

	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
)

// Catalog is Central's validated in-memory index of generated release messages.
type Catalog struct {
	Clients    map[string]*releasepb.ClientRelease
	Browsers   map[string]*releasepb.BrowserRelease
	Playwright map[string]*releasepb.PlaywrightRelease
	Launchers  map[string]*releasepb.LauncherRelease
	Probes     map[string]*releasepb.ProbeRelease
}

// DesktopTarget identifies one supported desktop release target.
type DesktopTarget struct {
	Platform string
	Arch     string
}

var supportedDesktopTargets = [...]DesktopTarget{
	{Platform: "darwin", Arch: "arm64"},
	{Platform: "linux", Arch: "amd64"},
	{Platform: "windows", Arch: "amd64"},
}

func SupportedDesktopTargets() []DesktopTarget { return slices.Clone(supportedDesktopTargets[:]) }

func TargetKey(platform, arch string) string {
	return strings.TrimSpace(platform) + "/" + strings.TrimSpace(arch)
}

func ReleaseKey(channel, platform, arch string) string {
	return strings.TrimSpace(channel) + "/" + TargetKey(platform, arch)
}

func ComponentKey(channel, platform, arch, version string) string {
	return ReleaseKey(channel, platform, arch) + "/" + strings.TrimSpace(version)
}

func IsSupportedDesktopTarget(platform, arch string) bool {
	target := TargetKey(platform, arch)
	for _, supported := range supportedDesktopTargets {
		if TargetKey(supported.Platform, supported.Arch) == target {
			return true
		}
	}
	return false
}

func CompleteDesktopTargetSet(targets map[string]struct{}) bool {
	if len(targets) != len(supportedDesktopTargets) {
		return false
	}
	for _, supported := range supportedDesktopTargets {
		if _, exists := targets[TargetKey(supported.Platform, supported.Arch)]; !exists {
			return false
		}
	}
	return true
}
