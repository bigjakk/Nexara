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
// rather than a fixture shaped like them, so a change to
// registry_migrations.go that quietly loosened a parameter would show up
// here.

// migrationRouteCount is how many endpoints registerMigrationEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
const migrationRouteCount = 7

const testMigrationJobID = "7b7a6f5e-4d3c-4b2a-9190-000000000007"

// migrationRoute renders one migration route's path with the test ids
// substituted.
func migrationRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":id", testMigrationJobID,
	).Replace(path)
}

// migrationRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go: every migration
// route that is deliberately not a plain cluster-scoped Check.
//
// Six of the seven are here, which is the whole point of writing the
// reasons down: a migration straddles TWO clusters and the global routes
// name NEITHER of them in the path, so there is nothing for
// clusterIDFromParam to resolve and a gate could not run at all.
var migrationRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/migrations": "Advisory: accessibleClusters narrows the SQL scope and the handler " +
		"re-checks each row on either end; no single cluster to gate on",
	"POST /api/v1/migrations": "Deferred: the source and target clusters are in the body, which no " +
		"middleware can read",
	"GET /api/v1/migrations/:id":          "Deferred: the clusters come off the loaded job row",
	"POST /api/v1/migrations/:id/check":   "Deferred: the clusters come off the loaded job row",
	"POST /api/v1/migrations/:id/execute": "Deferred: the clusters come off the loaded job row",
	"POST /api/v1/migrations/:id/cancel":  "Deferred: the clusters come off the loaded job row",
}

// migrationLegacyPermissions is what each handler checked with hand-placed
// calls BEFORE Phase 6c, transcribed from internal/api/handlers/migrations.go
// at commit 3099aa8: 11 requireClusterPerm calls and one accessibleClusters,
// across 7 handlers.
//
// shape says what the declaration must be, and calls is how many permission
// calls the handler made. Only the ONE route whose subject is a cluster in
// its own path could hoist its call into middleware; the other six keep
// theirs, which is what Deferred and Advisory mean.
var migrationLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
}{
	"GET /api/v1/migrations":                      {"view:migration", "Advisory", 1},
	"POST /api/v1/migrations":                     {"manage:migration", "Deferred", 2},
	"GET /api/v1/migrations/:id":                  {"view:migration", "Deferred", 2},
	"POST /api/v1/migrations/:id/check":           {"manage:migration", "Deferred", 2},
	"POST /api/v1/migrations/:id/execute":         {"manage:migration", "Deferred", 2},
	"POST /api/v1/migrations/:id/cancel":          {"manage:migration", "Deferred", 2},
	"GET /api/v1/clusters/:cluster_id/migrations": {"view:migration", "Check", 1},
}

// declaredMigrationEndpoints returns every declaration in this domain,
// keyed "METHOD path".
func declaredMigrationEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, migrationScope) || e.Path == clusterScope+"/migrations" {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestMigrationRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// Unlike the five domains of Phase 6b, almost nothing here moved into
// middleware: exactly ONE of the twelve hand-placed calls was hoistable,
// and the tally is what says so out loud rather than leaving a reader to
// infer it from six Deferred declarations.
func TestMigrationRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredMigrationEndpoints(t)
	if len(declared) != migrationRouteCount {
		t.Fatalf("the registry declares %d migration routes, want %d", len(declared), migrationRouteCount)
	}
	if len(migrationLegacyPermissions) != migrationRouteCount {
		t.Fatalf("migrationLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(migrationLegacyPermissions), migrationRouteCount)
	}

	var hoisted, kept int
	for key, want := range migrationLegacyPermissions {
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
				t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path", key, e.Permissions.Check.Scope)
			}
		case "Deferred":
			kept += want.calls
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: the clusters it authorizes are not in the path",
					key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
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
	if hoisted != 1 || kept != 11 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 1 / 11",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := migrationLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in migrationLegacyPermissions — a new migration route "+
				"must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestMigrationJobIDIsAPathParameterOnly is the reason :id is safe to keep
// as this domain's spelling.
//
// clusterIDFromParam reads TWO names — cluster_id, falling back to id — so
// "id" is a name the permission gate also reads. It resolves to the PATH
// here, which checkPathParams requires, and namesACluster refuses a
// cluster-scoped Check on a path whose first placeholder is not
// :cluster_id, so the fallback cannot turn a job id into a cluster the
// gate would authorize. Both halves are asserted, because either one alone
// would leave the other free to change.
func TestMigrationJobIDIsAPathParameterOnly(t *testing.T) {
	for key, e := range declaredMigrationEndpoints(t) {
		if !strings.HasPrefix(e.Path, migrationScope+"/:id") {
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
		if e.Permissions.Deferred == "" {
			t.Errorf("%s declares %q; a job path names no cluster, so nothing static can gate it",
				key, e.Permissions.Describe())
		}
		// The fail-closed half: a cluster-scoped Check on this path is
		// refused at registration, so nobody can "simplify" the Deferred
		// away into a gate that would authorize the wrong object.
		if namesACluster(pathParamNames(e.Path), e.Path) {
			t.Errorf("%s would accept a cluster-scoped Check, but :id is a job id, not a cluster id", key)
		}
	}
}

// probeMigrationEndpoint is a declared migration endpoint with its handler
// swapped for a capture.
func probeMigrationEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestMigrationCreateRequiresOnlyWhatTheHandlerDid pins the required SET of
// the create body against what the handler refused before the migration,
// derived from `git show HEAD:internal/api/handlers/migrations.go`.
//
// It is a SET rather than a list of positive cases because both dialogs
// send all sixteen keys on every create: a parameter that quietly became
// required would be invisible to every fixture, which is exactly how the HA
// migration nearly shipped a required `state`.
func TestMigrationCreateRequiresOnlyWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, migrationScope)
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)

	// The six the handler refused an empty or zero value for: uuid.Parse
	// failed on an empty cluster id, source_node was "" is required, vmid
	// had to be positive, and vm_type / migration_type had to be in their
	// membership maps.
	want := []string{
		"migration_type", "source_cluster_id", "source_node", "target_cluster_id", "vm_type", "vmid",
	}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
}

// TestMigrationCreateAcceptsWhatTheDialogsSend is the compatibility
// assertion for this domain.
//
// MigrateJobDialog and BulkMigrateDialog both send target_node,
// target_storage and disk_format UNCONDITIONALLY, leaving each EMPTY when
// the chosen mode does not use it. apischema treats "" as a value the
// caller supplied and every registered format rejects it, so borrowing the
// node-name or storage-id format for any of the three would 400 a request
// that has always worked.
func TestMigrationCreateAcceptsWhatTheDialogsSend(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeMigrationEndpoint(t, fiber.MethodPost, migrationScope, cap))

	body := `{"source_cluster_id":"` + testClusterID + `","target_cluster_id":"` + testClusterID + `",` +
		`"source_node":"pve-01","target_node":"","vmid":100,"vm_type":"qemu",` +
		`"migration_type":"intra-cluster","migration_mode":"live","storage_map":{},"network_map":{},` +
		`"online":true,"bwlimit_kib":0,"delete_source":false,"target_vmid":0,` +
		`"target_storage":"","disk_format":""}`
	status, env := send(t, app, jsonRequest(http.MethodPost, migrationRoute(migrationScope), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the exact payload both dialogs send", status, env.Message)
	}
	for _, key := range []string{"target_node", "target_storage", "disk_format"} {
		if got := cap.params.String(key); got != "" {
			t.Errorf("%s = %q, want the empty sentinel to survive", key, got)
		}
	}
}

// TestMigrationCreateRejectsWhatTheHandlerUsedTo pins the five "x is
// required"/"x must be one of" checks the handler no longer makes, plus the
// one spelling the Enum deliberately drops.
func TestMigrationCreateRejectsWhatTheHandlerUsedTo(t *testing.T) {
	base := map[string]string{
		"source_cluster_id": `"` + testClusterID + `"`,
		"target_cluster_id": `"` + testClusterID + `"`,
		"source_node":       `"pve-01"`,
		"vmid":              `100`,
		"vm_type":           `"qemu"`,
		"migration_type":    `"intra-cluster"`,
		"target_node":       `"pve-02"`,
	}
	bodyWith := func(overrides map[string]string) string {
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range overrides {
			merged[k] = v
		}
		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			if merged[k] == "" {
				continue // an omitted key
			}
			parts = append(parts, `"`+k+`":`+merged[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}

	for _, tt := range []struct {
		name      string
		overrides map[string]string
		field     string
	}{
		{"no source node", map[string]string{"source_node": ""}, "source_node:"},
		{"empty source node", map[string]string{"source_node": `""`}, "source_node:"},
		{"a source node that is not a node", map[string]string{"source_node": `"a/b"`}, "source_node:"},
		{"vmid zero", map[string]string{"vmid": `0`}, "vmid:"},
		{"vmid negative", map[string]string{"vmid": `-1`}, "vmid:"},
		{"no vm type", map[string]string{"vm_type": ""}, "vm_type:"},
		{"a vm type that is neither", map[string]string{"vm_type": `"kvm"`}, "vm_type:"},
		{"no migration type", map[string]string{"migration_type": ""}, "migration_type:"},
		{"a migration type that is neither", map[string]string{"migration_type": `"warm"`}, "migration_type:"},
		{"no source cluster", map[string]string{"source_cluster_id": ""}, "source_cluster_id:"},
		{"a source cluster that is not a uuid", map[string]string{"source_cluster_id": `"nope"`}, "source_cluster_id:"},
		{"no target cluster", map[string]string{"target_cluster_id": ""}, "target_cluster_id:"},
		// The one spelling that is newly refused: an explicit "" used to
		// mean live, and an Enum rejects it because apischema treats the
		// empty string as a value the caller supplied. Omitting the key is
		// how a caller asks for the default, and that still works — see
		// TestMigrationModeDefaultsToLive.
		{"an empty migration mode", map[string]string{"migration_mode": `""`}, "migration_mode:"},
		{"a migration mode that is none of the three", map[string]string{"migration_mode": `"cold"`}, "migration_mode:"},
		{"a misspelled key", map[string]string{"onlien": `true`}, "onlien:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeMigrationEndpoint(t, fiber.MethodPost, migrationScope, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, migrationRoute(migrationScope), bodyWith(tt.overrides)))
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
}

// TestMigrationModeDefaultsToLive pins the substitution the handler used to
// make, now stated as the schema's Default so the docs answer "what happens
// if I leave this out".
func TestMigrationModeDefaultsToLive(t *testing.T) {
	if got := declaredEndpoint(t, fiber.MethodPost, migrationScope).Parameters["migration_mode"].Default; got != "live" {
		t.Errorf("migration_mode default = %#v, want \"live\"", got)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeMigrationEndpoint(t, fiber.MethodPost, migrationScope, cap))
	body := `{"source_cluster_id":"` + testClusterID + `","target_cluster_id":"` + testClusterID + `",` +
		`"source_node":"pve-01","vmid":100,"vm_type":"qemu","migration_type":"intra-cluster"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, migrationRoute(migrationScope), body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("migration_mode"); got != "live" {
		t.Errorf("migration_mode = %q, want live", got)
	}
	if cap.params.Has("migration_mode") {
		t.Error("migration_mode reads as supplied; a default is not something the caller sent")
	}
}

// TestMigrationListPagingIsBounded covers both listings' ?limit= and
// ?offset=, including the two spellings they deliberately drop: the handler
// CLAMPED an out-of-range limit to 50 and a negative offset to 0, so
// ?limit=5000 silently answered with a page the caller never asked for.
func TestMigrationListPagingIsBounded(t *testing.T) {
	for _, path := range []string{migrationScope, clusterScope + "/migrations"} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, path)
			if got := e.Parameters["limit"].Default; got != 50 {
				t.Errorf("limit default = %#v, want 50 — the value the handler substituted", got)
			}
			if got := e.Parameters["offset"].Default; got != 0 {
				t.Errorf("offset default = %#v, want 0", got)
			}

			target := migrationRoute(path)
			for _, tt := range []struct {
				query string
				want  int
				limit int64
			}{
				{query: "", want: fiber.StatusNoContent, limit: 50},
				{query: "?limit=500&offset=0", want: fiber.StatusNoContent, limit: 500},
				{query: "?limit=0", want: fiber.StatusBadRequest},
				{query: "?limit=5000", want: fiber.StatusBadRequest},
				{query: "?offset=-1", want: fiber.StatusBadRequest},
				{query: "?limits=10", want: fiber.StatusBadRequest},
			} {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeMigrationEndpoint(t, fiber.MethodGet, path, cap))
				status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
				if status != tt.want {
					t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
					continue
				}
				if tt.want == fiber.StatusNoContent && cap.params.Int("limit") != tt.limit {
					t.Errorf("%q: limit = %d, want %d", tt.query, cap.params.Int("limit"), tt.limit)
				}
			}
		})
	}
}

// TestMigrationByClusterRouteIsGatedByItsDeclaration proves the one
// hoistable permission in this domain is the permission the route
// enforces, end to end.
func TestMigrationByClusterRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, clusterScope+"/migrations")
	if e.Permissions.Describe() != "view:migration" {
		t.Fatalf("the per-cluster listing declares %q, want view:migration", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := migrationRoute(clusterScope + "/migrations")

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

	t.Run("a caller holding view:migration gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:migration": true}), gated)
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
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:migration": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryMigrationEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryMigrationEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredMigrationEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Migrations" {
			t.Errorf("%s is in group %q, want Migrations", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
