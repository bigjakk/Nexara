package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// Registration ORDER decides which route Fiber serves, and until this file
// nothing stated the rule or checked it.
//
// The Registry type's own doc comment says order is preserved "because
// Fiber matches routes in the order they are registered: a literal path
// registered after a :param path that also matches it is unreachable", and
// buildRegistry carries exactly one ad-hoc instance of the constraint — a
// comment that registerBackupEndpoints must run AFTER registerPBSEndpoints.
// Between the two there is no general rule, and nothing fails if one is
// broken. A route made unreachable this way does not 500, does not log,
// and does not disappear from /settings/api-docs: it is in the route table,
// it is in the declarations, and the wrong handler answers it. The symptom
// an operator sees is a 400 or a 404 from a handler they never called.
//
// registry_shadow_guard_test.go covers the neighbouring case — a registry
// route against a LEGACY path in router.go — and owns the routing model
// this file reuses (pathIsCapturedBy, segmentCaptures, isOptionalSegment).
// It does not look at one registry route against another, which is the
// whole of the 535-route surface and the only part that grows.
//
// Two things the first run of this guard established about the registry as
// it stands, worth writing down because neither is visible from reading
// buildRegistry:
//
//   - Exactly ONE pair of declarations is order-dependent at all:
//     GET /api/v1/alerts/summary ahead of GET /api/v1/alerts/:id, declared
//     adjacently in registerAlertEndpoints. Swap those two lines and the
//     summary endpoint stops existing, silently. Nothing said so before
//     this file.
//   - buildRegistry's "AFTER registerPBSEndpoints" comment does not
//     currently constrain anything. Every route registerPBSEndpoints
//     declares is /pbs-servers plus at most one segment; every
//     registerBackupEndpoints route under that prefix is three segments or
//     more, so neither block can capture the other at any width. Running
//     the two in either order produces zero findings. The comment reads as
//     a routing constraint and is really a statement of provenance — that
//     the pair keeps the positions it had in router.go. That may become a
//     real constraint the day a two-segment backup route is added, which
//     is exactly when this guard starts earning its place.

// registryOrderExemptions lists shadowed pairs that are deliberate, keyed
// "METHOD earlier-path -> METHOD later-path" (exactly as the finding is
// worded) and carrying the reason the unreachable route is harmless.
//
// It is EMPTY, and that is the current state of the registry rather than a
// placeholder: every declared route is reachable today. The mechanism
// exists anyway, and is exercised by
// TestRegistryShadowedRoutes_AnExemptionSuppressesTheFinding, because the
// alternative when someone hits a deliberate shadow is that they delete
// the guard instead of recording the decision.
//
// An entry here is a claim that nobody needs to reach the later route. If
// that is true the route should usually be deleted instead; if it is not
// true, reordering the two declarations is the fix. The same reasoning
// publicRoutes and selfServiceRoutes carry applies: an exemption list
// whose entries do not each state a reason stops being read and becomes a
// place to make failures go away.
var registryOrderExemptions = map[string]string{}

// registryShadowedRoutes reports, for endpoints in REGISTRATION ORDER,
// every pair where an earlier declaration matches everything a later one
// matches — so the later route is mounted but unreachable.
//
// The comparison is pathIsCapturedBy, borrowed whole from
// registry_shadow_guard_test.go rather than re-derived. Two independent
// models of Fiber's matching would be two things to keep correct, and
// their failure mode is silent: one of them stops agreeing with the router
// and nobody finds out, because a guard that has stopped catching things
// looks exactly like a codebase with nothing to catch.
//
// It does NOT model Fiber's `*` or `+` wildcards in the EARLIER path,
// which would capture far more than a `:param` segment does. That is safe
// only because checkPathParams (registry.go) refuses both characters at
// registration, so a declared path cannot contain one — the two guards are
// load-bearing together. Relax that refusal and this comparison starts
// under-reporting, silently, which is the failure mode described above.
//
// The pair check is symmetric in shape but not in outcome, which is the
// point. A literal declared BEFORE a parameterised route that would also
// match it is correct and common — GET /api/v1/alerts/summary sits ahead of
// GET /api/v1/alerts/:id for precisely this reason — so only the
// earlier-captures-later direction is a finding.
//
// Both same-name and different-name parameter collisions are covered,
// because segmentCaptures treats any ":x" as matching any single segment.
// The different-name case is worth stating: Register's duplicate check
// keys on "METHOD path" with the parameter name INCLUDED, so
// "GET /api/v1/x/:a" and "GET /api/v1/x/:b" are two distinct keys and both
// register without complaint. Fiber then serves every request from the
// first. Nothing else in the package notices.
//
// The exemption map is a parameter rather than a package reference so a
// synthetic case can prove the suppression works.
func registryShadowedRoutes(eps []Endpoint, exempt map[string]string) (findings []string, compared int) {
	for i, earlier := range eps {
		for j := i + 1; j < len(eps); j++ {
			later := eps[j]
			if !strings.EqualFold(earlier.Method, later.Method) {
				continue
			}
			compared++

			// normalizeRoutePath for the same reason
			// registryLegacyRouteConflicts uses it: StrictRouting is
			// unset, so a trailing slash must not desync the segment
			// counts this comparison is built on.
			earlierPath := normalizeRoutePath(earlier.Path)
			laterPath := normalizeRoutePath(later.Path)
			if !pathIsCapturedBy(earlierPath, laterPath) {
				continue
			}

			key := fmt.Sprintf("%s %s -> %s %s",
				earlier.Method, earlierPath, later.Method, laterPath)
			if _, ok := exempt[key]; ok {
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"%s is UNREACHABLE: %s %s is declared earlier (position %d vs %d) and matches "+
					"every request it would match, so Fiber — which stops at the first matching "+
					"route — always serves the earlier handler. Move the later declaration ahead "+
					"of the earlier one (or, if the shadowing is deliberate, add %q to "+
					"registryOrderExemptions with the reason).",
				key, earlier.Method, earlierPath, i, j, key))
		}
	}
	sort.Strings(findings)
	return findings, compared
}

// TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne is the production
// guard, over every endpoint buildRegistry declares, in the order it
// declares them.
//
// It reads Registry.Endpoints() rather than app.GetRoutes() deliberately.
// Endpoints() is documented to return declarations in registration order
// and mountRegistry walks that same slice; GetRoutes returns Fiber's own
// table, whose ordering across methods is an implementation detail this
// guard has no reason to depend on.
func TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()
	if len(eps) == 0 {
		t.Fatal("the stub server declared no endpoints; the guard would pass vacuously")
	}

	findings, compared := registryShadowedRoutes(eps, registryOrderExemptions)

	// Anti-vacuity. len(eps) alone does not prove the cross-product ran:
	// a same-method filter that never matched, or an inner loop that
	// started at the wrong index, would leave this at zero while the test
	// still reported success over 535 endpoints.
	if compared == 0 {
		t.Fatalf("declared %d endpoints but compared 0 same-method pairs; the guard would pass vacuously",
			len(eps))
	}

	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne_RejectsAnInjectedShadow
// is the guard above run over the REAL 535 declarations with one extra
// route spliced in at the end — a literal that an already-declared
// parameterised route captures.
//
// It exists because the registry is clean, and a clean guard is exactly
// where vacuity hides: a detector that had stopped comparing anything
// would report zero findings over 535 endpoints and look identical to
// this one. Running the production function over the production input
// plus a single known-bad declaration is what tells the two apart.
//
// It also pins the baseline. If the real registry ever grows a shadow of
// its own, the count here moves and this test says so alongside the
// production one, rather than quietly absorbing it.
func TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne_RejectsAnInjectedShadow(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()

	baseline, compared := registryShadowedRoutes(eps, registryOrderExemptions)
	if compared == 0 {
		t.Fatal("compared no pairs over the real registry")
	}
	if len(baseline) != 0 {
		t.Fatalf("the real registry already has %d shadowed route(s) (%v); this test measures "+
			"the delta from a clean baseline, so fix those first",
			len(baseline), baseline)
	}

	// GET /api/v1/clusters/:cluster_id is declared in
	// registry_clusters.go, so a literal sibling appended here is
	// registered after it and can never be reached.
	const injected = "/api/v1/clusters/archived"
	mutated := append(eps, orderProbeEndpoints(injected)...)

	findings, _ := registryShadowedRoutes(mutated, registryOrderExemptions)
	if len(findings) != 1 {
		t.Fatalf("injecting %s produced %d findings (%v), want exactly 1 — the detector is "+
			"either blind to the shadow or reporting unrelated pairs", injected, len(findings), findings)
	}
	if !strings.Contains(findings[0], injected) || !strings.Contains(findings[0], "UNREACHABLE") {
		t.Errorf("finding = %q, want it to report %s as unreachable", findings[0], injected)
	}
}

// TestFiberServesTheFirstRegisteredMatch is the premise everything above
// rests on, demonstrated instead of assumed.
//
// The claim "an earlier parameterised route makes a later literal
// unreachable" is a statement about Fiber v3's router, not about Nexara,
// and a guard built on a wrong belief about it would either fire on
// healthy routes or — worse — sit quiet over a real one. So this mounts
// two endpoints through the production mountRegistry path, in both orders,
// and looks at which handler actually ran.
//
// Both orders matter. The shadowed order proves the hazard is real; the
// safe order proves Fiber is not simply preferring literals regardless of
// registration, which would make this whole file pointless.
func TestFiberServesTheFirstRegisteredMatch(t *testing.T) {
	paramEndpoint := func(c *capture) Endpoint {
		return registryProbeEndpoint("/api/v1/probe/:id",
			Permissions{Public: "synthetic route for the ordering guard test"}, c.handler(),
			apischema.Properties{"id": {Type: apischema.String}})
	}
	literalEndpoint := func(c *capture) Endpoint {
		return registryProbeEndpointNoParams("/api/v1/probe/summary",
			Permissions{Public: "synthetic route for the ordering guard test"}, c.handler())
	}

	t.Run("parameterised first shadows the literal", func(t *testing.T) {
		var param, literal capture
		app := newRegistryApp(t, noAuth(), paramEndpoint(&param), literalEndpoint(&literal))

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/probe/summary", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		_ = resp.Body.Close()

		if literal.called {
			t.Error("GET /api/v1/probe/summary reached its own literal handler even though " +
				"/api/v1/probe/:id was registered first — Fiber does not match in registration " +
				"order after all, and registryShadowedRoutes is built on a false premise")
		}
		if !param.called {
			t.Fatal("neither handler ran; the probe request did not reach the router at all, so " +
				"this test proves nothing about matching order")
		}
	})

	t.Run("literal first keeps both reachable", func(t *testing.T) {
		var param, literal capture
		app := newRegistryApp(t, noAuth(), literalEndpoint(&literal), paramEndpoint(&param))

		for _, path := range []string{"/api/v1/probe/summary", "/api/v1/probe/abc123"} {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			if err != nil {
				t.Fatalf("app.Test %s: %v", path, err)
			}
			_ = resp.Body.Close()
		}
		if !literal.called {
			t.Error("the literal route did not serve its own path even when registered first")
		}
		if !param.called {
			t.Error("the parameterised route did not serve /api/v1/probe/abc123; registering the " +
				"literal first is supposed to shadow nothing")
		}
	})
}

// --- bite-proofs --------------------------------------------------------

// TestRegistryShadowedRoutes_CatchesAParameterBeforeALiteral is the
// mutation the production guard exists for, run against the real
// detector: two endpoints declared in the wrong order.
func TestRegistryShadowedRoutes_CatchesAParameterBeforeALiteral(t *testing.T) {
	t.Parallel()

	eps := orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary")
	findings, compared := registryShadowedRoutes(eps, nil)

	if compared == 0 {
		t.Fatal("compared no pairs")
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly 1", findings)
	}
	for _, want := range []string{"UNREACHABLE", "/api/v1/alerts/summary", "/api/v1/alerts/:id"} {
		if !strings.Contains(findings[0], want) {
			t.Errorf("finding = %q, want it to mention %q", findings[0], want)
		}
	}
}

// TestRegistryShadowedRoutes_LiteralBeforeParameterIsFine is the negative
// the guard would be useless without: the CORRECT order must not be
// reported. A check that fired on both orders would have to be disabled on
// its first run, and /api/v1/alerts/summary ahead of /api/v1/alerts/:id is
// a real pair in the registry today.
func TestRegistryShadowedRoutes_LiteralBeforeParameterIsFine(t *testing.T) {
	t.Parallel()

	eps := orderProbeEndpoints("/api/v1/alerts/summary", "/api/v1/alerts/:id")
	if findings, _ := registryShadowedRoutes(eps, nil); len(findings) != 0 {
		t.Errorf("findings = %v, want none — a literal declared first shadows nothing", findings)
	}
}

// TestRegistryShadowedRoutes_CatchesAParameterShadowingAnotherParameter
// covers the hole Register's duplicate check leaves open: it keys on the
// path INCLUDING the parameter name, so two spellings of the same shape
// both register and the second is dead.
func TestRegistryShadowedRoutes_CatchesAParameterShadowingAnotherParameter(t *testing.T) {
	t.Parallel()

	// Register really does accept both — assert that rather than assuming
	// it, because if it ever started refusing them this finding would
	// become unreachable and the case below would be dead weight.
	reg := NewRegistry()
	for _, path := range []string{"/api/v1/probe/:id", "/api/v1/probe/:probe_id"} {
		if err := reg.register(registryProbeEndpoint(path,
			Permissions{Public: "synthetic route for the ordering guard test"}, noopParamsHandler,
			apischema.Properties{strings.TrimPrefix(pathSegments(path)[3], ":"): {Type: apischema.String}},
		)); err != nil {
			t.Fatalf("Register refused %s: %v", path, err)
		}
	}

	findings, _ := registryShadowedRoutes(reg.Endpoints(), nil)
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly 1 — two parameter names for one shape are two "+
			"registrations and one reachable route", findings)
	}
	if !strings.Contains(findings[0], "/api/v1/probe/:probe_id") {
		t.Errorf("finding = %q, want the second spelling named as the unreachable one", findings[0])
	}
}

// TestRegistryShadowedRoutes_DifferentMethodOrWidthIsNotAFinding pins the
// two ways a pair can look alike and not collide. Without these the
// detector could be made to "pass" by reporting everything, which is the
// same failure as reporting nothing.
func TestRegistryShadowedRoutes_DifferentMethodOrWidthIsNotAFinding(t *testing.T) {
	t.Parallel()

	t.Run("different method", func(t *testing.T) {
		t.Parallel()
		eps := orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary")
		eps[1].Method = fiber.MethodPost
		if findings, _ := registryShadowedRoutes(eps, nil); len(findings) != 0 {
			t.Errorf("findings = %v, want none — Fiber matches per method", findings)
		}
	})

	t.Run("different segment count", func(t *testing.T) {
		t.Parallel()
		eps := orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary/counts")
		if findings, _ := registryShadowedRoutes(eps, nil); len(findings) != 0 {
			t.Errorf("findings = %v, want none — a one-segment parameter cannot match two segments", findings)
		}
	})
}

// TestRegistryShadowedRoutes_AnExemptionSuppressesTheFinding exercises the
// escape hatch registryOrderExemptions provides. The production map is
// empty, so without this the mechanism would ship untested — and an
// untested escape hatch discovered mid-incident is one that gets used
// wrongly or bypassed entirely.
func TestRegistryShadowedRoutes_AnExemptionSuppressesTheFinding(t *testing.T) {
	t.Parallel()

	eps := orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary")
	key := "GET /api/v1/alerts/:id -> GET /api/v1/alerts/summary"

	if findings, _ := registryShadowedRoutes(eps, nil); len(findings) != 1 {
		t.Fatalf("without an exemption: findings = %v, want exactly 1", findings)
	}
	findings, _ := registryShadowedRoutes(eps, map[string]string{key: "deliberate, for the test"})
	if len(findings) != 0 {
		t.Errorf("with the exemption: findings = %v, want none", findings)
	}

	// The key has to be the one the failure message prints, or an
	// operator copying it out of a failing run would write an entry that
	// silently suppresses nothing.
	unexempted, _ := registryShadowedRoutes(eps, nil)
	if !strings.Contains(unexempted[0], key) {
		t.Errorf("finding = %q does not contain the exemption key %q; the key an operator would "+
			"copy from the failure must be the key the map is read with", unexempted[0], key)
	}
}

// TestRegistryOrderExemptionsAllCarryAReason keeps the list from rotting
// into a set of bare paths. It passes trivially while the map is empty and
// starts mattering the moment it is not.
func TestRegistryOrderExemptionsAllCarryAReason(t *testing.T) {
	t.Parallel()

	for key, reason := range registryOrderExemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("registryOrderExemptions[%q] has no reason; state why the later route being "+
				"unreachable is acceptable, or reorder the declarations", key)
		}
	}
}

// orderProbeEndpoints builds GET probe endpoints for the given paths, in
// order, declaring a string parameter for every :param segment. Nothing
// registers them — registryShadowedRoutes reads only Method and Path — so
// the parameters keep each probe a complete declaration rather than
// satisfying any check.
func orderProbeEndpoints(paths ...string) []Endpoint {
	out := make([]Endpoint, 0, len(paths))
	for _, path := range paths {
		params := apischema.Properties{}
		for _, name := range pathParamNames(path) {
			params[name] = apischema.Property{Type: apischema.String}
		}
		out = append(out, registryProbeEndpoint(path,
			Permissions{Public: "synthetic route for the ordering guard test"},
			noopParamsHandler, params))
	}
	return out
}
