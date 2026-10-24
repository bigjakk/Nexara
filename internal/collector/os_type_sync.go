package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// OSTypeSyncInterval is how often SyncAllGuestOSTypes runs. OS detection has a
// loop of its own because every guest-agent call to a VM whose config enables
// the agent but whose agent does not answer costs a hard 3 s (PVE pings the
// agent first, guest-ping timeout 3), and asking each guest on the metrics pass
// stretched that pass, and so the sample spacing, by 3 s per such VM.
const OSTypeSyncInterval = time.Minute

// osTypeSyncTimeout bounds one cluster's pass: a config read per guest, plus an
// agent call per running VM that can take 3 s, one guest at a time.
const osTypeSyncTimeout = 4 * time.Minute

// SyncAllGuestOSTypes refreshes the detected OS of every guest on every active
// cluster (see refreshOSType), each cluster in parallel. The guests come from
// the vms inventory the metrics pass keeps, so this adds no listing calls.
//
// Re-entrancy-guarded like SyncAllGuestSmbios: a tick arriving while a pass is
// still running is skipped, not queued.
func (s *Syncer) SyncAllGuestOSTypes(ctx context.Context) {
	if !s.osTypeSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.osTypeSyncInFlight.Store(false)

	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Warn("os type sync: failed to list clusters", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster db.Cluster) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("os type sync panicked",
						"cluster_id", cluster.ID, "panic", r)
				}
			}()
			if err := s.syncClusterGuestOSTypes(ctx, cluster); err != nil {
				// Debug, not Warn: the metrics pass already reports cluster
				// reachability problems loudly.
				s.logger.Debug("os type sync failed",
					"cluster_id", cluster.ID, "error", err)
			}
		}(cluster)
	}
	wg.Wait()
}

// syncClusterGuestOSTypes refreshes one cluster's guests, skipping those on a
// node that is not online, and tells the frontend which guests' OS changed so
// a new icon shows without waiting for an unrelated refetch.
func (s *Syncer) syncClusterGuestOSTypes(ctx context.Context, cluster db.Cluster) error {
	syncCtx, cancel := context.WithTimeout(ctx, osTypeSyncTimeout)
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

	for i, guest := range guests {
		node, known := nodeByID[guest.NodeID]
		if !known || node.Status != "online" {
			continue
		}
		if syncCtx.Err() != nil {
			// Out of budget; the next pass starts over from the first guest.
			s.logger.Debug("os type sync: out of budget",
				"cluster_id", cluster.ID, "guests_left", len(guests)-i)
			break
		}
		if s.refreshOSType(syncCtx, client, guest, node.Name, int(guest.Vmid), guest.Type, guest.Status) && s.eventPub != nil {
			resourceType := "vm"
			if guest.Type == "lxc" {
				resourceType = "container"
			}
			s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
				events.KindVMStateChange, resourceType, guest.ID.String(), "ostype_sync")
		}
	}
	return nil
}

// refreshOSType detects the current OS for a guest and persists it. Tracks
// two fields:
//
//   - ostype: the "best detected" value — guest-agent ID when available
//     (e.g. "ubuntu", "home-assistant"), else the Proxmox config setting.
//   - config_ostype: the Proxmox config setting (e.g. "l26", "win11") used
//     as a UI fallback when the guest-agent value isn't a known distro and
//     we still want to render a family icon.
//
// It reports whether it stored a new value in either field. Best-effort — a
// failure is logged but never propagated, since this is a cosmetic OS
// detection path.
func (s *Syncer) refreshOSType(ctx context.Context, client ProxmoxClient, dbVM db.Vm, nodeName string, vmid int, kind, status string) bool {
	// Always fetch the Proxmox config first — its ostype value is the
	// fallback we display when the guest-agent reports something the UI
	// doesn't recognize.
	var (
		config proxmox.VMConfig
		err    error
	)
	switch kind {
	case "qemu":
		config, err = client.GetVMConfig(ctx, nodeName, vmid)
	case "lxc":
		config, err = client.GetCTConfig(ctx, nodeName, vmid)
	default:
		return false
	}
	if err != nil {
		s.logger.Debug("ostype fetch failed",
			"vmid", vmid, "node", nodeName, "kind", kind, "error", err)
		return false
	}
	rawConfigOS, _ := config["ostype"].(string)
	configOSType := strings.ToLower(rawConfigOS)

	changed := false
	if configOSType != "" && configOSType != dbVM.ConfigOstype {
		if err := s.queries.SetVMConfigOSType(ctx, db.SetVMConfigOSTypeParams{ID: dbVM.ID, ConfigOstype: configOSType}); err != nil {
			s.logger.Debug("config_ostype persist failed",
				"vmid", vmid, "kind", kind, "error", err)
		} else {
			changed = true
		}
	}

	// Prefer the guest-agent reported OS when QEMU and running — it's more
	// specific (distro name vs. generic "l26") and handles the case where
	// the Proxmox config says "other" but the guest is really Linux.
	detected := ""
	if kind == "qemu" && status == "running" {
		if info, err := client.GetGuestAgentOSInfo(ctx, nodeName, vmid); err == nil && info != nil {
			if info.ID != "" {
				detected = strings.ToLower(info.ID)
			} else if info.Name != "" {
				detected = strings.ToLower(info.Name)
			}
		}
	}
	if detected == "" {
		detected = configOSType
	}

	if detected == "" || detected == dbVM.Ostype {
		return changed
	}
	if err := s.queries.SetVMOSType(ctx, db.SetVMOSTypeParams{ID: dbVM.ID, Ostype: detected}); err != nil {
		s.logger.Debug("ostype persist failed",
			"vmid", vmid, "kind", kind, "error", err)
		return changed
	}
	return true
}
