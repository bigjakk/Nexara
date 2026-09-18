package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// vmFolderRouteCount is how many endpoints registerVMFolderEndpoints declares.
// It is FOUR of VMFoldersHandler's five — see TestVMFolderReparentIsStillLegacy
// for the fifth.
const vmFolderRouteCount = 4

// vmFolderLegacyPermissions is the permission each MIGRATED handler checked
// with a hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/vm_folders.go` at commit eaeafa7. The
// file held five such calls; the fifth belongs to Update, which stays legacy
// and keeps its own.
var vmFolderLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/vm-folders":               "view:vm_folder",
	"POST /api/v1/clusters/:cluster_id/vm-folders":              "manage:vm_folder",
	"DELETE /api/v1/clusters/:cluster_id/vm-folders/:folder_id": "manage:vm_folder",
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/folder":        "manage:vm_folder",
}

func declaredVMFolderEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, want := vmFolderLegacyPermissions[e.Method+" "+e.Path]; want {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestVMFolderRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting four requireClusterPerm calls a refactor rather than a change.
func TestVMFolderRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredVMFolderEndpoints(t)
	if len(declared) != vmFolderRouteCount {
		t.Fatalf("the registry declares %d VM-folder routes, want %d", len(declared), vmFolderRouteCount)
	}
	if len(vmFolderLegacyPermissions) != vmFolderRouteCount {
		t.Fatalf("vmFolderLegacyPermissions has %d entries, want %d",
			len(vmFolderLegacyPermissions), vmFolderRouteCount)
	}

	var view, manage int
	for key, want := range vmFolderLegacyPermissions {
		switch want {
		case "view:vm_folder":
			view++
		case "manage:vm_folder":
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
	if view != 1 || manage != 3 {
		t.Errorf("the tally splits %d view:vm_folder / %d manage:vm_folder, want 1 / 3", view, manage)
	}
}

// TestVMFolderReparentIsStillLegacy records the vocabulary gap this domain hit,
// so it stays a decision rather than becoming an omission.
//
// PATCH /vm-folders/:folder_id carries a THREE-state parent_id — absent leaves
// the folder where it is, an explicit null moves it to the top level, a uuid
// moves it under that folder — decoded by the handler's own jsonNullUUID.
// apischema's present() reads an explicit JSON null as ABSENT (validate.go), so
// a declaration would collapse the first two states and silently turn "move
// this folder to the top level" into a request that answers 200 and changes
// nothing. That is worse than leaving the route imperative: a caller cannot
// tell it did not happen.
//
// Pinned from both sides: the route must still be registered, and it must NOT
// be in the registry — a well-meaning later declaration of it is a silent
// behaviour change, not a compile error.
func TestVMFolderReparentIsStillLegacy(t *testing.T) {
	const key = "PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id"

	s := newRouteStubServer(t)
	if registryRouteKeySet(s.registry.Endpoints())[key] {
		t.Error("the folder re-parent is declared in the registry, but apischema reads an explicit JSON " +
			"null as absent, so \"move to the top level\" would become a no-op — see registerVMFolderEndpoints")
	}
	registered := false
	for _, r := range s.app.GetRoutes(true) {
		if r.Method+" "+normalizeRoutePath(r.Path) == key {
			registered = true
		}
	}
	if !registered {
		t.Error("the folder re-parent is not registered at all — it is meant to stay in router.go, not to disappear")
	}
	if !legacyRouteBaseline[key] {
		t.Error("the folder re-parent is not in legacyRouteBaseline; the ratchet would flag it as a new legacy route")
	}
}

// TestVMFolderNullMeansTheSameAsAbsentWhereItIsDeclared is the other half of
// that decision: the two routes that DO carry an optional folder reference were
// safe to declare precisely because null and absent mean the same thing on
// them, and this pins that they still do.
//
// On the create, both mean "top level". On the guest assignment, both mean
// "unassign". If either ever grew a third meaning it would have to follow the
// PATCH out of the registry, and this is where that shows up.
func TestVMFolderNullMeansTheSameAsAbsentWhereItIsDeclared(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		field  string
		body   string
	}{
		{fiber.MethodPost, vmFolderScope, "parent_id", `{"name":"folder01","parent_id":null}`},
		{fiber.MethodPut, clusterScope + "/vms/:vm_id/folder", "folder_id", `{"folder_id":null}`},
	} {
		t.Run(tt.method+" "+tt.field, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, tt.method, tt.path)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", testVMFolderGuestID).Replace(tt.path)
			method := http.MethodPost
			if tt.method == fiber.MethodPut {
				method = http.MethodPut
			}
			if status, env := send(t, app, jsonRequest(method, target, tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if cap.params.Has(tt.field) {
				t.Errorf("an explicit null %s reads as supplied; the handler treats null and absent alike, "+
					"so a change here means the route can no longer be declared", tt.field)
			}
		})
	}
}

// testVMFolderGuestID follows the file-local convention for a synthetic id
// (see testVMID and testNodeID): a fixed prefix with a counter, so nothing in
// it can be mistaken for a value that came from somewhere real.
const testVMFolderGuestID = "8a1d2ab3-3f0e-4b4c-9f8a-000000000009"

// TestEveryVMFolderEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryVMFolderEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredVMFolderEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Virtual Machines" {
			t.Errorf("%s is in group %q, want Virtual Machines", key, e.Group)
		}
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
