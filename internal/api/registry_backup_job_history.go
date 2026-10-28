package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// backupJobHistoryIDParam is the job id of a backup job's History: the shape
// Proxmox gives one, a letter and then letters, digits, underscore and dash
// (pve_verify_configid), by the catalogue's rule for naming an object that
// already exists. It is stricter than the id of the routes that change a job
// (pveBackupJobIDParam, a traversal anchor) on purpose: this id is read as the
// resource id of a backup row, and a guest's VMID is one too, so a number is a
// guest's backups and not a job's.
var backupJobHistoryIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("pve-configid-existing"),
	MaxLength:   apischema.Ptr(128),
	Typetext:    "<job id>",
	Description: "PVE backup job id, as GET /clusters/{cluster_id}/backup-jobs reports it.",
}

// backupJobHistoryRoute is a Proxmox backup job's History.
func backupJobHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.BackupJobHistory(),
		Path: clusterScope + "/backup-jobs/:job_id/history",
		Description: "List what happened to one backup job, newest first: created, changed or deleted — in " +
			"Proxmox too — and run from Nexara, with each run's outcome. The job is the id Proxmox gave " +
			"it. The runs Proxmox starts on the job's schedule are not here: the task they start names no " +
			"job, so they are in the History of each guest they back up. A job id starts with a letter, as " +
			"Proxmox's do: a number is a guest's backups, not a job. Requires view:audit and " +
			"view:backup on the cluster; a task's exit text needs view:task too. A cluster that does not " +
			"exist is a 404.",
		Resource: "backup",
		Parameters: clusterParams(apischema.Properties{
			"job_id": backupJobHistoryIDParam,
		}),
	}
}
