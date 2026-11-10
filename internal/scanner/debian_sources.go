package scanner

import (
	"bufio"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// The tracker's debsecan export, one file per release. Its last section
	// names the binaries of every source the tracker has issues for, which
	// is the binary → source map the tracker JSON is keyed by
	// (security-tracker bin/security_db.py, calculateDebsecan1).
	debsecanReleaseURL = "https://security-tracker.debian.org/tracker/debsecan/release/1/"
	maxDebsecanSize    = 64 << 20  // compressed; about 2 MB in 2026
	maxDebsecanText    = 256 << 20 // decompressed; about 20 MB

	// sourceListRetry is how soon a release's list is fetched again after a
	// failure. The last list fetched, if any, is used meanwhile.
	sourceListRetry = 15 * time.Minute
)

// releaseSources is one release's binary → source map and when it was last
// fetched, or a fetch last failed.
type releaseSources struct {
	sources map[string]string // the last list fetched; nil until one is
	checked time.Time
	ok      bool
}

// binarySources returns the source package each binary of release is built
// from, per the tracker, cached for cacheTTL; nil when no list could be
// fetched. Nodes and scans all read the same list, so a source does not come
// and go with one node's changelog fetch.
func (c *CVEClient) binarySources(ctx context.Context, release string) map[string]string {
	if release == "" {
		return nil
	}
	c.sourcesMu.Lock()
	defer c.sourcesMu.Unlock()

	rs := c.sourcesByRelease[release]
	wait := c.cacheTTL
	if !rs.ok {
		wait = sourceListRetry
	}
	if !rs.checked.IsZero() && time.Since(rs.checked) < wait {
		return rs.sources
	}
	sources, err := c.fetchBinarySources(ctx, release)
	rs.checked, rs.ok = time.Now(), err == nil
	if err != nil {
		c.logger.Warn("could not fetch the Debian tracker's binary list; sources come from changelogs",
			"release", release, "error", err)
	} else {
		rs.sources = sources
	}
	c.sourcesByRelease[release] = rs
	return rs.sources
}

func (c *CVEClient) fetchBinarySources(ctx context.Context, release string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.debsecanURL+url.PathEscape(release), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if err := checkUpstreamStatus(resp); err != nil {
		return nil, err
	}

	zr, err := zlib.NewReader(io.LimitReader(resp.Body, maxDebsecanSize))
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer zr.Close()
	return parseDebsecanSources(io.LimitReader(zr, maxDebsecanText))
}

// parseDebsecanSources reads a debsecan VERSION 1 release file: an issue list,
// a vulnerability list, then lines of "source,binary binary…", the sections
// split by blank lines. A source whose one binary has its own name is not
// listed; the lookup's fallback to the binary name covers it.
func parseDebsecanSources(r io.Reader) (map[string]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20) // a source line lists every binary
	if !sc.Scan() || sc.Text() != "VERSION 1" {
		return nil, errors.New("not a debsecan VERSION 1 file")
	}
	section := 0
	sources := make(map[string]string)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			section++
			continue
		}
		if section != 2 {
			continue
		}
		if src, bins, ok := strings.Cut(line, ","); ok {
			for _, bin := range strings.Fields(bins) {
				sources[bin] = src
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(sources) == 0 {
		return nil, errors.New("debsecan file lists no binaries")
	}
	return sources, nil
}
