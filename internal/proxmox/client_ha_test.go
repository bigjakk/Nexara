package proxmox

import (
	"context"
	"net/http"
	"testing"
)

func TestArmHA(t *testing.T) {
	called := false
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/ha/status/arm-ha": func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			jsonResponse(w, nil)
		},
	})
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if err := c.ArmHA(context.Background()); err != nil {
		t.Fatalf("ArmHA: %v", err)
	}
	if !called {
		t.Error("arm-ha endpoint was not called")
	}
}

func TestDisarmHA(t *testing.T) {
	var gotMode string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/ha/status/disarm-ha": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			gotMode = r.FormValue("resource-mode")
			jsonResponse(w, nil)
		},
	})
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if err := c.DisarmHA(context.Background(), "ignore"); err != nil {
		t.Fatalf("DisarmHA: %v", err)
	}
	if gotMode != "ignore" {
		t.Errorf("resource-mode = %q, want ignore", gotMode)
	}
}
