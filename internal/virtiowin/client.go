package virtiowin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

const (
	// httpTimeout bounds a single upstream request. Both requests we make are
	// small — a redirect and an autoindex page — so this is generous.
	httpTimeout = 30 * time.Second

	// maxIndexBytes caps the archive autoindex read. The real page is ~16 KB;
	// this is the "don't stream an unbounded body into memory because upstream
	// changed" guard, matching how the scanner feeds are capped.
	maxIndexBytes = 2 << 20 // 2 MiB

	// userAgent is deliberately NOT browser-shaped. fedorapeople sits behind
	// Anubis, whose default policy challenges User-Agents containing "Mozilla"
	// with a proof-of-work interstitial and lets plain tool agents through. A
	// browser-like UA here would get an HTML challenge page instead of the
	// redirect we need. (Verified against the live host.)
	userAgent = "Nexara/1.0 (+https://github.com/bigjakk/Nexara)"
)

// Release is one virtio-win version as published upstream.
type Release struct {
	Version     string // upstream directory version, e.g. "0.1.302-1"
	ISOVersion  string // version as it appears in the ISO filename, "0.1.302"
	ISOFilename string
	ISOURL      string
	ISOSize     int64 // 0 when not probed
	IsStable    bool
}

// Client fetches release information from the upstream mirror.
type Client struct {
	http   *http.Client
	logger *slog.Logger
	base   string // NormalizeBase'd download root; defaults to BaseURL
}

// NewClient builds a Client over an SSRF-guarded, non-redirect-following HTTP
// client. Not following redirects is a requirement here rather than a
// precaution: the stable-virtio pointer IS a redirect, and following it would
// consume the Location header we need to read.
func NewClient(logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		http:   netguard.NewHTTPClient(httpTimeout),
		logger: logger,
		base:   BaseURL,
	}
}

// WithBase returns a shallow copy of the client pointed at a different
// download root, sharing the underlying HTTP client so the mirror override
// does not cost a fresh TLS handshake per check. An empty base means upstream.
//
// Normalising here rather than trusting the caller is what lets releaseFor
// build URLs without re-validating them: every URL this client hands a Proxmox
// node to dial is base + a path made of digits, so base having a host is the
// whole guarantee. Anything NormalizeBase rejects falls back to upstream.
func (c *Client) WithBase(base string) *Client {
	normalized, err := NormalizeBase(base)
	if err != nil || normalized == "" {
		normalized = BaseURL
	}
	clone := *c
	clone.base = normalized
	return &clone
}

// do issues one upstream request under the User-Agent Anubis lets through.
func (c *Client) do(ctx context.Context, method, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s request for %s: %w", method, rawURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, rawURL, err)
	}
	return resp, nil
}

// CheckStable resolves the version upstream currently marks stable.
//
// stable-virtio/ is a 301 whose Location names the current version directory,
// which makes discovery a single cheap request with no HTML parsing. Upstream
// answers with an http:// Location; only the version is extracted from it and
// the URL rebuilt under the configured base, so the header is never trusted as
// a URL and its scheme is never inherited.
func (c *Client) CheckStable(ctx context.Context) (Release, error) {
	resp, err := c.do(ctx, http.MethodGet, c.base+"/stable-virtio/")
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return Release{}, fmt.Errorf("virtiowin: expected a redirect from stable-virtio/, got status %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return Release{}, fmt.Errorf("virtiowin: stable-virtio/ returned %d with no Location header", resp.StatusCode)
	}
	version := ParseVersionFromPath(location)
	if version == "" {
		return Release{}, fmt.Errorf("virtiowin: could not parse a version out of stable redirect %q", location)
	}
	rel, err := c.releaseFor(version)
	if err != nil {
		return Release{}, err
	}
	rel.IsStable = true
	return rel, nil
}

// hrefPattern pulls href targets out of the Apache autoindex.
var hrefPattern = regexp.MustCompile(`href="([^"]+)"`)

// ListArchive returns every version present in the archive index.
//
// This is the fallback path for CheckStable and the source for prune decisions.
// Parsing an autoindex is brittle by nature, so callers treat a failure here as
// non-fatal when they already have a stable answer.
func (c *Client) ListArchive(ctx context.Context) ([]Release, error) {
	resp, err := c.do(ctx, http.MethodGet, c.base+"/archive-virtio/")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("virtiowin: archive index returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	if err != nil {
		return nil, fmt.Errorf("virtiowin: read archive index: %w", err)
	}

	seen := make(map[string]struct{})
	var out []Release
	for _, m := range hrefPattern.FindAllStringSubmatch(string(body), -1) {
		href := m[1]
		// Skip absolute links (the page's own nav/footer) and sort links.
		if strings.HasPrefix(href, "http") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "?") {
			continue
		}
		version := ParseVersionFromPath(href)
		if version == "" {
			continue
		}
		if _, dup := seen[version]; dup {
			continue
		}
		seen[version] = struct{}{}
		rel, err := c.releaseFor(version)
		if err != nil {
			continue
		}
		out = append(out, rel)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("virtiowin: archive index contained no recognisable versions")
	}
	return out, nil
}

// CheckLatest resolves the current stable release, falling back to the newest
// version in the archive index when the redirect shape changes upstream.
// Degrading rather than failing matters: the redirect is a single point of
// failure for a job that otherwise runs unattended for months.
func (c *Client) CheckLatest(ctx context.Context) (Release, error) {
	rel, err := c.CheckStable(ctx)
	if err == nil {
		return rel, nil
	}
	stableErr := err
	c.logger.Warn("virtio-win: stable redirect unusable, falling back to archive index", "error", stableErr)

	archive, archiveErr := c.ListArchive(ctx)
	if archiveErr != nil {
		return Release{}, fmt.Errorf("virtiowin: stable redirect failed (%w) and archive fallback failed: %w", stableErr, archiveErr)
	}
	// ListArchive never returns an empty slice without an error, so there is
	// always one to pick — and it already holds the built Release, so there is
	// nothing to rebuild. Deliberately not flagged stable: this is a best guess
	// from directory names, not upstream's own statement of what stable is.
	newest := archive[0]
	for _, r := range archive[1:] {
		if Compare(r.Version, newest.Version) > 0 {
			newest = r
		}
	}
	return newest, nil
}

// ProbeSize issues a HEAD for the ISO to learn its size, for display and for a
// free reachability check before asking a Proxmox node to fetch ~837 MiB.
// A failure is reported as size 0 with an error; callers may ignore it.
func (c *Client) ProbeSize(ctx context.Context, isoURL string) (int64, error) {
	resp, err := c.do(ctx, http.MethodHead, isoURL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD %s: status %d", isoURL, resp.StatusCode)
	}
	size, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("HEAD %s: unparseable Content-Length: %w", isoURL, err)
	}
	return size, nil
}

// releaseFor builds a Release from a version string.
//
// The version is validated in BuildISOURLFrom, which rejects anything that is
// not a well-formed upstream version before it can reach a Proxmox node. The
// resulting URL needs no second check: it is always c.base + a path built from
// that validated version, and WithBase admits no base that NormalizeBase has
// not already required to be http(s) with a host.
func (c *Client) releaseFor(version string) (Release, error) {
	isoURL, err := BuildISOURLFrom(c.base, version)
	if err != nil {
		return Release{}, err
	}
	dirVersion, isoVersion := SplitVersion(version)
	return Release{
		Version:     dirVersion,
		ISOVersion:  isoVersion,
		ISOFilename: ISOFilename(version),
		ISOURL:      isoURL,
	}, nil
}
