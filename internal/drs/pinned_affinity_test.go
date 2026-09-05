package drs

import (
	"log/slog"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestPlanDoesNotMigratePinnedWorkload verifies the planner never selects a
// pinned workload (PCI/USB passthrough, etc.) as a migration candidate. Pinned
// VMs stay in the workload map so they still count toward node scores, but they
// must not be moved — the planner has to fall back to a movable VM.
//
// node1 (hot) holds a big pinned VM 100 and a smaller movable VM 101; node2 is
// empty. VM 100 has the larger impact, so it is the first migration candidate
// considered and relocating it WOULD improve balance — only the pinned-skip
// stops it. The correct outcome is to move the movable VM 101 instead.
func TestPlanDoesNotMigratePinnedWorkload(t *testing.T) {
	weights := DefaultWeights()

	nodeEntries := map[string]proxmox.NodeListEntry{
		"node1": {Node: "node1", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1", CPUUsage: 0.5, CPUs: 8, Mem: 8e9, MaxMem: 8e9, Pinned: true},
			{VMID: 101, Type: "qemu", Node: "node1", CPUUsage: 0.3, CPUs: 4, Mem: 3e9, MaxMem: 6e9},
		},
		"node2": {},
	}

	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], weights)
	}
	if CalculateImbalance(scores) <= 0.25 {
		t.Fatalf("test setup: expected an imbalanced cluster")
	}

	recs := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, slog.Default())

	if len(recs) == 0 {
		t.Fatal("expected the movable VM to be migrated to rebalance node1")
	}
	for _, r := range recs {
		if r.VMID == 100 {
			t.Errorf("pinned VM 100 must never be migrated, got %+v", r)
		}
	}
}

// TestPlanRespectsAntiAffinityWithPinnedPartner reproduces a real incident:
// vm 122 has PCI passthrough so it is pinned to pve-01, and vm 119 has a
// negative-affinity (anti-affinity) rule against it. pve-01 is the least-loaded
// node, so the planner is tempted to migrate 119 onto it — but pve-01 hosts
// 119's pinned anti-affinity partner, which Proxmox HA would reject (exit code
// 2). The planner must skip pve-01 and rebalance some other way.
//
// Regression guard: the bug was that pinned VMs were filtered out before
// planning, so the anti-affinity check couldn't see vm 122 on pve-01 and allowed
// the illegal move.
func TestPlanRespectsAntiAffinityWithPinnedPartner(t *testing.T) {
	weights := DefaultWeights()

	nodeEntries := map[string]proxmox.NodeListEntry{
		"pve-01": {Node: "pve-01", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"pve-02": {Node: "pve-02", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"pve-03": {Node: "pve-03", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		// pve-01: only the pinned passthrough VM — least loaded, so the most
		// tempting migration target.
		"pve-01": {
			{VMID: 122, Type: "qemu", Node: "pve-01", CPUUsage: 0.1, CPUs: 2, Mem: 1e9, MaxMem: 4e9, Pinned: true},
		},
		// pve-02: the hot node. 119 is the biggest workload (tried first), with a
		// movable bystander to absorb the rebalance.
		"pve-02": {
			{VMID: 119, Type: "qemu", Node: "pve-02", CPUUsage: 0.6, CPUs: 8, Mem: 11e9, MaxMem: 12e9},
			{VMID: 200, Type: "qemu", Node: "pve-02", CPUUsage: 0.4, CPUs: 4, Mem: 4e9, MaxMem: 8e9},
		},
		"pve-03": {
			{VMID: 203, Type: "qemu", Node: "pve-03", CPUUsage: 0.3, CPUs: 4, Mem: 5e9, MaxMem: 8e9},
		},
	}

	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], weights)
	}
	if CalculateImbalance(scores) <= 0.25 {
		t.Fatalf("test setup: expected an imbalanced cluster")
	}

	rules := []Rule{
		{Type: RuleTypeAntiAffinity, VMIDs: []int{119, 122}, Enabled: true},
	}

	recs := Plan(scores, nodeWorkloads, nodeEntries, rules, weights, 0.25, slog.Default())

	// The planner should still find a legal way to rebalance the hot node.
	if len(recs) == 0 {
		t.Fatal("expected at least one legal rebalancing recommendation")
	}
	for _, r := range recs {
		if r.VMID == 122 {
			t.Errorf("pinned VM 122 must never be migrated, got %+v", r)
		}
		if r.VMID == 119 && r.TargetNode == "pve-01" {
			t.Errorf("anti-affinity violated: vm 119 recommended onto pve-01 where its pinned partner 122 lives: %+v", r)
		}
	}
}

// TestCheckMigrationReason confirms a blocked move reports an actionable reason
// (surfaced in planner logs), and an allowed move reports none.
func TestCheckMigrationReason(t *testing.T) {
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 100, Type: "qemu", Node: "node1"}},
		"node2": {{VMID: 200, Type: "qemu", Node: "node2"}},
	}
	rules := []Rule{{Type: RuleTypeAntiAffinity, VMIDs: []int{100, 200}, Enabled: true}}

	if allowed, reason := checkMigration(
		Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node2"},
		nodeWorkloads, rules,
	); allowed || reason == "" {
		t.Errorf("expected blocked move with a reason, got allowed=%v reason=%q", allowed, reason)
	}

	if allowed, reason := checkMigration(
		Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node3"},
		nodeWorkloads, rules,
	); !allowed || reason != "" {
		t.Errorf("expected allowed move with no reason, got allowed=%v reason=%q", allowed, reason)
	}
}
