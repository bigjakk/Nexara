package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// backupJobHistoryRoute is a Proxmox backup job's History.
func backupJobHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.BackupJobHistory(),
		Path: clusterScope + "/backup-jobs/:job_id/history",
		Description: "List what happened to one backup job, newest first: created, changed or deleted — in " +
			"Proxmox too — and run from Nexara, with each run's outcome. The job is the id Proxmox gave " +
			"it. The runs Proxmox starts on the job's schedule are not here: the task they start names no " +
			"job, so they are in the History of each guest they back up. The id is taken as the routes that " +
			"change the job take it. Only the rows of a job's own actions are read, so an id that is also " +
			"a guest's VMID or a node's name reads none of that guest's or node's backups. Requires " +
			"view:audit and view:backup on the cluster; a task's exit text needs view:task too. A cluster " +
			"that does not exist is a 404.",
		Resource: "backup",
		Parameters: clusterParams(apischema.Properties{
			"job_id": pveBackupJobIDParam,
		}),
	}
}
