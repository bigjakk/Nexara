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
)

// snapshotSyncTimeout bounds one cluster's snapshot inventory pass. The pass
// issues one snapshot listing per guest, sequentially, so the bound is
// generous for large clusters while still guaranteeing a wedged connection
// can't pin the goroutine across passes.
const snapshotSyncTimeout = 4 * time.Minute

// SyncAllGuestSnapshots runs one guest snapshot inventory pass over every
// active cluster. Proxmox has no bulk snapshot endpoint — the pass fans out
// one GET /nodes/{node}/{qemu|lxc}/{vmid}/snapshot per guest — which is why
// this runs on its own SNAPSHOT_SYNC_INTERVAL cadence instead of riding the
// 30s metrics tick.
//
// Re-entrancy-guarded like SyncAllResources: if a pass is still running when
// the next tick fires, the tick is skipped rather than queued.
func (s *Syncer) SyncAllGuestSnapshots(ctx context.Context) {
	if !s.snapshotSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.snapshotSyncInFlight.Store(false)

	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Warn("snapshot sync: failed to list active clusters", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster db.Cluster) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("snapshot sync panicked",
						"cluster_id", cluster.ID, "panic", r)
				}
			}()
			if err := s.syncClusterGuestSnapshots(ctx, cluster); err != nil {
				// Debug, not Warn: the slow loop already reports cluster
				// reachability problems loudly.
				s.logger.Debug("snapshot sync failed",
					"cluster_id", cluster.ID, "error", err)
			}
		}(cluster)
	}
	wg.Wait()
}

// syncClusterGuestSnapshots converges one cluster's guest_snapshots rows with
// what Proxmox reports. Prune safety rules, in order of blast radius:
//
//   - client build failure → return before touching anything;
//   - guest on a non-online node, or whose listing errored, or whose listing
//     came back raw-empty (anomalous — PVE always includes the synthetic
//     "current" entry) → that guest's rows are left untouched;
//   - a guest listed successfully → its rows are exact-diffed against the
//     listing (the endpoint reads the guest's config, so like
//     /cluster/resources it is config-authoritative and needs no grace);
//   - rows whose vmid is no longer in the vms inventory at all → deleted.
//     vmids come from the grace-protected vms table, not from a live listing,
//     so a transient Proxmox blip cannot cascade into snapshot loss.
func (s *Syncer) syncClusterGuestSnapshots(ctx context.Context, cluster db.Cluster) error {
	// The per-guest listing fan-out runs under a per-cluster budget; the
	// final vanished-guest cleanup deliberately does NOT (see below), so it
	// still runs when a huge/slow cluster blows the listing budget.
	syncCtx, cancel := context.WithTimeout(ctx, snapshotSyncTimeout)
	defer cancel()

	client, err := s.proxmoxClient(syncCtx, cluster)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}

	nodes, err := s.queries.ListNodesByCluster(syncCtx, cluster.ID)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	nodeByID := make(map[uuid.UUID]db.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}

	guests, err := s.queries.ListVMsByCluster(syncCtx, cluster.ID)
	if err != nil {
		return fmt.Errorf("list guests: %w", err)
	}

	existingRows, err := s.queries.ListGuestSnapshotsByCluster(syncCtx, cluster.ID)
	if err != nil {
		return fmt.Errorf("list existing snapshots: %w", err)
	}
	existing := make(map[int32]map[string]db.GuestSnapshot)
	for _, row := range existingRows {
		if existing[row.Vmid] == nil {
			existing[row.Vmid] = make(map[string]db.GuestSnapshot)
		}
		existing[row.Vmid][row.Name] = row
	}

	changed := false
	listed := 0
	allVmids := make([]int32, 0, len(guests))
	for _, guest := range guests {
		allVmids = append(allVmids, guest.Vmid)

		node, known := nodeByID[guest.NodeID]
		if !known || node.Status != "online" {
			// Dead or not-yet-registered node: listing every guest on it
			// would just serially time out. Keep the rows we have.
			continue
		}

		// Templates are listed too: PVE refuses to snapshot them, but a
		// listing is self-correcting if rows pre-date the conversion,
		// whereas skipping would strand those rows forever.
		var snaps []proxmox.Snapshot
		var listErr error
		switch guest.Type {
		case "qemu":
			snaps, listErr = client.ListVMSnapshots(syncCtx, node.Name, int(guest.Vmid))
		case "lxc":
			snaps, listErr = client.ListCTSnapshots(syncCtx, node.Name, int(guest.Vmid))
		default:
			continue
		}
		if listErr != nil {
			s.logger.Debug("snapshot sync: listing failed",
				"cluster_id", cluster.ID, "vmid", guest.Vmid, "error", listErr)
			continue
		}
		if len(snaps) == 0 {
			// PVE always returns at least the synthetic "current" entry; a
			// raw-empty 200 is anomalous, not "no snapshots". Don't prune on it.
			s.logger.Debug("snapshot sync: raw-empty listing, skipping guest",
				"cluster_id", cluster.ID, "vmid", guest.Vmid)
			continue
		}

		listed++
		current := make(map[string]bool, len(snaps))
		names := make([]string, 0, len(snaps))
		for _, snap := range snaps {
			if snap.Name == "current" {
				// Synthetic "you are here" entry, not a snapshot. The per-VM
				// API handlers filter it the same way; it must never persist.
				continue
			}
			current[snap.Name] = true
			names = append(names, snap.Name)

			vmstate := snap.VMState != 0
			prev, seen := existing[guest.Vmid][snap.Name]
			differs := !seen || prev.Description != snap.Description || prev.Parent != snap.Parent ||
				prev.Vmstate != vmstate || prev.SnapTime != snap.SnapTime ||
				prev.Node != node.Name || prev.GuestType != guest.Type
			// Always upsert, even when unchanged: last_seen_at doubles as the
			// page's "last synced" signal and must advance on quiet passes.
			if _, upErr := s.queries.UpsertGuestSnapshot(syncCtx, db.UpsertGuestSnapshotParams{
				ClusterID:   cluster.ID,
				Vmid:        guest.Vmid,
				Name:        snap.Name,
				GuestType:   guest.Type,
				Node:        node.Name,
				Description: snap.Description,
				Parent:      snap.Parent,
				Vmstate:     vmstate,
				SnapTime:    snap.SnapTime,
			}); upErr != nil {
				s.logger.Warn("snapshot sync: upsert failed",
					"cluster_id", cluster.ID, "vmid", guest.Vmid,
					"snapshot", snap.Name, "error", upErr)
			} else if differs {
				changed = true
			}
		}

		stale := false
		for name := range existing[guest.Vmid] {
			if !current[name] {
				stale = true
				break
			}
		}
		if stale {
			removed, delErr := s.queries.DeleteGuestSnapshotsNotInSet(syncCtx, db.DeleteGuestSnapshotsNotInSetParams{
				ClusterID: cluster.ID,
				Vmid:      guest.Vmid,
				Names:     names,
			})
			if delErr != nil {
				s.logger.Warn("snapshot sync: stale snapshot delete failed",
					"cluster_id", cluster.ID, "vmid", guest.Vmid, "error", delErr)
			} else if removed > 0 {
				changed = true
			}
		}
	}

	if syncCtx.Err() != nil {
		// The listing budget expired mid-pass. Guests are walked in vmid
		// order, so without this signal the same tail would silently stay
		// stale on every pass. Raise SNAPSHOT_SYNC_INTERVAL pressure relief:
		// the interval gates passes, not this per-cluster budget.
		s.logger.Warn("snapshot sync: pass truncated by timeout",
			"cluster_id", cluster.ID, "listed", listed, "guests", len(guests))
	}

	// The vanished-guest cleanup runs on a fresh, short budget derived from
	// the OUTER ctx: the vmid list is DB-derived and complete even when the
	// listing loop was truncated, and skipping cleanup whenever a pass ran
	// long would let snapshots of destroyed guests linger indefinitely.
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCleanup()

	// Re-read the inventory and union it with the pass-start list: a guest
	// created (and resynced via the API) while a long pass was running must
	// not have its fresh rows deleted by the stale pass-start snapshot.
	vmidSet := make(map[int32]bool, len(allVmids))
	for _, vmid := range allVmids {
		vmidSet[vmid] = true
	}
	if latest, listErr := s.queries.ListVMsByCluster(cleanupCtx, cluster.ID); listErr == nil {
		for _, guest := range latest {
			if !vmidSet[guest.Vmid] {
				vmidSet[guest.Vmid] = true
				allVmids = append(allVmids, guest.Vmid)
			}
		}
	} else {
		s.logger.Warn("snapshot sync: inventory re-read for cleanup failed, using pass-start list",
			"cluster_id", cluster.ID, "error", listErr)
	}

	removed, err := s.queries.DeleteGuestSnapshotsForVanishedGuests(cleanupCtx, db.DeleteGuestSnapshotsForVanishedGuestsParams{
		ClusterID: cluster.ID,
		Vmids:     allVmids,
	})
	if err != nil {
		s.logger.Warn("snapshot sync: vanished guest cleanup failed",
			"cluster_id", cluster.ID, "error", err)
	} else if removed > 0 {
		s.logger.Info("snapshot sync: removed snapshots of vanished guests",
			"cluster_id", cluster.ID, "count", removed)
		changed = true
	}

	if changed && s.eventPub != nil {
		s.eventPub.ClusterEvent(cleanupCtx, cluster.ID.String(),
			events.KindSnapshotChange, "cluster", cluster.ID.String(), "sync_complete")
	}
	return nil
}
