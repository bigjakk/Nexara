package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
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
	key := fmt.Sprintf("%s:%d", arg.ClusterID.String(), arg.Vmid)
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

func (m *mockQueries) GetNodeByClusterAndName(_ context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
	key := arg.ClusterID.String() + ":" + arg.Name
	node, ok := m.nodes[key]
	if !ok {
		return db.Node{}, errors.New("node not found")
	}
	return node, nil
}

func (m *mockQueries) ListVMStatusesByCluster(_ context.Context, _ uuid.UUID) ([]db.ListVMStatusesByClusterRow, error) {
	return nil, nil
}

func (m *mockQueries) DeleteStaleVMs(_ context.Context, _ db.DeleteStaleVMsParams) error {
	return nil
}

func (m *mockQueries) UpdateNodeAddress(_ context.Context, _ db.UpdateNodeAddressParams) error {
	return nil
}

// PBS mock methods (no-op stubs for interface compliance).
func (m *mockQueries) ListActivePBSServers(_ context.Context) ([]db.PbsServer, error) {
	return nil, nil
}

func (m *mockQueries) UpsertPBSSnapshot(_ context.Context, _ db.UpsertPBSSnapshotParams) (db.PbsSnapshot, error) {
	return db.PbsSnapshot{}, nil
}

func (m *mockQueries) UpsertPBSSyncJob(_ context.Context, _ db.UpsertPBSSyncJobParams) (db.PbsSyncJob, error) {
	return db.PbsSyncJob{}, nil
}

func (m *mockQueries) UpsertPBSVerifyJob(_ context.Context, _ db.UpsertPBSVerifyJobParams) (db.PbsVerifyJob, error) {
	return db.PbsVerifyJob{}, nil
}

func (m *mockQueries) DeleteStalePBSSnapshots(_ context.Context, _ db.DeleteStalePBSSnapshotsParams) error {
	return nil
}

func (m *mockQueries) DeleteStalePBSSyncJobs(_ context.Context, _ db.DeleteStalePBSSyncJobsParams) error {
	return nil
}

func (m *mockQueries) DeleteStalePBSVerifyJobs(_ context.Context, _ db.DeleteStalePBSVerifyJobsParams) error {
	return nil
}

func (m *mockQueries) InsertAuditLog(_ context.Context, _ db.InsertAuditLogParams) error {
	return nil
}

func (m *mockQueries) InsertAuditLogWithSource(_ context.Context, _ db.InsertAuditLogWithSourceParams) error {
	return nil
}

func (m *mockQueries) GetTaskSyncState(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, errors.New("no sync state")
}

func (m *mockQueries) UpsertTaskSyncState(_ context.Context, _ db.UpsertTaskSyncStateParams) error {
	return nil
}

func (m *mockQueries) ExistsTaskHistoryByUPID(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockQueries) ExistsAuditLogByUPID(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockQueries) GetVMByClusterAndVmid(_ context.Context, _ db.GetVMByClusterAndVmidParams) (db.Vm, error) {
	return db.Vm{}, fmt.Errorf("not found")
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
				CPU:        0.25,
				Memory:     proxmox.Memory{Total: 17179869184, Used: 8589934592},
				RootFS:     proxmox.RootFS{Total: 107374182400},
			})
		},
		"/api2/json/nodes/pve2/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{
				Node:       "pve2",
				PVEVersion: "pve-manager/8.1.3",
				CPUInfo:    proxmox.CPUInfo{CPUs: 16},
				CPU:        0.10,
				Memory:     proxmox.Memory{Total: 34359738368, Used: 17179869184},
				RootFS:     proxmox.RootFS{Total: 214748364800},
			})
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 100, Name: "web-server", Status: "running", CPUs: 4, MaxMem: 4294967296, MaxDisk: 53687091200, Uptime: 3600,
					CPU: 0.5, Mem: 2147483648, NetIn: 1000, NetOut: 2000, DiskRead: 3000, DiskWrite: 4000},
			})
		},
		"/api2/json/nodes/pve2/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 101, Name: "db-server", Status: "running", CPUs: 8, MaxMem: 8589934592, MaxDisk: 107374182400, Template: 0,
					CPU: 0.8, Mem: 4294967296, NetIn: 5000, NetOut: 6000, DiskRead: 7000, DiskWrite: 8000},
			})
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{
				{VMID: 200, Name: "dns-ct", Status: "running", CPUs: 1, MaxMem: 536870912, MaxDisk: 10737418240,
					CPU: 0.1, Mem: 268435456, NetIn: 500, NetOut: 600, DiskRead: 700, DiskWrite: 800},
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

	result, err := syncer.SyncCluster(context.Background(), cluster)
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

	// Verify metric result.
	if result == nil {
		t.Fatal("expected non-nil metric result")
	}
	if len(result.NodeMetrics) != 2 {
		t.Fatalf("expected 2 node metrics, got %d", len(result.NodeMetrics))
	}
	if len(result.VMMetrics) != 3 {
		t.Fatalf("expected 3 VM metrics, got %d", len(result.VMMetrics))
	}

	// Verify pve1 node metrics.
	nm1 := result.NodeMetrics[0]
	if nm1.CPUUsage != 0.25 {
		t.Errorf("pve1 CPUUsage = %f, want 0.25", nm1.CPUUsage)
	}
	if nm1.MemUsed != 8589934592 {
		t.Errorf("pve1 MemUsed = %d, want 8589934592", nm1.MemUsed)
	}
	if nm1.MemTotal != 17179869184 {
		t.Errorf("pve1 MemTotal = %d, want 17179869184", nm1.MemTotal)
	}
	// pve1 disk I/O = VM 100 (3000/4000) + CT 200 (700/800) = 3700/4800.
	if nm1.DiskRead != 3700 {
		t.Errorf("pve1 DiskRead = %d, want 3700", nm1.DiskRead)
	}
	if nm1.DiskWrite != 4800 {
		t.Errorf("pve1 DiskWrite = %d, want 4800", nm1.DiskWrite)
	}
	// pve1 net I/O = VM 100 (1000/2000) + CT 200 (500/600) = 1500/2600.
	if nm1.NetIn != 1500 {
		t.Errorf("pve1 NetIn = %d, want 1500", nm1.NetIn)
	}
	if nm1.NetOut != 2600 {
		t.Errorf("pve1 NetOut = %d, want 2600", nm1.NetOut)
	}

	// Verify pve2 node metrics.
	nm2 := result.NodeMetrics[1]
	if nm2.CPUUsage != 0.10 {
		t.Errorf("pve2 CPUUsage = %f, want 0.10", nm2.CPUUsage)
	}
	if nm2.DiskRead != 7000 {
		t.Errorf("pve2 DiskRead = %d, want 7000", nm2.DiskRead)
	}
	if nm2.NetIn != 5000 {
		t.Errorf("pve2 NetIn = %d, want 5000", nm2.NetIn)
	}

	// Verify VM metric snapshots have correct CPU values.
	if result.VMMetrics[0].CPUUsage != 0.5 {
		t.Errorf("VM 100 CPUUsage = %f, want 0.5", result.VMMetrics[0].CPUUsage)
	}
	if result.VMMetrics[1].CPUUsage != 0.1 {
		t.Errorf("CT 200 CPUUsage = %f, want 0.1", result.VMMetrics[1].CPUUsage)
	}
	if result.VMMetrics[2].CPUUsage != 0.8 {
		t.Errorf("VM 101 CPUUsage = %f, want 0.8", result.VMMetrics[2].CPUUsage)
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
	_, err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	firstNodeID := mq.upsertVMCalls[0].NodeID

	// Second sync (VM migrated to pve2).
	_, err = syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
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

	result, err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("SyncCluster with empty cluster: %v", err)
	}

	if len(mq.upsertNodeCalls) != 0 {
		t.Errorf("expected 0 node upserts, got %d", len(mq.upsertNodeCalls))
	}

	if result == nil {
		t.Fatal("expected non-nil result even for empty cluster")
	}
	if len(result.NodeMetrics) != 0 {
		t.Errorf("expected 0 node metrics, got %d", len(result.NodeMetrics))
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

	_, err := syncer.SyncCluster(context.Background(), cluster)
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
	result, err := syncer.SyncCluster(context.Background(), cluster)
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

	// Metric result should have both nodes (pve2 used fallback data).
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.NodeMetrics) != 2 {
		t.Errorf("expected 2 node metrics, got %d", len(result.NodeMetrics))
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

	_, err := syncer.SyncCluster(context.Background(), cluster)
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

	results := syncer.SyncAll(context.Background())
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ClusterID != cluster.ID {
		t.Errorf("result cluster ID = %s, want %s", results[0].ClusterID, cluster.ID)
	}
}

func TestSyncCluster_MetricSnapshots(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{
				{Node: "pve1", Status: "online", MaxCPU: 4, MaxMem: 8589934592, MaxDisk: 53687091200},
			})
		},
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{
				Node:    "pve1",
				CPUInfo: proxmox.CPUInfo{CPUs: 4},
				CPU:     0.75,
				Memory:  proxmox.Memory{Total: 8589934592, Used: 6442450944},
				RootFS:  proxmox.RootFS{Total: 53687091200},
			})
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{
				{VMID: 100, Name: "vm1", Status: "running", CPUs: 2, MaxMem: 4294967296,
					CPU: 0.50, Mem: 2147483648, DiskRead: 100, DiskWrite: 200, NetIn: 300, NetOut: 400},
				{VMID: 101, Name: "vm2", Status: "stopped", CPUs: 1, MaxMem: 2147483648,
					CPU: 0, Mem: 0, DiskRead: 0, DiskWrite: 0, NetIn: 0, NetOut: 0},
			})
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{
				{VMID: 200, Name: "ct1", Status: "running", CPUs: 1, MaxMem: 1073741824,
					CPU: 0.20, Mem: 536870912, DiskRead: 50, DiskWrite: 60, NetIn: 70, NetOut: 80},
			})
		},
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	syncer := newTestSyncer(mq)
	cluster := makeCluster(t, srv.URL)

	result, err := syncer.SyncCluster(context.Background(), cluster)
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}

	// 1 node metric.
	if len(result.NodeMetrics) != 1 {
		t.Fatalf("expected 1 node metric, got %d", len(result.NodeMetrics))
	}
	nm := result.NodeMetrics[0]
	if nm.CPUUsage != 0.75 {
		t.Errorf("node CPUUsage = %f, want 0.75", nm.CPUUsage)
	}
	if nm.MemUsed != 6442450944 {
		t.Errorf("node MemUsed = %d, want 6442450944", nm.MemUsed)
	}
	// Node disk I/O = VM1 (100/200) + CT1 (50/60) = 150/260.
	if nm.DiskRead != 150 {
		t.Errorf("node DiskRead = %d, want 150", nm.DiskRead)
	}
	if nm.DiskWrite != 260 {
		t.Errorf("node DiskWrite = %d, want 260", nm.DiskWrite)
	}
	// Node net I/O = VM1 (300/400) + CT1 (70/80) = 370/480.
	if nm.NetIn != 370 {
		t.Errorf("node NetIn = %d, want 370", nm.NetIn)
	}
	if nm.NetOut != 480 {
		t.Errorf("node NetOut = %d, want 480", nm.NetOut)
	}

	// 3 VM metrics (2 QEMU + 1 LXC).
	if len(result.VMMetrics) != 3 {
		t.Fatalf("expected 3 VM metrics, got %d", len(result.VMMetrics))
	}
	// VM1.
	if result.VMMetrics[0].CPUUsage != 0.50 {
		t.Errorf("VM1 CPUUsage = %f, want 0.50", result.VMMetrics[0].CPUUsage)
	}
	if result.VMMetrics[0].MemUsed != 2147483648 {
		t.Errorf("VM1 MemUsed = %d, want 2147483648", result.VMMetrics[0].MemUsed)
	}
	// VM2 (stopped).
	if result.VMMetrics[1].CPUUsage != 0 {
		t.Errorf("VM2 CPUUsage = %f, want 0", result.VMMetrics[1].CPUUsage)
	}
	// CT1.
	if result.VMMetrics[2].CPUUsage != 0.20 {
		t.Errorf("CT1 CPUUsage = %f, want 0.20", result.VMMetrics[2].CPUUsage)
	}
	if result.VMMetrics[2].MemUsed != 536870912 {
		t.Errorf("CT1 MemUsed = %d, want 536870912", result.VMMetrics[2].MemUsed)
	}
}
