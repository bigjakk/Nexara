package api

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// firewallTemplateRouteCount is how many endpoints
// registerFirewallTemplateEndpoints declares. It is FOUR of the domain's
// six: the two writes carry a JSON array of objects that apischema cannot
// describe, and TestFirewallTemplateWritesAreStillLegacy pins that on
// purpose rather than leaving it to be noticed as a gap.
const firewallTemplateRouteCount = 4

const testTemplateID = "7b1c9d2e-4a35-4f60-8c21-000000000006"

// firewallTemplateRoutesOutsideTheClusterCheckShape is this domain's half of
// the registry-wide exception list in registry_vms_test.go.
//
// Three entries, all for the same reason: a firewall template is stored in
// NEXARA's database, not on a cluster, so there is no cluster for a
// cluster-scoped check to resolve. The handlers used requirePerm
// (HasGlobalPermission) rather than requireClusterPerm, and the paths name
// no cluster — Register would refuse a cluster-scoped Check on them.
//
// The fourth route, apply, is NOT here: it writes into one cluster's
// firewall and is cluster-scoped, which is the whole point of the split.
var firewallTemplateRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/firewall-templates": "global: a template belongs to the install rather than to a " +
		"cluster, and the path names none",
	"GET /api/v1/firewall-templates/:id":    "global, for the reason the listing gives",
	"DELETE /api/v1/firewall-templates/:id": "global, for the reason the listing gives",
}

// firewallTemplateLegacyPermissions is what each handler checked with a
// hand-placed call BEFORE Phase 6e, transcribed from
// `git show HEAD:internal/api/handlers/networks.go` at commit eb888b6.
//
// It covers only the FOUR migrated routes. The two that stay legacy kept
// their calls — both requirePerm(c, "manage", "network") — and are asserted
// separately, because a tally entry for a route that is still legacy would
// read as a migration that did not happen.
var firewallTemplateLegacyPermissions = map[string]string{
	"GET /api/v1/firewall-templates":                                 "view:network",
	"GET /api/v1/firewall-templates/:id":                             "view:network",
	"DELETE /api/v1/firewall-templates/:id":                          "delete:network",
	"POST /api/v1/clusters/:cluster_id/firewall-templates/:id/apply": "manage:network",
}

func TestFirewallTemplateRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredNetworkEndpoints(t, firewallTemplateLegacyPermissions)
	if len(declared) != firewallTemplateRouteCount {
		t.Fatalf("the registry declares %d template routes, want %d", len(declared), firewallTemplateRouteCount)
	}

	// The scope differs WITHIN this slice, which is why it cannot use
	// assertNetworkTally: three routes are global and the fourth is
	// cluster-scoped, and getting that backwards is the one mistake here
	// that would change who can do what.
	byPermission := map[string]int{}
	for key, want := range firewallTemplateLegacyPermissions {
		e := declared[key]
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		wantScope := ScopeGlobal
		if strings.HasPrefix(e.Path, clusterScope) {
			wantScope = ScopeCluster
		}
		if e.Permissions.Check.Scope != wantScope {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, wantScope)
		}
		byPermission[want]++
	}
	for perm, calls := range map[string]int{
		"view:network":   2,
		"manage:network": 1,
		"delete:network": 1,
	} {
		if byPermission[perm] != calls {
			t.Errorf("%d template routes declare %s, want %d", byPermission[perm], perm, calls)
		}
	}
}

// TestApplyTemplateIsClusterScopedNotGlobal is the one permission decision
// in this slice that could go wrong quietly.
//
// The other template routes are global because a template belongs to the
// install. Apply is not: it writes rules into ONE cluster's firewall.
// Declaring it global would refuse every operator whose manage:network is
// scoped to that cluster — a global check needs a global grant, and a global
// grant already covers every cluster, so it would admit no one new — and the
// route would still look correct next to its siblings.
func TestApplyTemplateIsClusterScopedNotGlobal(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/firewall-templates/:id/apply")
	if e.Permissions.Check == nil {
		t.Fatalf("apply declares %q rather than a Check", e.Permissions.Describe())
	}
	if e.Permissions.Check.Scope != ScopeCluster {
		t.Fatalf("apply is %s-scoped; it writes into one cluster's firewall", e.Permissions.Check.Scope)
	}
	// :cluster_id must be the FIRST path parameter, which is what
	// namesACluster requires of a cluster-scoped Check — and what stops the
	// gate resolving the TEMPLATE id as a cluster.
	if got := pathParamNames(e.Path); len(got) == 0 || got[0] != "cluster_id" {
		t.Fatalf("apply's path parameters are %v; the cluster must be the first", got)
	}
	// And :id resolves from the PATH, not the body — the gate reads both
	// cluster_id and id, so an id that came from a body could disagree with
	// the one the handler acts on.
	if src := apischema.ResolveSource("id", e.Parameters["id"], e.Method, pathParamNames(e.Path)); src != apischema.SourcePath {
		t.Errorf("apply reads id from %q, want the path", src)
	}
}

// TestFirewallTemplateWritesAreStillLegacy pins the two routes this
// migration deliberately left behind, so that "4 of 6" is an assertion
// rather than a thing a reader has to notice.
//
// Their body carries `rules`, a JSON ARRAY OF OBJECTS, and apischema's
// Property.Items is restricted to scalar element types — compileItems
// refuses an Object element outright. Three halves are asserted: the routes
// are NOT in the registry, they ARE still mounted (a route that vanished
// would be an outage rather than a deferral), and the registry would still
// refuse the declaration they need. The third is what keeps this from
// rotting into a note nobody re-reads: the day apischema grows object items,
// this test fails and the carve-out gets revisited.
func TestFirewallTemplateWritesAreStillLegacy(t *testing.T) {
	s := newRouteStubServer(t)

	legacy := []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, firewallTemplateScope},
		{fiber.MethodPut, firewallTemplateScope + "/:id"},
	}

	for _, want := range legacy {
		for _, e := range s.registry.Endpoints() {
			if e.Method == want.method && e.Path == want.path {
				t.Errorf("%s %s is declared, but its body carries an array of objects that a "+
					"parameter schema cannot describe", want.method, want.path)
			}
		}
		var mounted bool
		for _, r := range s.app.GetRoutes(true) {
			if r.Method == want.method && normalizeRoutePath(r.Path) == want.path {
				mounted = true
			}
		}
		if !mounted {
			t.Errorf("%s %s is neither declared nor mounted; it has been lost, not deferred",
				want.method, want.path)
		}
	}

	// The reason, asserted rather than asserted-about: an array of objects
	// is refused at registration.
	err := NewRegistry().register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallTemplateScope,
		Description: "synthetic probe for the object-items refusal",
		Group:       "Firewall",
		Permissions: globalCheck("manage", "network"),
		Parameters: apischema.Properties{
			"rules": {
				Type:     apischema.Array,
				Optional: true,
				Items:    &apischema.Property{Type: apischema.Object},
			},
		},
		Handler: noopParamsHandler,
	})
	if err == nil {
		t.Fatal("Register accepted an array of objects; the reason these two routes stay legacy no longer holds")
	}
	if !strings.Contains(err.Error(), "scalar element types") {
		t.Errorf("Register refused the object-items schema for the wrong reason: %v", err)
	}
}

// TestFirewallTemplateIDsAreUUIDs pins the identifier shape on the three
// routes that take one. The handlers parsed it with uuid.Parse and answered
// 400 for anything else; the format states the same rule one layer earlier
// and names the field.
func TestFirewallTemplateIDsAreUUIDs(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodGet, firewallTemplateScope + "/:id"},
		{fiber.MethodDelete, firewallTemplateScope + "/:id"},
		{fiber.MethodPost, clusterScope + "/firewall-templates/:id/apply"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if got := requireDeclaredProperty(t, e, "id").Format; got != "uuid" {
				t.Fatalf("id declares format %q, want uuid", got)
			}
			base := map[string]any{"id": testTemplateID}
			if strings.HasPrefix(e.Path, clusterScope) {
				base["cluster_id"] = testClusterID
			}
			if _, err := e.Parameters.Validate(base); err != nil {
				t.Fatalf("a well-formed template id was rejected: %v", err)
			}
			for _, bad := range []string{"", "..", "not-a-uuid", "7b1c9d2e-4a35-4f60-8c21"} {
				probe := map[string]any{}
				for k, v := range base {
					probe[k] = v
				}
				probe["id"] = bad
				if _, err := e.Parameters.Validate(probe); err == nil {
					t.Errorf("id accepted %q", bad)
				}
			}
		})
	}
}
