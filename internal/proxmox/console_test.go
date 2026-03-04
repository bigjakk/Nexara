package proxmox

import (
	"context"
	"net/http"
	"testing"
)

func termProxyHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		jsonResponse(w, TermProxyResponse{
			Port:   FlexInt(5900),
			Ticket: "PVEVNC:test-ticket",
			UPID:   "UPID:node1:0001:0001:0001:vncproxy:0:user@pam:",
			User:   "user@pam",
		})
	}
}

func TestNodeTermProxy(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/node1/termproxy": termProxyHandler(t),
	})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	resp, err := client.NodeTermProxy(context.Background(), "node1")
	if err != nil {
		t.Fatalf("NodeTermProxy: %v", err)
	}
	if int(resp.Port) != 5900 {
		t.Errorf("expected port 5900, got %d", resp.Port)
	}
	if resp.Ticket != "PVEVNC:test-ticket" {
		t.Errorf("unexpected ticket: %s", resp.Ticket)
	}
}

func TestNodeTermProxy_EmptyNode(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	_, err := client.NodeTermProxy(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty node")
	}
}

func TestVMTermProxy(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/node1/qemu/100/termproxy": termProxyHandler(t),
	})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	resp, err := client.VMTermProxy(context.Background(), "node1", 100)
	if err != nil {
		t.Fatalf("VMTermProxy: %v", err)
	}
	if int(resp.Port) != 5900 {
		t.Errorf("expected port 5900, got %d", resp.Port)
	}
}

func TestVMTermProxy_InvalidVMID(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	_, err := client.VMTermProxy(context.Background(), "node1", -1)
	if err == nil {
		t.Fatal("expected error for invalid VMID")
	}
}

func TestCTTermProxy(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/node1/lxc/200/termproxy": termProxyHandler(t),
	})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	resp, err := client.CTTermProxy(context.Background(), "node1", 200)
	if err != nil {
		t.Fatalf("CTTermProxy: %v", err)
	}
	if int(resp.Port) != 5900 {
		t.Errorf("expected port 5900, got %d", resp.Port)
	}
}

func TestCTTermProxy_EmptyNode(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	_, err := client.CTTermProxy(context.Background(), "", 200)
	if err == nil {
		t.Fatal("expected error for empty node")
	}
}
