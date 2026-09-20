package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams and optFlag — lives in
// registry_vms.go, where the first migrated domain defined it.

// scheduleScope is the collection all four routes hang off.
const scheduleScope = clusterScope + "/schedules"

// scheduleSpecParam is the cron expression a scheduled task fires on.
//
// The declaration bounds and shapes it; what it deliberately does NOT do is
// validate the expression, which stays with cronspec on the handler side. That
// is not tidiness: cronspec.NextValidRun both validates the spec AND returns
// the next run stored on the row, in one call, because an invalid next_run_at
// reads as "due now" and a schedule that can never fire would then be claimed
// and run on every scheduler tick. Splitting the check out here would leave two
// places that have to agree about what a valid spec is.
var scheduleSpecParam = apischema.Property{
	Type:      apischema.String,
	MinLength: apischema.Ptr(1),
	MaxLength: apischema.Ptr(256),
	Typetext:  "<cron expression>",
	Description: "Cron expression, in the five-field form plus the descriptors robfig/cron accepts. " +
		"Refused when it names a date that cannot occur, which would otherwise store a next run the " +
		"scheduler reads as \"due now\" forever.",
}

// scheduleParamsParam is the opaque per-action payload.
//
// Carried through rather than described: the accepted keys belong to whichever
// action the row names, and apischema has no nested-object schema. An absent
// value becomes `{}` in the handler, which is what the column has always
// stored for a task that needs no options.
var scheduleParamsParam = apischema.Property{
	Type:        apischema.Object,
	Optional:    true,
	Typetext:    "<object>",
	Description: "Action-specific options. Omitted, an empty object is stored.",
}

// registerScheduleEndpoints declares ScheduleHandler's four routes.
//
// All four are a plain cluster-scoped Check — manage:schedule for the three
// writes, view:schedule for the listing — and none of them is conditional.
//
// The Check is NOT the whole story on the two per-task routes, and the same
// sentence appears on registry_metrics.go for the same reason: a scheduled task
// is addressed by its uuid, which says nothing about which cluster owns it,
// while the Check resolves the cluster from the PATH. So the update and the
// delete additionally re-read the row and compare its cluster
// (ScheduleHandler.taskInCluster), and both statements carry a cluster_id
// predicate of their own (queries/scheduled_tasks.sql). Without that,
// manage:schedule on any one cluster authorized a write to every scheduled task
// in the install.
//
// The create body carries TWO vocabularies — `action` and `resource_type` —
// and both are restated here from the code behind them. Unlike a Proxmox
// vocabulary that is safe: the values select a branch in Nexara's OWN scheduler
// (internal/scheduler), so an unlisted value is not a call Proxmox would reject
// but a row nothing will ever execute. That is the worst failure available on
// this table, because it is invisible at the moment the caller could still fix
// it — the row is stored, the schedule reads as armed, and the refusal lands on
// every fire in last_error.
//
// `action` restates the handler's validScheduleActions map;
// TestScheduleActionVocabulary pins the two. `resource_type` restates
// scheduler.scheduledResourceTypes, and carried no Enum at all until the
// published Typetext ("<vm|lxc>") was found to be naming a value the scheduler
// has never had a branch for; TestScheduleResourceTypeVocabulary pins the
// declaration, the scheduler and handlers.snapshotScheduleGuestKind together.
func registerScheduleEndpoints(reg *Registry, h *handlers.ScheduleHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   scheduleScope,
		Description: "Create a scheduled task on a guest — a recurring snapshot or reboot. The stored next " +
			"run is computed from the cron expression at creation, so a spec that can never fire is refused here.",
		Group:       "Tasks",
		Permissions: clusterCheck("manage", "schedule"),
		Parameters: clusterParams(apischema.Properties{
			// The Enum is scheduler.scheduledResourceTypes restated one
			// layer earlier, on the same terms as `action` below, and it
			// closes the same hole from the other side: without it any
			// string of 32 characters or fewer was stored, and the value
			// this parameter's own Typetext told callers to send — "lxc",
			// which is Proxmox's word for the guest type — was one the
			// scheduler has no branch for. Such a row is accepted, shows in
			// the UI as armed, and then fails on EVERY fire with
			// "unsupported resource type" into last_error.
			//
			// "ct" and not "lxc" because "ct" is what is stored: the SPA has
			// always sent it, both scheduler switches have always read it,
			// and handlers.snapshotScheduleGuestKind keys on it. Correcting
			// the docs to match the system leaves every existing row valid;
			// correcting the system to match the docs would strand them.
			"resource_type": {
				Type:     apischema.String,
				Enum:     []string{"vm", "ct"},
				Typetext: "<vm|ct>",
				Description: "Kind of object the task acts on — a VM or a container. Stored verbatim and " +
					"read back by the scheduler when the task fires, which is why it is the " +
					"scheduler's spelling (\"ct\") and not Proxmox's (\"lxc\").",
			},
			"resource_id": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(128),
				Typetext:    "<id>",
				Description: "Identifier of the object the task acts on, as the scheduler expects to read it back.",
			},
			"node": apischema.StdOption("node-name"),
			"action": {
				Type:        apischema.String,
				Enum:        []string{"snapshot", "reboot"},
				Typetext:    "<snapshot|reboot>",
				Description: "What the task does when it fires.",
			},
			"schedule": scheduleSpecParam,
			"params":   scheduleParamsParam,
			"enabled":  optFlag("Whether the task fires. Created disabled unless set."),
		}),
		Handler: h.Create,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        scheduleScope,
		Description: "List the cluster's scheduled tasks with their last and next run.",
		Group:       "Tasks",
		Permissions: clusterCheck("view", "schedule"),
		Parameters:  clusterParams(nil),
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   scheduleScope + "/:id",
		Description: "Update a scheduled task's cron expression, its options and whether it is enabled. " +
			"The resource it acts on cannot be changed.",
		Group:       "Tasks",
		Permissions: clusterCheck("manage", "schedule"),
		Parameters: clusterParams(apischema.Properties{
			"id": {
				Type:        apischema.String,
				Format:      "uuid",
				Source:      apischema.SourcePath,
				Typetext:    "<uuid>",
				Description: "Scheduled task identifier.",
			},
			"schedule": scheduleSpecParam,
			"params":   scheduleParamsParam,
			"enabled":  optFlag("Whether the task fires. Omitted disables it, which is what the handler has always read out of a body that left it out."),
		}),
		Handler: h.Update,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        scheduleScope + "/:id",
		Description: "Delete a scheduled task.",
		Group:       "Tasks",
		Permissions: clusterCheck("manage", "schedule"),
		Parameters: clusterParams(apischema.Properties{
			"id": {
				Type:        apischema.String,
				Format:      "uuid",
				Source:      apischema.SourcePath,
				Typetext:    "<uuid>",
				Description: "Scheduled task identifier.",
			},
		}),
		Handler: h.Delete,
	})
}
