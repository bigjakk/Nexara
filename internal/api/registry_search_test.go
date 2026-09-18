package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// searchRouteCount is how many endpoints registerSearchEndpoints declares.
const searchRouteCount = 1

// searchRoutesOutsideTheClusterCheckShape records why the one search route is
// not a plain cluster-scoped Check. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var searchRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/search": "Advisory: the search spans every cluster, so there is no single cluster a gate " +
		"could resolve; accessibleClusters(\"view\", \"cluster\") builds the scope and PermitsCluster " +
		"filters every candidate row",
}

// TestSearchDeclaresTheFilterItApplies is the tally for this one-route domain.
//
// The handler never held a require*Perm call at all — it opened with
// accessibleClusters (`git show HEAD:internal/api/handlers/search.go`, commit
// eaeafa7), which is a permissionLeaves entry and therefore satisfied the old
// call-graph guard without ever gating anything. Advisory is the shape that
// says so out loud, and registryEnforcementGaps still holds the handler to
// reaching that leaf.
func TestSearchDeclaresTheFilterItApplies(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, pathPrefix+"search")

	if e.Permissions.Advisory == nil {
		t.Fatalf("GET /api/v1/search declares %q, want Advisory — nothing gates this route; the handler "+
			"filters every row it considers", e.Permissions.Describe())
	}
	if got := e.Permissions.Advisory.String(); got != "view:cluster" {
		t.Errorf("the advisory permission is %q, want view:cluster — the pair accessibleClusters is called with", got)
	}
	if e.Permissions.Advisory.Scope != ScopeCluster {
		t.Errorf("the advisory permission is %s-scoped; the filter is applied per cluster",
			e.Permissions.Advisory.Scope)
	}
	if !strings.Contains(e.Permissions.Advisory.Reason, "accessibleClusters") {
		t.Errorf("the Advisory reason does not name what does the filtering: %q", e.Permissions.Advisory.Reason)
	}
	if !strings.Contains(e.Permissions.Advisory.Reason, "PermitsCluster") {
		t.Errorf("the Advisory reason does not name the per-row check: %q", e.Permissions.Advisory.Reason)
	}
}

// TestSearchTermIsBoundedAndOptional pins the one parameter, including the
// spelling the SPA sends when the box is empty.
func TestSearchTermIsBoundedAndOptional(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, pathPrefix+"search")
	prop := e.Parameters["q"]
	if !prop.Optional {
		t.Error("q is required; the SPA fetches this route before the box has anything in it")
	}
	if prop.MaxLength == nil {
		t.Error("q declares no maximum length; the term is compared against every row in the inventory")
	}

	for _, tt := range []struct {
		query string
		want  int
	}{
		{"", fiber.StatusNoContent},
		{"?q=", fiber.StatusNoContent},
		{"?q=pve", fiber.StatusNoContent},
		{"?q=" + strings.Repeat("a", 257), fiber.StatusBadRequest},
		{"?query=pve", fiber.StatusBadRequest},
	} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, pathPrefix+"search"+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
		}
	}
}

// TestSearchEndpointIsDocumented holds the declaration to the standard that
// makes this whole effort worth doing.
func TestSearchEndpointIsDocumented(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, pathPrefix+"search")
	if err := e.Parameters.Compile(); err != nil {
		t.Errorf("GET /api/v1/search: %v", err)
	}
	if strings.TrimSpace(e.Description) == "" {
		t.Error("GET /api/v1/search has no description")
	}
	if e.Group != "Settings" {
		t.Errorf("GET /api/v1/search is in group %q, want Settings — the section endpointMeta already filed it under", e.Group)
	}
	for name, prop := range e.Parameters {
		if strings.TrimSpace(prop.Description) == "" {
			t.Errorf("parameter %q has no description", name)
		}
	}
}
