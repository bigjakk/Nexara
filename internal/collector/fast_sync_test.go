package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// newResourceServer fakes GET /cluster/resources with the given entries.
func newResourceServer(t *testing.T, resources []proxmox.ClusterResource) *httptest.Server {
	t.Helper()
	return newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/resources": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, resources)
		},
	})
}

// seedFastSync seeds one node + one guest into the mock and returns the node ID.
func seedFastSync(mq *mockQueries, cluster db.Cluster, nodeStatus, vmStatus, vmName string) uuid.UUID {
	nodeID := uuid.New()
	mq.nodes[cluster.ID.String()+":pve1"] = db.Node{
		ID: nodeID, ClusterID: cluster.ID, Name: "pve1", Status: nodeStatus,
	}
	mq.vms[fmt.Sprintf("%s:%d", cluster.ID.String(), 100)] = db.Vm{
		ID: uuid.New(), ClusterID: cluster.ID, NodeID: nodeID,
		Vmid: 100, Name: vmName, Type: "qemu", Status: vmStatus,
	}
	return nodeID
}

func TestSyncClusterResources(t *testing.T) {
	nodeOnline := proxmox.ClusterResource{Type: "node", Node: "pve1", Status: "online"}
	guestRunning := proxmox.ClusterResource{
		Type: "qemu", VMID: 100, Node: "pve1", Status: "running",
		Name: "web-server", MaxCPU: 4, MaxMem: 4294967296,
	}

	t.Run("quiet tick costs nothing and publishes nothing", func(t *testing.T) {
		srv := newResourceServer(t, []proxmox.ClusterResource{nodeOnline, guestRunning})
		defer srv.Close()
		mq := newMockQueries()
		pub := &mockEventPub{}
		s := newTestSyncer(mq)
		s.SetEventPublisher(pub)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")

		if err := s.syncClusterResources(context.Background(), cluster); err != nil {
			t.Fatalf("syncClusterResources: %v", err)
		}
		if len(mq.upsertVMCalls) != 0 {
			t.Errorf("expected no upserts on a quiet tick, got %d", len(mq.upsertVMCalls))
		}
		if got := pub.countKind(events.KindInventoryChange); got != 0 {
			t.Errorf("inventory_change = %d, want 0 (events: %+v)", got, pub.events)
		}
	})

	// The slow pass reads the node's live guest list; /cluster/resources is
	// pvestatd's, up to 10s behind. A tick must not write that stale status
	// back over the newer one — it would also record a status change that
	// never happened in the guest's History.
	t.Run("an unchanged listing never reverts a newer status", func(t *testing.T) {
		var mu sync.Mutex
		listed := guestRunning
		srv := newTestServer(t, map[string]http.HandlerFunc{
			"/api2/json/cluster/resources": func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				jsonResponse(w, []proxmox.ClusterResource{nodeOnline, listed})
			},
		})
		defer srv.Close()
		list := func(status string) {
			mu.Lock()
			defer mu.Unlock()
			listed.Status = status
		}
		mq := newMockQueries()
		s := newTestSyncer(mq)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")
		tick := func() {
			t.Helper()
			if err := s.syncClusterResources(context.Background(), cluster); err != nil {
				t.Fatalf("syncClusterResources: %v", err)
			}
		}
		key := fmt.Sprintf("%s:%d", cluster.ID, 100)

		tick() // the listing agrees with the row
		vm := mq.vms[key]
		vm.Status = "stopped" // the slow pass saw the stop first
		mq.vms[key] = vm
		tick() // pvestatd has not caught up
		if len(mq.upsertVMCalls) != 0 {
			t.Fatalf("an unchanged listing wrote its stale status back: %+v", mq.upsertVMCalls)
		}

		list("stopped")
		tick() // caught up: nothing to write
		list("running")
		tick() // a change in the listing is news
		if got := mq.vms[key].Status; got != "running" {
			t.Fatalf("a listing that changed was not adopted: the row says %q", got)
		}

		// A write that fails is retried next tick: the listing is only
		// remembered once the row holds it.
		mq.upsertVMFailures = 1
		list("stopped")
		tick()
		tick()
		if got := mq.vms[key].Status; got != "stopped" {
			t.Fatalf("a failed write was never retried: the row says %q", got)
		}
	})

	// A guest pvestatd has not reported yet is listed "unknown" with no name,
	// and so is every guest for a cycle after pve-cluster restarts: written,
	// it recorded a false "Status unknown" in every new guest's History. One
	// that stays unknown, or whose node is offline, is unknown for real.
	t.Run("a guest pvestatd has not reported yet writes nothing", func(t *testing.T) {
		for _, tc := range []struct {
			node       string
			unknownFor time.Duration // already listed "unknown" for this long
			upserts    int           // the one write is the "unknown"
		}{{"online", 0, 0}, {"unknown", 0, 0}, {"online", 2 * unknownStatusGrace, 1}, {"offline", 0, 1}} {
			node := nodeOnline
			node.Status = tc.node
			srv := newResourceServer(t, []proxmox.ClusterResource{node,
				{Type: "qemu", VMID: 100, Node: "pve1", Status: "unknown"}})
			mq := newMockQueries()
			s := newTestSyncer(mq)
			cluster := makeCluster(t, srv.URL)
			seedFastSync(mq, cluster, "online", "running", "web-server")
			if tc.unknownFor > 0 {
				s.resourceUnknownSince.Store(cluster.ID, map[int32]time.Time{100: time.Now().Add(-tc.unknownFor)})
			}

			if err := s.syncClusterResources(context.Background(), cluster); err != nil {
				t.Fatalf("node %s: syncClusterResources: %v", tc.node, err)
			}
			srv.Close()
			if len(mq.upsertVMCalls) != tc.upserts || tc.upserts == 1 && mq.upsertVMCalls[0].Status != "unknown" {
				t.Errorf("node %s, unknown for %v: upserts %+v, want %d writing \"unknown\"",
					tc.node, tc.unknownFor, mq.upsertVMCalls, tc.upserts)
			}
			// The first sighting is kept for the next tick, or the grace never ends.
			kept, _ := s.resourceUnknownSince.Load(cluster.ID)
			if since, _ := kept.(map[int32]time.Time); tc.node != "offline" && since[100].IsZero() {
				t.Errorf("node %s: the first sighting of \"unknown\" was not kept for the next tick", tc.node)
			}
		}
	})

	t.Run("status flip upserts and publishes both events", func(t *testing.T) {
		stopped := guestRunning
		stopped.Status = "stopped"
		srv := newResourceServer(t, []proxmox.ClusterResource{nodeOnline, stopped})
		defer srv.Close()
		mq := newMockQueries()
		pub := &mockEventPub{}
		s := newTestSyncer(mq)
		s.SetEventPublisher(pub)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")

		if err := s.syncClusterResources(context.Background(), cluster); err != nil {
			t.Fatalf("syncClusterResources: %v", err)
		}
		if len(mq.upsertVMCalls) != 1 {
			t.Fatalf("expected 1 upsert, got %d", len(mq.upsertVMCalls))
		}
		if got := pub.countKind(events.KindVMStateChange); got != 1 {
			t.Errorf("vm_state_change = %d, want 1", got)
		}
		if got := pub.countKind(events.KindInventoryChange); got != 1 {
			t.Errorf("inventory_change = %d, want 1", got)
		}
	})

	// /cluster/resources reads a running VM's QEMU run state; the slow pass
	// stores "suspended" for paused and "running" for every other state. Storing
	// the raw value flipped the row on every tick.
	t.Run("a QEMU run state is stored as the slow pass stores it", func(t *testing.T) {
		for _, tc := range []struct{ listed, stored, wantWrite string }{
			{"paused", "running", "suspended"},
			{"paused", "suspended", ""}, // already agrees: no write, no event
			{"prelaunch", "running", ""},
		} {
			guest := guestRunning
			guest.Status = tc.listed
			srv := newResourceServer(t, []proxmox.ClusterResource{nodeOnline, guest})
			mq := newMockQueries()
			pub := &mockEventPub{}
			s := newTestSyncer(mq)
			s.SetEventPublisher(pub)
			cluster := makeCluster(t, srv.URL)
			seedFastSync(mq, cluster, "online", tc.stored, "web-server")

			if err := s.syncClusterResources(context.Background(), cluster); err != nil {
				t.Fatalf("%s over %s: syncClusterResources: %v", tc.listed, tc.stored, err)
			}
			srv.Close()
			wantUpserts := 0
			if tc.wantWrite != "" {
				wantUpserts = 1
			}
			if len(mq.upsertVMCalls) != wantUpserts {
				t.Fatalf("%s over %s: %d upserts, want %d", tc.listed, tc.stored, len(mq.upsertVMCalls), wantUpserts)
			}
			if wantUpserts == 1 && mq.upsertVMCalls[0].Status != tc.wantWrite {
				t.Errorf("%s over %s: wrote %q, want %q", tc.listed, tc.stored, mq.upsertVMCalls[0].Status, tc.wantWrite)
			}
			if got := pub.countKind(events.KindVMStateChange); got != wantUpserts {
				t.Errorf("%s over %s: vm_state_change = %d, want %d", tc.listed, tc.stored, got, wantUpserts)
			}
		}
	})

	t.Run("guest absent from cluster config is deleted immediately", func(t *testing.T) {
		srv := newResourceServer(t, []proxmox.ClusterResource{nodeOnline})
		defer srv.Close()
		mq := newMockQueries()
		pub := &mockEventPub{}
		s := newTestSyncer(mq)
		s.SetEventPublisher(pub)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")

		if err := s.syncClusterResources(context.Background(), cluster); err != nil {
			t.Fatalf("syncClusterResources: %v", err)
		}
		if len(mq.deleteAbsentCalls) != 1 {
			t.Fatalf("expected 1 delete-absent call, got %d", len(mq.deleteAbsentCalls))
		}
		if len(mq.vms) != 0 {
			t.Errorf("expected the stale guest to be deleted, %d rows remain", len(mq.vms))
		}
		if got := pub.countKind(events.KindInventoryChange); got != 1 {
			t.Errorf("inventory_change = %d, want 1", got)
		}
	})

	t.Run("payload without node entries is rejected and deletes nothing", func(t *testing.T) {
		// A guest-less, node-less payload must be treated as malformed —
		// acting on it would wipe the inventory.
		srv := newResourceServer(t, []proxmox.ClusterResource{})
		defer srv.Close()
		mq := newMockQueries()
		s := newTestSyncer(mq)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")

		if err := s.syncClusterResources(context.Background(), cluster); err == nil {
			t.Fatal("expected an error for a payload with no node entries")
		}
		if len(mq.deleteAbsentCalls) != 0 {
			t.Errorf("expected no deletions on a malformed payload, got %d calls", len(mq.deleteAbsentCalls))
		}
		if len(mq.vms) != 1 {
			t.Errorf("guest rows must survive a malformed payload, %d remain", len(mq.vms))
		}
	})

	t.Run("node status flip publishes inventory_change", func(t *testing.T) {
		offline := nodeOnline
		offline.Status = "offline"
		srv := newResourceServer(t, []proxmox.ClusterResource{offline, guestRunning})
		defer srv.Close()
		mq := newMockQueries()
		pub := &mockEventPub{}
		s := newTestSyncer(mq)
		s.SetEventPublisher(pub)
		cluster := makeCluster(t, srv.URL)
		seedFastSync(mq, cluster, "online", "running", "web-server")

		if err := s.syncClusterResources(context.Background(), cluster); err != nil {
			t.Fatalf("syncClusterResources: %v", err)
		}
		if got := pub.countKind(events.KindInventoryChange); got != 1 {
			t.Errorf("inventory_change = %d, want 1 (events: %+v)", got, pub.events)
		}
	})

	t.Run("guest on a node the slow loop has not registered is skipped", func(t *testing.T) {
		newNodeGuest := guestRunning
		newNodeGuest.Node = "pve9"
		srv := newResourceServer(t, []proxmox.ClusterResource{
			nodeOnline,
			{Type: "node", Node: "pve9", Status: "online"},
			newNodeGuest,
		})
		defer srv.Close()
		mq := newMockQueries()
		pub := &mockEventPub{}
		s := newTestSyncer(mq)
		s.SetEventPublisher(pub)
		cluster := makeCluster(t, srv.URL)
		nodeID := seedFastSync(mq, cluster, "online", "running", "web-server")
		_ = nodeID
		// Remove the seeded guest so only the unknown-node guest exists upstream.
		delete(mq.vms, fmt.Sprintf("%s:%d", cluster.ID.String(), 100))

		if err := s.syncClusterResources(context.Background(), cluster); err != nil {
			t.Fatalf("syncClusterResources: %v", err)
		}
		if len(mq.upsertVMCalls) != 0 {
			t.Errorf("expected the unknown-node guest to be skipped, got %d upserts", len(mq.upsertVMCalls))
		}
	})
}
