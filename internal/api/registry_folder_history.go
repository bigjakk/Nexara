package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// folderHistoryIDParam is the folder a History is read for: its row id, or
// handlers.UnfiledFolderSegment for the guests in no folder, as the folder
// page's own path names them.
var folderHistoryIDParam = apischema.Property{
	Type:   apischema.String,
	Source: apischema.SourcePath,
	Pattern: "^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|" +
		handlers.UnfiledFolderSegment + ")$",
	Typetext:    "<uuid|" + handlers.UnfiledFolderSegment + ">",
	Description: "Folder identifier, or " + handlers.UnfiledFolderSegment + " for the guests in no folder.",
}

// folderHistoryRoute is a folder's History tab: view:audit and view:vm_folder
// on the cluster. Its guests are each the caller's only under the guest's own
// view permission (HistoryHandler.List drops the rest), as on their pages.
func folderHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.FolderHistory(),
		Path: vmFolderScope + "/:folder_id/history",
		Description: "List what happened to the guests in one folder and the folders below it, newest first — " +
			"each guest's History from its own create, clone or restore, as on its own page — and to those " +
			"folders: their creation, renames and moves, and the guests filed into and out of them. A guest's " +
			"departure is listed for moves made from this release on. " + handlers.UnfiledFolderSegment +
			" as the folder lists the guests in no folder. Of more than 500 guests the caller may open it " +
			"follows the 500 most recently active, and guests gives how many of how many. Each source is read " +
			"newest first up to 20,000 entries, all_time too; past that the total stops at 20,001, with no " +
			"page past it, and searched_from gives how far back the read is whole. Requires view:audit and " +
			"view:vm_folder; a guest is " +
			"listed only with view:vm or view:container by its type, and tasks with no audit entry and " +
			"backup-job entries need view:task.",
		Resource:   "vm_folder",
		Parameters: clusterParams(apischema.Properties{"folder_id": folderHistoryIDParam}),
	}
}
