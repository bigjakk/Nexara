package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// GET and PUT .../nodes/:node_name/options and GET .../nodes/:node_name/notes,
// driven through the REAL declarations, their real permission gate and the real
// NodeHandler, with stand-ins only at the two edges the handler reaches out to,
// Proxmox and the database (the access routes' stand-ins,
// registry_access_update_user_test.go). A value can be followed from the request
// to the form Proxmox receives and the audit row every Viewer can read, which the
// unit tests of the client and of the audit builder cannot do: they pass with the
// handler deleted.

// nodeOptionsPath is the one path the settings' read and the write are declared
// on, and nodeNotesPath the notes' read, which has its own route and permission.
const (
	nodeOptionsPath = nodeScope + "/:node_name/options"
	nodeNotesPath   = nodeScope + "/:node_name/notes"
)

// nodeOptionsPVEPath is where the stand-in Proxmox sees the node config, which
// both routes reach: GET and PUT /nodes/{node}/config.
const nodeOptionsPVEPath = "/api2/json/nodes/" + testNodeName + "/config"

// nodeOptionsStaleDigestBody is what PVE::Tools::assert_if_modified makes
// Proxmox answer when the digest a write carries is not the file's: a plain 500
// whose message is the die string, with no rejection map.
const nodeOptionsStaleDigestBody = `{"data":null,"message":"detected modified configuration - file changed by other user? Try again.\n"}`

// newNodeOptionsStandIns builds what a route test here mounts the real
// NodeHandler on: the stand-in Proxmox, and the stand-in database holding the
// cluster row that points at it.
func newNodeOptionsStandIns(t *testing.T) (*accessUpdatePVE, *accessUpdateDB, *handlers.NodeHandler) {
	t.Helper()
	pve := &accessUpdatePVE{}
	secret, err := crypto.Encrypt("token-secret-value", accessUpdateEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	store := &accessUpdateDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               pve.serve(t),
		TokenID:              accessUpdateSelf + "!api",
		TokenSecretEncrypted: secret,
		IsActive:             true,
	}}
	return pve, store, handlers.NewNodeHandler(db.New(store), accessUpdateEncKey, nil)
}

// newNodeOptionsApp mounts the real settings read, notes read and write
// declarations, each carrying the real NodeHandler method wired to the two
// stand-ins, behind a gate that grants exactly grants ("action:resource").
func newNodeOptionsApp(t *testing.T, grants map[string]bool) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	return newNodeOptionsAppWithAuth(t, stubAuth(grants))
}

// newNodeOptionsAppWithAuth is newNodeOptionsApp behind any authentication
// middleware, for the tests that need an RBAC engine that does something stubAuth's
// does not, such as fail.
func newNodeOptionsAppWithAuth(t *testing.T, auth fiber.Handler) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve, store, h := newNodeOptionsStandIns(t)

	get := declaredEndpoint(t, fiber.MethodGet, nodeOptionsPath)
	get.Handler = h.GetNodeOptions
	notes := declaredEndpoint(t, fiber.MethodGet, nodeNotesPath)
	notes.Handler = h.GetNodeNotes
	put := declaredEndpoint(t, fiber.MethodPut, nodeOptionsPath)
	put.Handler = h.SetNodeOptions

	return newRegistryApp(t, auth, get, notes, put), pve, store
}

// nodeOptionsGrants is a caller who may do exactly what its names say.
func nodeOptionsGrants(perms ...string) map[string]bool {
	out := make(map[string]bool, len(perms))
	for _, p := range perms {
		out[p] = true
	}
	return out
}

// nodeOptionsRequest is the request a client sends to the options route, signed
// in as the stub session.
func nodeOptionsRequest(method, body string) *http.Request {
	target := nodeRoute(nodeOptionsPath)
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = jsonRequest(method, target, body)
	}
	req.Header.Set("X-Test-User", "yes")
	return req
}

// nodeNotesRequest is the request a client sends to read a node's notes, signed
// in as the stub session.
func nodeNotesRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, nodeRoute(nodeNotesPath), nil)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// nodeOptionsSend runs one request and returns the status and the raw body,
// which is what a caller receives.
func nodeOptionsSend(t *testing.T, app *fiber.App, req *http.Request) (int, []byte) {
	t.Helper()
	status, _, body := nodeOptionsSendFull(t, app, req)
	return status, body
}

// nodeOptionsSendFull is nodeOptionsSend with the response headers too, for the
// assertions that are about a header.
func nodeOptionsSendFull(t *testing.T, app *fiber.App, req *http.Request) (int, http.Header, []byte) {
	t.Helper()
	// Longer than app.Test's second: some of these requests carry a body of half a
	// megabyte, encoded and forwarded, and the race detector and a busy machine
	// should not turn that into a timeout that says nothing about the handler.
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

// sameForm compares two forms with a missing map and an empty one equal: the
// stand-in's decoded body of a request that carried no key is empty, and a
// reflect.DeepEqual of that against nil says they differ.
func sameForm(got, want url.Values) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// TestNodeOptionsDeclaration holds the PUT's body to the struct the client sends
// and to the bounds Proxmox's schema states, so a field added to one and not the
// other, or a bound loosened, fails here.
//
// The bounds are PVE's own: $confdesc in pve-manager's PVE/NodeConfig.pm declares
// startall-onboot-delay as an integer 0-300, ballooning-target as an integer
// 0-100 and description as at most 64*1024 characters.
func TestNodeOptionsDeclaration(t *testing.T) {
	put := declaredEndpoint(t, fiber.MethodPut, nodeOptionsPath)
	get := declaredEndpoint(t, fiber.MethodGet, nodeOptionsPath)
	notes := declaredEndpoint(t, fiber.MethodGet, nodeNotesPath)

	var body []string
	for name := range put.Parameters {
		if name == "cluster_id" || name == "node_name" {
			continue
		}
		body = append(body, name)
	}
	slices.Sort(body)
	want := proxmoxJSONTags(t, proxmox.NodeOptions{})
	slices.Sort(want)
	if !slices.Equal(body, want) {
		t.Errorf("the PUT declares the body %v, but proxmox.NodeOptions forwards %v — a field in one and not the "+
			"other is a setting that is silently rejected or silently never sent", body, want)
	}

	for _, read := range []struct {
		name string
		e    Endpoint
	}{{"settings", get}, {"notes", notes}} {
		params := make([]string, 0, len(read.e.Parameters))
		for name := range read.e.Parameters {
			params = append(params, name)
		}
		slices.Sort(params)
		if wantGet := []string{"cluster_id", "node_name"}; !slices.Equal(params, wantGet) {
			t.Errorf("the %s GET declares %v, want only its path parameters %v", read.name, params, wantGet)
		}
	}
	// The notes are read under manage:node and the settings under view:node, and
	// the declarations say so out loud: a Viewer must not reach the notes.
	if got := notes.Permissions.Describe(); got != "manage:node" {
		t.Errorf("the notes GET declares %q, want manage:node — every built-in Viewer holds view:node", got)
	}
	if got := get.Permissions.Describe(); got != "view:node" {
		t.Errorf("the settings GET declares %q, want view:node", got)
	}

	for name, prop := range put.Parameters {
		// No parameter has a Default: the handler reads the integers with p.OptInt,
		// which a Default cannot fool, so a declared one would only document a copy
		// of Proxmox's own that the handler never sends.
		if prop.Default != nil {
			t.Errorf("%s declares the default %#v; a default here is documentation of a value the handler never sends", name, prop.Default)
		}
		if name == "cluster_id" || name == "node_name" {
			continue
		}
		if !prop.Optional {
			t.Errorf("%s is required; every setting is optional in PVE's schema, and a save sends only what changed", name)
		}
	}

	for _, tt := range []struct {
		name string
		max  float64
	}{
		{"startall-onboot-delay", 300},
		{"ballooning-target", 100},
	} {
		prop := put.Parameters[tt.name]
		if prop.Minimum == nil || *prop.Minimum != 0 || prop.Maximum == nil || *prop.Maximum != tt.max {
			t.Errorf("%s declares the range %v..%v, want 0..%v exactly as PVE's schema states it", tt.name, prop.Minimum, prop.Maximum, tt.max)
		}
	}

	// Property strings Proxmox owns, validates and versions: a format or a pattern
	// here would restate, and date, a rule the cluster in front of the operator
	// applies for itself. Their caps are Nexara's own, a bound on the request well
	// above what any valid value needs, so they are pinned exactly: one lowered
	// would refuse a value Proxmox accepts, and TestNodeOptionsParameters carries
	// the longest realistic ones.
	for _, tt := range []struct {
		name string
		max  int
	}{{"wakeonlan", 256}, {"location", 512}} {
		prop := put.Parameters[tt.name]
		if prop.Format != "" || prop.Pattern != "" {
			t.Errorf("%s declares format %q and pattern %q; Proxmox validates this property string", tt.name, prop.Format, prop.Pattern)
		}
		if prop.MaxLength == nil || *prop.MaxLength != tt.max {
			t.Errorf("%s declares the length bound %v, want %d", tt.name, prop.MaxLength, tt.max)
		}
	}
	if d := put.Parameters["description"]; d.MaxLength == nil || *d.MaxLength != 65536 {
		t.Errorf("description declares the length bound %v, want 65536 characters as PVE's schema states it", d.MaxLength)
	}

	del := put.Parameters["delete"]
	if del.Type != "array" || del.Items == nil || del.Items.Type != "string" {
		t.Errorf("delete is declared as %q of %+v, want an array of strings", del.Type, del.Items)
	}
	if len(del.Items.Enum) != 0 {
		t.Errorf("delete's items carry the enum %v; the client owns which keys may be named, so a copy here would date", del.Items.Enum)
	}
}

// TestNodeOptionsParameters drives the PUT's schema with a capture in place of
// the handler: what the schema lets through, what it refuses before the handler
// runs, and what the handler would read.
func TestNodeOptionsParameters(t *testing.T) {
	// 65536 two-byte characters are 128 KiB on the wire and exactly the bound in
	// characters, which is how PVE counts it: a bound counted in bytes would refuse
	// the first, and one that was not enforced would admit the second.
	atBound := strings.Repeat("é", 65536)
	overBound := strings.Repeat("é", 65537)

	// The longest values Proxmox accepts for the two property strings, as they
	// would really be written. pve-iface is a letter and up to 20 more characters,
	// with an optional .<number> or :<number> after it (pve-common's
	// pve_verify_iface); a location's name is up to 128 characters of any kind, and
	// each coordinate a number of any length that is in range.
	longestWake := "mac=02:00:00:00:00:01,bind-interface=" + strings.Repeat("a", 21) +
		".4094,broadcast-address=255.255.255.255"
	longestLocation := "latitude=-89.123456789012345,longitude=-179.123456789012345,name=" + strings.Repeat("é", 128)

	longEntry := strings.Repeat("a", 33)
	// The bound is on the list, sixteen entries; what may be IN it is the client's.
	entries := func(n int) string {
		return `{"delete":[` + strings.TrimSuffix(strings.Repeat(`"wakeonlan",`, n), ",") + `]}`
	}

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"nothing", `{}`, fiber.StatusNoContent},
		{"a delay of 0", `{"startall-onboot-delay":0}`, fiber.StatusNoContent},
		{"the longest delay", `{"startall-onboot-delay":300}`, fiber.StatusNoContent},
		{"a delay one over", `{"startall-onboot-delay":301}`, fiber.StatusBadRequest},
		{"a negative delay", `{"startall-onboot-delay":-1}`, fiber.StatusBadRequest},
		{"a target of 0", `{"ballooning-target":0}`, fiber.StatusNoContent},
		{"the highest target", `{"ballooning-target":100}`, fiber.StatusNoContent},
		{"a target one over", `{"ballooning-target":101}`, fiber.StatusBadRequest},
		{"a negative target", `{"ballooning-target":-1}`, fiber.StatusBadRequest},
		{"a fraction for an integer", `{"ballooning-target":75.5}`, fiber.StatusBadRequest},
		{"text for an integer", `{"ballooning-target":"high"}`, fiber.StatusBadRequest},
		// Clearing goes through delete. An empty string for an integer is no value
		// at all, and is refused rather than read as 0.
		{"an empty string for an integer", `{"ballooning-target":""}`, fiber.StatusBadRequest},
		{"notes at the bound, counted in characters", `{"description":"` + atBound + `"}`, fiber.StatusNoContent},
		{"notes one character over", `{"description":"` + overBound + `"}`, fiber.StatusBadRequest},
		{"an empty notes string", `{"description":""}`, fiber.StatusNoContent},
		{"the longest realistic wakeonlan", `{"wakeonlan":"` + longestWake + `"}`, fiber.StatusNoContent},
		{"the longest realistic location", `{"location":"` + longestLocation + `"}`, fiber.StatusNoContent},
		{"a wakeonlan over its bound", `{"wakeonlan":"` + strings.Repeat("a", 257) + `"}`, fiber.StatusBadRequest},
		{"a location over its bound", `{"location":"` + strings.Repeat("a", 513) + `"}`, fiber.StatusBadRequest},
		{"every key at once", `{"startall-onboot-delay":30,"ballooning-target":80,"wakeonlan":"02:00:00:00:00:01",` +
			`"location":"latitude=0,longitude=0","description":"sentinel notes","delete":["wakeonlan"],"digest":"abc"}`,
			fiber.StatusNoContent},
		{"sixteen keys to clear", entries(16), fiber.StatusNoContent},
		{"seventeen keys to clear", entries(17), fiber.StatusBadRequest},
		{"a key to clear that is too long", `{"delete":["` + longEntry + `"]}`, fiber.StatusBadRequest},
		// The ACME half of the same file is not this route's: its keys are not
		// declared, so they are unknown parameters, not silently ignored ones.
		{"an ACME key", `{"acme":"account=default"}`, fiber.StatusBadRequest},
		{"an ACME domain key", `{"acmedomain0":"node.example.com"}`, fiber.StatusBadRequest},
		{"a misspelled key", `{"ballooning_target":80}`, fiber.StatusBadRequest},
		{"the node, which is a path parameter", `{"node":"pve-02"}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, clipForFailure(env.Message, 200), tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
			if tt.want == fiber.StatusNoContent && !cap.called {
				t.Error("the handler did not run for a request the schema accepts")
			}
		})
	}

	t.Run("0 reaches the handler as a supplied value and an omitted key does not", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
		status, env := send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), `{"startall-onboot-delay":0}`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptInt("startall-onboot-delay"); v != 0 || !supplied {
			t.Errorf("startall-onboot-delay reads (%d, %v), want (0, true): 0 is a value", v, supplied)
		}
		if v, supplied := cap.params.OptInt("ballooning-target"); v != 0 || supplied {
			t.Errorf("ballooning-target reads (%d, %v), want (0, false): an omitted key is not 0", v, supplied)
		}
		if got := cap.params.Strings("delete"); got != nil {
			t.Errorf("delete reads %v, want nil when omitted", got)
		}
	})

	t.Run("null is an omitted key", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
		status, env := send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), `{"ballooning-target":null,"description":null}`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if _, supplied := cap.params.OptInt("ballooning-target"); supplied {
			t.Error("a null integer reads as supplied; it would be written as 0")
		}
		if cap.params.Has("description") {
			t.Error("a null description reads as supplied")
		}
	})

	t.Run("line breaks in the notes arrive intact", func(t *testing.T) {
		const notes = "sentinel notes\nsecond line\n\nafter a blank line\n"
		body, err := json.Marshal(map[string]string{"description": notes})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
		status, env := send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), string(body)))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.String("description"); got != notes {
			t.Errorf("description = %q, want %q", got, notes)
		}
	})

	t.Run("the keys to clear arrive in order", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
		status, env := send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), `{"delete":["location","wakeonlan"]}`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got, want := cap.params.Strings("delete"), []string{"location", "wakeonlan"}; !slices.Equal(got, want) {
			t.Errorf("delete = %v, want %v", got, want)
		}
	})
}

// clipForFailure shortens a failure message so a refusal that echoes a 64 KiB value
// does not bury the line that says what went wrong.
func clipForFailure(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TestNodeOptionsWriteAuditRow follows a write from the request, through the
// declaration and the handler, to the form Proxmox receives and the audit row.
//
// The row is what every Viewer reads (view:audit), so this is where the promise
// that it carries the names of the settings an edit set and cleared, the values of
// the two integers, and none of the notes, the location or the Wake-on-LAN
// setting, is kept against the code that writes it. The first case sends every
// field and asserts they reached Proxmox, so that their absence from the row is an
// absence and not a save that never carried them.
//
// The refusals are here because each of them has two halves: the answer the caller
// gets, and the two things that must NOT happen — a request reaching Proxmox for a
// refusal the client makes, and a row written for a save that did not happen. The
// stand-in Proxmox always succeeds unless told otherwise, so without these cases a
// row written ahead of the Proxmox call would pass.
func TestNodeOptionsWriteAuditRow(t *testing.T) {
	const (
		wake     = "mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"
		location = "latitude=12.5,longitude=-45.25,name=Site A"
		notes    = "sentinel notes\nsecond line"
	)
	// Every case here is a save with no digest, or one that is refused before the
	// digest is looked at: a save that sends the save token a read returned is
	// checked against the file, which this stand-in, answering every read with an
	// empty envelope, has none of. Those are followed against a stand-in that holds
	// the file (TestNodeConfigSaveWithTheToken and the tests around it).
	token := "v1." + strings.Repeat("A", 43)
	freeText := []string{wake, location, notes, "02:00:00:00:00:01", "vmbr0", "192.0.2.255", "Site A", "12.5", "-45.25", "sentinel", "indented", token}

	tests := []struct {
		name string
		body string

		// pveStatus, when set, is the HTTP status the stand-in Proxmox answers the
		// write with, and pveBody what it says.
		pveStatus int
		pveBody   string

		wantStatus int
		// wantMessage is a substring of the caller's error message, for the
		// answers that are errors.
		wantMessage string
		// wantForm is the form the one Proxmox request carried. Nil marks a
		// refusal made before anything is sent: no request may reach Proxmox.
		wantForm url.Values
		// wantAudit is the exact details of the one audit row, as stored. Empty
		// means no row may be written.
		wantAudit string
	}{
		{
			name: "every setting",
			body: `{"startall-onboot-delay":0,"ballooning-target":75,` +
				`"wakeonlan":"` + wake + `","location":"` + location + `",` +
				`"description":"sentinel notes\nsecond line"}`,
			wantStatus: fiber.StatusOK,
			wantForm: url.Values{
				"startall-onboot-delay": {"0"},
				"ballooning-target":     {"75"},
				"wakeonlan":             {wake},
				"location":              {location},
				"description":           {notes},
			},
			wantAudit: `{"ballooning-target":75,"cleared":[],"settings":["startall-onboot-delay","ballooning-target","wakeonlan","location","description"],"startall-onboot-delay":0}`,
		},
		{
			name:       "clearing two settings",
			body:       `{"delete":["wakeonlan","location"]}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"delete": {"wakeonlan,location"}},
			wantAudit:  `{"cleared":["wakeonlan","location"],"settings":[]}`,
		},
		{
			name:       "a clear beside a write of a different key",
			body:       `{"ballooning-target":0,"delete":["startall-onboot-delay"]}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"ballooning-target": {"0"}, "delete": {"startall-onboot-delay"}},
			wantAudit:  `{"ballooning-target":0,"cleared":["startall-onboot-delay"],"settings":["ballooning-target"]}`,
		},
		{
			// The empty string is "leave alone", not "remove" and not "set to
			// empty": nothing is sent for it, and the row says only what was set.
			name:       "empty strings send no key beside a real setting",
			body:       `{"ballooning-target":75,"wakeonlan":"","location":"","description":""}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"ballooning-target": {"75"}},
			wantAudit:  `{"ballooning-target":75,"cleared":[],"settings":["ballooning-target"]}`,
		},
		{
			// The notes go to Proxmox exactly as sent: a leading blank line, the
			// indentation and the trailing break are all part of them, and a
			// handler that trimmed them would change what the operator wrote. The
			// row names the setting and never the text.
			name:       "notes arrive byte for byte, whitespace and all",
			body:       `{"description":"\n  indented\nsecond line\n"}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"description": {"\n  indented\nsecond line\n"}},
			wantAudit:  `{"cleared":[],"settings":["description"]}`,
		},
		{
			name:       "notes that are only whitespace are still notes",
			body:       `{"description":"  \n"}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"description": {"  \n"}},
			wantAudit:  `{"cleared":[],"settings":["description"]}`,
		},
		{
			// A save that changes nothing is refused: Proxmox would rewrite the
			// whole config file for it, moving the digest under every open dialog,
			// and the row would name nothing.
			name:        "a request that names nothing is refused before anything is sent",
			body:        `{}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "nothing to change",
		},
		{
			name:        "empty strings alone name nothing",
			body:        `{"wakeonlan":"","location":"","description":""}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "nothing to change",
		},
		{
			// Refused before the save token is looked at, so it costs no read of the
			// file's digest: nothing reaches Proxmox.
			name:        "a digest alone names nothing, and is not looked at",
			body:        `{"digest":"` + token + `"}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "nothing to change",
		},
		{
			name:        "setting and clearing one key is refused before anything is sent",
			body:        `{"ballooning-target":0,"delete":["ballooning-target"]}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "set and cleared",
		},
		{
			name:        "clearing the same key as a string it also sets",
			body:        `{"wakeonlan":"02:00:00:00:00:01","delete":["wakeonlan"]}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "set and cleared",
		},
		{
			// The ACME keys share the file, and PVE would erase them: refused as the
			// ACME config's, naming where to clear them.
			name:        "clearing an ACME key is refused before anything is sent",
			body:        `{"delete":["acmedomain0"]}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "ACME",
		},
		{
			name:        "clearing the account setting is refused too",
			body:        `{"delete":["wakeonlan","acme"]}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "ACME",
		},
		{
			name:        "a key that is no setting is refused",
			body:        `{"delete":["digest"]}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "not a node option that can be cleared",
		},
		{
			// parse_property_string skips a whitespace-only segment, so this passes
			// PVE's format check and dies at the write with a plain 500.
			name:        "a line break the format check would let through is refused",
			body:        `{"wakeonlan":"02:00:00:00:00:01,\n"}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "line break",
		},
		{
			// Deliberately stricter than Proxmox, which writes these: a carriage
			// return in a whitespace-only segment passes its format check.
			name:        "a carriage return in a wakeonlan segment is refused",
			body:        `{"wakeonlan":"02:00:00:00:00:01,\r"}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "control character",
		},
		{
			name:        "an escape sequence in a location name is refused",
			body:        `{"location":"latitude=0,longitude=0,name=rack\u001b[31m01"}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "control character",
		},
		{
			name:        "a Unicode line separator in a location name is refused",
			body:        `{"location":"latitude=0,longitude=0,name=rack\u202801"}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "control character",
		},
		{
			// pveproxy answers a body over its post limit 501, with the reason in
			// the body, and the shared mapping would call that a gateway failure.
			// The caller's own request is too large and no retry will shrink it.
			name:        "a body Proxmox refuses as too large is 413 and leaves no row",
			body:        `{"description":"sentinel notes"}`,
			pveStatus:   http.StatusNotImplemented,
			pveBody:     `for data too large`,
			wantStatus:  fiber.StatusRequestEntityTooLarge,
			wantMessage: "too large for Proxmox",
			wantForm:    url.Values{"description": {"sentinel notes"}},
		},
		{
			// 501 is also how pveproxy says "no such uri": that is not this.
			name:        "any other 501 is a gateway failure, not 413",
			body:        `{"ballooning-target":75}`,
			pveStatus:   http.StatusNotImplemented,
			pveBody:     `no such uri`,
			wantStatus:  fiber.StatusBadGateway,
			wantMessage: "no such uri",
			wantForm:    url.Values{"ballooning-target": {"75"}},
		},
		{
			// A PVE that predates the key has no such property, and says so in the
			// rejection map: the caller's own parameter, so a 400 that names it.
			name:        "an older Proxmox VE that does not know location answers 400 naming it",
			body:        `{"location":"latitude=0,longitude=0"}`,
			pveStatus:   http.StatusBadRequest,
			pveBody:     `{"data":null,"errors":{"location":"property is not defined in schema and the schema does not allow additional properties"}}`,
			wantStatus:  fiber.StatusBadRequest,
			wantMessage: "location",
			wantForm:    url.Values{"location": {"latitude=0,longitude=0"}},
		},
		{
			name:        "a token without Sys.Modify is a 403 and leaves no row",
			body:        `{"ballooning-target":75}`,
			pveStatus:   http.StatusForbidden,
			pveBody:     `Permission check failed (/, Sys.Modify)`,
			wantStatus:  fiber.StatusForbidden,
			wantMessage: "Sys.Modify",
			wantForm:    url.Values{"ballooning-target": {"75"}},
		},
		{
			// pveproxy forwards the write to the node itself; one that is down fails
			// the forward, which is the gateway failing and not the config moving.
			name:       "an unreachable node is a gateway failure and leaves no row",
			body:       `{"ballooning-target":75}`,
			pveStatus:  http.StatusInternalServerError,
			pveBody:    `{"data":null,"message":"hostname lookup failed - no route to node\n"}`,
			wantStatus: fiber.StatusBadGateway,
			wantForm:   url.Values{"ballooning-target": {"75"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
			if tt.pveStatus != 0 {
				pve.refuse(nodeOptionsPVEPath, tt.pveStatus, tt.pveBody)
			}
			status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodPut, tt.body))
			if status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), tt.wantStatus)
			}
			if tt.wantMessage != "" && !strings.Contains(string(body), tt.wantMessage) {
				t.Errorf("the answer %s does not say %q", clipForFailure(string(body), 300), tt.wantMessage)
			}

			sent, rows := pve.requests(), store.auditRows()
			if tt.wantForm == nil {
				if len(sent) != 0 {
					t.Errorf("a refusal the client makes reached Proxmox: %+v", sent)
				}
			} else {
				if len(sent) != 1 {
					t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
				}
				if sent[0].method != http.MethodPut || sent[0].path != nodeOptionsPVEPath {
					t.Fatalf("the handler sent %s %s, want PUT %s", sent[0].method, sent[0].path, nodeOptionsPVEPath)
				}
				if !sameForm(sent[0].form, tt.wantForm) {
					t.Errorf("Proxmox received the form %v, want %v", sent[0].form, tt.wantForm)
				}
			}

			if tt.wantAudit == "" {
				if len(rows) != 0 {
					t.Errorf("the request wrote %d audit row(s), want none", len(rows))
				}
				return
			}
			row := oneAccessAuditRow(t, rows)
			if row.resourceType != "node" || row.resourceID != testNodeName || row.action != "set_options" {
				t.Errorf("the audit row is (%q, %q, %q), want (node, %s, set_options)",
					row.resourceType, row.resourceID, row.action, testNodeName)
			}
			if got := rows[0][0]; got != handlers.ClusterUUID(uuid.MustParse(testClusterID)) {
				t.Errorf("the audit row names the cluster %v, want the one the node belongs to", got)
			}
			if string(row.details) != tt.wantAudit {
				t.Errorf("the audit details are %s, want %s", row.details, tt.wantAudit)
			}
			for _, secret := range freeText {
				if strings.Contains(string(row.details), secret) {
					t.Errorf("the audit details %s carry %q, which view:audit would show every Viewer", row.details, secret)
				}
			}
		})
	}
}

// TestNodeOptionsRefusalsComeAfterTheClusterIsResolved pins where the client's
// refusals sit: behind createProxmoxClient, never in front of it.
//
// The route sweep (registry_route_sweep_test.go) sends every declared parameter at
// once — its `delete` entry is a made-up string the client refuses as no node
// option — and, in its required-only pass, none of them, which is a write that
// changes nothing. It expects the handler to get as far as its dependencies before
// anything fails. A refusal made ahead of the cluster lookup would answer either
// request with a 400 and fail the sweep for a reason that has nothing to do with
// the registry. This holds the order itself, for every refusal the client makes: a
// cluster that is not there is a 404 even for a request the client would refuse.
func TestNodeOptionsRefusalsComeAfterTheClusterIsResolved(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"set and cleared, an ACME key to clear and a line break, all at once",
			`{"ballooning-target":0,"delete":["ballooning-target","acmedomain0"],"wakeonlan":"02:00:00:00:00:01,\n"}`},
		{"a request that changes nothing", `{}`},
		{"empty strings alone", `{"wakeonlan":"","location":"","description":""}`},
		{"a control character in a location", `{"location":"latitude=0,longitude=0,name=rack\u001b01"}`},
		// The save token is looked at after the refusals and after the cluster is
		// resolved, since it takes a read of the file's digest from that cluster.
		{"a save token with a request that changes nothing", `{"digest":"v1.` + strings.Repeat("A", 43) + `"}`},
		{"a save token on an ordinary save", `{"ballooning-target":75,"digest":"v1.` + strings.Repeat("A", 43) + `"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
			store.failClusterRead(1, pgx.ErrNoRows)

			status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodPut, tt.body))
			if status != fiber.StatusNotFound {
				t.Fatalf("status = %d (%s), want the cluster's 404 — a 400 means a refusal now runs before the cluster is resolved",
					status, clipForFailure(string(body), 300))
			}
			if sent := pve.requests(); len(sent) != 0 {
				t.Errorf("%d request(s) reached Proxmox: %+v", len(sent), sent)
			}
			if rows := store.auditRows(); len(rows) != 0 {
				t.Errorf("wrote %d audit row(s), want none", len(rows))
			}
		})
	}
}

// TestNodeOptionsReadShowsOnlyItsOwnKeys follows a read of the settings from the
// stand-in Proxmox's reply to the body a caller receives.
//
// PVE answers with the whole node config: the four settings this route is for, the
// notes, the ACME settings of the same file, and the file's digest. Only the
// settings, and the digest for a caller who may write, may come out. The ACME
// settings are read through .../acme-config under their own permission, and the
// notes through .../notes under manage:node: this route is gated on view:node,
// which every built-in Viewer holds, and old notes sometimes hold credentials. The
// integers come out as numbers whichever way PVE spelled them, 0 included.
//
// The digest that comes out is the save token and not Proxmox's digest, which
// hashes the whole file, notes included. Only the compare-and-swap uses it, so it is
// for a caller who may write (TestNodeConfigTokenIsForWriters follows it for a
// Viewer). The reads here that look at it are made by a manager, and the stand-in's
// raw digest must not appear anywhere in what they return.
func TestNodeOptionsReadShowsOnlyItsOwnKeys(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef01234567"

	t.Run("a node with every setting, notes and an ACME config", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("view:node", "manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{
			"acme":"account=sentinel-account","acmedomain0":"node.example.com","acmedomain3":"other.example.com",
			"ballooning-target":"75","startall-onboot-delay":0,
			"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0",
			"location":"latitude=0,longitude=0,name=rack01",
			"description":"sentinel notes\nsecond line\n",
			"digest":"`+digest+`"}}`)

		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := []string{"ballooning-target", "digest", "location", "startall-onboot-delay", "wakeonlan"}
		if !slices.Equal(keys, want) {
			t.Errorf("the body carries the keys %v, want exactly %v: %s", keys, want, body)
		}
		// The notes sit in the very reply this route reads, and must not come out:
		// every built-in Viewer holds the permission it is gated on.
		for _, leaked := range []string{"description", "sentinel notes", "second line"} {
			if strings.Contains(string(body), leaked) {
				t.Errorf("the notes reached the settings read (%q), which every Viewer can make: %s", leaked, body)
			}
		}
		for _, leaked := range []string{"acme", "sentinel-account", "node.example.com", "other.example.com"} {
			if strings.Contains(string(body), leaked) {
				t.Errorf("the ACME config reached the body (%q): %s", leaked, body)
			}
		}
		// Numbers, not the strings PVE sometimes sends: the page compares them.
		if v, ok := got["ballooning-target"].(float64); !ok || v != 75 {
			t.Errorf("ballooning-target = %#v, want the number 75", got["ballooning-target"])
		}
		if v, ok := got["startall-onboot-delay"].(float64); !ok || v != 0 {
			t.Errorf("startall-onboot-delay = %#v, want the number 0 — a delay of 0 is set, and not absent", got["startall-onboot-delay"])
		}
		requireSaveToken(t, body, digest)

		sent := pve.requests()
		if len(sent) != 1 || sent[0].method != http.MethodGet || sent[0].path != nodeOptionsPVEPath {
			t.Errorf("Proxmox received %+v, want one GET %s", sent, nodeOptionsPVEPath)
		}
		if rows := store.auditRows(); len(rows) != 0 {
			t.Errorf("a read wrote %d audit row(s), want none", len(rows))
		}
	})

	t.Run("a setting the node does not have is absent, not 0, and the notes are not a setting", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node", "manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{"description":"sentinel notes\n","digest":"`+digest+`"}}`)

		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		if _, keys := decodeKeys(t, body); !slices.Equal(keys, []string{"digest"}) {
			t.Errorf("body = %s, want the token alone: an unset integer must not read as 0, or the page would show a delay that is not stored", body)
		}
		requireSaveToken(t, body, digest)
	})

	t.Run("a node with no config file is an empty object", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{}}`)

		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		if string(body) != `{}` {
			t.Errorf("body = %s, want {} — no digest either, so the save that follows carries none", body)
		}
	})

	t.Run("a token without Sys.Audit is a 403", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node"))
		pve.refuse(nodeOptionsPVEPath, http.StatusForbidden, `Permission check failed (/, Sys.Audit)`)

		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
	})
}

// TestNodeNotesReadIsManageNodeOnly follows the notes read through the real route:
// who may make it, what comes back, and how it may be kept.
//
// The notes are free text, old ones sometimes hold credentials, and every built-in
// Viewer — an SSO user provisioned on first login among them — holds view:node. So
// they are read under manage:node, on a route of their own, and the answer is the
// notes and the save token for the config file they live in and nothing else: not
// the settings, which are the other route's, and not the ACME config that shares
// the file. The token is there because the Notes dialog pins it for its
// compare-and-swap; Proxmox's own digest, which hashes the notes, is not. The
// answer is marked no-store, so no cache between Nexara and the operator keeps a
// copy of text that may hold a secret.
func TestNodeNotesReadIsManageNodeOnly(t *testing.T) {
	const (
		digest = "0123456789abcdef0123456789abcdef01234567"
		// Leading blank line, indentation and a trailing break: the read must not
		// trim, wrap or re-encode any of it.
		notes = "\n  indented\nsecond line\n"
	)
	const everything = `{"data":{
		"acme":"account=sentinel-account","acmedomain0":"node.example.com",
		"ballooning-target":"75","startall-onboot-delay":0,
		"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0",
		"location":"latitude=0,longitude=0,name=rack01",
		"description":"\n  indented\nsecond line\n",
		"digest":"` + digest + `"}}`

	t.Run("manage:node reads the notes byte for byte, with the token and nothing else", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		pve.reply(nodeOptionsPVEPath, everything)

		status, header, body := nodeOptionsSendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if want := []string{"description", "digest"}; !slices.Equal(keys, want) {
			t.Errorf("the body carries the keys %v, want exactly %v: %s", keys, want, body)
		}
		if got["description"] != notes {
			t.Errorf("description = %q, want the notes exactly as Proxmox stores them: %q", got["description"], notes)
		}
		requireSaveToken(t, body, digest)
		for _, leaked := range []string{"sentinel-account", "node.example.com", "ballooning", "wakeonlan", "02:00:00:00:00:01", "rack01", "startall"} {
			if strings.Contains(string(body), leaked) {
				t.Errorf("the notes read carries %q, which is not the notes: %s", leaked, body)
			}
		}
		if got := header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store — the notes may hold a secret, and no cache may keep them", got)
		}

		sent := pve.requests()
		if len(sent) != 1 || sent[0].method != http.MethodGet || sent[0].path != nodeOptionsPVEPath {
			t.Errorf("Proxmox received %+v, want one GET %s", sent, nodeOptionsPVEPath)
		}
		if rows := store.auditRows(); len(rows) != 0 {
			t.Errorf("a read wrote %d audit row(s), want none", len(rows))
		}
	})

	t.Run("a node with settings and no notes answers its token alone", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{"ballooning-target":"75","wakeonlan":"02:00:00:00:00:01","digest":"`+digest+`"}}`)

		status, header, body := nodeOptionsSendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		if _, keys := decodeKeys(t, body); !slices.Equal(keys, []string{"digest"}) {
			t.Errorf("body = %s, want the token alone", body)
		}
		requireSaveToken(t, body, digest)
		if got := header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})

	t.Run("a node with no config file answers an empty object", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{}}`)

		status, header, body := nodeOptionsSendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		if string(body) != `{}` {
			t.Errorf("body = %s, want {}", body)
		}
		if got := header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})

	t.Run("view:node alone is refused, and nothing reaches Proxmox", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node"))
		pve.reply(nodeOptionsPVEPath, everything)

		status, _, body := nodeOptionsSendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403 for a Viewer", status, clipForFailure(string(body), 300))
		}
		if strings.Contains(string(body), "indented") {
			t.Errorf("the refusal carries the notes: %s", body)
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused read reached Proxmox: %+v", sent)
		}

		// The same caller, against the same reply, still reads the settings — and
		// without the notes, which is the whole of the split.
		status, body = nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK {
			t.Fatalf("settings status = %d (%s), want 200 for a Viewer", status, clipForFailure(string(body), 300))
		}
		if strings.Contains(string(body), "indented") || strings.Contains(string(body), "description") {
			t.Errorf("the Viewer's read of the settings carries the notes: %s", body)
		}
	})

	t.Run("an operator who may manage but not view still reads the notes", func(t *testing.T) {
		// The two permissions are not ordered: manage:node is what the route asks
		// for, and a caller holding it and nothing else is let through.
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		pve.reply(nodeOptionsPVEPath, everything)
		if status, body := nodeOptionsSend(t, app, nodeNotesRequest()); status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
	})

	t.Run("a token without Sys.Audit is a 403 and carries no notes", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		pve.refuse(nodeOptionsPVEPath, http.StatusForbidden, `Permission check failed (/, Sys.Audit)`)

		status, _, body := nodeOptionsSendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node", "manage:node"))
		req := httptest.NewRequest(http.MethodGet, nodeRoute(nodeNotesPath), nil)
		if status, _ := nodeOptionsSend(t, app, req); status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("an anonymous request reached Proxmox: %+v", sent)
		}
	})
}

// TestNodeOptionsWriteOverTheProxmoxPostLimit drives a note through a stand-in
// that refuses a body the way pveproxy does, rather than replaying a canned 501:
// it answers 501 "for data too large" to a request whose Content-Length is over
// the limit, and 200 to any other. The limit is $limit_max_post in pve-http-server's
// src/PVE/APIServer/AnyEvent.pm — 64 KiB before libpve-http-server-perl 5.2.1, 512
// KiB since — checked against the FORM-ENCODED body, where a line break is three
// bytes (%0A) and "é" six (%C3%A9).
//
// So the notes a caller is refused for are not the long ones by character count:
// the declared bound is 65536 characters, 12000 accented ones are over the limit,
// and 60000 plain ones are not. The refusal is 413 with a message that says what
// the limit is, the node is untouched, and no audit row records a save that did not
// happen.
func TestNodeOptionsWriteOverTheProxmoxPostLimit(t *testing.T) {
	const limit = 64 * 1024

	for _, tt := range []struct {
		name       string
		notes      string
		wantStatus int
	}{
		{"60000 plain characters are under the limit", strings.Repeat("a", 60000), fiber.StatusOK},
		{"5000 accented characters are under it too", strings.Repeat("é", 5000), fiber.StatusOK},
		{"12000 accented characters are inside the declared bound and over the limit", strings.Repeat("é", 12000), fiber.StatusRequestEntityTooLarge},
		{"25000 line breaks are inside the declared bound and over the limit", strings.Repeat("\n", 25000), fiber.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, _, store := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))

			var mu sync.Mutex
			var lengths []int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				lengths = append(lengths, r.ContentLength)
				mu.Unlock()
				if r.ContentLength > limit {
					w.WriteHeader(http.StatusNotImplemented)
					_, _ = w.Write([]byte("for data too large"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":null}`))
			}))
			t.Cleanup(srv.Close)
			store.cluster.ApiUrl = srv.URL

			body, err := json.Marshal(map[string]string{"description": tt.notes})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			status, answer := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodPut, string(body)))
			if status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(answer), 300), tt.wantStatus)
			}

			mu.Lock()
			sent := append([]int64(nil), lengths...)
			mu.Unlock()
			if len(sent) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1: %v", len(sent), sent)
			}
			rows := store.auditRows()
			if tt.wantStatus == fiber.StatusOK {
				if sent[0] > limit {
					t.Errorf("the request was %d bytes, over the limit the stand-in refuses at; the case is not what it says", sent[0])
				}
				if len(rows) != 1 {
					t.Errorf("the save wrote %d audit rows, want 1", len(rows))
				}
				return
			}
			if sent[0] <= limit {
				t.Errorf("the request was %d bytes, not over the limit; the case is not what it says", sent[0])
			}
			if !strings.Contains(string(answer), "64 KiB") || !strings.Contains(string(answer), "too large for Proxmox") {
				t.Errorf("the answer %s does not say what the limit is", clipForFailure(string(answer), 300))
			}
			if len(rows) != 0 {
				t.Errorf("a save Proxmox refused wrote %d audit row(s), want none", len(rows))
			}
		})
	}
}

// TestNodeOptionsRoutesAreGatedByTheirDeclaration proves the declaration is what
// refuses a caller, with the real handler behind it: a caller who may read a node
// may not change it, and a refused write reaches neither Proxmox nor the audit log.
func TestNodeOptionsRoutesAreGatedByTheirDeclaration(t *testing.T) {
	t.Run("view:node alone reads but cannot write", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("view:node"))

		if status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, "")); status != fiber.StatusOK {
			t.Fatalf("GET status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		before := len(pve.requests())

		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodPut, `{"ballooning-target":75}`))
		if status != fiber.StatusForbidden {
			t.Fatalf("PUT status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
		if after := len(pve.requests()); after != before {
			t.Errorf("a refused write reached Proxmox (%d requests became %d)", before, after)
		}
		if rows := store.auditRows(); len(rows) != 0 {
			t.Errorf("a refused write wrote %d audit row(s), want none", len(rows))
		}
	})

	t.Run("manage:node alone cannot read the settings", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusForbidden {
			t.Fatalf("GET status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused read reached Proxmox: %+v", sent)
		}
	})

	t.Run("the certificate permission is not the node's", func(t *testing.T) {
		// The ACME half of the same file is gated on certificate; these routes are
		// the node's own settings and notes and must not be reachable through it.
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:certificate", "manage:certificate"))
		for _, req := range []*http.Request{
			nodeOptionsRequest(http.MethodGet, ""),
			nodeNotesRequest(),
			nodeOptionsRequest(http.MethodPut, `{}`),
		} {
			status, _ := nodeOptionsSend(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("%s %s status = %d, want 403 for a caller holding only the certificate permissions",
					req.Method, req.URL.Path, status)
			}
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused request reached Proxmox: %+v", sent)
		}
	})

	t.Run("manage:node writes", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, nodeOptionsGrants("manage:node"))
		status, body := nodeOptionsSend(t, app, nodeOptionsRequest(http.MethodPut, `{"ballooning-target":75}`))
		if status != fiber.StatusOK {
			t.Fatalf("PUT status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		if sent := pve.requests(); len(sent) != 1 {
			t.Errorf("Proxmox received %d requests, want 1", len(sent))
		}
		if rows := store.auditRows(); len(rows) != 1 {
			t.Errorf("the write left %d audit rows, want 1", len(rows))
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, nodeOptionsGrants("view:node", "manage:node"))
		req := httptest.NewRequest(http.MethodGet, nodeRoute(nodeOptionsPath), nil)
		if status, _ := nodeOptionsSend(t, app, req); status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("an anonymous request reached Proxmox: %+v", sent)
		}
	})
}
