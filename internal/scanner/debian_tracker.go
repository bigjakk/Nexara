package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	debianTrackerURL = "https://security-tracker.debian.org/tracker/data/json"
	cacheTTL         = 24 * time.Hour
	maxTrackerSize   = 100 * 1024 * 1024 // 100 MB limit for tracker data
)

// CVEInfo holds enriched CVE data from the Debian security tracker.
type CVEInfo struct {
	CVEID       string
	Severity    string
	CVSSScore   float32
	Description string
}

// CVEClient fetches and caches CVE data from the Debian Security Tracker.
type CVEClient struct {
	httpClient *http.Client
	queries    *db.Queries
	logger     *slog.Logger
	// trackerData caches the parsed Debian tracker JSON for the duration of a scan
	trackerData map[string]map[string]debianCVEEntry
}

// debianCVEEntry is the per-CVE entry inside a package's map.
// e.g. {"CVE-2024-1234": {"releases": {"bookworm": {"status": "resolved", "fixed_version": "1.2.3"}}, "urgency": "high"}}
type debianCVEEntry struct {
	Releases    map[string]debianRelease `json:"releases"`
	Description string                   `json:"description"`
	Urgency     string                   `json:"urgency"`
}

type debianRelease struct {
	Status       string `json:"status"`
	FixedVersion string `json:"fixed_version"`
	Urgency      string `json:"urgency"`
}

// NewCVEClient creates a new CVE data client.
func NewCVEClient(queries *db.Queries, logger *slog.Logger) *CVEClient {
	return &CVEClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		queries:    queries,
		logger:     logger,
	}
}

// LookupPackageCVEs returns known CVEs for the given package names.
// It fetches from the Debian Security Tracker and caches results in DB.
func (c *CVEClient) LookupPackageCVEs(ctx context.Context, packageNames []string) (map[string][]CVEInfo, error) {
	if err := c.ensureTrackerData(ctx); err != nil {
		return nil, fmt.Errorf("fetch tracker data: %w", err)
	}

	result := make(map[string][]CVEInfo)
	for _, pkg := range packageNames {
		cves, ok := c.trackerData[pkg]
		if !ok {
			continue
		}
		for cveID, entry := range cves {
			// Check if any supported release has an open (unfixed) vulnerability
			for _, rel := range entry.Releases {
				if rel.Status == "resolved" {
					continue
				}
				severity := normalizeSeverity(entry.Urgency)
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
				break // one entry per CVE is enough
			}
		}
	}

	return result, nil
}

// LookupPackageUpdates checks Proxmox apt update results for known CVEs.
// Updates from Proxmox represent packages that have fixes available.
func (c *CVEClient) LookupPackageUpdates(ctx context.Context, updates []AptUpdateInfo) ([]VulnResult, error) {
	if err := c.ensureTrackerData(ctx); err != nil {
		return nil, fmt.Errorf("fetch tracker data: %w", err)
	}

	var results []VulnResult
	seen := make(map[string]bool)

	for _, upd := range updates {
		pkgCVEs, ok := c.trackerData[upd.Package]
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
			seen[key] = true

			severity := normalizeSeverity(entry.Urgency)
			fixedVer := ""
			for _, rel := range entry.Releases {
				if rel.FixedVersion != "" {
					fixedVer = rel.FixedVersion
					break
				}
			}
			if fixedVer == "" {
				fixedVer = upd.NewVersion
			}

			desc := entry.Description
			if desc == "" {
				desc = upd.Title
			}

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

func (c *CVEClient) ensureTrackerData(ctx context.Context) error {
	if c.trackerData != nil {
		return nil
	}

	c.logger.Info("fetching Debian security tracker data")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, debianTrackerURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tracker returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTrackerSize))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var data map[string]map[string]debianCVEEntry
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("parse tracker JSON: %w", err)
	}

	c.trackerData = data
	c.logger.Info("loaded Debian security tracker", "packages", len(data))
	return nil
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
