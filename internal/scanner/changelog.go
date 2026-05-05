package scanner

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// CVE references in Debian-format changelogs follow the standard
// CVE-YYYY-NNNN+ pattern. We accept 4–7-digit sequence numbers (current
// MITRE allocator goes up to 6 digits; allow headroom) and we restrict
// the year to 1999..2099 to filter out random "CVE-XX" mentions in prose.
var cveRefPattern = regexp.MustCompile(`(?i)CVE-(?:19|20)\d{2}-\d{4,7}`)

// changelogEntryHeader matches the start of a Debian-format changelog
// entry: "pkgname (version) distribution; urgency=...". The package name
// is captured loosely (lowercase letters, digits, common punctuation) and
// the version is whatever sits between the parentheses.
var changelogEntryHeader = regexp.MustCompile(`(?m)^([a-z0-9][a-z0-9.+-]*)\s*\(([^)]+)\)\s+\S+;`)

// extractCVEsFromChangelog returns the deduplicated, sorted set of CVE IDs
// referenced anywhere in the given changelog text. Sorting is deterministic
// so callers can use the result as part of a stable signature.
func extractCVEsFromChangelog(text string) []string {
	if text == "" {
		return nil
	}
	matches := cveRefPattern.FindAllString(text, -1)
	return dedupSortedUpper(matches)
}

// extractCVEsFromChangelogRange returns CVE IDs referenced only within
// changelog entries whose version is strictly greater than installedVer
// (and ≤ targetVer when targetVer is non-empty). This filters out historical
// entries the user has already received — `apt-get changelog` returns the
// full upstream history, but only entries strictly newer than installed
// represent fixes that aren't yet on the cluster.
//
// Falls back to extracting all CVEs when version parsing fails for both
// inputs (rare — mostly malformed changelogs); never returns nothing
// silently when CVEs are present in the text.
func extractCVEsFromChangelogRange(text, installedVer, targetVer string) []string {
	if text == "" {
		return nil
	}
	if installedVer == "" {
		// No baseline to compare against — return all CVEs. This is the
		// pessimistic "if in doubt, surface it" path.
		return extractCVEsFromChangelog(text)
	}

	entries := splitChangelogEntries(text)
	if len(entries) == 0 {
		return extractCVEsFromChangelog(text)
	}

	var matches []string
	parseable := false
	for _, e := range entries {
		// installed_version < entry_version  → this entry is a fix the
		// user does not yet have. Include its CVE refs.
		cmp, ok := compareDebianVersion(installedVer, e.version)
		if !ok {
			continue
		}
		parseable = true
		if cmp >= 0 {
			continue // already at or past this entry — skip
		}
		if targetVer != "" {
			cmp2, ok2 := compareDebianVersion(e.version, targetVer)
			if ok2 && cmp2 > 0 {
				continue // beyond what the upgrade applies
			}
		}
		matches = append(matches, cveRefPattern.FindAllString(e.body, -1)...)
	}
	if !parseable {
		// Couldn't parse any entry version against installed — fall back
		// to including all CVEs rather than silently dropping everything.
		return extractCVEsFromChangelog(text)
	}
	return dedupSortedUpper(matches)
}

type changelogEntry struct {
	version string
	body    string
}

// splitChangelogEntries breaks a Debian-format changelog into per-version
// entries. Each entry starts at a header line ("pkg (ver) distro; …") and
// extends to the next header or EOF.
func splitChangelogEntries(text string) []changelogEntry {
	idxs := changelogEntryHeader.FindAllStringSubmatchIndex(text, -1)
	if len(idxs) == 0 {
		return nil
	}
	entries := make([]changelogEntry, 0, len(idxs))
	for i, m := range idxs {
		// m[0]:m[1] = full header line; m[4]:m[5] = version capture group.
		end := len(text)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		entries = append(entries, changelogEntry{
			version: strings.TrimSpace(text[m[4]:m[5]]),
			body:    text[m[1]:end],
		})
	}
	return entries
}

func dedupSortedUpper(matches []string) []string {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		seen[strings.ToUpper(m)] = true
	}
	out := make([]string, 0, len(seen))
	for cve := range seen {
		out = append(out, cve)
	}
	sort.Strings(out)
	return out
}

// changelogFetcher fetches and caches per-(package, version) changelog
// text from a Proxmox node. Caching is per-fetcher (single scan scope):
// all nodes in a cluster share the same upgrade target list, so we want
// to avoid 3× the API calls. Caches the raw text — extraction happens
// per call site so different (installed, target) brackets can reuse the
// same fetch.
type changelogFetcher struct {
	client *proxmox.Client
	logger *slog.Logger

	mu     sync.Mutex
	byPkg  map[string]string // key: "pkg=version" → raw changelog text
	failed map[string]bool   // key: "pkg=version" → true if fetch failed
}

func newChangelogFetcher(client *proxmox.Client, logger *slog.Logger) *changelogFetcher {
	return &changelogFetcher{
		client: client,
		logger: logger,
		byPkg:  make(map[string]string),
		failed: make(map[string]bool),
	}
}

// augmentWithChangelogCVEs takes the vulnerability list produced by the
// Debian Security Tracker matcher and adds entries for any CVEs found in
// the changelogs of pending updates that the tracker missed. The new
// entries are sparsely populated (severity = "unknown", CVSS = proxy)
// since the changelog text doesn't carry that metadata — downstream
// EPSS/KEV enrichment fills in the risk picture.
//
// node is the Proxmox node name (the changelog endpoint is per-node).
func augmentWithChangelogCVEs(
	ctx context.Context,
	vulns []VulnResult,
	updates []AptUpdateInfo,
	node string,
	changelogs *changelogFetcher,
	logger *slog.Logger,
) []VulnResult {
	// Build an index of (cve_id, package) → already-present, so we don't
	// duplicate entries the tracker produced. Same CVE on different
	// packages is allowed — those are genuinely separate findings.
	existing := make(map[string]bool, len(vulns))
	for _, v := range vulns {
		existing[v.CVEID+"@"+v.PackageName] = true
	}

	added := 0
	for _, upd := range updates {
		// Fetch the changelog for the new (target) version, then extract
		// only CVE references from entries strictly newer than what the
		// node has installed. apt-get changelog returns the full upstream
		// history; without bracketing we'd surface CVEs that were fixed
		// long before the user's current version.
		cves := changelogs.CVEsBetween(ctx, node, upd.Package, upd.OldVersion, upd.NewVersion)
		for _, cve := range cves {
			if existing[cve+"@"+upd.Package] {
				continue
			}
			vulns = append(vulns, VulnResult{
				CVEID:          cve,
				PackageName:    upd.Package,
				CurrentVersion: upd.OldVersion,
				FixedVersion:   upd.NewVersion,
				Severity:       "unknown",
				CVSSScore:      0,
				Description:    "Referenced in " + upd.Package + " changelog",
			})
			existing[cve+"@"+upd.Package] = true
			added++
		}
	}

	if added > 0 {
		logger.Info("changelog augmentation added CVEs",
			"node", node, "added", added, "total", len(vulns))
	}
	return vulns
}

// CVEsBetween returns the CVE IDs referenced in changelog entries strictly
// newer than installedVer and at most targetVer. Fetches the target's full
// changelog from the Proxmox node if not cached. Errors produce nil —
// best-effort enrichment.
func (f *changelogFetcher) CVEsBetween(ctx context.Context, node, pkg, installedVer, targetVer string) []string {
	text := f.fetchText(ctx, node, pkg, targetVer)
	if text == "" {
		return nil
	}
	return extractCVEsFromChangelogRange(text, installedVer, targetVer)
}

func (f *changelogFetcher) fetchText(ctx context.Context, node, pkg, version string) string {
	if pkg == "" {
		return ""
	}
	key := pkg + "=" + version

	f.mu.Lock()
	if text, ok := f.byPkg[key]; ok {
		f.mu.Unlock()
		return text
	}
	if f.failed[key] {
		f.mu.Unlock()
		return ""
	}
	f.mu.Unlock()

	text, err := f.client.GetNodeAptChangelog(ctx, node, pkg, version)
	if err != nil {
		f.logger.Debug("changelog fetch failed",
			"node", node, "pkg", pkg, "version", version, "error", err)
		f.mu.Lock()
		f.failed[key] = true
		f.mu.Unlock()
		return ""
	}

	f.mu.Lock()
	f.byPkg[key] = text
	f.mu.Unlock()
	return text
}
