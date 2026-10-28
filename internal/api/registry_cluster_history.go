package api

import (
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// clusterHistoryRoute is a cluster's History tab: view:audit and view:cluster
// on the cluster. A guest's rows in it are the caller's only under the
// guest's own view permission, as on the guest's page.
func clusterHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.ClusterHistory(),
		Path: clusterScope + "/history",
		Description: "List everything that happened in one cluster, newest first: every action Nexara recorded " +
			"in it — done in Nexara or in Proxmox — tasks with no audit entry, and its guests' status changes " +
			"no task explains and parts of backup jobs. A burst of sync failures is one entry, with how many " +
			"and between when, and one on each side of searched_from when it spans it. Each source is read " +
			"newest first up to 2,000 entries, or 20,000 narrowed by category or source, the source named " +
			"counted alone. " +
			historyStretchText + " Requires view:audit and view:cluster; a guest's entries need view:vm or " +
			"view:container by its type, and tasks with no audit entry and backup-job entries view:task.",
		Resource:   "cluster",
		Parameters: clusterParams(nil),
	}
}
