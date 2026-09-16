package drs

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// Evaluate-level coverage for the per-node workload listings, which are the one
// input where swallowing a read does not merely drop a constraint — it inverts
// the score.
//
// The three gates in evaluate_test.go all fail the evaluation, so a test that
// asserts "an error came back" is enough for them. This one must not: a single
// flaky node has to leave the other N-1 balancing, so the treatment is to drop
// the node rather than abort, and "no error" is therefore also what the BUG
// looks like. The assertion has to be about the plan itself — specifically that
// the node nobody could read was not chosen as a target.
//
// The fixture is built so the right answer and the wrong answer are different
// nodes. pve-02 is, in reality, a busy node; when its listing fails nothing in
// the evaluation can know that, so it scores as idle (or near-idle) and carries
// no guests for an anti-affinity rule to trip over. The correct target is
// pve-03, which was actually read and actually has room.

const (
	unreadNode     = "pve-02"
	readableTarget = "pve-03"
	unreadNodeCPU  = 8
	unreadNodeMem  = 16 << 30

	node3QemuPath = "/api2/json/nodes/pve-03/qemu"
	node3LxcPath  = "/api2/json/nodes/pve-03/lxc"
	node1VM102Cfg = "/api2/json/nodes/pve-01/qemu/102/config"
	node2VM201Cfg = "/api2/json/nodes/pve-02/qemu/201/config"
	node3VM301Cfg = "/api2/json/nodes/pve-03/qemu/301/config"
)

// unreadableNodeHandlers serves a three-node cluster in which every listing
// reads. Tests break exactly one of them on top of it.
//
// Scores under the fixture's weights (cpu 0.3, mem 0.7), each node 8 CPU /
// 16 GiB, with every guest's Mem equal to its MaxMem:
//
//	pve-01  VMs 101+102, 12 GiB, 2.0 CPU   → 0.3*0.2500 + 0.7*0.7500 = 0.6000
//	pve-02  VM 201 1 GiB + CT 251 10 GiB   → 0.3*0.2125 + 0.7*0.6875 = 0.5450
//	pve-03  VM 301, 4 GiB, 0.8 CPU         → 0.3*0.1000 + 0.7*0.2500 = 0.2050
//
// pve-02's load is split across the two listings on purpose, because the two
// failures degrade it by different amounts and both have to land below pve-03
// for the test to be about targeting rather than about arithmetic:
//
//	VM listing fails   → no workloads at all (the `continue` skipped the
//	                     container listing too)          → score 0.0000
//	CT listing fails   → VM 201 alone, 1 GiB             → score 0.0475
//
// Either way the fullest node on the cluster ranks below the genuinely-idle
// pve-03, so findSourceTargetPairs — which orders by score gap — puts it at the
// top of the target list. A planner that still targets it is not making a
// marginal misjudgement; it is sending the guest to the worst node available,
// chosen precisely because it could not be read.
func unreadableNodeHandlers() map[string]http.HandlerFunc {
	empty := func(w http.ResponseWriter, _ *http.Request) { haData(w, []struct{}{}) }
	noPassthrough := func(w http.ResponseWriter, _ *http.Request) { haData(w, proxmox.VMConfig{}) }
	vm := func(vmid int, mem int64, cpu float64, cpus int) proxmox.VirtualMachine {
		return proxmox.VirtualMachine{
			VMID: vmid, Name: "linux" + strconv.Itoa(vmid), Status: "running",
			CPU: cpu, CPUs: cpus, Mem: mem, MaxMem: mem,
		}
	}

	return map[string]http.HandlerFunc{
		nodesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.NodeListEntry{
				{Node: "pve-01", Status: "online", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
				{Node: unreadNode, Status: "online", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
				{Node: readableTarget, Status: "online", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
			})
		},
		node1QemuPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.VirtualMachine{
				vm(101, 6<<30, 0.25, 4),
				vm(102, 6<<30, 0.25, 4),
			})
		},
		node2QemuPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.VirtualMachine{vm(201, 1<<30, 0.05, 2)})
		},
		node3QemuPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.VirtualMachine{vm(301, 4<<30, 0.2, 4)})
		},
		node2LxcPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.Container{{
				VMID: 251, Name: "linux251", Status: "running",
				CPU: 0.4, CPUs: 4, Mem: 10 << 30, MaxMem: 10 << 30,
			}})
		},
		node1LxcPath: empty,
		node3LxcPath: empty,

		// detectPassthrough reads every running QEMU guest's config. Empty =
		// no hostpci/usb entries, so every guest stays migratable.
		node1VMCfgPath: noPassthrough,
		node1VM102Cfg:  noPassthrough,
		node2VM201Cfg:  noPassthrough,
		node3VM301Cfg:  noPassthrough,

		haStatusPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAStatusEntry{
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (idle, Wed Apr 29 07:55:26 2026)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: unreadNode, Status: "pve-02 (idle, Wed Apr 29 07:55:26 2026)"},
				{ID: "lrm:pve-03", Type: "lrm", Node: readableTarget, Status: "pve-03 (idle, Wed Apr 29 07:55:26 2026)"},
			})
		},
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HARuleEntry{})
		},
	}
}

func failListing(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "nope", http.StatusInternalServerError)
}

// assertUnreadNodeNotPlannedAgainst is the assertion this file exists for. It
// fails on the specific wrong outcome — the unread node used as a migration
// endpoint — rather than on "an error was returned", which is what the
// swallowing code does NOT do and so could never be caught that way.
//
// Both directions matter. Being chosen as a TARGET is the inverted score: the
// node is preferred because nothing is known about it. Being chosen as a SOURCE
// is the same missing data read the other way round — the planner moving a
// guest it believes is there, off a node whose guest list it never obtained.
func assertUnreadNodeNotPlannedAgainst(t *testing.T, result *EvalResult) {
	t.Helper()
	for _, r := range result.Recommendations {
		if r.TargetNode == unreadNode {
			t.Errorf("DRS planned a migration of %s %d onto %s, whose guests could not be listed: %q\n"+
				"An unread node has no workloads, so it scores as idle AND trips no anti-affinity rule — "+
				"it is the most attractive target on the cluster precisely because nothing is known about it.",
				r.VMType, r.VMID, unreadNode, r.Reason)
		}
		if r.SourceNode == unreadNode {
			t.Errorf("DRS planned to migrate %s %d off %s, whose guests could not be listed: %q\n"+
				"The guest list it planned from is not this node's.", r.VMType, r.VMID, unreadNode, r.Reason)
		}
	}
	if _, scored := result.NodeScores[unreadNode]; scored {
		t.Errorf("NodeScores still carries %s (score %.4f) after its listing failed; "+
			"a score computed from workloads nobody could read is what makes it a target",
			unreadNode, result.NodeScores[unreadNode].Score)
	}
}

// assertBalancedTheReadableNodes checks the evaluation still did its job over
// what it COULD read. Without it every assertion above would hold for a DRS
// that gave up and planned nothing — the drop treatment has to keep the rest of
// the cluster balancing, which is the whole reason it was chosen over failing.
func assertBalancedTheReadableNodes(t *testing.T, result *EvalResult) {
	t.Helper()
	for _, node := range []string{"pve-01", readableTarget} {
		if _, scored := result.NodeScores[node]; !scored {
			t.Errorf("NodeScores is missing %s, which was read successfully", node)
		}
	}
	if len(result.Recommendations) == 0 {
		t.Fatalf("no recommendations at all (imbalance %.4f, threshold %.4f); pve-01 and %s are "+
			"imbalanced and a migration between them is available",
			result.Imbalance, result.Threshold, readableTarget)
	}
	for _, r := range result.Recommendations {
		if r.SourceNode != "pve-01" || r.TargetNode != readableTarget {
			t.Errorf("recommendation %s %d goes %s -> %s, want pve-01 -> %s (the two nodes that were read)",
				r.VMType, r.VMID, r.SourceNode, r.TargetNode, readableTarget)
		}
	}
}

// TestEvaluate_DoesNotTargetANodeWhoseVMsCouldNotBeListed is the mutation
// target. Restoring the swallowing `continue` — which left nodeEntries
// populated for the node it gave up on — makes this fail on the recommendation,
// not on an error code.
func TestEvaluate_DoesNotTargetANodeWhoseVMsCouldNotBeListed(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[node2QemuPath] = failListing

	e, _, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Evaluate failed the whole cluster over one node's listing: %v\n"+
			"One flaky node must not stop the other nodes from being balanced.", err)
	}
	if result == nil {
		t.Fatal("result = nil; the evaluation should have completed over the readable nodes")
	}

	assertUnreadNodeNotPlannedAgainst(t, result)
	assertBalancedTheReadableNodes(t, result)
	assertOnlyReadEndpoints(t, hit, handlers)
}

// TestEvaluate_DoesNotTargetANodeWhoseContainersCouldNotBeListed covers the
// second listing, and is not a copy of the test above. The VM listing SUCCEEDS
// here, so the node ends up with a PARTIAL workload set rather than an empty
// one — undercounted rather than absent, and undercounted is preferred for
// exactly the same reason. Keeping a node on the strength of the listing that
// happened to work is the tempting half-fix; this is what refuses it.
func TestEvaluate_DoesNotTargetANodeWhoseContainersCouldNotBeListed(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[node2LxcPath] = failListing

	e, _, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Evaluate failed the whole cluster over one node's container listing: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil; the evaluation should have completed over the readable nodes")
	}

	assertUnreadNodeNotPlannedAgainst(t, result)
	assertBalancedTheReadableNodes(t, result)
	assertOnlyReadEndpoints(t, hit, handlers)
}

// TestEvaluate_StopsWhenOnlyOneNodeCouldBeListed is the boundary case that
// "all N failed" misses, and the reason the check counts readable nodes rather
// than comparing failures to eligibles. Two of three nodes unreadable leaves a
// single score in the map; CalculateImbalance returns 0 for fewer than two
// entries and Plan bails at the same count, so Evaluate would hand back
// imbalance 0.0000 with no recommendations — which the API renders, and the UI
// reads, as a green "Cluster Balanced, variance 0%" for a cluster two thirds of
// which nobody could read.
func TestEvaluate_StopsWhenOnlyOneNodeCouldBeListed(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[node1QemuPath] = failListing
	handlers[node2QemuPath] = failListing

	e, q, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("two of three nodes were unreadable and Evaluate returned %+v; "+
			"a lone surviving node scores as imbalance 0 and reports the cluster balanced", result)
	}
	if result != nil {
		t.Errorf("result = %+v alongside the error, want nil", result)
	}
	// The cause must survive: without it the operator gets a count and no
	// reason, since the per-node detail only exists in a Warn log line.
	if !strings.Contains(err.Error(), "list VMs") {
		t.Errorf("error %q does not carry the underlying listing failure", err)
	}
	assertStoppedAtTheGate(t, q, hit, handlers)
}

// TestEvaluate_StopsWhenNoNodeCouldBeListed is the same boundary at its limit.
// It is kept alongside the two-node case because it is the one that also proves
// the check is reached when `eligible` and `unreadable` are equal — the shape
// the gate was originally written as.
//
// Every eligible node failing is a cluster-wide fault by any reading, and
// GetNodes above already hard-fails on those, so erroring here is consistent
// rather than novel.
func TestEvaluate_StopsWhenNoNodeCouldBeListed(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[node1QemuPath] = failListing
	handlers[node2QemuPath] = failListing
	handlers[node3QemuPath] = failListing

	e, q, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("every node's guest listing failed and Evaluate returned %+v; "+
			"with no node readable there is no balance verdict to give", result)
	}
	if result != nil {
		t.Errorf("result = %+v alongside the error, want nil", result)
	}
	assertStoppedAtTheGate(t, q, hit, handlers)
}

// TestEvaluate_CompletesWhenEveryNodeIsIneligible pins the other half of that
// boundary, and is why the check counts failures instead of testing
// `len(scores) == 0`. A cluster whose nodes are all offline — or all in HA
// maintenance — also reaches the end with no scores, but nothing failed to
// read, so there is nothing to report and no reason to fail. Zero versus
// zero-out-of-zero, the same distinction unhealthyHANodes draws for unparseable
// LRM entries.
func TestEvaluate_CompletesWhenEveryNodeIsIneligible(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[nodesPath] = func(w http.ResponseWriter, _ *http.Request) {
		haData(w, []proxmox.NodeListEntry{
			{Node: "pve-01", Status: "offline", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
			{Node: unreadNode, Status: "offline", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
		})
	}

	e, _, _ := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Evaluate errored on a cluster with no eligible nodes: %v\n"+
			"No listing failed here — there was simply nothing to list.", err)
	}
	if result == nil {
		t.Fatal("result = nil, want an empty-but-successful evaluation")
	}
	if len(result.NodeScores) != 0 {
		t.Errorf("NodeScores = %v, want empty", result.NodeScores)
	}
}

// TestEvaluate_CompletesOnASingleNodeClusterThatReads is the third way to land
// under the two-node threshold, and the one that must NOT fail. A one-node
// cluster read successfully has no migrations available and never did; the
// threshold exists to catch a cluster whose nodes went missing, not one that
// only ever had a single node. `unreadable > 0` is what tells them apart, and
// this is the test that holds that term in place.
func TestEvaluate_CompletesOnASingleNodeClusterThatReads(t *testing.T) {
	handlers := unreadableNodeHandlers()
	handlers[nodesPath] = func(w http.ResponseWriter, _ *http.Request) {
		haData(w, []proxmox.NodeListEntry{
			{Node: "pve-01", Status: "online", MaxCPU: unreadNodeCPU, MaxMem: unreadNodeMem},
		})
	}

	e, _, _ := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Evaluate errored on a healthy single-node cluster: %v\n"+
			"Nothing failed to read — there is simply nowhere to migrate to.", err)
	}
	if result == nil {
		t.Fatal("result = nil on a cluster whose only node read fine")
	}
	if len(result.NodeScores) != 1 {
		t.Errorf("NodeScores = %v, want the one node that was read", result.NodeScores)
	}
	if len(result.Recommendations) != 0 {
		t.Errorf("Recommendations = %v on a single-node cluster, want none", result.Recommendations)
	}
}
