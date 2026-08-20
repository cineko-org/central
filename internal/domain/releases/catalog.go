package releases

import (
	"slices"
	"strings"

	contracts "github.com/cineko-org/contracts/v3"
)

// Catalog is the validated in-memory release inventory used for resolution.
type Catalog struct {
	Clients    map[string]contracts.ClientRelease
	Browsers   map[string]contracts.BrowserRelease
	Playwright map[string]contracts.PlaywrightRelease
	Launchers  map[string]contracts.LauncherRelease
	Probes     map[string]contracts.ProbeRelease
}

// ActiveDesktopResolverVersion identifies the deterministic desktop selection
// policy persisted with the active desktop manifest fingerprint.
const ActiveDesktopResolverVersion = 1

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

// SupportedDesktopTargets returns the targets in the canonical manifest order.
func SupportedDesktopTargets() []DesktopTarget {
	return slices.Clone(supportedDesktopTargets[:])
}

// TargetKey returns the canonical platform/architecture key.
func TargetKey(platform, arch string) string {
	return strings.TrimSpace(platform) + "/" + strings.TrimSpace(arch)
}

// IsSupportedDesktopTarget reports whether a platform/architecture pair is
// part of the complete desktop release set.
func IsSupportedDesktopTarget(platform, arch string) bool {
	target := TargetKey(platform, arch)
	for _, supported := range supportedDesktopTargets {
		if TargetKey(supported.Platform, supported.Arch) == target {
			return true
		}
	}
	return false
}

// CompleteDesktopTargetSet reports whether targets contain exactly every
// supported desktop target and no unsupported target.
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

// ReleaseKey returns the channel and target portion of a release identity.
func ReleaseKey(channel, platform, arch string) string {
	return strings.TrimSpace(channel) + "/" + TargetKey(platform, arch)
}

// ComponentKey returns the complete versioned release identity.
func ComponentKey(channel, platform, arch, version string) string {
	return ReleaseKey(channel, platform, arch) + "/" + strings.TrimSpace(version)
}
