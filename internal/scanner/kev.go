package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	kevFeedURL    = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	kevMaxBytes   = 50 * 1024 * 1024
	kevHTTPTimeout = 60 * time.Second
)

// KEVClient fetches CISA's Known Exploited Vulnerabilities catalog and caches
// it locally. The catalog is small (~1500 entries) so we mirror it whole.
type KEVClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger
}

// NewKEVClient creates a new KEV client.
func NewKEVClient(queries *db.Queries, logger *slog.Logger) *KEVClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &KEVClient{
		httpClient: &http.Client{Timeout: kevHTTPTimeout},
		queries:    queries,
		logger:     logger,
	}
}

// kevFeed is the top-level CISA KEV JSON shape.
type kevFeed struct {
	Title       string         `json:"title"`
	CatalogVersion string      `json:"catalogVersion"`
	DateReleased   string      `json:"dateReleased"`
	Count       int            `json:"count"`
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
	c.logger.Info("fetching CISA KEV catalog", "url", kevFeedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kevFeedURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Nexara/1.0 CVE-scanner")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("KEV feed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, kevMaxBytes))
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	var feed kevFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return 0, fmt.Errorf("parse KEV JSON: %w", err)
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

// IsKEV checks the local cache for a CVE. Errors propagate as "not in KEV"
// since this is a best-effort enrichment.
func (c *KEVClient) IsKEV(ctx context.Context, cveID string) bool {
	_, err := c.queries.GetKEVEntry(ctx, cveID)
	return err == nil
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
