package collector

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// snapTestEnv is the shared scaffolding for syncClusterGuestSnapshots tests:
// a mock DB, an event recorder, and a Syncer pointed at an httptest Proxmox.
type snapTestEnv struct {
	m       *mockQueries
	pub     *mockEventPub
	syncer  *Syncer
	cluster db.Cluster
	node    db.Node
}

func newSnapTestEnv(t *testing.T, routes map[string]http.HandlerFunc) *snapTestEnv {
	t.Helper()
	srv := newTestServer(t, routes)
	t.Cleanup(srv.Close)

	m := newMockQueries()
	pub := &mockEventPub{}
	s := newTestSyncer(m)
	s.eventPub = pub
	cluster := makeCluster(t, srv.URL)
	node := db.Node{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve1", Status: "online"}
	m.nodesByCluster = []db.Node{node}
	return &snapTestEnv{m: m, pub: pub, syncer: s, cluster: cluster, node: node}
}

func (e *snapTestEnv) guest(vmid int32, guestType string, node db.Node) db.Vm {
	return db.Vm{
		ID:        uuid.New(),
		ClusterID: e.cluster.ID,
		NodeID:    node.ID,
		Vmid:      vmid,
		Type:      guestType,
	}
}

func (e *snapTestEnv) existingRow(vmid int32, name string) db.GuestSnapshot {
	return db.GuestSnapshot{
		ClusterID: e.cluster.ID,
		Vmid:      vmid,
		Name:      name,
		GuestType: "qemu",
		Node:      "pve1",
	}
}

func TestSyncClusterGuestSnapshots_DiffsAndPublishes(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{
				{Name: "current", Parent: "snapB"},
				{Name: "snapA", SnapTime: 1000, VMState: 1, Description: "before upgrade"},
				{Name: "snapB", SnapTime: 2000, Parent: "snapA"},
			})
		},
		"/api2/json/nodes/pve1/lxc/101/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{
				{Name: "current"},
				{Name: "snapC", SnapTime: 3000},
			})
		},
	})
	env.m.vmsByCluster = []db.Vm{
		env.guest(100, "qemu", env.node),
		env.guest(101, "lxc", env.node),
	}
	// snapA exists with stale fields (description changed on PVE); "ghost" was
	// deleted on PVE and must be pruned.
	stale := env.existingRow(100, "snapA")
	stale.SnapTime = 1000
	stale.Vmstate = true
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{stale, env.existingRow(100, "ghost")}
	env.m.guestSnapNotInSetRemoved = 1

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if got := len(env.m.guestSnapUpserts); got != 3 {
		t.Fatalf("expected 3 upserts, got %d: %+v", got, env.m.guestSnapUpserts)
	}
	byName := make(map[string]db.UpsertGuestSnapshotParams)
	for _, up := range env.m.guestSnapUpserts {
		if up.Name == "current" {
			t.Fatal("the synthetic current entry must never be upserted")
		}
		byName[up.Name] = up
	}
	snapA := byName["snapA"]
	if !snapA.Vmstate || snapA.GuestType != "qemu" || snapA.Node != "pve1" || snapA.Description != "before upgrade" {
		t.Fatalf("snapA upsert fields wrong: %+v", snapA)
	}
	if snapC := byName["snapC"]; snapC.GuestType != "lxc" || snapC.SnapTime != 3000 {
		t.Fatalf("snapC upsert fields wrong: %+v", snapC)
	}

	if got := len(env.m.guestSnapNotInSetCalls); got != 1 {
		t.Fatalf("expected 1 stale-set delete (vmid 100 only), got %d: %+v", got, env.m.guestSnapNotInSetCalls)
	}
	del := env.m.guestSnapNotInSetCalls[0]
	if del.Vmid != 100 || len(del.Names) != 2 {
		t.Fatalf("unexpected stale-set delete: %+v", del)
	}

	if got := len(env.m.guestSnapVanishedCalls); got != 1 {
		t.Fatalf("expected 1 vanished-guest cleanup, got %d", got)
	}
	if vmids := env.m.guestSnapVanishedCalls[0].Vmids; len(vmids) != 2 {
		t.Fatalf("vanished cleanup should keep all inventoried vmids, got %v", vmids)
	}

	if got := env.pub.countKind(events.KindSnapshotChange); got != 1 {
		t.Fatalf("expected exactly 1 snapshot_change event, got %d", got)
	}
}

func TestSyncClusterGuestSnapshots_ListingErrorSkipsPrune(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{env.existingRow(100, "ghost")}

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if len(env.m.guestSnapUpserts) != 0 || len(env.m.guestSnapNotInSetCalls) != 0 {
		t.Fatalf("errored guest must not be touched: upserts=%+v deletes=%+v",
			env.m.guestSnapUpserts, env.m.guestSnapNotInSetCalls)
	}
	// The guest is still inventoried, so vanished-cleanup must keep it.
	if vmids := env.m.guestSnapVanishedCalls[0].Vmids; len(vmids) != 1 || vmids[0] != 100 {
		t.Fatalf("vanished cleanup lost an errored guest: %v", vmids)
	}
	if got := env.pub.countKind(events.KindSnapshotChange); got != 0 {
		t.Fatalf("no change should publish no event, got %d", got)
	}
}

func TestSyncClusterGuestSnapshots_RawEmptyListingIsAnomalous(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{})
		},
	})
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{env.existingRow(100, "ghost")}

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(env.m.guestSnapUpserts) != 0 || len(env.m.guestSnapNotInSetCalls) != 0 {
		t.Fatal("raw-empty listing (missing even 'current') must not prune")
	}
}

func TestSyncClusterGuestSnapshots_OfflineNodeSkipped(t *testing.T) {
	hitOffline := false
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve2/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			hitOffline = true
			jsonResponse(w, []proxmox.Snapshot{{Name: "current"}})
		},
	})
	offline := db.Node{ID: uuid.New(), ClusterID: env.cluster.ID, Name: "pve2", Status: "offline"}
	env.m.nodesByCluster = append(env.m.nodesByCluster, offline)
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", offline)}
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{env.existingRow(100, "keepme")}

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if hitOffline {
		t.Fatal("guests on non-online nodes must not be listed")
	}
	if len(env.m.guestSnapNotInSetCalls) != 0 {
		t.Fatal("guests on non-online nodes must not be pruned")
	}
	if vmids := env.m.guestSnapVanishedCalls[0].Vmids; len(vmids) != 1 || vmids[0] != 100 {
		t.Fatalf("vanished cleanup lost an offline-node guest: %v", vmids)
	}
}

func TestSyncClusterGuestSnapshots_QuietPassPublishesNothing(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{
				{Name: "current"},
				{Name: "snapA", SnapTime: 1000},
			})
		},
	})
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}
	unchanged := env.existingRow(100, "snapA")
	unchanged.SnapTime = 1000
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{unchanged}

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// The row is still touched so last_seen_at advances…
	if len(env.m.guestSnapUpserts) != 1 {
		t.Fatalf("expected the unchanged row to be touched, got %+v", env.m.guestSnapUpserts)
	}
	// …but nothing changed, so no event and no deletes.
	if len(env.m.guestSnapNotInSetCalls) != 0 {
		t.Fatal("quiet pass must not delete")
	}
	if got := env.pub.countKind(events.KindSnapshotChange); got != 0 {
		t.Fatalf("quiet pass must not publish, got %d events", got)
	}
}

func TestSyncClusterGuestSnapshots_VanishedCleanupPublishes(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{
				{Name: "current"},
				{Name: "snapA", SnapTime: 1000},
			})
		},
	})
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}
	unchanged := env.existingRow(100, "snapA")
	unchanged.SnapTime = 1000
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{unchanged}
	env.m.guestSnapVanishedRemoved = 2 // rows of a destroyed guest were dropped

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := env.pub.countKind(events.KindSnapshotChange); got != 1 {
		t.Fatalf("vanished-guest cleanup is a change and must publish, got %d events", got)
	}
}

func TestSyncClusterGuestSnapshots_ClientBuildFailurePrunesNothing(t *testing.T) {
	env := newSnapTestEnv(t, nil)
	env.cluster.TokenSecretEncrypted = "not-decryptable"
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}
	env.m.guestSnapshotsByCluster = []db.GuestSnapshot{env.existingRow(100, "keepme")}

	if err := env.syncer.syncClusterGuestSnapshots(context.Background(), env.cluster); err == nil {
		t.Fatal("expected client build error")
	}
	if len(env.m.guestSnapVanishedCalls) != 0 || len(env.m.guestSnapNotInSetCalls) != 0 {
		t.Fatal("an unreachable cluster must prune nothing")
	}
}

func TestSyncAllGuestSnapshots_FansOutPerCluster(t *testing.T) {
	env := newSnapTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/snapshot": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Snapshot{
				{Name: "current"},
				{Name: "snapA", SnapTime: 1000},
			})
		},
	})
	env.m.clusters = []db.Cluster{env.cluster}
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}

	env.syncer.SyncAllGuestSnapshots(context.Background())

	if len(env.m.guestSnapUpserts) != 1 || env.m.guestSnapUpserts[0].Name != "snapA" {
		t.Fatalf("expected snapA upserted via the fan-out path, got %+v", env.m.guestSnapUpserts)
	}
}
