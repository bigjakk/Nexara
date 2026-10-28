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
			"job, so they are in the History of each guest they back up. Requires view:audit and " +
			"view:backup on the cluster; a task's exit text needs view:task too. A cluster that does not " +
			"exist is a 404.",
		Resource: "backup",
		Parameters: clusterParams(apischema.Properties{
			"job_id": pveBackupJobIDParam,
		}),
	}
}
