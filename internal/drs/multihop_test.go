package drs

import (
	"log/slog"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestPlanCollapsesMultiHopForSameVM is the end-to-end guard for the multi-hop
// fix. This score landscape makes the greedy loop migrate vm 1001 n3→n1 and then
// (a later iteration, after vm 1000 moves) n1→n3 — a pure round trip — while vm
// 1000 moves n3→n2 once. Without collapsing, the executor would live-migrate vm
// 1001 twice only to return it to n3. After the fix the planner must emit at
// most one recommendation per VMID, drop the net no-op round trip, and keep the
// genuine move.
func TestPlanCollapsesMultiHopForSameVM(t *testing.T) {
	weights := DefaultWeights()

	nodeEntries := map[string]proxmox.NodeListEntry{
		"n1": {Node: "n1", Status: "online", MaxCPU: 32, MaxMem: 64e9},
		"n2": {Node: "n2", Status: "online", MaxCPU: 32, MaxMem: 16e9},
		"n3": {Node: "n3", Status: "online", MaxCPU: 16, MaxMem: 16e9},
		"n4": {Node: "n4", Status: "online", MaxCPU: 4, MaxMem: 8e9},
	}
	nodeWorkloads := map[string][]Workload{
		"n3": {
			{VMID: 1000, Type: "qemu", Node: "n3", CPUUsage: 0.4, CPUs: 8, Mem: 10e9, MaxMem: 32e9},
			{VMID: 1001, Type: "qemu", Node: "n3", CPUUsage: 0.7, CPUs: 4, Mem: 3.5e9, MaxMem: 16e9},
		},
		"n4": {
			{VMID: 1002, Type: "qemu", Node: "n4", CPUUsage: 0.85, CPUs: 3, Mem: 2.3e9, MaxMem: 8e9},
		},
	}

	scores := make(map[string]NodeScore)
	for name, ne := range nodeEntries {
		scores[name] = ScoreNode(ne, nodeWorkloads[name], weights)
	}
	if CalculateImbalance(scores) <= 0.05 {
		t.Fatalf("test setup: expected an imbalanced cluster")
	}

	recs := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.05, slog.Default())

	// Core invariant: each guest is migrated at most once per run.
	seen := make(map[int]int)
	for _, r := range recs {
		seen[r.VMID]++
		if r.SourceNode == r.TargetNode {
			t.Errorf("recommendation must never be a no-op (source==target): %+v", r)
		}
	}
	for vmid, count := range seen {
		if count > 1 {
			t.Errorf("vmid %d appears %d times; the planner must emit at most one hop per guest: %+v", vmid, count, recs)
		}
	}

	// The genuine move (vm 1000 n3→n2) survives; the round-tripped vm 1001 is
	// dropped because its net source and target are identical.
	if seen[1001] != 0 {
		t.Errorf("expected the round-tripped vm 1001 to be dropped, got recs: %+v", recs)
	}
	if seen[1000] != 1 {
		t.Errorf("expected the genuine vm 1000 move to survive exactly once, got recs: %+v", recs)
	}
}

// TestCollapseHops unit-tests the collapse logic directly with hand-built hop
// lists, independent of the planner's score landscape.
func TestCollapseHops(t *testing.T) {
	t.Run("merges a->b->c into a->c", func(t *testing.T) {
		in := []Recommendation{
			{VMID: 1, VMType: "qemu", SourceNode: "a", TargetNode: "b", ScoreBefore: 1.0, ScoreAfter: 0.7, ExpectedImprovement: 0.3, Reason: "hop1"},
			{VMID: 1, VMType: "qemu", SourceNode: "b", TargetNode: "c", ScoreBefore: 0.7, ScoreAfter: 0.4, ExpectedImprovement: 0.3, Reason: "hop2"},
		}
		out := collapseHops(in)
		if len(out) != 1 {
			t.Fatalf("expected 1 recommendation, got %d: %+v", len(out), out)
		}
		got := out[0]
		if got.SourceNode != "a" || got.TargetNode != "c" {
			t.Errorf("expected net move a->c, got %s->%s", got.SourceNode, got.TargetNode)
		}
		if got.ScoreBefore != 1.0 || got.ScoreAfter != 0.4 {
			t.Errorf("expected ScoreBefore=1.0 (first hop) ScoreAfter=0.4 (last hop), got before=%.2f after=%.2f", got.ScoreBefore, got.ScoreAfter)
		}
		if got.ExpectedImprovement != 0.6 {
			t.Errorf("expected improvement 0.6 (1.0-0.4), got %.2f", got.ExpectedImprovement)
		}
	})

	t.Run("drops a->b->a round trip", func(t *testing.T) {
		in := []Recommendation{
			{VMID: 1, SourceNode: "a", TargetNode: "b"},
			{VMID: 1, SourceNode: "b", TargetNode: "a"},
		}
		out := collapseHops(in)
		if len(out) != 0 {
			t.Errorf("expected the round trip to be dropped, got %+v", out)
		}
	})

	t.Run("merges three hops a->b->c->d into a->d", func(t *testing.T) {
		in := []Recommendation{
			{VMID: 7, SourceNode: "a", TargetNode: "b"},
			{VMID: 7, SourceNode: "b", TargetNode: "c"},
			{VMID: 7, SourceNode: "c", TargetNode: "d"},
		}
		out := collapseHops(in)
		if len(out) != 1 || out[0].SourceNode != "a" || out[0].TargetNode != "d" {
			t.Fatalf("expected single a->d, got %+v", out)
		}
	})

	t.Run("leaves single hops untouched and preserves order + reason", func(t *testing.T) {
		in := []Recommendation{
			{VMID: 1, SourceNode: "a", TargetNode: "b", Reason: "r1"},
			{VMID: 2, SourceNode: "c", TargetNode: "d", Reason: "r2"},
		}
		out := collapseHops(in)
		if len(out) != 2 {
			t.Fatalf("expected 2 recommendations, got %d", len(out))
		}
		if out[0].VMID != 1 || out[1].VMID != 2 {
			t.Errorf("expected first-appearance order [1,2], got [%d,%d]", out[0].VMID, out[1].VMID)
		}
		if out[0].Reason != "r1" || out[1].Reason != "r2" {
			t.Errorf("single-hop reasons must be preserved, got %q,%q", out[0].Reason, out[1].Reason)
		}
	})

	t.Run("interleaved guests: X a->b, Y c->d, X b->e", func(t *testing.T) {
		in := []Recommendation{
			{VMID: 1, SourceNode: "a", TargetNode: "b"},
			{VMID: 2, SourceNode: "c", TargetNode: "d"},
			{VMID: 1, SourceNode: "b", TargetNode: "e"},
		}
		out := collapseHops(in)
		if len(out) != 2 {
			t.Fatalf("expected 2 recommendations, got %d: %+v", len(out), out)
		}
		// Order preserved by first appearance: VM 1 then VM 2.
		if out[0].VMID != 1 || out[0].SourceNode != "a" || out[0].TargetNode != "e" {
			t.Errorf("expected VM 1 net a->e first, got %+v", out[0])
		}
		if out[1].VMID != 2 || out[1].SourceNode != "c" || out[1].TargetNode != "d" {
			t.Errorf("expected VM 2 c->d second, got %+v", out[1])
		}
	})

	t.Run("nil and single-element inputs pass through", func(t *testing.T) {
		if out := collapseHops(nil); len(out) != 0 {
			t.Errorf("nil input should yield empty, got %+v", out)
		}
		one := []Recommendation{{VMID: 1, SourceNode: "a", TargetNode: "b"}}
		if out := collapseHops(one); len(out) != 1 {
			t.Errorf("single input should pass through, got %+v", out)
		}
	})
}
