package handlers

import (
	"context"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// UnfiledFolderSegment is the folder path segment of the guests in no folder,
// which the sidebar tree calls Discovered (frontend folder-tree.ts).
const UnfiledFolderSegment = "unassigned"

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
		Select:      selectFolder,
		ObjectTypes: []string{"vm_folder", "vm_folder_membership"},
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

	sel := HistorySelection{Guests: make([]HistoryGuest, 0, len(vmids))}
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
// one of the cluster's folders.
func folderSubtree(all []db.VmFolder, id uuid.UUID) []uuid.UUID {
	if !slices.ContainsFunc(all, func(f db.VmFolder) bool { return f.ID == id }) {
		return nil
	}
	subtree := []uuid.UUID{id}
	for i := 0; i < len(subtree); i++ {
		for _, f := range all {
			if f.ParentID == (pgtype.UUID{Bytes: subtree[i], Valid: true}) {
				subtree = append(subtree, f.ID)
			}
		}
	}
	return subtree
}
