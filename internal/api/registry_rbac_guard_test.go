package api

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// This file closes the holes in rbac_route_guard_test.go's guards that the
// Phase 2 security review flagged: routeHandlerKey only ever resolves a
// LIVE ROUTE's terminal handler, which for a registry route is
// api.Endpoint.serve.func1 — never a handlers.-qualified name — so both
// TestGuard_EveryRouteEnforcesPermission and
// TestGuard_DocumentedPermissionMatchesEnforcement silently `continue`d past
// every registry route; and publicRouteKeys parses router.go's .Get/.Post/…
// calls, which never sees mountRegistry's bare call, so a registry endpoint
// declaring Permissions{Public: "..."} would go out unauthenticated with
// nothing to notice.
//
// The fix reads the DECLARATION instead of the route table — see
// registryEnforcementGaps, registryPublicRouteKeys and
// registryEnforcedActions below — and rbac_route_guard_test.go's three
// TestGuard_* functions were extended to call them (search that file for
// "registry" to find the edits).
//
// A second review pass added registrySelfServiceRouteKeys further down:
// Public got folded into a reviewed, enumerable list
// (TestGuard_PublicRoutesAreExpected) but SelfService — the riskier of the
// two middleware-free-yet-authenticated shapes — did not, so an inline
// Permissions.SelfService reason could pass with no equivalent review an
// operator could diff against later.

// registryEndpointsByKey indexes eps by their normalized "METHOD path" key
// — the same shape registryRouteKeySet uses, but keeping the Endpoint
// itself rather than a bool. A caller that needs the declaration (not just
// "is this a registry route") uses this; registryRouteKeySet is built on
// top of it.
func registryEndpointsByKey(eps []Endpoint) map[string]Endpoint {
	out := make(map[string]Endpoint, len(eps))
	for _, e := range eps {
		out[e.Method+" "+normalizeRoutePath(e.Path)] = e
	}
	return out
}

// registryRouteKeySet returns the normalized "METHOD path" key of every
// endpoint eps declares, in the same shape normalizeRoutePath produces for
// a live route in s.app.GetRoutes(true). It is how a guard recognizes "this
// route belongs to the registry" without re-deriving that from the mounted
// route table — which, for a Deferred, Advisory or SelfService endpoint,
// carries no middleware to recognize it by in the first place.
func registryRouteKeySet(eps []Endpoint) map[string]bool {
	byKey := registryEndpointsByKey(eps)
	out := make(map[string]bool, len(byKey))
	for key := range byKey {
		out[key] = true
	}
	return out
}

// registryPublicRouteKeys returns the normalized "METHOD path" key of every
// registry endpoint declaring Permissions.Public. It is the registry
// counterpart to publicRouteKeys' source-level parse of router.go: folding
// this into TestGuard_PublicRoutesAreExpected's `actual` set is what keeps
// "going public" a deliberate, reason-carrying edit to publicRoutes for a
// registry route too, the same as it already is for a legacy one.
func registryPublicRouteKeys(eps []Endpoint) map[string]bool {
	out := map[string]bool{}
	for _, e := range eps {
		if e.Permissions.Public != "" {
			out[e.Method+" "+normalizeRoutePath(e.Path)] = true
		}
	}
	return out
}

// registrySelfServiceRouteKeys returns the normalized "METHOD path" key of
// every registry endpoint declaring Permissions.SelfService. Unlike
// Public, this is NOT the exemption check itself (SelfService still needs
// no RBAC check — see registryEnforcementGaps' doc comment) — it feeds
// TestGuard_RegistrySelfServiceRoutesAreReviewed, which requires each such
// route to ALSO be listed in selfServiceRoutes. SelfService is the riskier
// of the two shapes that install no middleware while still authenticating
// the caller — the session check runs, which is exactly where an IDOR (a
// subject taken from the path or body rather than the session) hides, per
// the field's own doc comment — so an inline reason string, written once
// and never diffed against again, is not enough on its own; it needs the
// same reviewed, enumerable list a legacy self-service route already
// requires.
func registrySelfServiceRouteKeys(eps []Endpoint) map[string]bool {
	out := map[string]bool{}
	for _, e := range eps {
		if e.Permissions.SelfService != "" {
			out[e.Method+" "+normalizeRoutePath(e.Path)] = true
		}
	}
	return out
}

// enforcementShape names which of the two shapes registryEnforcementGaps
// requires to prove a reachable RBAC check — or "" for a shape it does not
// apply to. Check/Alternatives are structural (see registryEnforcementGaps'
// doc comment); Public and SelfService install no middleware but are
// exempt by design, the latter as of the review that added
// registrySelfServiceRouteKeys above.
func enforcementShape(p Permissions) string {
	switch {
	case p.Deferred != "":
		return "Deferred"
	case p.Advisory != nil:
		return "Advisory"
	default:
		return ""
	}
}

// enforcementGapMessage decides whether a Deferred/Advisory endpoint's
// resolved handler key proves it enforces anything, and returns the
// finding message if not, or "" if the endpoint is clean. key is whatever
// routeHandlerKey resolved e.Handler to — "" if it could not be resolved to
// a handlers.-qualified function at all.
//
// Split out from registryEnforcementGaps so a test can drive the DECISION
// with a fabricated key and a hand-built graph, proving the logic without
// needing a real handlers-package symbol of the new Handler signature to
// reference — none exists until Phase 4 migrates one. routeHandlerKey
// itself is exercised against 550+ real symbols by every run of
// TestGuard_EveryRouteEnforcesPermission already; nothing here needs to
// re-prove that it resolves a real one.
func enforcementGapMessage(method, path, shape, key string, graph *callGraph) string {
	switch {
	case key == "":
		return fmt.Sprintf(
			"%s %s: %s permission needs the handler to reach an RBAC check, but its Handler does not "+
				"resolve to a handlers.-qualified function — this guard cannot verify it",
			method, path, shape)
	case !graph.reachesPermissionCheck(key):
		return fmt.Sprintf(
			"%s %s: %s permission but handlers.%s performs no RBAC check reachable by static analysis",
			method, path, shape, key)
	default:
		return ""
	}
}

// registryEnforcementGaps is the registry counterpart to
// TestGuard_EveryRouteEnforcesPermission's legacy walk. A route's
// Permissions declaration already says how it is authorized, so — unlike a
// legacy handler, whose only record of intent is its call graph — most
// shapes need no rediscovery:
//
//   - Check / Alternatives install real middleware (Permissions.middleware):
//     handlers.RequireClusterPermission, handlers.RequirePermission or
//     handlers.RequireAnyPermission, every one of which calls a
//     permissionLeaves function itself. That is satisfied structurally by
//     the declaration — TestRegistryChainOrder and
//     TestRegistryPermissionGateDeniesAndAllows already pin it end to end —
//     so there is nothing for THIS guard to walk.
//   - Public installs no middleware by design, and is exempted here on
//     purpose: it is checked by TestGuard_PublicRoutesAreExpected instead,
//     via registryPublicRouteKeys above, which is where "going public"
//     already has to be a deliberate, reason-carrying edit.
//   - SelfService installs no middleware EITHER, and — unlike Deferred and
//     Advisory below — that is not something this guard can verify by
//     walking further: the whole point of the shape is that a permission
//     check would be meaningless (the subject is the caller's own identity,
//     not something a grant could deny), and every legacy analogue proves
//     the point by counter-example — selfServiceRoutes' own handlers (e.g.
//     AuthHandler.GetMe) call no permission leaf at all, so requiring one
//     here would flag a correct declaration. Its safety is instead the
//     reviewed, enumerable list TestGuard_RegistrySelfServiceRoutesAreReviewed
//     requires further down — the same standard selfServiceRoutes already
//     holds a legacy self-service route to.
//   - Deferred and Advisory install no middleware because the HANDLER is
//     the gate: Deferred decides its own action/resource at request time,
//     and AdvisoryCheck's own doc comment gives accessibleClusters — itself
//     a permissionLeaves entry — as the filter it documents. Both shapes
//     are required to actually reach a permission leaf, proven the same way
//     the legacy walk proves it for a hand-placed check: resolve the
//     declared Handler to a call-graph key with routeHandlerKey (the exact
//     function a live route's terminal handler already resolves through)
//     and walk from there with graph.reachesPermissionCheck.
func registryEnforcementGaps(eps []Endpoint, graph *callGraph) []string {
	var gaps []string
	for _, e := range eps {
		shape := enforcementShape(e.Permissions)
		if shape == "" {
			continue // Check/Alternatives: structural. Public/SelfService: exempt by design — see the doc comment above.
		}
		if msg := enforcementGapMessage(e.Method, e.Path, shape, routeHandlerKey(e.Handler), graph); msg != "" {
			gaps = append(gaps, msg)
		}
	}
	return gaps
}

// registryEnforcedActions returns the action(s) a registry endpoint's
// Check/Alternatives middleware actually enforces — the registry
// equivalent of rbac_route_guard_test.go's literalActionsFor call-graph
// walk, except exact rather than approximate: Permissions.middleware is a
// pure function of Check/Alternatives (see registryEnforcementGaps' doc
// comment), so there is nothing to statically infer. The other four
// shapes enforce no STATIC action — Deferred/Advisory decide at request
// time, Public/SelfService enforce none at all — and return an empty map,
// matching what literalActionsFor returns for a fully dynamic legacy
// action.
func registryEnforcedActions(p Permissions) map[string]bool {
	out := map[string]bool{}
	switch {
	case p.Check != nil:
		out[p.Check.Action] = true
	case len(p.Alternatives) > 0:
		for _, alt := range p.Alternatives {
			out[alt.Action] = true
		}
	}
	return out
}

// actionMatchesDeclaration reports whether at least one action named in
// declared ("view:vm" or "view:vm|view:node" — endpointMeta's Permission
// format) is present in enforced. Shared by
// TestGuard_DocumentedPermissionMatchesEnforcement's legacy call-graph path
// and its registry Check/Alternatives path (registryEnforcedActions above),
// so the two cannot silently define "matches" differently.
func actionMatchesDeclaration(declared string, enforced map[string]bool) bool {
	for _, alt := range strings.Split(declared, "|") {
		action, _, found := strings.Cut(strings.TrimSpace(alt), ":")
		if !found {
			continue
		}
		if enforced[action] {
			return true
		}
	}
	return false
}

// documentedPermissionViolation reports the failure message for one
// route's curated endpointMeta.Permission against its enforced
// action(s), or "" if they agree, or there is nothing to verify (a fully
// dynamic legacy action, or a registry shape with no static action —
// Deferred/Advisory/Public/SelfService, per registryEnforcedActions).
// Factored out of TestGuard_DocumentedPermissionMatchesEnforcement so a
// synthetic case — registry or legacy shaped — can prove the comparison
// fires, without needing a live route table (a real registry endpoint
// cannot share a path with a real curated endpointMeta entry without
// ALSO colliding with the legacy route that currently owns it).
func documentedPermissionViolation(key, declared, handlerDescription string, enforced map[string]bool) string {
	if len(enforced) == 0 {
		return ""
	}
	if actionMatchesDeclaration(declared, enforced) {
		return ""
	}
	return fmt.Sprintf("%s: API docs promise permission %q but %s gates on action(s) %v — "+
		"update endpointMeta in internal/api/handlers/api_docs.go to match the code",
		key, declared, handlerDescription, sortedKeys(enforced))
}

// selfServiceReviewViolations reports every key in declared that is not
// also a key in reviewed — the pure comparison
// TestGuard_RegistrySelfServiceRoutesAreReviewed drives against the real
// selfServiceRoutes map, factored out so a synthetic case can prove it
// fires without needing the real map or a live server.
func selfServiceReviewViolations(declared map[string]bool, reviewed map[string]string) []string {
	var out []string
	for key := range declared {
		if _, listed := reviewed[key]; !listed {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// registryProbeEndpoint builds a minimal, valid Endpoint for these tests:
// every field Register requires is filled with a throwaway value except
// Path, Permissions and Handler, which the caller supplies. params must
// declare an entry for every :param segment in path (see checkPathParams);
// registryProbeEndpointNoParams covers the common case of a path with
// none.
func registryProbeEndpoint(path string, perms Permissions, h Handler, params apischema.Properties) Endpoint {
	return Endpoint{
		Method:      fiber.MethodGet,
		Path:        path,
		Description: "Synthetic probe endpoint for a guard test.",
		Group:       "Test",
		Permissions: perms,
		Parameters:  params,
		Handler:     h,
	}
}

// registryProbeEndpointNoParams is registryProbeEndpoint for a path with
// no :param segments, which is most of these tests.
func registryProbeEndpointNoParams(path string, perms Permissions, h Handler) Endpoint {
	return registryProbeEndpoint(path, perms, h, apischema.Properties{})
}

// noopParamsHandler is a Handler-shaped function literal defined in
// package api — deliberately NOT in internal/api/handlers, unlike every
// real handler the codebase convention (see internal/api/CLAUDE.md)
// requires. routeHandlerKey can only ever resolve a name inside
// /handlers., so this is what "the guard cannot verify it" actually looks
// like at the reflect/runtime level, not a hand-typed stand-in for it.
func noopParamsHandler(_ fiber.Ctx, _ *apischema.Params) error { return nil }

// TestRegistryEnforcementGaps_UnresolvableHandlerIsFlagged proves the first
// branch bites end to end: a Deferred (or Advisory) endpoint whose Handler
// cannot be resolved to a handlers.-qualified function is reported by the
// real registryEnforcementGaps + routeHandlerKey pairing, rather than
// silently passing the way routeHandlerKey's "" used to make it before this
// guard existed.
func TestRegistryEnforcementGaps_UnresolvableHandlerIsFlagged(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/probe",
		Permissions{Deferred: "the resource depends on the request body"}, noopParamsHandler))

	got := registryEnforcementGaps(reg.Endpoints(), &callGraph{})
	if len(got) != 1 {
		t.Fatalf("gaps = %v, want exactly 1", got)
	}
	if !strings.Contains(got[0], "does not resolve to a handlers.-qualified function") {
		t.Errorf("gap message = %q, want it to name the unresolvable handler", got[0])
	}
}

// TestEnforcementGapMessage drives the resolved-key decision directly with
// a fabricated key and a hand-built graph, proving both outcomes a real
// resolvable handler could produce: absent from the graph (no RBAC check
// reachable) is flagged, and present with a permission leaf downstream is
// not. See the function's own doc comment for why a fabricated key is the
// right tool here rather than a real handlers-package symbol.
func TestEnforcementGapMessage(t *testing.T) {
	const key = "FakeHandler.Serve"

	for _, shape := range []string{"Deferred", "Advisory"} {
		t.Run(shape+"/unresolvable handler", func(t *testing.T) {
			msg := enforcementGapMessage("GET", "/api/v1/probe", shape, "", &callGraph{})
			if !strings.Contains(msg, "does not resolve to a handlers.-qualified function") {
				t.Errorf("message = %q, want it to name the unresolvable handler", msg)
			}
		})

		t.Run(shape+"/not reaching a leaf is flagged", func(t *testing.T) {
			graph := &callGraph{calls: map[string]map[string]bool{
				key: {"someUnrelatedHelper": true},
			}}
			msg := enforcementGapMessage("GET", "/api/v1/probe", shape, key, graph)
			if !strings.Contains(msg, "performs no RBAC check reachable") {
				t.Errorf("message = %q, want it to say no RBAC check was reachable", msg)
			}
		})

		t.Run(shape+"/reaching a leaf passes", func(t *testing.T) {
			graph := &callGraph{calls: map[string]map[string]bool{
				key: {"accessibleClusters": true},
			}}
			if msg := enforcementGapMessage("GET", "/api/v1/probe", shape, key, graph); msg != "" {
				t.Errorf("message = %q, want \"\" — the handler reaches a permission leaf", msg)
			}
		})
	}
}

// TestEnforcementShape pins the shape/exemption split registryEnforcementGaps
// relies on.
func TestEnforcementShape(t *testing.T) {
	cases := []struct {
		name  string
		perms Permissions
		want  string
	}{
		{"check", Permissions{Check: &Check{Action: "view", Resource: "widget", Scope: ScopeGlobal}}, ""},
		{"alternatives", Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeGlobal},
			{Action: "view", Resource: "container", Scope: ScopeGlobal},
		}}, ""},
		{"deferred", Permissions{Deferred: "the resource depends on the request body"}, "Deferred"},
		{"advisory", Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeGlobal},
			Reason: "accessibleClusters filters the listing",
		}}, "Advisory"},
		{"public", Permissions{Public: "no session exists yet"}, ""},
		{"self-service", Permissions{SelfService: "acts on the caller's own identity, taken from the session"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := enforcementShape(tc.perms); got != tc.want {
				t.Errorf("enforcementShape(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestRegistryEnforcementGaps_OtherShapesAreExempt proves the negative for
// the four shapes registryEnforcementGaps skips outright, using the same
// unresolvable handler TestRegistryEnforcementGaps_UnresolvableHandlerIsFlagged
// proves DOES get flagged for Deferred/Advisory — so this is not "an easy
// case passes", it is "the exact case that fails for Deferred/Advisory does
// not fail for these".
func TestRegistryEnforcementGaps_OtherShapesAreExempt(t *testing.T) {
	cases := []struct {
		name  string
		perms Permissions
	}{
		{"check", Permissions{Check: &Check{Action: "view", Resource: "widget", Scope: ScopeGlobal}}},
		{"alternatives", Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeGlobal},
			{Action: "view", Resource: "container", Scope: ScopeGlobal},
		}}},
		{"public", Permissions{Public: "no session exists yet"}},
		{"self-service", Permissions{SelfService: "acts on the caller's own identity, taken from the session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(registryProbeEndpointNoParams("/api/v1/probe", tc.perms, noopParamsHandler))
			if got := registryEnforcementGaps(reg.Endpoints(), &callGraph{}); len(got) != 0 {
				t.Errorf("gaps = %v, want none — %s installs no middleware by design and is exempt from this check", got, tc.name)
			}
		})
	}
}

// TestRegistryPublicRouteKeys proves registryPublicRouteKeys reports
// exactly the endpoints declaring Permissions.Public, so folding it into
// TestGuard_PublicRoutesAreExpected's `actual` set cannot silently miss one.
func TestRegistryPublicRouteKeys(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/public-probe",
		Permissions{Public: "no session exists yet"}, noopParamsHandler))
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        "/api/v1/gated-probe",
		Description: "Synthetic probe endpoint for a guard test.",
		Group:       "Test",
		Permissions: Permissions{Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeGlobal}},
		Parameters:  apischema.Properties{},
		Handler:     noopParamsHandler,
	})

	got := registryPublicRouteKeys(reg.Endpoints())
	want := map[string]bool{"GET /api/v1/public-probe": true}
	if len(got) != len(want) || !got["GET /api/v1/public-probe"] {
		t.Errorf("registryPublicRouteKeys = %v, want %v", got, want)
	}
	if got["POST /api/v1/gated-probe"] {
		t.Error("registryPublicRouteKeys included a Check-gated endpoint")
	}
}

// TestRegistrySelfServiceRouteKeys proves registrySelfServiceRouteKeys
// reports exactly the endpoints declaring Permissions.SelfService.
func TestRegistrySelfServiceRouteKeys(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/self-service-probe",
		Permissions{SelfService: "acts on the caller's own identity, taken from the session"}, noopParamsHandler))
	reg.Register(registryProbeEndpointNoParams("/api/v1/public-probe",
		Permissions{Public: "no session exists yet"}, noopParamsHandler))

	got := registrySelfServiceRouteKeys(reg.Endpoints())
	want := map[string]bool{"GET /api/v1/self-service-probe": true}
	if len(got) != len(want) || !got["GET /api/v1/self-service-probe"] {
		t.Errorf("registrySelfServiceRouteKeys = %v, want %v", got, want)
	}
	if got["GET /api/v1/public-probe"] {
		t.Error("registrySelfServiceRouteKeys included a Public endpoint")
	}
}

// TestSelfServiceReviewViolations_CatchesAnUnlistedRoute proves the guard
// bites: a registry route declaring SelfService that is not ALSO a key in
// the reviewed list is reported by name, and an unrelated listed route is
// not.
func TestSelfServiceReviewViolations_CatchesAnUnlistedRoute(t *testing.T) {
	declared := map[string]bool{"GET /api/v1/self-service-probe": true}
	reviewed := map[string]string{"GET /api/v1/auth/me": "returns the caller's own profile"}

	got := selfServiceReviewViolations(declared, reviewed)
	if len(got) != 1 || got[0] != "GET /api/v1/self-service-probe" {
		t.Fatalf("violations = %v, want exactly [\"GET /api/v1/self-service-probe\"]", got)
	}
}

// TestRegistryEnforcedActions pins registryEnforcedActions' shape-by-shape
// behavior: an exact action for Check, every action for Alternatives, and
// nothing statically knowable for the four shapes with no middleware.
func TestRegistryEnforcedActions(t *testing.T) {
	cases := []struct {
		name  string
		perms Permissions
		want  map[string]bool
	}{
		{"check", Permissions{Check: &Check{Action: "manage", Resource: "role", Scope: ScopeGlobal}},
			map[string]bool{"manage": true}},
		{"alternatives", Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeGlobal},
			{Action: "view", Resource: "container", Scope: ScopeGlobal},
		}}, map[string]bool{"view": true}},
		{"deferred", Permissions{Deferred: "the resource depends on the request body"}, map[string]bool{}},
		{"advisory", Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeGlobal},
			Reason: "accessibleClusters filters the listing",
		}}, map[string]bool{}},
		{"public", Permissions{Public: "no session exists yet"}, map[string]bool{}},
		{"self-service", Permissions{SelfService: "acts on the caller's own identity, taken from the session"}, map[string]bool{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := registryEnforcedActions(tc.perms)
			if len(got) != len(tc.want) {
				t.Fatalf("registryEnforcedActions(%s) = %v, want %v", tc.name, got, tc.want)
			}
			for action := range tc.want {
				if !got[action] {
					t.Errorf("registryEnforcedActions(%s) = %v, want it to include %q", tc.name, got, action)
				}
			}
		})
	}
}

// TestActionMatchesDeclaration pins the shared "does declared name an
// enforced action" comparison both the legacy call-graph path and the
// registry Check/Alternatives path route through.
func TestActionMatchesDeclaration(t *testing.T) {
	if !actionMatchesDeclaration("view:vm|view:container", map[string]bool{"view": true}) {
		t.Error(`want a match: "view" is the action behind the second alternative`)
	}
	if actionMatchesDeclaration("view:vm", map[string]bool{"manage": true}) {
		t.Error(`want no match: declared names "view", enforced only has "manage"`)
	}
}

// TestDocumentedPermissionViolation_CatchesADriftedAction proves finding
// 2 bites for BOTH shapes: a registry endpoint whose Check enforces a
// different action than endpointMeta documents, and the pre-existing
// legacy shape (already covered by the guard before this review, pinned
// here so the two paths cannot silently diverge in what counts as a
// violation).
func TestDocumentedPermissionViolation_CatchesADriftedAction(t *testing.T) {
	t.Run("registry: Check drifts from endpointMeta", func(t *testing.T) {
		enforced := registryEnforcedActions(Permissions{Check: &Check{Action: "manage", Resource: "role", Scope: ScopeGlobal}})
		msg := documentedPermissionViolation("GET /api/v1/rbac/permissions", "view:role", "registry endpoint (manage:role)", enforced)
		if msg == "" {
			t.Fatal("want a violation: endpointMeta says view:role, the registry Check enforces manage:role")
		}
		if !strings.Contains(msg, "view:role") || !strings.Contains(msg, "manage") {
			t.Errorf("message = %q, want it to name both the documented and the enforced action", msg)
		}
	})

	t.Run("registry: Check matches endpointMeta", func(t *testing.T) {
		enforced := registryEnforcedActions(Permissions{Check: &Check{Action: "view", Resource: "role", Scope: ScopeGlobal}})
		if msg := documentedPermissionViolation("GET /api/v1/rbac/permissions", "view:role", "registry endpoint (view:role)", enforced); msg != "" {
			t.Errorf(`message = %q, want "" — the declared and enforced actions agree`, msg)
		}
	})

	t.Run("registry: Deferred has nothing static to compare", func(t *testing.T) {
		enforced := registryEnforcedActions(Permissions{Deferred: "the resource depends on the request body"})
		if msg := documentedPermissionViolation("POST /api/v1/probe", "view:vm", "registry endpoint (deferred)", enforced); msg != "" {
			t.Errorf(`message = %q, want "" — nothing statically enforced to contradict the docs`, msg)
		}
	})

	t.Run("legacy: drifted action", func(t *testing.T) {
		msg := documentedPermissionViolation("POST /api/v1/probe", "view:vm", "handlers.VMHandler.ConsoleToken",
			map[string]bool{"console": true})
		if msg == "" {
			t.Fatal("want a violation: docs say view:vm, the handler gates on console")
		}
	})
}

// TestGuard_RegistrySelfServiceRoutesAreReviewed is the production guard:
// every registered registry endpoint declaring Permissions.SelfService
// must also be listed in selfServiceRoutes. It iterates zero times today —
// nothing is migrated yet — and starts doing real work the moment Phase 4
// registers a SelfService endpoint.
func TestGuard_RegistrySelfServiceRoutesAreReviewed(t *testing.T) {
	s := newRouteStubServer(t)
	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	declared := map[string]bool{}
	for key := range registrySelfServiceRouteKeys(endpoints.Endpoints()) {
		// Only routes that actually registered — router.go gates several
		// blocks on a handler being non-nil; mirrors
		// TestGuard_PublicRoutesAreExpected.
		if registered[key] {
			declared[key] = true
		}
	}

	for _, key := range selfServiceReviewViolations(declared, selfServiceRoutes) {
		t.Errorf("registry route %s declares Permissions.SelfService but is not listed in "+
			"selfServiceRoutes — add it there with the reviewed reason its subject comes from "+
			"the session, not from caller input", key)
	}
}
