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
		totalNet  int64
		wantMin   float64
		wantMax   float64
	}{
		{
			name: "idle node",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				CPU:    0.0,
				MaxCPU: 8,
				Mem:    0,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: nil,
			totalNet:  1,
			wantMin:   0.0,
			wantMax:   0.01,
		},
		{
			name: "fully loaded node",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				CPU:    1.0,
				MaxCPU: 8,
				Mem:    16 * 1024 * 1024 * 1024,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: []Workload{
				{VMID: 100, NetIn: 1000000, NetOut: 1000000},
			},
			totalNet: 2000000,
			wantMin:  0.8,  // cpu 0.4 + mem 0.4
			wantMax:  1.05, // plus some network
		},
		{
			name: "50% loaded node",
			node: proxmox.NodeListEntry{
				Node:   "node1",
				Status: "online",
				CPU:    0.5,
				MaxCPU: 8,
				Mem:    8 * 1024 * 1024 * 1024,
				MaxMem: 16 * 1024 * 1024 * 1024,
			},
			workloads: nil,
			totalNet:  1,
			wantMin:   0.35,
			wantMax:   0.45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := ScoreNode(tt.node, tt.workloads, tt.totalNet, weights)
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
		"node1": {Node: "node1", Status: "online", CPU: 0.9, MaxCPU: 8, Mem: 15e9, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", CPU: 0.1, MaxCPU: 8, Mem: 2e9, MaxMem: 16e9},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 100, Type: "qemu", Node: "node1", CPUUsage: 0.5, CPUs: 4, Mem: 4e9, MaxMem: 4e9, NetIn: 1000, NetOut: 1000},
			{VMID: 101, Type: "qemu", Node: "node1", CPUUsage: 0.3, CPUs: 2, Mem: 2e9, MaxMem: 2e9, NetIn: 500, NetOut: 500},
		},
		"node2": {},
	}

	scores := make(map[string]NodeScore)
	var totalNet int64 = 3000
	for name, n := range nodeEntries {
		scores[name] = ScoreNode(n, nodeWorkloads[name], totalNet, weights)
	}

	imbalance := CalculateImbalance(scores)
	if imbalance <= 0.25 {
		t.Fatalf("expected imbalanced cluster, got %f", imbalance)
	}

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, totalNet)

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
