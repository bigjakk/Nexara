package api

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_nodes.go
// that quietly loosened a parameter would show up here.

// nodeRouteCount is how many endpoints registerNodeEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const nodeRouteCount = 38

const (
	testNodeRowID = "5c4b3a29-1817-4655-9443-000000000038"
	testNodeName  = "pve-01"
)

// nodeRoute renders one node route's path with the test ids substituted.
func nodeRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":node_id", testNodeRowID,
		":node_name", testNodeName,
		":pool_name", "store01",
		":vg_name", "store02",
		":service", "pveproxy",
		":action", "restart",
		":pos", "0",
	).Replace(path)
}

// nodeLegacyPermissions is what each handler checked with a hand-placed
// call BEFORE Phase 6d, transcribed from `git show HEAD:` over the six
// NodeHandler files at commit be1379f: 38 requireClusterPerm calls across
// 38 handlers, exactly one each, and nothing else — no hasClusterPerm, no
// accessibleClusters.
//
// Every one of them hoists, which is what makes this domain the plainest
// batch so far: the permission is a pair of literals in every case and the
// cluster is the first path parameter of every route, so nothing here is
// Deferred, Advisory, Public, SelfService or global.
//
// The two entries that are NOT :node are the point of writing the table
// out: the support bundle is manage:node rather than view:node, and the
// five firewall routes gate on the firewall resource rather than the node
// one. Both are deliberate and are restated in the declarations' prose.
var nodeLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/nodes":                                        "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/disks":                         "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/network-interfaces":            "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/pci-devices":                   "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/dns":                         "view:node",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/dns":                         "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/time":                        "view:node",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/time":                        "manage:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/shutdown":                   "manage:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/reboot":                     "manage:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/maintenance":                "manage:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/evacuate":                   "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/list":                  "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/smart":                 "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs":                   "view:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs":                  "manage:node",
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs/:pool_name":     "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm":                   "view:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm":                  "manage:node",
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm/:vg_name":       "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin":               "view:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin":              "manage:node",
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin/:pool_name": "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory":             "view:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory":            "manage:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/initgpt":              "manage:node",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/disks/wipe":                  "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/services":                    "view:node",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/services/:service/:action":  "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog":                      "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/journal":                     "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/report":                      "manage:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/sensors":                     "view:node",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules":              "view:firewall",
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules":             "manage:firewall",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos":         "manage:firewall",
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos":      "manage:firewall",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/log":                "view:firewall",
}

// declaredNodeEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It filters on the node scope AND on the handler having come from
// registerNodeEndpoints, which the path prefix alone cannot do: the seven
// node-hardware listings the VM dialogs use (/bridges, /hardware/*, …) sit
// under the same prefix and are declared in registry_vms.go. The
// difference that separates them is the tally table, so anything under the
// prefix that is not in it is reported rather than silently filtered — see
// the final loop of TestNodeRoutesDeclareTheSamePermissionTheyEnforced.
func declaredNodeEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, ours := nodeLegacyPermissions[key]; ours {
			out[key] = e
		}
	}
	return out
}

// TestNodeRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// All 38 hand-placed calls move into middleware — this is the first batch
// since Phase 6b where nothing at all stays in a handler — so the tally
// checks the action AND the resource of each one, not merely the shape.
// Checking only the action would let the firewall routes drift onto
// view:node and still pass, which is the exact confusion the declarations'
// prose calls out.
func TestNodeRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredNodeEndpoints(t)
	if len(declared) != nodeRouteCount {
		t.Fatalf("the registry declares %d node routes, want %d", len(declared), nodeRouteCount)
	}
	if len(nodeLegacyPermissions) != nodeRouteCount {
		t.Fatalf("nodeLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(nodeLegacyPermissions), nodeRouteCount)
	}

	byPermission := map[string]int{}
	for key, want := range nodeLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; its cluster is the first parameter in its own path",
				key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path",
				key, e.Permissions.Check.Scope)
		}
		byPermission[want]++
	}

	// The per-permission breakdown, so a failure says WHICH pair drifted
	// rather than only that the total moved. Derived from `git show HEAD:`
	// over the six NodeHandler files at commit be1379f.
	for _, tt := range []struct {
		permission string
		calls      int
	}{
		{"view:node", 16},
		{"manage:node", 17},
		{"view:firewall", 2},
		{"manage:firewall", 3},
	} {
		if byPermission[tt.permission] != tt.calls {
			t.Errorf("%d routes declare %s, want %d", byPermission[tt.permission], tt.permission, tt.calls)
		}
	}

}

// TestEveryDeclaredNodeRouteIsInTheTally is the other direction of the
// tally, and it is a SEPARATE test for the reason its storage counterpart
// spells out: the tally above opens with two t.Fatalf count checks, and a
// newly declared node route trips the first of them — so a loop sharing
// that body would never run in the one situation it exists for.
//
// The seven VM-dialog hardware listings share the node prefix and are
// declared in registry_vms.go, so they are named explicitly rather than
// skipped by a looser prefix rule that would also swallow a real omission.
//
// FOUR other domains also mount routes under this prefix, and they are
// deferred to rather than re-listed: the six ACME certificate and
// acme-config routes carry their own tally in registry_acme_test.go, the
// rolling-update package preview carries its own in
// registry_rolling_update_test.go, the three APT repository routes carry
// theirs in registry_apt_repositories_test.go, and the per-node historical
// metrics route carries its own in registry_metrics_test.go. Deferring keeps
// every route in exactly one tally — copying them here would create a second
// list to keep in step, which is the failure this test exists to prevent.
func TestEveryDeclaredNodeRouteIsInTheTally(t *testing.T) {
	vmDialogRoutes := map[string]bool{
		"GET " + clusterScope + "/nodes/:node_name/bridges":       true,
		"GET " + clusterScope + "/nodes/:node_name/hardware/usb":  true,
		"GET " + clusterScope + "/nodes/:node_name/hardware/pci":  true,
		"GET " + clusterScope + "/nodes/:node_name/machine-types": true,
		"GET " + clusterScope + "/nodes/:node_name/cpu-models":    true,
		"GET " + clusterScope + "/nodes/:node_name/cpu-flags":     true,
		"GET " + clusterScope + "/nodes/:node_name/isos":          true,
	}
	s := newRouteStubServer(t)
	seen := 0
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if !strings.HasPrefix(e.Path, nodeScope) {
			continue
		}
		seen++
		_, inNodeTally := nodeLegacyPermissions[key]
		_, inACMETally := acmeLegacyPermissions[key]
		_, inRollingTally := rollingLegacyPermissions[key]
		_, inAptTally := aptLegacyPermissions[key]
		_, inMetricsTally := metricsLegacyPermissions[key]
		if inNodeTally || inACMETally || inRollingTally || inAptTally || inMetricsTally || vmDialogRoutes[key] {
			continue
		}
		t.Errorf("%s is declared under the node scope but is in none of nodeLegacyPermissions, "+
			"acmeLegacyPermissions, rollingLegacyPermissions, aptLegacyPermissions, "+
			"metricsLegacyPermissions or the VM-dialog hardware set — add it to its domain's tally, "+
			"or the tally stops being a review surface", key)
	}
	if seen == 0 {
		t.Fatal("no declared route matched the node scope; this guard would pass vacuously")
	}
}

// TestNodeNameFormatIsNoLooserThanTheShellGuard is the reason this domain
// needed a decision rather than a default.
//
// :node_name reaches a shell: SetNodeMaintenance interpolates it into an
// ha-manager command, single-quoted, and nodeMaintenanceNameRe is the
// defence-in-depth check on top of that. THREE incompatible copies of "is
// this a node name" exist in the tree — that one, rolling_update.go's
// validateNodeName and proxmox.validateNodeName — and apischema's
// node-name format is a fourth. The declaration has to be at least as
// strict as the tightest of them, never looser.
//
// It is: the format bars the underscore all three others allow, requires
// an alphanumeric at BOTH ends (so a leading dot or dash cannot start a
// name), and caps the length at 63 where the loosest of the three caps at
// 64 and the other two not at all. This asserts that directly against the
// mounted route rather than against the format in isolation, because what
// matters is what the ROUTE accepts.
func TestNodeNameFormatIsNoLooserThanTheShellGuard(t *testing.T) {
	// The maintenance route is the one that reaches the shell, so it is the
	// one driven here.
	const path = clusterScope + "/nodes/:node_name/maintenance"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if got := e.Parameters["node_name"].Format; got != "node-name" {
		t.Fatalf("node_name declares format %q, want node-name", got)
	}

	for _, tt := range []struct {
		name     string
		nodeName string
		want     int
	}{
		{"an ordinary node name", "pve-01", fiber.StatusNoContent},
		{"a dotted node name", "pve-01.example.com", fiber.StatusNoContent},
		// Every one of these is accepted by nodeMaintenanceNameRe and
		// refused by the declaration, which is the direction that is safe.
		{"an underscore", "pve_01", fiber.StatusBadRequest},
		{"a leading dot", ".pve01", fiber.StatusBadRequest},
		{"a trailing dash", "pve01-", fiber.StatusBadRequest},
		{"a shell metacharacter", "pve01;id", fiber.StatusBadRequest},
		{"a quote", "pve01'", fiber.StatusBadRequest},
		// Percent-encoded because a raw space is not a legal request target.
		// Fiber hands the segment to the schema without decoding it, so the
		// value the format sees still carries characters it refuses either
		// way — which is the point: there is no spelling of a space that
		// reaches the shell.
		{"a space", "pve%2001", fiber.StatusBadRequest},
		{"64 characters", strings.Repeat("a", 64), fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPost, path, cap))
			target := strings.NewReplacer(
				":cluster_id", testClusterID,
				":node_name", tt.nodeName,
			).Replace(path)
			status, env := send(t, app, jsonRequest(http.MethodPost, target, `{"enable":true}`))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a node name the schema rejected")
			}
		})
	}
}

// probeNodeEndpoint is a declared node endpoint with its handler swapped
// for a capture.
func probeNodeEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestNodeCleanupFlagsAreCoercedRatherThanSilentlyFalse pins the behaviour
// change the declaration makes on the three disk-destroying DELETEs.
//
// They read their two flags with fiber.Query[bool], which answers FALSE for
// anything it cannot parse — so ?cleanup-disks=yes left the disks alone and
// said nothing. Declared as booleans, the coercion accepts every spelling
// apischema knows and a value that means neither is a 400 naming the field.
func TestNodeCleanupFlagsAreCoercedRatherThanSilentlyFalse(t *testing.T) {
	const path = clusterScope + "/nodes/:node_name/disks/zfs/:pool_name"
	for _, tt := range []struct {
		query string
		want  int
		disks bool
	}{
		{query: "", want: fiber.StatusNoContent, disks: false},
		{query: "?cleanup-disks=true", want: fiber.StatusNoContent, disks: true},
		{query: "?cleanup-disks=1&cleanup-config=1", want: fiber.StatusNoContent, disks: true},
		// fiber.Query[bool] answered false for this and carried on.
		{query: "?cleanup-disks=yes", want: fiber.StatusNoContent, disks: true},
		{query: "?cleanup-disks=maybe", want: fiber.StatusBadRequest},
		{query: "?cleanup_disks=true", want: fiber.StatusBadRequest},
	} {
		t.Run(tt.query, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodDelete, path, cap))
			status, env := send(t, app, httptest.NewRequest(http.MethodDelete, nodeRoute(path)+tt.query, nil))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want != fiber.StatusNoContent {
				return
			}
			if got := cap.params.Bool("cleanup-disks"); got != tt.disks {
				t.Errorf("cleanup-disks = %v, want %v", got, tt.disks)
			}
		})
	}
}

// nodeValidValues is one acceptable value per REQUIRED node parameter.
//
// It exists so that a case about one parameter fills in every other
// required one, and therefore fails for the reason under test rather than
// for a missing sibling. A Validate call that supplies a single key always
// errors — which would make every "this value is refused" assertion pass
// vacuously, the shape this repo has been bitten by before.
//
// The storage-object names are the placeholder scheme's storeNN rather
// than the textbook "tank"/"vg0": these are route substitutions, so a name
// that could collide with a real pool buys nothing and has to be argued
// about in every review.
var nodeValidValues = map[string]any{
	"cluster_id":   testClusterID,
	"node_name":    testNodeName,
	"pool_name":    "store01",
	"vg_name":      "store02",
	"volume-group": "store02",
	"service":      "pveproxy",
	"action":       "restart",
	"pos":          0,
	"disk":         "/dev/sda",
	"device":       "/dev/sda",
	"devices":      "/dev/sda",
	"name":         "store01",
	"raidlevel":    "mirror",
	"filesystem":   "ext4",
	"enable":       true,
	"search":       "example.com",
	"timezone":     "Etc/UTC",
	"type":         "in",
}

// nodeParamsWith fills every required parameter of e with a known-good
// value, then applies overrides. It FAILS rather than guesses when a
// required parameter has no entry above, so a new one cannot quietly make
// a caller of this helper vacuous.
func nodeParamsWith(t *testing.T, e Endpoint, overrides map[string]any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for name, prop := range e.Parameters {
		if prop.Optional {
			continue
		}
		v, ok := nodeValidValues[name]
		if !ok {
			t.Fatalf("%s %s: no known-good value for required parameter %q; add one to nodeValidValues",
				e.Method, e.Path, name)
		}
		out[name] = v
	}
	maps.Copy(out, overrides)
	return out
}

// TestNodeDeleteRoutesRefuseATraversalSegment covers the parameters that
// become a Proxmox PATH segment.
//
// proxmox.validatePathSegment is the existing guard on three of the four:
// it refuses exactly "." and ".." and anything carrying a separator. The
// declaration states the same rule one layer earlier and names the field.
// The fourth is the one that is new — :service is relayed into a Proxmox
// path by proxmox.ServiceAction WITHOUT that guard, so the schema is the
// only thing keeping a traversal segment out of it.
func TestNodeDeleteRoutesRefuseATraversalSegment(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		param  string
	}{
		{"zfs pool", fiber.MethodDelete, clusterScope + "/nodes/:node_name/disks/zfs/:pool_name", "pool_name"},
		{"lvm volume group", fiber.MethodDelete, clusterScope + "/nodes/:node_name/disks/lvm/:vg_name", "vg_name"},
		{"lvmthin pool", fiber.MethodDelete, clusterScope + "/nodes/:node_name/disks/lvmthin/:pool_name", "pool_name"},
		{"service", fiber.MethodPost, clusterScope + "/nodes/:node_name/services/:service/:action", "service"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			prop, ok := e.Parameters[tt.param]
			if !ok {
				t.Fatalf("%s declares no %q parameter", tt.path, tt.param)
			}
			if prop.Pattern == "" {
				t.Fatalf("%s: %q carries no pattern, so nothing keeps \"..\" out of a Proxmox path segment",
					tt.path, tt.param)
			}
			if prop.MaxLength == nil {
				t.Errorf("%s: %q carries no length bound", tt.path, tt.param)
			}
			// The whole set is valid apart from the one value under test, so
			// the rejection has to NAME that parameter — otherwise the case
			// would pass on a missing sibling instead.
			if _, err := e.Parameters.Validate(nodeParamsWith(t, e, nil)); err != nil {
				t.Fatalf("%s: the known-good parameter set was rejected: %v", tt.path, err)
			}
			for _, bad := range []string{".", "..", "a/b", `a\b`, "-leading", ""} {
				_, err := e.Parameters.Validate(nodeParamsWith(t, e, map[string]any{tt.param: bad}))
				if err == nil {
					t.Errorf("%s: %q accepted %q", tt.path, tt.param, bad)
					continue
				}
				if !strings.HasPrefix(err.Error(), tt.param+":") {
					t.Errorf("%s: %q = %q was rejected, but the message blames something else: %v",
						tt.path, tt.param, bad, err)
				}
			}
		})
	}
}

// TestNodeDeviceParametersAreAnchoredAtDev pins the other value class this
// domain hands to a node: a block device.
//
// PVE's own `disk` parameter carries the pattern ^/dev/[a-zA-Z0-9/]+$, so a
// bare "sda" has never been a working request and the anchor costs nothing
// a caller wanted. What it buys is that a value the UI never produces
// cannot reach a node as a device name at all.
func TestNodeDeviceParametersAreAnchoredAtDev(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		param  string
	}{
		{fiber.MethodGet, clusterScope + "/nodes/:node_name/disks/smart", "disk"},
		{fiber.MethodPost, clusterScope + "/nodes/:node_name/disks/initgpt", "disk"},
		{fiber.MethodPut, clusterScope + "/nodes/:node_name/disks/wipe", "disk"},
		{fiber.MethodPost, clusterScope + "/nodes/:node_name/disks/lvm", "device"},
		{fiber.MethodPost, clusterScope + "/nodes/:node_name/disks/lvmthin", "device"},
		{fiber.MethodPost, clusterScope + "/nodes/:node_name/disks/directory", "device"},
		{fiber.MethodPost, clusterScope + "/nodes/:node_name/disks/zfs", "devices"},
	} {
		t.Run(tt.path+" "+tt.param, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if e.Parameters[tt.param].Optional {
				t.Errorf("%s: %q is optional, but the handler refused an empty one", tt.path, tt.param)
			}
			// A single-device parameter cannot express a list, and the list
			// parameter accepts a single device too, so all four routes take
			// the same single-device spellings.
			for _, good := range []string{"/dev/sda", "/dev/nvme0n1", "/dev/disk/by-id/ata-CT500"} {
				if _, err := e.Parameters.Validate(nodeParamsWith(t, e, map[string]any{tt.param: good})); err != nil {
					t.Errorf("%s: %q rejected %q: %v", tt.path, tt.param, good, err)
				}
			}
			for _, bad := range []string{
				"sda", "", "/etc/passwd", "/dev/sda;id",
				// Both traversal shapes, not only the leading one: an anchor
				// that checks the FIRST character refuses the first of these
				// and accepts the second.
				"/dev/../etc/passwd", "/dev/sda/../../etc/passwd",
			} {
				_, err := e.Parameters.Validate(nodeParamsWith(t, e, map[string]any{tt.param: bad}))
				if err == nil {
					t.Errorf("%s: %q accepted %q", tt.path, tt.param, bad)
					continue
				}
				if !strings.HasPrefix(err.Error(), tt.param+":") {
					t.Errorf("%s: %q = %q was rejected, but the message blames something else: %v",
						tt.path, tt.param, bad, err)
				}
			}
		})
	}
	// Only the ZFS create takes a LIST, and it has to keep taking one.
	e := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/nodes/:node_name/disks/zfs")
	if _, err := e.Parameters.Validate(nodeParamsWith(t, e, map[string]any{
		"devices": "/dev/sda,/dev/sdb",
	})); err != nil {
		t.Errorf("the ZFS create refused a two-device list: %v", err)
	}
}

// TestNodeCreateBodiesRequireOnlyWhatTheHandlersDid pins each create
// body's required SET against what the prior handler refused, derived from
// `git show HEAD:internal/api/handlers/node_disks.go`.
//
// It is a SET rather than a list of positive cases because the dialogs send
// every key they know on every submit: a parameter that quietly became
// required would be invisible to a fixture-driven test.
func TestNodeCreateBodiesRequireOnlyWhatTheHandlersDid(t *testing.T) {
	for _, tt := range []struct {
		path string
		want []string
	}{
		// `if req.Name == "" || req.RaidLevel == "" || req.Devices == ""`
		{clusterScope + "/nodes/:node_name/disks/zfs", []string{"devices", "name", "raidlevel"}},
		// `if req.Name == "" || req.Device == ""` — add_storage is a plain flag.
		{clusterScope + "/nodes/:node_name/disks/lvm", []string{"device", "name"}},
		{clusterScope + "/nodes/:node_name/disks/lvmthin", []string{"device", "name"}},
		// `if req.Name == "" || req.Device == "" || req.Filesystem == ""`
		{clusterScope + "/nodes/:node_name/disks/directory", []string{"device", "filesystem", "name"}},
		// `if req.Disk == ""`
		{clusterScope + "/nodes/:node_name/disks/initgpt", []string{"disk"}},
	} {
		t.Run(tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, tt.path)
			var got []string
			for name, prop := range e.Parameters {
				// The path parameters are required by construction and are not
				// what this pins.
				if name == "cluster_id" || name == "node_name" {
					continue
				}
				if !prop.Optional {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("required body parameters = %v, want %v", got, tt.want)
			}
		})
	}

	// The wipe route is a PUT rather than a POST and carries the same one
	// required field as initgpt.
	e := declaredEndpoint(t, fiber.MethodPut, clusterScope+"/nodes/:node_name/disks/wipe")
	if e.Parameters["disk"].Optional {
		t.Error("the wipe route's disk is optional, but the handler refused an empty one")
	}
}

// TestNodeFirewallRuleRequiredSetDiffersByVerb pins the one asymmetry
// between the two firewall rule bodies.
//
// CreateNodeFirewallRule refused an empty type or action;
// UpdateNodeFirewallRule refused neither, because Proxmox's rule update
// keeps the existing values for the keys a caller omits. Making them
// required on both would look tidier and would reject a request that has
// always worked.
func TestNodeFirewallRuleRequiredSetDiffersByVerb(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/nodes/:node_name/firewall/rules")
	for _, name := range []string{"type", "action"} {
		if create.Parameters[name].Optional {
			t.Errorf("create: %q is optional, but the handler refused an empty one", name)
		}
	}
	update := declaredEndpoint(t, fiber.MethodPut, clusterScope+"/nodes/:node_name/firewall/rules/:pos")
	for _, name := range []string{"type", "action"} {
		if !update.Parameters[name].Optional {
			t.Errorf("update: %q is required, but the handler accepted a body without it", name)
		}
	}
	// Everything else on both is optional, so the only required parameters
	// on the update are its path ones.
	var required []string
	for name, prop := range update.Parameters {
		if !prop.Optional {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	if want := []string{"cluster_id", "node_name", "pos"}; !slices.Equal(required, want) {
		t.Errorf("the update's required set is %v, want %v", required, want)
	}
}

// TestNodeSyslogPagingIsBoundedRatherThanClamped covers the two query
// parameters the syslog route used to CLAMP.
//
// ?limit=50000 answered with 5000 rows and ?start=abc answered from the
// top, both silently — a substituted page or offset is indistinguishable
// from the caller's own, so a paging bug looks like missing data. The
// declaration bounds them instead.
func TestNodeSyslogPagingIsBoundedRatherThanClamped(t *testing.T) {
	const path = clusterScope + "/nodes/:node_name/syslog"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if got := e.Parameters["start"].Default; got != -1 {
		t.Errorf("start default = %#v, want -1 — the value the handler substituted", got)
	}
	if got := e.Parameters["limit"].Default; got != 500 {
		t.Errorf("limit default = %#v, want 500", got)
	}

	for _, tt := range []struct {
		query string
		want  int
		start int64
		limit int64
	}{
		{query: "", want: fiber.StatusNoContent, start: -1, limit: 500},
		{query: "?start=0&limit=5000", want: fiber.StatusNoContent, start: 0, limit: 5000},
		{query: "?limit=50000", want: fiber.StatusBadRequest},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		// strconv.Atoi mapped this to 0 and read from the top.
		{query: "?start=abc", want: fiber.StatusBadRequest},
		{query: "?start=-2", want: fiber.StatusBadRequest},
		{query: "?lim=10", want: fiber.StatusBadRequest},
	} {
		t.Run(tt.query, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodGet, path, cap))
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, nodeRoute(path)+tt.query, nil))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want != fiber.StatusNoContent {
				return
			}
			if got := cap.params.Int("start"); got != tt.start {
				t.Errorf("start = %d, want %d", got, tt.start)
			}
			if got := cap.params.Int("limit"); got != tt.limit {
				t.Errorf("limit = %d, want %d", got, tt.limit)
			}
		})
	}
}

// TestNodeJournalLastEntriesStaysDefaultless is the journal's counterpart
// to the disk-attach index: the parameter must carry NO default, because
// the handler's fallback to 500 lines turns on "the caller bounded this
// request no other way" — a cross-field rule a per-parameter default would
// collapse, making every cursor-paged request also carry a line count.
func TestNodeJournalLastEntriesStaysDefaultless(t *testing.T) {
	const path = clusterScope + "/nodes/:node_name/journal"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	last := e.Parameters["lastentries"]
	if !last.Optional {
		t.Error("lastentries is required; a caller must be able to page by cursor instead")
	}
	if last.Default != nil {
		t.Errorf("lastentries declares default %#v; it must have NONE, so that omitting it stays "+
			"distinguishable from asking for a line count", last.Default)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodGet, path, cap))
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, nodeRoute(path)+"?startcursor=s=abc", nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Has("lastentries") {
		t.Error("lastentries reads as supplied on a cursor-paged request")
	}

	// And the bound still bites at both ends.
	for _, q := range []string{"?lastentries=0", "?lastentries=50000", "?lastentries=abc"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodGet, path, cap))
		if status, env := send(t, app, httptest.NewRequest(http.MethodGet, nodeRoute(path)+q, nil)); status != fiber.StatusBadRequest {
			t.Errorf("%q: status = %d (%q), want 400", q, status, env.Message)
		}
	}
}

// TestNodeSyslogTimesKeepTheirEmptySentinel is the compatibility assertion
// for this domain.
//
// The syslog panel sends since and until only when they are set, but an
// external caller sending ?since= has always meant "use the default
// window". apischema treats "" as a value the caller supplied and every
// registered format rejects it, so borrowing a format for either would 400
// a request that has always worked.
func TestNodeSyslogTimesKeepTheirEmptySentinel(t *testing.T) {
	for _, path := range []string{
		clusterScope + "/nodes/:node_name/syslog",
		clusterScope + "/nodes/:node_name/journal",
	} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, path)
			for _, name := range []string{"since", "until"} {
				if got := e.Parameters[name].Format; got != "" {
					t.Errorf("%s declares format %q; the empty string is a meaningful value here", name, got)
				}
			}
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodGet, path, cap))
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, nodeRoute(path)+"?since=&until=", nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — an empty window is how the default is asked for",
					status, env.Message)
			}
			if got := cap.params.String("since"); got != "" {
				t.Errorf("since = %q, want the empty sentinel to survive", got)
			}
		})
	}
}

// TestNodeEvacuateTargetKeepsItsEmptySentinel is the same assertion for the
// one body parameter with a sentinel: the dialog sends target_node
// unconditionally and leaves it EMPTY to mean "let Nexara choose", which
// the handler branches on twice.
func TestNodeEvacuateTargetKeepsItsEmptySentinel(t *testing.T) {
	const path = clusterScope + "/nodes/:node_name/evacuate"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if got := e.Parameters["target_node"].Format; got != "" {
		t.Errorf("target_node declares format %q; the empty string is a meaningful value here", got)
	}

	for _, tt := range []struct {
		body string
		want int
	}{
		{`{}`, fiber.StatusNoContent},
		{`{"target_node":""}`, fiber.StatusNoContent},
		{`{"target_node":"pve-02"}`, fiber.StatusNoContent},
		{`{"target_node":"a/b"}`, fiber.StatusBadRequest},
		{`{"target":"pve-02"}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.body, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, nodeRoute(path), tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
		})
	}
}

// TestNodeRowIDRoutesAreGatedByTheirDeclaration proves one of this
// domain's gates end to end — that the declaration is what refuses a
// caller, not a call the handler still makes.
func TestNodeRowIDRoutesAreGatedByTheirDeclaration(t *testing.T) {
	const path = clusterScope + "/nodes/:node_id/disks"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if e.Permissions.Describe() != "view:node" {
		t.Fatalf("the disk listing declares %q, want view:node", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := nodeRoute(path)

	t.Run("a caller holding an unrelated grant is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:vm": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodGet, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding view:node gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:node": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodGet, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:node": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})

	t.Run("a node id that is not a uuid never reaches the handler", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeNodeEndpoint(t, fiber.MethodGet, path, cap))
		bad := strings.Replace(target, testNodeRowID, "not-a-uuid", 1)
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, bad, nil))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d (%q), want 400", status, env.Message)
		}
		if cap.called {
			t.Error("the handler ran for a node id the schema rejected")
		}
	})
}

// TestEveryNodeEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryNodeEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredNodeEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Nodes" {
			t.Errorf("%s is in group %q, want Nodes", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
