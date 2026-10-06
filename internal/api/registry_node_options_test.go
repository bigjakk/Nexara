package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// GET and PUT .../nodes/:node_name/options and GET .../nodes/:node_name/notes, through
// the real declarations, permission gate and NodeHandler, with stand-ins only for
// Proxmox and the database (registry_access_kit_test.go). A value can be followed from
// the request to the form Proxmox receives and the audit row every Viewer can read,
// which the client's and the audit builder's unit tests cannot do.

const (
	nodeOptionsPath = nodeScope + "/:node_name/options"
	nodeNotesPath   = nodeScope + "/:node_name/notes"
	// nodeOptionsPVEPath is where the stand-in Proxmox sees the node config, which
	// both routes reach.
	nodeOptionsPVEPath = "/api2/json/nodes/" + testNodeName + "/config"
	// nodeOptionsStaleDigestBody is what PVE::Tools::assert_if_modified makes Proxmox
	// answer when a write's digest is not the file's: a plain 500, no rejection map.
	nodeOptionsStaleDigestBody = `{"data":null,"message":"detected modified configuration - file changed by other user? Try again.\n"}`
)

// newNodeOptionsApp mounts the real settings read, notes read and settings write behind
// a caller who holds exactly grants.
func newNodeOptionsApp(t *testing.T, grants map[string]bool) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve := &accessUpdatePVE{}
	store := newClusterStore(t, pve.serve(t), "", "")
	h := handlers.NewNodeHandler(db.New(store), accessUpdateEncKey, nil)
	return mountReal(t, stubAuth(grants),
		realRoute{fiber.MethodGet, nodeOptionsPath, h.GetNodeOptions},
		realRoute{fiber.MethodGet, nodeNotesPath, h.GetNodeNotes},
		realRoute{fiber.MethodPut, nodeOptionsPath, h.SetNodeOptions},
	), pve, store
}

// nodeOptionsRequest is a request to the options route, signed in as the stub session.
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

func nodeNotesRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, nodeRoute(nodeNotesPath), nil)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// TestNodeOptionsDeclaration holds the PUT's body to the struct the client sends and
// to the bounds of PVE's $confdesc (PVE/NodeConfig.pm): startall-onboot-delay 0-300,
// ballooning-target 0-100, description at most 64*1024 characters.
func TestNodeOptionsDeclaration(t *testing.T) {
	put := sharedEndpoint(t, fiber.MethodPut, nodeOptionsPath)
	get := sharedEndpoint(t, fiber.MethodGet, nodeOptionsPath)
	notes := sharedEndpoint(t, fiber.MethodGet, nodeNotesPath)

	var body []string
	for name := range put.Parameters {
		if name != "cluster_id" && name != "node_name" {
			body = append(body, name)
		}
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
	// A Viewer must not reach the notes: every built-in Viewer holds view:node.
	if got := notes.Permissions.Describe(); got != "manage:node" {
		t.Errorf("the notes GET declares %q, want manage:node", got)
	}
	if got := get.Permissions.Describe(); got != "view:node" {
		t.Errorf("the settings GET declares %q, want view:node", got)
	}

	for name, prop := range put.Parameters {
		// The handler reads the integers with p.OptInt, which a Default cannot fool, so a
		// declared one would only document a copy of Proxmox's own that is never sent.
		if prop.Default != nil {
			t.Errorf("%s declares the default %#v; the handler never sends one", name, prop.Default)
		}
		if name != "cluster_id" && name != "node_name" && !prop.Optional {
			t.Errorf("%s is required; every setting is optional in PVE's schema, and a save sends only what changed", name)
		}
	}
	for _, tt := range []struct {
		name string
		max  float64
	}{{"startall-onboot-delay", 300}, {"ballooning-target", 100}} {
		prop := put.Parameters[tt.name]
		if prop.Minimum == nil || *prop.Minimum != 0 || prop.Maximum == nil || *prop.Maximum != tt.max {
			t.Errorf("%s declares the range %v..%v, want 0..%v exactly as PVE's schema states it", tt.name, prop.Minimum, prop.Maximum, tt.max)
		}
	}
	// Property strings Proxmox owns and validates: no format or pattern here, which
	// would restate and date its rule. The caps are Nexara's own, pinned exactly (one
	// lowered would refuse a value Proxmox accepts; TestNodeOptionsParameters carries
	// the longest realistic ones).
	for _, tt := range []struct {
		name string
		max  int
	}{{"wakeonlan", 256}, {"location", 512}, {"description", 65536}} {
		prop := put.Parameters[tt.name]
		if prop.MaxLength == nil || *prop.MaxLength != tt.max {
			t.Errorf("%s declares the length bound %v, want %d", tt.name, prop.MaxLength, tt.max)
		}
		if tt.name != "description" && (prop.Format != "" || prop.Pattern != "") {
			t.Errorf("%s declares format %q and pattern %q; Proxmox validates this property string", tt.name, prop.Format, prop.Pattern)
		}
	}

	del := put.Parameters["delete"]
	if del.Type != "array" || del.Items == nil || del.Items.Type != "string" {
		t.Errorf("delete is declared as %q of %+v, want an array of strings", del.Type, del.Items)
	} else if len(del.Items.Enum) != 0 {
		t.Errorf("delete's items carry the enum %v; the client owns which keys may be named, so a copy here would date", del.Items.Enum)
	}
}

// TestNodeOptionsParameters drives the PUT's schema with a capture in place of the
// handler: what it lets through, what it refuses before the handler runs, and what
// the handler would read.
func TestNodeOptionsParameters(t *testing.T) {
	// 65536 two-byte characters are 128 KiB on the wire and exactly the bound in
	// characters, as PVE counts it: a bound counted in bytes would refuse the first,
	// and one not enforced would admit the second.
	atBound, overBound := strings.Repeat("é", 65536), strings.Repeat("é", 65537)
	// The longest values Proxmox accepts for the two property strings (pve-iface is a
	// letter and up to 20 more characters, an optional .<number> after; a location's
	// name is up to 128 characters, each coordinate a number of any length in range).
	longestWake := "mac=02:00:00:00:00:01,bind-interface=" + strings.Repeat("a", 21) + ".4094,broadcast-address=255.255.255.255"
	longestLocation := "latitude=-89.123456789012345,longitude=-179.123456789012345,name=" + strings.Repeat("é", 128)
	// The bound is on the list, sixteen entries; what may be IN it is the client's.
	entries := func(n int) string {
		return `{"delete":[` + strings.TrimSuffix(strings.Repeat(`"wakeonlan",`, n), ",") + `]}`
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPut, nodeOptionsPath, cap))
	put := func(body string) (int, ErrorResponse) {
		cap.called, cap.params = false, nil
		return send(t, app, jsonRequest(http.MethodPut, nodeRoute(nodeOptionsPath), body))
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
		// Clearing goes through delete: an empty string for an integer is refused, not read as 0.
		{"an empty string for an integer", `{"ballooning-target":""}`, fiber.StatusBadRequest},
		{"notes at the bound, counted in characters", `{"description":"` + atBound + `"}`, fiber.StatusNoContent},
		{"notes one character over", `{"description":"` + overBound + `"}`, fiber.StatusBadRequest},
		{"an empty notes string", `{"description":""}`, fiber.StatusNoContent},
		{"the longest realistic wakeonlan", `{"wakeonlan":"` + longestWake + `"}`, fiber.StatusNoContent},
		{"the longest realistic location", `{"location":"` + longestLocation + `"}`, fiber.StatusNoContent},
		{"a wakeonlan over its bound", `{"wakeonlan":"` + strings.Repeat("a", 257) + `"}`, fiber.StatusBadRequest},
		{"a location over its bound", `{"location":"` + strings.Repeat("a", 513) + `"}`, fiber.StatusBadRequest},
		{"every key at once", `{"startall-onboot-delay":30,"ballooning-target":80,"wakeonlan":"02:00:00:00:00:01",` +
			`"location":"latitude=0,longitude=0","description":"sentinel notes","delete":["wakeonlan"],"digest":"abc"}`, fiber.StatusNoContent},
		{"sixteen keys to clear", entries(16), fiber.StatusNoContent},
		{"seventeen keys to clear", entries(17), fiber.StatusBadRequest},
		{"a key to clear that is too long", `{"delete":["` + strings.Repeat("a", 33) + `"]}`, fiber.StatusBadRequest},
		// The ACME half of the same file is not this route's: its keys are unknown parameters.
		{"an ACME key", `{"acme":"account=default"}`, fiber.StatusBadRequest},
		{"an ACME domain key", `{"acmedomain0":"node.example.com"}`, fiber.StatusBadRequest},
		{"a misspelled key", `{"ballooning_target":80}`, fiber.StatusBadRequest},
		{"the node, which is a path parameter", `{"node":"pve-02"}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status, env := put(tt.body)
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, clipForFailure(env.Message, 200), tt.want)
			}
			if got := tt.want == fiber.StatusNoContent; cap.called != got {
				t.Errorf("handler ran = %v, want %v", cap.called, got)
			}
		})
	}

	t.Run("what the handler reads", func(t *testing.T) {
		const notes = "sentinel notes\nsecond line\n\nafter a blank line\n"
		raw, _ := json.Marshal(map[string]string{"description": notes})
		if status, env := put(string(raw)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.String("description"); got != notes {
			t.Errorf("description = %q, want the line breaks intact: %q", got, notes)
		}

		if status, env := put(`{"startall-onboot-delay":0,"delete":["location","wakeonlan"]}`); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptInt("startall-onboot-delay"); v != 0 || !supplied {
			t.Errorf("startall-onboot-delay reads (%d, %v), want (0, true): 0 is a value", v, supplied)
		}
		if v, supplied := cap.params.OptInt("ballooning-target"); v != 0 || supplied {
			t.Errorf("ballooning-target reads (%d, %v), want (0, false): an omitted key is not 0", v, supplied)
		}
		if got, want := cap.params.Strings("delete"), []string{"location", "wakeonlan"}; !slices.Equal(got, want) {
			t.Errorf("delete = %v, want %v, in order", got, want)
		}

		if status, env := put(`{"ballooning-target":null,"description":null}`); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if _, supplied := cap.params.OptInt("ballooning-target"); supplied || cap.params.Has("description") {
			t.Error("a null reads as supplied; it would be written as 0 or as empty notes")
		}
		if got := cap.params.Strings("delete"); got != nil {
			t.Errorf("delete reads %v, want nil when omitted", got)
		}
	})
}

// TestNodeOptionsWriteAuditRow follows a write to the form Proxmox receives and the
// audit row. The row (read by every Viewer, view:audit) names the settings an edit
// set and cleared and the values of the two integers, and none of the notes, the
// location or the Wake-on-LAN setting: the first case sends every field so that their
// absence from the row is an absence. Each refusal has two halves, the caller's answer
// and what must NOT happen: a request reaching Proxmox for a refusal the client makes,
// and a row for a save that did not happen. The stand-in Proxmox succeeds unless told
// otherwise, so a row written ahead of the Proxmox call would show.
func TestNodeOptionsWriteAuditRow(t *testing.T) {
	const (
		wake     = "mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"
		location = "latitude=12.5,longitude=-45.25,name=Site A"
		notes    = "sentinel notes\nsecond line"
	)
	// Every case is a save with no digest, or refused before the digest is looked at;
	// a save that sends a token is followed against the stand-in that holds the file
	// (registry_node_config_token_test.go).
	token := "v1." + strings.Repeat("A", 43)
	never := []string{wake, location, notes, "02:00:00:00:00:01", "vmbr0", "192.0.2.255", "Site A", "12.5", "-45.25", "sentinel", "indented", token}

	saved := func(form url.Values, details string) auditWant {
		return auditWant{status: fiber.StatusOK, method: http.MethodPut, path: nodeOptionsPVEPath, form: form,
			audit: auditRow("node", testNodeName, "set_options", details), never: never}
	}
	// refused is a refusal Nexara's client makes: nothing is sent, nothing recorded.
	refused := func(message string) auditWant {
		return auditWant{status: fiber.StatusBadRequest, message: []string{message}}
	}
	// pveRefuses is a save that reaches Proxmox, which refuses it.
	pveRefuses := func(status int, form url.Values, message string) auditWant {
		w := auditWant{status: status, method: http.MethodPut, path: nodeOptionsPVEPath, form: form}
		if message != "" {
			w.message = []string{message}
		}
		return w
	}

	tests := []struct {
		name      string
		body      string
		pveStatus int // the stand-in Proxmox answers the write with this status and pveBody
		pveBody   string
		want      auditWant
	}{
		{name: "every setting",
			body: `{"startall-onboot-delay":0,"ballooning-target":75,"wakeonlan":"` + wake + `","location":"` + location +
				`","description":"sentinel notes\nsecond line"}`,
			want: saved(url.Values{"startall-onboot-delay": {"0"}, "ballooning-target": {"75"}, "wakeonlan": {wake},
				"location": {location}, "description": {notes}},
				`{"ballooning-target":75,"cleared":[],"settings":["startall-onboot-delay","ballooning-target","wakeonlan","location","description"],"startall-onboot-delay":0}`)},
		{name: "clearing two settings", body: `{"delete":["wakeonlan","location"]}`,
			want: saved(url.Values{"delete": {"wakeonlan,location"}}, `{"cleared":["wakeonlan","location"],"settings":[]}`)},
		{name: "a clear beside a write of a different key", body: `{"ballooning-target":0,"delete":["startall-onboot-delay"]}`,
			want: saved(url.Values{"ballooning-target": {"0"}, "delete": {"startall-onboot-delay"}},
				`{"ballooning-target":0,"cleared":["startall-onboot-delay"],"settings":["ballooning-target"]}`)},
		{
			// The empty string is "leave alone", not "remove" and not "set to empty":
			// nothing is sent for it, and the row says only what was set.
			name: "empty strings send no key beside a real setting", body: `{"ballooning-target":75,"wakeonlan":"","location":"","description":""}`,
			want: saved(url.Values{"ballooning-target": {"75"}}, `{"ballooning-target":75,"cleared":[],"settings":["ballooning-target"]}`),
		},
		{
			// The notes go to Proxmox exactly as sent; a handler that trimmed them would
			// change what the operator wrote. The row names the setting, never the text.
			name: "notes arrive byte for byte, whitespace and all", body: `{"description":"\n  indented\nsecond line\n"}`,
			want: saved(url.Values{"description": {"\n  indented\nsecond line\n"}}, `{"cleared":[],"settings":["description"]}`),
		},
		{name: "notes that are only whitespace are still notes", body: `{"description":"  \n"}`,
			want: saved(url.Values{"description": {"  \n"}}, `{"cleared":[],"settings":["description"]}`)},

		// A save that changes nothing is refused: Proxmox would rewrite the whole file
		// for it, moving the digest under every open dialog.
		{name: "a request that names nothing is refused before anything is sent", body: `{}`, want: refused("nothing to change")},
		{name: "empty strings alone name nothing", body: `{"wakeonlan":"","location":"","description":""}`, want: refused("nothing to change")},
		{name: "a digest alone names nothing, and is not looked at", body: `{"digest":"` + token + `"}`, want: refused("nothing to change")},
		{name: "setting and clearing one key is refused before anything is sent",
			body: `{"ballooning-target":0,"delete":["ballooning-target"]}`, want: refused("set and cleared")},
		{name: "clearing the same key as a string it also sets",
			body: `{"wakeonlan":"02:00:00:00:00:01","delete":["wakeonlan"]}`, want: refused("set and cleared")},
		// The ACME keys share the file and PVE would erase them: refused as the ACME config's.
		{name: "clearing an ACME key is refused before anything is sent", body: `{"delete":["acmedomain0"]}`, want: refused("ACME")},
		{name: "clearing the account setting is refused too", body: `{"delete":["wakeonlan","acme"]}`, want: refused("ACME")},
		{name: "a key that is no setting is refused", body: `{"delete":["digest"]}`, want: refused("not a node option that can be cleared")},
		{
			// parse_property_string skips a whitespace-only segment, so this passes PVE's
			// format check and dies at the write with a plain 500.
			name: "a line break the format check would let through is refused", body: `{"wakeonlan":"02:00:00:00:00:01,\n"}`,
			want: refused("line break"),
		},
		{
			// Deliberately stricter than Proxmox, which writes these.
			name: "a carriage return in a wakeonlan segment is refused", body: `{"wakeonlan":"02:00:00:00:00:01,\r"}`,
			want: refused("control character"),
		},
		{name: "an escape sequence in a location name is refused", body: `{"location":"latitude=0,longitude=0,name=rack\u001b[31m01"}`,
			want: refused("control character")},
		{name: "a Unicode line separator in a location name is refused", body: `{"location":"latitude=0,longitude=0,name=rack\u202801"}`,
			want: refused("control character")},

		{
			// pveproxy answers a body over its post limit 501 with the reason in the body,
			// which the shared mapping would call a gateway failure: the caller's own
			// request is too large and no retry will shrink it.
			name: "a body Proxmox refuses as too large is 413 and leaves no row", body: `{"description":"sentinel notes"}`,
			pveStatus: http.StatusNotImplemented, pveBody: `for data too large`,
			want: pveRefuses(fiber.StatusRequestEntityTooLarge, url.Values{"description": {"sentinel notes"}}, "too large for Proxmox"),
		},
		{
			// 501 is also how pveproxy says "no such uri": that is not this.
			name: "any other 501 is a gateway failure, not 413", body: `{"ballooning-target":75}`,
			pveStatus: http.StatusNotImplemented, pveBody: `no such uri`,
			want: pveRefuses(fiber.StatusBadGateway, url.Values{"ballooning-target": {"75"}}, "no such uri"),
		},
		{
			// A PVE that predates the key says so in the rejection map: the caller's own parameter.
			name: "an older Proxmox VE that does not know location answers 400 naming it", body: `{"location":"latitude=0,longitude=0"}`,
			pveStatus: http.StatusBadRequest,
			pveBody:   `{"data":null,"errors":{"location":"property is not defined in schema and the schema does not allow additional properties"}}`,
			want:      pveRefuses(fiber.StatusBadRequest, url.Values{"location": {"latitude=0,longitude=0"}}, "location"),
		},
		{name: "a token without Sys.Modify is a 403 and leaves no row", body: `{"ballooning-target":75}`,
			pveStatus: http.StatusForbidden, pveBody: `Permission check failed (/, Sys.Modify)`,
			want: pveRefuses(fiber.StatusForbidden, url.Values{"ballooning-target": {"75"}}, "Sys.Modify")},
		{
			// pveproxy forwards the write to the node itself; one that is down fails the
			// forward, which is the gateway failing and not the config moving.
			name: "an unreachable node is a gateway failure and leaves no row", body: `{"ballooning-target":75}`,
			pveStatus: http.StatusInternalServerError, pveBody: `{"data":null,"message":"hostname lookup failed - no route to node\n"}`,
			want: pveRefuses(fiber.StatusBadGateway, url.Values{"ballooning-target": {"75"}}, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newNodeOptionsApp(t, grantsOf("manage:node"))
			if tt.pveStatus != 0 {
				pve.refuse(nodeOptionsPVEPath, tt.pveStatus, tt.pveBody)
			}
			requireAudited(t, app, pve, store, nodeOptionsRequest(http.MethodPut, tt.body), tt.want)
		})
	}
}

// TestNodeOptionsReadShowsOnlyItsOwnKeys follows a read of the settings from the
// stand-in Proxmox's reply to the body. PVE answers with the whole node config; only
// the four settings, and the digest for a caller who may write, may come out. The
// notes are read under manage:node on their own route, the ACME settings under
// theirs, and this route is gated on view:node, which every built-in Viewer holds.
// The digest that comes out is the save token, never Proxmox's. The integers come out
// as numbers whichever way PVE spelled them, 0 included.
func TestNodeOptionsReadShowsOnlyItsOwnKeys(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef01234567"

	t.Run("a node with every setting, notes and an ACME config", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, grantsOf("view:node", "manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{
			"acme":"account=sentinel-account","acmedomain0":"node.example.com","acmedomain3":"other.example.com",
			"ballooning-target":"75","startall-onboot-delay":0,
			"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0",
			"location":"latitude=0,longitude=0,name=rack01",
			"description":"sentinel notes\nsecond line\n",
			"digest":"`+digest+`"}}`)

		status, body := sendBody(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		got, keys := decodeKeys(t, body)
		if want := []string{"ballooning-target", "digest", "location", "startall-onboot-delay", "wakeonlan"}; !slices.Equal(keys, want) {
			t.Errorf("the body carries the keys %v, want exactly %v: %s", keys, want, body)
		}
		for _, leaked := range []string{"description", "sentinel notes", "second line", "acme", "sentinel-account", "node.example.com", "other.example.com"} {
			if strings.Contains(string(body), leaked) {
				t.Errorf("the notes or the ACME config reached the settings read (%q), which every Viewer can make: %s", leaked, body)
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
		if sent := pve.requests(); len(sent) != 1 || sent[0].method != http.MethodGet || sent[0].path != nodeOptionsPVEPath {
			t.Errorf("Proxmox received %+v, want one GET %s", sent, nodeOptionsPVEPath)
		}
		if rows := store.auditRows(); len(rows) != 0 {
			t.Errorf("a read wrote %d audit row(s), want none", len(rows))
		}
	})

	t.Run("a setting the node does not have is absent, not 0, and the notes are not a setting", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, grantsOf("view:node", "manage:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{"description":"sentinel notes\n","digest":"`+digest+`"}}`)

		status, body := sendBody(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if _, keys := decodeKeys(t, body); status != fiber.StatusOK || !slices.Equal(keys, []string{"digest"}) {
			t.Errorf("status %d, body = %s, want the token alone: an unset integer must not read as 0", status, body)
		}
		requireSaveToken(t, body, digest)
	})

	t.Run("a node with no config file is an empty object", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, grantsOf("view:node"))
		pve.reply(nodeOptionsPVEPath, `{"data":{}}`)
		if status, body := sendBody(t, app, nodeOptionsRequest(http.MethodGet, "")); status != fiber.StatusOK || string(body) != `{}` {
			t.Errorf("status %d, body = %s, want 200 {} — no digest either, so the save that follows carries none", status, body)
		}
	})

	t.Run("a token without Sys.Audit is a 403", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, grantsOf("view:node"))
		pve.refuse(nodeOptionsPVEPath, http.StatusForbidden, `Permission check failed (/, Sys.Audit)`)
		if status, body := sendBody(t, app, nodeOptionsRequest(http.MethodGet, "")); status != fiber.StatusForbidden {
			t.Errorf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
	})
}

// TestNodeNotesReadIsManageNodeOnly follows the notes read through the real route. The
// notes are free text that old nodes sometimes hold credentials in, and every built-in
// Viewer holds view:node, so they are read under manage:node on a route of their own,
// and the answer is the notes and the save token for the file they live in: not the
// settings, not the ACME config, not Proxmox's digest. It is marked no-store so no
// cache keeps a copy of text that may hold a secret.
func TestNodeNotesReadIsManageNodeOnly(t *testing.T) {
	const (
		digest = "0123456789abcdef0123456789abcdef01234567"
		// A leading blank line, indentation and a trailing break: the read must not
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
	manager := grantsOf("manage:node") // not view:node: the two permissions are not ordered

	t.Run("manage:node reads the notes byte for byte, with the token and nothing else", func(t *testing.T) {
		app, pve, store := newNodeOptionsApp(t, manager)
		pve.reply(nodeOptionsPVEPath, everything)

		status, header, body := sendFull(t, app, nodeNotesRequest())
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		got, keys := decodeKeys(t, body)
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
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if sent := pve.requests(); len(sent) != 1 || sent[0].method != http.MethodGet || sent[0].path != nodeOptionsPVEPath {
			t.Errorf("Proxmox received %+v, want one GET %s", sent, nodeOptionsPVEPath)
		}
		if rows := store.auditRows(); len(rows) != 0 {
			t.Errorf("a read wrote %d audit row(s), want none", len(rows))
		}
	})

	for _, tt := range []struct{ name, reply, want string }{
		{"a node with settings and no notes answers its token alone",
			`{"data":{"ballooning-target":"75","wakeonlan":"02:00:00:00:00:01","digest":"` + digest + `"}}`, `{"digest":"`},
		{"a node with no config file answers an empty object", `{"data":{}}`, `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, _ := newNodeOptionsApp(t, manager)
			pve.reply(nodeOptionsPVEPath, tt.reply)
			status, header, body := sendFull(t, app, nodeNotesRequest())
			if status != fiber.StatusOK || !strings.HasPrefix(string(body), tt.want) || header.Get("Cache-Control") != "no-store" {
				t.Errorf("status %d, Cache-Control %q, body %s, want 200 no-store starting %s", status, header.Get("Cache-Control"), body, tt.want)
			}
			if tt.want != `{}` {
				if _, keys := decodeKeys(t, body); !slices.Equal(keys, []string{"digest"}) {
					t.Errorf("body = %s, want the token alone", body)
				}
				requireSaveToken(t, body, digest)
			}
		})
	}

	t.Run("view:node alone is refused, and nothing reaches Proxmox", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, grantsOf("view:node"))
		pve.reply(nodeOptionsPVEPath, everything)

		status, body := sendBody(t, app, nodeNotesRequest())
		if status != fiber.StatusForbidden || strings.Contains(string(body), "indented") {
			t.Fatalf("status = %d (%s), want a 403 that carries no notes", status, clipForFailure(string(body), 300))
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused read reached Proxmox: %+v", sent)
		}
		// The same caller, against the same reply, still reads the settings — without the notes.
		status, body = sendBody(t, app, nodeOptionsRequest(http.MethodGet, ""))
		if status != fiber.StatusOK || strings.Contains(string(body), "indented") || strings.Contains(string(body), "description") {
			t.Errorf("the Viewer's read of the settings = %d %s, want 200 without the notes", status, body)
		}
	})

	t.Run("a token without Sys.Audit is a 403 and carries no notes", func(t *testing.T) {
		app, pve, _ := newNodeOptionsApp(t, manager)
		pve.refuse(nodeOptionsPVEPath, http.StatusForbidden, `Permission check failed (/, Sys.Audit)`)
		if status, body := sendBody(t, app, nodeNotesRequest()); status != fiber.StatusForbidden || strings.Contains(string(body), "indented") {
			t.Errorf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
	})
}

// TestNodeOptionsWriteOverTheProxmoxPostLimit drives a note through a stand-in that
// refuses a body the way pveproxy does: 501 "for data too large" for a Content-Length
// over $limit_max_post (64 KiB before libpve-http-server-perl 5.2.1, 512 KiB since),
// checked against the FORM-ENCODED body, where a line break is three bytes and "é" six.
// So the notes refused are not the long ones by character count: the declared bound
// is 65536 characters, 12000 accented ones are over the limit and 60000 plain ones
// are not. The refusal is a 413 saying what the limit is, with no audit row.
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
			app, _, store := newNodeOptionsApp(t, grantsOf("manage:node"))

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

			body, _ := json.Marshal(map[string]string{"description": tt.notes})
			status, answer := sendBody(t, app, nodeOptionsRequest(http.MethodPut, string(body)))
			if status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(answer), 300), tt.wantStatus)
			}
			mu.Lock()
			sent := append([]int64(nil), lengths...)
			mu.Unlock()
			if len(sent) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1: %v", len(sent), sent)
			}
			if over := sent[0] > limit; over != (tt.wantStatus != fiber.StatusOK) {
				t.Errorf("the request was %d bytes, the case is not what it says (limit %d)", sent[0], limit)
			}
			rows := store.auditRows()
			if tt.wantStatus == fiber.StatusOK {
				if len(rows) != 1 {
					t.Errorf("the save wrote %d audit rows, want 1", len(rows))
				}
				return
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
// refuses a caller, with the real handler behind it: a caller who may read a node may
// not change it, the certificate permissions (the ACME half of the same file) reach
// none of these routes, and a refused request reaches neither Proxmox nor the audit log.
func TestNodeOptionsRoutesAreGatedByTheirDeclaration(t *testing.T) {
	get := func() *http.Request { return nodeOptionsRequest(http.MethodGet, "") }
	put := func() *http.Request { return nodeOptionsRequest(http.MethodPut, `{"ballooning-target":75}`) }
	anonymous := func(path string) func() *http.Request {
		return func() *http.Request { return httptest.NewRequest(http.MethodGet, nodeRoute(path), nil) }
	}
	type step struct {
		req  func() *http.Request
		want int
	}
	for _, tt := range []struct {
		name   string
		grants []string // none and a nil session: the anonymous caller
		steps  []step
	}{
		{"view:node alone reads but cannot write", []string{"view:node"}, []step{{get, 200}, {put, 403}}},
		{"manage:node alone cannot read the settings but writes", []string{"manage:node"}, []step{{get, 403}, {put, 200}}},
		{"the certificate permissions are not the node's", []string{"view:certificate", "manage:certificate"},
			[]step{{get, 403}, {nodeNotesRequest, 403}, {func() *http.Request { return nodeOptionsRequest(http.MethodPut, `{}`) }, 403}}},
		{"an anonymous caller is refused before the gate", []string{"view:node", "manage:node"},
			[]step{{anonymous(nodeOptionsPath), 401}, {anonymous(nodeNotesPath), 401}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newNodeOptionsApp(t, grantsOf(tt.grants...))
			var served, written int
			for _, s := range tt.steps {
				req := s.req()
				if status, body := sendBody(t, app, req); status != s.want {
					t.Fatalf("%s %s = %d (%s), want %d", req.Method, req.URL.Path, status, clipForFailure(string(body), 300), s.want)
				}
				if s.want == fiber.StatusOK {
					served++
					if req.Method == http.MethodPut {
						written++
					}
				}
			}
			if sent := pve.requests(); len(sent) != served {
				t.Errorf("Proxmox received %d requests, want %d: a refused request must not reach it (%+v)", len(sent), served, sent)
			}
			if rows := store.auditRows(); len(rows) != written {
				t.Errorf("the requests left %d audit rows, want %d", len(rows), written)
			}
		})
	}
}
