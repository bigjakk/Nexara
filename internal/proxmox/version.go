package proxmox

import (
	"regexp"
	"strconv"
	"strings"
)

// PVE capability thresholds — the minimum Proxmox VE release that introduced
// each feature. Use with VersionAtLeast to gate version-dependent behavior.
const (
	// CapHARules — HA node/resource affinity rules (HA groups deprecated, PVE 9.0).
	CapHARules = "9.0"
	// CapOCIImages — OCI registry image pull (PVE 9.1).
	CapOCIImages = "9.1"
	// CapCRSDynamic — CRS dynamic load balancer, i.e. native DRS (PVE 9.2).
	CapCRSDynamic = "9.2"
	// CapHAArmDisarm — cluster-wide Arm/Disarm HA maintenance (PVE 9.2).
	CapHAArmDisarm = "9.2"
)

var versionRe = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// ParseVersion extracts major.minor.patch from a Proxmox version string such as
// "9.1.2" or "9.1". Missing components default to 0. A trailing build suffix
// (e.g. "9.1.2-3") is ignored. ok is false when the string has no leading
// numeric component.
func ParseVersion(s string) (major, minor, patch int, ok bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		minor, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	return major, minor, patch, true
}

// VersionAtLeast reports whether the Proxmox version current is >= minVer,
// compared leniently across major.minor.patch. An empty or unparseable current
// returns false (fail closed for capability checks).
func VersionAtLeast(current, minVer string) bool {
	ca, cb, cc, ok := ParseVersion(current)
	if !ok {
		return false
	}
	ma, mb, mc, ok := ParseVersion(minVer)
	if !ok {
		return false
	}
	switch {
	case ca != ma:
		return ca > ma
	case cb != mb:
		return cb > mb
	default:
		return cc >= mc
	}
}
