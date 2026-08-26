package veeam

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MinBuildVersion is the oldest VBR build this integration supports.
//
// 13.0.x reports Proxmox jobs with type "Unknown" and an empty lastRun, so job
// state, RPO and coverage would all be silently wrong rather than missing.
// Refusing is the honest answer; the check compares major.minor only, because
// the patch and build components move constantly and carry no compatibility
// meaning.
const MinBuildVersion = "13.1"

// DefaultRevision is the newest x-api-version this client is written against.
// Every fixture in testdata/ was captured at this revision.
const DefaultRevision = "1.3-rev2"

// probeRevisions is the descending fallback ladder, used only when
// /swagger/index.js is unavailable. Kept short deliberately: each rung costs a
// password grant against a live server.
var probeRevisions = []string{"1.3-rev2", "1.3-rev1", "1.3-rev0"}

// revisionPattern matches the API revisions listed in the Swagger UI
// bootstrap, which VBR serves unauthenticated at /swagger/index.js. The
// bootstrap embeds a JSON config whose urls[] entries look like
//
//	{"url":"/swagger/v1.3-rev2/swagger.json","name":"V1.3-REV2"}
//
// Anchoring on the URL rather than the name is deliberate: the name is
// uppercased ("V1.3-REV2") while the x-api-version header takes the bare
// lowercase form ("1.3-rev2"), so the URL is the one that needs no case
// repair beyond lowering. Note the `v` prefix appears in the spec path but
// never in the header.
var revisionPattern = regexp.MustCompile(`(?i)/swagger/v(\d+\.\d+-rev\d+)/swagger\.json`)

// parseRevisions extracts the supported API revisions from a Swagger UI
// bootstrap body, newest first. Returns nil if none are present.
func parseRevisions(body string) []string {
	matches := revisionPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rev := strings.ToLower(m[1])
		if _, dup := seen[rev]; dup {
			continue
		}
		seen[rev] = struct{}{}
		out = append(out, rev)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return compareRevisions(out[i], out[j]) > 0
	})
	return out
}

// compareRevisions orders "major.minor-revN" strings numerically. Returns >0
// when a is newer than b, <0 when older, 0 when equal.
//
// Numeric rather than lexical because VBR will eventually ship a rev10, and
// string ordering would rank it below rev9.
func compareRevisions(a, b string) int {
	aMaj, aMin, aRev := splitRevision(a)
	bMaj, bMin, bRev := splitRevision(b)
	switch {
	case aMaj != bMaj:
		return aMaj - bMaj
	case aMin != bMin:
		return aMin - bMin
	default:
		return aRev - bRev
	}
}

// splitRevision decomposes "1.3-rev2" into (1, 3, 2). Unparseable components
// become -1, which sorts them below anything well-formed.
func splitRevision(s string) (major, minor, rev int) {
	major, minor, rev = -1, -1, -1

	verPart, revPart, hasRev := strings.Cut(strings.ToLower(s), "-rev")
	if hasRev {
		if n, err := strconv.Atoi(revPart); err == nil {
			rev = n
		}
	}
	majPart, minPart, hasDot := strings.Cut(verPart, ".")
	if n, err := strconv.Atoi(majPart); err == nil {
		major = n
	}
	if hasDot {
		if n, err := strconv.Atoi(minPart); err == nil {
			minor = n
		}
	}
	return major, minor, rev
}

// pickRevision returns the newest revision in supported that this client knows
// how to speak, or "" if there is no overlap.
//
// "Knows how to speak" means "not newer than DefaultRevision". Taking a newer
// revision than the fixtures were captured at would opt us into schema changes
// nobody has looked at — the whole reason the revision is pinned and persisted
// rather than left to the server's default.
func pickRevision(supported []string) string {
	best := ""
	for _, rev := range supported {
		if compareRevisions(rev, DefaultRevision) > 0 {
			continue
		}
		if best == "" || compareRevisions(rev, best) > 0 {
			best = rev
		}
	}
	return best
}

// buildVersionSupported reports whether a VBR buildVersion meets
// MinBuildVersion, comparing major.minor only.
//
// An unparseable or empty version is NOT supported: this gates a feature that
// silently misreports on older builds, so an unknown version has to fail
// closed.
func buildVersionSupported(buildVersion string) bool {
	gotMaj, gotMin, ok := splitBuildVersion(buildVersion)
	if !ok {
		return false
	}
	minMaj, minMin, ok := splitBuildVersion(MinBuildVersion)
	if !ok {
		return false
	}
	if gotMaj != minMaj {
		return gotMaj > minMaj
	}
	return gotMin >= minMin
}

// splitBuildVersion pulls major and minor out of a dotted version string of
// any arity ("13.1", "13.1.0.411"). ok is false if either is missing or
// non-numeric.
func splitBuildVersion(s string) (major, minor int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
