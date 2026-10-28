package handlers

import "context"

// BackupJobHistory is a Proxmox backup job's: what was done to it, and the
// runs asked for from Nexara. A job is the id Proxmox gave it, never a name or
// a schedule, which change; and its rows are filed under that id (backup).
// The runs the scheduler starts name no job in the Proxmox task they start —
// the task's id holds a guest, or nothing — so they are the guests' and the
// node's, and not here.
//
// The type is shared: a guest's ad-hoc backup and its restores are filed under
// its VMID and a node's ingested vzdump tasks under the node's name, all as
// backup. A job's id is held to the actions that are a job's (backup_job_*),
// which every row of it has, from before its create row named it in details
// too — so a job whose id is a number or a node's name, as one made without an
// id by Proxmox's UUID can be, reads its own rows and not the guest's or the
// node's.
func BackupJobHistory() HistoryKind {
	return HistoryKind{
		Name:       "backup-job",
		Categories: []string{"config", "runs", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "config", Field: HistoryFieldEntry, Equals: []string{"observed"}},
			{Category: "config", Field: HistoryFieldAction, Equals: []string{
				"backup_job_created", "backup_job_updated", "backup_job_deleted"}},
			{Category: "runs", Field: HistoryFieldAction, Like: []string{`backup\_job\_run%`}},
			{Category: "runs", Field: HistoryFieldWorker, Equals: []string{"vzdump"}},
		},
		Select:      selectBackupJob,
		ObjectTypes: []string{"backup"},
		// A job edited in Proxmox is seen by the observer.
		ObservedTypes: []HistoryObservedType{{Type: "backup"}},
	}
}

// selectBackupJob is the backup-job kind's selector: the rows filed under the
// path's job id with a job's actions. The one id it reads otherwise is
// backupJobIDUnknown, which a create files its row under when it could not tell
// which job it made: every such create, of any job, is filed there and names no
// job in details, so that id is read only by the rows that do (job_id) — which
// a job really called so has from its create on — and the unknown creates are
// in no job's History.
func selectBackupJob(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	job := r.Params.String("job_id")
	if job == backupJobIDUnknown {
		return HistorySelection{Objects: []HistoryObject{
			{ResourceType: "backup", ResourceID: job, DetailKey: "job_id", Value: job},
		}}, nil
	}
	return HistorySelection{Objects: []HistoryObject{
		{ResourceType: "backup", Value: job, ActionLike: `backup\_job\_%`},
	}}, nil
}
