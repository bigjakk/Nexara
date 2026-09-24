package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The reads, the update and the delete of an HA resource, driven the way
// registry_ha_create_test.go drives the create: the REAL declaration, its
// permission gate and the REAL handler, with stand-ins only for Proxmox and the
// database. The stand-in Proxmox answers with the resource's RAW section
// config, which is what PVE's GET /cluster/ha/resources[/{sid}] serves: a
// property left at its default is absent there (see proxmox.HAResource).

// haResourcePVE stands in for the cluster's Proxmox API. It serves the resource
// list and the one resource vm:109 from fixed JSON, accepts the update and the
// delete, and records every request it saw, with the update's form.
type haResourcePVE struct {
	list string // the data array GET /cluster/ha/resources answers with
	one  string // the data object GET /cluster/ha/resources/vm:109 answers with
	// oneFails makes the single-resource GET answer 500, the way a
	// snapshot read that did not get through looks to the handler.
	oneFails bool

	mu      sync.Mutex
	seen    []string     // "METHOD path"
	updates []url.Values // the form of each PUT
}

func (p *haResourcePVE) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api2/json/cluster/ha/resources":
			_, _ = w.Write([]byte(`{"data":` + p.list + `}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api2/json/cluster/ha/resources/vm:109":
			if p.oneFails {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"data":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":` + p.one + `}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api2/json/cluster/ha/resources/vm:109":
			if err := r.ParseForm(); err != nil {
				t.Errorf("stand-in Proxmox: ParseForm: %v", err)
			}
			p.mu.Lock()
			p.updates = append(p.updates, r.PostForm)
			p.mu.Unlock()
			_, _ = w.Write([]byte(`{"data":null}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api2/json/cluster/ha/resources/vm:109":
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			t.Errorf("stand-in Proxmox: unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *haResourcePVE) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// newHAResourceApp mounts the real declaration of method+path, carrying the
// HAHandler method pick selects, wired to the stand-in Proxmox and the stand-in
// database of registry_ha_create_test.go.
func newHAResourceApp(t *testing.T, method, path string, pve *haResourcePVE,
	pick func(*handlers.HAHandler) Handler,
) (*fiber.App, *haCreateDBTX) {
	t.Helper()
	baseURL := pve.serve(t)
	encrypted, err := crypto.Encrypt("token-secret-value", haCreateEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(haCreateCacheQueries{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, haCreateEncKey, nil, nil)

	dbtx := &haCreateDBTX{}
	e := declaredEndpoint(t, method, path)
	e.Handler = pick(handlers.NewHAHandler(db.New(dbtx), haCreateEncKey, nil))

	authed := stubAuth(map[string]bool{"view:ha": true, "manage:ha": true})
	auth := func(c fiber.Ctx) error {
		handlers.SetProxmoxCacheLocal(c, cache)
		return authed(c)
	}
	return newRegistryApp(t, auth, e), dbtx
}

// getJSON sends an authenticated GET and decodes the body with UseNumber, so a
// count reads back as the digits Nexara wrote.
func getJSON(t *testing.T, app *fiber.App, target string, into any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Test-User", "yes")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("GET %s: status %d (%s), want 200", target, resp.StatusCode, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("GET %s: body is not the expected JSON (%s): %v", target, body, err)
	}
}

// haSettingKeys are the three resource properties whose default Proxmox leaves
// unwritten, and that Nexara must therefore pass through as absent.
var haSettingKeys = []string{"max_restart", "max_relocate", "failback"}

// checkHASettings asserts that obj carries exactly want among haSettingKeys:
// each wanted key as those digits, and every other one absent.
func checkHASettings(t *testing.T, what string, obj map[string]any, want map[string]string) {
	t.Helper()
	for _, key := range haSettingKeys {
		got, present := obj[key]
		wantDigits, wanted := want[key]
		switch {
		case wanted && !present:
			t.Errorf("%s: %s is missing, want %s", what, key, wantDigits)
		case wanted:
			if n, ok := got.(json.Number); !ok || n.String() != wantDigits {
				t.Errorf("%s: %s = %v, want %s", what, key, got, wantDigits)
			}
		case present:
			t.Errorf("%s: %s = %v for a resource that does not set it; an absent key is "+
				"Proxmox's default and has to stay absent", what, key, got)
		}
	}
}

// TestHAResourceReadsPassUnsetSettingsThroughAsAbsent follows the three
// properties from Proxmox's raw config to the JSON Nexara serves, for both
// reads. Unset has to stay absent and an explicit value — 0 included — has to
// stay that value, or the SPA cannot tell a resource on the defaults from one
// with restart, relocation or failback switched off. As ints both read as 0.
func TestHAResourceReadsPassUnsetSettingsThroughAsAbsent(t *testing.T) {
	t.Run("ListResources", func(t *testing.T) {
		pve := &haResourcePVE{list: `[` +
			`{"sid":"vm:101","type":"vm","state":"started","digest":"0a1b"},` +
			`{"sid":"vm:102","type":"vm","state":"started","max_restart":0,"max_relocate":0,"failback":0,"digest":"0a1b"},` +
			`{"sid":"ct:103","type":"ct","state":"stopped","max_restart":3,"max_relocate":2,"failback":1,"digest":"0a1b"}` +
			`]`}
		app, _ := newHAResourceApp(t, fiber.MethodGet, haScope+"/resources", pve,
			func(h *handlers.HAHandler) Handler { return h.ListResources })

		var got struct {
			Items []map[string]any `json:"items"`
		}
		getJSON(t, app, haRoute(haScope+"/resources"), &got)

		want := map[string]map[string]string{
			"vm:101": {},
			"vm:102": {"max_restart": "0", "max_relocate": "0", "failback": "0"},
			"ct:103": {"max_restart": "3", "max_relocate": "2", "failback": "1"},
		}
		bySID := map[string]map[string]any{}
		for _, item := range got.Items {
			sid, _ := item["sid"].(string)
			bySID[sid] = item
		}
		for sid, settings := range want {
			item, ok := bySID[sid]
			if !ok {
				t.Errorf("%s is missing from the listing", sid)
				continue
			}
			checkHASettings(t, sid, item, settings)
		}
		if len(got.Items) != len(want) {
			t.Errorf("the listing has %d resources, want %d", len(got.Items), len(want))
		}
	})

	t.Run("GetResource", func(t *testing.T) {
		pve := &haResourcePVE{one: `{"sid":"vm:109","type":"vm","state":"started","max_restart":0,"digest":"0a1b"}`}
		app, _ := newHAResourceApp(t, fiber.MethodGet, haScope+"/resources/:sid", pve,
			func(h *handlers.HAHandler) Handler { return h.GetResource })

		var got map[string]any
		getJSON(t, app, haRoute(haScope+"/resources/:sid"), &got)
		if got["sid"] != "vm:109" {
			t.Fatalf("the response's sid is %v, want vm:109", got["sid"])
		}
		checkHASettings(t, "vm:109", got, map[string]string{"max_restart": "0"})
	})
}

// haDeleteAudit returns the details of the one audit row the delete wrote,
// failing unless there is exactly one and it records the deletion of vm:109.
func haDeleteAudit(t *testing.T, d *haCreateDBTX) map[string]any {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.audits) != 1 {
		t.Fatalf("the request wrote %d audit rows, want exactly 1", len(d.audits))
	}
	args := d.audits[0]
	if len(args) != 6 {
		t.Fatalf("the audit insert took %d arguments, want InsertAuditLog's 6", len(args))
	}
	resourceType, _ := args[2].(string)
	resourceID, _ := args[3].(string)
	action, _ := args[4].(string)
	if resourceType != "ha_resource" || resourceID != "vm:109" || action != "deleted" {
		t.Fatalf("the audit row is (%q, %q, %q), want (ha_resource, vm:109, deleted)", resourceType, resourceID, action)
	}
	raw, ok := args[5].(json.RawMessage)
	if !ok {
		t.Fatalf("the audit details argument is %T, want json.RawMessage", args[5])
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var details map[string]any
	if err := dec.Decode(&details); err != nil {
		t.Fatalf("the audit details are not a JSON object (%s): %v", raw, err)
	}
	return details
}

// TestHADeleteResourceAuditsTheSettingsItStored pins the delete's audit row to
// the resource's stored state: a retry count or failback the resource set is
// recorded, 0 included, and one it left to Proxmox's default is not. These were
// ints tested != 0, so an explicit 0 — restart or relocation switched off —
// vanished from the row and failback was never recorded at all.
//
// A snapshot that could not be read must not pass for a resource on every
// default, which is what its missing keys would otherwise say — and its error
// must stay out of the row, which every Viewer can read and which a connection
// error would fill with the PVE host and port. So each case compares the WHOLE
// detail map, not the keys it expects: a key nobody asked for fails it.
func TestHADeleteResourceAuditsTheSettingsItStored(t *testing.T) {
	num := func(s string) json.Number { return json.Number(s) }
	for _, tt := range []struct {
		name     string
		one      string
		oneFails bool
		// want is the entire detail map the row must carry. The stand-in
		// database resolves no guest name, so there is no "name".
		want map[string]any
	}{
		{
			name: "explicit zeros are recorded",
			one:  `{"sid":"vm:109","type":"vm","state":"started","max_restart":0,"max_relocate":0,"failback":0,"digest":"0a1b"}`,
			want: map[string]any{
				"sid": "vm:109", "resource_type": "vm", "state": "started",
				"max_restart": num("0"), "max_relocate": num("0"), "failback": num("0"),
			},
		},
		{
			name: "a resource on every default records none of them",
			one:  `{"sid":"vm:109","type":"vm","state":"started","digest":"0a1b"}`,
			want: map[string]any{"sid": "vm:109", "resource_type": "vm", "state": "started"},
		},
		{
			name: "each recorded key carries its own value",
			one:  `{"sid":"vm:109","type":"vm","state":"started","max_restart":5,"failback":0,"digest":"0a1b"}`,
			want: map[string]any{
				"sid": "vm:109", "resource_type": "vm", "state": "started",
				"max_restart": num("5"), "failback": num("0"),
			},
		},
		{
			name:     "an unread snapshot is marked, and nothing else is said",
			oneFails: true,
			want:     map[string]any{"sid": "vm:109", "prior_state_unknown": true},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := &haResourcePVE{one: tt.one, oneFails: tt.oneFails}
			app, dbtx := newHAResourceApp(t, fiber.MethodDelete, haScope+"/resources/:sid", pve,
				func(h *handlers.HAHandler) Handler { return h.DeleteResource })

			req := httptest.NewRequest(http.MethodDelete, haRoute(haScope+"/resources/:sid"), nil)
			req.Header.Set("X-Test-User", "yes")
			if status, env := send(t, app, req); status != fiber.StatusOK {
				t.Fatalf("status = %d (%q), want 200", status, env.Message)
			}
			// The snapshot read and then the delete itself: without the
			// delete, the row below would be describing nothing.
			wantSeen := []string{
				"GET /api2/json/cluster/ha/resources/vm:109",
				"DELETE /api2/json/cluster/ha/resources/vm:109",
			}
			if got := pve.requests(); len(got) != 2 || got[0] != wantSeen[0] || got[1] != wantSeen[1] {
				t.Fatalf("Proxmox saw %q, want %q", got, wantSeen)
			}

			if got := haDeleteAudit(t, dbtx); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("the audit details are %v, want exactly %v", got, tt.want)
			}
		})
	}
}

// TestHAUpdateResourceForwardsOnlyWhatItWasSent follows an edit from the
// request body to the form Proxmox receives, through the real declaration and
// handler. The edit dialog sends only the fields the operator changed, and that
// is worth nothing unless every hop after it adds none: each case here must
// arrive as exactly its own keys. An empty group is the one translation — it
// leaves as delete=group, because Proxmox refuses group= (see
// UpdateHAResource in internal/proxmox/client_ha.go).
func TestHAUpdateResourceForwardsOnlyWhatItWasSent(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want url.Values
	}{
		{"a comment alone", `{"comment":"note"}`, url.Values{"comment": {"note"}}},
		{"an emptied comment", `{"comment":""}`, url.Values{"comment": {""}}},
		{"state alone", `{"state":"stopped"}`, url.Values{"state": {"stopped"}}},
		{"an explicit 0", `{"max_restart":0}`, url.Values{"max_restart": {"0"}}},
		{"failback off", `{"failback":0}`, url.Values{"failback": {"0"}}},
		{"a group", `{"group":"ha-group01"}`, url.Values{"group": {"ha-group01"}}},
		{"no group", `{"group":""}`, url.Values{"delete": {"group"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := &haResourcePVE{}
			app, _ := newHAResourceApp(t, fiber.MethodPut, haScope+"/resources/:sid", pve,
				func(h *handlers.HAHandler) Handler { return h.UpdateResource })

			req := jsonRequest(http.MethodPut, haRoute(haScope+"/resources/:sid"), tt.body)
			req.Header.Set("X-Test-User", "yes")
			if status, env := send(t, app, req); status != fiber.StatusOK {
				t.Fatalf("status = %d (%q), want 200", status, env.Message)
			}
			pve.mu.Lock()
			defer pve.mu.Unlock()
			if len(pve.updates) != 1 {
				t.Fatalf("Proxmox received %d updates, want exactly 1 (saw %q)", len(pve.updates), pve.seen)
			}
			if got := pve.updates[0]; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Proxmox received %v, want exactly %v", got, tt.want)
			}
		})
	}
}
