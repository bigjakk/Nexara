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

// Registration ORDER decides which route Fiber serves: a literal declared after a
// :param route that also matches it is mounted and unreachable, and nothing 500s or logs.
// Today exactly one pair is order-dependent, GET /alerts/summary ahead of GET
// /alerts/:id. buildRegistry's "AFTER registerPBSEndpoints" comment constrains nothing
// yet (no two-segment backup route exists), so it is provenance rather than a rule.
// registry_shadow_guard_test.go owns the routing model this file reuses
// (pathIsCapturedBy) and the registry-against-legacy case.

// registryOrderExemptions lists deliberate shadowed pairs, keyed "METHOD earlier-path ->
// METHOD later-path" as the finding words it, each with the reason the unreachable route
// is harmless. It is empty because every declared route is reachable today; the mechanism
// exists so a deliberate shadow is recorded rather than the guard deleted.
var registryOrderExemptions = map[string]string{}

// registryShadowedRoutes reports, for endpoints in REGISTRATION ORDER, every pair where
// an earlier declaration matches everything a later one matches, so the later route is
// mounted but unreachable. Only earlier-captures-later is a finding: a literal declared
// before the parameterised route that would match it is correct. The comparison is
// pathIsCapturedBy, borrowed whole from registry_shadow_guard_test.go; it models no `*` or
// `+` in the EARLIER path, which checkPathParams refuses at registration, so the two are
// load-bearing together. Parameter names do not matter ("/x/:a" and "/x/:b" are two
// Register keys and one reachable route). exempt is a parameter so a synthetic case can
// prove the suppression.
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

// TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne is the production guard, over every
// endpoint buildRegistry declares in declaration order (Endpoints(), which mountRegistry
// walks; Fiber's own table orders across methods by implementation detail).
func TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne(t *testing.T) {
	eps := sharedRouteStub(t).registry.Endpoints()
	if len(eps) == 0 {
		t.Fatal("the stub server declared no endpoints; the guard would pass vacuously")
	}

	findings, compared := registryShadowedRoutes(eps, registryOrderExemptions)

	// len(eps) alone does not prove the cross-product ran.
	if compared == 0 {
		t.Fatalf("declared %d endpoints but compared 0 same-method pairs; the guard would pass vacuously",
			len(eps))
	}

	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne_RejectsAnInjectedShadow runs the
// guard over the REAL declarations, which are clean and so cannot tell a working detector
// from one that stopped comparing, with a fault spliced in, and the correct neighbours
// the detector must leave alone. Rows: a literal appended after GET /clusters/:cluster_id;
// one parameter name shadowing another (Register accepts both, so only this notices); an
// exemption suppressing a finding by the key the finding prints; and the three shapes
// that look alike and do not collide.
func TestGuard_NoDeclaredRouteIsShadowedByAnEarlierOne_RejectsAnInjectedShadow(t *testing.T) {
	eps := sharedRouteStub(t).registry.Endpoints()
	baseline, compared := registryShadowedRoutes(eps, registryOrderExemptions)
	if compared == 0 || len(baseline) != 0 {
		t.Fatalf("the real registry must be clean and non-empty: compared %d pairs, findings %v", compared, baseline)
	}

	reg := NewRegistry()
	for _, path := range []string{"/api/v1/probe/:id", "/api/v1/probe/:probe_id"} {
		if err := reg.register(registryProbeEndpoint(path, Permissions{Public: "synthetic route for the ordering guard test"},
			noopParamsHandler, apischema.Properties{strings.TrimPrefix(pathSegments(path)[3], ":"): {Type: apischema.String}})); err != nil {
			t.Fatalf("Register refused %s, so the two-spellings case is unreachable: %v", path, err)
		}
	}
	otherMethod := orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary")
	otherMethod[1].Method = fiber.MethodPost
	const exemptKey = "GET /api/v1/alerts/:id -> GET /api/v1/alerts/summary"

	for _, tt := range []struct {
		name    string
		eps     []Endpoint
		exempt  map[string]string
		want    []string // what the one finding carries; nil: no finding
		because string
	}{
		{"a literal after the parameterised route", append(eps, orderProbeEndpoints("/api/v1/clusters/archived")...),
			registryOrderExemptions, []string{"UNREACHABLE", "/api/v1/clusters/archived"}, ""},
		{"a parameter shadowing another parameter", reg.Endpoints(), nil,
			[]string{"UNREACHABLE", "/api/v1/probe/:probe_id"}, ""},
		{"the finding's own key exempts it", orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary"),
			map[string]string{exemptKey: "deliberate, for the test"}, nil, "an exemption suppresses its finding"},
		{"a literal before the parameter", orderProbeEndpoints("/api/v1/alerts/summary", "/api/v1/alerts/:id"), nil, nil,
			"a literal declared first shadows nothing"},
		{"another method", otherMethod, nil, nil, "Fiber matches per method"},
		{"another width", orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary/counts"), nil, nil,
			"a one-segment parameter cannot match two segments"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			findings, _ := registryShadowedRoutes(tt.eps, tt.exempt)
			if tt.want == nil {
				if len(findings) != 0 {
					t.Fatalf("findings %v, want none: %s", findings, tt.because)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("findings %v, want exactly 1", findings)
			}
			for _, w := range tt.want {
				if !strings.Contains(findings[0], w) {
					t.Errorf("finding %q does not carry %q", findings[0], w)
				}
			}
		})
	}

	// The key an operator copies out of a failing run must be the key the map is read with.
	unexempted, _ := registryShadowedRoutes(orderProbeEndpoints("/api/v1/alerts/:id", "/api/v1/alerts/summary"), nil)
	if len(unexempted) != 1 || !strings.Contains(unexempted[0], exemptKey) {
		t.Errorf("findings %v do not print the exemption key %q", unexempted, exemptKey)
	}
}

// TestFiberServesTheFirstRegisteredMatch is the premise the guard rests on, shown instead
// of assumed: through the production mountRegistry path an earlier :param route answers
// a later literal's path, and with the literal first both are reachable (so Fiber is not
// simply preferring literals).
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

// TestRegistryOrderExemptionsAllCarryAReason keeps the list from becoming bare paths; it
// is vacuous while the map is empty.
func TestRegistryOrderExemptionsAllCarryAReason(t *testing.T) {
	t.Parallel()

	for key, reason := range registryOrderExemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("registryOrderExemptions[%q] has no reason; state why the later route being "+
				"unreachable is acceptable, or reorder the declarations", key)
		}
	}
}

// orderProbeEndpoints builds GET probe endpoints for paths, in order, declaring a string
// parameter for every :param segment. registryShadowedRoutes reads only Method and Path.
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
