package drs

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// Guests DRS leaves where they are for now, because Proxmox would refuse to
// migrate them or just has. They are PINNED rather than filtered out, for the
// reason Evaluate gives: they still occupy their node and still count for
// affinity rules.

const (
	migrationBackoffBase = 15 * time.Minute
	migrationBackoffMax  = 6 * time.Hour
)

// migrationBackoff is how long DRS leaves a guest alone after this many of its
// migrations have failed in a row: doubling, so a guest that can never move costs
// a few failed attempts a day (and their audit rows) rather than one a minute.
func migrationBackoff(failures int) time.Duration {
	wait := migrationBackoffBase
	for i := 1; i < failures && wait < migrationBackoffMax; i++ {
		wait *= 2
	}
	return min(wait, migrationBackoffMax)
}

// pinBackedOffGuests pins the guests still inside their back-off. Evaluate stops
// when the failures cannot be read: planning without them would retry a failing
// guest, and a database that cannot answer this cannot answer ListDRSRules either.
func (e *Engine) pinBackedOffGuests(ctx context.Context, clusterID uuid.UUID, nodeWorkloads map[string][]Workload, now time.Time) error {
	failures, err := e.evalReads.ListDRSMigrationFailures(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("list DRS migration failures: %w", err)
	}

	retryAt := make(map[int]time.Time, len(failures))
	for _, f := range failures {
		if at := f.LastFailedAt.Add(migrationBackoff(int(f.Failures))); now.Before(at) {
			retryAt[int(f.VmID)] = at
		}
	}

	for node, workloads := range nodeWorkloads {
		for i, w := range workloads {
			at, backedOff := retryAt[w.VMID]
			if !backedOff || w.Pinned {
				continue
			}
			e.logger.Info("DRS leaving a guest in place after failed migrations",
				"cluster_id", clusterID, "vmid", w.VMID, "node", node, "retry_at", at)
			workloads[i].Pinned = true
		}
	}
	return nil
}

// pinBusyGuests pins the guests Proxmox cannot migrate right now: those with a
// config lock (backup, snapshot, migrate...) and those with a task running.
//
// The lock alone is not enough. A VM's disk move sets no lock; its worker holds
// the VM's config FILE lock for the whole copy, which only shows as a running
// task. A migration started meanwhile waits 10 s for that file and ends with
// "migration aborted".
//
// A node whose tasks cannot be listed keeps its guests in place for this pass:
// nothing says they are free, and the next evaluation looks again.
func (e *Engine) pinBusyGuests(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID, nodes map[string]proxmox.NodeListEntry, nodeWorkloads map[string][]Workload) {
	unlisted := make(map[string]bool)
	// Keyed by VMID across every node: at a live migration's cutover the guest is
	// listed on the target while its task still runs on the source.
	running := make(map[int]string)
	for node := range nodes {
		tasks, err := client.GetActiveNodeTasks(ctx, node)
		if err != nil {
			unlisted[node] = true
			e.logger.Warn("DRS leaving this node's guests in place: its running tasks could not be listed",
				"cluster_id", clusterID, "node", node, "error", err)
			continue
		}
		for _, t := range tasks {
			if vmid, ok := runningGuestTask(t); ok {
				running[vmid] = t.Type
			}
		}
	}

	for node, workloads := range nodeWorkloads {
		for i, w := range workloads {
			task := running[w.VMID]
			if w.Pinned || (w.Lock == "" && task == "" && !unlisted[node]) {
				continue
			}
			e.logger.Info("DRS leaving a busy guest in place",
				"cluster_id", clusterID, "vmid", w.VMID, "node", node, "lock", w.Lock, "task", task)
			workloads[i].Pinned = true
		}
	}
}

// runningGuestTask returns the VMID of the guest a running task works on. Open
// consoles do not count: they carry the VMID but take no lock, Proxmox migrates
// the guest regardless, and one left open in a browser tab would otherwise keep
// the guest from ever being balanced. A task whose id is not a number has no guest.
func runningGuestTask(t proxmox.NodeTask) (int, bool) {
	if t.EndTime > 0 || t.Type == "vncproxy" || t.Type == "spiceproxy" {
		return 0, false
	}
	vmid, err := strconv.Atoi(t.ID)
	if err != nil {
		return 0, false
	}
	return vmid, true
}
