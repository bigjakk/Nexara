package collector

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// smbiosTestEnv is the scaffolding for syncClusterGuestSmbios tests: a mock DB
// and a Syncer pointed at an httptest Proxmox.
type smbiosTestEnv struct {
	m       *mockQueries
	syncer  *Syncer
	cluster db.Cluster
	node    db.Node
}

func newSmbiosTestEnv(t *testing.T, routes map[string]http.HandlerFunc) *smbiosTestEnv {
	t.Helper()
	srv := newTestServer(t, routes)
	t.Cleanup(srv.Close)

	m := newMockQueries()
	s := newTestSyncer(m)
	cluster := makeCluster(t, srv.URL)
	node := db.Node{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve1", Status: "online"}
	m.nodesByCluster = []db.Node{node}
	return &smbiosTestEnv{m: m, syncer: s, cluster: cluster, node: node}
}

func (e *smbiosTestEnv) guest(vmid int32, guestType string, node db.Node) db.Vm {
	return db.Vm{
		ID:        uuid.New(),
		ClusterID: e.cluster.ID,
		NodeID:    node.ID,
		Vmid:      vmid,
		Type:      guestType,
	}
}

// vmConfigRoute serves a QEMU config, counting how many times it was asked
// for — the point of the cache is that a converged guest is not re-read.
func vmConfigRoute(hits *atomic.Int64, config map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		jsonResponse(w, config)
	}
}

func TestSmbiosUUIDFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "bare uuid",
			config: map[string]any{"smbios1": "uuid=413babd1-94db-4f46-83ed-7c1ba5ab644e"},
			want:   "413babd1-94db-4f46-83ed-7c1ba5ab644e",
		},
		{
			// Proxmox writes a property string, and uuid= is not always the
			// first key — a guest with a custom manufacturer or serial pushes
			// it along. Splitting on the first "=" alone would return the
			// base64 manufacturer here.
			name:   "uuid after other properties",
			config: map[string]any{"smbios1": "manufacturer=UUVNVQ==,uuid=413babd1-94db-4f46-83ed-7c1ba5ab644e,family=Uw=="},
			want:   "413babd1-94db-4f46-83ed-7c1ba5ab644e",
		},
		{
			name:   "uppercase is normalised",
			config: map[string]any{"smbios1": "uuid=413BABD1-94DB-4F46-83ED-7C1BA5AB644E"},
			want:   "413babd1-94db-4f46-83ed-7c1ba5ab644e",
		},
		{
			// A guest can carry smbios1 settings with no uuid at all.
			name:   "no uuid key",
			config: map[string]any{"smbios1": "manufacturer=UUVNVQ=="},
			want:   "",
		},
		{
			// Rejected rather than cached: a malformed value would sit in the
			// cache looking like a legitimate key and never match anything,
			// with nothing to say why correlation had silently degraded.
			name:   "malformed uuid is rejected",
			config: map[string]any{"smbios1": "uuid=not-a-uuid"},
			want:   "",
		},
		{
			name:   "no smbios1 at all",
			config: map[string]any{"name": "web01"},
			want:   "",
		},
		{
			name:   "smbios1 is not a string",
			config: map[string]any{"smbios1": 42},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smbiosUUIDFromConfig(tt.config); got != tt.want {
				t.Errorf("smbiosUUIDFromConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyncClusterGuestSmbios_CachesQemuGuestsOnly(t *testing.T) {
	var qemuHits, ctHits atomic.Int64
	env := newSmbiosTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/config": vmConfigRoute(&qemuHits, map[string]any{
			"smbios1": "uuid=413babd1-94db-4f46-83ed-7c1ba5ab644e",
		}),
		"/api2/json/nodes/pve1/lxc/101/config": vmConfigRoute(&ctHits, map[string]any{}),
	})
	env.m.vmsByCluster = []db.Vm{
		env.guest(100, "qemu", env.node),
		env.guest(101, "lxc", env.node),
	}

	if err := env.syncer.syncClusterGuestSmbios(context.Background(), env.cluster); err != nil {
		t.Fatalf("syncClusterGuestSmbios: %v", err)
	}

	if len(env.m.smbiosUpserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(env.m.smbiosUpserts))
	}
	got := env.m.smbiosUpserts[0]
	if got.Vmid != 100 || got.SmbiosUuid != "413babd1-94db-4f46-83ed-7c1ba5ab644e" {
		t.Errorf("unexpected upsert: %+v", got)
	}
	// LXC containers have no SMBIOS table and Veeam cannot back them up, so
	// the pass must not spend a config read on one.
	if ctHits.Load() != 0 {
		t.Errorf("container config was read %d times, want 0", ctHits.Load())
	}

	// The prune list carries EVERY guest, container included: it is what stops
	// a guest that this pass legitimately skipped from being read as vanished.
	if len(env.m.smbiosVanishedCalls) != 1 {
		t.Fatalf("expected 1 vanished-guest prune, got %d", len(env.m.smbiosVanishedCalls))
	}
	if vmids := env.m.smbiosVanishedCalls[0].Vmids; len(vmids) != 2 {
		t.Errorf("prune vmids = %v, want both guests", vmids)
	}
}

func TestSyncClusterGuestSmbios_SkipsFreshCacheAndRefreshesStale(t *testing.T) {
	var hits atomic.Int64
	env := newSmbiosTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/config": vmConfigRoute(&hits, map[string]any{
			"smbios1": "uuid=413babd1-94db-4f46-83ed-7c1ba5ab644e",
		}),
		"/api2/json/nodes/pve1/qemu/101/config": vmConfigRoute(&hits, map[string]any{
			"smbios1": "uuid=5fab8faa-c4cb-4605-ad44-feda61f0e2bf",
		}),
	})
	env.m.vmsByCluster = []db.Vm{
		env.guest(100, "qemu", env.node),
		env.guest(101, "qemu", env.node),
	}
	env.m.guestSmbiosByCluster = []db.GuestSmbios{
		// Fresh — skipped.
		{ClusterID: env.cluster.ID, Vmid: 100, SmbiosUuid: "old", LastSeenAt: time.Now()},
		// Past the refresh window — re-read. A guest rebuilt in place keeps
		// its vmid and gets a NEW uuid, so a cache that never expired would
		// go on reporting the replaced machine's identity forever and hand
		// the replacement its predecessor's backups.
		{ClusterID: env.cluster.ID, Vmid: 101, SmbiosUuid: "older", LastSeenAt: time.Now().Add(-2 * smbiosRefreshInterval)},
	}

	if err := env.syncer.syncClusterGuestSmbios(context.Background(), env.cluster); err != nil {
		t.Fatalf("syncClusterGuestSmbios: %v", err)
	}

	if hits.Load() != 1 {
		t.Fatalf("config reads = %d, want exactly 1 (the stale guest)", hits.Load())
	}
	if len(env.m.smbiosUpserts) != 1 || env.m.smbiosUpserts[0].Vmid != 101 {
		t.Fatalf("expected only vmid 101 refreshed, got %+v", env.m.smbiosUpserts)
	}
}

func TestSyncClusterGuestSmbios_LeavesCacheAloneOnFailure(t *testing.T) {
	env := newSmbiosTestEnv(t, map[string]http.HandlerFunc{
		// A guest whose config read fails, and one on an offline node that is
		// never asked for at all.
		"/api2/json/nodes/pve1/qemu/100/config": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	offline := db.Node{ID: uuid.New(), ClusterID: env.cluster.ID, Name: "pve2", Status: "offline"}
	env.m.nodesByCluster = append(env.m.nodesByCluster, offline)
	env.m.vmsByCluster = []db.Vm{
		env.guest(100, "qemu", env.node),
		env.guest(200, "qemu", offline),
	}

	if err := env.syncer.syncClusterGuestSmbios(context.Background(), env.cluster); err != nil {
		t.Fatalf("syncClusterGuestSmbios: %v", err)
	}

	// Neither guest yields a write. A blip must never clear a cached uuid:
	// correlation would silently drop from the deterministic tier to the
	// low-confidence name tier with nothing to indicate why.
	if len(env.m.smbiosUpserts) != 0 {
		t.Errorf("expected no upserts, got %+v", env.m.smbiosUpserts)
	}
	// The prune still runs, and still lists both guests, so neither is
	// mistaken for one that has gone away.
	if len(env.m.smbiosVanishedCalls) != 1 {
		t.Fatalf("expected 1 prune call, got %d", len(env.m.smbiosVanishedCalls))
	}
	if vmids := env.m.smbiosVanishedCalls[0].Vmids; len(vmids) != 2 {
		t.Errorf("prune vmids = %v, want both guests", vmids)
	}
}

func TestSyncAllGuestSmbios_OnlyVisitsVeeamLinkedClusters(t *testing.T) {
	m := newMockQueries()
	s := newTestSyncer(m)
	// No cluster carries a Veeam platform mapping, so the pass must not even
	// list guests: an install without Veeam pays nothing for a correlation it
	// cannot use.
	m.veeamLinkedClusters = nil
	m.vmsByCluster = []db.Vm{{ID: uuid.New(), Vmid: 100, Type: "qemu"}}

	s.SyncAllGuestSmbios(context.Background())

	if len(m.smbiosUpserts) != 0 || len(m.smbiosVanishedCalls) != 0 {
		t.Errorf("pass touched the database with no Veeam-linked cluster: upserts=%d prunes=%d",
			len(m.smbiosUpserts), len(m.smbiosVanishedCalls))
	}
}

func TestSyncClusterGuestSmbios_RecordsGuestsWithNoSmbiosUUID(t *testing.T) {
	var hits atomic.Int64
	env := newSmbiosTestEnv(t, map[string]http.HandlerFunc{
		// A QEMU guest whose config has no smbios1 line at all.
		"/api2/json/nodes/pve1/qemu/100/config": vmConfigRoute(&hits, map[string]any{"name": "web01"}),
	})
	env.m.vmsByCluster = []db.Vm{env.guest(100, "qemu", env.node)}

	if err := env.syncer.syncClusterGuestSmbios(context.Background(), env.cluster); err != nil {
		t.Fatalf("syncClusterGuestSmbios: %v", err)
	}

	// The empty result is STORED. It is the difference between "this guest has
	// no SMBIOS uuid" — where correlation's name tier is the best honest
	// answer — and "this guest has not been looked at yet", where any match
	// would be a guess. Skipping the write would also re-read this guest's
	// config on every pass forever, since "not cached" is the only thing that
	// schedules a read.
	if len(env.m.smbiosUpserts) != 1 {
		t.Fatalf("expected the empty result to be recorded, got %d upserts", len(env.m.smbiosUpserts))
	}
	if got := env.m.smbiosUpserts[0]; got.Vmid != 100 || got.SmbiosUuid != "" {
		t.Errorf("upsert = %+v, want vmid 100 with an empty uuid", got)
	}
}

func TestSyncClusterGuestSmbios_BudgetExhaustionKeepsTheWholePruneList(t *testing.T) {
	var hits atomic.Int64
	env := newSmbiosTestEnv(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/config": vmConfigRoute(&hits, map[string]any{
			"smbios1": "uuid=413babd1-94db-4f46-83ed-7c1ba5ab644e",
		}),
		"/api2/json/nodes/pve1/qemu/101/config": vmConfigRoute(&hits, map[string]any{
			"smbios1": "uuid=5fab8faa-c4cb-4605-ad44-feda61f0e2bf",
		}),
	})
	env.m.vmsByCluster = []db.Vm{
		env.guest(100, "qemu", env.node),
		env.guest(101, "qemu", env.node),
		env.guest(102, "qemu", env.node),
	}

	// Already out of budget, which is what a cluster with more uncached
	// guests than smbiosSyncTimeout allows looks like partway through.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := env.syncer.syncClusterGuestSmbios(ctx, env.cluster); err != nil {
		t.Fatalf("syncClusterGuestSmbios: %v", err)
	}

	if hits.Load() != 0 {
		t.Errorf("config reads = %d, want 0 — a dead context should stop the fan-out", hits.Load())
	}

	// The prune list must still describe the WHOLE inventory. Collecting it as
	// the read loop went would hand the prune a truncated list and delete the
	// cached uuid of every guest the pass never reached — which, because the
	// name tier keys on the absence of that record, is what lets a rebuilt
	// host name-match its replacement and report as protected.
	if len(env.m.smbiosVanishedCalls) != 1 {
		t.Fatalf("expected 1 prune call, got %d", len(env.m.smbiosVanishedCalls))
	}
	if vmids := env.m.smbiosVanishedCalls[0].Vmids; len(vmids) != 3 {
		t.Errorf("prune vmids = %v, want all three guests despite the exhausted budget", vmids)
	}
}
