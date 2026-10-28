package handlers

import "context"

// The Veeam kinds: a Veeam Backup & Replication server's History, a job's and
// one run's. All global: a Veeam server is Nexara's own row, and its job and
// session rows carry the cluster the job's platform maps to, or none. The ids
// Veeam gives its jobs and sessions are its own, not Nexara's: a restored copy
// of one server, added as a second, repeats every one. So a job's and a run's
// own rows are read within the server the path names (veeam_server_id, which
// every one of them carries), and not by the id alone. A session is a run of a
// job; it is stored in veeam_sessions, which is not a source of ListHistory — a
// run is shown beside the job's History as a live row
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

// VeeamJobHistory is a job's, within the server the path names: what was done
// to it — started, stopped, put on or taken off its schedule — and to its runs.
//
// A run's row names the job and the server, and a job's runs are read by both
// (one containment of the two keys), as the job's own rows are held to the
// server: a restored copy of a server that repeats the job's id adds neither.
func VeeamJobHistory() HistoryKind {
	return HistoryKind{
		Name:       "veeam-job",
		Categories: []string{"runs", "schedule", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "schedule", Field: HistoryFieldAction, Like: []string{`veeam\_job\_enabled%`, `veeam\_job\_disabled%`}},
			{Category: "runs", Field: HistoryFieldAction, Like: veeamControlActions},
		},
		Select:      selectVeeamJob,
		Global:      true,
		ObjectTypes: []string{"veeam_job", "veeam_session"},
	}
}

// selectVeeamJob is the job kind's selector. The job's own rows are the ones
// filed under its id that name the path's server, so a second server that
// repeats the id does not add its own; the stops of its runs name the job and
// the server, and are the ones that name both.
func selectVeeamJob(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	server, job := r.Params.String("id"), r.Params.String("job_id")
	return HistorySelection{Objects: []HistoryObject{
		{ResourceType: "veeam_job", ResourceID: job, DetailKey: "veeam_server_id", Value: server},
		{ResourceType: "veeam_session", DetailKey: "veeam_job_id", Value: job, AndKey: "veeam_server_id", AndValue: server},
	}}, nil
}

// VeeamSessionHistory is one run's, within the server the path names: what was
// done to it.
func VeeamSessionHistory() HistoryKind {
	return HistoryKind{
		Name:       "veeam-session",
		Categories: []string{"stop", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "stop", Field: HistoryFieldAction, Like: []string{`veeam\_session\_stop%`}},
		},
		Select:      selectVeeamSession,
		Global:      true,
		ObjectTypes: []string{"veeam_session"},
	}
}

// selectVeeamSession is the run kind's selector: the rows filed under the run's
// id that name the path's server.
func selectVeeamSession(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	server, session := r.Params.String("id"), r.Params.String("session_id")
	return HistorySelection{Objects: []HistoryObject{
		{ResourceType: "veeam_session", ResourceID: session, DetailKey: "veeam_server_id", Value: server},
	}}, nil
}

// veeamControlActions are the job and session control actions, which a
// failure of is the action and "_failed".
var veeamControlActions = []string{`veeam\_job\_%`, `veeam\_session\_%`}
