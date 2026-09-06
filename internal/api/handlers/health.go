package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Health-issue severity levels (most-severe first when ranked).
const (
	healthSevErr  = "err"
	healthSevWarn = "warn"
)

// healthIssueResponse is one infrastructure-health problem attached to a cluster
// in the clusters API. It is intentionally generic so new signals are a single
// aggregator rule with no frontend change.
type healthIssueResponse struct {
	Type     string `json:"type"`     // e.g. "node_offline", "disk_failed", "ceph"
	Severity string `json:"severity"` // "err" | "warn"
	Scope    string `json:"scope"`    // "cluster" | "node" | "storage" | "guest"
	Target   string `json:"target"`   // affected resource name ("" for cluster-scoped)
	Summary  string `json:"summary"`  // short category label
	Detail   string `json:"detail"`   // human-readable reason
}

// endpointFingerprintStale reports whether a (cluster, node) pair shows the
// cluster's pinned certificate going stale: the cluster's api_url addresses
// this node's pveproxy directly, and the certificate that node now serves
// differs from what is pinned.
//
// Both guards exist to avoid crying wolf, which matters more here than
// catching every case — an alarm the operator cannot clear is worse than none.
// nodes.ssl_fingerprint only ever describes what pveproxy serves, so it is
// evidence about the configured endpoint in exactly one situation: the URL
// names a member and talks to port 8006.
//   - A VIP, load balancer or DNS name matches no member address.
//   - A reverse proxy terminating TLS on the node's own address (https://node/
//     or :443 in front of :8006) does match the address, and would otherwise
//     have the operator's proxy certificate compared against pveproxy's —
//     firing permanently, and unfixable, since re-pinning the fingerprint this
//     reports would pin a certificate that endpoint never presents.
//
// The comparison itself is proxmox.EndpointCertificateChanged, shared with the
// client cache's endpoint selection so the banner and the code that actually
// routes around a bad endpoint can never disagree about what "changed" means.
// It normalises both sides, matching the TLS verifier; in practice both
// columns hold Proxmox's uppercase colon-separated form, so that is defence
// against a hand-entered pin rather than a routine difference.
func endpointFingerprintStale(apiURL, pinned, nodeAddress, nodeFingerprint string) bool {
	if nodeAddress == "" || nodeFingerprint == "" || pinned == "" {
		return false
	}
	if !proxmox.APIURLIsDirectToPVEProxy(apiURL) {
		return false
	}
	if !strings.EqualFold(nodeAddress, proxmox.APIURLHost(apiURL)) {
		return false
	}
	return proxmox.EndpointCertificateChanged(pinned, nodeFingerprint)
}

// buildAllClusterIssues runs the health-aggregator queries once and returns the
// problems grouped by cluster id. Each query failure is skipped so one broken
// signal never blanks the whole indicator.
func buildAllClusterIssues(ctx context.Context, q *db.Queries) map[uuid.UUID][]healthIssueResponse {
	issues := make(map[uuid.UUID][]healthIssueResponse)
	add := func(cid uuid.UUID, iss healthIssueResponse) {
		issues[cid] = append(issues[cid], iss)
	}

	// Ceph — folded in as issue type "ceph" (one per health check).
	if rows, err := q.GetLatestCephHealthPerCluster(ctx); err == nil {
		for _, r := range rows {
			for _, it := range cephIssues(r.HealthStatus, r.HealthChecks) {
				add(r.ClusterID, it)
			}
		}
	}

	// TLS — the certificate the configured endpoint presents no longer matches
	// the fingerprint pinned for the cluster.
	//
	// This is worth its own signal because nothing else reports it. Every live
	// Proxmox call for the cluster fails the handshake, but the collector
	// fails over to another member (failoverCluster) and keeps inventory and
	// metrics flowing, so the cluster looks healthy everywhere else while the
	// UI's live tabs return 502. The collector's own tls_fingerprint_mismatch
	// audit entry only fires when the whole sync fails, which failover
	// prevents.
	//
	// Compared against the per-node fingerprint the collector re-reads each
	// sync, so this reflects the certificate actually being served — not a
	// second stale copy.
	//
	// A cluster whose node addresses were never learned reports nothing, which
	// covers the single-node install. That is the case this signal is least
	// needed for: with no member to fail over to the sync genuinely fails, so
	// the collector's existing tls_fingerprint_mismatch audit entry does fire.
	if rows, err := q.ListClusterPinnedFingerprints(ctx); err == nil {
		for _, r := range rows {
			if !endpointFingerprintStale(r.ApiUrl, r.TlsFingerprint, r.NodeAddress, r.SslFingerprint) {
				continue
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "tls_fingerprint_changed", Severity: healthSevErr, Scope: "cluster",
				Summary: "TLS certificate changed",
				// The fingerprint is shown so it can be checked against the node
				// itself (`pvenode cert info` on its console), not so it can be
				// pasted back in: re-pinning goes through fetch-fingerprint,
				// which performs its own live handshake.
				Detail: fmt.Sprintf(
					"%s is presenting a different certificate than the one pinned for this cluster, "+
						"so live Proxmox operations fail. This is expected after a Proxmox upgrade or a "+
						"certificate renewal. It now presents %s — confirm that on the node before "+
						"accepting it.",
					r.NodeName, r.SslFingerprint),
			})
		}
	}

	// Nodes — offline or HA-fenced.
	if rows, err := q.ListNodeHealthProblems(ctx); err == nil {
		for _, r := range rows {
			if r.Status == "offline" {
				add(r.ClusterID, healthIssueResponse{
					Type: "node_offline", Severity: healthSevErr, Scope: "node",
					Target: r.Name, Summary: "Node offline", Detail: r.Name + " is offline",
				})
			}
			if r.HaState == "fence" {
				add(r.ClusterID, healthIssueResponse{
					Type: "node_fenced", Severity: healthSevErr, Scope: "node",
					Target: r.Name, Summary: "Node fenced", Detail: "HA is fencing " + r.Name,
				})
			}
		}
	}

	// Disks — SMART not healthy.
	if rows, err := q.ListFailedDisks(ctx); err == nil {
		for _, r := range rows {
			detail := fmt.Sprintf("%s on %s: %s", r.DevPath, r.NodeName, r.Health)
			if r.Model != "" {
				detail = fmt.Sprintf("%s (%s) on %s: %s", r.DevPath, r.Model, r.NodeName, r.Health)
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "disk_failed", Severity: healthSevErr, Scope: "node",
				Target: r.NodeName, Summary: "Disk SMART failure", Detail: detail,
			})
		}
	}

	// Storage — inactive (enabled but unreachable).
	if rows, err := q.ListInactiveStorage(ctx); err == nil {
		for _, r := range rows {
			add(r.ClusterID, healthIssueResponse{
				Type: "storage_inactive", Severity: healthSevWarn, Scope: "storage",
				Target: r.Storage, Summary: "Storage inactive",
				Detail: r.Storage + " is enabled but not active",
			})
		}
	}

	// Storage — near full.
	if rows, err := q.ListStorageNearFull(ctx); err == nil {
		for _, r := range rows {
			pct := 0
			if r.Total > 0 {
				pct = int(r.Used * 100 / r.Total)
			}
			sev := healthSevWarn
			if pct >= 95 {
				sev = healthSevErr
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "storage_full", Severity: sev, Scope: "storage",
				Target: r.Storage, Summary: "Storage near full",
				Detail: fmt.Sprintf("%s is %d%% used", r.Storage, pct),
			})
		}
	}

	// Tasks — recent failures, grouped by type so we surface a count, not spam.
	if rows, err := q.ListRecentFailedTasksByType(ctx); err == nil {
		for _, r := range rows {
			noun := "task"
			if r.Cnt != 1 {
				noun = "tasks"
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "task_failed", Severity: healthSevWarn, Scope: "cluster", Target: "",
				Summary: "Failed tasks",
				Detail:  fmt.Sprintf("%d failed %s %s in the last 24h", r.Cnt, r.TaskType, noun),
			})
		}
	}

	// Guests — HA resource in error state.
	if rows, err := q.ListHAErrorGuests(ctx); err == nil {
		for _, r := range rows {
			add(r.ClusterID, healthIssueResponse{
				Type: "ha_error", Severity: healthSevErr, Scope: "guest",
				Target: r.Name, Summary: "HA resource error",
				Detail: r.Name + " HA state is error",
			})
		}
	}

	// Cluster — lost quorum.
	if rows, err := q.ListNonQuorateClusters(ctx); err == nil {
		for _, cid := range rows {
			add(cid, healthIssueResponse{
				Type: "quorum_lost", Severity: healthSevErr, Scope: "cluster", Target: "",
				Summary: "Quorum lost", Detail: "Cluster has lost corosync quorum",
			})
		}
	}

	// Nodes — root filesystem near full.
	if rows, err := q.ListRootfsFullNodes(ctx); err == nil {
		for _, r := range rows {
			pct := 0
			if r.DiskTotal > 0 {
				pct = int(r.RootfsUsed * 100 / r.DiskTotal)
			}
			sev := healthSevWarn
			if pct >= 95 {
				sev = healthSevErr
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "node_disk_full", Severity: sev, Scope: "node",
				Target: r.Name, Summary: "Root disk near full",
				Detail: fmt.Sprintf("%s root filesystem is %d%% used", r.Name, pct),
			})
		}
	}

	// Guests — paused by storage I/O error.
	if rows, err := q.ListIOErrorGuests(ctx); err == nil {
		for _, r := range rows {
			add(r.ClusterID, healthIssueResponse{
				Type: "guest_io_error", Severity: healthSevErr, Scope: "guest",
				Target: r.Name, Summary: "Guest I/O error",
				Detail: r.Name + " is paused by a storage I/O error",
			})
		}
	}

	// Replication — failing jobs.
	if rows, err := q.ListFailedReplication(ctx); err == nil {
		for _, r := range rows {
			detail := fmt.Sprintf("Guest %d → %s is failing", r.Guest, r.Target)
			if r.Error != "" {
				detail = fmt.Sprintf("Guest %d → %s: %s", r.Guest, r.Target, r.Error)
			} else if r.FailCount > 0 {
				detail = fmt.Sprintf("Guest %d → %s: %d failed attempts", r.Guest, r.Target, r.FailCount)
			}
			add(r.ClusterID, healthIssueResponse{
				Type: "replication_failed", Severity: healthSevErr, Scope: "guest",
				Target: fmt.Sprintf("guest %d", r.Guest), Summary: "Replication failing", Detail: detail,
			})
		}
	}

	for cid := range issues {
		sortIssues(issues[cid])
	}
	return issues
}

func healthSevRank(s string) int {
	switch s {
	case healthSevErr:
		return 0
	case healthSevWarn:
		return 1
	default:
		return 2
	}
}

// sortIssues orders errors first, then alphabetically by summary, then target.
func sortIssues(items []healthIssueResponse) {
	sort.SliceStable(items, func(i, j int) bool {
		if ri, rj := healthSevRank(items[i].Severity), healthSevRank(items[j].Severity); ri != rj {
			return ri < rj
		}
		if items[i].Summary != items[j].Summary {
			return items[i].Summary < items[j].Summary
		}
		return items[i].Target < items[j].Target
	})
}

// cephIssues converts persisted Ceph status + checks JSON into health issues.
func cephIssues(status string, checksJSON []byte) []healthIssueResponse {
	if status == "" || status == "HEALTH_OK" || status == "HEALTH_UNKNOWN" {
		return nil
	}
	statusSev := healthSevWarn
	if status == "HEALTH_ERR" {
		statusSev = healthSevErr
	}

	var checks []proxmox.CephHealthCheckItem
	if len(checksJSON) > 0 {
		_ = json.Unmarshal(checksJSON, &checks)
	}
	if len(checks) == 0 {
		return []healthIssueResponse{{
			Type: "ceph", Severity: statusSev, Scope: "cluster", Target: "",
			Summary: "Ceph storage", Detail: "Ceph health: " + cephStatusLabel(status),
		}}
	}

	out := make([]healthIssueResponse, 0, len(checks))
	for _, c := range checks {
		sev := healthSevWarn
		if strings.EqualFold(c.Severity, "HEALTH_ERR") {
			sev = healthSevErr
		}
		out = append(out, healthIssueResponse{
			Type: "ceph", Severity: sev, Scope: "cluster", Target: "",
			Summary: "Ceph storage", Detail: c.Message,
		})
	}
	return out
}

func cephStatusLabel(status string) string {
	switch status {
	case "HEALTH_WARN":
		return "Warning"
	case "HEALTH_ERR":
		return "Error"
	default:
		return status
	}
}
