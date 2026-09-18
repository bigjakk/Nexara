package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts — rather
// than a fixture shaped like them, so a change to registry_rolling_update.go
// that quietly loosened a parameter would show up here.

// rollingRouteCount is how many endpoints registerRollingUpdateEndpoints
// declares. It is ALL 19 of RollingUpdateHandler's routes; nothing in this
// domain was left legacy. See vmRouteCount in registry_vms_test.go for why the
// registry total is a sum of per-domain constants.
const rollingRouteCount = 19

const (
	testRollingJobID  = "7d2f1a55-3c84-4e19-9b60-00000000001a"
	testRollingNodeID = "7d2f1a55-3c84-4e19-9b60-00000000002b"
	testRollingNode   = "pve-02"
)

func rollingRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":node_id", testRollingNodeID,
		":node", testRollingNode,
		":id", testRollingJobID,
	).Replace(path)
}

// rollingLegacyPermissions is what each handler checked with a hand-placed
// requireClusterPerm call BEFORE Phase 6h, transcribed from
// `git show HEAD:internal/api/handlers/rolling_update.go` at commit 3ca85e9.
//
// The value is the full "action:resource", because this domain is the only one
// in the batch that uses TWO resources. That split is the thing worth reviewing:
// ssh_credentials gates the material a job upgrades WITH, and all seven of its
// routes are manage — including the reads, because GetSSHCredentials reports a
// stored root credential's username, port and auth type and ListSSHKnownHosts
// reports every node Nexara can reach over SSH. There is no
// view:ssh_credentials at all.
//
// Every entry is one call, and every call hoists: each handler resolved the
// cluster from its own path with clusterIDFromParam and then made exactly one
// static requireClusterPerm call.
var rollingLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/rolling-updates":                                     "view:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates":                                    "manage:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/preflight-ha":                       "view:rolling_update",
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id":                                 "view:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/start":                          "manage:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/cancel":                         "manage:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/pause":                          "manage:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/resume":                         "manage:rolling_update",
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes":                           "view:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/confirm-upgrade": "manage:rolling_update",
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/skip":            "manage:rolling_update",
	"GET /api/v1/clusters/:cluster_id/nodes/:node/packages":                                "view:rolling_update",

	"GET /api/v1/clusters/:cluster_id/ssh-credentials":        "manage:ssh_credentials",
	"PUT /api/v1/clusters/:cluster_id/ssh-credentials":        "manage:ssh_credentials",
	"DELETE /api/v1/clusters/:cluster_id/ssh-credentials":     "manage:ssh_credentials",
	"POST /api/v1/clusters/:cluster_id/ssh-credentials/test":  "manage:ssh_credentials",
	"GET /api/v1/clusters/:cluster_id/ssh-known-hosts":        "manage:ssh_credentials",
	"POST /api/v1/clusters/:cluster_id/ssh-known-hosts":       "manage:ssh_credentials",
	"DELETE /api/v1/clusters/:cluster_id/ssh-known-hosts/:id": "manage:ssh_credentials",
}

// declaredRollingEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// The membership test is STRUCTURAL — one of the domain's three prefixes, or one
// of the two resources its declarations name — and deliberately not "is it in
// rollingLegacyPermissions". Filtering on the tally's own keys is how the reverse
// check at the bottom of TestRollingUpdateRoutesDeclareTheSamePermissionTheyEnforced
// becomes vacuous: every key would be in the table by construction, so a
// twentieth route added alongside a bumped rollingRouteCount would drop out of
// the tally with nothing failing. See declaredACMEEndpoints, which carries the
// same fix for the same reason.
//
// The resource clause is what catches the package preview, which hangs off
// /nodes/:node — a prefix shared with NodeHandler's 38 routes and ACME's six —
// and it catches a future route wherever it is mounted.
func declaredRollingEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		permission := e.Permissions.Describe()
		inDomain := strings.HasPrefix(e.Path, rollingScope) ||
			strings.HasPrefix(e.Path, sshCredentialScope) ||
			strings.HasPrefix(e.Path, sshKnownHostScope) ||
			strings.Contains(permission, rollingUpdateResource) ||
			strings.Contains(permission, sshCredentialResource)
		if inDomain {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestRollingUpdateRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change: 19 hand-placed calls in,
// 19 declared cluster-scoped Checks out, none kept in a handler.
func TestRollingUpdateRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredRollingEndpoints(t)
	if len(declared) != rollingRouteCount {
		t.Fatalf("the registry declares %d rolling-update routes, want %d", len(declared), rollingRouteCount)
	}
	if len(rollingLegacyPermissions) != rollingRouteCount {
		t.Fatalf("rollingLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(rollingLegacyPermissions), rollingRouteCount)
	}

	byPermission := map[string]int{}
	for key, want := range rollingLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeCluster)
		}
		byPermission[want]++
	}
	// Stated rather than left to be counted. The number that matters is the
	// seven manage:ssh_credentials: if any of them drifted to a view resource
	// the stored credential's shape would become readable to every Viewer.
	for _, want := range []struct {
		permission string
		count      int
	}{
		{"view:rolling_update", 5},
		{"manage:rolling_update", 7},
		{"manage:ssh_credentials", 7},
	} {
		if byPermission[want.permission] != want.count {
			t.Errorf("%d routes declare %s, want %d", byPermission[want.permission], want.permission, want.count)
		}
	}
	if len(byPermission) != 3 {
		t.Errorf("the domain declares %d distinct permissions, want 3: %v", len(byPermission), byPermission)
	}

	for key := range declared {
		if _, listed := rollingLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in rollingLegacyPermissions — a new rolling-update route "+
				"must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestRollingUpdateRoutesAreGatedByTheirDeclaration proves the 19 hoisted
// permissions are the permissions the routes enforce, end to end.
//
// The "unrelated grant" is the OTHER resource in this domain, not a foreign one:
// manage:rolling_update must not open an SSH credential route and
// manage:ssh_credentials must not start a job, which is the split the tally
// above only asserts on paper.
func TestRollingUpdateRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range rollingLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := rollingRoute(path)

			unrelated := "manage:ssh_credentials"
			if want == unrelated {
				unrelated = "manage:rolling_update"
			}
			app := newRegistryApp(t, stubAuth(map[string]bool{unrelated: true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding only %q got %d, want 403", unrelated, status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
			status, _ = send(t, app, httptest.NewRequest(method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Fatalf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// probeRollingEndpoint is a declared rolling-update endpoint with its handler
// swapped for a capture and its gate removed, so a parameter test needs neither
// a database nor a session.
func probeRollingEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestPreflightIsRegisteredBeforeTheJobIDRoute is the ordering assertion the
// legacy block did not need and the registry has to state.
//
// Fiber matches in registration order, and /rolling-updates/preflight-ha is the
// same WIDTH as /rolling-updates/:id. Nothing captures it today because the two
// carry different methods, but that is a coincidence of the current route set
// rather than a property of the paths — declaring the literal first is what
// keeps it true after the next route is added. The alert summary carries the
// same note for the same reason.
func TestPreflightIsRegisteredBeforeTheJobIDRoute(t *testing.T) {
	s := newRouteStubServer(t)
	preflight, byID := -1, -1
	for i, e := range s.registry.Endpoints() {
		switch e.Path {
		case rollingScope + "/preflight-ha":
			preflight = i
		case rollingScope + "/:id":
			byID = i
		}
	}
	if preflight < 0 || byID < 0 {
		t.Fatalf("expected both routes to be declared; preflight=%d byID=%d", preflight, byID)
	}
	if preflight > byID {
		t.Errorf("POST %s/preflight-ha is declared after %s/:id — Fiber matches in registration order, "+
			"and a later route on that path would swallow it", rollingScope, rollingScope)
	}

	// And end to end: the literal path reaches its own handler.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeRollingEndpoint(t, fiber.MethodPost, rollingScope+"/preflight-ha", cap),
		probeRollingEndpoint(t, fiber.MethodGet, rollingScope+"/:id", &capture{}))
	target := pathPrefix + "clusters/" + testClusterID + "/rolling-updates/preflight-ha"
	status, env := send(t, app, jsonRequest(http.MethodPost, target, `{"nodes":["`+testRollingNode+`"]}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.called {
		t.Error("the preflight request reached a different handler")
	}
}

// TestRollingUpdatePagingIsDeclaredAndBounded covers the two query parameters
// that were previously UNDECLARED.
//
// ListJobs read them straight off c.Query with strconv.Atoi and a silent
// fallback, so ?limit=0 and ?limit=500 both came back as twenty rows with no
// indication. An undeclared key is now a 400, so the first half of this test is
// a compatibility check — the keys must exist — and the second is the trade the
// alert, migration, CVE and PBS-task listings already made: a page size the
// caller did not choose is indistinguishable from one they did.
func TestRollingUpdatePagingIsDeclaredAndBounded(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, rollingScope)
	for _, name := range []string{"limit", "offset"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Fatalf("GET %s declares no %q — it was read off c.Query before the migration, so an "+
				"undeclared key now 400s every caller that sends it", rollingScope, name)
		}
		if !prop.Optional {
			t.Errorf("%s is required; it has always been optional", name)
		}
		src := apischema.ResolveSource(name, prop, fiber.MethodGet, pathParamNames(rollingScope))
		if src != apischema.SourceQuery {
			t.Errorf("%s resolves to %q, want the query string", name, src)
		}
	}
	if got := e.Parameters["limit"].Default; got != 20 {
		t.Errorf("limit defaults to %v, want 20 — the value the handler substituted", got)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRollingEndpoint(t, fiber.MethodGet, rollingScope, cap))
	base := pathPrefix + "clusters/" + testClusterID + "/rolling-updates"

	for _, tc := range []struct {
		query string
		want  int64
	}{
		{"", 20},
		{"?limit=50", 50},
		{"?limit=100", 100},
	} {
		cap.called = false
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, base+tc.query, nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("%q: status = %d (%q), want 204", tc.query, status, env.Message)
		}
		if got := cap.params.Int("limit"); got != tc.want {
			t.Errorf("%q: limit = %d, want %d", tc.query, got, tc.want)
		}
	}

	for _, bad := range []string{"?limit=0", "?limit=101", "?limit=many", "?offset=-1"} {
		cap.called = false
		status, _ := send(t, app, httptest.NewRequest(http.MethodGet, base+bad, nil))
		if status != fiber.StatusBadRequest {
			t.Errorf("%q: status = %d, want 400 rather than a silent fallback", bad, status)
		}
		if cap.called {
			t.Errorf("%q: the handler ran for an out-of-range page", bad)
		}
	}
}

// TestRollingUpdateNodeListIsBoundedAndTyped pins the create body's node list
// against what the handler enforced, and records the one element rule that is
// deliberately TIGHTER.
//
// The handler refused an empty list ("At least one node is required") and more
// than 64 ("Too many nodes (max 64)"), and checked each element only for
// `n == "" || len(n) > 128`. The element rule is now the node-name format, which
// is a superset of what Proxmox itself allows in a node name — so nothing a real
// cluster can contain is refused, while a slash or a control character no longer
// reaches GetNodeAptUpdates and the orchestrator's SSH layer.
func TestRollingUpdateNodeListIsBoundedAndTyped(t *testing.T) {
	for _, path := range []string{rollingScope, rollingScope + "/preflight-ha"} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, path)
			prop, ok := e.Parameters["nodes"]
			if !ok {
				t.Fatal("declares no nodes parameter")
			}
			if prop.Optional {
				t.Error("nodes is optional; both routes have always refused a request without one")
			}
			if prop.Type != apischema.Array {
				t.Fatalf("nodes declares type %q, want %q", prop.Type, apischema.Array)
			}
			if prop.MinLength == nil || *prop.MinLength != 1 {
				t.Errorf("nodes declares min length %v, want 1", prop.MinLength)
			}
			if prop.MaxLength == nil || *prop.MaxLength != rollingMaxNodeCount {
				t.Errorf("nodes declares max length %v, want %d", prop.MaxLength, rollingMaxNodeCount)
			}
			if prop.Items == nil || prop.Items.Format != "node-name" {
				t.Fatalf("nodes declares items %+v, want the node-name format", prop.Items)
			}

			base := map[string]any{"cluster_id": testClusterID}
			validate := func(nodes any) error {
				in := map[string]any{"cluster_id": base["cluster_id"], "nodes": nodes}
				_, err := e.Parameters.Validate(in)
				return err
			}
			if err := validate([]any{"pve-01", "pve-02"}); err != nil {
				t.Errorf("a real node list was refused: %v", err)
			}
			for _, bad := range []struct {
				name  string
				nodes any
			}{
				{"empty list", []any{}},
				{"empty element", []any{""}},
				{"a traversal", []any{".."}},
				{"a path separator", []any{"pve-01/../pve-02"}},
				{"65 nodes", func() any {
					out := make([]any, 65)
					for i := range out {
						out[i] = "pve-01"
					}
					return out
				}()},
			} {
				if err := validate(bad.nodes); err == nil {
					t.Errorf("%s was accepted as a node list", bad.name)
				}
			}
		})
	}
}

// TestRollingUpdateCreateKeepsItsDefaults pins the four booleans and the
// ha_policy sentinel against what the handler substituted.
//
// The two that default to TRUE are the point: a job created without saying
// otherwise DRAINS each node before upgrading it and migrates its guests back
// afterwards. Flipping either default silently changes what every existing
// client's create does.
func TestRollingUpdateCreateKeepsItsDefaults(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, rollingScope)
	for _, want := range []struct {
		name    string
		value   bool
		because string
	}{
		{"reboot_after_update", false, "a job only reboots when apt asks, unless the caller says otherwise"},
		{"auto_restore_guests", true, "a drained job migrates its guests back"},
		{"auto_upgrade", false, "an upgrade runs by hand unless SSH is configured and the caller opts in"},
		{"drain_guests", true, "the drained shape is the only one that existed before in-place was added"},
	} {
		prop, ok := e.Parameters[want.name]
		if !ok {
			t.Errorf("declares no %q parameter", want.name)
			continue
		}
		if !prop.Optional {
			t.Errorf("%q is required; the handler has always substituted a default", want.name)
		}
		if prop.Default != want.value {
			t.Errorf("%q defaults to %v, want %v — %s", want.name, prop.Default, want.value, want.because)
		}
	}

	policy, ok := e.Parameters["ha_policy"]
	if !ok {
		t.Fatal("declares no ha_policy parameter")
	}
	if policy.Default != "warn" {
		t.Errorf("ha_policy defaults to %v, want warn", policy.Default)
	}
	// The EMPTY string stays a member: the handler read it as "unspecified" and
	// substituted warn, so dropping it would 400 a request that has always
	// worked.
	if !slices.Contains(policy.Enum, "") {
		t.Errorf("ha_policy declares enum %v with no empty member; \"\" has always meant warn", policy.Enum)
	}
	for _, want := range []string{"strict", "warn"} {
		if !slices.Contains(policy.Enum, want) {
			t.Errorf("ha_policy declares enum %v, which is missing %q", policy.Enum, want)
		}
	}

	// And end to end: the enum now refuses what the handler refused by hand, and
	// the defaults arrive.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRollingEndpoint(t, fiber.MethodPost, rollingScope, cap))
	target := pathPrefix + "clusters/" + testClusterID + "/rolling-updates"

	status, env := send(t, app, jsonRequest(http.MethodPost, target, `{"nodes":["`+testRollingNode+`"]}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.params.Bool("drain_guests") || !cap.params.Bool("auto_restore_guests") {
		t.Error("a create with no body flags did not arrive drained and auto-restoring")
	}
	if cap.params.Bool("reboot_after_update") || cap.params.Bool("auto_upgrade") {
		t.Error("a create with no body flags arrived rebooting or auto-upgrading")
	}
	if got := cap.params.Int("parallelism"); got != 1 {
		t.Errorf("parallelism = %d, want 1", got)
	}

	cap.called = false
	status, _ = send(t, app, jsonRequest(http.MethodPost, target,
		`{"nodes":["`+testRollingNode+`"],"ha_policy":"lenient"}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an ha_policy outside the vocabulary", status)
	}
	if cap.called {
		t.Error("the handler ran for an unknown ha_policy")
	}

	// Parallelism 0 is a REFUSAL now rather than a silent 1. The wizard clamps
	// to [1, selected nodes] before it sends, so nothing Nexara's own UI
	// produces changes.
	cap.called = false
	status, _ = send(t, app, jsonRequest(http.MethodPost, target,
		`{"nodes":["`+testRollingNode+`"],"parallelism":0}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for parallelism 0", status)
	}
}

// TestRollingUpdateNotifyChannelKeepsItsEmptySentinel is the compatibility
// assertion for the one uuid-valued body field whose EMPTY value is meaningful.
//
// The handler parsed it only `if *req.NotifyChannelID != ""`, so "" has always
// meant "notify nobody". Every registered format refuses the empty string, which
// is why the rule is a pattern.
func TestRollingUpdateNotifyChannelKeepsItsEmptySentinel(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, rollingScope)
	prop, ok := e.Parameters["notify_channel_id"]
	if !ok {
		t.Fatal("declares no notify_channel_id")
	}
	if prop.Format != "" {
		t.Errorf("notify_channel_id declares format %q; every registered format rejects \"\", which is "+
			"the no-channel sentinel", prop.Format)
	}
	if prop.Pattern != emptyOrUUID {
		t.Errorf("notify_channel_id declares pattern %q, want the empty-or-uuid one", prop.Pattern)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRollingEndpoint(t, fiber.MethodPost, rollingScope, cap))
	target := pathPrefix + "clusters/" + testClusterID + "/rolling-updates"
	for _, tc := range []struct {
		value string
		want  int
	}{
		{`""`, fiber.StatusNoContent},
		{`"` + testClusterID + `"`, fiber.StatusNoContent},
		{`"not-a-uuid"`, fiber.StatusBadRequest},
	} {
		cap.called = false
		body := `{"nodes":["` + testRollingNode + `"],"notify_channel_id":` + tc.value + `}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != tc.want {
			t.Errorf("notify_channel_id=%s: status = %d (%q), want %d", tc.value, status, env.Message, tc.want)
		}
	}
}

// TestSSHCredentialParamsMatchWhatTheHandlerEnforced pins the credential body,
// including the two substitutions the handler KEEPS.
//
// username and port are both defaulted by the declaration AND normalised in the
// handler, and that duplication is deliberate: a Default only fills an ABSENT
// value, while the form sends "" and 0 for a CLEARED field. Dropping either half
// would turn a cleared field into a 400 on a form that has worked for years.
func TestSSHCredentialParamsMatchWhatTheHandlerEnforced(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, sshCredentialScope)

	if got := e.Parameters["username"].Default; got != "root" {
		t.Errorf("username defaults to %v, want root", got)
	}
	if got := e.Parameters["port"].Default; got != 22 {
		t.Errorf("port defaults to %v, want 22", got)
	}
	// Minimum 0 rather than 1, on purpose: the form's number input sends 0 for
	// a cleared field and the handler reads that as 22.
	if m := e.Parameters["port"].Minimum; m == nil || *m != 0 {
		t.Errorf("port declares minimum %v, want 0 — a cleared number input sends 0", m)
	}
	if m := e.Parameters["port"].Maximum; m == nil || *m != 65535 {
		t.Errorf("port declares maximum %v, want 65535", m)
	}
	auth := e.Parameters["auth_type"]
	if auth.Optional {
		t.Error("auth_type is optional; the handler has always refused a request without it")
	}
	if !slices.Equal(auth.Enum, []string{"password", "key"}) {
		t.Errorf("auth_type declares enum %v, want [password key]", auth.Enum)
	}
	for _, name := range []string{"password", "private_key"} {
		if !e.Parameters[name].Optional {
			t.Errorf("%q is required; which of the two is needed depends on auth_type, which is a "+
				"cross-field rule the handler owns", name)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRollingEndpoint(t, fiber.MethodPut, sshCredentialScope, cap))
	target := pathPrefix + "clusters/" + testClusterID + "/ssh-credentials"

	// The cleared-field shape the form sends.
	status, env := send(t, app, jsonRequest(http.MethodPut, target,
		`{"auth_type":"key","username":"","port":0,"private_key":"x"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 for a cleared username and port", status, env.Message)
	}
	if got := cap.params.String("username"); got != "" {
		t.Errorf("username = %q; the DECLARATION must pass the empty value through so the handler can "+
			"normalise it — a default that fired here would hide the cleared field", got)
	}
	if got := cap.params.Int("port"); got != 0 {
		t.Errorf("port = %d, want 0 passed through to the handler's own normalisation", got)
	}

	// Omitted entirely: the declared defaults fill in.
	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPut, target, `{"auth_type":"key","private_key":"x"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("username"); got != "root" {
		t.Errorf("username = %q, want root", got)
	}
	if got := cap.params.Int("port"); got != 22 {
		t.Errorf("port = %d, want 22", got)
	}

	for _, bad := range []string{
		`{"private_key":"x"}`,
		`{"auth_type":"kerberos","private_key":"x"}`,
		`{"auth_type":"key","port":70000,"private_key":"x"}`,
	} {
		cap.called = false
		status, _ := send(t, app, jsonRequest(http.MethodPut, target, bad))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, status)
		}
		if cap.called {
			t.Errorf("%s: the handler ran", bad)
		}
	}
}

// TestSSHNodeNameUsesTheTightestFormat pins the two SSH routes that take a node
// name in their body.
//
// handlers.validateNodeName is the THIRD of four spellings of this check in the
// tree, and it stays in the handler: it guards the value at the point where it
// is about to be resolved to an address and dialled, and an exported validator
// with one caller is exactly the opt-in-guard shape this codebase has been
// bitten by. The declaration states the TIGHTEST of the four one layer earlier.
func TestSSHNodeNameUsesTheTightestFormat(t *testing.T) {
	for _, r := range []struct{ method, path string }{
		{fiber.MethodPost, sshCredentialScope + "/test"},
		{fiber.MethodPost, sshKnownHostScope},
	} {
		t.Run(r.path, func(t *testing.T) {
			e := declaredEndpoint(t, r.method, r.path)
			prop, ok := e.Parameters["node_name"]
			if !ok {
				t.Fatal("declares no node_name parameter")
			}
			if prop.Format != "node-name" {
				t.Errorf("node_name declares format %q, want node-name", prop.Format)
			}
			if prop.Optional {
				t.Error("node_name is optional; both routes act on exactly one node")
			}
		})
	}

	// The pin's fingerprint bound is the handler's own, restated: required and
	// at most 128 characters.
	pin := declaredEndpoint(t, fiber.MethodPost, sshKnownHostScope).Parameters["expected_fingerprint"]
	if pin.Optional {
		t.Error("expected_fingerprint is optional; the pin refuses a request without it")
	}
	if pin.MinLength == nil || *pin.MinLength != 1 {
		t.Errorf("expected_fingerprint declares min length %v, want 1", pin.MinLength)
	}
	if pin.MaxLength == nil || *pin.MaxLength != 128 {
		t.Errorf("expected_fingerprint declares max length %v, want 128", pin.MaxLength)
	}
	// No format: an SSH host-key fingerprint is "SHA256:" plus base64, which
	// apischema's fingerprint-sha256 — the hex form a TLS certificate prints —
	// would refuse outright.
	if pin.Format != "" {
		t.Errorf("expected_fingerprint declares format %q; an SSH fingerprint is not the TLS shape", pin.Format)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRollingEndpoint(t, fiber.MethodPost, sshKnownHostScope, cap))
	target := pathPrefix + "clusters/" + testClusterID + "/ssh-known-hosts"
	for _, bad := range []string{
		`{"expected_fingerprint":"SHA256:abc"}`,
		`{"node_name":"","expected_fingerprint":"SHA256:abc"}`,
		`{"node_name":"..","expected_fingerprint":"SHA256:abc"}`,
		`{"node_name":"` + testRollingNode + `"}`,
		`{"node_name":"` + testRollingNode + `","expected_fingerprint":""}`,
	} {
		cap.called = false
		status, _ := send(t, app, jsonRequest(http.MethodPost, target, bad))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, status)
		}
		if cap.called {
			t.Errorf("%s: the handler ran", bad)
		}
	}
	status, env := send(t, app, jsonRequest(http.MethodPost, target,
		`{"node_name":"`+testRollingNode+`","expected_fingerprint":"SHA256:AAAABBBBCCCCDDDD"}`))
	if status != fiber.StatusNoContent {
		t.Errorf("a real pin was refused: status = %d (%q)", status, env.Message)
	}
}

// TestRollingUpdateIdentifiersAreUUIDs keeps the path identifiers honest. Both
// are Nexara's own row ids, and node_id in particular is NOT the Proxmox node
// name the same domain's package preview takes — confusing the two is what the
// format makes impossible.
func TestRollingUpdateIdentifiersAreUUIDs(t *testing.T) {
	for key := range rollingLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		for _, name := range []string{"id", "node_id"} {
			prop, declared := e.Parameters[name]
			if !declared {
				continue
			}
			if prop.Format != "uuid" {
				t.Errorf("%s: %q declares format %q, want uuid", key, name, prop.Format)
			}
			if src := apischema.ResolveSource(name, prop, method, pathParamNames(path)); src != apischema.SourcePath {
				t.Errorf("%s: %q resolves to %q, want the path — the permission middleware reads that "+
					"name out of the path", key, name, src)
			}
		}
	}

	// And the package preview's :node is the Proxmox NAME, not a uuid.
	preview := declaredEndpoint(t, fiber.MethodGet, rollingNodeScope+"/packages")
	if got := preview.Parameters["node"].Format; got != "node-name" {
		t.Errorf("the package preview's node declares format %q, want node-name", got)
	}
}

// TestEveryRollingUpdateEndpointIsDocumented holds the
// declaration-is-the-documentation rule for this domain.
func TestEveryRollingUpdateEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredRollingEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Rolling Updates" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Rolling Updates")
		}
	}
}
