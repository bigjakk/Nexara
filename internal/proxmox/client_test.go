package proxmox

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer creates a test HTTP server with the given route handlers.
func newTestServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, handler := range routes {
		mux.HandleFunc(pattern, handler)
	}
	return httptest.NewServer(mux)
}

// newTestClient creates a Client pointing at the given test server URL.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	c, err := NewClient(ClientConfig{
		BaseURL:     serverURL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret-token-value",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// jsonResponse wraps data in a Proxmox API envelope and writes it.
func jsonResponse(w http.ResponseWriter, data interface{}) {
	raw, _ := json.Marshal(data)
	resp := map[string]json.RawMessage{"data": raw}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// --- Constructor validation ---

func TestNewClient_MissingBaseURL(t *testing.T) {
	_, err := NewClient(ClientConfig{
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err == nil {
		t.Fatal("expected error for missing BaseURL")
	}
}

func TestNewClient_MissingTokenID(t *testing.T) {
	_, err := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenSecret: "secret",
	})
	if err == nil {
		t.Fatal("expected error for missing TokenID")
	}
}

func TestNewClient_MissingTokenSecret(t *testing.T) {
	_, err := NewClient(ClientConfig{
		BaseURL: "https://pve.example.com:8006",
		TokenID: "user@pam!test",
	})
	if err == nil {
		t.Fatal("expected error for missing TokenSecret")
	}
}

func TestNewClient_TrailingSlashNormalized(t *testing.T) {
	c, err := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006/",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("baseURL should not have trailing slash, got %q", c.baseURL)
	}
}

// --- Auth header ---

func TestAuthHeaderFormat(t *testing.T) {
	var gotAuth string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			jsonResponse(w, []NodeListEntry{})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, _ = c.GetNodes(context.Background())

	expected := "PVEAPIToken=user@pam!test=secret-token-value"
	if gotAuth != expected {
		t.Errorf("auth header = %q, want %q", gotAuth, expected)
	}
}

// --- GetNodes ---

func TestGetNodes(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []NodeListEntry{
				{Node: "pve1", Status: "online", CPU: 0.15, MaxCPU: 8, Mem: 4294967296, MaxMem: 17179869184, Uptime: 86400},
				{Node: "pve2", Status: "online", CPU: 0.45, MaxCPU: 16, Mem: 8589934592, MaxMem: 34359738368, Uptime: 172800},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	nodes, err := c.GetNodes(context.Background())
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	if nodes[0].Node != "pve1" {
		t.Errorf("nodes[0].Node = %q, want %q", nodes[0].Node, "pve1")
	}
	if nodes[1].MaxCPU != 16 {
		t.Errorf("nodes[1].MaxCPU = %d, want 16", nodes[1].MaxCPU)
	}
}

// --- GetNodeStatus ---

func TestGetNodeStatus(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, NodeStatus{
				Node:       "pve1",
				Uptime:     86400,
				PVEVersion: "pve-manager/8.1.3/ec5affc9",
				CPU:        0.12,
				CPUInfo:    CPUInfo{Cores: 4, CPUs: 8, Model: "Intel Xeon E5-2680", Sockets: 1, Threads: 2},
				Memory:     Memory{Total: 17179869184, Used: 4294967296, Free: 12884901888},
				RootFS:     RootFS{Total: 107374182400, Used: 21474836480, Free: 85899345920, Avail: 80530636800},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	status, err := c.GetNodeStatus(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetNodeStatus: %v", err)
	}
	if status.Node != "pve1" {
		t.Errorf("Node = %q, want %q", status.Node, "pve1")
	}
	if status.CPUInfo.Cores != 4 {
		t.Errorf("CPUInfo.Cores = %d, want 4", status.CPUInfo.Cores)
	}
	if status.Memory.Total != 17179869184 {
		t.Errorf("Memory.Total = %d, want 17179869184", status.Memory.Total)
	}
}

// --- GetVMs ---

func TestGetVMs(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []VirtualMachine{
				{VMID: 100, Name: "web-server", Status: "running", CPUs: 4, Mem: 2147483648, MaxMem: 4294967296},
				{VMID: 101, Name: "db-server", Status: "stopped", CPUs: 2, Mem: 0, MaxMem: 8589934592},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	vms, err := c.GetVMs(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetVMs: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("got %d VMs, want 2", len(vms))
	}
	if vms[0].Name != "web-server" {
		t.Errorf("vms[0].Name = %q, want %q", vms[0].Name, "web-server")
	}
	// Verify Node backfill
	for i, vm := range vms {
		if vm.Node != "pve1" {
			t.Errorf("vms[%d].Node = %q, want %q", i, vm.Node, "pve1")
		}
	}
}

// --- GetContainers ---

func TestGetContainers(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []Container{
				{VMID: 200, Name: "dns-ct", Status: "running", CPUs: 1, Mem: 268435456, MaxMem: 536870912},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cts, err := c.GetContainers(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetContainers: %v", err)
	}
	if len(cts) != 1 {
		t.Fatalf("got %d containers, want 1", len(cts))
	}
	if cts[0].VMID != 200 {
		t.Errorf("cts[0].VMID = %d, want 200", cts[0].VMID)
	}
	if cts[0].Node != "pve1" {
		t.Errorf("cts[0].Node = %q, want %q", cts[0].Node, "pve1")
	}
}

// --- GetClusterResources ---

func TestGetClusterResources(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/resources": func(w http.ResponseWriter, r *http.Request) {
			resType := r.URL.Query().Get("type")
			resources := []ClusterResource{
				{ID: "node/pve1", Type: ResourceTypeNode, Node: "pve1", Status: "online"},
				{ID: "qemu/100", Type: ResourceTypeQEMU, Node: "pve1", VMID: 100, Name: "web-server", Status: "running"},
				{ID: "lxc/200", Type: ResourceTypeLXC, Node: "pve1", VMID: 200, Name: "dns-ct", Status: "running"},
			}
			if resType != "" {
				var filtered []ClusterResource
				for _, r := range resources {
					if r.Type == resType {
						filtered = append(filtered, r)
					}
				}
				resources = filtered
			}
			jsonResponse(w, resources)
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	// All resources
	all, err := c.GetClusterResources(context.Background(), "")
	if err != nil {
		t.Fatalf("GetClusterResources (all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d resources, want 3", len(all))
	}

	// Filtered
	qemuOnly, err := c.GetClusterResources(context.Background(), ResourceTypeQEMU)
	if err != nil {
		t.Fatalf("GetClusterResources (qemu): %v", err)
	}
	if len(qemuOnly) != 1 {
		t.Fatalf("got %d qemu resources, want 1", len(qemuOnly))
	}
	if qemuOnly[0].VMID != 100 {
		t.Errorf("qemuOnly[0].VMID = %d, want 100", qemuOnly[0].VMID)
	}
}

// --- GetStoragePools ---

func TestGetStoragePools(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []StoragePool{
				{Storage: "local", Type: "dir", Content: "images,rootdir", Active: 1, Enabled: 1, Total: 107374182400, Used: 21474836480, Avail: 85899345920},
				{Storage: "local-lvm", Type: "lvmthin", Content: "images,rootdir", Active: 1, Enabled: 1, Total: 214748364800, Used: 42949672960, Avail: 171798691840},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	pools, err := c.GetStoragePools(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetStoragePools: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("got %d pools, want 2", len(pools))
	}
	if pools[0].Storage != "local" {
		t.Errorf("pools[0].Storage = %q, want %q", pools[0].Storage, "local")
	}
}

// --- GetClusterStatus ---

func TestGetClusterStatus(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []ClusterStatusEntry{
				{Type: "cluster", ID: "cluster", Name: "proxmox-cluster", Version: 3, Quorate: 1, Nodes: 2},
				{Type: "node", ID: "node/pve1", Name: "pve1", IP: "10.0.0.1", Online: 1, NodeID: 1},
				{Type: "node", ID: "node/pve2", Name: "pve2", IP: "10.0.0.2", Online: 1, NodeID: 2},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	entries, err := c.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Type != "cluster" {
		t.Errorf("entries[0].Type = %q, want %q", entries[0].Type, "cluster")
	}
	if entries[1].IP != "10.0.0.1" {
		t.Errorf("entries[1].IP = %q, want %q", entries[1].IP, "10.0.0.1")
	}
}

// --- HTTP error mapping ---

func TestHTTPError_401_Forbidden(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("no ticket"))
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodes(context.Background())
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestHTTPError_403_Forbidden(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("permission denied"))
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodes(context.Background())
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestHTTPError_404_NotFound(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/nonexistent/status": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodeStatus(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestHTTPError_500_APIError(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal server error"))
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodes(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
}

// --- Connection error ---

func TestConnectionError(t *testing.T) {
	c, err := NewClient(ClientConfig{
		BaseURL:     "http://127.0.0.1:1", // closed port
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.GetNodes(context.Background())
	if !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got %v", err)
	}
}

// --- Invalid JSON ---

func TestInvalidJSON(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("this is not json"))
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodes(context.Background())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("expected ErrInvalidResponse, got %v", err)
	}
}

func TestInvalidDataJSON(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data": "not an array"}`))
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodes(context.Background())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("expected ErrInvalidResponse, got %v", err)
	}
}

// --- TLS fingerprint ---

func TestTLSFingerprint_Match(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, []NodeListEntry{{Node: "pve1", Status: "online"}})
	}))
	defer srv.Close()

	// Extract the server's certificate fingerprint.
	cert := srv.TLS.Certificates[0].Certificate[0]
	sum := sha256.Sum256(cert)
	fingerprint := hex.EncodeToString(sum[:])

	c, err := NewClient(ClientConfig{
		BaseURL:        srv.URL,
		TokenID:        "user@pam!test",
		TokenSecret:    "secret",
		TLSFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	nodes, err := c.GetNodes(context.Background())
	if err != nil {
		t.Fatalf("GetNodes with matching fingerprint: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
}

func TestTLSFingerprint_Mismatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, []NodeListEntry{})
	}))
	defer srv.Close()

	wrongFingerprint := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes

	c, err := NewClient(ClientConfig{
		BaseURL:        srv.URL,
		TokenID:        "user@pam!test",
		TokenSecret:    "secret",
		TLSFingerprint: wrongFingerprint,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.GetNodes(context.Background())
	if err == nil {
		t.Fatal("expected error for fingerprint mismatch")
	}
	if !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got %v", err)
	}
}

func TestTLSFingerprint_ColonFormat(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, []NodeListEntry{{Node: "pve1", Status: "online"}})
	}))
	defer srv.Close()

	cert := srv.TLS.Certificates[0].Certificate[0]
	sum := sha256.Sum256(cert)
	hexStr := hex.EncodeToString(sum[:])

	// Build colon-separated uppercase fingerprint (like Proxmox shows).
	var parts []string
	for i := 0; i < len(hexStr); i += 2 {
		parts = append(parts, strings.ToUpper(hexStr[i:i+2]))
	}
	colonFingerprint := strings.Join(parts, ":")

	c, err := NewClient(ClientConfig{
		BaseURL:        srv.URL,
		TokenID:        "user@pam!test",
		TokenSecret:    "secret",
		TLSFingerprint: colonFingerprint,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	nodes, err := c.GetNodes(context.Background())
	if err != nil {
		t.Fatalf("GetNodes with colon fingerprint: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
}

// --- formatFingerprint ---

func TestFormatFingerprint(t *testing.T) {
	data := []byte("test certificate data")
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])

	got := formatFingerprint(data)
	if got != expected {
		t.Errorf("formatFingerprint = %q, want %q", got, expected)
	}
}

// --- Context cancellation ---

func TestContextCancellation(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []NodeListEntry{})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.GetNodes(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// --- Verify TLS config is used in non-fingerprint mode ---

func TestNewClient_NoFingerprint_UsesSystemCA(t *testing.T) {
	c, err := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}

	// Without a fingerprint, InsecureSkipVerify should be false (use system CAs).
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false without TLSFingerprint")
	}
}

// --- Node name validation ---

func TestValidateNodeName_Empty(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetNodeStatus(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty node name")
	}
}

func TestValidateNodeName_PathTraversal(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	for _, name := range []string{"../etc", "pve1/../admin", "foo/bar"} {
		_, err := c.GetNodeStatus(context.Background(), name)
		if err == nil {
			t.Errorf("expected error for node name %q", name)
		}
	}
}

// --- Verify TLS min version ---

func TestNewClient_TLSMinVersion(t *testing.T) {
	c, err := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d (TLS 1.2)", transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
}

// --- doPost ---

func TestDoPost_Success(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/status/start": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			jsonResponse(w, "UPID:pve1:00001234:0001ABCD:65000000:qmstart:100:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.StartVM(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("StartVM: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
	if !strings.HasPrefix(upid, "UPID:pve1:") {
		t.Errorf("unexpected UPID format: %q", upid)
	}
}

func TestDoPost_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{"404", http.StatusNotFound, ErrNotFound},
		{"401", http.StatusUnauthorized, ErrForbidden},
		{"403", http.StatusForbidden, ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/qemu/100/status/start": func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.statusCode)
					_, _ = w.Write([]byte("error"))
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			_, err := c.StartVM(context.Background(), "pve1", 100)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// --- VM lifecycle actions ---

func TestVMStatusActions(t *testing.T) {
	actions := []string{"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume"}

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/qemu/100/status/" + action: func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					jsonResponse(w, "UPID:pve1:000012:00AB:65000000:qm"+action+":100:user@pam:")
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			var upid string
			var err error

			switch action {
			case "start":
				upid, err = c.StartVM(context.Background(), "pve1", 100)
			case "stop":
				upid, err = c.StopVM(context.Background(), "pve1", 100)
			case "shutdown":
				upid, err = c.ShutdownVM(context.Background(), "pve1", 100)
			case "reboot":
				upid, err = c.RebootVM(context.Background(), "pve1", 100)
			case "reset":
				upid, err = c.ResetVM(context.Background(), "pve1", 100)
			case "suspend":
				upid, err = c.SuspendVM(context.Background(), "pve1", 100)
			case "resume":
				upid, err = c.ResumeVM(context.Background(), "pve1", 100)
			}

			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			if upid == "" {
				t.Errorf("%s: expected non-empty UPID", action)
			}
		})
	}
}

func TestVMStatusAction_InvalidNode(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.StartVM(context.Background(), "", 100)
	if err == nil {
		t.Fatal("expected error for empty node")
	}

	_, err = c.StartVM(context.Background(), "../etc", 100)
	if err == nil {
		t.Fatal("expected error for path traversal node")
	}
}

func TestVMStatusAction_InvalidVMID(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.StartVM(context.Background(), "pve1", 0)
	if err == nil {
		t.Fatal("expected error for VMID 0")
	}

	_, err = c.StartVM(context.Background(), "pve1", -1)
	if err == nil {
		t.Fatal("expected error for negative VMID")
	}
}

// --- CloneVM ---

func TestCloneVM(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/clone": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("expected form content type, got %q", ct)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.FormValue("newid") != "200" {
				t.Errorf("newid = %q, want 200", r.FormValue("newid"))
			}
			if r.FormValue("name") != "clone-test" {
				t.Errorf("name = %q, want clone-test", r.FormValue("name"))
			}
			if r.FormValue("full") != "1" {
				t.Errorf("full = %q, want 1", r.FormValue("full"))
			}
			jsonResponse(w, "UPID:pve1:000012:00AB:65000000:qmclone:100:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.CloneVM(context.Background(), "pve1", 100, CloneParams{
		NewID: 200,
		Name:  "clone-test",
		Full:  true,
	})
	if err != nil {
		t.Fatalf("CloneVM: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
}

func TestCloneVM_MissingNewID(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.CloneVM(context.Background(), "pve1", 100, CloneParams{})
	if err == nil {
		t.Fatal("expected error for missing newid")
	}
}

// --- DestroyVM ---

func TestDestroyVM(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("expected DELETE, got %s", r.Method)
			}
			jsonResponse(w, "UPID:pve1:000012:00AB:65000000:qmdestroy:100:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.DestroyVM(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("DestroyVM: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
}

// --- GetTaskStatus ---

func TestGetTaskStatus(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/tasks/UPID%3Apve1%3A000012%3A00AB%3A65000000%3Aqmstart%3A100%3Auser%40pam%3A/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, TaskStatus{
				Status:     "stopped",
				ExitStatus: "OK",
				Type:       "qmstart",
				UPID:       "UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:",
				Node:       "pve1",
				PID:        18,
				StartTime:  1694649344,
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	status, err := c.GetTaskStatus(context.Background(), "pve1", "UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:")
	if err != nil {
		t.Fatalf("GetTaskStatus: %v", err)
	}
	if status.Status != "stopped" {
		t.Errorf("Status = %q, want stopped", status.Status)
	}
	if status.ExitStatus != "OK" {
		t.Errorf("ExitStatus = %q, want OK", status.ExitStatus)
	}
}

func TestGetTaskStatus_EmptyUPID(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.GetTaskStatus(context.Background(), "pve1", "")
	if err == nil {
		t.Fatal("expected error for empty UPID")
	}
}

// --- CT lifecycle actions ---

func TestCTStatusActions(t *testing.T) {
	actions := []string{"start", "stop", "shutdown", "reboot", "suspend", "resume"}

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/lxc/200/status/" + action: func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					jsonResponse(w, "UPID:pve1:000012:00AB:65000000:vz"+action+":200:user@pam:")
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			var upid string
			var err error

			switch action {
			case "start":
				upid, err = c.StartCT(context.Background(), "pve1", 200)
			case "stop":
				upid, err = c.StopCT(context.Background(), "pve1", 200)
			case "shutdown":
				upid, err = c.ShutdownCT(context.Background(), "pve1", 200)
			case "reboot":
				upid, err = c.RebootCT(context.Background(), "pve1", 200)
			case "suspend":
				upid, err = c.SuspendCT(context.Background(), "pve1", 200)
			case "resume":
				upid, err = c.ResumeCT(context.Background(), "pve1", 200)
			}

			if err != nil {
				t.Fatalf("%s CT: %v", action, err)
			}
			if upid == "" {
				t.Errorf("%s CT: expected non-empty UPID", action)
			}
		})
	}
}

func TestCTStatusAction_InvalidNode(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.StartCT(context.Background(), "", 200)
	if err == nil {
		t.Fatal("expected error for empty node")
	}

	_, err = c.StartCT(context.Background(), "../etc", 200)
	if err == nil {
		t.Fatal("expected error for path traversal node")
	}
}

func TestCTStatusAction_InvalidVMID(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.StartCT(context.Background(), "pve1", 0)
	if err == nil {
		t.Fatal("expected error for VMID 0")
	}

	_, err = c.StartCT(context.Background(), "pve1", -1)
	if err == nil {
		t.Fatal("expected error for negative VMID")
	}
}

// --- CloneCT ---

func TestCloneCT(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/lxc/200/clone": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.FormValue("newid") != "300" {
				t.Errorf("newid = %q, want 300", r.FormValue("newid"))
			}
			if r.FormValue("hostname") != "clone-ct" {
				t.Errorf("hostname = %q, want clone-ct", r.FormValue("hostname"))
			}
			if r.FormValue("full") != "1" {
				t.Errorf("full = %q, want 1", r.FormValue("full"))
			}
			jsonResponse(w, "UPID:pve1:000012:00AB:65000000:vzclone:200:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.CloneCT(context.Background(), "pve1", 200, CloneParams{
		NewID: 300,
		Name:  "clone-ct",
		Full:  true,
	})
	if err != nil {
		t.Fatalf("CloneCT: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
}

func TestCloneCT_MissingNewID(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.CloneCT(context.Background(), "pve1", 200, CloneParams{})
	if err == nil {
		t.Fatal("expected error for missing newid")
	}
}

// --- DestroyCT ---

func TestDestroyCT(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/lxc/200": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("expected DELETE, got %s", r.Method)
			}
			jsonResponse(w, "UPID:pve1:000012:00AB:65000000:vzdestroy:200:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.DestroyCT(context.Background(), "pve1", 200)
	if err != nil {
		t.Fatalf("DestroyCT: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
}

// --- MigrateCT ---

func TestMigrateCT(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/lxc/200/migrate": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.FormValue("target") != "pve2" {
				t.Errorf("target = %q, want pve2", r.FormValue("target"))
			}
			if r.FormValue("restart") != "1" {
				t.Errorf("restart = %q, want 1", r.FormValue("restart"))
			}
			jsonResponse(w, "UPID:pve1:000012:00AB:65000000:vzmigrate:200:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.MigrateCT(context.Background(), "pve1", 200, MigrateParams{
		Target: "pve2",
		Online: true,
	})
	if err != nil {
		t.Fatalf("MigrateCT: %v", err)
	}
	if upid == "" {
		t.Error("expected non-empty UPID")
	}
}

func TestMigrateCT_MissingTarget(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "https://pve.example.com:8006",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})

	_, err := c.MigrateCT(context.Background(), "pve1", 200, MigrateParams{})
	if err == nil {
		t.Fatal("expected error for missing target")
	}
}

// --- validateVMID ---

func TestValidateVMID(t *testing.T) {
	tests := []struct {
		vmid    int
		wantErr bool
	}{
		{100, false},
		{1, false},
		{0, true},
		{-1, true},
	}
	for _, tt := range tests {
		err := validateVMID(tt.vmid)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateVMID(%d): err=%v, wantErr=%v", tt.vmid, err, tt.wantErr)
		}
	}
}
