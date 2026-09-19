package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// aptRouteCount is how many endpoints registerAptRepositoryEndpoints declares.
const aptRouteCount = 3

// aptLegacyPermissions is the permission each handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/apt_repositories.go` at commit eaeafa7 —
// three handlers, three calls, every one cluster-scoped on the "apt_repository"
// resource.
//
// It is also the tally TestEveryDeclaredNodeRouteIsInTheTally defers to: these
// three share the /nodes/ prefix but are not NodeHandler's.
var aptLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":  "view:apt_repository",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":  "manage:apt_repository",
	"POST /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories": "manage:apt_repository",
}

func declaredAptEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if e.Path == aptScope {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestAptRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// deleting three requireClusterPerm calls a refactor rather than a change.
func TestAptRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredAptEndpoints(t)
	if len(declared) != aptRouteCount {
		t.Fatalf("the registry declares %d APT repository routes, want %d", len(declared), aptRouteCount)
	}
	if len(aptLegacyPermissions) != aptRouteCount {
		t.Fatalf("aptLegacyPermissions has %d entries, want %d", len(aptLegacyPermissions), aptRouteCount)
	}

	var view, manage int
	for key, want := range aptLegacyPermissions {
		switch want {
		case "view:apt_repository":
			view++
		case "manage:apt_repository":
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
	if view != 1 || manage != 2 {
		t.Errorf("the tally splits %d view:apt_repository / %d manage:apt_repository, want 1 / 2", view, manage)
	}

	for key := range declared {
		if _, listed := aptLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in aptLegacyPermissions — a new APT route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestAptToggleEnforcesTheHandlersOwnRules pins the three refusals the handler
// used to write itself, now stated one layer earlier.
//
// The handle pattern is the one that matters: the value becomes part of the
// request Proxmox parses, and the handler's own handlePattern was the only
// thing keeping it to a lowercase identifier.
func TestAptToggleEnforcesTheHandlersOwnRules(t *testing.T) {
	target := strings.NewReplacer(":cluster_id", testClusterID, ":node", "pve-01").Replace(aptScope)

	for _, tt := range []struct {
		name   string
		method string
		body   string
		want   int
		field  string
	}{
		{"a valid toggle", fiber.MethodPut, `{"path":"/etc/apt/sources.list","index":1,"enabled":true}`, fiber.StatusNoContent, ""},
		{"no path", fiber.MethodPut, `{"index":0,"enabled":true}`, fiber.StatusBadRequest, "path:"},
		{"empty path", fiber.MethodPut, `{"path":"","index":0}`, fiber.StatusBadRequest, "path:"},
		{"negative index", fiber.MethodPut, `{"path":"/etc/apt/sources.list","index":-1}`, fiber.StatusBadRequest, "index:"},
		{"a misspelled key", fiber.MethodPut, `{"path":"/etc/apt/sources.list","enable":true}`, fiber.StatusBadRequest, "enable:"},
		{"a valid standard repo", fiber.MethodPost, `{"handle":"no-subscription"}`, fiber.StatusNoContent, ""},
		{"an uppercase handle", fiber.MethodPost, `{"handle":"No-Subscription"}`, fiber.StatusBadRequest, "handle:"},
		{"a traversing handle", fiber.MethodPost, `{"handle":"../etc"}`, fiber.StatusBadRequest, "handle:"},
		{"no handle", fiber.MethodPost, `{}`, fiber.StatusBadRequest, "handle:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, tt.method, aptScope)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			method := http.MethodPut
			if tt.method == fiber.MethodPost {
				method = http.MethodPost
			}
			status, env := send(t, app, jsonRequest(method, target, tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.field == "" {
				return
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestEveryAptEndpointIsDocumented holds the declarations to the standard that
// makes this whole effort worth doing.
func TestEveryAptEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredAptEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		// "Nodes", not the "Node Management" these three carried until the
		// group vocabulary was canonicalised: the Nodes section already holds
		// the other writes against a node (dns, time, services, reboot,
		// shutdown, maintenance, the disk lifecycle), so a second section for
		// three more of them split one docs heading in two and left an
		// operator looking under the wrong one. See canonicalGroups in
		// registry_group_guard_test.go.
		if e.Group != "Nodes" {
			t.Errorf("%s is in group %q, want Nodes", key, e.Group)
		}
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		if e.Parameters["node"].Format != "node-name" {
			t.Errorf("%s declares node with format %q, want node-name", key, e.Parameters["node"].Format)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
