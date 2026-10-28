package handlers

// BackupJobHistory is a Proxmox backup job's: what was done to it, and the
// runs asked for from Nexara. A job is the id Proxmox gave it, never a name or
// a schedule, which change; and its rows are filed under that id (backup).
// The runs the scheduler starts name no job in the Proxmox task they start —
// the task's id holds a guest, or nothing — so they are the guests' and the
// node's, and not here.
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
		Select:      SelectObjects("job_id", HistoryObject{ResourceType: "backup"}),
		ObjectTypes: []string{"backup"},
		// A job edited in Proxmox is seen by the observer.
		ObservedTypes: []HistoryObservedType{{Type: "backup"}},
	}
}
