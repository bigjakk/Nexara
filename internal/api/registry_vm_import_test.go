package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_vm_import.go that quietly loosened a parameter would show up here.

// vmImportRouteCount is how many endpoints registerVMImportEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
const vmImportRouteCount = 11

const testImportJobID = "7b6a5948-3726-4514-9302-000000000011"

func vmImportRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":storage", "store01",
		":id", testImportJobID,
	).Replace(path)
}

// vmImportLegacyPermissions is what each handler checked with hand-placed
// calls BEFORE Phase 6f, transcribed from
// `git show HEAD:internal/api/handlers/vm_import.go` at commit 1d2b59f.
//
// Every entry is one static requireClusterPerm, so every one hoists. The
// table is here for the two entries that are NOT manage:vm_import: both are
// deliberate, and a table is what stops a later "consistency" pass quietly
// lowering them.
var vmImportLegacyPermissions = map[string]string{
	"POST /api/v1/clusters/:cluster_id/import-metadata":                  "view:vm_import",
	"GET /api/v1/clusters/:cluster_id/query-url-metadata":                "manage:storage",
	"GET /api/v1/clusters/:cluster_id/vm-import-sources":                 "view:vm_import",
	"GET /api/v1/clusters/:cluster_id/vm-import-sources/content":         "view:vm_import",
	"POST /api/v1/clusters/:cluster_id/vm-import-sources/esxi":           "manage:vm_import",
	"POST /api/v1/clusters/:cluster_id/vm-import-sources/enable-content": "manage:storage",
	"DELETE /api/v1/clusters/:cluster_id/vm-import-sources/:storage":     "manage:vm_import",
	"GET /api/v1/clusters/:cluster_id/vm-imports":                        "view:vm_import",
	"POST /api/v1/clusters/:cluster_id/vm-imports":                       "manage:vm_import",
	"GET /api/v1/clusters/:cluster_id/vm-imports/:id":                    "view:vm_import",
	"POST /api/v1/clusters/:cluster_id/vm-imports/:id/cancel":            "manage:vm_import",
}

// declaredVMImportEndpoints returns every declaration in this domain, keyed
// "METHOD path". It filters on vmImportLegacyPermissions' own keys because
// the /clusters/:cluster_id prefix is shared with nine other domains.
func declaredVMImportEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, listed := vmImportLegacyPermissions[key]; listed {
			out[key] = e
		}
	}
	return out
}

// TestVMImportRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// All 11 hand-placed calls move into middleware and none stays: this is the
// uniform shape — resolve the cluster from the path, then one static
// requireClusterPerm — that the registry exists for.
func TestVMImportRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredVMImportEndpoints(t)
	if len(declared) != vmImportRouteCount {
		t.Fatalf("the registry declares %d VM-import routes, want %d", len(declared), vmImportRouteCount)
	}
	if len(vmImportLegacyPermissions) != vmImportRouteCount {
		t.Fatalf("vmImportLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(vmImportLegacyPermissions), vmImportRouteCount)
	}

	hoisted := 0
	for key, want := range vmImportLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; its cluster is in its own path",
				key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path",
				key, e.Permissions.Check.Scope)
		}
	}
	if hoisted != vmImportRouteCount {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want %d / 0",
			hoisted, vmImportRouteCount-hoisted, vmImportRouteCount)
	}

	for key := range declared {
		if _, listed := vmImportLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in vmImportLegacyPermissions — a new VM-import route must "+
				"be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestURLProbeKeepsItsStorageGrant is the assertion behind the one
// permission in this domain that looks wrong and is not.
//
// query-url-metadata makes a NODE issue an outbound request to a
// caller-supplied URL — an SSRF-shaped primitive — so it was deliberately
// held to manage:storage, the same bar as the download it precedes, rather
// than to the lower manage:vm_import. enable-content is manage:storage for
// a related reason: changing what a storage may hold is a
// storage-management act. A later "these are import routes, they should all
// be manage:vm_import" pass would be a privilege reduction on both, and
// this is where it fails.
func TestURLProbeKeepsItsStorageGrant(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		why    string
	}{
		{fiber.MethodGet, clusterScope + "/query-url-metadata",
			"the node makes an outbound request to a caller-supplied URL"},
		{fiber.MethodPost, clusterScope + "/vm-import-sources/enable-content",
			"it changes what a storage may hold"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if got := e.Permissions.Describe(); got != "manage:storage" {
				t.Fatalf("declares %q, want manage:storage — %s", got, tt.why)
			}
			// And the Description has to say WHY, or the next reader sees an
			// inconsistency rather than a decision.
			if !strings.Contains(e.Description, "manage:storage") ||
				!strings.Contains(e.Description, "manage:vm_import") {
				t.Errorf("the Description does not name both grants, so the docs read as an "+
					"inconsistency rather than a decision: %q", e.Description)
			}

			// End to end: a caller holding only manage:vm_import is refused.
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := vmImportRoute(tt.path)
			app := newRegistryApp(t, stubAuth(map[string]bool{"manage:vm_import": true}), gated)
			status, _ := send(t, app, authedRequest(tt.method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding manage:vm_import got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller holding only manage:vm_import")
			}
		})
	}
}

// TestImportSourceDeleteAnchorsItsStorageSegment is the traversal guard for
// this domain's one path segment that is not a uuid.
//
// proxmox.GetStorageConfig and DeleteStorage build "/storage/" +
// url.PathEscape(name), and PathEscape leaves "." and ".." alone — so behind a
// normalising proxy an un-anchored "." resolves onto the storage COLLECTION
// (pveproxy itself takes it literally; see proxmox.validatePathSegment). The
// handler only checked the segment was non-empty, which ".." satisfies.
func TestImportSourceDeleteAnchorsItsStorageSegment(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, clusterScope+"/vm-import-sources/:storage")
	prop := e.Parameters["storage"]
	if prop.Format != "storage-id" {
		t.Fatalf("storage declares format %q, want storage-id — it is the only thing keeping \"..\" "+
			"out of the Proxmox path", prop.Format)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeVMImportEndpoint(t, fiber.MethodDelete, clusterScope+"/vm-import-sources/:storage", cap))
	target := "/api/v1/clusters/" + testClusterID + "/vm-import-sources/.."
	status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status == fiber.StatusNoContent {
		t.Fatal("a traversal segment reached the handler")
	}
	if cap.called {
		t.Error("the handler ran for a traversal segment")
	}
}

// probeVMImportEndpoint is a declared VM-import endpoint with its handler
// swapped for a capture.
func probeVMImportEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestVMImportRequiredSetsMatchTheHandlers pins each body's required SET
// against what the handler refused before the migration, derived from
// `git show HEAD:internal/api/handlers/vm_import.go`.
//
// `node` on the two source-reading routes is the one addition, and it is not
// a tightening of behaviour: resolveNode answers "node is required" for an
// empty one on the metadata route, and proxmox.validateNodeName answers
// "node name is required" on the import, so a request without it has
// always failed — the rejection now names the field instead of arriving as
// an upstream sentence.
func TestVMImportRequiredSetsMatchTheHandlers(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		want   []string
	}{
		{fiber.MethodPost, clusterScope + "/import-metadata",
			[]string{"cluster_id", "node", "storage", "volume"}},
		{fiber.MethodGet, clusterScope + "/vm-import-sources/content",
			[]string{"cluster_id", "node", "storage"}},
		{fiber.MethodPost, clusterScope + "/vm-import-sources/esxi",
			[]string{"cluster_id", "password", "server", "storage", "username"}},
		{fiber.MethodPost, clusterScope + "/vm-import-sources/enable-content",
			[]string{"cluster_id", "storage"}},
		{fiber.MethodPost, clusterScope + "/vm-imports",
			[]string{"cluster_id", "node", "storage", "target_node", "target_storage", "volume"}},
		{fiber.MethodGet, clusterScope + "/query-url-metadata",
			[]string{"cluster_id", "url"}},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			var got []string
			for name, prop := range e.Parameters {
				if !prop.Optional {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("required parameters = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestEsxiPasswordCannotTravelInTheQueryString is what declaring the
// credential buys beyond validation: checkMisplaced refuses a declared body
// parameter that arrives as a query parameter, so it cannot be moved into a
// URL that proxies and access logs record.
func TestEsxiPasswordCannotTravelInTheQueryString(t *testing.T) {
	const path = clusterScope + "/vm-import-sources/esxi"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVMImportEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"storage":"esxi01","server":"esxi.example.com","username":"root@pam","password":"s"}`
	target := vmImportRoute(path) + "?password=leaked"
	status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d (%q), want 400", status, env.Message)
	}
	if !strings.Contains(env.Message, "request body") {
		t.Errorf("message = %q, want it to say the parameter belongs in the body", env.Message)
	}
	if cap.called {
		t.Error("the handler ran for a request that carried a credential in the URL")
	}
}

// TestStartImportKeepsItsSentinels is the compatibility assertion for this
// domain.
//
// Three shapes have to survive, and each one is a request the wizard
// actually sends: source_acquisition:"" (a file already staged on the
// storage), vmid:0 (allocate the next free one), and the three *bool guest
// overrides omitted entirely (keep whatever the source metadata derived).
func TestStartImportKeepsItsSentinels(t *testing.T) {
	const path = clusterScope + "/vm-imports"
	e := declaredEndpoint(t, fiber.MethodPost, path)

	if !slices.Contains(e.Parameters["source_acquisition"].Enum, "") {
		t.Errorf("source_acquisition's enum %v drops the empty spelling, which the handler reads as "+
			"\"staged\" and the wizard sends", e.Parameters["source_acquisition"].Enum)
	}
	if got := e.Parameters["vmid"].Default; got != nil {
		t.Errorf("vmid declares default %#v; it must have NONE, because 0 already means "+
			"\"allocate the next free one\"", got)
	}
	if min := e.Parameters["vmid"].Minimum; min == nil || *min != 0 {
		t.Errorf("vmid declares minimum %v, want 0 — the auto-allocate sentinel", min)
	}
	for _, name := range []string{"onboot", "agent", "numa", "firewall"} {
		prop := e.Parameters[name]
		if prop.Type != "boolean" || !prop.Optional {
			t.Errorf("%s is %s/optional=%v, want an optional boolean", name, prop.Type, prop.Optional)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; it must have NONE, or the import starts writing the "+
				"key onto every guest whose requester never mentioned it", name, prop.Default)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVMImportEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"node":"pve-01","storage":"store01","volume":"store01:import/appliance.ova",` +
		`"target_node":"pve-01","target_storage":"store01","source_acquisition":"","vmid":0}`
	status, env := send(t, app, jsonRequest(http.MethodPost, vmImportRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is what the import wizard sends", status, env.Message)
	}
	if got := cap.params.String("source_acquisition"); got != "" {
		t.Errorf("source_acquisition = %q, want the empty sentinel to survive", got)
	}
	if got := cap.params.Int("vmid"); got != 0 {
		t.Errorf("vmid = %d, want 0", got)
	}
	for _, name := range []string{"onboot", "agent", "numa"} {
		if _, supplied := cap.params.OptBool(name); supplied {
			t.Errorf("%s reads as supplied on a body that omitted it; the import would write the key", name)
		}
	}

	// An explicit VMID below Proxmox's floor is still the handler's to
	// refuse — 0 and 100..999999999 are two disjoint ranges and one numeric
	// bound pair cannot express the gap — but the schema still catches the
	// ceiling.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeVMImportEndpoint(t, fiber.MethodPost, path, cap))
	over := `{"node":"pve-01","storage":"store01","volume":"store01:import/a.ova",` +
		`"target_node":"pve-01","target_storage":"store01","vmid":1000000000}`
	if status, _ := send(t, app, jsonRequest(http.MethodPost, vmImportRoute(path), over)); status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a vmid above Proxmox's ceiling", status)
	}
}

// TestQueryURLMetadataNodeTakesTheEmptySentinel pins the other compatibility
// decision: pickImportNode branches on `nodeName != ""`, and every
// registered format rejects the empty string, so the parameter carries a
// pattern rather than the node-name format.
func TestQueryURLMetadataNodeTakesTheEmptySentinel(t *testing.T) {
	const path = clusterScope + "/query-url-metadata"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if e.Parameters["node"].Format != "" {
		t.Errorf("node declares format %q; the empty sentinel would be rejected by any of them",
			e.Parameters["node"].Format)
	}
	re := regexp.MustCompile(e.Parameters["node"].Pattern)
	if !re.MatchString("") {
		t.Error("node's pattern refuses the empty string, which means \"any online node\"")
	}
	for _, bad := range []string{".", "..", "../etc"} {
		if re.MatchString(bad) {
			t.Errorf("node's pattern accepts %q", bad)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVMImportEndpoint(t, fiber.MethodGet, path, cap))
	target := vmImportRoute(path) + "?url=https%3A%2F%2Fexample.com%2Fa.ova&node="
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — an empty node has always meant \"any online node\"", status, env.Message)
	}
}

// TestEveryVMImportEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryVMImportEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredVMImportEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "VM Import" {
			t.Errorf("%s is in group %q, want VM Import", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
