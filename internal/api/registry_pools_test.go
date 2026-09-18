package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// poolRouteCount is how many endpoints registerPoolEndpoints declares. The
// pool LISTING is VMHandler's and is counted under vmRouteCount.
const poolRouteCount = 4

// poolLegacyPermissions is the permission each handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/pools.go` at commit eaeafa7 — four
// handlers, four calls, every one cluster-scoped on the "pool" resource.
var poolLegacyPermissions = map[string]string{
	"POST /api/v1/clusters/:cluster_id/pools":            "manage:pool",
	"GET /api/v1/clusters/:cluster_id/pools/:pool_id":    "view:pool",
	"PUT /api/v1/clusters/:cluster_id/pools/:pool_id":    "manage:pool",
	"DELETE /api/v1/clusters/:cluster_id/pools/:pool_id": "manage:pool",
}

func declaredPoolEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, want := poolLegacyPermissions[e.Method+" "+e.Path]; want {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestPoolRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// deleting four requireClusterPerm calls a refactor rather than a change.
func TestPoolRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredPoolEndpoints(t)
	if len(declared) != poolRouteCount {
		t.Fatalf("the registry declares %d pool routes, want %d", len(declared), poolRouteCount)
	}
	if len(poolLegacyPermissions) != poolRouteCount {
		t.Fatalf("poolLegacyPermissions has %d entries, want %d", len(poolLegacyPermissions), poolRouteCount)
	}

	var view, manage int
	for key, want := range poolLegacyPermissions {
		switch want {
		case "view:pool":
			view++
		case "manage:pool":
			manage++
		}
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
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path", key, e.Permissions.Check.Scope)
		}
	}
	if view != 1 || manage != 3 {
		t.Errorf("the tally splits %d view:pool / %d manage:pool, want 1 / 3", view, manage)
	}
}

// TestPoolIDIsLooserThanConfigID is the compatibility assertion this domain
// needed a decision about.
//
// A pool created outside Nexara can be named in ways PVE's own pve-configid
// format refuses — a leading digit, a dot — and the format is what the PATH
// parameter would have to satisfy on a get, an edit or a delete. Tightening it
// would therefore make an existing pool unmanageable through this API, which is
// the same trap snapshotNameParam documents. This pins that the looser pattern
// stays looser.
func TestPoolIDIsLooserThanConfigID(t *testing.T) {
	const path = clusterScope + "/pools/:pool_id"
	prop := declaredEndpoint(t, fiber.MethodDelete, path).Parameters["pool_id"]
	if prop.Format != "" {
		t.Fatalf("pool_id declares format %q; pve-configid refuses a leading digit and a dot, which a "+
			"pool created outside Nexara may carry — and an unaddressable pool cannot be deleted", prop.Format)
	}

	for _, tt := range []struct {
		id   string
		want int
	}{
		{"Pool-01", fiber.StatusNoContent},
		{"01.pool", fiber.StatusNoContent},
		{"p", fiber.StatusNoContent}, // one character: pve-configid demands two
		{"-leading-dash", fiber.StatusBadRequest},
		{"pool@name", fiber.StatusBadRequest},
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodDelete, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		target := strings.NewReplacer(":cluster_id", testClusterID, ":pool_id", tt.id).Replace(path)
		status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != tt.want {
			t.Errorf("pool id %q: status = %d (%q), want %d", tt.id, status, env.Message, tt.want)
		}
	}
}

// TestPoolUpdateCommentStaysTristate pins the absent-versus-empty distinction
// the handler forwards as a *string.
//
// Omitting `comment` must leave the stored one alone; sending it empty must
// clear it. A Default on the parameter would collapse the two and overwrite
// every pool comment whose editor never touched the field.
func TestPoolUpdateCommentStaysTristate(t *testing.T) {
	const path = clusterScope + "/pools/:pool_id"
	prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["comment"]
	if !prop.Optional {
		t.Error("comment is required; the edit dialog saves without it")
	}
	if prop.Default != nil {
		t.Errorf("comment declares default %#v; a default makes p.OptString report every request as "+
			"supplied, which would clear the stored comment on a save that never mentioned it", prop.Default)
	}

	target := strings.NewReplacer(":cluster_id", testClusterID, ":pool_id", "pool01").Replace(path)
	for _, tt := range []struct {
		body     string
		supplied bool
		value    string
	}{
		{body: `{}`, supplied: false},
		{body: `{"comment":""}`, supplied: true, value: ""},
		{body: `{"comment":"nightly"}`, supplied: true, value: "nightly"},
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPut, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body))
		if status != fiber.StatusNoContent {
			t.Fatalf("body %s: status = %d (%q), want 204", tt.body, status, env.Message)
		}
		value, supplied := cap.params.OptString("comment")
		if supplied != tt.supplied || (tt.supplied && value != tt.value) {
			t.Errorf("body %s: comment read back as (%q, supplied=%v), want (%q, %v)",
				tt.body, value, supplied, tt.value, tt.supplied)
		}
	}
}

// TestEveryPoolEndpointIsDocumented holds the declarations to the standard that
// makes this whole effort worth doing.
func TestEveryPoolEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredPoolEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Virtual Machines" {
			t.Errorf("%s is in group %q, want Virtual Machines — the section the pool listing already sits in",
				key, e.Group)
		}
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
