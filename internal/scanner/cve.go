package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// safeInt32 converts an int to int32 with bounds clamping (gosec G115).
func safeInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v) //nolint:gosec // bounds checked above
}

// Engine performs CVE scanning on Proxmox clusters.
type Engine struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
}

// NewEngine creates a new CVE scanner engine.
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
	}
}

// ScanCluster performs a full CVE scan of all nodes in a cluster.
// It creates its own scan record and returns the scan ID.
func (e *Engine) ScanCluster(ctx context.Context, clusterID uuid.UUID) (uuid.UUID, error) {
	nodes, err := e.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return uuid.Nil, fmt.Errorf("no nodes found for cluster %s", clusterID)
	}

	scan, err := e.queries.InsertCVEScan(ctx, db.InsertCVEScanParams{
		ClusterID:  clusterID,
		Status:     "running",
		TotalNodes: safeInt32(len(nodes)),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create scan: %w", err)
	}

	return e.runScan(ctx, clusterID, scan.ID, nodes)
}

// RunScanWithID performs a CVE scan using a pre-existing scan record.
// The scan record should already exist with status "running".
func (e *Engine) RunScanWithID(ctx context.Context, clusterID, scanID uuid.UUID) (uuid.UUID, error) {
	nodes, err := e.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		e.failScan(ctx, scanID, fmt.Sprintf("list nodes: %v", err))
		return scanID, fmt.Errorf("list nodes: %w", err)
	}

	// Backfill total_nodes — the handler creates the scan record before it
	// knows how many nodes the cluster has.
	_ = e.queries.UpdateCVEScanTotalNodes(ctx, db.UpdateCVEScanTotalNodesParams{
		ID:         scanID,
		TotalNodes: safeInt32(len(nodes)),
	})

	_ = e.queries.UpdateCVEScanCounts(ctx, db.UpdateCVEScanCountsParams{
		ID:           scanID,
		ScannedNodes: 0,
		TotalVulns:   0,
		CriticalCount: 0,
		HighCount:     0,
		MediumCount:   0,
		LowCount:      0,
	})

	// Mark as running
	_ = e.queries.UpdateCVEScanStatus(ctx, db.UpdateCVEScanStatusParams{
		ID:     scanID,
		Status: "running",
	})

	return e.runScan(ctx, clusterID, scanID, nodes)
}

func (e *Engine) runScan(ctx context.Context, clusterID, scanID uuid.UUID, nodes []db.Node) (uuid.UUID, error) {
	// Create Proxmox client
	client, err := e.createClient(ctx, clusterID)
	if err != nil {
		e.failScan(ctx, scanID, fmt.Sprintf("create proxmox client: %v", err))
		return scanID, nil
	}

	// Initialize CVE data client
	cveClient := NewCVEClient(e.queries, e.logger.With("component", "cve-client"))

	var (
		totalVulns    int32
		criticalCount int32
		highCount     int32
		mediumCount   int32
		lowCount      int32
		unknownCount  int32
		scannedNodes  int32
	)

	for _, node := range nodes {
		// Create scan node record
		scanNode, err := e.queries.InsertCVEScanNode(ctx, db.InsertCVEScanNodeParams{
			ScanID:   scanID,
			NodeID:   node.ID,
			NodeName: node.Name,
			Status:   "scanning",
		})
		if err != nil {
			e.logger.Error("failed to create scan node record", "node", node.Name, "error", err)
			continue
		}

		// Detect Debian release for release-aware CVE filtering.
		release := ""
		if status, err := client.GetNodeStatus(ctx, node.Name); err == nil {
			release = DebianReleaseFromPVEVersion(status.PVEVersion)
		} else {
			e.logger.Warn("failed to get node status; release-aware CVE filtering disabled",
				"node", node.Name, "error", err)
		}

		// Get pending updates from Proxmox
		updates, err := client.GetNodeAptUpdates(ctx, node.Name)
		if err != nil {
			e.logger.Error("failed to get apt updates", "node", node.Name, "error", err)
			e.failScanNode(ctx, scanNode.ID, fmt.Sprintf("get apt updates: %v", err))
			scannedNodes++
			continue
		}

		// Convert Proxmox updates to our format
		aptUpdates := make([]AptUpdateInfo, 0, len(updates))
		for _, u := range updates {
			isSecurityUpdate := strings.Contains(u.Origin, "security") ||
				strings.Contains(u.Origin, "Debian-Security") ||
				strings.EqualFold(u.Priority, "required")
			aptUpdates = append(aptUpdates, AptUpdateInfo{
				Package:          u.Package,
				Title:            u.Title,
				OldVersion:       u.OldVersion,
				NewVersion:       u.NewVersion,
				Origin:           u.Origin,
				IsSecurityUpdate: isSecurityUpdate,
			})
		}

		// Match against Debian tracker CVEs
		vulns, err := cveClient.LookupPackageUpdates(ctx, aptUpdates, release)
		if err != nil {
			e.logger.Warn("CVE lookup failed, using update data only", "node", node.Name, "error", err)
			// Fall back to treating all security updates as vulns
			vulns = securityUpdatesToVulns(aptUpdates)
		}

		// Store vulnerabilities
		var nodeCritical, nodeHigh, nodeMedium, nodeLow, nodeUnknown int32
		for _, v := range vulns {
			_, insertErr := e.queries.InsertCVEScanVuln(ctx, db.InsertCVEScanVulnParams{
				ScanID:         scanID,
				ScanNodeID:     scanNode.ID,
				CveID:          v.CVEID,
				PackageName:    v.PackageName,
				CurrentVersion: v.CurrentVersion,
				FixedVersion:   pgtype.Text{String: v.FixedVersion, Valid: v.FixedVersion != ""},
				Severity:       v.Severity,
				CvssScore:      pgtype.Float4{Float32: v.CVSSScore, Valid: true},
				Description:    v.Description,
			})
			if insertErr != nil {
				e.logger.Error("failed to insert vuln", "cve", v.CVEID, "error", insertErr)
				continue
			}

			switch v.Severity {
			case "critical":
				nodeCritical++
			case "high":
				nodeHigh++
			case "medium":
				nodeMedium++
			case "low":
				nodeLow++
			default:
				nodeUnknown++
			}
		}

		nodeVulns := safeInt32(len(vulns))
		postureScore := computePostureScore(nodeCritical, nodeHigh, nodeMedium, nodeLow, nodeUnknown)

		now := time.Now()
		_ = e.queries.UpdateCVEScanNode(ctx, db.UpdateCVEScanNodeParams{
			ID:            scanNode.ID,
			Status:        "completed",
			PackagesTotal: safeInt32(len(updates)),
			VulnsFound:    nodeVulns,
			PostureScore:  pgtype.Float4{Float32: postureScore, Valid: true},
			ScannedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		})

		totalVulns += nodeVulns
		criticalCount += nodeCritical
		highCount += nodeHigh
		mediumCount += nodeMedium
		lowCount += nodeLow
		unknownCount += nodeUnknown
		scannedNodes++

		e.logger.Info("node scan complete",
			"node", node.Name,
			"vulns", nodeVulns,
			"posture_score", postureScore,
		)
	}

	// Update scan summary
	_ = e.queries.UpdateCVEScanCounts(ctx, db.UpdateCVEScanCountsParams{
		ID:            scanID,
		ScannedNodes:  scannedNodes,
		TotalVulns:    totalVulns,
		CriticalCount: criticalCount,
		HighCount:     highCount,
		MediumCount:   mediumCount,
		LowCount:      lowCount,
	})

	now := time.Now()
	_ = e.queries.UpdateCVEScanStatus(ctx, db.UpdateCVEScanStatusParams{
		ID:          scanID,
		Status:      "completed",
		CompletedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})

	e.logger.Info("cluster CVE scan complete",
		"cluster_id", clusterID,
		"scan_id", scanID,
		"total_vulns", totalVulns,
		"critical", criticalCount,
		"high", highCount,
		"medium", mediumCount,
		"low", lowCount,
		"unknown", unknownCount,
	)

	return scanID, nil
}

func (e *Engine) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := e.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, e.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        120 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return client, nil
}

func (e *Engine) failScan(ctx context.Context, scanID uuid.UUID, errMsg string) {
	now := time.Now()
	_ = e.queries.UpdateCVEScanStatus(ctx, db.UpdateCVEScanStatusParams{
		ID:           scanID,
		Status:       "failed",
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		CompletedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
}

func (e *Engine) failScanNode(ctx context.Context, nodeID uuid.UUID, errMsg string) {
	_ = e.queries.UpdateCVEScanNode(ctx, db.UpdateCVEScanNodeParams{
		ID:           nodeID,
		Status:       "failed",
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
	})
}

// computePostureScore weights vulnerabilities by severity. Unknown-severity
// CVEs (Debian tracker entries with no urgency assigned) are weighted as low —
// we know there's an unfixed CVE applicable to the user's release, we just
// don't have a triaged urgency for it.
func computePostureScore(critical, high, medium, low, unknown int32) float32 {
	score := float32(100) - float32(critical*25+high*10+medium*3+low*1+unknown*1)
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func securityUpdatesToVulns(updates []AptUpdateInfo) []VulnResult {
	results := make([]VulnResult, 0, len(updates))
	for _, u := range updates {
		if !u.IsSecurityUpdate {
			continue
		}
		results = append(results, VulnResult{
			CVEID:          "SEC-" + u.Package,
			PackageName:    u.Package,
			CurrentVersion: u.OldVersion,
			FixedVersion:   u.NewVersion,
			Severity:       "medium",
			CVSSScore:      5.0,
			Description:    u.Title,
		})
	}
	return results
}
