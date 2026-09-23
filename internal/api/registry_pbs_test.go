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
// rather than a fixture shaped like them, so a change to registry_pbs.go
// that quietly loosened a parameter would show up here. Several of them
// took over from internal/api/handlers/pbs_servers_test.go, which used to
// mount the handlers on a bare app and assert parameter rules that are now
// the schema's.

// pbsRouteCount is how many endpoints registerPBSEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const pbsRouteCount = 6

const testPBSServerID = "5c4b3a29-1817-4615-9413-000000000006"

func pbsRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":id", testPBSServerID,
	).Replace(path)
}

// pbsRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// Five of the six are here, which is the point of writing the reasons
// down: a PBS server may belong to a cluster or to no cluster at all, and
// which of the two decides whether the grant is cluster-scoped or
// instance-wide. That is a DB lookup on four of them and a body read on
// the fifth — neither is something middleware can do.
var pbsRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/pbs-servers": "Advisory: accessibleClusters builds the filter and the handler applies it " +
		"per row — PermitsCluster for a cluster-bound server, HasGlobal for a standalone one",
	"POST /api/v1/pbs-servers": "Deferred: the scope depends on the cluster_id in the body, or on its " +
		"absence",
	"GET /api/v1/pbs-servers/:id":    "Deferred: the scope depends on the loaded row's cluster_id",
	"PUT /api/v1/pbs-servers/:id":    "Deferred: the scope depends on the loaded row's cluster_id",
	"DELETE /api/v1/pbs-servers/:id": "Deferred: the scope depends on the loaded row's cluster_id",
}

// pbsLegacyPermissions is what each handler checked with hand-placed calls
// BEFORE Phase 6c, transcribed from internal/api/handlers/pbs_servers.go
// at commit 3099aa8.
//
// calls counts the permission calls each handler made, and it is
// deliberately 2 for Create and Update: each has a per-branch pair
// (requireClusterPerm OR requirePerm), and Update additionally authorizes
// the cluster it is MOVING the server to.
var pbsLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
}{
	"GET /api/v1/pbs-servers":                      {"view:pbs", "Advisory", 1},
	"POST /api/v1/pbs-servers":                     {"manage:pbs", "Deferred", 2},
	"GET /api/v1/pbs-servers/:id":                  {"view:pbs", "Deferred", 2},
	"PUT /api/v1/pbs-servers/:id":                  {"manage:pbs", "Deferred", 3},
	"DELETE /api/v1/pbs-servers/:id":               {"delete:pbs", "Deferred", 2},
	"GET /api/v1/clusters/:cluster_id/pbs-servers": {"view:pbs", "Check", 1},
}

// declaredPBSEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It filters on the exact paths rather than on the /pbs-servers prefix,
// because the 19 backup routes nested under /pbs-servers/:pbs_id are NOT
// in this domain — they are declared in registerBackupEndpoints, as Deferred
// routes that gate through BackupHandler.requirePBSPerm.
func declaredPBSEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if e.Path == pbsScope || e.Path == pbsScope+"/:id" || e.Path == clusterScope+"/pbs-servers" {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestPBSRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// Almost nothing hoists here: exactly ONE of the eleven hand-placed calls
// moved into middleware, and the tally says so out loud rather than
// leaving a reader to infer it from five reason strings.
func TestPBSRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredPBSEndpoints(t)
	if len(declared) != pbsRouteCount {
		t.Fatalf("the registry declares %d PBS routes, want %d", len(declared), pbsRouteCount)
	}
	if len(pbsLegacyPermissions) != pbsRouteCount {
		t.Fatalf("pbsLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(pbsLegacyPermissions), pbsRouteCount)
	}

	var hoisted, kept int
	for key, want := range pbsLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		switch want.shape {
		case "Check":
			hoisted += want.calls
			if e.Permissions.Check == nil {
				t.Errorf("%s declares %q rather than a Check; its cluster is in its own path",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Describe(); got != want.permission {
				t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
			}
			if e.Permissions.Check.Scope != ScopeCluster {
				t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path",
					key, e.Permissions.Check.Scope)
			}
		case "Deferred":
			kept += want.calls
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: the scope depends on whether the server belongs "+
					"to a cluster", key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
			}
			// And the reason has to name what the handler actually looks at,
			// not merely say that it looks at something.
			if !strings.Contains(e.Permissions.Deferred, "standalone") {
				t.Errorf("%s: the Deferred reason does not name the standalone branch, which is the half "+
					"an operator would otherwise miss: %q", key, e.Permissions.Deferred)
			}
		case "Advisory":
			kept += want.calls
			if e.Permissions.Advisory == nil {
				t.Errorf("%s declares %q, want Advisory: the listing filters rather than gates",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Advisory.String(); got != want.permission {
				t.Errorf("%s filters on %q but the handler used %q", key, got, want.permission)
			}
			if !strings.Contains(e.Permissions.Advisory.Reason, "accessibleClusters") {
				t.Errorf("%s: the Advisory reason does not name accessibleClusters, which is what does "+
					"the filtering: %q", key, e.Permissions.Advisory.Reason)
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 1 || kept != 10 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 1 / 10",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := pbsLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in pbsLegacyPermissions — a new PBS route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestPBSServerIDIsAPathParameterOnly is the fail-closed half of spelling
// this domain's identifier :id.
//
// clusterIDFromParam reads TWO names — cluster_id, falling back to id — so
// "id" is a name the permission gate also reads. It resolves to the PATH
// here, which checkPathParams requires, and namesACluster refuses a
// cluster-scoped Check on this path, so nobody can "simplify" the Deferred
// away into a gate that would authorize a PBS server id as if it were a
// cluster.
func TestPBSServerIDIsAPathParameterOnly(t *testing.T) {
	for key, e := range declaredPBSEndpoints(t) {
		if !strings.HasSuffix(e.Path, "/:id") {
			continue
		}
		prop, ok := e.Parameters["id"]
		if !ok {
			t.Errorf("%s has :id with no entry in Parameters", key)
			continue
		}
		if prop.Source != "" && prop.Source != "path" {
			t.Errorf("%s declares id with source %q; the gate reads it from the path", key, prop.Source)
		}
		if prop.Format != "uuid" {
			t.Errorf("%s declares id with format %q, want uuid", key, prop.Format)
		}
		if namesACluster(pathParamNames(e.Path), e.Path) {
			t.Errorf("%s would accept a cluster-scoped Check, but :id is a PBS server id", key)
		}
	}
}

// TestPBSClusterIDTakesItsWireNameUnderAnAlias is the assertion behind the
// one declaration in this domain that could not use the name its callers
// send.
//
// checkPathParams refuses a body parameter named "cluster_id", because
// clusterIDFromParam reads that name to decide which cluster the gate
// authorizes. On these two paths no gate runs at all, but the guard is
// about the name rather than about today's path, and routing around it
// would reopen the hole on the next path edit. The parameter is therefore
// declared as attached_cluster_id with "cluster_id" as its alias, and BOTH
// spellings have to keep working: both dialogs send "cluster_id".
func TestPBSClusterIDTakesItsWireNameUnderAnAlias(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, pbsScope},
		{fiber.MethodPut, pbsScope + "/:id"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if _, wrong := e.Parameters["cluster_id"]; wrong {
				t.Fatal("the body declares a parameter literally named cluster_id; checkPathParams refuses " +
					"that because the permission gate reads the same name, and Register would have panicked")
			}
			if got := e.Parameters["attached_cluster_id"].Alias; got != "cluster_id" {
				t.Errorf("attached_cluster_id declares alias %q, want cluster_id — both dialogs send it "+
					"and would otherwise get \"unknown parameter\"", got)
			}
		})
	}

	// Both spellings reach the handler as attached_cluster_id, and sending
	// both at once is a collision Validate reports rather than silently
	// picking a winner.
	for _, spelling := range []string{"cluster_id", "attached_cluster_id"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
		body := `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t",` +
			`"token_secret":"s","` + spelling + `":"` + testClusterID + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("attached_cluster_id"); got != testClusterID {
			t.Errorf("spelling %q reached the handler as %q", spelling, got)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
	body := `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t",` +
		`"token_secret":"s","cluster_id":"` + testClusterID + `","attached_cluster_id":"` + testClusterID + `"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope, body)); status != fiber.StatusBadRequest {
		t.Errorf("both spellings at once: status = %d (%q), want 400", status, env.Message)
	}
}

// probePBSEndpoint is a declared PBS endpoint with its handler swapped for
// a capture.
func probePBSEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestPBSCreateRequiresOnlyWhatTheHandlerDid pins the required SET of the
// create body against what the handler refused before the migration,
// derived from `git show HEAD:internal/api/handlers/pbs_servers.go` — one
// combined check for name, api_url, token_id and token_secret, and nothing
// else.
func TestPBSCreateRequiresOnlyWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, pbsScope)
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"api_url", "name", "token_id", "token_secret"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
}

// TestPBSCreateRejectsWhatTheHandlerUsedTo took over from
// TestPBSCreate_MissingFields and TestPBSCreate_InvalidClusterID in
// internal/api/handlers/pbs_servers_test.go: the same cases, driven
// through the real declaration, and now naming the field that is wrong.
func TestPBSCreateRejectsWhatTheHandlerUsedTo(t *testing.T) {
	const valid = `"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t","token_secret":"s"`
	for _, tt := range []struct{ name, body, field string }{
		{"empty body", `{}`, "api_url:"},
		{"missing name", `{"api_url":"https://pbs.example.com:8007","token_id":"u@pam!t","token_secret":"s"}`, "name:"},
		{"empty name", `{` + valid + `,"name":""}`, "name:"},
		{"missing api_url", `{"name":"backup01","token_id":"u@pam!t","token_secret":"s"}`, "api_url:"},
		{"missing token_id", `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_secret":"s"}`, "token_id:"},
		{"missing token_secret", `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t"}`, "token_secret:"},
		{"a name over 255 characters", `{` + valid + `,"name":"` + strings.Repeat("x", 256) + `"}`, "name:"},
		{"a cluster_id that is not a uuid", `{` + valid + `,"cluster_id":"not-a-uuid"}`, "attached_cluster_id:"},
		// A key the bound struct would have dropped in silence. Spelled
		// with a transposition rather than a dropped letter so the linter's
		// spell check does not flag the fixture itself.
		{"a misspelled key", `{` + valid + `,"tls_fingreprint":"AA"}`, "tls_fingreprint:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope, tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}

	// A malformed body is still a 400 rather than a 500 — the case
	// TestPBSCreate_InvalidJSON used to cover.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
	if status, _ := send(t, app, jsonRequest(http.MethodPost, pbsScope, "not json")); status != fiber.StatusBadRequest {
		t.Errorf("malformed JSON: status = %d, want 400", status)
	}
}

// TestPBSPathIDsAreValidated took over from TestPBSGet_InvalidUUID and
// TestPBSListByCluster_InvalidUUID: an id that is not a UUID is refused by
// the schema, before the handler and before any query.
func TestPBSPathIDsAreValidated(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		target string
		field  string
	}{
		{fiber.MethodGet, pbsScope + "/:id", pbsScope + "/not-a-uuid", "id:"},
		{fiber.MethodDelete, pbsScope + "/:id", pbsScope + "/not-a-uuid", "id:"},
		{fiber.MethodGet, clusterScope + "/pbs-servers", "/api/v1/clusters/bad-uuid/pbs-servers", "cluster_id:"},
	} {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, tt.method, tt.path, cap))
			status, env := send(t, app, httptest.NewRequest(tt.method, tt.target, nil))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for an id the schema rejected")
			}
		})
	}
}

// TestPBSCreateAcceptsWhatTheDialogSends is the compatibility assertion
// for this domain.
//
// AddPBSServerDialog sends tls_fingerprint:"" when the fetch step was
// skipped, and cluster_id:null for a standalone server — apischema reads a
// JSON null as absent, and every registered format rejects "", which is
// why tls_fingerprint carries none.
func TestPBSCreateAcceptsWhatTheDialogSends(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
	body := `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t",` +
		`"token_secret":"s","tls_fingerprint":"","cluster_id":null}`
	status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope, body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the payload the add dialog sends", status, env.Message)
	}
	if got := cap.params.String("tls_fingerprint"); got != "" {
		t.Errorf("tls_fingerprint = %q, want the empty sentinel to survive", got)
	}
	if cap.params.Has("attached_cluster_id") {
		t.Error("a JSON null read as supplied; it means \"standalone\", which is the absent case")
	}

	// And the empty string means standalone too, which is the branch the
	// handler takes on `*req.ClusterID != ""`.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
	body = `{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t",` +
		`"token_secret":"s","cluster_id":""}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope, body)); status != fiber.StatusNoContent {
		t.Fatalf("empty cluster_id: status = %d (%q), want 204 — it has always meant standalone", status, env.Message)
	}
	if got := cap.params.String("attached_cluster_id"); got != "" {
		t.Errorf("attached_cluster_id = %q, want the empty sentinel to survive", got)
	}
}

// TestPBSUpdateRefusesAnEmptyClusterID records a PRE-EXISTING bug rather
// than a rule worth having.
//
// UpdatePBSServer has always parsed cluster_id with uuid.Parse and
// answered 400 for anything that failed, so there is no way to DETACH a
// PBS server from its cluster through this endpoint — and
// EditPBSServerDialog's "None" option sends exactly the empty string this
// refuses. The declaration states that rule rather than changing it: this
// migration is meant to be a refactor, and making detach work is a
// behaviour change scoped separately.
//
// The test is here so the bug is recorded at the boundary where it lives,
// and so that fixing it later is a deliberate edit to BOTH the declaration
// and this expectation.
func TestPBSUpdateRefusesAnEmptyClusterID(t *testing.T) {
	const path = pbsScope + "/:id"
	if got := declaredEndpoint(t, fiber.MethodPut, path).Parameters["attached_cluster_id"].Format; got != "uuid" {
		t.Errorf("attached_cluster_id declares format %q on the update, want uuid — the handler has always "+
			"refused anything uuid.Parse rejects", got)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPut, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, pbsRoute(path), `{"cluster_id":""}`))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d (%q), want 400 — matching the pre-migration refusal", status, env.Message)
	}
	if !strings.HasPrefix(env.Message, "attached_cluster_id:") {
		t.Errorf("message = %q, want it to name the field, which the handler's generic "+
			"\"Invalid cluster_id format\" did not", env.Message)
	}
}

// TestPBSUpdateKeepsEveryFieldOptional pins the partial-update contract:
// the handler seeded every parameter from the stored row and overwrote
// only what the caller sent, so a required field here would break a save
// that changes one thing.
func TestPBSUpdateKeepsEveryFieldOptional(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, pbsScope+"/:id")
	var required []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	if want := []string{"id"}; !slices.Equal(required, want) {
		t.Errorf("required parameters = %v, want %v — everything but the path id is a partial update",
			required, want)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPut, pbsScope+"/:id", cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, pbsRoute(pbsScope+"/:id"), `{"name":"renamed"}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptString("name"); !supplied || value != "renamed" {
		t.Errorf("name read back as (%q, supplied=%v)", value, supplied)
	}
	if _, supplied := cap.params.OptString("api_url"); supplied {
		t.Error("api_url reads as supplied on a body that omitted it; none of these may carry a default")
	}
}

// TestPBSSecretsCannotTravelInTheQueryString is what declaring the secret
// buys beyond validation.
//
// checkMisplaced refuses a declared body parameter that arrives as a query
// parameter, so a caller cannot move a credential into a URL that proxies
// and access logs record.
func TestPBSSecretsCannotTravelInTheQueryString(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probePBSEndpoint(t, fiber.MethodPost, pbsScope, cap))
	status, env := send(t, app, jsonRequest(http.MethodPost, pbsScope+"?token_secret=leaked",
		`{"name":"backup01","api_url":"https://pbs.example.com:8007","token_id":"u@pam!t","token_secret":"s"}`))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d (%q), want 400", status, env.Message)
	}
	if !strings.Contains(env.Message, "request body") {
		t.Errorf("message = %q, want it to say the parameter belongs in the body", env.Message)
	}
	if cap.called {
		t.Error("the handler ran for a request that carried a secret in the URL")
	}
}

// TestPBSByClusterRouteIsGatedByItsDeclaration proves the one hoistable
// permission in this domain is the permission the route enforces, end to
// end. It took over the per-cluster half of
// handlers.TestPBS_NonAdminDenied, which a bare handler mount can no
// longer exercise at all now that the check is middleware.
func TestPBSByClusterRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, clusterScope+"/pbs-servers")
	if e.Permissions.Describe() != "view:pbs" {
		t.Fatalf("the per-cluster listing declares %q, want view:pbs", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := pbsRoute(clusterScope + "/pbs-servers")

	t.Run("a caller holding an unrelated grant is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:backup": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodGet, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding view:pbs gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:pbs": true}), gated)
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
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:pbs": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryPBSEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryPBSEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredPBSEndpoints(t) {
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
