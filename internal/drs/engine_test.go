package drs

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
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

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, slog.Default())

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

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, slog.Default())

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

	recommendations := Plan(scores, nodeWorkloads, nodeEntries, nil, weights, 0.25, slog.Default())

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

func TestExtractLRMState(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{
			name:   "active with watchdog",
			status: "pve2 (active, watchdog active, Wed Apr 29 07:55:26 2026)",
			want:   "active",
		},
		{
			name:   "idle",
			status: "pve1 (idle, Wed Apr 29 07:55:26 2026)",
			want:   "idle",
		},
		{
			name:   "maintenance",
			status: "pve3 (maintenance, watchdog active, Wed Apr 29 07:55:26 2026)",
			want:   "maintenance",
		},
		{
			name:   "maintenance mode collapses to maintenance",
			status: "pve3 (maintenance mode, Wed Apr 29 07:55:26 2026)",
			want:   "maintenance",
		},
		{
			name:   "wait_for_agent_lock",
			status: "pve3 (wait_for_agent_lock, Wed Apr 29 07:55:26 2026)",
			want:   "wait_for_agent_lock",
		},
		{
			name:   "no parens — unparseable",
			status: "pve3 active",
			want:   "",
		},
		{
			name:   "empty",
			status: "",
			want:   "",
		},
		// The shapes a format change would arrive as. Each returns "",
		// which classifyHAEntries fails open on and counts — see
		// TestUnhealthyHANodes_UnparseableLRMStates for what the caller
		// does once every entry looks like this.
		{
			name:   "bracketed instead of parenthesized",
			status: "pve-01 [active, watchdog active]",
			want:   "",
		},
		{
			name:   "bare state token with no node prefix or parens",
			status: "maintenance",
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLRMState(tc.status)
			if got != tc.want {
				t.Errorf("extractLRMState(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// TestUnhealthyHANodes_SeparatesNoneFromUnreadable covers the half that talks
// to Proxmox; TestClassifyHAEntries below covers the pure half. Nothing used to
// cover this one, which is how a failed read returning an empty skip set —
// indistinguishable from "no node is in HA maintenance" — went unnoticed.
//
// Why every status in the first subtest is an error, and why no version of PVE
// is carved out here, is argued on unhealthyHANodes itself. Do not restate it.
func TestUnhealthyHANodes_SeparatesNoneFromUnreadable(t *testing.T) {
	statusEntries := func(entries []proxmox.HAStatusEntry) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { haData(w, entries) }
	}

	t.Run("a status that could not be read stops the evaluation", func(t *testing.T) {
		for _, status := range []int{
			http.StatusInternalServerError, // the cluster could not answer
			http.StatusServiceUnavailable,  // not quorate
			http.StatusForbidden,           // token lacks the HA privilege
			http.StatusNotFound,            // a reverse-proxy rewrite, not a PVE answer
		} {
			srv, _ := haTestServer(t, map[string]http.HandlerFunc{
				haStatusPath: func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "nope", status)
				},
			})
			skip, err := unhealthyHANodes(context.Background(), newHAClient(t, srv.URL), slog.Default(), uuid.New())
			if err == nil {
				t.Errorf("a %d on /cluster/ha/status/current read as a cluster with no node in maintenance (skip=%v); DRS would migrate onto a node HA is evacuating", status, keys(skip))
			}
			if skip != nil {
				t.Errorf("skip = %v alongside the error, want nil", keys(skip))
			}
		}
	})

	t.Run("a cluster with nothing in maintenance is not an error", func(t *testing.T) {
		srv, _ := haTestServer(t, map[string]http.HandlerFunc{
			haStatusPath: statusEntries([]proxmox.HAStatusEntry{
				{ID: "quorum", Type: "quorum", Status: "OK"},
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (idle, Wed Apr 29 07:55:26 2026)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (active, watchdog active, Wed Apr 29 07:55:26 2026)"},
			}),
		})
		skip, err := unhealthyHANodes(context.Background(), newHAClient(t, srv.URL), slog.Default(), uuid.New())
		if err != nil {
			t.Fatalf("unhealthyHANodes on a cluster with no node in maintenance: %v", err)
		}
		if len(skip) != 0 {
			t.Errorf("skip = %v, want empty", keys(skip))
		}
	})

	t.Run("a node in maintenance is still reported", func(t *testing.T) {
		srv, _ := haTestServer(t, map[string]http.HandlerFunc{
			haStatusPath: statusEntries([]proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (active, watchdog active, ...)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (maintenance, watchdog active, ...)"},
			}),
		})
		skip, err := unhealthyHANodes(context.Background(), newHAClient(t, srv.URL), slog.Default(), uuid.New())
		if err != nil {
			t.Fatalf("unhealthyHANodes: %v", err)
		}
		if _, ok := skip["pve-02"]; !ok || len(skip) != 1 {
			t.Errorf("skip = %v, want exactly pve-02", keys(skip))
		}
	})
}

func TestClassifyHAEntries(t *testing.T) {
	// Build a fake Proxmox client backed by a test HTTP server returning
	// a real-shape /cluster/ha/status/current payload with one node in
	// maintenance.
	tests := []struct {
		name            string
		entries         []proxmox.HAStatusEntry
		want            map[string]struct{}
		wantUnparseable int
		wantLRMTotal    int
	}{
		{
			name: "all active",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve1", Type: "lrm", Node: "pve1", Status: "pve1 (active, watchdog active, Wed Apr 29 07:55:26 2026)"},
				{ID: "lrm:pve2", Type: "lrm", Node: "pve2", Status: "pve2 (active, watchdog active, Wed Apr 29 07:55:26 2026)"},
			},
			want:         map[string]struct{}{},
			wantLRMTotal: 2,
		},
		{
			name: "maintenance node skipped",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve1", Type: "lrm", Node: "pve1", Status: "pve1 (active, watchdog active, ...)"},
				{ID: "lrm:pve2", Type: "lrm", Node: "pve2", Status: "pve2 (maintenance, watchdog active, ...)"},
				{ID: "service:vm:100", Type: "service", Node: "pve2", Status: "vm:100 (pve2, started)"},
			},
			want:         map[string]struct{}{"pve2": {}},
			wantLRMTotal: 2, // the service entry is not an lrm entry
		},
		{
			name: "node-type entries are ignored (no LRM info there)",
			entries: []proxmox.HAStatusEntry{
				{ID: "node/pve1", Type: "node", Node: "pve1", Status: "online"},
				{ID: "node/pve2", Type: "node", Node: "pve2", Status: "online"},
			},
			want: map[string]struct{}{},
			// Zero lrm entries means zero unparseable ones. The caller
			// must not read that as "everything is unparseable".
			wantLRMTotal: 0,
		},
		{
			// One node answering in a shape we don't recognise is not a
			// format change. It stays out of the skip set — failing open
			// per entry is deliberate — but it is counted, so the caller
			// can say so instead of staying silent.
			name: "one unparseable state is counted, not skipped",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (active, watchdog active, ...)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 active"},
			},
			want:            map[string]struct{}{},
			wantUnparseable: 1,
			wantLRMTotal:    2,
		},
		{
			// An unparseable sibling must not cost the entries that DID
			// parse: pve-02 is in maintenance and stays in the skip set.
			name: "a maintenance node survives an unparseable sibling",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (active, watchdog active, ...)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (maintenance, watchdog active, ...)"},
				{ID: "lrm:pve-03", Type: "lrm", Node: "pve-03", Status: "pve-03 something unrecognised"},
			},
			want:            map[string]struct{}{"pve-02": {}},
			wantUnparseable: 1,
			wantLRMTotal:    3,
		},
		{
			// The format-change signature. classifyHAEntries still fails
			// open on every one of them — deciding what an all-unparseable
			// listing means is the caller's job, and the counts are what
			// let it decide.
			name: "every state unparseable is still fail-open here, and fully counted",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: ""},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: ""},
				{ID: "lrm:pve-03", Type: "lrm", Node: "pve-03", Status: ""},
			},
			want:            map[string]struct{}{},
			wantUnparseable: 3,
			wantLRMTotal:    3,
		},
		{
			// "node" renamed away is the same class of format change as
			// "status" renamed away, and it arrives one field earlier. An
			// lrm entry with no node must count as a state we could not
			// read — dropping it uncounted would leave unparseable ==
			// lrmTotal == 0 and make the caller's check vacuous, on a
			// cluster where two nodes are in maintenance.
			name: "lrm entries with no node attribution count as unparseable",
			entries: []proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Status: "pve-01 (maintenance, watchdog active, ...)"},
				{ID: "lrm:pve-02", Type: "lrm", Status: "pve-02 (maintenance, watchdog active, ...)"},
			},
			want:            map[string]struct{}{},
			wantUnparseable: 2,
			wantLRMTotal:    2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, unparseable, lrmTotal := classifyHAEntries(tc.entries)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d skipped, want %d (got=%v want=%v)", len(got), len(tc.want), keys(got), keys(tc.want))
			}
			for k := range tc.want {
				if _, ok := got[k]; !ok {
					t.Errorf("expected %q to be marked unhealthy", k)
				}
			}
			if unparseable != tc.wantUnparseable {
				t.Errorf("unparseable = %d, want %d", unparseable, tc.wantUnparseable)
			}
			if lrmTotal != tc.wantLRMTotal {
				t.Errorf("lrmTotal = %d, want %d", lrmTotal, tc.wantLRMTotal)
			}
		})
	}
}

// TestUnhealthyHANodes_UnparseableLRMStates covers the second route to the
// collapse TestUnhealthyHANodes_SeparatesNoneFromUnreadable covers the first
// of: the read succeeds, every entry decodes, and every state parses to "".
// classifyHAEntries treats "" as healthy, so an all-unparseable listing
// produces an empty skip set that is indistinguishable from a healthy cluster
// — the maintenance filter passes everything and nothing is logged.
//
// The assertions here are deliberately three-way. Failing on any unparseable
// entry would let one odd node strand DRS for the whole cluster; failing on
// none of them is the silent collapse. Only "all of them" is unambiguous.
func TestUnhealthyHANodes_UnparseableLRMStates(t *testing.T) {
	statusEntries := func(entries []proxmox.HAStatusEntry) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { haData(w, entries) }
	}
	// run returns the skip set, how many WARN-or-above records
	// unhealthyHANodes emitted for this listing, and the error.
	run := func(t *testing.T, entries []proxmox.HAStatusEntry) (map[string]struct{}, int, error) {
		t.Helper()
		srv, _ := haTestServer(t, map[string]http.HandlerFunc{haStatusPath: statusEntries(entries)})
		warns := &warnCounter{}
		skip, err := unhealthyHANodes(context.Background(), newHAClient(t, srv.URL), slog.New(warns), uuid.New())
		warns.mu.Lock()
		defer warns.mu.Unlock()
		return skip, warns.warns, err
	}

	t.Run("every LRM state unparseable stops the evaluation", func(t *testing.T) {
		// Four shapes of the same format change: the field this reads
		// renamed away (so Status decodes empty), the state moved into
		// HAStatusEntry.State — which has no Go reader, so it would
		// decode fine and change nothing here — the status reshaped out
		// of the "<node> (<state>, ...)" form, and the node attribution
		// renamed away one field earlier.
		for name, entries := range map[string][]proxmox.HAStatusEntry{
			"status field renamed away": {
				{ID: "quorum", Type: "quorum", Status: "OK"},
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02"},
			},
			"state moved to the Go-unread State field": {
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", State: "active"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", State: "maintenance"},
			},
			"status is a bare token with no parens": {
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "active"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "maintenance"},
			},
			"node attribution renamed away": {
				{ID: "lrm:pve-01", Type: "lrm", Status: "pve-01 (active, watchdog active, ...)"},
				{ID: "lrm:pve-02", Type: "lrm", Status: "pve-02 (maintenance, watchdog active, ...)"},
			},
		} {
			skip, warns, err := run(t, entries)
			if err == nil {
				t.Errorf("%s: read as a cluster with no node in maintenance (skip=%v); DRS would migrate onto a node HA is evacuating", name, keys(skip))
			}
			if skip != nil {
				t.Errorf("%s: skip = %v alongside the error, want nil", name, keys(skip))
			}
			// The error is the report. Warning as well would say the
			// nodes are "being treated as healthy" on a path that
			// treats them as nothing at all.
			if warns != 0 {
				t.Errorf("%s: warns = %d on the error path, want 0", name, warns)
			}
		}
	})

	t.Run("some unparseable warns and proceeds", func(t *testing.T) {
		skip, warns, err := run(t, []proxmox.HAStatusEntry{
			{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (active, watchdog active, ...)"},
			{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (maintenance, watchdog active, ...)"},
			{ID: "lrm:pve-03", Type: "lrm", Node: "pve-03", Status: "pve-03 something unrecognised"},
		})
		if err != nil {
			t.Fatalf("one unparseable entry stranded the whole evaluation: %v", err)
		}
		if _, ok := skip["pve-02"]; !ok || len(skip) != 1 {
			t.Errorf("skip = %v, want exactly pve-02", keys(skip))
		}
		if warns == 0 {
			t.Error("an unparseable LRM state was passed over silently; it must be logged at WARN")
		}
	})

	t.Run("a listing with no LRM entries is not a format change", func(t *testing.T) {
		// unparseable == lrmTotal == 0 here. Erroring on the bare
		// equality would fail an evaluation that should have passed.
		skip, warns, err := run(t, []proxmox.HAStatusEntry{
			{ID: "quorum", Type: "quorum", Status: "OK"},
			{ID: "node/pve-01", Type: "node", Node: "pve-01", Status: "online"},
		})
		if err != nil {
			t.Fatalf("a listing with no lrm entries was read as a format change: %v", err)
		}
		if len(skip) != 0 {
			t.Errorf("skip = %v, want empty", keys(skip))
		}
		if warns != 0 {
			t.Errorf("warns = %d on a listing with nothing unparseable, want 0", warns)
		}
	})

	t.Run("a fully parseable listing warns about nothing", func(t *testing.T) {
		_, warns, err := run(t, []proxmox.HAStatusEntry{
			{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (idle, Wed Apr 29 07:55:26 2026)"},
			{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (maintenance mode, Wed Apr 29 07:55:26 2026)"},
		})
		if err != nil {
			t.Fatalf("unhealthyHANodes: %v", err)
		}
		if warns != 0 {
			t.Errorf("warns = %d on a fully parseable listing, want 0", warns)
		}
	})
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
