package handlers

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auditkey"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// HAHistory is the HA tab's kind: the cluster's HA resources, groups, rules
// and arming, told apart by type.
func HAHistory() HistoryKind {
	return HistoryKind{
		Name:       "ha",
		Categories: []string{"resources", "groups", "rules", "arming", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("resources", "ha_resource"),
			typeRule("groups", "ha_group"),
			typeRule("rules", "ha_rule"),
			typeRule("arming", "ha"),
		},
		Select:        SelectTypes("ha_resource", "ha_group", "ha_rule", "ha"),
		ObjectTypes:   []string{"ha_resource", "ha_group", "ha_rule", "ha"},
		ObservedTypes: observedOf("ha_resource", "ha_group", "ha_rule"),
	}
}

// HAGroupHistory is one HA group's kind.
func HAGroupHistory() HistoryKind {
	return objectHistory("ha-group", "group", []string{"ha_group"}, "ha_group")
}

// HARuleHistory is one HA rule's kind. The DRS tab's rows of Proxmox's own HA
// rules read it too: a rule is the same object from either page.
func HARuleHistory() HistoryKind {
	return objectHistory("ha-rule", "rule", []string{"ha_rule"}, "ha_rule")
}

// HAResourceHistory is one HA resource's kind: its own rows, and the HA
// manager's tasks on its guest — the start, stop, migration and relocation it
// ordered, which are the guest's tasks and carry no HA resource.
func HAResourceHistory() HistoryKind {
	return HistoryKind{
		Name:       "ha-resource",
		Categories: []string{"lifecycle", "config", "tasks", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "tasks", Field: HistoryFieldWorker, Like: []string{"ha%"}},
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select:        selectHAResource,
		ObjectTypes:   []string{"ha_resource"},
		ObservedTypes: observedOf("ha_resource"),
	}
}

// selectHAResource names the resource by the sid its rows are filed under —
// "vm:109", as Proxmox spells it — or, for a bare VMID, by every spelling a row
// may have kept (haSIDKey files a bare id as the sid when it can tell the type,
// and as sent when it cannot). Its guest is the VMID the sid carries, from the
// guest's own start (guestHistoryCutoff): a previous holder of the VMID is not
// this resource's, though it too may have had one. Only the guest's HA tasks
// come in with it ("ha%": hastart, hastop, hamigrate, harelocate), and only for
// a caller who may open the guest (viewableGuests).
func selectHAResource(ctx context.Context, r HistoryRequest) (HistorySelection, error) {
	sid := decodeParamValue(r.Params.String("sid"))
	sids := []string{sid}
	var vmid int32
	if bare, ok := auditkey.BareVMID(sid); ok {
		vmid, sids = bare, []string{sid, "vm:" + sid, "ct:" + sid}
	} else if kind, id, typed := strings.Cut(sid, ":"); typed && (kind == "vm" || kind == "ct") {
		vmid, _ = auditkey.BareVMID(id)
	}
	sel := HistorySelection{Objects: make([]HistoryObject, len(sids))}
	for i, s := range sids {
		sel.Objects[i] = HistoryObject{ResourceType: "ha_resource", Value: s}
	}
	if vmid == 0 {
		return sel, nil
	}
	events, err := r.Queries.ListGuestLifecycleEvents(ctx, db.ListGuestLifecycleEventsParams{
		ClusterID: ClusterUUID(r.ClusterID),
		Vmids:     []int32{vmid},
	})
	if err != nil {
		return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the resource's history")
	}
	after, inclusive := guestHistoryCutoff(events)
	sel.Guests = []HistoryGuest{{VMID: vmid, After: after, Inclusive: inclusive}}
	sel.GuestWorkers = []string{"ha%"}
	return sel, nil
}
