package rolling

import (
	"context"
	"fmt"
	"strings"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// nodeRAMOverhead is reserved for the OS/hypervisor on each node (2 GiB).
// Proxmox typically reserves ~1-2 GiB for the kernel and core services.
const nodeRAMOverhead int64 = 2 * 1024 * 1024 * 1024

// guestResource tracks a guest's resource allocation for capacity checking.
type guestResource struct {
	VMID        int
	Name        string
	Type        string // "qemu" or "lxc"
	MaxMem      int64  // configured maximum memory in bytes
	CPUs        int    // configured vCPUs
	Passthrough bool   // PCI/USB passthrough — won't be migrated
}

// nodeCapacity tracks a node's total resources and running guest allocations.
type nodeCapacity struct {
	Name          string
	MaxMem        int64 // total physical RAM
	MaxCPU        int   // total logical CPUs
	Guests        []guestResource
	MigratableMem int64 // sum of MaxMem for non-passthrough running guests
}

// capacityTarget represents a potential migration target with its free resources.
type capacityTarget struct {
	name    string
	freeMem int64
}

// AnalyzeCapacity checks if remaining cluster nodes can absorb the workloads
// when each batch of nodes is drained during a rolling update. It simulates
// draining nodes in order (respecting parallelism) and verifies that the
// remaining nodes have enough free memory to accept migrated guests.
//
// Returns capacity conflicts using the HAConflict type so they integrate
// seamlessly with the existing pre-flight report and frontend rendering.
func AnalyzeCapacity(
	ctx context.Context,
	client *proxmox.Client,
	nodesToDrain []string,
	parallelism int32,
) ([]HAConflict, bool, error) {
	var conflicts []HAConflict
	hasErrors := false

	// Get all cluster nodes.
	clusterNodes, err := client.GetNodes(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get cluster nodes: %w", err)
	}

	// Build capacity map for each online node.
	nodeMap := make(map[string]*nodeCapacity, len(clusterNodes))
	for _, cn := range clusterNodes {
		if cn.Status != "online" {
			continue
		}
		nc := &nodeCapacity{
			Name:   cn.Node,
			MaxMem: cn.MaxMem,
			MaxCPU: cn.MaxCPU,
		}

		// Get VMs on this node.
		vms, vmErr := client.GetVMs(ctx, cn.Node)
		if vmErr == nil {
			for _, vm := range vms {
				if vm.Template == 1 || (vm.Status != "running" && vm.Status != "paused") {
					continue
				}
				gr := guestResource{
					VMID:   vm.VMID,
					Name:   vm.Name,
					Type:   "qemu",
					MaxMem: vm.MaxMem,
					CPUs:   vm.CPUs,
				}
				config, cfgErr := client.GetVMConfig(ctx, cn.Node, vm.VMID)
				if cfgErr == nil && hasPassthrough(config) {
					gr.Passthrough = true
				}
				nc.Guests = append(nc.Guests, gr)
				if !gr.Passthrough {
					nc.MigratableMem += gr.MaxMem
				}
			}
		}

		// Get containers on this node.
		cts, ctErr := client.GetContainers(ctx, cn.Node)
		if ctErr == nil {
			for _, ct := range cts {
				if ct.Template == 1 || ct.Status != "running" {
					continue
				}
				gr := guestResource{
					VMID:   ct.VMID,
					Name:   ct.Name,
					Type:   "lxc",
					MaxMem: ct.MaxMem,
					CPUs:   ct.CPUs,
				}
				nc.Guests = append(nc.Guests, gr)
				nc.MigratableMem += gr.MaxMem
			}
		}

		nodeMap[cn.Node] = nc
	}

	// Process each batch of nodes (determined by parallelism).
	// With parallelism=P, up to P nodes are drained simultaneously.
	// Remaining nodes must absorb all migratable guests from the batch.
	for batchStart := 0; batchStart < len(nodesToDrain); batchStart += int(parallelism) {
		batchEnd := batchStart + int(parallelism)
		if batchEnd > len(nodesToDrain) {
			batchEnd = len(nodesToDrain)
		}
		batch := nodesToDrain[batchStart:batchEnd]

		batchSet := make(map[string]bool, len(batch))
		for _, n := range batch {
			batchSet[n] = true
		}

		// Sum migratable guest memory from this batch.
		var batchMem int64
		var batchGuests []guestResource
		for _, nodeName := range batch {
			nc, ok := nodeMap[nodeName]
			if !ok {
				continue
			}
			for _, g := range nc.Guests {
				if g.Passthrough {
					continue
				}
				batchMem += g.MaxMem
				batchGuests = append(batchGuests, g)
			}
		}

		if batchMem == 0 {
			continue
		}

		// Calculate free capacity on remaining nodes.
		// Free memory = total RAM - OS overhead - sum of all guest allocations.
		var totalFreeMem int64
		var targets []capacityTarget

		for name, nc := range nodeMap {
			if batchSet[name] {
				continue
			}
			var allocatedMem int64
			for _, g := range nc.Guests {
				allocatedMem += g.MaxMem
			}
			freeMem := nc.MaxMem - nodeRAMOverhead - allocatedMem
			if freeMem < 0 {
				freeMem = 0
			}
			totalFreeMem += freeMem
			targets = append(targets, capacityTarget{name: name, freeMem: freeMem})
		}

		// Check 1: total memory across remaining nodes.
		if batchMem > totalFreeMem {
			batchLabel := strings.Join(batch, ", ")
			deficit := batchMem - totalFreeMem
			conflicts = append(conflicts, HAConflict{
				Source:   "capacity",
				RuleName: "memory",
				Type:     "insufficient_memory",
				Severity: "error",
				Message: fmt.Sprintf(
					"Insufficient memory to drain node(s) %s: guests require %s but remaining nodes only have %s free (short by %s; %s reserved per node for OS)",
					batchLabel,
					formatBytes(batchMem),
					formatBytes(totalFreeMem),
					formatBytes(deficit),
					formatBytes(nodeRAMOverhead),
				),
				Node: batch[0],
			})
			hasErrors = true
		}

		// Check 2: each guest individually — does it fit on any single target?
		// A guest can't be split across nodes, so even if total free > total needed,
		// a large guest might not fit on any individual node.
		for _, g := range batchGuests {
			fitsAnywhere := false
			var maxTargetFree int64
			for _, t := range targets {
				if t.freeMem > maxTargetFree {
					maxTargetFree = t.freeMem
				}
				if t.freeMem >= g.MaxMem {
					fitsAnywhere = true
					break
				}
			}
			if !fitsAnywhere && len(targets) > 0 {
				label := guestTypeLabel(g.Type)
				gNode := findGuestNodeInBatch(g.VMID, batch, nodeMap)
				conflicts = append(conflicts, HAConflict{
					Source:   "capacity",
					RuleName: "memory",
					Type:     "guest_too_large",
					Severity: "error",
					VMID:     g.VMID,
					VMName:   g.Name,
					Message: fmt.Sprintf(
						"%s %d (%s) requires %s memory but no remaining node has enough free capacity (largest available: %s)",
						label, g.VMID, g.Name,
						formatBytes(g.MaxMem),
						formatBytes(maxTargetFree),
					),
					Node: gNode,
				})
				hasErrors = true
			}
		}
	}

	return conflicts, hasErrors, nil
}

// findGuestNodeInBatch finds which batch node a guest belongs to.
func findGuestNodeInBatch(vmid int, batch []string, nodeMap map[string]*nodeCapacity) string {
	for _, n := range batch {
		nc, ok := nodeMap[n]
		if !ok {
			continue
		}
		for _, g := range nc.Guests {
			if g.VMID == vmid {
				return n
			}
		}
	}
	return ""
}

func formatBytes(b int64) string {
	const gib = 1024 * 1024 * 1024
	if b >= gib {
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	}
	const mib = 1024 * 1024
	return fmt.Sprintf("%.0f MiB", float64(b)/float64(mib))
}

func guestTypeLabel(t string) string {
	if t == "lxc" {
		return "CT"
	}
	return "VM"
}
