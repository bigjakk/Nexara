package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// fastSyncTimeout bounds one cluster's resource pass — a single HTTP call
// plus a handful of small queries. Generous; the point is that a wedged
// connection can't pin the goroutine across many ticks.
const fastSyncTimeout = 30 * time.Second

// SyncAllResources runs one lightweight inventory pass over every active
// cluster: a single GET /cluster/resources per cluster, diffed against the
// DB, with the same events the full sync publishes. It is the fast half of
// the fast/slow collector split — guest existence, placement, status, and
// identity converge within one fast tick (seconds), while the heavy
// per-node enrichment (metrics, hardware, ostype, Ceph, tasks) stays on the
// slow SyncAll cadence.
//
// Re-entrancy-guarded: if a pass is still running when the next tick fires,
// the tick is skipped rather than queued.
func (s *Syncer) SyncAllResources(ctx context.Context) {
	if !s.fastSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.fastSyncInFlight.Store(false)

	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Warn("fast sync: failed to list active clusters", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster db.Cluster) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("fast sync panicked",
						"cluster_id", cluster.ID, "panic", r)
				}
			}()
			syncCtx, cancel := context.WithTimeout(ctx, fastSyncTimeout)
			defer cancel()
			if err := s.syncClusterResources(syncCtx, cluster); err != nil {
				// Debug, not Warn: this fires every few seconds and the slow
				// loop already reports cluster reachability problems loudly.
				s.logger.Debug("fast sync failed",
					"cluster_id", cluster.ID, "error", err)
			}
		}(cluster)
	}
	wg.Wait()
}

// syncClusterResources diffs one cluster's config-level inventory against
// the DB and converges it: guests are upserted only when a visible field
// changed, guests absent from the cluster configuration are deleted
// immediately (no grace — /cluster/resources has no migration-cutover blind
// spot), and node status flips are applied. Publishes the same event kinds
// the full sync does, so the frontend needs nothing new.
func (s *Syncer) syncClusterResources(ctx context.Context, cluster db.Cluster) error {
	client, err := s.proxmoxClient(ctx, cluster)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}

	resources, err := client.GetClusterResources(ctx, "")
	if err != nil {
		return fmt.Errorf("get cluster resources: %w", err)
	}

	guests := make(map[int32]proxmox.ClusterResource)
	nodeStatus := make(map[string]string)
	for _, r := range resources {
		switch r.Type {
		case "qemu", "lxc":
			if r.VMID > 0 {
				guests[safeconv.Int32(r.VMID)] = r
			}
		case "node":
			nodeStatus[r.Node] = r.Status
		}
	}
	// A resources payload that lists no nodes is malformed or truncated — a
	// healthy cluster always reports its members. Bail before treating it as
	// authoritative; acting on it could wipe the guest inventory.
	if len(nodeStatus) == 0 {
		return fmt.Errorf("cluster resources returned no node entries")
	}

	inventoryChanged := false

	// Node status flips. Rowcount-guarded UPDATE: unchanged nodes cost no
	// write and report no change.
	for name, status := range nodeStatus {
		changed, updErr := s.queries.UpdateNodeStatusFast(ctx, db.UpdateNodeStatusFastParams{
			ClusterID: cluster.ID,
			Name:      name,
			Status:    status,
		})
		if updErr != nil {
			s.logger.Warn("fast sync: node status update failed",
				"cluster_id", cluster.ID, "node", name, "error", updErr)
			continue
		}
		if changed > 0 {
			s.logger.Info("node status changed (fast sync)",
				"cluster_id", cluster.ID, "node", name, "new_status", status)
			inventoryChanged = true
		}
	}

	// Guest diff. Upsert only rows with a visible change so a quiet tick
	// costs three SELECTs and nothing else.
	known := make(map[int32]db.ListVMStatusesByClusterRow)
	if rows, listErr := s.queries.ListVMStatusesByCluster(ctx, cluster.ID); listErr == nil {
		for _, r := range rows {
			known[r.Vmid] = r
		}
	} else {
		return fmt.Errorf("snapshot vms: %w", listErr)
	}

	nodeIDByName := make(map[string]uuid.UUID)
	if nodes, listErr := s.queries.ListNodesByCluster(ctx, cluster.ID); listErr == nil {
		for _, n := range nodes {
			nodeIDByName[n.Name] = n.ID
		}
	} else {
		return fmt.Errorf("snapshot nodes: %w", listErr)
	}

	for vmid, r := range guests {
		nodeID, nodeKnown := nodeIDByName[r.Node]
		if !nodeKnown {
			// Brand-new node the slow loop hasn't registered yet; it owns
			// node-row creation (full hardware detail). Skip until then.
			continue
		}
		old, exists := known[vmid]
		isTemplate := r.Template > 0
		if exists &&
			old.Status == r.Status &&
			old.NodeID == nodeID &&
			old.Name == r.Name &&
			old.Template == isTemplate &&
			old.Pool == r.Pool &&
			old.HaState == r.HAState &&
			old.Tags == r.Tags {
			continue
		}
		if _, upErr := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: cluster.ID,
			NodeID:    nodeID,
			Vmid:      vmid,
			Name:      r.Name,
			Type:      r.Type,
			Status:    r.Status,
			CpuCount:  safeconv.Int32(r.MaxCPU),
			MemTotal:  r.MaxMem,
			DiskTotal: r.MaxDisk,
			Uptime:    r.Uptime,
			Template:  isTemplate,
			Tags:      r.Tags,
			HaState:   r.HAState,
			Pool:      r.Pool,
		}); upErr != nil {
			s.logger.Warn("fast sync: guest upsert failed",
				"cluster_id", cluster.ID, "vmid", vmid, "error", upErr)
			continue
		}
		inventoryChanged = true
		if exists && old.Status != r.Status && s.eventPub != nil {
			s.logger.Info("VM status changed (fast sync)",
				"vmid", vmid,
				"old_status", old.Status,
				"new_status", r.Status,
				"cluster_id", cluster.ID,
			)
			s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
				events.KindVMStateChange, "vm", old.ID.String(), "status_sync")
		}
	}

	// Deletions: anything the cluster configuration no longer lists is gone
	// for real — destroyed guests leave the tree within one fast tick
	// instead of after the slow loop's grace window.
	vmids := make([]int32, 0, len(guests))
	for vmid := range guests {
		vmids = append(vmids, vmid)
	}
	removed, delErr := s.queries.DeleteVMsAbsentFromCluster(ctx, db.DeleteVMsAbsentFromClusterParams{
		ClusterID: cluster.ID,
		Vmids:     vmids,
	})
	if delErr != nil {
		s.logger.Warn("fast sync: stale guest delete failed",
			"cluster_id", cluster.ID, "error", delErr)
	} else if removed > 0 {
		s.logger.Info("guests removed from cluster config (fast sync)",
			"cluster_id", cluster.ID, "count", removed)
		inventoryChanged = true
	}

	if inventoryChanged && s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
			events.KindInventoryChange, "cluster", cluster.ID.String(), "resource_sync")
	}
	return nil
}
