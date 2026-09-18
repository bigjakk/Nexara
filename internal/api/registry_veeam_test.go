package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_veeam.go
// that quietly loosened a parameter would show up here.

// veeamRouteCount is how many endpoints registerVeeamEndpoints declares.
// See vmRouteCount in registry_vms_test.go for why the registry total is a
// sum of per-domain constants rather than one number.
const veeamRouteCount = 25

const (
	testVeeamServerID     = "9d5e1c70-2b34-4a18-8c66-000000000007"
	testVeeamUpstreamID   = "1f0b6c22-7d4e-4a55-9b31-000000000008"
	testVeeamSecondaryID  = "4a7c8e91-5b62-4d03-8f14-000000000009"
	testVeeamSessionIDVal = "6e2d9f43-0a71-4c88-9d25-00000000000a"
)

func veeamRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":vm_id", testVMID,
		":id", testVeeamServerID,
		":repository_id", testVeeamUpstreamID,
		":platform_id", testVeeamUpstreamID,
		":object_id", testVeeamSecondaryID,
		":job_id", testVeeamUpstreamID,
		":session_id", testVeeamSessionIDVal,
	).Replace(path)
}

// veeamRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// TWENTY-FOUR of the 25 are here, the highest proportion of any domain so
// far, and it is the domain's own shape rather than an artefact of the
// migration. A Veeam server can protect SEVERAL Proxmox clusters — unlike a
// PBS server, which maps to at most one — so the server registry itself is an
// instance-wide resource and its path names no cluster at all. The data those
// servers produce IS per-cluster, but the cluster is resolved at request time
// through the veeam_platforms mapping rather than read out of the URL.
//
// The one route that is NOT here is the VM detail page's Veeam card: its
// subject is a guest, its path starts with /clusters/:cluster_id, and it gates
// on that cluster like every other per-guest route.
//
// The map is built rather than written out because the 24 share only THREE
// reasons between them — 11 global, 4 filtered, 9 deferred — and twenty-four
// copies of three sentences is a list nobody re-reads, which is the failure the
// exception surface exists to avoid.
var veeamRoutesOutsideTheClusterCheckShape = func() map[string]string {
	const global = "global: a Veeam server can protect several clusters, so the registry and the " +
		"answers that span every one of them are instance-wide; the path names no cluster"
	const filtered = "Advisory: accessibleClusters builds the scope and permitsPlatform applies it per " +
		"row through the server's veeam_platforms mapping; the listing spans every cluster the server " +
		"protects, so there is no single cluster to gate on"
	const deferred = "Deferred: the cluster comes from the row — the handler loads the object, job or " +
		"session and gates on the cluster its Veeam platform maps to, or instance-wide when unmapped"

	out := map[string]string{}
	for _, r := range []string{
		"POST /api/v1/veeam-servers",
		"GET /api/v1/veeam-servers",
		"GET /api/v1/veeam-servers/:id",
		"PUT /api/v1/veeam-servers/:id",
		"DELETE /api/v1/veeam-servers/:id",
		"POST /api/v1/veeam-servers/:id/test",
		"GET /api/v1/veeam-servers/:id/repositories",
		"GET /api/v1/veeam-servers/:id/repositories/:repository_id/metrics",
		"GET /api/v1/veeam-servers/:id/platforms",
		"PUT /api/v1/veeam-servers/:id/platforms/:platform_id",
		"GET /api/v1/veeam-servers/:id/infrastructure",
	} {
		out[r] = global
	}
	for _, r := range []string{
		"GET /api/v1/veeam-servers/:id/jobs",
		"GET /api/v1/veeam-servers/:id/sessions",
		"GET /api/v1/veeam-servers/:id/backup-objects",
		"GET /api/v1/veeam-servers/:id/orphaned-objects",
	} {
		out[r] = filtered
	}
	for _, r := range []string{
		"GET /api/v1/veeam-servers/:id/backup-objects/:object_id/restore-points",
		"PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest",
		"POST /api/v1/veeam-servers/:id/jobs/:job_id/start",
		"POST /api/v1/veeam-servers/:id/jobs/:job_id/stop",
		"POST /api/v1/veeam-servers/:id/jobs/:job_id/enable",
		"POST /api/v1/veeam-servers/:id/jobs/:job_id/disable",
		"POST /api/v1/veeam-servers/:id/sessions/:session_id/stop",
		"GET /api/v1/veeam-servers/:id/sessions/:session_id/logs",
		"GET /api/v1/veeam-servers/:id/sessions/:session_id/tasks",
	} {
		out[r] = deferred
	}
	return out
}()

// veeamLegacyPermissions is what each handler checked with hand-placed calls
// BEFORE Phase 6g, transcribed from `git show HEAD:internal/api/handlers/veeam.go`
// and its four siblings at commit facdf56.
//
// calls counts the permission calls each handler made, per ROUTE rather than
// per source line, the way backupLegacyPermissions does. Two routes make TWO:
//
//   - List and Get run requirePerm("view","veeam") as the gate AND
//     hasGlobalPerm("manage","veeam") through callerSeesUsername, which decides
//     whether the configured account name is serialized. Only the first hoists;
//     the second is a rendering decision no middleware can make.
//   - MapBackupObjectGuest runs accessibleClusters("view","veeam") to resolve the
//     object and then requireClusterPerm("manage","veeam", …) on the cluster its
//     platform maps to.
var veeamLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
	// hoisted is how many of `calls` move into middleware. It is stated rather
	// than derived from the shape because the two two-call Checks hoist only
	// one of their two, and a shape-derived number would silently claim both.
	hoisted int
}{
	"POST /api/v1/veeam-servers":                                        {"manage:veeam", "Check", 1, 1},
	"GET /api/v1/veeam-servers":                                         {"view:veeam", "Check", 2, 1},
	"GET /api/v1/veeam-servers/:id":                                     {"view:veeam", "Check", 2, 1},
	"PUT /api/v1/veeam-servers/:id":                                     {"manage:veeam", "Check", 1, 1},
	"DELETE /api/v1/veeam-servers/:id":                                  {"delete:veeam", "Check", 1, 1},
	"POST /api/v1/veeam-servers/:id/test":                               {"manage:veeam", "Check", 1, 1},
	"GET /api/v1/veeam-servers/:id/repositories":                        {"view:veeam", "Check", 1, 1},
	"GET /api/v1/veeam-servers/:id/repositories/:repository_id/metrics": {"view:veeam", "Check", 1, 1},
	"GET /api/v1/veeam-servers/:id/platforms":                           {"view:veeam", "Check", 1, 1},
	"PUT /api/v1/veeam-servers/:id/platforms/:platform_id":              {"manage:veeam", "Check", 1, 1},
	"GET /api/v1/veeam-servers/:id/infrastructure":                      {"view:veeam", "Check", 1, 1},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/veeam":                 {"view:veeam", "Check", 1, 1},

	"GET /api/v1/veeam-servers/:id/jobs":             {"view:veeam", "Advisory", 1, 0},
	"GET /api/v1/veeam-servers/:id/sessions":         {"view:veeam", "Advisory", 1, 0},
	"GET /api/v1/veeam-servers/:id/backup-objects":   {"view:veeam", "Advisory", 1, 0},
	"GET /api/v1/veeam-servers/:id/orphaned-objects": {"view:veeam", "Advisory", 1, 0},

	"GET /api/v1/veeam-servers/:id/backup-objects/:object_id/restore-points": {"view:veeam", "Deferred", 1, 0},
	"PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest":          {"manage:veeam", "Deferred", 2, 0},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/start":                      {"execute:veeam", "Deferred", 1, 0},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/stop":                       {"execute:veeam", "Deferred", 1, 0},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/enable":                     {"execute:veeam", "Deferred", 1, 0},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/disable":                    {"execute:veeam", "Deferred", 1, 0},
	"POST /api/v1/veeam-servers/:id/sessions/:session_id/stop":               {"execute:veeam", "Deferred", 1, 0},
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/logs":                {"view:veeam", "Deferred", 1, 0},
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/tasks":               {"view:veeam", "Deferred", 1, 0},
}

// declaredVeeamEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It filters on veeamLegacyPermissions' own keys rather than on a path prefix,
// because the domain spans two unrelated prefixes — the /veeam-servers
// collection and one route nested under /clusters/:cluster_id — and a prefix
// filter would either miss the card or sweep in every other cluster route.
func declaredVeeamEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, listed := veeamLegacyPermissions[key]; listed {
			out[key] = e
		}
	}
	return out
}

// TestVeeamRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// this migration a refactor rather than a change.
//
// Twelve of the 28 hand-placed calls move into middleware and 16 stay. The
// tally says so out loud rather than leaving a reader to infer it from
// twenty-four reason strings.
func TestVeeamRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredVeeamEndpoints(t)
	if len(declared) != veeamRouteCount {
		t.Fatalf("the registry declares %d veeam routes, want %d", len(declared), veeamRouteCount)
	}
	if len(veeamLegacyPermissions) != veeamRouteCount {
		t.Fatalf("veeamLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(veeamLegacyPermissions), veeamRouteCount)
	}

	var hoisted, kept int
	for key, want := range veeamLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		hoisted += want.hoisted
		kept += want.calls - want.hoisted

		switch want.shape {
		case "Check":
			if e.Permissions.Check == nil {
				t.Errorf("%s declares %q rather than a Check; its permission was a single static call",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Describe(); got != want.permission {
				t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
			}
			// Scope is the one mistake here that would change who can do what:
			// declaring the server registry cluster-scoped would refuse every
			// operator, and declaring the per-guest card global would let a
			// caller holding view:veeam instance-wide read a cluster they hold
			// nothing on.
			wantScope := ScopeGlobal
			if strings.HasPrefix(e.Path, clusterScope) {
				wantScope = ScopeCluster
			}
			if e.Permissions.Check.Scope != wantScope {
				t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, wantScope)
			}
		case "Advisory":
			if e.Permissions.Advisory == nil {
				t.Errorf("%s declares %q, want Advisory: the listing filters rather than gates",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Advisory.String(); got != want.permission {
				t.Errorf("%s filters on %q but the handler used %q", key, got, want.permission)
			}
			for _, phrase := range []string{"accessibleClusters", "permitsPlatform"} {
				if !strings.Contains(e.Permissions.Advisory.Reason, phrase) {
					t.Errorf("%s: the Advisory reason does not name %q, which is half of what does the "+
						"filtering: %q", key, phrase, e.Permissions.Advisory.Reason)
				}
			}
		case "Deferred":
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: the cluster comes out of a DB lookup",
					key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
			}
			// And the reason has to name the mapping the decision runs through,
			// not merely say that the handler looks at something.
			if !strings.Contains(e.Permissions.Deferred, "veeam_platforms") {
				t.Errorf("%s: the Deferred reason does not name veeam_platforms, which is what resolves "+
					"the cluster: %q", key, e.Permissions.Deferred)
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 12 || kept != 16 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 12 / 16",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := veeamLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in veeamLegacyPermissions — a new veeam route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestVeeamJobControlReasonNamesItsOwnVerb is the assertion behind the one
// Deferred reason that is NOT the row-scope one.
//
// The four job-control routes resolve the same mapping under "execute" rather
// than "view", and a reason string copied from the read routes would document
// the wrong grant to an operator building a role — the exact failure the
// Deferred reason exists to prevent.
func TestVeeamJobControlReasonNamesItsOwnVerb(t *testing.T) {
	for _, action := range []string{"start", "stop", "enable", "disable"} {
		key := "POST " + veeamScope + "/:id/jobs/:job_id/" + action
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, veeamScope+"/:id/jobs/:job_id/"+action)
			if !strings.Contains(e.Permissions.Deferred, `accessibleClusters("execute", "veeam")`) {
				t.Errorf("the Deferred reason does not name the execute-verb resolution: %q", e.Permissions.Deferred)
			}
			if !strings.Contains(e.Permissions.Deferred, "BEFORE loading the job") {
				t.Errorf("the Deferred reason does not state that the coarse gate runs before the row is "+
					"loaded, which is what closes the 404-vs-403 oracle: %q", e.Permissions.Deferred)
			}
		})
	}
}

// TestVeeamGlobalRoutesAreGatedByTheirDeclaration proves the twelve hoisted
// permissions are the permissions the routes enforce, end to end. It is what
// replaces the handler-level TestVeeamRoutes_RequirePermission for the nine
// routes whose gate left the handler body.
func TestVeeamGlobalRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range veeamLegacyPermissions {
		if want.shape != "Check" {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if got := e.Permissions.Describe(); got != want.permission {
				t.Fatalf("declares %q, want %q", got, want.permission)
			}
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			// The limiter is dropped for this fixture: its budget is per-IP and
			// shared, so several subtests firing through one instance would
			// start answering 429 and hide the permission result.
			gated.RateLimiter = nil
			target := veeamRoute(path)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:pbs": true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding an unrelated grant got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want.permission)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
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

// TestVeeamLimitersShareOneBudgetPerGroup pins the property the legacy block
// spelled out in prose and nothing asserted: the three connect routes share ONE
// limiter and the seven control routes share ONE, rather than each route
// constructing its own.
//
// A per-route instance holds a per-route STORE, which multiplies the budget the
// limiter exists to cap — ten domain logons a minute becomes thirty across
// create, update and test — and nothing about the behaviour of a single route
// would look wrong.
//
// It has to be a BEHAVIOURAL test, and that is the whole reason it is written
// this way. Comparing the two fiber.Handler values with reflect.Value.Pointer()
// reads like the obvious check and is vacuous: Pointer() on a func returns the
// CODE pointer, so every closure limiter.New returns — however many separate
// stores they hold — compares equal, and the regression above would sail past
// it. Spending the budget is the only thing that can tell one store from two.
//
// So: fire Max+1 requests SPREAD ACROSS the group's routes and require the last
// to be refused. With one shared store the budget is exhausted; with a store per
// route none of them ever gets close, because Max+1 split N ways is far under
// Max each.
func TestVeeamLimitersShareOneBudgetPerGroup(t *testing.T) {
	// Every endpoint comes from ONE registry, because that is the thing under
	// test: declaredVeeamEndpoints builds a fresh Server, and two calls would
	// hand back two sets of limiters however buildRegistry was written.
	declared := declaredVeeamEndpoints(t)

	groups := []struct {
		name string
		// max is the limiter's own budget, from middleware.go:
		// veeamConnectLimiter is 10/min and veeamControlLimiter is 30/min.
		max  int
		keys []string
	}{
		{"connect", 10, []string{
			"POST " + veeamScope,
			"PUT " + veeamScope + "/:id",
			"POST " + veeamScope + "/:id/test",
		}},
		{"control", 30, []string{
			"POST " + veeamScope + "/:id/jobs/:job_id/start",
			"POST " + veeamScope + "/:id/jobs/:job_id/stop",
			"POST " + veeamScope + "/:id/jobs/:job_id/enable",
			"POST " + veeamScope + "/:id/jobs/:job_id/disable",
			"POST " + veeamScope + "/:id/sessions/:session_id/stop",
			"GET " + veeamScope + "/:id/sessions/:session_id/logs",
			"GET " + veeamScope + "/:id/sessions/:session_id/tasks",
		}},
	}

	limited := map[string]bool{}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			mounted := make([]Endpoint, 0, len(g.keys))
			for _, key := range g.keys {
				limited[key] = true
				e, ok := declared[key]
				if !ok {
					t.Fatalf("%s is not declared", key)
				}
				if e.RateLimiter == nil {
					t.Fatalf("%s declares no rate limiter, but it spends a real Veeam logon", key)
				}
				// Only the handler and the gate are replaced; the RateLimiter is
				// the production instance, which is what makes this a test of
				// the declaration rather than of a fixture.
				e.Handler = (&capture{}).handler()
				e.Permissions = Permissions{SelfService: "limiter fixture; authorization is exercised separately"}
				mounted = append(mounted, e)
			}
			app := newRegistryApp(t, noAuth(), mounted...)

			// Max+1 requests, round-robin over the group's routes, all from the
			// one IP app.Test presents. The first Max may pass; the last must
			// not, and only a SHARED store can refuse it.
			var status int
			for i := 0; i <= g.max; i++ {
				key := g.keys[i%len(g.keys)]
				method, path, _ := strings.Cut(key, " ")
				status, _ = send(t, app, httptest.NewRequest(method, veeamRoute(path), nil))
			}
			if status != fiber.StatusTooManyRequests {
				t.Errorf("request %d across the %d %s routes returned %d, want 429 — the group is not "+
					"sharing one limiter store, so its %d/min budget is really %d/min",
					g.max+1, len(g.keys), g.name, status, g.max, g.max*len(g.keys))
			}
		})
	}

	// The other direction: a route that should NOT hold a logon budget must not
	// carry one, or an ordinary listing would start spending the control group's.
	for key, e := range declared {
		if limited[key] || e.RateLimiter == nil {
			continue
		}
		t.Errorf("%s carries a rate limiter but spends no Veeam logon; it would eat a budget it "+
			"does not need", key)
	}
}

// TestVeeamCreateRequiresWhatTheHandlerDid pins the required SET of the create
// body against the one combined check the handler made, derived from
// `git show HEAD:internal/api/handlers/veeam_servers.go`.
func TestVeeamCreateRequiresWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, veeamScope)
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"base_url", "name", "password", "username"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
	// verify_tls defaulted to true in the handler (`verifyTLS := true`), and a
	// declaration that lost that would start storing credentials against
	// unverified connections for every caller that omits the field.
	if got := e.Parameters["verify_tls"].Default; got != true {
		t.Errorf("verify_tls declares default %#v, want true — omitting it has always verified", got)
	}
}

// TestVeeamUpdateKeepsItsTristates is the compatibility assertion for the
// partial-update contract.
//
// Every connection field was bound as a POINTER precisely so that "the caller
// never mentioned this" stays distinct from "the caller sent empty or false":
// an empty tls_fingerprint CLEARS the pin, and a Default on verify_tls or
// enabled would re-assert them on every partial save — so the edit dialog,
// which sends only what it has, would start rewriting the rest of the row.
func TestVeeamUpdateKeepsItsTristates(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, veeamScope+"/:id")
	for _, name := range []string{
		"name", "base_url", "username", "password", "tls_fingerprint", "verify_tls", "enabled",
	} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Fatalf("%s is not declared", name)
		}
		if !prop.Optional {
			t.Errorf("%s is required; the handler left an absent field alone", name)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; it must have NONE, so that omitting it stays "+
				"distinguishable from sending empty or false", name, prop.Default)
		}
	}
	// And end to end: the enable/disable toggle's payload reaches the handler
	// with only `enabled` supplied.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVeeamEndpoint(t, fiber.MethodPut, veeamScope+"/:id", cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, veeamRoute(veeamScope+"/:id"), `{"enabled":false}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptBool("enabled"); !supplied || value {
		t.Errorf("enabled read back as (%v, supplied=%v), want (false, true)", value, supplied)
	}
	for _, absent := range []string{"name", "base_url", "username", "password", "tls_fingerprint"} {
		if _, supplied := cap.params.OptString(absent); supplied {
			t.Errorf("%s reads as supplied on a body that omitted it; the update would rewrite it", absent)
		}
	}
	if _, supplied := cap.params.OptBool("verify_tls"); supplied {
		t.Error("verify_tls reads as supplied on a body that omitted it; the update would re-assert it")
	}
}

// probeVeeamEndpoint is a declared veeam endpoint with its handler swapped for
// a capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeVeeamEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	e.RateLimiter = nil
	return e
}

// TestMapBackupObjectGuestKeepsItsBothOrNeitherShape pins the declaration the
// handler's cross-field rule reads.
//
// `(req.ClusterID == nil) != (req.VMID == nil)` became
// `clusterSupplied != vmidSupplied`, and that translation is only correct while
// BOTH parameters stay optional with no default: a default on either would make
// it always-supplied, and the rule would then refuse every clear. apischema
// reads an explicit JSON null as absent (present() in validate.go), which is
// what keeps `{"cluster_id":null,"vmid":null}` — the payload the unmap button
// sends — meaning "clear it".
func TestMapBackupObjectGuestKeepsItsBothOrNeitherShape(t *testing.T) {
	const path = veeamScope + "/:id/backup-objects/:object_id/guest"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	for _, name := range []string{"guest_cluster_id", "vmid"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Fatalf("%s is not declared", name)
		}
		if !prop.Optional {
			t.Errorf("%s is required; clearing the mapping sends neither", name)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; it must have NONE, or every clear would read as a pin", name, prop.Default)
		}
	}
	// vmid <= 0 is not a Proxmox guest, and apischema would otherwise take an
	// explicit 0 as a supplied value — which the both-or-neither rule would
	// then read as a pin to VMID 0.
	if min := e.Parameters["vmid"].Minimum; min == nil || *min != 1 {
		t.Errorf("vmid declares minimum %v, want 1", min)
	}

	// End to end: the unmap payload arrives with neither supplied.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVeeamEndpoint(t, fiber.MethodPut, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, veeamRoute(path), `{"cluster_id":null,"vmid":null}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the payload the unmap button sends", status, env.Message)
	}
	if _, supplied := cap.params.OptString("guest_cluster_id"); supplied {
		t.Error("an explicit null cluster_id reads as supplied; the unmap would be read as a pin")
	}
	if _, supplied := cap.params.OptInt("vmid"); supplied {
		t.Error("an explicit null vmid reads as supplied; the unmap would be read as a pin")
	}
}

// TestVeeamClusterAliasesBindTheBodyKeyCallersSend is the other half of the
// alias decision.
//
// checkPathParams refuses a parameter NAMED cluster_id that resolves to
// anything but the path, so both bodies declare a name of their own. The alias
// is what keeps the existing callers working — the platform dropdown and the
// object-mapping dialog both send {"cluster_id": …} — and without this test
// the rename would be a silent break of two working requests.
func TestVeeamClusterAliasesBindTheBodyKeyCallersSend(t *testing.T) {
	for _, tt := range []struct {
		method  string
		path    string
		name    string
		body    string
		wantKey string
	}{
		{
			fiber.MethodPut, veeamScope + "/:id/platforms/:platform_id", "platform_cluster_id",
			`{"cluster_id":"` + testClusterID + `"}`, "platform_cluster_id",
		},
		{
			fiber.MethodPut, veeamScope + "/:id/backup-objects/:object_id/guest", "guest_cluster_id",
			`{"cluster_id":"` + testClusterID + `","vmid":101}`, "guest_cluster_id",
		},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			prop, ok := e.Parameters[tt.name]
			if !ok {
				t.Fatalf("%s is not declared", tt.name)
			}
			if prop.Alias != "cluster_id" {
				t.Fatalf("%s declares alias %q, want cluster_id — the dialogs send that spelling", tt.name, prop.Alias)
			}
			if _, declared := e.Parameters["cluster_id"]; declared {
				t.Error("cluster_id is declared as a parameter NAME; checkPathParams refuses that because " +
					"the permission middleware reads the same name out of the path")
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeVeeamEndpoint(t, tt.method, tt.path, cap))
			status, env := send(t, app, jsonRequest(tt.method, veeamRoute(tt.path), tt.body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — this is what the dialog sends", status, env.Message)
			}
			if got, supplied := cap.params.OptString(tt.wantKey); !supplied || got != testClusterID {
				t.Errorf("%s read back as (%q, supplied=%v), want the aliased body value to bind",
					tt.wantKey, got, supplied)
			}
		})
	}
}

// TestVeeamIdentifiersAreUUIDs pins the identifier shape on every path
// parameter in this domain. The handlers parsed each one with uuid.Parse and
// answered 400 for anything else; the format states the same rule one layer
// earlier and names the field.
func TestVeeamIdentifiersAreUUIDs(t *testing.T) {
	declared := declaredVeeamEndpoints(t)
	if len(declared) == 0 {
		t.Fatal("no veeam routes are declared; this guard would pass vacuously")
	}
	for key, e := range declared {
		for _, name := range pathParamNames(e.Path) {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Fatalf("%s: path parameter %q is not declared", key, name)
			}
			if prop.Format != "uuid" {
				t.Errorf("%s: path parameter %q declares format %q, want uuid — every identifier in this "+
					"domain is one, and the handlers refused anything else", key, name, prop.Format)
			}
		}
	}

	// And end to end on the one that reaches the widest surface: a malformed
	// server id is refused before the handler runs.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVeeamEndpoint(t, fiber.MethodGet, veeamScope+"/:id", cap))
	status, _ := send(t, app, httptest.NewRequest(http.MethodGet, pathPrefix+"veeam-servers/not-a-uuid", nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed server id", status)
	}
	if cap.called {
		t.Error("the handler ran for a malformed server id")
	}
}

// TestVeeamRepositoryRangeKeepsItsFallback is the assertion behind the one
// parameter in this domain that deliberately carries NO enum.
//
// parseVeeamRange answers an unrecognised value with the 7-day window rather
// than an error, and closing the vocabulary would turn a request that has
// always worked into a 400.
func TestVeeamRepositoryRangeKeepsItsFallback(t *testing.T) {
	const path = veeamScope + "/:id/repositories/:repository_id/metrics"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	prop := e.Parameters["range"]
	if len(prop.Enum) != 0 {
		t.Errorf("range declares enum %v; the handler falls back to 7d rather than refusing, so an enum "+
			"would break a request that has always worked", prop.Enum)
	}
	if prop.Default != "7d" {
		t.Errorf("range declares default %#v, want \"7d\" — the window an omitted parameter produced", prop.Default)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVeeamEndpoint(t, fiber.MethodGet, path, cap))
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, veeamRoute(path)+"?range=nonsense", nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — an unrecognised range falls back rather than failing", status, env.Message)
	}
}

// TestEveryVeeamEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryVeeamEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredVeeamEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Backup" {
			t.Errorf("%s is in group %q, want Backup", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
