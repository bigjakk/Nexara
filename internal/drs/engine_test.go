package drs

import (
	"testing"

	"github.com/proxdash/proxdash/internal/proxmox"
)

func TestScoreNode(t *testing.T) {
	weights := DefaultWeights()

	tests := []struct {
		name      string
		node      proxmox.NodeListEntry
		workloads []Workload
		wantMin   float64
		wantMax   float64
	}{
		{
			name: "idle node with no workloads",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				MaxCPU: 8,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: nil,
			wantMin:   0.0,
			wantMax:   0.0,
		},
		{
			name: "node with heavy workload",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				MaxCPU: 8,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: []Workload{
				{VMID: 100, CPUUsage: 1.0, CPUs: 8, Mem: 16 * 1024 * 1024 * 1024, MaxMem: 16 * 1024 * 1024 * 1024},
			},
			wantMin: 0.95, // cpu 0.5 + mem 0.5
			wantMax: 1.05,
		},
		{
			name: "node with 50% workload",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				MaxCPU: 8,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: []Workload{
				{VMID: 101, CPUUsage: 0.5, CPUs: 8, Mem: 8 * 1024 * 1024 * 1024, MaxMem: 8 * 1024 * 1024 * 1024},
			},
			wantMin: 0.45,
			wantMax: 0.55,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := ScoreNode(tt.node, tt.workloads, weights)
			if score.Score < tt.wantMin || score.Score > tt.wantMax {
				t.Errorf("ScoreNode() = %f, want between %f and %f", score.Score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCalculateImbalance(t *testing.T) {
	tests := []struct {
		name    string
		scores  map[string]NodeScore
		wantMin float64
		wantMax float64
	}{
		{
			name:    "single node",
			scores:  map[string]NodeScore{"a": {Score: 0.5}},
			wantMin: 0.0,
			wantMax: 0.0,
		},
		{
			name: "perfectly balanced",
			scores: map[string]NodeScore{
				"a": {Score: 0.5},
				"b": {Score: 0.5},
				"c": {Score: 0.5},
			},
			wantMin: 0.0,
			wantMax: 0.0,
		},
		{
			name: "slightly imbalanced",
			scores: map[string]NodeScore{
				"a": {Score: 0.4},
				"b": {Score: 0.5},
				"c": {Score: 0.6},
			},
			wantMin: 0.1,
			wantMax: 0.25,
		},
		{
			name: "heavily imbalanced",
			scores: map[string]NodeScore{
				"a": {Score: 0.1},
				"b": {Score: 0.9},
			},
			wantMin: 0.5,
			wantMax: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imbalance := CalculateImbalance(tt.scores)
			if imbalance < tt.wantMin || imbalance > tt.wantMax {
				t.Errorf("CalculateImbalance() = %f, want between %f and %f", imbalance, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestPlanProducesMigrations(t *testing.T) {
	weights := DefaultWeights()

	nodeEntries := map[string]proxmox.NodeListEntry{
		"node1": {Node: "node1", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1", CPUUsage: 0.5, CPUs: 4, Mem: 4e9, MaxMem: 4e9},
			{VMID: 101, Type: "qemu", Node: "node1", CPUUsage: 0.3, CPUs: 2, Mem: 2e9, MaxMem: 2e9},
		},
		"node2": {},
	}

	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], weights)
	}

	imbalance := CalculateImbalance(scores)
	if imbalance <= 0.25 {
		t.Fatalf("expected imbalanced cluster, got %f", imbalance)
	}

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25)

	if len(recommendations) == 0 {
		t.Fatal("expected at least one migration recommendation")
	}

	for _, r := range recommendations {
		if r.SourceNode != "node1" {
			t.Errorf("expected source node1, got %s", r.SourceNode)
		}
		if r.TargetNode != "node2" {
			t.Errorf("expected target node2, got %s", r.TargetNode)
		}
		if r.ExpectedImprovement <= 0 {
			t.Errorf("expected positive improvement, got %f", r.ExpectedImprovement)
		}
	}
}

func TestPlanRejectsSingleVMThrashing(t *testing.T) {
	weights := DefaultWeights()

	// 1 VM on 3 nodes — moving it just relocates the imbalance, shouldn't produce recommendations.
	nodeEntries := map[string]proxmox.NodeListEntry{
		"node1": {Node: "node1", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node3": {Node: "node3", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1", CPUUsage: 0.5, CPUs: 4, Mem: 4e9, MaxMem: 4e9},
		},
		"node2": {},
		"node3": {},
	}

	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], weights)
	}

	imbalance := CalculateImbalance(scores)
	if imbalance <= 0.25 {
		t.Fatalf("expected imbalanced cluster, got %f", imbalance)
	}

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25)

	if len(recommendations) != 0 {
		t.Errorf("expected no recommendations for single VM on 3 nodes, got %d: %+v",
			len(recommendations), recommendations)
	}
}

func TestPlanThreeNodesWithEmptyNode(t *testing.T) {
	weights := DefaultWeights()

	// 2 VMs on node1, 1 VM on node2, node3 empty — should move a VM to node3.
	nodeEntries := map[string]proxmox.NodeListEntry{
		"node1": {Node: "node1", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node3": {Node: "node3", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1", CPUUsage: 0.3, CPUs: 2, Mem: 2e9, MaxMem: 2e9},
			{VMID: 101, Type: "qemu", Node: "node1", CPUUsage: 0.2, CPUs: 2, Mem: 2e9, MaxMem: 2e9},
		},
		"node2": {
			{VMID: 102, Type: "qemu", Node: "node2", CPUUsage: 0.4, CPUs: 4, Mem: 4e9, MaxMem: 4e9},
		},
		"node3": {},
	}

	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], weights)
	}

	imbalance := CalculateImbalance(scores)
	if imbalance <= 0.25 {
		t.Fatalf("expected imbalanced cluster, got %f", imbalance)
	}

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25)

	if len(recommendations) == 0 {
		t.Fatal("expected at least one migration recommendation to spread VMs to empty node3")
	}

	// At least one VM should move to node3.
	movedToNode3 := false
	for _, r := range recommendations {
		if r.TargetNode == "node3" {
			movedToNode3 = true
		}
		if r.ExpectedImprovement <= 0 {
			t.Errorf("expected positive improvement, got %f", r.ExpectedImprovement)
		}
	}
	if !movedToNode3 {
		t.Errorf("expected at least one VM to move to node3, got: %+v", recommendations)
	}
}

func TestRulesAffinity(t *testing.T) {
	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1"},
			{VMID: 101, Type: "qemu", Node: "node1"},
		},
		"node2": {
			{VMID: 200, Type: "qemu", Node: "node2"},
		},
	}

	rules := []Rule{
		{Type: RuleTypeAffinity, VMIDs: []int{100, 101}, Enabled: true},
	}

	// Moving VM 100 away from 101 should be blocked.
	migration := Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node2"}
	if IsMigrationAllowed(migration, nodeWorkloads, rules) {
		t.Error("expected affinity rule to block migration of VM 100 to node2")
	}

	// Moving a non-grouped VM should be allowed.
	migration2 := Recommendation{VMID: 200, SourceNode: "node2", TargetNode: "node1"}
	if !IsMigrationAllowed(migration2, nodeWorkloads, rules) {
		t.Error("expected migration of VM 200 to be allowed")
	}
}

func TestRulesAntiAffinity(t *testing.T) {
	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1"},
		},
		"node2": {
			{VMID: 200, Type: "qemu", Node: "node2"},
		},
	}

	rules := []Rule{
		{Type: RuleTypeAntiAffinity, VMIDs: []int{100, 200}, Enabled: true},
	}

	// Moving VM 100 to node2 where 200 already is should be blocked.
	migration := Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node2"}
	if IsMigrationAllowed(migration, nodeWorkloads, rules) {
		t.Error("expected anti-affinity rule to block migration")
	}

	// Moving to node3 (empty) should be fine.
	nodeWorkloads["node3"] = nil
	migration2 := Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node3"}
	if !IsMigrationAllowed(migration2, nodeWorkloads, rules) {
		t.Error("expected migration to node3 to be allowed")
	}
}

func TestRulesPin(t *testing.T) {
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 100, Type: "qemu", Node: "node1"}},
		"node2": {},
	}

	rules := []Rule{
		{Type: RuleTypePin, VMIDs: []int{100}, NodeNames: []string{"node1"}, Enabled: true},
	}

	// Moving VM 100 away from its pinned node should be blocked.
	migration := Recommendation{VMID: 100, SourceNode: "node1", TargetNode: "node2"}
	if IsMigrationAllowed(migration, nodeWorkloads, rules) {
		t.Error("expected pin rule to block migration away from node1")
	}

	// Moving to the pinned node should be allowed.
	migration2 := Recommendation{VMID: 100, SourceNode: "node2", TargetNode: "node1"}
	if !IsMigrationAllowed(migration2, nodeWorkloads, rules) {
		t.Error("expected migration to pinned node1 to be allowed")
	}
}

func TestCheckViolations(t *testing.T) {
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 100, Type: "qemu", Node: "node1"}},
		"node2": {{VMID: 101, Type: "qemu", Node: "node2"}},
	}

	// Affinity rule violated: VMs 100 and 101 should be together.
	rules := []Rule{
		{Type: RuleTypeAffinity, VMIDs: []int{100, 101}, Enabled: true},
	}

	violations := CheckViolations(nodeWorkloads, rules)
	if len(violations) == 0 {
		t.Error("expected affinity violation")
	}

	// Anti-affinity rule satisfied: VMs on different nodes.
	rules2 := []Rule{
		{Type: RuleTypeAntiAffinity, VMIDs: []int{100, 101}, Enabled: true},
	}
	violations2 := CheckViolations(nodeWorkloads, rules2)
	if len(violations2) != 0 {
		t.Errorf("expected no anti-affinity violations, got %v", violations2)
	}
}
