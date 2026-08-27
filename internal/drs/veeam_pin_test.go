package drs

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// fakeVeeamOwnedQueries stands in for the one database read pinVeeamInfrastructure
// makes.
type fakeVeeamOwnedQueries struct {
	rows []db.ListVeeamInfrastructureGuestsForClusterRow
	err  error
}

func (f *fakeVeeamOwnedQueries) ListVeeamInfrastructureGuestsForCluster(
	context.Context, uuid.UUID,
) ([]db.ListVeeamInfrastructureGuestsForClusterRow, error) {
	return f.rows, f.err
}

// TestPinVeeamInfrastructure_PinsButKeepsScoring is the load-bearing property.
//
// Veeam's guests must be PINNED, not removed. A pinned workload still counts
// toward node scoring, which is what keeps the balance honest: three worker
// appliances on one node is real load, and a planner blind to them would read
// that node as idle and pile more onto it. Removing them would also hide them
// from affinity rules, so something could be migrated onto a node hosting a
// pinned anti-affinity partner — the trap already documented for containers
// under include_containers=false.
func TestPinVeeamInfrastructure_PinsButKeepsScoring(t *testing.T) {
	clusterID := uuid.New()
	e := &Engine{
		logger: slog.New(slog.DiscardHandler),
		veeamOwned: &fakeVeeamOwnedQueries{rows: []db.ListVeeamInfrastructureGuestsForClusterRow{
			{Vmid: 103, Role: "worker"},
			{Vmid: 124, Role: "backup_server"},
		}},
	}

	nodeWorkloads := map[string][]Workload{
		"node1": {
			{VMID: 103, Name: "veeam13-appliance01", Type: "qemu", Node: "node1", CPUUsage: 0.4, CPUs: 4, Mem: 8e9, MaxMem: 8e9},
			{VMID: 200, Name: "web01", Type: "qemu", Node: "node1", CPUUsage: 0.3, CPUs: 4, Mem: 4e9, MaxMem: 4e9},
		},
		"node2": {
			{VMID: 124, Name: "vbr01", Type: "qemu", Node: "node2", CPUUsage: 0.2, CPUs: 2, Mem: 2e9, MaxMem: 2e9},
		},
	}

	e.pinVeeamInfrastructure(context.Background(), clusterID, true, nodeWorkloads)

	pinned := map[int]bool{}
	total := 0
	for _, workloads := range nodeWorkloads {
		for _, w := range workloads {
			total++
			pinned[w.VMID] = w.Pinned
		}
	}
	// Still three workloads: nothing was dropped, so node scoring sees the
	// same load it always did.
	if total != 3 {
		t.Fatalf("workloads = %d, want 3 — Veeam's guests are pinned, never removed", total)
	}
	if !pinned[103] {
		t.Error("the worker appliance was not pinned")
	}
	if !pinned[124] {
		t.Error("the VBR server was not pinned")
	}
	if pinned[200] {
		t.Error("an ordinary guest was pinned")
	}
}

// A failed lookup must leave the workloads alone. DRS balancing a cluster
// while briefly unaware of its worker appliances is a far smaller problem than
// DRS not running at all — and a failure here is transient by nature.
func TestPinVeeamInfrastructure_LookupFailureIsNotFatal(t *testing.T) {
	e := &Engine{
		logger:     slog.New(slog.DiscardHandler),
		veeamOwned: &fakeVeeamOwnedQueries{err: errors.New("connection reset")},
	}
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 103, Type: "qemu", Node: "node1"}},
	}

	e.pinVeeamInfrastructure(context.Background(), uuid.New(), true, nodeWorkloads)

	if nodeWorkloads["node1"][0].Pinned {
		t.Error("a failed lookup pinned a workload it could not identify")
	}
	if len(nodeWorkloads["node1"]) != 1 {
		t.Error("a failed lookup dropped a workload")
	}
}

// The config gate must actually gate. Without a test through this path an
// inverted or dropped condition leaves every other test here passing.
func TestPinVeeamInfrastructure_RespectsTheConfigToggle(t *testing.T) {
	e := &Engine{
		logger: slog.New(slog.DiscardHandler),
		veeamOwned: &fakeVeeamOwnedQueries{rows: []db.ListVeeamInfrastructureGuestsForClusterRow{
			{Vmid: 103, Role: "worker"},
		}},
	}
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 103, Type: "qemu", Node: "node1"}},
	}

	e.pinVeeamInfrastructure(context.Background(), uuid.New(), false, nodeWorkloads)

	if nodeWorkloads["node1"][0].Pinned {
		t.Error("a disabled toggle still pinned a Veeam worker")
	}
}

// A guest holding BOTH roles — the VBR server fills a proxy role itself —
// must be reported as the backup server, which is the more specific fact. The
// query orders (vmid, role) to guarantee that; a last-write-wins map would
// throw the ordering away.
func TestPinVeeamInfrastructure_PrefersTheMoreSpecificRole(t *testing.T) {
	e := &Engine{
		logger: slog.New(slog.DiscardHandler),
		veeamOwned: &fakeVeeamOwnedQueries{rows: []db.ListVeeamInfrastructureGuestsForClusterRow{
			// Query order: backup_server sorts before worker.
			{Vmid: 124, Role: "backup_server"},
			{Vmid: 124, Role: "worker"},
		}},
	}
	nodeWorkloads := map[string][]Workload{
		"node1": {{VMID: 124, Type: "qemu", Node: "node1"}},
	}

	e.pinVeeamInfrastructure(context.Background(), uuid.New(), true, nodeWorkloads)

	if !nodeWorkloads["node1"][0].Pinned {
		t.Error("a guest holding both roles was not pinned")
	}
}

// The planner must not select a pinned Veeam worker even when moving it would
// be the single best rebalancing move available.
func TestPlanDoesNotMigratePinnedVeeamWorker(t *testing.T) {
	weights := DefaultWeights()

	nodeEntries := map[string]proxmox.NodeListEntry{
		"node1": {Node: "node1", Status: "online", MaxCPU: 8, MaxMem: 16e9},
		"node2": {Node: "node2", Status: "online", MaxCPU: 8, MaxMem: 16e9},
	}
	nodeWorkloads := map[string][]Workload{
		"node1": {
			// The heaviest workload, and therefore the first candidate the
			// planner considers.
			{VMID: 103, Name: "veeam13-appliance01", Type: "qemu", Node: "node1",
				CPUUsage: 0.5, CPUs: 8, Mem: 8e9, MaxMem: 8e9, Pinned: true},
			{VMID: 200, Name: "web01", Type: "qemu", Node: "node1",
				CPUUsage: 0.3, CPUs: 4, Mem: 3e9, MaxMem: 6e9},
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

	recs := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, slog.New(slog.DiscardHandler))

	if len(recs) == 0 {
		t.Fatal("expected the movable guest to be migrated instead")
	}
	for _, r := range recs {
		if r.VMID == 103 {
			t.Errorf("DRS planned a migration of a Veeam worker: %+v — that kills the backup running on it", r)
		}
	}
}
