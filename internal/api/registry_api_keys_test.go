package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// apiKeyRouteCount is how many endpoints registerAPIKeyEndpoints declares. It
// is ALL 6 of APIKeyHandler's routes; nothing in this domain was left legacy.
// See vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants.
const apiKeyRouteCount = 6

const testAPIKeyID = "d4e5f6a7-0000-4000-8000-000000000055"

// apiKeyLegacyPermissions is what each handler checked with a hand-placed
// requirePerm call BEFORE this migration, transcribed from
// `git show HEAD:internal/api/handlers/api_keys.go` at commit 357be6f.
//
// All six made exactly one static, unconditional call, so all six hoist: 6 in,
// 6 out, none kept. Create's extra refusal — an API-key-authenticated caller
// may not mint more keys — is NOT a permission and stays in the handler.
var apiKeyLegacyPermissions = map[string]string{
	"POST /api/v1/api-keys":             "manage:api_key",
	"GET /api/v1/api-keys":              "manage:api_key",
	"DELETE /api/v1/api-keys":           "manage:api_key",
	"DELETE /api/v1/api-keys/:id":       "manage:api_key",
	"GET /api/v1/admin/api-keys":        "manage:user",
	"DELETE /api/v1/admin/api-keys/:id": "manage:user",
}

// apiKeyRoutesOutsideTheClusterCheckShape is every route in this domain: an API
// key belongs to an ACCOUNT, and no path here names a cluster.
var apiKeyRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/api-keys":             "an API key belongs to a Nexara account; the path names no cluster",
	"GET /api/v1/api-keys":              "an API key belongs to a Nexara account; the path names no cluster",
	"DELETE /api/v1/api-keys":           "an API key belongs to a Nexara account; the path names no cluster",
	"DELETE /api/v1/api-keys/:id":       "an API key belongs to a Nexara account; the path names no cluster",
	"GET /api/v1/admin/api-keys":        "the instance-wide admin view; the path names no cluster",
	"DELETE /api/v1/admin/api-keys/:id": "the instance-wide admin view; the path names no cluster",
}

// declaredAPIKeyEndpoints returns every declaration in this domain, keyed
// "METHOD path".
func declaredAPIKeyEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, apiKeyScope) || strings.HasPrefix(e.Path, adminAPIKeyScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestAPIKeyRoutesDeclareTheSamePermissionTheyEnforced is the tally: 6
// hand-placed calls in, 6 declared global Checks out, none kept.
//
// This is also, since the endpointMeta cleanup below, the ONLY place that
// checks a declared route's permission against apiKeyLegacyPermissions —
// see the retirement note where TestAPIKeyDocsPromiseWhatTheRoutesEnforce
// used to be for why, and why nothing was lost when that test went.
func TestAPIKeyRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredAPIKeyEndpoints(t)
	if len(declared) != apiKeyRouteCount {
		t.Fatalf("the registry declares %d API key routes, want %d", len(declared), apiKeyRouteCount)
	}
	if len(apiKeyLegacyPermissions) != apiKeyRouteCount {
		t.Fatalf("apiKeyLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(apiKeyLegacyPermissions), apiKeyRouteCount)
	}

	hoisted := 0
	for key, want := range apiKeyLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — it made one static, unconditional requirePerm call",
				key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeGlobal)
		}
	}
	if hoisted != apiKeyRouteCount {
		t.Errorf("the tally moves %d permission call(s) into middleware, want all %d", hoisted, apiKeyRouteCount)
	}

	for key := range declared {
		if _, listed := apiKeyLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in apiKeyLegacyPermissions — a new API key route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestAPIKeyAdminRoutesNeedADifferentGrant is the split that matters in this
// domain, asserted on its own rather than only inside the tally.
//
// manage:api_key lets a caller mint and revoke THEIR OWN keys. The two admin
// routes enumerate and revoke EVERYONE's, so they take manage:user — and a
// caller holding manage:api_key alone must be refused by them. Getting that
// backwards would turn the grant every API user needs into the grant that
// exposes every API user's keys.
func TestAPIKeyAdminRoutesNeedADifferentGrant(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{fiber.MethodGet, adminAPIKeyScope},
		{fiber.MethodDelete, adminAPIKeyScope + "/:id"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(route.path, ":id", testAPIKeyID)

			app := newRegistryApp(t, stubAuth(map[string]bool{"manage:api_key": true}), gated)
			status, _ := send(t, app, authedRequest(route.method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding only manage:api_key got %d, want 403 — that grant is for the "+
					"caller's OWN keys", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller holding only manage:api_key")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{"manage:user": true}), gated)
			status, _ = send(t, app, authedRequest(route.method, target))
			if status == fiber.StatusForbidden {
				t.Fatal("a caller holding manage:user got 403")
			}
		})
	}
}

// TestAPIKeySelfServiceRoutesAreGated is the assertion that the four
// "self-service" key routes are gated at all.
//
// They were listed in selfServiceRoutes before this migration even though every
// one of them called requirePerm(c, "manage", "api_key") — so the guards that
// would have noticed a missing gate were skipping them. This is the direct
// check that they are not open to every authenticated caller.
func TestAPIKeySelfServiceRoutesAreGated(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{fiber.MethodPost, apiKeyScope},
		{fiber.MethodGet, apiKeyScope},
		{fiber.MethodDelete, apiKeyScope},
		{fiber.MethodDelete, apiKeyScope + "/:id"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(route.path, ":id", testAPIKeyID)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:cluster": true}), gated)
			status, _ := send(t, app, authedRequest(route.method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("an authenticated caller holding no API key grant got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without manage:api_key")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{"manage:api_key": true}), gated)
			status, _ = send(t, app, httptest.NewRequest(route.method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Fatalf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// TestAPIKeyRoutesLeftTheSelfServiceExemption is the other half of that fix:
// none of these routes may be listed in selfServiceRoutes any more, because a
// listed route is skipped by TestGuard_EveryRouteEnforcesPermission and by
// TestGuard_DocumentedPermissionMatchesEnforcement before either looks at it.
func TestAPIKeyRoutesLeftTheSelfServiceExemption(t *testing.T) {
	for key := range apiKeyLegacyPermissions {
		if why, listed := selfServiceRoutes[key]; listed {
			t.Errorf("%s is still listed in selfServiceRoutes (%q), but it declares a real Check — the "+
				"exemption makes the guards skip a route that is gated", key, why)
		}
	}
}

// TestAPIKeyDocsPromiseWhatTheRoutesEnforce (RETIRED) used to compare
// endpointMeta's curated permission for each of these 6 routes against
// apiKeyLegacyPermissions, with an explicit "compared != apiKeyRouteCount"
// check specifically so it would fail loudly — rather than pass vacuously —
// the moment those curated entries went away.
//
// They did go away, deliberately: internal/api/handlers/api_docs.go's
// GetDocs renders a registry-declared route from its DECLARATION and never
// reads endpointMeta for it at all, so this test's own premise ("endpointMeta's
// curated permission is what an operator reads when building a role") had
// already stopped being true for these 6 routes specifically — the overlay
// text for them was dead weight nobody could see, same as the ~217 other
// migrated-route entries the same cleanup removed. Keeping this test alive
// by keeping those 6 entries alive would have meant preserving dead
// production data — confirmed dead by a one-off manual check at the time
// of that cleanup (not a test in this repo a reader can re-run): the
// rendered /api/v1/api-docs payload was byte-identical, same SHA-256,
// with and without all 223 removed entries — purely to keep this guard's
// input non-empty.
//
// The coverage did not evaporate: TestAPIKeyRoutesDeclareTheSamePermissionTheyEnforced
// above makes the SAME comparison against apiKeyLegacyPermissions, except
// through e.Permissions.Describe() on the live DECLARATION — the thing GetDocs
// actually renders — rather than through the dead overlay. Its own
// `hoisted != apiKeyRouteCount` check at the end is that test's equivalent
// anti-vacuity guard, over the value that matters now.

// probeAPIKeyEndpoint is a declared API key endpoint with its handler swapped
// for a capture and its gate removed, so a parameter test needs neither a
// database nor a session.
func probeAPIKeyEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestAPIKeyExpiryBounds pins the three things about expires_in that the old
// *int64 encoded, plus the one that it got wrong.
//
//   - Absent means "never expires", so there must be no Default: with one, every
//     request would look as though it had asked for a fixed lifetime.
//   - Below an hour is refused, which is the handler's own rule moved out.
//   - Above the cap is refused, which is NEW. The value is multiplied into a
//     time.Duration — int64 nanoseconds — and a caller asking for 1e18 seconds
//     overflowed it into a NEGATIVE duration, producing a key whose expiry was
//     already in the past the moment it was handed over. That is a key that
//     silently does not work, not a rejected request.
func TestAPIKeyExpiryBounds(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, apiKeyScope)
	prop := e.Parameters["expires_in"]
	if !prop.Optional {
		t.Error("expires_in is required; omitting it has always meant a key that never expires")
	}
	if prop.Default != nil {
		t.Errorf("expires_in declares default %v — that would give every key a fixed lifetime", prop.Default)
	}
	if prop.Minimum == nil || *prop.Minimum != 3600 {
		t.Errorf("expires_in declares minimum %v, want 3600", prop.Minimum)
	}
	if prop.Maximum == nil {
		t.Fatal("expires_in declares no maximum; time.Duration is int64 nanoseconds and overflows past ~9.2e9 seconds")
	}
	// The bound has to be BELOW the wrap, not merely present.
	const durationOverflowSeconds = 9.2e9
	if *prop.Maximum >= durationOverflowSeconds {
		t.Errorf("expires_in declares maximum %v, which is at or past the point time.Duration wraps (%v)",
			*prop.Maximum, durationOverflowSeconds)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAPIKeyEndpoint(t, fiber.MethodPost, apiKeyScope, cap))

	for _, tc := range []struct {
		name, body string
		wantStatus int
	}{
		{"never expires", `{"name":"ci"}`, fiber.StatusNoContent},
		{"an hour", `{"name":"ci","expires_in":3600}`, fiber.StatusNoContent},
		{"under an hour", `{"name":"ci","expires_in":3599}`, fiber.StatusBadRequest},
		{"overflows time.Duration", `{"name":"ci","expires_in":1000000000000000000}`, fiber.StatusBadRequest},
		{"no name", `{}`, fiber.StatusBadRequest},
		{"empty name", `{"name":""}`, fiber.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap.called = false
			status, env := send(t, app, jsonRequest(http.MethodPost, apiKeyScope, tc.body))
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tc.wantStatus)
			}
			if tc.wantStatus == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a request the schema should have refused")
			}
		})
	}

	// And the distinction the *int64 existed for survives.
	cap.called = false
	if _, env := send(t, app, jsonRequest(http.MethodPost, apiKeyScope, `{"name":"ci"}`)); env.Message != "" {
		t.Fatalf("unexpected error: %q", env.Message)
	}
	if _, supplied := cap.params.OptInt("expires_in"); supplied {
		t.Error("expires_in reads as supplied although the request never sent it")
	}
}

// TestAPIKeyIdentifiersAreUUIDsFromThePath keeps the two :id parameters honest.
// :id is one of the two names clusterIDFromParam falls back to (gateParamNames
// in registry.go); resolving it from the PATH is what keeps that fallback from
// ever disagreeing with the handler.
func TestAPIKeyIdentifiersAreUUIDsFromThePath(t *testing.T) {
	for _, path := range []string{apiKeyScope + "/:id", adminAPIKeyScope + "/:id"} {
		e := declaredEndpoint(t, fiber.MethodDelete, path)
		prop, ok := e.Parameters["id"]
		if !ok {
			t.Errorf("%s declares no id parameter", path)
			continue
		}
		if prop.Format != "uuid" {
			t.Errorf("%s: id declares format %q, want uuid", path, prop.Format)
		}
		if prop.Source != apischema.SourcePath {
			t.Errorf("%s: id declares source %q, want %q", path, prop.Source, apischema.SourcePath)
		}
	}
}

// TestEveryAPIKeyEndpointIsDocumented holds the
// declaration-is-the-documentation rule for this domain. The admin pair sits in
// the "User Management" section, matching where endpointMeta already put it.
func TestEveryAPIKeyEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredAPIKeyEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		want := "API Keys"
		if strings.HasPrefix(e.Path, adminAPIKeyScope) {
			want = "User Management"
		}
		if e.Group != want {
			t.Errorf("%s declares group %q, want %q", key, e.Group, want)
		}
	}
}
