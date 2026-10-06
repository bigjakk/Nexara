package api

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// This file closes the holes in rbac_route_guard_test.go's guards that the Phase 2
// security review flagged: routeHandlerKey only resolves a LIVE ROUTE's terminal handler,
// which for a registry route is api.Endpoint.serve.func1, so both guards there silently
// `continue`d past every registry route, and publicRouteKeys parses router.go, which never
// sees mountRegistry's call, so a registry endpoint declaring Permissions{Public: "..."}
// would go out unauthenticated with nothing to notice. The fix reads the DECLARATION
// (registryEnforcementGaps, registryPublicRouteKeys; rbac_route_guard_test.go calls both),
// and SelfService, the riskier of the two middleware-free-yet-authenticated shapes, joined
// Public as a reviewed, enumerable list (TestGuard_RegistrySelfServiceRoutesAreReviewed).

// registryEndpointsByKey indexes eps by their normalized "METHOD path" key, keeping the
// Endpoint (registryRouteKeySet is built on top of it).
func registryEndpointsByKey(eps []Endpoint) map[string]Endpoint {
	out := make(map[string]Endpoint, len(eps))
	for _, e := range eps {
		out[e.Method+" "+normalizeRoutePath(e.Path)] = e
	}
	return out
}

// registryRouteKeySet returns the normalized "METHOD path" key of every endpoint eps
// declares, in normalizeRoutePath's shape: how a guard recognizes "this route belongs to
// the registry" without the mounted table, which for a Deferred, Advisory or SelfService
// endpoint carries no middleware to recognize it by.
func registryRouteKeySet(eps []Endpoint) map[string]bool {
	byKey := registryEndpointsByKey(eps)
	out := make(map[string]bool, len(byKey))
	for key := range byKey {
		out[key] = true
	}
	return out
}

// registryPublicRouteKeys returns the key of every registry endpoint declaring
// Permissions.Public, the counterpart to publicRouteKeys' parse of router.go: it keeps
// "going public" a deliberate, reasoned edit to publicRoutes for a registry route too.
func registryPublicRouteKeys(eps []Endpoint) map[string]bool {
	out := map[string]bool{}
	for _, e := range eps {
		if e.Permissions.Public != "" {
			out[e.Method+" "+normalizeRoutePath(e.Path)] = true
		}
	}
	return out
}

// registrySelfServiceRouteKeys returns the key of every registry endpoint declaring
// Permissions.SelfService. It feeds TestGuard_RegistrySelfServiceRoutesAreReviewed, which
// requires each to be listed in selfServiceRoutes too: the session check runs, so an IDOR
// (a subject taken from the path or body, not the session) hides exactly there, and an
// inline reason written once and never diffed is not enough.
func registrySelfServiceRouteKeys(eps []Endpoint) map[string]bool {
	out := map[string]bool{}
	for _, e := range eps {
		if e.Permissions.SelfService != "" {
			out[e.Method+" "+normalizeRoutePath(e.Path)] = true
		}
	}
	return out
}

// enforcementShape names the shape registryEnforcementGaps must prove a reachable RBAC check
// for, or "" for the shapes it skips (Check/Alternatives are structural; Public and
// SelfService install no middleware and are exempt by design).
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

// enforcementGapMessage decides whether a Deferred/Advisory endpoint's resolved handler key
// (routeHandlerKey's, "" when it names no handlers.-qualified function) proves it enforces
// anything, and returns the finding or "". Split out so a fabricated key and a hand-built
// graph can drive the decision without depending on one real handler.
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

// registryEnforcementGaps is the registry counterpart to TestGuard_EveryRouteEnforcesPermission's
// legacy walk. A declaration already says how a route is authorized, so most shapes need no
// rediscovery: Check/Alternatives install real middleware (pinned end to end by
// TestRegistryChainOrder and TestRegistryPermissionGateDeniesAndAllows); Public is held by
// TestGuard_PublicRoutesAreExpected; SelfService by the reviewed list, since a permission
// check there would be meaningless (selfServiceRoutes' own handlers call no leaf). Deferred
// and Advisory install none because the HANDLER is the gate, so each must reach a permission
// leaf: resolve Handler with routeHandlerKey and walk graph.reachesPermissionCheck.
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

// actionMatchesDeclaration reports whether at least one action named in declared ("view:vm"
// or "view:vm|view:node", endpointMeta's format) is in enforced; its caller is
// documentedPermissionViolation, for the legacy call-graph path of
// TestGuard_DocumentedPermissionMatchesEnforcement.
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

// documentedPermissionViolation reports the failure for one route's endpointMeta.Permission
// against its enforced action(s), or "" when they agree or there is nothing to verify (a
// fully dynamic legacy action). Factored out so a synthetic case can prove it fires.
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

// normalizePermissionList renders "a:b|c:d" comparably: entries trimmed, sorted, rejoined, so
// Describe()'s " | " and endpointMeta's "|" agree whatever order the alternatives were written in.
func normalizePermissionList(s string) string {
	parts := strings.Split(s, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

// selfServiceReviewViolations reports every key in declared that is not also in reviewed:
// the pure comparison TestGuard_RegistrySelfServiceRoutesAreReviewed drives.
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

// registryProbeEndpoint builds a minimal, valid Endpoint for these tests: path, Permissions
// and Handler from the caller, the rest throwaway. params must declare every :param
// segment of path (see checkPathParams).
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

// registryProbeEndpointNoParams is registryProbeEndpoint for a path with no :param segments.
func registryProbeEndpointNoParams(path string, perms Permissions, h Handler) Endpoint {
	return registryProbeEndpoint(path, perms, h, apischema.Properties{})
}

// noopParamsHandler is defined in package api, deliberately NOT in internal/api/handlers like
// every real handler: routeHandlerKey resolves only names inside /handlers., so this is what
// "the guard cannot verify it" looks like at the reflect/runtime level.
func noopParamsHandler(_ fiber.Ctx, _ *apischema.Params) error { return nil }

// TestSelfServiceReviewViolations_CatchesAnUnlistedRoute drives the decisions of this
// file's guards over probes, since the real registry is clean: an unlisted SelfService route
// is named; the Public and SelfService key filters pick exactly their own; a Deferred or
// Advisory endpoint is flagged when its handler cannot be resolved or reaches no permission
// leaf, and every other shape is exempt, by the very closure that is flagged for Deferred;
// and a documented permission that names no enforced action is a violation.
func TestSelfServiceReviewViolations_CatchesAnUnlistedRoute(t *testing.T) {
	const reason = "synthetic reason"
	check := &Check{Action: "manage", Resource: "widget", Scope: ScopeGlobal}
	advisory := &AdvisoryCheck{Check: Check{Action: "view", Resource: "cluster", Scope: ScopeGlobal}, Reason: "accessibleClusters filters"}

	if got := selfServiceReviewViolations(map[string]bool{"GET /api/v1/self-service-probe": true},
		map[string]string{"GET /api/v1/auth/me": "the caller's own profile"}); !slices.Equal(got, []string{"GET /api/v1/self-service-probe"}) {
		t.Errorf("review violations = %v, want exactly the unlisted route", got)
	}

	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/public-probe", Permissions{Public: reason}, noopParamsHandler))
	reg.Register(registryProbeEndpointNoParams("/api/v1/self-service-probe", Permissions{SelfService: reason}, noopParamsHandler))
	reg.Register(registryProbeEndpointNoParams("/api/v1/gated-probe", Permissions{Check: check}, noopParamsHandler))
	if got := registryPublicRouteKeys(reg.Endpoints()); !maps.Equal(got, map[string]bool{"GET /api/v1/public-probe": true}) {
		t.Errorf("public keys = %v, want only the Public endpoint", got)
	}
	if got := registrySelfServiceRouteKeys(reg.Endpoints()); !maps.Equal(got, map[string]bool{"GET /api/v1/self-service-probe": true}) {
		t.Errorf("self-service keys = %v, want only the SelfService endpoint", got)
	}

	const key = "FakeHandler.Serve"
	reaches := &callGraph{calls: map[string]map[string]bool{key: {"accessibleClusters": true}}}
	misses := &callGraph{calls: map[string]map[string]bool{key: {"someUnrelatedHelper": true}}}
	for name, shape := range map[string]Permissions{
		"Deferred": {Deferred: "the resource depends on the request body"}, "Advisory": {Advisory: advisory}} {
		if got := enforcementShape(shape); got != name {
			t.Fatalf("enforcementShape(%+v) = %q, want %q", shape, got, name)
		}
		for _, tt := range []struct {
			key   string
			graph *callGraph
			want  string // a fragment of the finding; empty: clean
		}{
			{"", &callGraph{}, "does not resolve to a handlers.-qualified function"},
			{key, misses, "performs no RBAC check reachable"},
			{key, reaches, ""},
		} {
			msg := enforcementGapMessage("GET", "/api/v1/probe", name, tt.key, tt.graph)
			if (tt.want == "") != (msg == "") || !strings.Contains(msg, tt.want) {
				t.Errorf("%s with key %q: message %q, want one carrying %q", name, tt.key, msg, tt.want)
			}
		}
		unresolvable := NewRegistry()
		unresolvable.Register(registryProbeEndpointNoParams("/api/v1/probe", shape, noopParamsHandler))
		if got := registryEnforcementGaps(unresolvable.Endpoints(), &callGraph{}); len(got) != 1 {
			t.Errorf("%s with a handler outside handlers/: gaps = %v, want exactly 1", name, got)
		}
	}
	for name, perms := range map[string]Permissions{"check": {Check: check}, "public": {Public: reason},
		"self-service": {SelfService: reason},
		"alternatives": {Alternatives: []Check{*check, {Action: "view", Resource: "widget", Scope: ScopeGlobal}}}} {
		exempt := NewRegistry()
		exempt.Register(registryProbeEndpointNoParams("/api/v1/probe", perms, noopParamsHandler))
		if got := registryEnforcementGaps(exempt.Endpoints(), &callGraph{}); len(got) != 0 {
			t.Errorf("%s installs no middleware by design or is structural, yet gaps = %v", name, got)
		}
	}

	if msg := documentedPermissionViolation("POST /api/v1/probe", "view:vm", "handlers.VMHandler.ConsoleToken",
		map[string]bool{"console": true}); !strings.Contains(msg, "view:vm") || !strings.Contains(msg, "console") {
		t.Errorf("drifted action: message %q, want a violation naming both the documented and the enforced action", msg)
	}
	for _, enforced := range []map[string]bool{{"view": true}, {}} {
		if msg := documentedPermissionViolation("GET /api/v1/probe", "view:vm|view:container", "handlers.X", enforced); msg != "" {
			t.Errorf("enforced %v: message %q, want none (an enforced alternative, or nothing static to compare)", enforced, msg)
		}
	}
}

// TestGuard_RegistrySelfServiceRoutesAreReviewed is the production guard:
// every registered registry endpoint declaring Permissions.SelfService
// must also be listed in selfServiceRoutes. It walks every SelfService
// declaration the registry holds.
func TestGuard_RegistrySelfServiceRoutesAreReviewed(t *testing.T) {
	s := sharedRouteStub(t)
	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	declared := map[string]bool{}
	for key := range registrySelfServiceRouteKeys(s.registry.Endpoints()) {
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
