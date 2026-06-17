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
