package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// guestHistoryRoute is a guest's History tab.
//
// It is a plain cluster-scoped Check on view:audit, the permission of the data
// the timeline is made of. The two parts that belong to other permissions — a
// task with no audit row (view:task) and a status change (view:vm or
// view:container, by the guest's kind) — are filtered on them in the handler
// rather than gated, so the gate states the one thing every caller needs; see
// HistoryHandler.List.
//
// Keyed on the PROXMOX VMID, not a vms.id, for the reason guestVMIDParams
// gives: the collector re-issues those, and a guest's history must outlive
// its inventory row, which it never would keyed on one.
func guestHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.GuestHistory(),
		Path: clusterScope + "/guests/:vmid/history",
		Description: "List what happened to one guest, newest first: its power actions, migrations, " +
			"snapshots, backups, clones, config and HA changes — done in Nexara or in Proxmox — with each " +
			"task's outcome, and the status changes no task explains, such as a shutdown or crash inside " +
			"the guest. Starts after the latest create, clone or restore of the VMID, or the destroy of the " +
			"guest that held it before, so a reused VMID does not inherit the previous guest's history. A " +
			"backup job that covers several guests, whose Proxmox task names none, is listed for each guest " +
			"it backed up, with that guest's own outcome and times, once the job has finished. Requires " +
			"view:audit, which shows each task's outcome; a task's exit text, tasks recorded only in the " +
			"task history and backup-job entries need view:task too, and status changes view:vm or " +
			"view:container. A cluster that does not exist is a 404.",
		AuditOnly: "a guest's own permission depends on its type — view:vm or view:container — so the " +
			"inventory's part of it, its status changes, is filtered on that instead",
		Parameters: guestVMIDParams(apischema.Properties{}),
	}
}
