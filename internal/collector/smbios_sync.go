package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// smbiosSyncTimeout bounds one cluster's SMBIOS pass. The pass issues at most
// one config read per QEMU guest, and only for guests whose cached value is
// missing or past its refresh window, so a converged cluster does almost
// nothing and this bound only ever bites on a first pass.
const smbiosSyncTimeout = 4 * time.Minute

// smbiosRefreshInterval is how long a cached uuid is trusted before it is read
// again.
//
// The plan's original shape was "fetch once, never refresh" — the uuid is
// stable for a VM's life, so a re-read looks like pure waste. It is not. A
// guest rebuilt IN PLACE keeps its vmid and gets a NEW smbios1 uuid, and a
// cache that never refreshes would go on reporting the old uuid forever. The
// old uuid is exactly what Veeam's stale backup object still carries, so the
// replacement machine would be reported as protected by a backup of the
// machine it replaced — the precise failure that correlating on the uuid
// instead of the name exists to prevent.
//
// A day bounds that window at a cost of one config read per guest per day,
// two orders of magnitude below the per-guest-per-pass fan-out the snapshot
// inventory already runs.
const smbiosRefreshInterval = 24 * time.Hour

// SyncAllGuestSmbios caches the smbios1 uuid of every QEMU guest on a cluster
// an operator has mapped a Veeam platform to.
//
// The uuid is the deterministic join key between Nexara's inventory and a
// Veeam backup object, and it is NOT in /cluster/resources — it only appears
// in GET /nodes/{node}/qemu/{vmid}/config, which is why this is a per-guest
// pass on its own cadence rather than a field on the resource sync.
//
// Cost is gated on the mapping: an install with no Veeam server, or one whose
// platform an operator has not mapped to a cluster yet, does no work at all
// here. That is also why a freshly created mapping takes two ticks to show
// results — this pass caches the uuids, and the Veeam inventory pass that
// follows is what correlates on them.
//
// Re-entrancy-guarded like SyncAllGuestSnapshots: a tick arriving while a pass
// is still running is skipped, not queued.
func (s *Syncer) SyncAllGuestSmbios(ctx context.Context) {
	if !s.smbiosSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.smbiosSyncInFlight.Store(false)

	clusters, err := s.queries.ListClustersWithVeeamPlatform(ctx)
	if err != nil {
		s.logger.Warn("smbios sync: failed to list Veeam-linked clusters", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster db.Cluster) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("smbios sync panicked",
						"cluster_id", cluster.ID, "panic", r)
				}
			}()
			if err := s.syncClusterGuestSmbios(ctx, cluster); err != nil {
				// Debug, not Warn: the slow loop already reports cluster
				// reachability problems loudly, and this pass adds nothing
				// to that diagnosis.
				s.logger.Debug("smbios sync failed",
					"cluster_id", cluster.ID, "error", err)
			}
		}(cluster)
	}
	wg.Wait()
}

// syncClusterGuestSmbios converges one cluster's guest_smbios rows.
//
// Prune safety, in order of blast radius:
//
//   - client build failure → return before touching anything;
//   - a guest on a non-online node, or whose config read failed, or whose
//     config carries no smbios1 line → its cached row is left exactly as it
//     was. A Proxmox blip must never look like "this guest lost its identity",
//     because a cleared row silently demotes correlation to the name tier;
//   - rows whose vmid is no longer in the vms inventory at all → deleted. The
//     vmids come from the grace-protected vms table rather than a live
//     listing, so a transient failure cannot cascade into cache loss.
func (s *Syncer) syncClusterGuestSmbios(ctx context.Context, cluster db.Cluster) error {
	syncCtx, cancel := context.WithTimeout(ctx, smbiosSyncTimeout)
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

	cachedRows, err := s.queries.ListGuestSmbiosByCluster(syncCtx, cluster.ID)
	if err != nil {
		return fmt.Errorf("list cached smbios uuids: %w", err)
	}
	cached := make(map[int32]db.GuestSmbios, len(cachedRows))
	for _, row := range cachedRows {
		cached[row.Vmid] = row
	}

	// Built in full BEFORE any config is read, and from every guest rather
	// than only the QEMU ones. It is the list the prune below keeps rows for,
	// so it must describe the whole inventory no matter how far the read loop
	// actually gets — collecting it inside that loop would mean a pass that
	// ran out of budget handed the prune a truncated inventory and deleted
	// the cached uuid of every guest it had not reached yet.
	allVmids := make([]int32, 0, len(guests))
	for _, guest := range guests {
		allVmids = append(allVmids, guest.Vmid)
	}

	fresh := time.Now().Add(-smbiosRefreshInterval)

	for _, guest := range guests {
		// LXC containers have no SMBIOS table, and Veeam cannot back them up
		// at all. A missing row for one is the correct steady state.
		if guest.Type != "qemu" {
			continue
		}
		if row, ok := cached[guest.Vmid]; ok && row.LastSeenAt.After(fresh) {
			continue
		}
		node, known := nodeByID[guest.NodeID]
		if !known || node.Status != "online" {
			continue
		}
		if syncCtx.Err() != nil {
			// Out of budget. Stop rather than issuing reads that will all
			// fail identically; the next pass resumes with whatever is still
			// uncached.
			break
		}

		config, cfgErr := client.GetVMConfig(syncCtx, node.Name, int(guest.Vmid))
		if cfgErr != nil {
			s.logger.Debug("smbios sync: config read failed",
				"cluster_id", cluster.ID, "vmid", guest.Vmid, "error", cfgErr)
			continue
		}
		// An empty result — no smbios1 line, or one carrying settings but no
		// uuid= — is STORED, not skipped. Two reasons, both load-bearing:
		//
		//   * without it the guest is re-read on every single pass forever,
		//     since "not cached" is the only thing that schedules a read;
		//   * it is the difference between "this guest has no SMBIOS uuid"
		//     and "this guest has not been looked at yet". Correlation's name
		//     tier needs the first and must refuse the second, or a cluster
		//     mapped seconds ago would name-match every backup object it has,
		//     orphans included, and report a rebuilt host as protected by a
		//     backup of the machine it replaced.
		smbiosUUID := smbiosUUIDFromConfig(config)

		if err := s.queries.UpsertGuestSmbios(syncCtx, db.UpsertGuestSmbiosParams{
			ClusterID:  cluster.ID,
			Vmid:       guest.Vmid,
			SmbiosUuid: smbiosUUID,
		}); err != nil {
			s.logger.Warn("smbios sync: upsert failed",
				"cluster_id", cluster.ID, "vmid", guest.Vmid, "error", err)
		}
	}

	// Deliberately NOT under syncCtx: a cluster large enough to blow the read
	// budget above still needs its vanished guests cleaned up, and this is one
	// statement against the local database.
	if _, err := s.queries.DeleteGuestSmbiosForVanishedGuests(ctx, db.DeleteGuestSmbiosForVanishedGuestsParams{
		ClusterID: cluster.ID,
		Vmids:     allVmids,
	}); err != nil {
		s.logger.Warn("smbios sync: pruning vanished guests failed",
			"cluster_id", cluster.ID, "error", err)
	}
	return nil
}

// smbiosUUIDFromConfig extracts the uuid from a QEMU guest's smbios1 setting.
//
// Proxmox stores it as a property string — "uuid=<uuid>,manufacturer=<b64>,…"
// — so the uuid is one comma-separated key among several and is not always
// first. Anything unparseable returns empty, which correlation reads as "no
// deterministic key for this guest" rather than as a match against a garbage
// value.
func smbiosUUIDFromConfig(config map[string]any) string {
	raw, ok := config["smbios1"].(string)
	if !ok {
		return ""
	}
	for _, part := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(part, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "uuid") {
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		// Validated, not merely non-empty: a malformed value stored here
		// would sit in the cache looking like a legitimate key and quietly
		// never match anything.
		if _, err := uuid.Parse(value); err != nil {
			return ""
		}
		return value
	}
	return ""
}
