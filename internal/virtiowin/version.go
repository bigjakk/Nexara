// Package virtiowin discovers virtio-win releases published by the Fedora
// virt group and turns them into ISO URLs Proxmox can fetch.
//
// Upstream layout, which the rest of this package encodes:
//
//	.../direct-downloads/stable-virtio/                 -> 301 to the current version dir
//	.../direct-downloads/archive-virtio/                -> autoindex of every version dir
//	.../archive-virtio/virtio-win-0.1.302-1/            -> one release
//	.../archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso
//
// Note the last line: the directory carries a release suffix ("-1") that the
// ISO filename does not. Getting that wrong 404s every download, so it lives in
// one place — SplitVersion / BuildISOURL — and is covered by tests.
package virtiowin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// BaseURL is the upstream download root. Kept https: the stable-virtio
	// redirect answers with an http:// Location, and we never want to inherit
	// that downgrade into a URL we hand to Proxmox.
	BaseURL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads"

	// StablePath is a redirect, not a document. A GET returns 301 whose
	// Location names the current stable version directory.
	StablePath = BaseURL + "/stable-virtio/"

	// ArchivePath is an Apache autoindex listing every published version.
	ArchivePath = BaseURL + "/archive-virtio/"
)

// versionPattern matches an upstream version with an optional release suffix:
// "0.1.302-1", "0.1.302", "0.1.96". Anchored — this validates whole strings
// parsed out of a redirect header or an HTML index, both attacker-adjacent
// inputs that end up in a URL.
var versionPattern = regexp.MustCompile(`^\d+(?:\.\d+)*(?:-\d+)?$`)

// dirPattern extracts a version from an upstream directory name.
var dirPattern = regexp.MustCompile(`virtio-win-(\d+(?:\.\d+)*(?:-\d+)?)/?`)

// ValidVersion reports whether v is a well-formed upstream version string.
func ValidVersion(v string) bool {
	return versionPattern.MatchString(v)
}

// SplitVersion separates an upstream version into the part used in directory
// names and the part used in the ISO filename.
//
// The ISO drops the release suffix: directory "virtio-win-0.1.302-1" holds
// "virtio-win-0.1.302.iso". A version with no suffix returns the same string
// twice.
func SplitVersion(version string) (dirVersion, isoVersion string) {
	return version, strings.SplitN(version, "-", 2)[0]
}

// ISOFilename returns the ISO basename for an upstream version, e.g.
// "virtio-win-0.1.302.iso" for "0.1.302-1".
func ISOFilename(version string) string {
	_, isoVersion := SplitVersion(version)
	return "virtio-win-" + isoVersion + ".iso"
}

// BuildISOURL returns the full https URL of the ISO for an upstream version.
// It returns an error rather than a malformed URL for an unparseable version,
// because the result is handed to a Proxmox node to fetch.
func BuildISOURL(version string) (string, error) {
	return BuildISOURLFrom(BaseURL, version)
}

// BuildISOURLFrom is BuildISOURL against an arbitrary download root, for the
// operator-configured mirror. The layout below the root is the upstream one —
// a mirror is expected to be a copy of the tree (`wget -m -np` produces
// exactly this), not an arbitrary file server.
//
// An empty base means upstream, so a caller need not special-case "no mirror".
func BuildISOURLFrom(base, version string) (string, error) {
	if !ValidVersion(version) {
		return "", fmt.Errorf("virtiowin: invalid version %q", version)
	}
	if base == "" {
		base = BaseURL
	}
	dirVersion, isoVersion := SplitVersion(version)
	return fmt.Sprintf("%s/archive-virtio/virtio-win-%s/virtio-win-%s.iso",
		strings.TrimSuffix(base, "/"), dirVersion, isoVersion), nil
}

// ParseVersionFromPath pulls a version out of an upstream directory name or a
// path/URL containing one. Returns "" when there is no match.
func ParseVersionFromPath(path string) string {
	m := dirPattern.FindStringSubmatch(path)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// Compare orders two upstream versions, returning -1, 0 or 1.
//
// Component-wise numeric comparison, not lexical: "0.1.96" is OLDER than
// "0.1.302", but sorts after it as a string. The release suffix breaks ties, so
// "0.1.302-2" beats "0.1.302-1". A component that will not parse as a number
// compares as 0, which keeps a malformed version from sorting above everything.
func Compare(a, b string) int {
	aBase, aRel := splitRelease(a)
	bBase, bRel := splitRelease(b)
	if c := compareNumericParts(aBase, bBase); c != 0 {
		return c
	}
	return compareNumericParts(aRel, bRel)
}

func splitRelease(v string) (base, release string) {
	parts := strings.SplitN(v, "-", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], "0"
}

func compareNumericParts(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	maxLen := max(len(aParts), len(bParts))
	for i := range maxLen {
		av, bv := 0, 0
		if i < len(aParts) {
			av, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bv, _ = strconv.Atoi(bParts[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Newest returns the highest version in the slice, or "" when empty.
func Newest(versions []string) string {
	newest := ""
	for _, v := range versions {
		if newest == "" || Compare(v, newest) > 0 {
			newest = v
		}
	}
	return newest
}
