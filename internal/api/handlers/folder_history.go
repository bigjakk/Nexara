package handlers

import (
	"context"
	"log/slog"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// UnfiledFolderSegment is the folder path segment of the guests in no folder,
// which the sidebar tree calls Discovered (frontend folder-tree.ts).
const UnfiledFolderSegment = "unassigned"

// folderHistoryGuestCap is the most guests a folder's History follows — the
// ones with the latest audit rows (ListMostActiveGuests) — as the Tasks tab it
// replaced filtered at most 500. Each guest costs its own rows and its
// lifecycle read, so a folder of thousands, or Discovered on a big cluster,
// would cost thousands of guests' Histories. The response says how many it
// follows of how many past it (HistorySelection.GuestsTotal), which the page
// shows.
const folderHistoryGuestCap = 500

// FolderHistory is the folder kind: what happened to the guests filed in a
// folder or below it, and to those folders themselves. A member's whole
// History is the folder's, from that guest's own cutoff, as on its own page: a
// folder is a view over its guests, as its Tasks tab was, and when a guest
// joined is not on record for memberships older than this release. A guest or
// a subfolder that left, and a subfolder deleted, show only where the row
// recorded the folder it left (from_folder_id, from_parent_id, a delete's
// parent_id), which they do from this release on.
func FolderHistory() HistoryKind {
	guest := GuestHistory()
	return HistoryKind{
		Name:       "folder",
		Categories: slices.Insert(slices.Clone(guest.Categories), len(guest.Categories)-1, "folder"),
		Default:    "other",
		Rules: append([]HistoryRule{
			{Category: "folder", Field: HistoryFieldResourceType, Equals: []string{"vm_folder", "vm_folder_membership"}},
		}, guestHistoryRules...),
		Select:         selectFolder,
		ObjectTypes:    []string{"vm_folder", "vm_folder_membership"},
		DefaultWindow:  objectHistoryDefaultWindow,
		RowCap:         objectHistoryRowCap,
		FilteredRowCap: objectHistoryRowCap,
	}
}

// selectFolder is the folder kind's selector: the guests filed in the folder
// or below it, each from its own cutoff, and the folders' own rows — their
// creation, renames and moves, the subfolders made, moved in or out of them or
// deleted, and the guests filed into and out of them. The folder and its
// subtree are the path's cluster's: a folder of another cluster is not found.
// Unfiled, it is the guests in no folder, and no folder's rows.
func selectFolder(ctx context.Context, r HistoryRequest) (HistorySelection, error) {
	var vmids []int32
	var folders []uuid.UUID
	if param := r.Params.String("folder_id"); param == UnfiledFolderSegment {
		unfiled, err := r.Queries.ListUnfiledGuests(ctx, r.ClusterID)
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder")
		}
		vmids = unfiled
	} else {
		id, err := uuid.Parse(param)
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusBadRequest, "Invalid folder id")
		}
		all, err := r.Queries.ListVMFoldersByCluster(ctx, r.ClusterID)
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder")
		}
		if folders = folderSubtree(all, id); len(folders) == 0 {
			return HistorySelection{}, fiber.NewError(fiber.StatusNotFound, "Folder not found")
		}
		members, err := r.Queries.ListVMFolderMembers(ctx, r.ClusterID)
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder")
		}
		for _, m := range members {
			if slices.Contains(folders, m.FolderID) {
				vmids = append(vmids, m.Vmid)
			}
		}
	}

	slices.Sort(vmids)
	vmids = slices.Compact(vmids)
	sel := HistorySelection{}
	// Only the members the caller may open count, and are followed: the rest
	// would take places under the cap that viewableGuests then empties, and
	// show in the count.
	if len(vmids) > 0 {
		types, err := r.Queries.ListGuestTypes(ctx, db.ListGuestTypesParams{ClusterID: r.ClusterID, Vmids: vmids})
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder")
		}
		members := make([]HistoryGuest, 0, len(vmids))
		for _, v := range vmids {
			members = append(members, HistoryGuest{VMID: v})
		}
		vmids = vmids[:0]
		for _, g := range viewableGuests(members, types, r.GuestTypes) {
			vmids = append(vmids, g.VMID)
		}
	}
	if len(vmids) > folderHistoryGuestCap {
		sel.GuestsTotal = len(vmids)
		active, err := r.Queries.ListMostActiveGuests(ctx, db.ListMostActiveGuestsParams{
			ClusterID: r.ClusterID, Vmids: vmids, RowLimit: folderHistoryGuestCap,
		})
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder")
		}
		vmids = active
	}
	sel.Guests = make([]HistoryGuest, 0, len(vmids))
	if len(vmids) > 0 {
		events, err := r.Queries.ListGuestLifecycleEvents(ctx, db.ListGuestLifecycleEventsParams{
			ClusterID: ClusterUUID(r.ClusterID),
			Vmids:     vmids,
		})
		if err != nil {
			return HistorySelection{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the folder's history")
		}
		byGuest := make(map[int32][]db.ListGuestLifecycleEventsRow, len(vmids))
		for _, e := range events {
			byGuest[e.Vmid] = append(byGuest[e.Vmid], e)
		}
		for _, vmid := range vmids {
			after, inclusive := guestHistoryCutoff(byGuest[vmid])
			sel.Guests = append(sel.Guests, HistoryGuest{VMID: vmid, After: after, Inclusive: inclusive})
		}
	}
	for _, f := range folders {
		id := f.String()
		sel.Objects = append(sel.Objects,
			HistoryObject{ResourceType: "vm_folder", Value: id},
			HistoryObject{ResourceType: "vm_folder", DetailKey: "parent_id", Value: id},
			HistoryObject{ResourceType: "vm_folder", DetailKey: "from_parent_id", Value: id},
			HistoryObject{ResourceType: "vm_folder_membership", DetailKey: "folder_id", Value: id},
			HistoryObject{ResourceType: "vm_folder_membership", DetailKey: "from_folder_id", Value: id})
	}
	return sel, nil
}

// folderSubtree is id and every folder below it, or nothing when id is not
// one of the cluster's folders. Each folder is taken once and the walk stops
// folderDepthCap levels down: a cycle in parent_id — two concurrent moves could
// leave one before moves took the cluster's folder lock — would otherwise loop
// here for ever, out of reach of the query timeout. Either is logged.
func folderSubtree(all []db.VmFolder, id uuid.UUID) []uuid.UUID {
	if !slices.ContainsFunc(all, func(f db.VmFolder) bool { return f.ID == id }) {
		return nil
	}
	children := make(map[uuid.UUID][]uuid.UUID, len(all))
	for _, f := range all {
		if f.ParentID.Valid {
			parent := uuid.UUID(f.ParentID.Bytes)
			children[parent] = append(children[parent], f.ID)
		}
	}
	seen := map[uuid.UUID]bool{id: true}
	subtree, level := []uuid.UUID{id}, []uuid.UUID{id}
	for depth := 0; len(level) > 0; depth++ {
		if depth == folderDepthCap {
			slog.Warn("folder tree deeper than the History reads", "folder_id", id, "depth", depth)
			break
		}
		var next []uuid.UUID
		for _, parent := range level {
			for _, child := range children[parent] {
				if seen[child] {
					slog.Warn("folder tree has a cycle; the History reads each folder once",
						"folder_id", id, "parent_id", parent, "child_id", child)
					continue
				}
				seen[child] = true
				next = append(next, child)
			}
		}
		subtree = append(subtree, next...)
		level = next
	}
	return subtree
}
