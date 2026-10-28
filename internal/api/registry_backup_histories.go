package api

import "github.com/bigjakk/nexara/internal/api/handlers"

// backupHistoryRoutes are the Histories of storage and backup: a storage's, a
// Proxmox Backup Server's and its datastores', a Veeam server's, job's and
// run's, and a Proxmox backup job's. Each opens on the window and reads no more
// rows than a folder's and a node's do (handlers.WithObjectWindow).
func backupHistoryRoutes() []historyRoute {
	routes := []historyRoute{
		storageHistoryRoute(),
		pbsHistoryRoute(),
		veeamServerHistoryRoute(),
		veeamJobHistoryRoute(),
		veeamSessionHistoryRoute(),
		backupJobHistoryRoute(),
	}
	for i := range routes {
		routes[i].Kind = handlers.WithObjectWindow(routes[i].Kind)
	}
	return routes
}
