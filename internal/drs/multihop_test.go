package drs

import (
	"io"
	"log/slog"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestPlanCollapsesMultiHopForSameVM is the end-to-end guard for the multi-hop
// fix. This score landscape (a hot n3 with two movable guests; empty n1/n2 of
// equal score; light n4) makes the greedy loop route guests through different
// intermediate nodes depending on Go's randomized map-iteration order — some
// runs produce a multi-hop or a pure round trip for one VMID. Whatever the path,
// collapseHops must always guarantee the two deterministic invariants below:
// at most one recommendation per VMID, and never a no-op (source == target).
//
// Asserting a *specific* outcome (e.g. "vm 1001 is the one dropped") would be
// flaky precisely because the path is map-order dependent — that mistake is what
// this version fixes. The exact collapse semantics are pinned deterministically
// by TestCollapseHops; here we run many iterations so the varied orderings
// (including the multi-hop ones) are exercised, and any unwiring of collapseHops
// surfaces as a duplicate VMID.
func TestPlanCollapsesMultiHopForSameVM(t *testing.T) {
	weights := DefaultWeights()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	nodeEntries := map[string]proxmox.NodeListEntry{
		"n1": {Node: "n1", Status: "online", MaxCPU: 32, MaxMem: 64e9},
		"n2": {Node: "n2", Status: "online", MaxCPU: 32, MaxMem: 16e9},
		"n3": {Node: "n3", Status: "online", MaxCPU: 16, MaxMem: 16e9},
		"n4": {Node: "n4", Status: "online", MaxCPU: 4, MaxMem: 8e9},
	}
	build := func() map[string][]Workload {
		return map[string][]Workload{
			"n3": {
				{VMID: 1000, Type: "qemu", Node: "n3", CPUUsage: 0.4, CPUs: 8, Mem: 10e9, MaxMem: 32e9},
				{VMID: 1001, Type: "qemu", Node: "n3", CPUUsage: 0.7, CPUs: 4, Mem: 3.5e9, MaxMem: 16e9},
			},
			"n4": {
				{VMID: 1002, Type: "qemu", Node: "n4", CPUUsage: 0.85, CPUs: 3, Mem: 2.3e9, MaxMem: 8e9},
			},
		}
	}

	sawRecommendation := false
	for i := 0; i < 1000; i++ {
		nodeWorkloads := build()
		scores := make(map[string]NodeScore)
		for name, ne := range nodeEntries {
			scores[name] = ScoreNode(ne, nodeWorkloads[name], weights)
		}

		recs := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.05, quiet)
		if len(recs) > 0 {
			sawRecommendation = true
		}

		seen := make(map[int]bool, len(recs))
		for _, r := range recs {
			if r.SourceNode == r.TargetNode {
				t.Fatalf("iter %d: recommendation must never be a no-op (source==target): %+v", i, r)
			}
			if seen[r.VMID] {
				t.Fatalf("iter %d: vmid %d recommended more than once — collapseHops not applied: %+v", i, r.VMID, recs)
			}
			seen[r.VMID] = true
		}
	}
	if !sawRecommendation {
		t.Fatal("expected the planner to produce at least one recommendation across iterations")
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
