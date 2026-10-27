package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// guestHistoryCategories is the type filter's vocabulary: exactly the values
// the category CASE in ListGuestHistory (queries/guest_history.sql) produces.
// The declaration refuses anything else, so a typo is a 400 rather than an
// empty page; TestGuestHistoryCategoryVocabulary holds the two together.
var guestHistoryCategories = []string{
	"power", "migration", "snapshot", "backup", "clone", "lifecycle", "config", "ha", "console", "other",
}

// registerGuestHistoryEndpoints declares GuestHistoryHandler's one route: a
// guest's History tab.
//
// It is a plain cluster-scoped Check on view:audit, the permission of the data
// the timeline is made of. The two parts that belong to other permissions — a
// task with no audit row (view:task) and a status change (view:vm or
// view:container, by the guest's kind) — are filtered on them in the handler
// rather than gated, so the gate states the one thing every caller needs; see
// GuestHistoryHandler.List.
//
// Keyed on the PROXMOX VMID, not a vms.id, for the reason guestVMIDParams
// gives: the collector re-issues those, and a guest's history must outlive
// its inventory row, which it never would keyed on one.
func registerGuestHistoryEndpoints(reg *Registry, h *handlers.GuestHistoryHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/guests/:vmid/history",
		Description: "List what happened to one guest, newest first: its power actions, migrations, " +
			"snapshots, backups, clones, config and HA changes — done in Nexara or in Proxmox — with each " +
			"task's outcome, and the status changes no task explains, such as a shutdown or crash inside " +
			"the guest. Starts after the latest create, clone or restore of the VMID, or the destroy of the " +
			"guest that held it before, so a reused VMID does not inherit the previous guest's history. A " +
			"backup job that covers several guests, whose Proxmox task names none, is listed for each guest " +
			"it backed up, with that guest's own outcome and times, once the job has finished. Requires " +
			"view:audit, which shows each task's outcome; a task's exit text, tasks recorded only in the " +
			"task history and backup-job entries need view:task too, and status changes view:vm or " +
			"view:container.",
		Group:       "Audit Log",
		Permissions: clusterCheck("view", "audit"),
		Parameters: guestVMIDParams(apischema.Properties{
			"limit": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     50,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(200.0),
				Typetext:    "<integer>",
				Description: "Page size.",
			},
			"offset": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     0,
				Minimum:     apischema.Ptr(0.0),
				Typetext:    "<integer>",
				Description: "Entries to skip.",
			},
			// The EMPTY string is "no filter", as on the task listing's
			// status; it is not a category.
			"category": {
				Type:        apischema.String,
				Optional:    true,
				Enum:        append([]string{""}, guestHistoryCategories...),
				Typetext:    "<power|migration|snapshot|backup|clone|lifecycle|config|ha|console|other>",
				Description: "Narrow to one kind of entry. Empty or omitted returns every kind.",
			},
			"source": {
				Type:     apischema.String,
				Optional: true,
				Enum:     []string{"", "nexara", "proxmox", "detected"},
				Typetext: "<nexara|proxmox|detected>",
				Description: "Narrow to what was done in Nexara, what was done in Proxmox, or the status " +
					"changes no task explains (detected). Empty or omitted returns every source.",
			},
			// Named as the audit listing names it, and parsed the same way.
			"start_time": {
				Type:        apischema.String,
				Optional:    true,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<RFC3339 timestamp>",
				Description: "Oldest entry to return, inclusive. Empty or omitted sets no lower bound.",
			},
		}),
		Handler: h.List,
	})
}
