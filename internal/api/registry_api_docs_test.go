package api

import (
	"testing"
)

// TestAPIDocsIsStillLegacy records the decision that GET /api/v1/api-docs, the endpoint this whole
// effort exists to improve, is the one route the registry cannot declare, and pins it as a decision rather
// than an oversight. It is in instanceSharedRoutes ("route catalog derived from the router; no tenant
// data"): authenticated, serving instance-level data identical for every caller, no subject to authorize,
// a shape with no member in the Permissions vocabulary. Check and Alternatives would install a gate it has
// never had; Deferred and Advisory require the handler to reach a permission leaf (registryEnforcementGaps)
// and GetDocs reaches none; Public would drop authentication it does have; SelfService is nearest and must
// not be used (it would make selfServiceRoutes' invariant, "acts solely on the caller's own identity",
// false). The same gap holds the three branding reads back (TestSettingsReadsAreStillLegacy). Pinned from
// both sides: still registered with its exemption, and NOT in the registry.
func TestAPIDocsIsStillLegacy(t *testing.T) {
	const key = "GET /api/v1/api-docs"

	s := sharedRouteStub(t)
	if registryRouteKeySet(s.registry.Endpoints())[key] {
		t.Error("the API docs route is declared in the registry, but Permissions has no shape for an " +
			"authenticated route with no subject to authorize — see this test's doc comment")
	}

	registered := false
	for _, r := range s.app.GetRoutes(true) {
		if r.Method+" "+normalizeRoutePath(r.Path) == key {
			registered = true
		}
	}
	if !registered {
		t.Error("the API docs route is not registered at all — it is meant to stay in router.go, not to disappear")
	}
	if !legacyRouteBaseline[key] {
		t.Error("the API docs route is not in legacyRouteBaseline; the ratchet would flag it as a new legacy route")
	}

	reason, shared := instanceSharedRoutes[key]
	if !shared {
		t.Fatal("the API docs route is not in instanceSharedRoutes; it is authenticated and gates on nothing")
	}
	// Carried verbatim rather than reworded: it is the reviewed sentence, and
	// TestGuard_ExemptionKeysMatchRegisteredRoutes only checks that SOME reason
	// is there.
	if reason != "route catalog derived from the router; no tenant data" {
		t.Errorf("the exemption reason is %q; it is the reviewed sentence and is kept verbatim", reason)
	}
	if _, self := selfServiceRoutes[key]; self {
		t.Error("the API docs route is in selfServiceRoutes, which claims it acts on the caller's own " +
			"identity — it serves the same catalog to everyone")
	}
}
