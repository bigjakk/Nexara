package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// storageHistoryRoute is a storage's History tab.
//
// Keyed on the storage's NAME, as Proxmox calls it on every node, for the
// reason guestHistoryRoute is keyed on the VMID: the storage_pools row behind
// storageScope's :storage_id is dropped and re-created by the collector, and a
// History has to outlive it. Its own collection, "storages", says so, since
// :storage_id beside it is a uuid.
func storageHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.StorageHistory(),
		Path: clusterScope + "/storages/:storage/history",
		Description: "List what happened to one storage, newest first: its definition added, changed or " +
			"removed (in Proxmox too), uploads, downloads and deletes of its content, disks moved onto it or " +
			"off it, and the backups and restores that name it, with each task's outcome. A storage is its " +
			"name in its cluster, as Proxmox calls it on every node, so the History outlives Nexara's " +
			"inventory row for the storage and never mixes in another cluster's storage of the same name. A " +
			"storage that is not shared is a different storage on every node: node leaves out what ran on " +
			"another node, and keeps what names none — the storage's configuration. Requires view:audit and " +
			"view:storage on the cluster; a task's exit text needs view:task too. A cluster that does not " +
			"exist is a 404.",
		Resource: "storage",
		Parameters: clusterParams(apischema.Properties{
			"storage": requiredStorage("Proxmox storage name, as the storage listing's `storage` field gives it."),
			"node": {
				Type:        apischema.String,
				Optional:    true,
				Pattern:     emptyOrNodeName,
				MaxLength:   apischema.Ptr(63),
				Typetext:    "<name>",
				Description: "Node to read the History of, for a storage that is not shared. Empty or omitted reads every node's.",
			},
		}),
	}
}
