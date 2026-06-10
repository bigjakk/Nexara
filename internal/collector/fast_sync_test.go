package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
