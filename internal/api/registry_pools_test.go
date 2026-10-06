package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
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
	return declaredEndpointsWhere(t, keyedIn(poolLegacyPermissions))
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

// TestPoolIDIsLooserThanConfigID is the compatibility assertion this domain needed a decision about. A
// pool created outside Nexara can be named in ways pve-configid refuses (a leading digit, a dot), and
// tightening the PATH parameter would make an existing pool unmanageable through this API (the trap
// snapshotNameParam documents). The accepted set is pve-poolid's segment charset, [A-Za-z0-9._-]+, read
// off verify_poolname in pve-access-control, not guessed: a leading DASH was once pinned here as a 400 and
// is valid, the invented strictness this test exists to prevent. Refused: a character outside the charset;
// NESTING ("infra/prod" is valid upstream but a slash cannot survive a path segment, see poolIDParam); and
// a name that is EXACTLY "." or "..", which TestPoolTraversalIsRefusedAtTheRoute asserts the handler never
// ran for. The dotted rows are the other half of that carve-out: a dot INSIDE a name, or two leading
// dots, is an ordinary pool name, and the tempting one-branch rule accepts ".hidden" and refuses "..archive".
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
		{"p", fiber.StatusNoContent},             // one character: pve-configid demands two
		{"db", fiber.StatusNoContent},            // two, the length the dot carve-out splits on
		{"-leading-dash", fiber.StatusNoContent}, // valid to verify_poolname
		{".hidden", fiber.StatusNoContent},       // ditto
		{".a", fiber.StatusNoContent},            // a leading dot at the shortest length that is not "."
		{"..archive", fiber.StatusNoContent},     // two leading dots; not "..", so nothing normalises it
		{"...", fiber.StatusNoContent},           // ditto, and the shape the naive one-branch regex breaks
		{"pool@name", fiber.StatusBadRequest},    // "@" is outside the charset
		// 404, not 400, and the difference is the point: a slash or an
		// empty segment does not match the ROUTE, so the request never
		// reaches the parameter schema. A nested pool id is unaddressable
		// here no matter what pattern this parameter carries.
		{"infra/prod", fiber.StatusNotFound},
		{"", fiber.StatusNotFound},
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

// TestPoolTraversalIsRefusedAtTheRoute proves the dot carve-out bites on a real request, and (what
// TestPoolIDIsLooserThanConfigID does not assert) that the refusal happens BEFORE the handler runs: a 400
// after the handler had sent the request, which a normalising proxy would land on the pool COLLECTION,
// would be the bug. Both spellings are refused by the parameter rule, not the router, measured: Fiber
// does not strip "." or ".." here, so the raw value reaches the declaration as "%2e%2e" does. Neither is
// redundant: widen the rule to the plain [A-Za-z0-9._-]+ charset and the two RAW rows answer 204 with the
// handler running while the escaped rows still answer 400 ("%" is outside it either way), so the escaped
// rows would stay green through the very regression this exists to catch. proxmox.validatePathSegment
// refuses the same pair at the choke point; this asserts the OTHER layer.
func TestPoolTraversalIsRefusedAtTheRoute(t *testing.T) {
	const path = clusterScope + "/pools/:pool_id"

	for _, id := range []string{".", "..", "%2e", "%2e%2e"} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodDelete, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		target := pathPrefix + "clusters/" + testClusterID + "/pools/" + id
		status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusBadRequest {
			t.Errorf("pool id %q: status = %d (%q), want 400 from the parameter rule", id, status, env.Message)
		}
		if cap.called {
			t.Errorf("pool id %q: the handler ran for a relative segment", id)
		}
	}
}

// TestPoolCreateAcceptsANestedID is the counterpart to the 404 rows above.
// A nested pool id is unreachable as a path segment, but CREATING one is a
// plain form field to Proxmox, so the create body must not inherit the
// path parameter's no-slash restriction.
//
// It is asserted separately from TestPoolIDIsLooserThanConfigID because
// the two parameters are now different declarations for a reason, and a
// test that walked "every pool id parameter" would have to pick one rule
// and would quietly re-merge them.
func TestPoolCreateAcceptsANestedID(t *testing.T) {
	const path = clusterScope + "/pools"
	target := strings.NewReplacer(":cluster_id", testClusterID).Replace(path)

	for _, tt := range []struct {
		id   string
		want int
	}{
		{"prod01", fiber.StatusNoContent},
		{"infra/prod", fiber.StatusNoContent},    // two levels
		{"infra/prod/db", fiber.StatusNoContent}, // three, pve-poolid's max
		{"a/b/c/d", fiber.StatusBadRequest},      // four
		{"infra//prod", fiber.StatusBadRequest},  // empty segment
		{"infra/prod/", fiber.StatusBadRequest},  // trailing separator
		{"pool@name", fiber.StatusBadRequest},    // outside the charset
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		body := `{"poolid":"` + tt.id + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != tt.want {
			t.Errorf("poolid %q: status = %d (%q), want %d", tt.id, status, env.Message, tt.want)
			continue
		}
		if tt.want == fiber.StatusNoContent && cap.params.String("poolid") != tt.id {
			t.Errorf("poolid %q reached the handler as %q", tt.id, cap.params.String("poolid"))
		}
	}
}

// TestPoolCreateRefusesADotSegmentID pins the create side's refusal of the two ids no browser can address,
// on a real request, before the handler runs, as a 400 naming the parameter, and that everything else a
// pool id may be still reaches the handler untouched: a nested id, a leading dot or dash, dots inside a
// name, and a nested id whose segments are dots (which pve-poolid-new admits on purpose). The precondition
// makes the refusal Nexara's rather than a restatement of Proxmox's: pve-poolid admits both ids, so this
// route accepted them before, and a Proxmox predating pve-manager 7eadbed6 would too.
func TestPoolCreateRefusesADotSegmentID(t *testing.T) {
	const path = clusterScope + "/pools"
	target := strings.NewReplacer(":cluster_id", testClusterID).Replace(path)

	loose := apischema.Properties{"poolid": {Type: apischema.String, Pattern: apischema.Rule("pve-poolid")}}
	for _, id := range []string{".", ".."} {
		if _, err := loose.Validate(map[string]any{"poolid": id}); err != nil {
			t.Fatalf("precondition: pve-poolid refuses %q (%v), so refusing it here would be Proxmox's rule, "+
				"not a Nexara restriction", id, err)
		}
	}

	for _, tt := range []struct {
		id      string
		refused bool
	}{
		{".", true},
		{"..", true},
		{"infra/prod", false},
		{".x", false},
		{"-x", false},
		{"..archive", false},
		{"...", false},
		{"a/..", false},
		{"./b", false},
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		body, err := json.Marshal(map[string]string{"poolid": tt.id})
		if err != nil {
			t.Fatalf("encoding body for %q: %v", tt.id, err)
		}
		status, env := send(t, app, jsonRequest(http.MethodPost, target, string(body)))
		if tt.refused {
			if status != fiber.StatusBadRequest || env.Error != "bad_request" || !strings.HasPrefix(env.Message, "poolid: ") {
				t.Errorf("poolid %q: answered %d %+v, want a 400 naming poolid", tt.id, status, env)
			}
			if cap.called {
				t.Errorf("poolid %q: the handler ran; the refusal has to come from the declaration", tt.id)
			}
			continue
		}
		if status != fiber.StatusNoContent || !cap.called || cap.params.String("poolid") != tt.id {
			t.Errorf("poolid %q: answered %d %+v and reached the handler as %v, want it passed through",
				tt.id, status, env, cap.called)
		}
	}
}

// TestPoolUpdateCommentStaysTristate pins the absent-versus-empty distinction
// the handler forwards as a *string.
//
// Omitting `comment` must leave the stored one alone; sending it empty must
// clear it. The handler's p.OptString read keeps the two apart whatever the
// declaration says (apischema.Property.Default), so the declaration check
// below is about the docs, and the requests after it pin the distinction.
func TestPoolUpdateCommentStaysTristate(t *testing.T) {
	const path = clusterScope + "/pools/:pool_id"
	prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["comment"]
	if !prop.Optional {
		t.Error("comment is required; the edit dialog saves without it")
	}
	if prop.Default != nil {
		t.Errorf("comment declares default %#v; an omitted comment is left alone, and a default "+
			"would document it as written", prop.Default)
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
