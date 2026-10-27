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
			"and between when. An unfiltered read holds the newest 2,000 entries of each source, and its " +
			"total stops at 2,001; a filtered one is not capped. Requires view:audit and view:cluster; a " +
			"guest's entries need view:vm or view:container by its type, and tasks with no audit entry and " +
			"backup-job entries view:task.",
		Resource:   "cluster",
		Parameters: clusterParams(nil),
	}
}
