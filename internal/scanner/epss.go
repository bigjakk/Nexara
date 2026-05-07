package scanner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	epssAPIBase     = "https://api.first.org/data/v1/epss"
	epssBatchSize   = 100 // CVEs per request — keeps URLs under 4 KB
	epssCacheTTL    = 24 * time.Hour
	epssMaxBytes    = 5 * 1024 * 1024
	epssHTTPTimeout = 30 * time.Second
)

// EPSSClient looks up FIRST EPSS scores (probability of exploitation in the
// next 30 days) for CVEs, caching results in the DB.
//
// Integrity (Finding A23): FIRST does not publish a detached signature for
// the EPSS API. We rely on:
//   - HTTPS to api.first.org with stdlib TLS verification
//   - The shared scanner HTTP client's no-redirect policy + dial guard
//   - Per-CVE cache in epss_cache; entries within epssCacheTTL are served
//     from the DB without re-contacting FIRST.
type EPSSClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger

	apiBase string // overridable for tests
}

// NewEPSSClient creates a new EPSS client. Pass the scanner-shared httpClient
// for consistent redirect/dial-guard behaviour. Pass nil for tests.
func NewEPSSClient(queries *db.Queries, httpClient *http.Client, logger *slog.Logger) *EPSSClient {
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = newScannerHTTPClient(epssHTTPTimeout)
	}
	return &EPSSClient{
		httpClient: httpClient,
		queries:    queries,
		logger:     logger,
		apiBase:    epssAPIBase,
	}
}

// EPSSData is the per-CVE EPSS lookup result returned to callers.
type EPSSData struct {
	Score      float32
	Percentile float32
	Found      bool // false → no EPSS data exists for this CVE
}

// epssAPIResponse is the FIRST API JSON shape.
type epssAPIResponse struct {
	Status string         `json:"status"`
	Total  int            `json:"total"`
	Data   []epssAPIEntry `json:"data"`
}

type epssAPIEntry struct {
	CVE        string `json:"cve"`
	EPSS       string `json:"epss"`       // string-encoded float
	Percentile string `json:"percentile"` // string-encoded float
	Date       string `json:"date"`
}

// LookupBatch returns EPSS data for the given CVE list. Cache entries within
// epssCacheTTL are served locally; missing/stale CVEs are fetched in batches
// from FIRST and upserted into the cache. Errors fetching are logged and
// produce zero-value EPSSData entries (Found=false) rather than aborting.
func (c *EPSSClient) LookupBatch(ctx context.Context, cveIDs []string) map[string]EPSSData {
	result := make(map[string]EPSSData, len(cveIDs))
	if len(cveIDs) == 0 {
		return result
	}

	// Dedup and check cache.
	cutoff := time.Now().Add(-epssCacheTTL)
	toFetch := make([]string, 0, len(cveIDs))
	seen := make(map[string]bool)
	for _, cve := range cveIDs {
		if seen[cve] {
			continue
		}
		seen[cve] = true

		entry, err := c.queries.GetEPSSEntry(ctx, cve)
		if err == nil && entry.FetchedAt.After(cutoff) {
			result[cve] = EPSSData{
				Score:      entry.Score,
				Percentile: entry.Percentile,
				Found:      true,
			}
			continue
		}
		toFetch = append(toFetch, cve)
	}

	if len(toFetch) == 0 {
		return result
	}

	c.logger.Info("fetching EPSS scores from FIRST API",
		"cve_count", len(toFetch), "cached", len(result))

	for i := 0; i < len(toFetch); i += epssBatchSize {
		// SECURITY (security-reviewer M3): once the parent ctx is cancelled
		// we stop iterating immediately — without this, every subsequent
		// batch fires off another request that the dialer cancels and we
		// burn a Warn log line per batch instead of bailing out cleanly.
		if err := ctx.Err(); err != nil {
			c.logger.Warn("EPSS lookup cancelled mid-batch", "error", err,
				"completed_batches", i/epssBatchSize, "remaining", len(toFetch)-i)
			break
		}
		end := i + epssBatchSize
		if end > len(toFetch) {
			end = len(toFetch)
		}
		batch := toFetch[i:end]
		c.fetchAndCacheBatch(ctx, batch, result)
	}

	// Mark CVEs that didn't come back as "not found" so callers can
	// distinguish "no EPSS data" from "lookup failed".
	for _, cve := range toFetch {
		if _, ok := result[cve]; !ok {
			result[cve] = EPSSData{Found: false}
		}
	}
	return result
}

func (c *EPSSClient) fetchAndCacheBatch(ctx context.Context, cveIDs []string, out map[string]EPSSData) {
	q := url.Values{}
	q.Set("cve", strings.Join(cveIDs, ","))
	q.Set("envelope", "true")

	reqURL := c.apiBase + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		c.logger.Warn("EPSS request build failed", "error", err)
		return
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("EPSS HTTP error", "error", err)
		return
	}
	defer resp.Body.Close()

	if err := checkUpstreamStatus(resp); err != nil {
		c.logger.Warn("EPSS unexpected response", "error", err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, epssMaxBytes))
	if err != nil {
		c.logger.Warn("EPSS read body failed", "error", err)
		return
	}

	var api epssAPIResponse
	if err := json.Unmarshal(body, &api); err != nil {
		c.logger.Warn("EPSS parse failed", "error", err)
		return
	}

	for _, e := range api.Data {
		score, err := strconv.ParseFloat(e.EPSS, 32)
		if err != nil {
			continue
		}
		pct, err := strconv.ParseFloat(e.Percentile, 32)
		if err != nil {
			continue
		}
		out[e.CVE] = EPSSData{
			Score:      float32(score),
			Percentile: float32(pct),
			Found:      true,
		}
		_ = c.queries.UpsertEPSSEntry(ctx, db.UpsertEPSSEntryParams{
			CveID:      e.CVE,
			Score:      float32(score),
			Percentile: float32(pct),
		})
	}
}
