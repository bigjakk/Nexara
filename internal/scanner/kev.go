package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	kevFeedURL     = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	kevMaxBytes    = 50 * 1024 * 1024
	kevHTTPTimeout = 60 * time.Second
)

// KEVClient fetches CISA's Known Exploited Vulnerabilities catalog and caches
// it locally. The catalog is small (~1500 entries) so we mirror it whole into
// the kev_cache table; the per-CVE rows are then served out of the DB by
// IsKEV() during scans without any additional network traffic.
//
// Integrity (Finding A23):
//   - Transport security: HTTPS only (`https://www.cisa.gov/...`), with the
//     stdlib's default TLS verification + the scanner-shared dial guard.
//   - Plausibility: kevPlausibilityChecks runs after JSON unmarshal, blocking
//     obvious tampering or upstream truncation (count mismatch, missing
//     version, future dateReleased).
//   - Storage integrity: the kev_cache rows are upserted on every refresh; a
//     prior bad row is replaced rather than retained.
//
// CISA does not currently publish a detached signature alongside the feed,
// so cryptographic verification is not part of the integrity story today.
// If they start publishing one, this is the natural place to wire it.
type KEVClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger

	feedURL string // overridable for tests
}

// NewKEVClient creates a new KEV client. Pass the scanner-shared httpClient
// so the redirect policy + dial-time SSRF guard apply. Pass nil for tests
// to fall back to a fresh local client.
func NewKEVClient(queries *db.Queries, httpClient *http.Client, logger *slog.Logger) *KEVClient {
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = newScannerHTTPClient(kevHTTPTimeout)
	}
	return &KEVClient{
		httpClient: httpClient,
		queries:    queries,
		logger:     logger,
		feedURL:    kevFeedURL,
	}
}

// kevFeed is the top-level CISA KEV JSON shape.
type kevFeed struct {
	Title           string     `json:"title"`
	CatalogVersion  string     `json:"catalogVersion"`
	DateReleased    string     `json:"dateReleased"`
	Count           int        `json:"count"`
	Vulnerabilities []kevEntry `json:"vulnerabilities"`
}

type kevEntry struct {
	CVEID                      string `json:"cveID"`
	VendorProject              string `json:"vendorProject"`
	Product                    string `json:"product"`
	VulnerabilityName          string `json:"vulnerabilityName"`
	DateAdded                  string `json:"dateAdded"`
	ShortDescription           string `json:"shortDescription"`
	RequiredAction             string `json:"requiredAction"`
	DueDate                    string `json:"dueDate"`
	KnownRansomwareCampaignUse string `json:"knownRansomwareCampaignUse"`
	Notes                      string `json:"notes"`
}

// errKEVPlausibility is returned when a KEV feed body parses but fails
// sanity checks (count mismatch, missing version, future-dated release).
// Without a published detached signature, plausibility is the strongest
// integrity signal we have on top of TLS.
var errKEVPlausibility = errors.New("scanner: KEV feed failed plausibility check")

// kevPlausibilityChecks runs structural sanity checks on a freshly-parsed
// KEV feed. Each check guards against obvious upstream truncation or
// tampering:
//
//   - title must be present (CISA always sets it)
//   - catalogVersion must be non-empty
//   - dateReleased, when set, must not be more than 24h in the future
//     (clock skew tolerance)
//   - count, when set, must match len(vulnerabilities) within ±2 (CISA has
//     historically been off-by-one when the catalog is mid-publish)
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
		// Try a few common shapes; if none parse, skip the future-date check
		// — the feed has shipped with two different formats over time.
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

// Refresh downloads the KEV catalog and upserts it into the local cache.
// Returns the number of entries written.
func (c *KEVClient) Refresh(ctx context.Context) (int, error) {
	c.logger.Info("fetching CISA KEV catalog", "url", c.feedURL)

	body, err := c.fetchFeed(ctx)
	if err != nil {
		return 0, err
	}

	var feed kevFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return 0, fmt.Errorf("parse KEV JSON: %w", err)
	}

	if err := kevPlausibilityChecks(&feed, time.Now()); err != nil {
		return 0, err
	}

	written := 0
	for _, e := range feed.Vulnerabilities {
		if e.CVEID == "" {
			continue
		}
		dateAdded, err := parseKEVDate(e.DateAdded)
		if err != nil {
			c.logger.Warn("skipping KEV entry with bad dateAdded",
				"cve", e.CVEID, "value", e.DateAdded)
			continue
		}
		params := db.UpsertKEVEntryParams{
			CveID:             e.CVEID,
			DateAdded:         pgtype.Date{Time: dateAdded, Valid: true},
			VendorProject:     e.VendorProject,
			Product:           e.Product,
			VulnerabilityName: e.VulnerabilityName,
			ShortDescription:  truncate(e.ShortDescription, 1000),
			RequiredAction:    truncate(e.RequiredAction, 1000),
			RansomwareUse:     isYes(e.KnownRansomwareCampaignUse),
		}
		if due, err := parseKEVDate(e.DueDate); err == nil {
			params.DueDate = pgtype.Date{Time: due, Valid: true}
		}

		if err := c.queries.UpsertKEVEntry(ctx, params); err != nil {
			c.logger.Error("failed to upsert KEV entry", "cve", e.CVEID, "error", err)
			continue
		}
		written++
	}

	c.logger.Info("KEV catalog refresh complete", "entries", written, "version", feed.CatalogVersion)
	return written, nil
}

func (c *KEVClient) fetchFeed(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if err := checkUpstreamStatus(resp); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, kevMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > kevMaxBytes {
		return nil, fmt.Errorf("KEV feed body exceeds %d bytes", kevMaxBytes)
	}
	return body, nil
}

// IsKEV checks the local cache for a CVE. ErrNoRows is the legitimate
// "this CVE is not in KEV" signal and returns false silently. Any other
// DB error (transient connection drop, query timeout) is logged so a
// silently-degraded scan doesn't mark every CVE as non-KEV without the
// operator noticing the lookup is broken.
func (c *KEVClient) IsKEV(ctx context.Context, cveID string) bool {
	_, err := c.queries.GetKEVEntry(ctx, cveID)
	if err == nil {
		return true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		c.logger.Warn("KEV lookup failed; assuming non-KEV", "cve", cveID, "error", err)
	}
	return false
}

func parseKEVDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	return time.Parse("2006-01-02", s)
}

func isYes(s string) bool {
	switch s {
	case "Known", "known", "Yes", "yes", "true":
		return true
	default:
		return false
	}
}
