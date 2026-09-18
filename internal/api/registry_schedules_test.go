package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// scheduleRouteCount is how many endpoints registerScheduleEndpoints declares.
const scheduleRouteCount = 4

// scheduleLegacyPermissions is the permission each handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/schedules.go` at commit eaeafa7 — four
// handlers, four calls, every one cluster-scoped on the "schedule" resource.
var scheduleLegacyPermissions = map[string]string{
	"POST /api/v1/clusters/:cluster_id/schedules":       "manage:schedule",
	"GET /api/v1/clusters/:cluster_id/schedules":        "view:schedule",
	"PUT /api/v1/clusters/:cluster_id/schedules/:id":    "manage:schedule",
	"DELETE /api/v1/clusters/:cluster_id/schedules/:id": "manage:schedule",
}

func declaredScheduleEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, scheduleScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestScheduleRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting four requireClusterPerm calls a refactor rather than a change.
func TestScheduleRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredScheduleEndpoints(t)
	if len(declared) != scheduleRouteCount {
		t.Fatalf("the registry declares %d schedule routes, want %d", len(declared), scheduleRouteCount)
	}
	if len(scheduleLegacyPermissions) != scheduleRouteCount {
		t.Fatalf("scheduleLegacyPermissions has %d entries, want %d",
			len(scheduleLegacyPermissions), scheduleRouteCount)
	}

	var view, manage int
	for key, want := range scheduleLegacyPermissions {
		switch want {
		case "view:schedule":
			view++
		case "manage:schedule":
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
		t.Errorf("the tally splits %d view:schedule / %d manage:schedule, want 1 / 3", view, manage)
	}

	for key := range declared {
		if _, listed := scheduleLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in scheduleLegacyPermissions", key)
		}
	}
}

// TestScheduleActionVocabulary pins the declared Enum against the handler's own
// validScheduleActions map.
//
// Unlike a Proxmox vocabulary these values select a branch in Nexara's OWN
// scheduler, so the drift that bites is a schema that accepts an action the
// scheduler has no branch for: the row is stored, the UI shows it, and it never
// runs.
func TestScheduleActionVocabulary(t *testing.T) {
	declaredEnum := slices.Clone(declaredEndpoint(t, fiber.MethodPost, scheduleScope).Parameters["action"].Enum)
	slices.Sort(declaredEnum)
	want := handlers.ScheduleActionKeys()
	if !slices.Equal(declaredEnum, want) {
		t.Errorf("the declared action enum is %v but handlers.validScheduleActions carries %v — an action "+
			"in one and not the other is a schedule nothing will ever execute", declaredEnum, want)
	}
}

// TestScheduleCreateRejectsWhatTheHandlerUsedTo pins the "x is required"
// refusals the handler no longer writes itself, and the one rule that stayed
// there.
func TestScheduleCreateRejectsWhatTheHandlerUsedTo(t *testing.T) {
	target := strings.ReplaceAll(scheduleScope, ":cluster_id", testClusterID)
	const valid = `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot","schedule":"0 3 * * *"}`

	for _, tt := range []struct{ name, body, field string }{
		{"no resource_type", `{"resource_id":"101","node":"pve-01","action":"snapshot","schedule":"0 3 * * *"}`, "resource_type:"},
		{"empty resource_id", `{"resource_type":"vm","resource_id":"","node":"pve-01","action":"snapshot","schedule":"0 3 * * *"}`, "resource_id:"},
		{"no node", `{"resource_type":"vm","resource_id":"101","action":"snapshot","schedule":"0 3 * * *"}`, "node:"},
		{"an unknown action", `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"migrate","schedule":"0 3 * * *"}`, "action:"},
		{"no schedule", `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot"}`, "schedule:"},
		{"a misspelled key", `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot","schedule":"0 3 * * *","enable":true}`, "enable:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPost, scheduleScope)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}

	// The cron expression is deliberately NOT validated by the schema — see
	// scheduleSpecParam — so a syntactically impossible one has to reach the
	// handler, where cronspec validates it and computes the stored next run in
	// the same call.
	t.Run("the cron expression reaches the handler unvalidated", func(t *testing.T) {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, scheduleScope)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		body := strings.ReplaceAll(valid, `"0 3 * * *"`, `"0 3 31 2 *"`)
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204 — the schema must not second-guess cronspec", status, env.Message)
		}
		if got := cap.params.String("schedule"); got != "0 3 31 2 *" {
			t.Errorf("schedule reached the handler as %q", got)
		}
	})

	// params is opaque and optional: absent means the handler stores `{}`.
	t.Run("params is optional and opaque", func(t *testing.T) {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, scheduleScope)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		if status, env := send(t, app, jsonRequest(http.MethodPost, target, valid)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if cap.params.Has("params") {
			t.Error("params reads as supplied on a body that omitted it")
		}
	})
}

// TestEveryScheduleEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryScheduleEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredScheduleEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Tasks" {
			t.Errorf("%s is in group %q, want Tasks", key, e.Group)
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
