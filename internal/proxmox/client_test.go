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
