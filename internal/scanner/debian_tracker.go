package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	debianTrackerURL = "https://security-tracker.debian.org/tracker/data/json"
	cacheTTL         = 24 * time.Hour
	maxTrackerSize   = 100 * 1024 * 1024 // 100 MB cap on the JSON dump
)

// CVEInfo holds enriched CVE data from the Debian security tracker.
type CVEInfo struct {
	CVEID       string
	Severity    string
	CVSSScore   float32
	Description string
}

// CVEClient fetches and caches CVE data from the Debian Security Tracker.
//
// The client is intended to be long-lived — one per scanner Engine, shared
// across every cluster scan. The parsed tracker map (~80 MB unmarshalled)
// is held in process memory under mu and re-validated against trackerLoadedAt
// + cacheTTL on every entry. When the in-memory copy is stale (or never
// loaded — fresh process start), we attempt a DB hit (external_feed_cache);
// a miss there falls through to a full HTTP fetch from
// security-tracker.debian.org.
type CVEClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger

	mu                sync.Mutex
	trackerData       map[string]map[string]debianCVEEntry
	trackerLoadedAt   time.Time
	cacheTTL          time.Duration // overridable for tests
	feedURL           string        // overridable for tests
}

// debianCVEEntry is the per-CVE entry inside a package's map.
// In the Debian Security Tracker JSON, status / fixed_version / urgency live per-release —
// the CVE-level object only has description, scope, and the releases map.
type debianCVEEntry struct {
	Releases    map[string]debianRelease `json:"releases"`
	Description string                   `json:"description"`
}

type debianRelease struct {
	Status       string `json:"status"`
	FixedVersion string `json:"fixed_version"`
	Urgency      string `json:"urgency"`
}

// NewCVEClient creates a new CVE data client. The caller supplies the shared
// scanner HTTP client so connection pools, redirect policy, and SSRF guards
// are consistent across all feed fetches. Pass nil for httpClient in tests
// to fall back to a local default.
func NewCVEClient(queries *db.Queries, httpClient *http.Client, logger *slog.Logger) *CVEClient {
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = newScannerHTTPClient(120 * time.Second)
	}
	return &CVEClient{
		httpClient: httpClient,
		queries:    queries,
		logger:     logger,
		cacheTTL:   cacheTTL,
		feedURL:    debianTrackerURL,
	}
}

// LookupPackageCVEs returns known CVEs for the given package names.
// It fetches from the Debian Security Tracker and caches results in DB.
func (c *CVEClient) LookupPackageCVEs(ctx context.Context, packageNames []string) (map[string][]CVEInfo, error) {
	tracker, err := c.snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch tracker data: %w", err)
	}

	result := make(map[string][]CVEInfo)
	for _, pkg := range packageNames {
		cves, ok := tracker[pkg]
		if !ok {
			continue
		}
		for cveID, entry := range cves {
			// Skip if every release has resolved status; otherwise pick the best
			// non-resolved release's urgency (highest severity wins).
			if !hasOpenRelease(entry.Releases) {
				continue
			}
			severity := bestSeverity(entry.Releases)
			info := CVEInfo{
				CVEID:       cveID,
				Severity:    severity,
				CVSSScore:   severityToScore(severity),
				Description: truncate(entry.Description, 500),
			}
			result[pkg] = append(result[pkg], info)

			// Cache in DB
			_ = c.queries.UpsertCVECache(ctx, db.UpsertCVECacheParams{
				CveID:       cveID,
				Severity:    severity,
				CvssScore:   pgtype.Float4{Float32: info.CVSSScore, Valid: true},
				Description: info.Description,
			})
		}
	}

	return result, nil
}

// LookupPackageUpdates checks Proxmox apt update results for known CVEs.
// Updates from Proxmox represent packages that have fixes available.
// release is the Debian release codename (e.g. "bookworm") of the scanned node;
// CVEs that don't apply to that release, or that the user has already patched
// past, are filtered out. Pass "" to disable release-aware filtering.
func (c *CVEClient) LookupPackageUpdates(ctx context.Context, updates []AptUpdateInfo, release string) ([]VulnResult, error) {
	tracker, err := c.snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch tracker data: %w", err)
	}

	var results []VulnResult
	seen := make(map[string]bool)

	for _, upd := range updates {
		pkgCVEs, ok := tracker[upd.Package]
		if !ok {
			// Even without CVE data, security-origin updates are vulnerabilities
			if upd.IsSecurityUpdate {
				key := upd.Package + ":unknown"
				if !seen[key] {
					seen[key] = true
					results = append(results, VulnResult{
						CVEID:          "UNKNOWN-" + upd.Package,
						PackageName:    upd.Package,
						CurrentVersion: upd.OldVersion,
						FixedVersion:   upd.NewVersion,
						Severity:       "medium",
						CVSSScore:      5.0,
						Description:    upd.Title,
					})
				}
			}
			continue
		}

		for cveID, entry := range pkgCVEs {
			key := upd.Package + ":" + cveID
			if seen[key] {
				continue
			}

			rel, useRelease := pickRelease(entry.Releases, release)
			if useRelease {
				// Skip CVEs that don't apply to this release.
				if rel.Status == "" {
					continue
				}
				// Skip CVEs already fixed in the user's installed version.
				if rel.Status == "resolved" && rel.FixedVersion != "" && rel.FixedVersion != "0" {
					if cmp, ok := compareDebianVersion(upd.OldVersion, rel.FixedVersion); ok && cmp >= 0 {
						continue
					}
				}
			} else if !hasOpenRelease(entry.Releases) {
				// No release info — fall back to "any release open" check.
				continue
			}

			severity := normalizeSeverity(rel.Urgency)
			if severity == "unknown" {
				// Per-release urgency missing; pick the worst urgency across releases.
				severity = bestSeverity(entry.Releases)
			}

			fixedVer := rel.FixedVersion
			if fixedVer == "" || fixedVer == "0" {
				for _, r := range entry.Releases {
					if r.FixedVersion != "" && r.FixedVersion != "0" {
						fixedVer = r.FixedVersion
						break
					}
				}
			}
			if fixedVer == "" || fixedVer == "0" {
				fixedVer = upd.NewVersion
			}

			desc := entry.Description
			if desc == "" {
				desc = upd.Title
			}

			seen[key] = true
			results = append(results, VulnResult{
				CVEID:          cveID,
				PackageName:    upd.Package,
				CurrentVersion: upd.OldVersion,
				FixedVersion:   fixedVer,
				Severity:       severity,
				CVSSScore:      severityToScore(severity),
				Description:    truncate(desc, 500),
			})

			_ = c.queries.UpsertCVECache(ctx, db.UpsertCVECacheParams{
				CveID:       cveID,
				Severity:    severity,
				CvssScore:   pgtype.Float4{Float32: severityToScore(severity), Valid: true},
				Description: truncate(desc, 500),
			})
		}
	}

	return results, nil
}

// snapshot returns the current tracker data, loading it from process cache,
// DB cache, or network in that order. Safe to call concurrently — the load
// is serialised by mu so simultaneous cluster scans share the result.
func (c *CVEClient) snapshot(ctx context.Context) (map[string]map[string]debianCVEEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.trackerData != nil && time.Since(c.trackerLoadedAt) < c.cacheTTL {
		return c.trackerData, nil
	}

	// Try the DB-backed cache before reaching out to the network.
	if c.queries != nil {
		if body, fetchedAt, hit, err := loadFeedCache(ctx, c.queries, feedSourceDebianTracker, c.cacheTTL); err != nil {
			c.logger.Warn("debian tracker DB cache read failed; falling through to network",
				"error", err)
		} else if hit {
			parsed, err := parseTracker(body)
			if err != nil {
				c.logger.Warn("debian tracker DB cache parse failed; falling through to network",
					"error", err)
			} else {
				c.trackerData = parsed
				c.trackerLoadedAt = fetchedAt
				c.logger.Info("loaded Debian security tracker from DB cache",
					"packages", len(parsed),
					"age", time.Since(fetchedAt).Round(time.Second).String())
				return c.trackerData, nil
			}
		}
	}

	// Fetch fresh from upstream.
	body, etag, err := c.fetchTracker(ctx)
	if err != nil {
		// Network failure: only fall back to the in-memory copy when it's
		// recent enough that callers couldn't have noticed the difference
		// (within 2× cacheTTL). We do NOT silently fall back to an old
		// (>2× TTL) snapshot — a tracker that's a week stale is missing
		// every CVE filed since, and surfacing the network error gives
		// runScan a chance to surface the partial-data state via
		// securityUpdatesToVulns instead of pretending all is well.
		if c.trackerData != nil && time.Since(c.trackerLoadedAt) < 2*c.cacheTTL {
			c.logger.Warn("debian tracker fetch failed; reusing recent in-memory copy",
				"age", time.Since(c.trackerLoadedAt).Round(time.Second).String(),
				"error", err)
			return c.trackerData, nil
		}
		return nil, err
	}

	parsed, perr := parseTracker(body)
	if perr != nil {
		return nil, fmt.Errorf("parse tracker JSON: %w", perr)
	}

	c.trackerData = parsed
	c.trackerLoadedAt = time.Now()

	// Persist to DB so the next scheduler restart hits cache instead of the
	// network. Best-effort: store errors are logged but don't fail the load.
	if c.queries != nil {
		if err := storeFeedCache(ctx, c.queries, feedSourceDebianTracker, body, etag); err != nil {
			c.logger.Warn("failed to persist debian tracker cache to DB", "error", err)
		}
	}

	c.logger.Info("loaded Debian security tracker from network",
		"packages", len(parsed),
		"size_bytes", len(body))
	return c.trackerData, nil
}

func (c *CVEClient) fetchTracker(ctx context.Context) (body []byte, etag string, err error) {
	c.logger.Info("fetching Debian security tracker data", "url", c.feedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.feedURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if err := checkUpstreamStatus(resp); err != nil {
		return nil, "", err
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxTrackerSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxTrackerSize {
		return nil, "", fmt.Errorf("tracker body exceeds %d bytes", maxTrackerSize)
	}

	return body, resp.Header.Get("ETag"), nil
}

func parseTracker(body []byte) (map[string]map[string]debianCVEEntry, error) {
	var data map[string]map[string]debianCVEEntry
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("tracker JSON parsed but contained no packages")
	}
	return data, nil
}

// AptUpdateInfo represents a pending package update from Proxmox.
type AptUpdateInfo struct {
	Package          string
	Title            string
	OldVersion       string
	NewVersion       string
	Origin           string
	IsSecurityUpdate bool
}

// VulnResult is a matched vulnerability ready for DB insertion.
type VulnResult struct {
	CVEID          string
	PackageName    string
	CurrentVersion string
	FixedVersion   string
	Severity       string
	CVSSScore      float32
	Description    string
}

// hasOpenRelease reports whether any release entry is in a non-resolved state.
func hasOpenRelease(releases map[string]debianRelease) bool {
	for _, r := range releases {
		if r.Status != "resolved" {
			return true
		}
	}
	return false
}

// bestSeverity returns the highest severity across all releases for a CVE,
// preferring releases that are still open.
func bestSeverity(releases map[string]debianRelease) string {
	best := "unknown"
	for _, r := range releases {
		s := normalizeSeverity(r.Urgency)
		if severityRank(s) > severityRank(best) {
			best = s
		}
	}
	return best
}

// pickRelease returns the release entry for the given codename and a flag
// indicating whether release-aware lookup was attempted. When release is empty,
// the second return value is false and callers should fall back to scanning
// all releases.
func pickRelease(releases map[string]debianRelease, release string) (debianRelease, bool) {
	if release == "" {
		return debianRelease{}, false
	}
	if r, ok := releases[release]; ok {
		return r, true
	}
	return debianRelease{}, true
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// pveMajorToDebianRelease maps a Proxmox VE major version to the Debian
// codename it ships on. Returns "" when unknown.
func pveMajorToDebianRelease(major int) string {
	switch major {
	case 9:
		return "trixie"
	case 8:
		return "bookworm"
	case 7:
		return "bullseye"
	case 6:
		return "buster"
	default:
		return ""
	}
}

// DebianReleaseFromPVEVersion parses a PVEVersion string like
// "pve-manager/8.2.4/abc123" or "8.2.4-1" and returns the Debian codename.
func DebianReleaseFromPVEVersion(pveVersion string) string {
	v := pveVersion
	if i := strings.Index(v, "/"); i >= 0 {
		// "pve-manager/8.2.4/..." → take segment after first slash
		rest := v[i+1:]
		if j := strings.Index(rest, "/"); j >= 0 {
			rest = rest[:j]
		}
		v = rest
	}
	// Now v looks like "8.2.4" or "8.2.4-1"
	if i := strings.IndexAny(v, ".-"); i > 0 {
		v = v[:i]
	}
	major := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			break
		}
		major = major*10 + int(ch-'0')
	}
	if major == 0 {
		return ""
	}
	return pveMajorToDebianRelease(major)
}

// compareDebianVersion compares two Debian package version strings.
// Returns -1, 0, or 1 (a < b, a == b, a > b) and ok=true on success;
// ok=false if either string is malformed.
//
// Implements the algorithm from deb-version(7): compares
// [epoch:]upstream-version[-debian-revision], where each segment is split into
// alternating non-digit / digit runs. Within a non-digit run, '~' < (empty) <
// any other char, with letters less than non-letters.
func compareDebianVersion(a, b string) (int, bool) {
	ea, ua, ra, okA := parseDebVersion(a)
	eb, ub, rb, okB := parseDebVersion(b)
	if !okA || !okB {
		return 0, false
	}
	if ea != eb {
		if ea < eb {
			return -1, true
		}
		return 1, true
	}
	if c := compareDebPart(ua, ub); c != 0 {
		return c, true
	}
	return compareDebPart(ra, rb), true
}

func parseDebVersion(v string) (epoch int, upstream, revision string, ok bool) {
	if v == "" {
		return 0, "", "", false
	}
	rest := v
	if i := strings.Index(rest, ":"); i > 0 {
		// Epoch must be all digits.
		for _, ch := range rest[:i] {
			if ch < '0' || ch > '9' {
				return 0, "", "", false
			}
		}
		for _, ch := range rest[:i] {
			epoch = epoch*10 + int(ch-'0')
		}
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, "-"); i >= 0 {
		upstream = rest[:i]
		revision = rest[i+1:]
	} else {
		upstream = rest
	}
	return epoch, upstream, revision, true
}

func compareDebPart(a, b string) int {
	for a != "" || b != "" {
		// Non-digit prefix
		i, j := 0, 0
		for i < len(a) && (a[i] < '0' || a[i] > '9') {
			i++
		}
		for j < len(b) && (b[j] < '0' || b[j] > '9') {
			j++
		}
		if c := compareDebChars(a[:i], b[:j]); c != 0 {
			return c
		}
		a, b = a[i:], b[j:]
		// Digit prefix
		i, j = 0, 0
		for i < len(a) && a[i] >= '0' && a[i] <= '9' {
			i++
		}
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			j++
		}
		na := trimLeadingZeros(a[:i])
		nb := trimLeadingZeros(b[:j])
		if len(na) != len(nb) {
			if len(na) < len(nb) {
				return -1
			}
			return 1
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
		a, b = a[i:], b[j:]
	}
	return 0
}

func trimLeadingZeros(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// compareDebChars compares two non-digit segments per Debian version rules:
// '~' sorts before everything (including empty); empty sorts before any
// non-tilde char; letters sort before non-letters; otherwise ASCII order.
func compareDebChars(a, b string) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var ca, cb byte
		if i < len(a) {
			ca = a[i]
		}
		if i < len(b) {
			cb = b[i]
		}
		if ca == cb {
			continue
		}
		oa := charOrder(ca, i < len(a))
		ob := charOrder(cb, i < len(b))
		if oa < ob {
			return -1
		}
		if oa > ob {
			return 1
		}
	}
	return 0
}

func charOrder(c byte, present bool) int {
	if !present {
		// Empty (end of string) sorts before anything except '~'.
		return 1
	}
	if c == '~' {
		return 0
	}
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return 2 + int(c)
	}
	// Non-letter symbols sort after letters.
	return 1000 + int(c)
}

func normalizeSeverity(urgency string) string {
	switch strings.ToLower(urgency) {
	case "unimportant", "low":
		return "low"
	case "medium", "medium*":
		return "medium"
	case "high":
		return "high"
	case "not yet assigned":
		return "unknown"
	case "critical":
		return "critical"
	default:
		return "unknown"
	}
}

func severityToScore(severity string) float32 {
	switch severity {
	case "critical":
		return 9.5
	case "high":
		return 7.5
	case "medium":
		return 5.0
	case "low":
		return 2.5
	default:
		return 0.0
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
