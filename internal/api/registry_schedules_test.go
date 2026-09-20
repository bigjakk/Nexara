package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/scheduler"
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

// TestScheduleResourceTypeVocabulary pins the declared resource_type Enum
// against the two places the stored value is read back.
//
// This is the same shape as TestScheduleActionVocabulary and it is here
// because the drift it guards actually happened. resource_type carried no Enum
// at all — any string of 32 characters or fewer was stored — while its
// published Typetext read "<vm|lxc>". "lxc" is Proxmox's word for the guest
// type and it is not the scheduler's: both switches in
// internal/scheduler/scheduler.go match "vm" and "ct" and return "unsupported
// resource type" for anything else. A caller following the docs therefore
// created a schedule that was accepted, shown as armed, and failed on EVERY
// fire into last_error, where nothing surfaces it.
//
// Nothing here writes the vocabulary down. The declaration is read from the
// registry, the scheduler's branches from scheduler.ResourceTypeKeys, and the
// snapshot-name check's from handlers.ScheduleResourceTypeKeys — three derived
// sets, because a test that restates the list is a fourth copy that drifts with
// the rest. The non-vacuity floor below is what stops all three going empty
// together and reporting agreement.
func TestScheduleResourceTypeVocabulary(t *testing.T) {
	declared := slices.Clone(declaredEndpoint(t, fiber.MethodPost, scheduleScope).Parameters["resource_type"].Enum)
	slices.Sort(declared)

	// Without this an Enum nobody declared compares equal to a scheduler that
	// branches on nothing, which is the state this test exists to report.
	if len(declared) == 0 {
		t.Fatal("resource_type declares no Enum, so any string is stored and the scheduler decides " +
			"on its first fire whether the row was ever runnable")
	}

	if want := scheduler.ResourceTypeKeys(); !slices.Equal(declared, want) {
		t.Errorf("the declared resource_type enum is %v but the scheduler branches on %v — a type in "+
			"one and not the other is a schedule that is accepted and then fails on every fire",
			declared, want)
	}
	if want := handlers.ScheduleResourceTypeKeys(); !slices.Equal(declared, want) {
		t.Errorf("the declared resource_type enum is %v but handlers.snapshotScheduleGuestKind knows "+
			"%v — a type missing there skips the snap_name check and defers a refusal Proxmox will "+
			"make on every fire", declared, want)
	}
}

// TestScheduleCreateRejectsAnUnrunnableResourceType is the behavioural half:
// the vocabulary guard above compares lists, and this one sends the value the
// docs used to advertise through the compiled declaration.
func TestScheduleCreateRejectsAnUnrunnableResourceType(t *testing.T) {
	target := strings.ReplaceAll(scheduleScope, ":cluster_id", testClusterID)

	for _, tt := range []struct {
		name         string
		resourceType string
		want         int
		why          string
	}{
		{name: "a VM", resourceType: "vm", want: fiber.StatusNoContent,
			why: "the ordinary case, and the control that stops this passing by refusing everything"},
		{name: "a container", resourceType: "ct", want: fiber.StatusNoContent,
			why: "what the SPA sends and what both scheduler switches read"},
		{name: "Proxmox's spelling", resourceType: "lxc", want: fiber.StatusBadRequest,
			why: "what the Typetext used to advertise; the scheduler has no branch for it"},
		{name: "a node", resourceType: "node", want: fiber.StatusBadRequest,
			why: "a plausible object kind that no branch handles"},
		{name: "the wrong case", resourceType: "VM", want: fiber.StatusBadRequest,
			why: "the scheduler compares exactly, so a case variant is as unrunnable as a typo"},
		// The Enum replaced a MinLength(1), which is the facet that used to
		// refuse this one. Kept as a row so the swap cannot quietly widen
		// what the route takes.
		{name: "empty", resourceType: "", want: fiber.StatusBadRequest,
			why: "no branch matches the empty string, and apischema counts it as a value the caller supplied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPost, scheduleScope)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			body := `{"resource_type":"` + tt.resourceType + `","resource_id":"101","node":"pve-01",` +
				`"action":"snapshot","schedule":"0 3 * * *"}`
			status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
			if status != tt.want {
				t.Fatalf("resource_type %q: status = %d (%q), want %d — %s",
					tt.resourceType, status, env.Message, tt.want, tt.why)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
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
