package handlers

// The Veeam kinds: a Veeam Backup & Replication server's History, a job's and
// one run's. All global: a Veeam server is Nexara's own row, and the ids Veeam
// gives its jobs and sessions are unique across the servers Nexara knows; their
// rows carry the cluster the job's platform maps to, or none. A session is a
// run of a job; it is stored in veeam_sessions, which is not a source of
// ListHistory — a run is shown beside the job's History as a live row
// (frontend/src/features/backup/veeam), and what is on record here is what was
// done to it.

// VeeamServerHistory is a Veeam server's: its own rows — added, changed,
// removed, tested, a platform mapped to a cluster, a backup object mapped to a
// guest — and every job and session control action taken on it, which name the
// server in details.
func VeeamServerHistory() HistoryKind {
	return HistoryKind{
		Name:       "veeam-server",
		Categories: []string{"server", "connection", "mapping", "jobs", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "server", Field: HistoryFieldAction, Equals: []string{
				"veeam_server_created", "veeam_server_updated", "veeam_server_deleted"}},
			{Category: "connection", Field: HistoryFieldAction, Equals: []string{
				"veeam_server_tested", "veeam_server_connect_failed", "veeam_server_credential_redirect_refused"}},
			{Category: "mapping", Field: HistoryFieldAction, Like: []string{`veeam\_platform\_%`, `veeam\_object\_%`}},
			{Category: "jobs", Field: HistoryFieldAction, Like: veeamControlActions},
		},
		Select: SelectObjects("id",
			HistoryObject{ResourceType: "veeam_server"},
			HistoryObject{ResourceType: "veeam_job", DetailKey: "veeam_server_id"},
			HistoryObject{ResourceType: "veeam_session", DetailKey: "veeam_server_id"}),
		Global:      true,
		ObjectTypes: []string{"veeam_server", "veeam_job", "veeam_session"},
	}
}

// VeeamJobHistory is a job's: what was done to it — started, stopped, put on
// or taken off its schedule — and to its runs.
func VeeamJobHistory() HistoryKind {
	return HistoryKind{
		Name:       "veeam-job",
		Categories: []string{"runs", "schedule", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "schedule", Field: HistoryFieldAction, Like: []string{`veeam\_job\_enabled%`, `veeam\_job\_disabled%`}},
			{Category: "runs", Field: HistoryFieldAction, Like: veeamControlActions},
		},
		Select: SelectObjects("job_id",
			HistoryObject{ResourceType: "veeam_job"},
			HistoryObject{ResourceType: "veeam_session", DetailKey: "veeam_job_id"}),
		Global:      true,
		ObjectTypes: []string{"veeam_job", "veeam_session"},
	}
}

// VeeamSessionHistory is one run's: what was done to it.
func VeeamSessionHistory() HistoryKind {
	return HistoryKind{
		Name:       "veeam-session",
		Categories: []string{"stop", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "stop", Field: HistoryFieldAction, Like: []string{`veeam\_session\_stop%`}},
		},
		Select:      SelectObjects("session_id", HistoryObject{ResourceType: "veeam_session"}),
		Global:      true,
		ObjectTypes: []string{"veeam_session"},
	}
}

// veeamControlActions are the job and session control actions, which a
// failure of is the action and "_failed".
var veeamControlActions = []string{`veeam\_job\_%`, `veeam\_session\_%`}
