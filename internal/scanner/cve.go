package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// Engine performs CVE scanning on Proxmox clusters.
//
// Per Finding A4 + A23 (Phase 3.8): the per-feed clients (Debian tracker, KEV,
// EPSS) are constructed *once* here and reused across every cluster scan.
// Before this refactor each `runScan` built a fresh CVEClient with an empty
// trackerData map, forcing a new ~80 MB Debian tracker download per cluster.
// Now the in-memory map is held under CVEClient.mu and revalidated against
// cacheTTL on each scan; a stale or empty map falls back to the persisted
// external_feed_cache row before reaching out to the network.
type Engine struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	logger        *slog.Logger
	notifier      *Notifier // optional; if nil, post-scan notifications are skipped

	httpClient *http.Client
	cveClient  *CVEClient
	kevClient  *KEVClient
	epssClient *EPSSClient
}

// NewEngine creates a new CVE scanner engine. registry may be nil — when it
// is, the post-scan notification step is a no-op (suitable for tests).
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger, registry *notifications.Registry) *Engine {
	if logger == nil {
		logger = slog.Default()
	}

	httpClient := newScannerHTTPClient(120 * time.Second)
	e := &Engine{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		httpClient:    httpClient,
		cveClient:     NewCVEClient(queries, httpClient, logger.With("component", "cve-client")),
		kevClient:     NewKEVClient(queries, httpClient, logger.With("component", "kev-client")),
		epssClient:    NewEPSSClient(queries, httpClient, logger.With("component", "epss-client")),
	}
	if registry != nil {
		n := NewNotifier(queries, registry, logger.With("component", "cve-notifier"))
		n.SetEncryptionKey(encryptionKey)
		e.notifier = n
	}
	return e
}

// KEVClient exposes the engine's shared KEV client so the scheduler's hourly
// refresh tick reuses the same connection pool / dial guard / signature hook
// rather than spinning up a fresh client on every tick.
func (e *Engine) KEVClient() *KEVClient { return e.kevClient }

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
		TotalNodes: safeconv.Int32(len(nodes)),
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
		TotalNodes: safeconv.Int32(len(nodes)),
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

	// CVE data + risk enrichment clients are held on the Engine and reused
	// across every cluster scan; their backing caches (tracker JSON in
	// memory, KEV in kev_cache, EPSS in epss_cache) survive the call.
	cveClient := e.cveClient
	kevClient := e.kevClient
	epssClient := e.epssClient

	// Per-scan changelog cache. Proxmox's apt/changelog endpoint covers
	// Proxmox-built packages (e.g. proxmox-kernel-*) that the Debian
	// Security Tracker doesn't index — we extract CVE refs directly from
	// the package's own changelog. Cached across all nodes in the cluster
	// since they all upgrade to the same versions.
	changelogs := newChangelogFetcher(client, e.logger.With("component", "changelog-fetcher"))

	var (
		totalVulns     int32
		criticalCount  int32
		highCount      int32
		mediumCount    int32
		lowCount       int32
		unknownCount   int32
		kevCount       int32
		actCount       int32
		attendCount    int32
		trackStarCount int32
		trackCount     int32
		scannedNodes   int32
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

		// Augment with CVEs extracted from each pending package's own
		// changelog. This catches CVEs that the Debian tracker doesn't
		// index under the apt-package's name — most importantly
		// proxmox-kernel-* and pve-kernel-* updates, where Debian tracks
		// upstream as "linux" but Proxmox ships a differently-named
		// package built from Ubuntu's kernel tree. Best-effort: a fetch
		// failure for one package does not abort the scan.
		vulns = augmentWithChangelogCVEs(ctx, vulns, aptUpdates, node.Name, changelogs, e.logger)

		// Enrich vulns with EPSS and KEV before bucketing. Both lookups
		// are best-effort: a failure leaves the vuln with no enrichment
		// rather than aborting the scan. KEV is cached locally (refreshed
		// hourly by the scheduler); EPSS is cached per-CVE with a 24h TTL
		// and lazy-fetched here for unknown CVEs.
		cveIDs := make([]string, 0, len(vulns))
		for _, v := range vulns {
			cveIDs = append(cveIDs, v.CVEID)
		}
		epssData := epssClient.LookupBatch(ctx, cveIDs)

		var nodeCritical, nodeHigh, nodeMedium, nodeLow, nodeUnknown, nodeKEV int32
		var nodeAct, nodeAttend, nodeTrackStar, nodeTrack int32
		for _, v := range vulns {
			cvssBase := severityToCVSSProxy(v.Severity)
			isKEV := kevClient.IsKEV(ctx, v.CVEID)
			epssScore := float32(0)
			epssPercentile := float32(0)
			epssValid := false
			if d, ok := epssData[v.CVEID]; ok && d.Found {
				epssScore = d.Score
				epssPercentile = d.Percentile
				epssValid = true
			}

			risk := computeRiskScore(cvssBase, epssScore, isKEV)
			riskSev := riskToSeverity(risk)
			ssvc := classifySSVC(cvssBase, epssScore, isKEV)

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
				RiskScore:      risk,
				RiskSeverity:   riskSev,
				Epss:           pgtype.Float4{Float32: epssScore, Valid: epssValid},
				EpssPercentile: pgtype.Float4{Float32: epssPercentile, Valid: epssValid},
				Kev:            isKEV,
				SsvcLabel:      ssvc,
			})
			if insertErr != nil {
				e.logger.Error("failed to insert vuln", "cve", v.CVEID, "error", insertErr)
				continue
			}

			switch riskSev {
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
			switch ssvc {
			case SSVCAct:
				nodeAct++
			case SSVCAttend:
				nodeAttend++
			case SSVCTrackStar:
				nodeTrackStar++
			default:
				nodeTrack++
			}
			if isKEV {
				nodeKEV++
			}
		}

		nodeVulns := safeconv.Int32(len(vulns))
		postureScore := ComputePostureScore(nodeCritical, nodeHigh, nodeMedium, nodeLow, nodeUnknown)

		now := time.Now()
		_ = e.queries.UpdateCVEScanNode(ctx, db.UpdateCVEScanNodeParams{
			ID:            scanNode.ID,
			Status:        "completed",
			PackagesTotal: safeconv.Int32(len(updates)),
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
		kevCount += nodeKEV
		actCount += nodeAct
		attendCount += nodeAttend
		trackStarCount += nodeTrackStar
		trackCount += nodeTrack
		scannedNodes++

		e.logger.Info("node scan complete",
			"node", node.Name,
			"vulns", nodeVulns,
			"kev", nodeKEV,
			"act", nodeAct,
			"attend", nodeAttend,
			"posture_score", postureScore,
		)
	}

	// Update scan summary
	_ = e.queries.UpdateCVEScanCounts(ctx, db.UpdateCVEScanCountsParams{
		ID:             scanID,
		ScannedNodes:   scannedNodes,
		TotalVulns:     totalVulns,
		CriticalCount:  criticalCount,
		HighCount:      highCount,
		MediumCount:    mediumCount,
		LowCount:       lowCount,
		UnknownCount:   unknownCount,
		KevCount:       kevCount,
		ActCount:       actCount,
		AttendCount:    attendCount,
		TrackStarCount: trackStarCount,
		TrackCount:     trackCount,
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
		"kev", kevCount,
		"act", actCount,
		"attend", attendCount,
		"track_star", trackStarCount,
		"track", trackCount,
	)

	if e.notifier != nil {
		e.notifier.MaybeNotify(ctx, clusterID, scanID)
	}

	return scanID, nil
}

// SetProxmoxCache attaches the shared per-server cache. Nil-safe.
func (e *Engine) SetProxmoxCache(cache *proxmox.ClientCache) {
	e.cache = cache
}

func (e *Engine) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	if e.cache != nil {
		client, err := e.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		e.logger.Warn("cve scanner: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}

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

// Phase 1 severity weights — calibrated so that 1 critical hits clearly,
// many lows accumulate into a visible but non-flooring deduction, and
// unknown counts the same as low (we know there's a CVE, we just don't
// have a triaged severity).
//
// Phase 2 will replace these constants with per-CVE risk scoring that
// multiplies CVSS by EPSS-or-KEV likelihood; until then, severity buckets
// are the only signal we have.
const (
	weightCritical = 25.0
	weightHigh     = 12.0
	weightMedium   = 5.0
	weightLow      = 2.0
	weightUnknown  = 2.0

	// Per-bucket deduction caps for the lower-severity buckets — no matter
	// how many CVEs accumulate, a single critical still hurts more.
	// Borrows the SSVC invariant that "Track" (large pile of lows) should
	// never escalate to "Act" (one exploited critical) by quantity alone.
	mediumBucketCap  = 22.0
	lowBucketCap     = 20.0
	unknownBucketCap = 20.0
)

// ComputePostureScore aggregates severity buckets into a 0–100 health score
// (higher = better). Within each bucket, count contributes logarithmically:
// log2(count+1). This pattern — per-bucket weight × sub-linear count factor —
// is adapted from Qualys TruRisk, which uses count^0.01 alongside per-CVE
// risk scores. Without those per-CVE scores yet, log2 gives the bucket
// weights enough room to drive the result while still letting count matter.
//
// Calibration anchors:
//   - 1 critical CVE       → ~25 deduction (notable hit)
//   - 100 low CVEs         → ~13 deduction (visible, not catastrophic)
//   - 1 critical + 100 low → ~38 deduction (critical still dominates)
//   - any volume of lows alone → never matches a single critical
func ComputePostureScore(critical, high, medium, low, unknown int32) float32 {
	deduction := bucketDeduction(weightCritical, critical, 0) +
		bucketDeduction(weightHigh, high, 0) +
		bucketDeduction(weightMedium, medium, mediumBucketCap) +
		bucketDeduction(weightLow, low, lowBucketCap) +
		bucketDeduction(weightUnknown, unknown, unknownBucketCap)
	score := 100 - deduction
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// bucketDeduction returns weight × log2(count+1), optionally capped.
// maxDeduction=0 means uncapped (used for high-severity buckets where
// stacking matters). Returns 0 for empty buckets.
func bucketDeduction(weight float32, count int32, maxDeduction float32) float32 {
	if count <= 0 {
		return 0
	}
	d := weight * float32(math.Log2(float64(count)+1))
	if maxDeduction > 0 && d > maxDeduction {
		return maxDeduction
	}
	return d
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
