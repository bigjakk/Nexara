package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// changelogRouteCount and versionRouteCount are how many endpoints
// registerChangelogEndpoints and registerVersionEndpoint declare.
const (
	changelogRouteCount = 1
	versionRouteCount   = 1
)

// anonymousRoutesOutsideTheClusterCheckShape records why the two routes served
// with no session at all are not cluster-scoped Checks. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var anonymousRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/version":   "Public: the SPA shell reads the build version before a session exists, and the response says nothing about the install",
	"GET /api/v1/changelog": "Public: the project's own published release notes, proxied from the anonymous GitHub feed",
}

// TestVersionAndChangelogReasonsMatchTheReviewedList keeps one exemption to one
// justification: the inline Public reason and publicRoutes' reason must be the
// same sentence, the way TestAuthExemptionReasonsMatchTheReviewedLists already
// requires for the auth domain.
func TestVersionAndChangelogReasonsMatchTheReviewedList(t *testing.T) {
	for _, key := range []string{"GET /api/v1/version", "GET /api/v1/changelog"} {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		if e.Permissions.Public == "" {
			t.Errorf("%s declares %q, want Public — it is served with no session", key, e.Permissions.Describe())
			continue
		}
		reviewed, listed := publicRoutes[key]
		if !listed {
			t.Errorf("%s declares Public but is not in publicRoutes", key)
			continue
		}
		if e.Permissions.Public != reviewed {
			t.Errorf("%s: the declared reason is %q but publicRoutes says %q — one exemption, one justification",
				key, e.Permissions.Public, reviewed)
		}
	}
}

// TestVersionAndChangelogAreServedWithoutASession proves the Public
// declaration end to end rather than only on paper: mountRegistry must attach
// NO authentication middleware, so a request carrying no session reaches the
// handler.
//
// It matters most for /api/v1/version, whose handler is a Server method rather
// than a handlers-package one: routeHandlerKey cannot resolve it, so the
// call-graph guards skip it and this is the check that is left.
func TestVersionAndChangelogAreServedWithoutASession(t *testing.T) {
	for _, path := range []string{pathPrefix + "version", pathPrefix + "changelog"} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodGet, path)
		e.Handler = cap.handler()
		// stubAuth would refuse an unauthenticated request; mounting it and
		// still getting through is what proves the chain skipped it.
		app := newRegistryApp(t, stubAuth(map[string]bool{}), e)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, path, nil))
		if status != fiber.StatusNoContent {
			t.Errorf("%s: status = %d (%q), want 204 — a Public route installs no authentication", path, status, env.Message)
		}
		if !cap.called {
			t.Errorf("%s: the handler did not run for an anonymous request", path)
		}
	}
}

// TestVersionAndChangelogTakeNoParameters pins that both declare an empty
// schema, which is what turns an unexpected query key into a 400 rather than
// silence.
func TestVersionAndChangelogTakeNoParameters(t *testing.T) {
	for _, path := range []string{pathPrefix + "version", pathPrefix + "changelog"} {
		e := declaredEndpoint(t, fiber.MethodGet, path)
		if len(e.Parameters) != 0 {
			t.Errorf("%s declares %d parameters, want none", path, len(e.Parameters))
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", path)
		}
		if e.Group != "Settings" {
			t.Errorf("%s is in group %q, want Settings — the section endpointMeta already filed both under", path, e.Group)
		}

		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		app := newRegistryApp(t, noAuth(), probe)
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, path+"?limit=5", nil))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s?limit=5: status = %d (%q), want 400", path, status, env.Message)
		}
	}
}

// TestHealthzIsStillLegacy records a decision, not a gap.
//
// /healthz is the Server's other hand-registered route and CANNOT be declared:
// Register refuses any path outside /api/v1/ (pathPrefix), and the container
// health check has to answer on a path that is not part of the API surface —
// moving it under /api/v1/ would change what every deployment's probe and
// docker-compose healthcheck point at.
//
// Pinned from both sides: it must still be registered and public, and it must
// NOT be in the registry.
func TestHealthzIsStillLegacy(t *testing.T) {
	const key = "GET /healthz"

	s := newRouteStubServer(t)
	if registryRouteKeySet(s.registry.Endpoints())[key] {
		t.Error("/healthz is declared in the registry, but Register refuses a path outside /api/v1/")
	}
	registered := false
	for _, r := range s.app.GetRoutes(true) {
		if r.Method+" "+normalizeRoutePath(r.Path) == key {
			registered = true
		}
	}
	if !registered {
		t.Error("/healthz is not registered at all — it is meant to stay in router.go, not to disappear")
	}
	if _, public := publicRoutes[key]; !public {
		t.Error("/healthz is not in publicRoutes; the probe runs before the app can authenticate anyone")
	}
}
