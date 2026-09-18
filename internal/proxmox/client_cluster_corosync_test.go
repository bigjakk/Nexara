package proxmox

import (
	"context"
	"net/http"
	"testing"
)

// PVE serves three paths under /cluster/config and they answer three
// different things. These tests stand up all three with the shapes PVE
// really returns, because the shapes are the whole subject:
//
//	/cluster/config        a directory index — [{"name": "..."}], no config
//	/cluster/config/nodes  the corosync nodelist, minus pve_addr/pve_fp
//	/cluster/config/join   the nodelist WITH those two, plus totem + digest
//
// The /nodes fixture is the one worth reading twice. Its published schema
// declares items as {"node": string} and nothing else, which reads like a
// bare index — but the handler returns
// hash_to_array($nodelist, 'node'), and hash_to_array stamps the key onto
// each entry and pushes the whole entry (pve-common,
// src/PVE/RESTHandler.pm), so every corosync field comes back too. A
// fixture built from the schema alone would understate what the old code
// received and would let a test pass for the wrong reason.

// corosyncNodelistEntry is one /cluster/config/nodes item: the corosync
// section's own fields plus the stamped id property.
func corosyncNodelistEntry(name string, id int, ring string) map[string]interface{} {
	return map[string]interface{}{
		"node": name, "name": name,
		"nodeid": id, "quorum_votes": 1, "ring0_addr": ring,
	}
}

// clusteredRoutes is a node that IS in a cluster.
func clusteredRoutes(indexHit, nodesHit *bool) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/api2/json/cluster/config": func(w http.ResponseWriter, r *http.Request) {
			*indexHit = true
			jsonResponse(w, []map[string]string{
				{"name": "apiversion"}, {"name": "join"},
				{"name": "nodes"}, {"name": "qdevice"}, {"name": "totem"},
			})
		},
		"/api2/json/cluster/config/nodes": func(w http.ResponseWriter, r *http.Request) {
			*nodesHit = true
			jsonResponse(w, []map[string]interface{}{
				corosyncNodelistEntry("pve-01", 1, "192.0.2.11"),
				corosyncNodelistEntry("pve-02", 2, "192.0.2.12"),
				corosyncNodelistEntry("pve-03", 3, "192.0.2.13"),
			})
		},
		"/api2/json/cluster/config/join": func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, map[string]interface{}{
				"config_digest":  "0000000000000000000000000000000000000000",
				"preferred_node": "pve-01",
				"totem": map[string]interface{}{
					"cluster_name": "cluster01", "config_version": "3",
				},
				// pve_addr is deliberately a DIFFERENT address from
				// ring0_addr on every node. They coincide on many real
				// clusters, but a fixture where they match cannot tell
				// "merged from /join" apart from "copied from the
				// ring address" — and separating the corosync link from
				// the management address is the reason PVE carries both.
				"nodelist": []map[string]interface{}{
					{"name": "pve-01", "nodeid": 1, "quorum_votes": 1,
						"ring0_addr": "192.0.2.11", "pve_addr": "198.51.100.11",
						"pve_fp": "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:" +
							"AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"},
					{"name": "pve-02", "nodeid": 2, "quorum_votes": 1,
						"ring0_addr": "192.0.2.12", "pve_addr": "198.51.100.12"},
					{"name": "pve-03", "nodeid": 3, "quorum_votes": 1,
						"ring0_addr": "192.0.2.13", "pve_addr": "198.51.100.13"},
				},
			})
		},
	}
}

// standaloneRoutes is a node that is NOT in a cluster: /nodes answers an
// empty list, /join raises 424 (PVE::Exception, HTTP_FAILED_DEPENDENCY,
// pve-cluster src/PVE/API2/ClusterConfig.pm).
func standaloneRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/api2/json/cluster/config/nodes": func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, []map[string]interface{}{})
		},
		"/api2/json/cluster/config/join": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusFailedDependency)
			_, _ = w.Write([]byte(`{"message":"node is not in a cluster, no join info available!"}`))
		},
	}
}

// TestGetCorosyncNodesMergesTheAddressFromJoin pins the fix. Every column
// the SPA's Corosync Nodes table renders is asserted BY VALUE: asserting
// only the length is what would let the original bug through, since the
// old read returned the right number of nodes with pve_addr blank.
func TestGetCorosyncNodesMergesTheAddressFromJoin(t *testing.T) {
	var indexHit, nodesHit bool
	srv := newTestServer(t, clusteredRoutes(&indexHit, &nodesHit))
	defer srv.Close()

	nodes, err := newTestClient(t, srv.URL).GetCorosyncNodes(context.Background())
	if err != nil {
		t.Fatalf("GetCorosyncNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	for i, want := range []struct {
		name, ring, mgmt string
		nodeID           float64
	}{
		{"pve-01", "192.0.2.11", "198.51.100.11", 1},
		{"pve-02", "192.0.2.12", "198.51.100.12", 2},
		{"pve-03", "192.0.2.13", "198.51.100.13", 3},
	} {
		got := nodes[i]
		if got.Name != want.name {
			t.Errorf("node %d name = %q, want %q", i, got.Name, want.name)
		}
		if got.Ring0Addr != want.ring {
			t.Errorf("node %d ring0_addr = %q, want %q", i, got.Ring0Addr, want.ring)
		}
		// The column the merge exists for, and the reason the two
		// addresses differ in the fixture: this must be the value from
		// /join, not the ring address /cluster/config/nodes already had.
		if got.PVEAddr != want.mgmt {
			t.Errorf("node %d pve_addr = %q, want %q — /cluster/config/nodes does not "+
				"carry it, so it must come from the join object", i, got.PVEAddr, want.mgmt)
		}
		if id, ok := got.NodeID.(float64); !ok || id != want.nodeID {
			t.Errorf("node %d nodeid = %#v, want %v", i, got.NodeID, want.nodeID)
		}
		if votes, ok := got.Quorate.(float64); !ok || votes != 1 {
			t.Errorf("node %d quorum_votes = %#v, want 1", i, got.Quorate)
		}
	}
	if nodes[0].PVEFP == "" {
		t.Error("node 0 pve_fp is empty; it is merged from the same join object")
	}
	if !nodesHit {
		t.Error("/cluster/config/nodes was not called; it is the authoritative source")
	}
}

// TestGetCorosyncNodesSurvivesAStandaloneNode is the regression this
// nearly shipped. /join raises 424 on a node that is not in a cluster, so
// sourcing the nodelist FROM /join turns a healthy standalone install into
// a 502 and an error banner — and makes the SPA's own empty state ("No
// corosync nodes found. This may be a standalone node.") unreachable.
func TestGetCorosyncNodesSurvivesAStandaloneNode(t *testing.T) {
	srv := newTestServer(t, standaloneRoutes())
	defer srv.Close()

	nodes, err := newTestClient(t, srv.URL).GetCorosyncNodes(context.Background())
	if err != nil {
		t.Fatalf("GetCorosyncNodes on a standalone node: %v — a node with no cluster "+
			"is a healthy configuration, not an error", err)
	}
	if len(nodes) != 0 {
		t.Errorf("got %d nodes, want 0", len(nodes))
	}
}

// TestGetClusterConfigReadsTheJoinObject pins the loud half of the pair:
// /cluster/config is an array, ClusterConfig is a struct, and decoding one
// into the other failed on every call this endpoint ever received.
func TestGetClusterConfigReadsTheJoinObject(t *testing.T) {
	var indexHit, nodesHit bool
	srv := newTestServer(t, clusteredRoutes(&indexHit, &nodesHit))
	defer srv.Close()

	cfg, err := newTestClient(t, srv.URL).GetClusterConfig(context.Background())
	if err != nil {
		t.Fatalf("GetClusterConfig: %v", err)
	}
	if len(cfg.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Name != "pve-01" {
		t.Errorf("first node = %q, want pve-01", cfg.Nodes[0].Name)
	}
	totem, ok := cfg.Totem.(map[string]interface{})
	if !ok {
		t.Fatalf("totem = %#v, want the object PVE sends", cfg.Totem)
	}
	if totem["cluster_name"] != "cluster01" {
		t.Errorf("totem.cluster_name = %v, want cluster01", totem["cluster_name"])
	}
	if indexHit {
		t.Error("/cluster/config was called; it is an index and carries no configuration")
	}
}

// TestGetClusterConfigSurvivesAStandaloneNode is GetClusterConfig's half
// of the 424 handling: an empty configuration is the truthful answer, and
// it is one the caller can render.
func TestGetClusterConfigSurvivesAStandaloneNode(t *testing.T) {
	srv := newTestServer(t, standaloneRoutes())
	defer srv.Close()

	cfg, err := newTestClient(t, srv.URL).GetClusterConfig(context.Background())
	if err != nil {
		t.Fatalf("GetClusterConfig on a standalone node: %v", err)
	}
	if len(cfg.Nodes) != 0 || cfg.Totem != nil {
		t.Errorf("got %+v, want an empty configuration", cfg)
	}
}

// TestIsNotInClusterErrorMatchesOnlyThatStatus keeps the predicate narrow.
// Widening it is how "I could not look" starts reading as "there is no
// cluster" — the same collapse IsHARulesUnsupportedError's comment warns
// about at length for 501.
func TestIsNotInClusterErrorMatchesOnlyThatStatus(t *testing.T) {
	t.Parallel()

	if IsNotInClusterError(nil) {
		t.Error("nil is not a not-in-a-cluster error")
	}
	if !IsNotInClusterError(&APIError{StatusCode: http.StatusFailedDependency}) {
		t.Error("424 must match")
	}
	for _, code := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusNotImplemented,
		http.StatusConflict,
	} {
		if IsNotInClusterError(&APIError{StatusCode: code}) {
			t.Errorf("%d must not match; it means the call failed, not that the node is standalone", code)
		}
	}
	if IsNotInClusterError(ErrNotFound) {
		t.Error("ErrNotFound must not match")
	}
	if IsNotInClusterError(ErrConnectionFailed) {
		t.Error("a connection failure must not match")
	}
}

// TestGetClusterJoinInfoStillReadsItsOwnPath guards the obvious
// over-correction: two functions now share this upstream call, and folding
// all three together would lose config_digest, which only this one exposes.
func TestGetClusterJoinInfoStillReadsItsOwnPath(t *testing.T) {
	var indexHit, nodesHit bool
	srv := newTestServer(t, clusteredRoutes(&indexHit, &nodesHit))
	defer srv.Close()

	info, err := newTestClient(t, srv.URL).GetClusterJoinInfo(context.Background())
	if err != nil {
		t.Fatalf("GetClusterJoinInfo: %v", err)
	}
	if info.ConfigDigest == "" {
		t.Error("config_digest is empty; it is this endpoint's reason to exist")
	}
	if len(info.NodeList) != 3 {
		t.Errorf("got %d nodelist entries, want 3", len(info.NodeList))
	}
	if nodesHit {
		t.Error("/cluster/config/nodes was called; this endpoint reads only /join")
	}
}
