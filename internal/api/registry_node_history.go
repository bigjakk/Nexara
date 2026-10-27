package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// nodeHistoryRoute is a node's History tab: view:audit and view:node on the
// cluster. The node name is checked against the cluster's nodes before the
// handler runs, as on every other node route.
func nodeHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.NodeHistory(),
		Path: nodeScope + "/:node_name/history",
		Description: "List what happened to one node, newest first: its updates, boots, shutdowns, maintenance " +
			"and option changes, its network, repository, OSD, storage, firewall-rule and certificate changes, " +
			"the tasks that ran on it — done in Nexara or in Proxmox — its status changes, and the guests that " +
			"arrived on it or left it. A guest's task, and any other entry naming a guest but a move, is listed " +
			"only with include_guests. A status change through unknown that is back within two minutes is not " +
			"listed. Requires view:audit and view:node; a guest's entries need view:vm or view:container by " +
			"its type, and tasks with no audit entry view:task.",
		Resource:   "node",
		Parameters: clusterParams(apischema.Properties{"node_name": apischema.StdOption("node-name")}),
	}
}
