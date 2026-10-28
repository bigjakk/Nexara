package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// pbsHistoryRoute is a PBS server's History, and one of its datastores'.
//
// A global kind: the server's rows carry the cluster it is attached to, or
// none, and a restore from it is filed in the cluster of the guest it
// restored. So its gate is global view:audit and global view:pbs — stricter
// than the server's own page, which a cluster-scoped holder reads for a server
// attached to their cluster — and global view:backup: most of its rows are the
// backups' (snapshots deleted or protected, restores), which view:backup owns
// beside the server itself.
func pbsHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.PBSHistory(),
		Path: pbsBackupScope + "/history",
		Description: "List what happened to one Proxmox Backup Server, newest first: the server added, " +
			"changed or removed, datastore garbage collections, prunes and snapshot changes, sync and " +
			"verify jobs run from Nexara — and, in PBS, changed — and the restores from it, with each " +
			"restore's outcome. With datastore, only what names that datastore: a restore names the " +
			"server and not the datastore, so it is the server's alone. Rows are kept across the " +
			"server's moves between clusters, and read in every cluster. Requires global view:audit, " +
			"global view:pbs and global view:backup; a task's exit text needs global view:task too.",
		Resource:      "pbs",
		AlsoResources: []string{"backup"},
		Parameters: pbsParams(apischema.Properties{
			"datastore": {
				Type:        apischema.String,
				Optional:    true,
				Pattern:     emptyOrPBSSafeID,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<datastore>",
				Description: "Datastore to read the History of. Empty or omitted reads the whole server's.",
			},
		}),
	}
}
