package proxmox

import (
	"context"
	"net/http"
	"net/url"
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

func TestValidateHAResourceID(t *testing.T) {
	tests := []struct {
		name    string
		sid     string
		wantErr bool
	}{
		{"vm", "vm:100", false},
		{"container", "ct:101", false},
		{"bare vmid", "100", false},
		{"max vmid", "999999999", false},

		{"traversal", "../../../../access/users/root@pam", true},
		{"traversal after type", "vm:../../../access", true},
		{"encoded traversal", "vm:%2e%2e%2faccess", true},
		{"query injection", "vm:100?force=1", true},
		{"fragment injection", "vm:100#x", true},
		{"slash", "vm:100/status", true},
		{"empty", "", true},
		{"type only", "vm:", true},
		{"non-numeric name", "vm:abc", true},
		{"uppercase type", "VM:100", true},
		{"newline", "vm:100\nX-Injected: 1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHAResourceID(tt.sid)
			if tt.wantErr && err == nil {
				t.Errorf("validateHAResourceID(%q) = nil, want an error", tt.sid)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateHAResourceID(%q) = %v, want nil", tt.sid, err)
			}
		})
	}
}

func TestGetHAResource_SendsSIDLiterally(t *testing.T) {
	// Proxmox rejects a percent-encoded colon here, so the SID must reach it raw
	// — which is exactly why validateHAResourceID has to carry the safety.
	srv, seen := newCaptureServer(t, `{"data":{"sid":"vm:100"}}`)
	c := newTestClient(t, srv.URL)

	if _, err := c.GetHAResource(context.Background(), "vm:100"); err != nil {
		t.Fatalf("GetHAResource: %v", err)
	}

	want := "/api2/json/cluster/ha/resources/vm:100"
	if len(*seen) != 1 || (*seen)[0] != want {
		t.Errorf("request target = %v, want [%s]", *seen, want)
	}
}

func TestHAResourceMethods_RejectInjectionWithoutIssuingRequest(t *testing.T) {
	// Three "..", not four, and the count is worked out rather than guessed:
	// all three methods build /api2/json/cluster/ha/resources/{sid}
	// (TestGetHAResource_SendsSIDLiterally pins that target), so the directory
	// containing the sid is three segments below /api2/json. The sid goes out
	// raw, so a normalising proxy in front of pveproxy would resolve the dots
	// (pveproxy itself reads the first ".." as the sid and finds no child ".."
	// below it — see validatePathSegment), and there three pops land on
	// /api2/json/access/users/root@pam, which is a real endpoint reachable
	// with the cluster's own token. A fourth over-pops to
	// /api2/access/users/root@pam, which is nothing — still refused, so the
	// test passed either way, but a reader deriving the target from the payload
	// would get a different answer than the one intended.
	const attack = "../../../access/users/root@pam"

	calls := map[string]func(c *Client) error{
		"GetHAResource": func(c *Client) error {
			_, err := c.GetHAResource(context.Background(), attack)
			return err
		},
		"UpdateHAResource": func(c *Client) error {
			return c.UpdateHAResource(context.Background(), attack, UpdateHAResourceParams{})
		},
		"DeleteHAResource": func(c *Client) error {
			return c.DeleteHAResource(context.Background(), attack)
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			if err := call(c); err == nil {
				t.Fatalf("%s(%q) succeeded, want rejection", name, attack)
			}
			if len(*seen) != 0 {
				t.Errorf("issued %d request(s) %v, want none", len(*seen), *seen)
			}
		})
	}
}

// TestCreateHAResource_RetryCounts pins how the create form carries
// max_restart and max_relocate. Proxmox defaults both to 1, but only for a key
// that is absent — a 0 it is sent is stored as 0 (see the comment in
// CreateHAResource) — so nil and 0 are different requests. The form used to
// send a count only when it was > 0, which made every explicit 0 a 1.
func TestCreateHAResource_RetryCounts(t *testing.T) {
	ptr := func(i int) *int { return &i }
	tests := []struct {
		name       string
		params     CreateHAResourceParams
		wantForm   map[string]string
		wantAbsent []string
	}{
		{
			name:     "an explicit 0 is sent rather than left to Proxmox's default of 1",
			params:   CreateHAResourceParams{SID: "vm:100", MaxRestart: ptr(0), MaxRelocate: ptr(0)},
			wantForm: map[string]string{"max_restart": "0", "max_relocate": "0"},
		},
		{
			name:       "an omitted count sends no key, so Proxmox's default applies",
			params:     CreateHAResourceParams{SID: "vm:100"},
			wantAbsent: []string{"max_restart", "max_relocate"},
		},
		{
			name:     "a positive count is sent as given",
			params:   CreateHAResourceParams{SID: "vm:100", MaxRestart: ptr(3), MaxRelocate: ptr(2)},
			wantForm: map[string]string{"max_restart": "3", "max_relocate": "2"},
		},
		{
			// Each key follows its own field: a 0 on one must neither drag
			// the other into the form nor be written under its name.
			name:       "only max_restart",
			params:     CreateHAResourceParams{SID: "vm:100", MaxRestart: ptr(0)},
			wantForm:   map[string]string{"max_restart": "0"},
			wantAbsent: []string{"max_relocate"},
		},
		{
			name:       "only max_relocate",
			params:     CreateHAResourceParams{SID: "vm:100", MaxRelocate: ptr(0)},
			wantForm:   map[string]string{"max_relocate": "0"},
			wantAbsent: []string{"max_restart"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				calls   int
				gotForm url.Values
			)
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/cluster/ha/resources": func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost {
						t.Errorf("method: want POST, got %s", r.Method)
					}
					if err := r.ParseForm(); err != nil {
						t.Errorf("ParseForm: %v", err)
					}
					gotForm = r.PostForm
					jsonResponse(w, nil)
				},
			})
			defer srv.Close()
			c := newTestClient(t, srv.URL)

			if err := c.CreateHAResource(context.Background(), tt.params); err != nil {
				t.Fatalf("CreateHAResource: %v", err)
			}
			// Without a form that arrived, every absence check below would
			// pass for the wrong reason.
			if calls != 1 {
				t.Fatalf("the create reached Proxmox %d times, want 1", calls)
			}
			if got := gotForm.Get("sid"); got != "vm:100" {
				t.Errorf("sid form param: want %q, got %q", "vm:100", got)
			}
			for k, want := range tt.wantForm {
				if got, ok := gotForm[k]; !ok || len(got) != 1 || got[0] != want {
					t.Errorf("%s form param: want [%q], got %q (present=%v)", k, want, got, ok)
				}
			}
			for _, k := range tt.wantAbsent {
				if _, ok := gotForm[k]; ok {
					t.Errorf("%s form param should be absent, got %q", k, gotForm.Get(k))
				}
			}
		})
	}
}
