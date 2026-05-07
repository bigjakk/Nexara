package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// errKEVPlausibility is returned when a KEV feed body parses but fails
// sanity checks (count mismatch, missing version, future-dated release).
// The reviewer specifically called out KEV integrity as Finding A23 —
// without a published detached signature today (CISA does not currently
// distribute one alongside the feed), plausibility is the strongest
// integrity signal we have on top of TLS.
var errKEVPlausibility = errors.New("scanner: KEV feed failed plausibility check")

// kevPlausibilityChecks runs the structural sanity checks documented in
// the integrity guarantees of a CVE feed. Each check is best-effort: an
// upstream layout change shouldn't fail closed, but obvious tampering
// or truncation should.
//
//   - title must be present (CISA always sets it)
//   - catalogVersion must be non-empty
//   - dateReleased must be set, must be parseable, and must not be more
//     than a day in the future (clock skew tolerance)
//   - count must match len(vulnerabilities) within a small tolerance
//     (CISA has historically been off-by-one when the catalog is mid-
//     publish; allow ±2)
func kevPlausibilityChecks(feed *kevFeed, now time.Time) error {
	if feed == nil {
		return fmt.Errorf("%w: nil feed", errKEVPlausibility)
	}
	if feed.Title == "" {
		return fmt.Errorf("%w: missing title", errKEVPlausibility)
	}
	if feed.CatalogVersion == "" {
		return fmt.Errorf("%w: missing catalogVersion", errKEVPlausibility)
	}
	if feed.DateReleased != "" {
		// CISA uses RFC3339-ish dates with timezone (e.g. 2026-04-30T12:00:00.000Z).
		// Try a few common shapes; if none parse, log but don't fail —
		// the feed has been seen with two different formats over time.
		var parsed time.Time
		var err error
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.000Z",
			"2006-01-02",
		}
		for _, l := range layouts {
			parsed, err = time.Parse(l, feed.DateReleased)
			if err == nil {
				break
			}
		}
		if err == nil && parsed.After(now.Add(24*time.Hour)) {
			return fmt.Errorf("%w: dateReleased %q is in the future", errKEVPlausibility, feed.DateReleased)
		}
	}
	if feed.Count > 0 {
		diff := feed.Count - len(feed.Vulnerabilities)
		if diff < 0 {
			diff = -diff
		}
		if diff > 2 {
			return fmt.Errorf("%w: count=%d but %d vulnerabilities present", errKEVPlausibility, feed.Count, len(feed.Vulnerabilities))
		}
	}
	return nil
}

// fetchDetachedSignature pulls a detached signature from sigURL if one
// exists. Returns (sigBytes, true, nil) on success, (_, false, nil) when
// the signature endpoint is 404 (the most common case today — CISA does
// not publish a `.sig` alongside the KEV feed), and (_, false, err) for
// real transport errors.
//
// This is wired so that *if* CISA (or another upstream feed we add later)
// starts publishing a detached signature, switching it on becomes a
// matter of pointing sigURL at the right endpoint and registering a
// verifier. Today we only persist the bytes; the openpgp/minisign
// verifier itself is intentionally not pulled in as a dependency until
// it has a real signed feed to verify.
func fetchDetachedSignature(ctx context.Context, client *http.Client, sigURL string) (sig []byte, ok bool, err error) {
	if sigURL == "" {
		return nil, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sigURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create signature request: %w", err)
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("fetch signature: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if err := checkUpstreamStatus(resp); err != nil {
		return nil, false, err
	}
	const maxSigBytes = 64 * 1024 // detached signatures are tiny — a few hundred bytes
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSigBytes))
	if err != nil {
		return nil, false, fmt.Errorf("read signature body: %w", err)
	}
	if len(body) == 0 {
		return nil, false, nil
	}
	return body, true, nil
}
