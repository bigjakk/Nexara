package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
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

// TestHAResourceReads_KeepAnUnsetSettingApartFromZero pins how both HA resource
// reads decode max_restart, max_relocate and failback. Proxmox answers them from
// the raw section config, where a property left at its default is ABSENT (see
// HAResource), so nil has to mean "Proxmox's default" and a pointer to 0 an
// explicit 0. As plain ints both read as 0, which is how a resource on the
// defaults came to be displayed, and edited, as 0 / 0.
//
// The fixtures give the three keys three different values, so a JSON tag
// crossed between two fields cannot pass.
func TestHAResourceReads_KeepAnUnsetSettingApartFromZero(t *testing.T) {
	ptr := func(i int) *int { return &i }
	tests := []struct {
		name string
		// config is the resource as Proxmox serialises it, digest included.
		config                                  string
		wantRestart, wantRelocate, wantFailback *int
	}{
		{
			name:   "a resource on every default carries none of the keys",
			config: `{"sid":"vm:101","type":"vm","state":"started","digest":"0a1b"}`,
		},
		{
			name:         "explicit zeros stay zeros",
			config:       `{"sid":"vm:101","type":"vm","max_restart":0,"max_relocate":0,"failback":0,"digest":"0a1b"}`,
			wantRestart:  ptr(0),
			wantRelocate: ptr(0),
			wantFailback: ptr(0),
		},
		{
			name:         "each key lands in its own field",
			config:       `{"sid":"vm:101","type":"vm","max_restart":3,"max_relocate":2,"failback":1,"digest":"0a1b"}`,
			wantRestart:  ptr(3),
			wantRelocate: ptr(2),
			wantFailback: ptr(1),
		},
		{
			name:         "one key set and the others left to the default",
			config:       `{"sid":"vm:101","type":"vm","max_relocate":0,"digest":"0a1b"}`,
			wantRelocate: ptr(0),
		},
	}
	show := func(p *int) string {
		if p == nil {
			return "nil"
		}
		return strconv.Itoa(*p)
	}
	check := func(t *testing.T, got HAResource, wantRestart, wantRelocate, wantFailback *int) {
		t.Helper()
		if got.SID != "vm:101" {
			// Without the resource itself, every nil below would pass for want
			// of anything decoded.
			t.Fatalf("decoded sid %q, want vm:101", got.SID)
		}
		for _, f := range []struct {
			key       string
			got, want *int
		}{
			{"max_restart", got.MaxRestart, wantRestart},
			{"max_relocate", got.MaxRelocate, wantRelocate},
			{"failback", got.Failback, wantFailback},
		} {
			if show(f.got) != show(f.want) {
				t.Errorf("%s decoded as %s, want %s", f.key, show(f.got), show(f.want))
			}
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("GetHAResource", func(t *testing.T) {
				srv, _ := newCaptureServer(t, `{"data":`+tt.config+`}`)
				got, err := newTestClient(t, srv.URL).GetHAResource(context.Background(), "vm:101")
				if err != nil {
					t.Fatalf("GetHAResource: %v", err)
				}
				check(t, *got, tt.wantRestart, tt.wantRelocate, tt.wantFailback)
			})
			t.Run("GetHAResources", func(t *testing.T) {
				srv, _ := newCaptureServer(t, `{"data":[`+tt.config+`]}`)
				got, err := newTestClient(t, srv.URL).GetHAResources(context.Background())
				if err != nil {
					t.Fatalf("GetHAResources: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("decoded %d resources, want 1", len(got))
				}
				check(t, got[0], tt.wantRestart, tt.wantRelocate, tt.wantFailback)
			})
		})
	}
}

// TestUpdateHAResource_Form pins the whole PUT form UpdateHAResource builds:
// exactly the keys the caller set, and nothing for a nil field, so an edit that
// changed one setting writes one setting. The one translation is an empty
// group: Proxmox refuses group= (see the comment in UpdateHAResource), so
// clearing the group goes out as delete=group.
func TestUpdateHAResource_Form(t *testing.T) {
	ptr := func(i int) *int { return &i }
	str := func(s string) *string { return &s }
	tests := []struct {
		name   string
		params UpdateHAResourceParams
		want   url.Values
	}{
		{
			name:   "a comment alone",
			params: UpdateHAResourceParams{Comment: str("maintenance window")},
			want:   url.Values{"comment": {"maintenance window"}},
		},
		{
			// Proxmox stores it as "" and SectionConfig's write_config never
			// writes an empty comment, so this is what clears one.
			name:   "an emptied comment is sent empty",
			params: UpdateHAResourceParams{Comment: str("")},
			want:   url.Values{"comment": {""}},
		},
		{
			name:   "an explicit 0 is sent",
			params: UpdateHAResourceParams{MaxRestart: ptr(0)},
			want:   url.Values{"max_restart": {"0"}},
		},
		{
			name:   "state and failback together",
			params: UpdateHAResourceParams{State: str("stopped"), Failback: ptr(0)},
			want:   url.Values{"state": {"stopped"}, "failback": {"0"}},
		},
		{
			name:   "a group is set",
			params: UpdateHAResourceParams{Group: str("ha-group01")},
			want:   url.Values{"group": {"ha-group01"}},
		},
		{
			name:   "an empty group is cleared with delete",
			params: UpdateHAResourceParams{Group: str("")},
			want:   url.Values{"delete": {"group"}},
		},
		{
			name:   "a group cleared alongside other changes",
			params: UpdateHAResourceParams{Group: str(""), MaxRelocate: ptr(3)},
			want:   url.Values{"delete": {"group"}, "max_relocate": {"3"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				calls   int
				gotForm url.Values
			)
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/cluster/ha/resources/vm:101": func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPut {
						t.Errorf("method: want PUT, got %s", r.Method)
					}
					if err := r.ParseForm(); err != nil {
						t.Errorf("ParseForm: %v", err)
					}
					gotForm = r.PostForm
					jsonResponse(w, nil)
				},
			})
			defer srv.Close()

			if err := newTestClient(t, srv.URL).UpdateHAResource(context.Background(), "vm:101", tt.params); err != nil {
				t.Fatalf("UpdateHAResource: %v", err)
			}
			if calls != 1 {
				t.Fatalf("the update reached Proxmox %d times, want 1", calls)
			}
			if !reflect.DeepEqual(gotForm, tt.want) {
				t.Errorf("form = %v, want exactly %v", gotForm, tt.want)
			}
		})
	}
}

// TestGetHAGroups_KeepsTheComment pins the group comment through the read and
// back out as JSON, which is the round trip the listing handler makes. The
// SPA's group editor sends its comment box back on every save, so a comment
// this struct dropped was shown blank and then cleared by the next save of any
// other field.
func TestGetHAGroups_KeepsTheComment(t *testing.T) {
	srv, _ := newCaptureServer(t, `{"data":[`+
		`{"group":"ha-group01","nodes":"pve-01:100,pve-02","type":"group","comment":"rack A pair","digest":"0a1b"},`+
		`{"group":"ha-group02","nodes":"pve-03","type":"group","digest":"0a1b"}]}`)
	groups, err := newTestClient(t, srv.URL).GetHAGroups(context.Background())
	if err != nil {
		t.Fatalf("GetHAGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("decoded %d groups, want 2", len(groups))
	}

	for _, tt := range []struct {
		group HAGroup
		want  string // the group's JSON, as the listing serves it
	}{
		{groups[0], `{"group":"ha-group01","nodes":"pve-01:100,pve-02","restricted":0,"nofailback":0,"comment":"rack A pair"}`},
		{groups[1], `{"group":"ha-group02","nodes":"pve-03","restricted":0,"nofailback":0}`},
	} {
		got, err := json.Marshal(tt.group)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(got) != tt.want {
			t.Errorf("%s serialises as %s, want %s", tt.group.Group, got, tt.want)
		}
	}
}
