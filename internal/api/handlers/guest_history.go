package handlers

import (
	"context"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// GuestHistory is the guest kind: a VM's or container's History tab,
// everything that happened to the guest. It is the only kind that reads the
// guest-shaped sources for one guest — its tasks, its status changes no task
// explains, its parts of backup jobs — and the only one with a cutoff: a
// reused VMID does not inherit the previous guest's history
// (guestHistoryCutoff).
func GuestHistory() HistoryKind {
	return HistoryKind{
		Name:       "guest",
		Categories: []string{"power", "migration", "snapshot", "backup", "clone", "lifecycle", "config", "ha", "console", "other"},
		Default:    "other",
		Rules:      guestHistoryRules,
		Select:     selectGuest,
		// The path names the guest; a status change, the inventory's part, is
		// filtered on its type instead (historyScope).
		GuestFromPath: true,
	}
}

// guestHistoryRules are the type filter's categories, matched in order.
// Nexara's own action names are matched before the Proxmox worker type,
// because they are the more specific of the two: DRS asks an HA-managed guest
// to migrate through an hamigrate task, and that is a migration. A start or
// stop of an HA-managed guest runs as hastart or hastop, from Nexara or the
// Proxmox GUI alike, and is a power action; "ha" is what is left — the guest's
// HA resource itself. Unmatched is "other" — still listed, under its own
// action name.
var guestHistoryRules = []HistoryRule{
	{Category: "power", Field: HistoryFieldEntry, Equals: []string{"status"}},
	{Category: "console", Field: HistoryFieldAction, Like: []string{"console%"}},
	{Category: "migration", Field: HistoryFieldAction, Like: []string{`drs\_%`, "%migrat%"}},
	{Category: "migration", Field: HistoryFieldWorker, Equals: []string{"qmigrate", "vzmigrate", "hamigrate", "harelocate"}},
	{Category: "snapshot", Field: HistoryFieldAction, Like: []string{"%snapshot%", "%rollback%"}},
	{Category: "snapshot", Field: HistoryFieldWorker, Equals: []string{
		"qmsnapshot", "qmdelsnapshot", "qmrollback", "vzsnapshot", "vzdelsnapshot", "vzrollback"}},
	{Category: "backup", Field: HistoryFieldAction, Like: []string{"%backup%", "%restore%"}},
	{Category: "backup", Field: HistoryFieldWorker, Equals: []string{"vzdump", "qmrestore", "vzrestore"}},
	{Category: "clone", Field: HistoryFieldAction, Like: []string{"%clone%"}},
	{Category: "clone", Field: HistoryFieldWorker, Equals: []string{"qmclone", "vzclone"}},
	{Category: "lifecycle", Field: HistoryFieldAction, Equals: []string{
		"create", "destroy", "vm_import", "convert-to-template", "convert_template"}},
	{Category: "lifecycle", Field: HistoryFieldWorker, Equals: []string{
		"qmcreate", "vzcreate", "qmdestroy", "vzdestroy", "qmtemplate", "vztemplate"}},
	{Category: "power", Field: HistoryFieldAction, Equals: []string{
		"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume", "pause"}},
	{Category: "power", Field: HistoryFieldWorker, Equals: []string{
		"qmstart", "qmstop", "qmshutdown", "qmreboot", "qmreset", "qmsuspend", "qmresume", "qmpause",
		"vzstart", "vzstop", "vzshutdown", "vzreboot", "vzsuspend", "vzresume", "hastart", "hastop"}},
	{Category: "ha", Field: HistoryFieldResourceType, Equals: []string{"ha_resource"}},
	{Category: "ha", Field: HistoryFieldWorker, Like: []string{"ha%"}},
	{Category: "migration", Field: HistoryFieldAction, Equals: []string{"disk_move", "move_disk", "volume_move"}},
	{Category: "migration", Field: HistoryFieldWorker, Equals: []string{"qmmove", "move_volume"}},
	{Category: "config", Field: HistoryFieldAction, Like: []string{"%config%", `disk\_%`, `media\_%`}},
	{Category: "config", Field: HistoryFieldAction, Equals: []string{"set_pool", "resize"}},
	{Category: "config", Field: HistoryFieldWorker, Equals: []string{"qmconfig", "resize"}},
}

// selectGuest is the guest kind's selector: the path's guest, from where its
// timeline starts.
func selectGuest(ctx context.Context, r HistoryRequest) (HistorySelection, error) {
	vmid := safeconv.Int32(int(r.Params.Int("vmid")))
	events, err := r.Queries.ListGuestLifecycleEvents(ctx, db.ListGuestLifecycleEventsParams{
		ClusterID: ClusterUUID(r.ClusterID),
		Vmid:      vmid,
	})
	if err != nil {
		return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the guest's history")
	}
	after, inclusive := guestHistoryCutoff(events)
	return HistorySelection{Guests: []HistoryGuest{{VMID: vmid, After: after, Inclusive: inclusive}}}, nil
}

// guestHistoryCutoff picks where a guest's timeline starts, from its lifecycle
// events newest first (ListGuestLifecycleEvents). The newest birth starts it,
// inclusive, so the create is the first entry. A newer destroy starts it after
// the destroy, exclusive: Proxmox reuses the lowest free VMID, so everything
// before it belongs to the previous guest that held the number.
//
// An event that failed, or is still running, is passed over: a create,
// restore or clone that failed made no guest — a failed clone into a VMID in
// use leaves the guest that holds it — and a failed destroy ended none. One
// whose end was never recorded counts: it is far likelier to be an old task
// whose row was pruned than one that failed.
//
// No event at all means no cutoff — the guest's whole recorded history.
func guestHistoryCutoff(events []db.ListGuestLifecycleEventsRow) (after pgtype.Timestamptz, inclusive bool) {
	for _, e := range events {
		switch historyTaskOutcome(e.TaskStatus, e.TaskExitStatus, e.RecordedStatus) {
		case historyOutcomeFailed, historyOutcomeRunning:
			continue
		}
		return pgtype.Timestamptz{Time: e.CreatedAt, Valid: true}, !e.IsDestroy
	}
	return pgtype.Timestamptz{}, false
}
