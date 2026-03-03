package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// --- Test encryption key ---

const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// --- Mock DB ---

type mockQueries struct {
	nodes        map[string]db.Node    // keyed by "clusterID:name"
	vms          map[string]db.Vm      // keyed by "clusterID:vmid"
	storagePools map[string]db.StoragePool
	clusters     []db.Cluster

	upsertNodeCalls    []db.UpsertNodeParams
	upsertVMCalls      []db.UpsertVMParams
	upsertStorageCalls []db.UpsertStoragePoolParams
}

func newMockQueries() *mockQueries {
	return &mockQueries{
		nodes:        make(map[string]db.Node),
		vms:          make(map[string]db.Vm),
		storagePools: make(map[string]db.StoragePool),
	}
}

func (m *mockQueries) ListActiveClusters(_ context.Context) ([]db.Cluster, error) {
	return m.clusters, nil
}

func (m *mockQueries) UpsertNode(_ context.Context, arg db.UpsertNodeParams) (db.Node, error) {
	m.upsertNodeCalls = append(m.upsertNodeCalls, arg)
	node := db.Node{
		ID:             uuid.New(),
		ClusterID:      arg.ClusterID,
		Name:           arg.Name,
		Status:         arg.Status,
		CpuCount:       arg.CpuCount,
		MemTotal:       arg.MemTotal,
		DiskTotal:      arg.DiskTotal,
		PveVersion:     arg.PveVersion,
		SslFingerprint: arg.SslFingerprint,
		Uptime:         arg.Uptime,
	}
	key := arg.ClusterID.String() + ":" + arg.Name
	m.nodes[key] = node
	return node, nil
}

func (m *mockQueries) UpsertVM(_ context.Context, arg db.UpsertVMParams) (db.Vm, error) {
	m.upsertVMCalls = append(m.upsertVMCalls, arg)
	vm := db.Vm{
		ID:        uuid.New(),
		ClusterID: arg.ClusterID,
		NodeID:    arg.NodeID,
		Vmid:      arg.Vmid,
		Name:      arg.Name,
		Type:      arg.Type,
		Status:    arg.Status,
	}
	key := arg.ClusterID.String() + ":" + string(rune(arg.Vmid))
	m.vms[key] = vm
	return vm, nil
}

func (m *mockQueries) UpsertStoragePool(_ context.Context, arg db.UpsertStoragePoolParams) (db.StoragePool, error) {
	m.upsertStorageCalls = append(m.upsertStorageCalls, arg)
	pool := db.StoragePool{
		ID:        uuid.New(),
		ClusterID: arg.ClusterID,
		NodeID:    arg.NodeID,
		Storage:   arg.Storage,
		Type:      arg.Type,
	}
	return pool, nil
}

// --- Test helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	raw, _ := json.Marshal(data)
	resp := map[string]json.RawMessage{"data": raw}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newTestServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, handler := range routes {
		mux.HandleFunc(pattern, handler)
	}
	return httptest.NewServer(mux)
}

func makeCluster(t *testing.T, serverURL string) db.Cluster {
	t.Helper()
	encrypted, err := crypto.Encrypt("secret-token-value", testEncryptionKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return db.Cluster{
		ID:                   uuid.New(),
		Name:                 "test-cluster",
		ApiUrl:               serverURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		TlsFingerprint:       "",
		SyncIntervalSeconds:  30,
		IsActive:             true,
	}
}

func newTestSyncer(queries SyncQueries) *Syncer {
	return &Syncer{
		queries:       queries,
		encryptionKey: testEncryptionKey,
		clientFactory: DefaultClientFactory,
		logger:        testLogger(),
	}
}

// --- Tests ---

func TestSyncCluster_Success(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{
				{Node: "pve1", Status: "online", MaxCPU: 8, MaxMem: 17179869184, MaxDisk: 107374182400, Uptime: 86400, SSLFingerprint: "abc123"},
				{Node: "pve2", Status: "online", MaxCPU: 16, MaxMem: 34359738368, MaxDisk: 214748364800, Uptime: 172800},
			})
		},
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{
				Node:       "pve1",
				PVEVersion: "pve-manager/8.1.3",
				CPUInfo:    proxmox.CPUInfo{CPUs: 8},
				Memory:     proxmox.Memory{Total: 17179869184},
				RootFS:     proxmox.RootFS{Total: 107374182400},
			})
		},
		"/api2/json/nodes/pve2/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{
				Node:       "pve2",
				PVEVersion: "pve-manager/8.1.3",
				CPUInfo:    proxmox.CPUInfo{CPUs: 16},
				Memory:     proxmox.Memory{Total: 34359738368},
				RootFS:     proxmox.RootFS{Total: 214748364800},
			})
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 100, Name: "web-server", Status: "running", CPUs: 4, MaxMem: 4294967296, MaxDisk: 53687091200, Uptime: 3600},
			})
		},
		"/api2/json/nodes/pve2/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 101, Name: "db-server", Status: "running", CPUs: 8, MaxMem: 8589934592, MaxDisk: 107374182400, Template: 0},
			})
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{
				{VMID: 200, Name: "dns-ct", Status: "running", CPUs: 1, MaxMem: 536870912, MaxDisk: 10737418240},
			})
		},
		"/api2/json/nodes/pve2/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{})
		},
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{
				{Storage: "local", Type: "dir", Content: "images,rootdir", Active: 1, Enabled: 1, Total: 107374182400, Used: 21474836480, Avail: 85899345920},
			})
		},
		"/api2/json/nodes/pve2/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{
				{Storage: "local-lvm", Type: "lvmthin", Content: "images", Active: 1, Enabled: 1, Shared: 0, Total: 214748364800, Used: 42949672960, Avail: 171798691840},
			})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}

	// Verify nodes.
	if len(mq.upsertNodeCalls) != 2 {
		t.Fatalf("expected 2 node upserts, got %d", len(mq.upsertNodeCalls))
	}
	if mq.upsertNodeCalls[0].Name != "pve1" {
		t.Errorf("node[0].Name = %q, want %q", mq.upsertNodeCalls[0].Name, "pve1")
	}
	if mq.upsertNodeCalls[0].PveVersion != "pve-manager/8.1.3" {
		t.Errorf("node[0].PveVersion = %q, want %q", mq.upsertNodeCalls[0].PveVersion, "pve-manager/8.1.3")
	}
	if mq.upsertNodeCalls[0].CpuCount != 8 {
		t.Errorf("node[0].CpuCount = %d, want 8", mq.upsertNodeCalls[0].CpuCount)
	}
	if mq.upsertNodeCalls[1].Name != "pve2" {
		t.Errorf("node[1].Name = %q, want %q", mq.upsertNodeCalls[1].Name, "pve2")
	}

	// Verify VMs: 2 QEMU + 1 LXC = 3.
	if len(mq.upsertVMCalls) != 3 {
		t.Fatalf("expected 3 VM upserts, got %d", len(mq.upsertVMCalls))
	}
	// First VM on pve1: QEMU 100.
	if mq.upsertVMCalls[0].Vmid != 100 || mq.upsertVMCalls[0].Type != "qemu" {
		t.Errorf("vm[0]: vmid=%d type=%q, want vmid=100 type=qemu", mq.upsertVMCalls[0].Vmid, mq.upsertVMCalls[0].Type)
	}
	// LXC container on pve1.
	if mq.upsertVMCalls[1].Vmid != 200 || mq.upsertVMCalls[1].Type != "lxc" {
		t.Errorf("vm[1]: vmid=%d type=%q, want vmid=200 type=lxc", mq.upsertVMCalls[1].Vmid, mq.upsertVMCalls[1].Type)
	}
	// QEMU 101 on pve2.
	if mq.upsertVMCalls[2].Vmid != 101 || mq.upsertVMCalls[2].Type != "qemu" {
		t.Errorf("vm[2]: vmid=%d type=%q, want vmid=101 type=qemu", mq.upsertVMCalls[2].Vmid, mq.upsertVMCalls[2].Type)
	}

	// Verify storage.
	if len(mq.upsertStorageCalls) != 2 {
		t.Fatalf("expected 2 storage upserts, got %d", len(mq.upsertStorageCalls))
	}
	if mq.upsertStorageCalls[0].Storage != "local" {
		t.Errorf("storage[0] = %q, want %q", mq.upsertStorageCalls[0].Storage, "local")
	}
	if !mq.upsertStorageCalls[0].Active {
		t.Error("storage[0].Active should be true")
	}
}

func TestSyncCluster_VMMigration(t *testing.T) {
	// VM 100 is on pve1 in first sync, then moves to pve2 in second sync.
	callCount := 0
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{
				{Node: "pve1", Status: "online", MaxCPU: 8, MaxMem: 17179869184, MaxDisk: 107374182400},
				{Node: "pve2", Status: "online", MaxCPU: 16, MaxMem: 34359738368, MaxDisk: 214748364800},
			})
		},
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{Node: "pve1", CPUInfo: proxmox.CPUInfo{CPUs: 8}, Memory: proxmox.Memory{Total: 17179869184}, RootFS: proxmox.RootFS{Total: 107374182400}})
		},
		"/api2/json/nodes/pve2/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{Node: "pve2", CPUInfo: proxmox.CPUInfo{CPUs: 16}, Memory: proxmox.Memory{Total: 34359738368}, RootFS: proxmox.RootFS{Total: 214748364800}})
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			callCount++
			if callCount <= 1 {
				// First sync: VM on pve1.
				jsonResponse(w, []proxmox.VirtualMachine{
					{VMID: 100, Name: "web-server", Status: "running", CPUs: 4, MaxMem: 4294967296},
				})
			} else {
				// Second sync: VM migrated away.
				jsonResponse(w, []proxmox.VirtualMachine{})
			}
		},
		"/api2/json/nodes/pve2/qemu": func(w http.ResponseWriter, _ *http.Request) {
			if callCount <= 1 {
				jsonResponse(w, []proxmox.VirtualMachine{})
			} else {
				// Second sync: VM now on pve2.
				jsonResponse(w, []proxmox.VirtualMachine{
					{VMID: 100, Name: "web-server", Status: "running", CPUs: 4, MaxMem: 4294967296},
				})
			}
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{})
		},
		"/api2/json/nodes/pve2/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{})
		},
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{})
		},
		"/api2/json/nodes/pve2/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	// First sync.
	if err := syncer.SyncCluster(context.Background(), cluster); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	firstNodeID := mq.upsertVMCalls[0].NodeID

	// Second sync (VM migrated to pve2).
	if err := syncer.SyncCluster(context.Background(), cluster); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// The last upsert for VMID 100 should have a different NodeID.
	var lastVMCall db.UpsertVMParams
	for _, call := range mq.upsertVMCalls {
		if call.Vmid == 100 {
			lastVMCall = call
		}
	}

	if lastVMCall.NodeID == firstNodeID {
		t.Error("VM should have migrated to a different node_id after second sync")
	}
}

func TestSyncCluster_EmptyCluster(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("SyncCluster with empty cluster: %v", err)
	}

	if len(mq.upsertNodeCalls) != 0 {
		t.Errorf("expected 0 node upserts, got %d", len(mq.upsertNodeCalls))
	}
}

func TestSyncCluster_AuthFailure(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("permission denied"))
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	err := syncer.SyncCluster(context.Background(), cluster)
	if err == nil {
		t.Fatal("expected error for auth failure")
	}

	if !errors.Is(err, proxmox.ErrForbidden) {
		t.Errorf("expected ErrForbidden wrapped in error, got: %v", err)
	}
}

func TestSyncCluster_PartialNodeFailure(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{
				{Node: "pve1", Status: "online", MaxCPU: 8, MaxMem: 17179869184, MaxDisk: 107374182400},
				{Node: "pve2", Status: "online", MaxCPU: 16, MaxMem: 34359738368, MaxDisk: 214748364800},
			})
		},
		// pve1 status works.
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{Node: "pve1", CPUInfo: proxmox.CPUInfo{CPUs: 8}, Memory: proxmox.Memory{Total: 17179869184}, RootFS: proxmox.RootFS{Total: 107374182400}})
		},
		// pve2 status fails — node unreachable.
		"/api2/json/nodes/pve2/status": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("node unreachable"))
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 100, Name: "web-server", Status: "running"},
			})
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{})
		},
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{})
		},
		// pve2 qemu fails.
		"/api2/json/nodes/pve2/qemu": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("node unreachable"))
		},
		"/api2/json/nodes/pve2/lxc": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("node unreachable"))
		},
		"/api2/json/nodes/pve2/storage": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("node unreachable"))
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	// Should not return an error — partial failure is logged but doesn't fail the cluster sync.
	err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("SyncCluster with partial failure: %v", err)
	}

	// pve1 should still be synced successfully.
	if len(mq.upsertNodeCalls) != 2 {
		t.Fatalf("expected 2 node upserts (both attempted), got %d", len(mq.upsertNodeCalls))
	}

	// pve1's VM should be synced.
	foundVM100 := false
	for _, call := range mq.upsertVMCalls {
		if call.Vmid == 100 {
			foundVM100 = true
		}
	}
	if !foundVM100 {
		t.Error("expected VM 100 to be synced from pve1")
	}
}

func TestSyncCluster_DecryptFailure(t *testing.T) {
	mq := newMockQueries()
	syncer := &Syncer{
		queries:       mq,
		encryptionKey: testEncryptionKey,
		clientFactory: DefaultClientFactory,
		logger:        testLogger(),
	}

	cluster := db.Cluster{
		ID:                   uuid.New(),
		Name:                 "bad-cluster",
		TokenSecretEncrypted: "not-valid-base64-encrypted-data!!",
	}

	err := syncer.SyncCluster(context.Background(), cluster)
	if err == nil {
		t.Fatal("expected error for decrypt failure")
	}
}

func TestSyncAll(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{})
		},
	})
	defer srv.Close()

	cluster := makeCluster(t, srv.URL)
	mq := newMockQueries()
	mq.clusters = []db.Cluster{cluster}
	syncer := newTestSyncer(mq)

	err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
}
