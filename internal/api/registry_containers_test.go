package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_containers.go that quietly loosened a parameter would show up
// here.

// containerRouteCount is how many endpoints registerContainerEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
const containerRouteCount = 18

const testCTID = "8a1d2ab3-3f0e-4b4c-9f8a-000000000002"

// ctRoute renders one container route's path with the test ids
// substituted, so a test can name the route the way the declaration does
// and still send a real request at it.
func ctRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":ct_id", testCTID,
		":snap_name", "snap01",
	).Replace(path)
}

// containerLegacyPermissions is the permission each container handler
// checked with a hand-placed requireClusterPerm call BEFORE Phase 6a,
// transcribed from internal/api/handlers/containers.go at commit 2f2500f
// (18 handlers, 18 calls, every one of them cluster-scoped on the
// "container" resource).
//
// It exists so the migration is verifiable rather than asserted: the check
// moved from the handler body into route middleware, and the only thing
// that makes that safe is the two being the same check. Both directions
// are compared below — a route in this table with no declaration, and a
// declared container route missing from this table, are each a failure —
// so neither list can quietly drift away from the other.
//
// DeleteSnapshot is the one entry that is NOT simply what the code always
// did: its gate was corrected from execute to delete before this
// migration, and the declaration carries the corrected value.
var containerLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/containers":                                       "view:container",
	"POST /api/v1/clusters/:cluster_id/containers":                                      "manage:container",
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id":                                "view:container",
	"DELETE /api/v1/clusters/:cluster_id/containers/:ct_id":                             "delete:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/status":                        "execute:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone":                         "manage:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/convert-to-template":           "manage:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone-to-template":             "manage:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/migrate":                       "execute:container",
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/config":                         "view:container",
	"PUT /api/v1/clusters/:cluster_id/containers/:ct_id/config":                         "manage:container",
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshot-capability":            "view:container",
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots":                      "view:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots":                     "execute:container",
	"DELETE /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name":        "delete:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback": "execute:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/disks/resize":                  "manage:container",
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/volumes/move":                  "execute:container",
}

// declaredContainerEndpoints returns every declaration whose path is under
// the /containers prefix, keyed "METHOD path".
func declaredContainerEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, containerScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestContainerRoutesDeclareTheSamePermissionTheyEnforced is the tally
// that makes deleting 18 requireClusterPerm calls a refactor rather than a
// change.
func TestContainerRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredContainerEndpoints(t)
	if len(declared) != containerRouteCount {
		t.Fatalf("the registry declares %d container routes, want %d", len(declared), containerRouteCount)
	}
	if len(containerLegacyPermissions) != containerRouteCount {
		t.Fatalf("containerLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(containerLegacyPermissions), containerRouteCount)
	}

	for key, want := range containerLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every container route's permission is "+
				"statically known, because queries.GetContainer filters type = 'lxc' and the guest kind "+
				"cannot surprise the gate", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path, so the "+
				"declaration has to as well", key, e.Permissions.Check.Scope)
		}
	}

	for key := range declared {
		if _, listed := containerLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in containerLegacyPermissions — a new container route "+
				"must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestContainerRoutesDeclareEveryPathParameter re-states checkPathParams'
// rule for this domain and adds the one it does NOT enforce: that
// :cluster_id is the FIRST placeholder.
//
// That ordering is what namesACluster requires of a cluster-scoped Check,
// and Register would refuse a route that broke it — but it would refuse it
// with a message about the permission, so this asserts the property
// directly rather than leaving it to be inferred from an unrelated panic.
func TestContainerRoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredContainerEndpoints(t) {
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first, or the permission "+
				"middleware cannot resolve the cluster the route acts on", key, names)
		}
		for _, name := range names {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s has :%s with no entry in Parameters", key, name)
				continue
			}
			if prop.Optional {
				t.Errorf("%s declares the path parameter %q optional; a URL segment is always present",
					key, name)
			}
		}
		// The per-guest routes take :ct_id, not :vm_id. They share the vms
		// table with the VM domain, and a copy-paste from registry_vms.go
		// that kept vmParams would declare a parameter the path never
		// supplies — silently, since checkPathParams only looks the other
		// way for a path param with no schema entry.
		if _, wrong := e.Parameters["vm_id"]; wrong {
			t.Errorf("%s declares vm_id; the container routes spell the guest parameter ct_id", key)
		}
		// And it has to be the guest uuid, not a loosely-typed copy. This
		// is what checkPathParams does NOT check: it requires an entry for
		// :ct_id, not that the entry validates a uuid — and a bare string
		// there would hand uuid.Parse whatever the URL carried, which the
		// handler reports as a 500 rather than a 400 (see parseParamUUID).
		if !strings.Contains(e.Path, "/:ct_id") {
			continue
		}
		if got := e.Parameters["ct_id"].Format; got != "uuid" {
			t.Errorf("%s declares ct_id with format %q, want uuid — StdOption(\"ct-id\") is the one "+
				"definition of what a guest id looks like", key, got)
		}
	}
}

// TestContainerStatusEnumMatchesTheHandler pins that the declared action
// vocabulary IS handlers.ContainerStatusActions rather than a hand-written
// literal beside it, and that it is NOT the VM list: LXC has no reset.
//
// The link to the switch in ContainerHandler.PerformAction is one hop
// further and is held by TestContainerStatusActions in the handlers
// package, which names the six actions that switch covers. Nothing here
// reads the switch itself — a case deleted from it would still leave this
// green, and would answer "dispatched" with an empty UPID. That gap is
// the VM route's too; closing it needs an AST walk over the handler, not
// an assertion on the schema.
func TestContainerStatusEnumMatchesTheHandler(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, containerScope+"/:ct_id/status")
	action := e.Parameters["action"]
	if len(action.Enum) == 0 {
		t.Fatal("the status endpoint declares no action enum")
	}
	if !slices.Equal(action.Enum, handlers.ContainerStatusActions) {
		t.Errorf("action enum = %v, want handlers.ContainerStatusActions %v",
			action.Enum, handlers.ContainerStatusActions)
	}
	if slices.Contains(action.Enum, "reset") {
		t.Error("the container status enum accepts reset; Proxmox's LXC status endpoint has no such action")
	}

	target := ctRoute(containerScope + "/:ct_id/status")
	for _, tt := range []struct {
		action string
		want   int
	}{
		{action: "start", want: fiber.StatusNoContent},
		{action: "shutdown", want: fiber.StatusNoContent},
		{action: "resume", want: fiber.StatusNoContent},
		{action: "reset", want: fiber.StatusBadRequest},
		{action: "", want: fiber.StatusBadRequest},
		{action: "START", want: fiber.StatusBadRequest},
	} {
		t.Run(tt.action, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, containerScope+"/:ct_id/status", cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, target, `{"action":"`+tt.action+`"}`))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for an action the schema rejected")
			}
		})
	}
}

// probeCTEndpoint is a declared container endpoint with its handler
// swapped for a capture, so a test can see exactly what the schema handed
// over without needing the handler's database and Proxmox client.
func probeCTEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	// The real declaration gates on a container permission; these tests are
	// about the parameters, and the gate is exercised by
	// TestContainerRouteIsGatedByItsDeclaration below.
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestContainerCloneTolerateTheEmptySentinel is the compatibility half of
// reusing cloneParams() for the container routes: the clone dialog sends
// one body for both guest kinds (CloneRequest in
// frontend/src/features/vms/types/vm.ts), storage:"" and all, and
// apischema treats "" as a value the caller SUPPLIED that every registered
// format rejects.
func TestContainerCloneTolerateTheEmptySentinel(t *testing.T) {
	for _, path := range []string{
		containerScope + "/:ct_id/clone",
		containerScope + "/:ct_id/clone-to-template",
	} {
		t.Run(path, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))

			body := `{"new_id":9001,"name":"","target":"","full":false,"storage":""}`
			status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — the clone dialog sends every key, empty ones included",
					status, env.Message)
			}
			for _, key := range []string{"name", "target", "storage"} {
				if got := cap.params.String(key); got != "" {
					t.Errorf("%s = %q, want the empty sentinel to survive", key, got)
				}
			}

			// A non-empty value is still held to its shape, and new_id is
			// still required: the handler's "new_id must be positive" check
			// is gone, so the schema is the only thing left enforcing it.
			for _, bad := range []string{
				`{"new_id":9001,"storage":"store01:vm-9-disk-0"}`,
				`{"new_id":0}`,
				`{"new_id":-1}`,
				`{"name":"clone01"}`,
			} {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
				if status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), bad)); status != fiber.StatusBadRequest {
					t.Errorf("body %s: status = %d (%q), want 400", bad, status, env.Message)
				}
			}
		})
	}
}

// TestCreateContainerAcceptsTheDialogsEmptyFields is the same
// compatibility question for the create body, which has more of these than
// any other route here: CreateCTDialog sends hostname, storage, rootfs,
// password and ssh_keys unconditionally, and every one of them can be
// empty when the user left the field alone. CreateCT drops an empty value
// rather than forwarding it, so "" and "absent" have always meant the same
// thing — borrowing storage-id for `storage` would have turned a working
// dialog into a 400.
func TestCreateContainerAcceptsTheDialogsEmptyFields(t *testing.T) {
	const path = containerScope
	body := `{"vmid":9001,"hostname":"","node":"pve-01",` +
		`"ostemplate":"store01:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst",` +
		`"storage":"","rootfs":"","memory":512,"swap":512,"cores":1,` +
		`"net0":"name=eth0,bridge=vmbr0,ip=dhcp","password":"","ssh_keys":"",` +
		`"unprivileged":true,"start":false}`

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — the create dialog sends these keys empty", status, env.Message)
	}
	for _, key := range []string{"hostname", "storage", "rootfs", "password", "ssh_keys"} {
		if got := cap.params.String(key); got != "" {
			t.Errorf("%s = %q, want the empty sentinel to survive", key, got)
		}
	}
	if !cap.params.Bool("unprivileged") {
		t.Error("unprivileged = false, want the value the caller sent")
	}

	// The other half of the empty sentinel: a storage id that IS filled in
	// still has to travel, and still has to be held to its shape. A pattern
	// that only ever sees "" in a test is a pattern nobody has checked.
	for _, tt := range []struct {
		storage string
		want    int
	}{
		{storage: "store01", want: fiber.StatusNoContent},
		{storage: "store01:vm-9-disk-0", want: fiber.StatusBadRequest},
		{storage: "-store01", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"vmid":9001,"node":"pve-01","ostemplate":"store01:vztmpl/t.tar.zst","storage":"` + tt.storage + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), body))
		if status != tt.want {
			t.Errorf("storage %q: status = %d (%q), want %d", tt.storage, status, env.Message, tt.want)
			continue
		}
		if tt.want == fiber.StatusNoContent && cap.params.String("storage") != tt.storage {
			t.Errorf("storage %q reached the handler as %q", tt.storage, cap.params.String("storage"))
		}
	}
}

// TestCreateContainerRejectsWhatTheHandlerUsedTo pins the other half: the
// three "x is required" checks the handler no longer makes are now the
// schema's job, and a misspelled key is refused instead of silently
// dropped.
func TestCreateContainerRejectsWhatTheHandlerUsedTo(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "missing vmid", body: `{"node":"pve-01","ostemplate":"store01:vztmpl/t.tar.zst"}`, wantField: "vmid:"},
		{name: "vmid zero", body: `{"vmid":0,"node":"pve-01","ostemplate":"store01:vztmpl/t.tar.zst"}`, wantField: "vmid:"},
		{name: "negative vmid", body: `{"vmid":-1,"node":"pve-01","ostemplate":"store01:vztmpl/t.tar.zst"}`, wantField: "vmid:"},
		{name: "missing node", body: `{"vmid":9001,"ostemplate":"store01:vztmpl/t.tar.zst"}`, wantField: "node:"},
		{name: "empty node", body: `{"vmid":9001,"node":"","ostemplate":"store01:vztmpl/t.tar.zst"}`, wantField: "node:"},
		{name: "missing ostemplate", body: `{"vmid":9001,"node":"pve-01"}`, wantField: "ostemplate:"},
		{name: "empty ostemplate", body: `{"vmid":9001,"node":"pve-01","ostemplate":""}`, wantField: "ostemplate:"},
		{
			// PVE's additionalProperties => 0. "swap" spelled "swapp" used
			// to be bound to nothing and silently ignored.
			name:      "a misspelled parameter",
			body:      `{"vmid":9001,"node":"pve-01","ostemplate":"store01:vztmpl/t.tar.zst","swapp":512}`,
			wantField: "swapp:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, containerScope, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(containerScope), tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.wantField) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.wantField)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestConvertToTemplateAcceptsAnEmptyBody covers the one route that takes
// no body parameters at all but is still POSTed with a JSON object:
// useConvertToTemplate sends `{}` explicitly. bodyValues decodes a
// mutating verb's body whether or not the schema declares one, precisely
// so an undeclared payload is refused — and "{}" has to stay on the right
// side of that line, because it carries nothing to refuse.
func TestConvertToTemplateAcceptsAnEmptyBody(t *testing.T) {
	const path = containerScope + "/:ct_id/convert-to-template"
	if got := len(declaredEndpoint(t, fiber.MethodPost, path).Parameters); got != 2 {
		t.Fatalf("convert-to-template declares %d parameters, want just the two path ids", got)
	}

	for _, body := range []string{`{}`, ``} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), body)); status != fiber.StatusNoContent {
			t.Errorf("body %q: status = %d (%q), want 204", body, status, env.Message)
		}
	}

	// A payload the route declares nothing for is still refused rather than
	// silently discarded.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), `{"new_id":9001}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for an undeclared parameter", status, env.Message)
	}
}

// TestContainerSnapshotBodyHasNoVMState pins a deliberate narrowing.
//
// The legacy body struct carried a vmstate field that was bound and then
// never read: Proxmox's CT snapshot endpoint has no such parameter,
// because LXC cannot save a guest's RAM. The declaration leaves it out, so
// a caller who sends it is told rather than silently ignored — and the
// frontend already omits it for containers (CreateSnapshotDialog sends
// vmstate only when kind === "vm").
func TestContainerSnapshotBodyHasNoVMState(t *testing.T) {
	const path = containerScope + "/:ct_id/snapshots"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if _, declared := e.Parameters["vmstate"]; declared {
		t.Error("the container snapshot endpoint declares vmstate; LXC has no RAM state to save, " +
			"so declaring it would document a parameter that does nothing")
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(path), `{"snap_name":"snap01","vmstate":true}`))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d (%q), want 400", status, env.Message)
	}
	if !strings.HasPrefix(env.Message, "vmstate:") {
		t.Errorf("message = %q, want it to name vmstate", env.Message)
	}
}

// TestContainerSnapshotNameRules covers both spellings of a snapshot name:
// the CREATE body, which takes the pve-configid format, and the path
// parameter on delete and rollback, which is deliberately looser so that a
// snapshot made outside Nexara stays deletable.
func TestContainerSnapshotNameRules(t *testing.T) {
	const createPath = containerScope + "/:ct_id/snapshots"

	t.Run("create takes the pve-configid format", func(t *testing.T) {
		e := declaredEndpoint(t, fiber.MethodPost, createPath)
		if got := e.Parameters["snap_name"].Format; got != "pve-configid" {
			t.Errorf("snap_name format = %q, want pve-configid", got)
		}
		for _, name := range []string{"", "1snap", "snap 01", "a"} {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, createPath, cap))
			body := `{"snap_name":"` + name + `"}`
			if status, env := send(t, app, jsonRequest(http.MethodPost, ctRoute(createPath), body)); status != fiber.StatusBadRequest {
				t.Errorf("snap_name %q: status = %d (%q), want 400", name, status, env.Message)
			}
		}
	})

	t.Run("the path parameter takes the looser pattern", func(t *testing.T) {
		for _, path := range []struct {
			method string
			path   string
		}{
			{fiber.MethodDelete, containerScope + "/:ct_id/snapshots/:snap_name"},
			{fiber.MethodPost, containerScope + "/:ct_id/snapshots/:snap_name/rollback"},
		} {
			e := declaredEndpoint(t, path.method, path.path)
			prop := e.Parameters["snap_name"]
			if prop.Format != "" {
				t.Errorf("%s %s: snap_name declares format %q; a snapshot made outside Nexara can carry "+
					"a name our own create would refuse, and it still has to be deletable",
					path.method, path.path, prop.Format)
			}
			if prop.Pattern == "" {
				t.Errorf("%s %s: snap_name declares no pattern; the path segment would reach Proxmox "+
					"unchecked", path.method, path.path)
			}
		}
	})
}

// TestContainerResizeKeepsProxmoxDeltaSyntax is the VM route's assertion
// applied to the container one, because the two bodies look identical and
// the wrong choice is silent: "+8G" means "grow by 8 GiB", and the
// disk-size format would normalize it to "8" — "grow TO 8 GiB", a shrink
// on anything already larger.
func TestContainerResizeKeepsProxmoxDeltaSyntax(t *testing.T) {
	const path = containerScope + "/:ct_id/disks/resize"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if e.Parameters["size"].Format != "" {
		t.Errorf("resize declares format %q; the delta form cannot survive normalization",
			e.Parameters["size"].Format)
	}

	target := ctRoute(path)
	for _, size := range []string{"+8G", "64G", "1T", "512M", "100"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"disk":"rootfs","size":"` + size + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusNoContent {
			t.Errorf("size %q: status = %d (%q), want 204", size, status, env.Message)
			continue
		}
		if got := cap.params.String("size"); got != size {
			t.Errorf("size %q reached the handler as %q; it must be passed through untouched", size, got)
		}
	}

	// mp0 is a container mount point, and the VM route's disk key pattern
	// has to accept it as readily as rootfs.
	for _, disk := range []string{"rootfs", "mp0", "mp7"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"disk":"` + disk + `","size":"+8G"}`
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
			t.Errorf("disk %q: status = %d (%q), want 204", disk, status, env.Message)
		}
	}

	for _, body := range []string{
		`{"disk":"rootfs","size":"-8G"}`,
		`{"disk":"rootfs","size":"8 G"}`,
		`{"disk":"rootfs","size":""}`,
		`{"disk":"","size":"+8G"}`,
		`{"size":"+8G"}`,
		`{"disk":"rootfs"}`,
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusBadRequest {
			t.Errorf("body %s: status = %d (%q), want 400", body, status, env.Message)
		}
	}
}

// TestMoveVolumeTakesVolumeNotDisk pins the parameter name this endpoint
// has always taken. The shared DiskMoveSpec calls the field Disk, and the
// obvious copy from the VM route would rename the wire parameter with it —
// silently breaking every caller, including MigrateBatchDialog, which
// sends "volume" for a container and "disk" for a VM through the same
// loop.
func TestMoveVolumeTakesVolumeNotDisk(t *testing.T) {
	const path = containerScope + "/:ct_id/volumes/move"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if _, ok := e.Parameters["volume"]; !ok {
		t.Fatal("the move endpoint declares no volume parameter")
	}
	if _, wrong := e.Parameters["disk"]; wrong {
		t.Error("the move endpoint declares disk; the container route's parameter is named volume")
	}
	if _, wrong := e.Parameters["format"]; wrong {
		t.Error("the move endpoint declares format; LXC has no image format, and CTParams drops it")
	}

	target := ctRoute(path)
	// bwlimit_kib is optional both ways: useMoveContainerVolume sends 0 and
	// MigrateBatchDialog omits it entirely.
	for _, body := range []string{
		`{"volume":"rootfs","storage":"store01","delete":false,"bwlimit_kib":0}`,
		`{"volume":"mp0","storage":"store01","delete":true}`,
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
			t.Errorf("body %s: status = %d (%q), want 204", body, status, env.Message)
		}
	}

	for _, tt := range []struct{ body, field string }{
		{`{"storage":"store01"}`, "volume:"},
		{`{"volume":"rootfs"}`, "storage:"},
		{`{"volume":"rootfs","storage":""}`, "storage:"},
		{`{"volume":"rootfs","storage":"store01","bwlimit_kib":-1}`, "bwlimit_kib:"},
		{`{"disk":"rootfs","storage":"store01"}`, "disk:"},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCTEndpoint(t, fiber.MethodPost, path, cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body))
		if status != fiber.StatusBadRequest {
			t.Errorf("body %s: status = %d (%q), want 400", tt.body, status, env.Message)
			continue
		}
		if !strings.HasPrefix(env.Message, tt.field) {
			t.Errorf("body %s: message = %q, want it to start with %q", tt.body, env.Message, tt.field)
		}
	}
}

// TestContainerRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end, on
// a REAL endpoint rather than a synthetic one.
//
// It is run against destroy because that is the most consequential of the
// 18 and the one whose deleted inline check would be least survivable: if
// the declaration and the middleware ever disagreed, every guard in this
// package would still pass — they read the declaration — and the route
// would ship authenticated but ungated.
func TestContainerRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, containerScope+"/:ct_id")
	if e.Permissions.Describe() != "delete:container" {
		t.Fatalf("the destroy endpoint declares %q, want delete:container", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := ctRoute(containerScope + "/:ct_id")

	t.Run("a caller holding only manage:container is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:container": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding delete:container gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"delete:container": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"delete:container": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryContainerEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing: every route and every
// parameter says what it is for, because the declaration IS the
// documentation and 319 endpoints in this API still say nothing.
//
// Only two of the four checks below can actually fire, and it is worth
// knowing which. Register PANICS on a blank Description, a blank Group or
// a schema that fails Compile, so those three have necessarily passed by
// the time declaredContainerEndpoints returns — they are belt and braces
// against buildRegistry ever being changed to report instead of panic,
// matching TestEveryVMEndpointCompiles. The two that bite are the Group
// value (Register requires A group, not THIS one) and the per-parameter
// Description, which Compile does not look at at all.
func TestEveryContainerEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredContainerEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Containers" {
			t.Errorf("%s is in group %q, want Containers", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
