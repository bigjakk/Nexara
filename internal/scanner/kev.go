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
	kevSignatureURL = "" // CISA does not currently publish a detached signature; leave blank.
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
//   - Detached signature: kevSignatureURL is queried via fetchDetachedSignature
//     when set. CISA does not currently publish one, so the constant is left
//     empty and the verification step logs "no signature available; relying
//     on TLS" — wired so a future operator can flip it on without a refactor.
//   - Plausibility: kevPlausibilityChecks runs after JSON unmarshal, blocking
//     obvious tampering (count mismatch, missing version, future dateReleased).
//   - Storage integrity: the kev_cache rows are upserted on every refresh; a
//     prior bad row is replaced rather than retained.
type KEVClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger

	feedURL      string // overridable for tests
	signatureURL string // overridable for tests
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
		httpClient:   httpClient,
		queries:      queries,
		logger:       logger,
		feedURL:      kevFeedURL,
		signatureURL: kevSignatureURL,
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

// Refresh downloads the KEV catalog and upserts it into the local cache.
// Returns the number of entries written.
func (c *KEVClient) Refresh(ctx context.Context) (int, error) {
	c.logger.Info("fetching CISA KEV catalog", "url", c.feedURL)

	body, err := c.fetchFeed(ctx)
	if err != nil {
		return 0, err
	}

	// Detached signature verification is wired but currently inactive — see
	// the integrity comment on KEVClient. When CISA publishes a signature
	// endpoint, set kevSignatureURL and add a verifier here.
	if sig, ok, sigErr := fetchDetachedSignature(ctx, c.httpClient, c.signatureURL); sigErr != nil {
		c.logger.Warn("KEV signature fetch failed; relying on TLS for integrity", "error", sigErr)
	} else if !ok {
		// Either signatureURL is empty (current default) or the endpoint
		// returned 404. Logged at Debug rather than Info so we don't spam
		// the operator's logs every refresh tick.
		c.logger.Debug("KEV detached signature not available; relying on TLS")
	} else {
		// SECURITY (security-reviewer H3 fix): if a signature endpoint is
		// configured AND returns bytes, we have no verifier wired yet — so
		// we cannot accept the feed. Failing closed makes the contract
		// "if you set the URL, ALSO wire the verifier" enforceable rather
		// than advisory; otherwise an operator who flips the URL on sees
		// "signature retrieved" log lines and is misled into believing
		// integrity is enforced.
		return 0, fmt.Errorf(
			"KEV signature endpoint returned %d bytes but no verifier is wired; refusing to import unverified feed",
			len(sig))
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
