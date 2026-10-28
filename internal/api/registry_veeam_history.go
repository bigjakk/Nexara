package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The Veeam Histories: a server's, a job's and a run's. Global kinds, gated on
// global view:audit and global view:veeam: what they read spans every cluster
// a server protects, as the server's own listings do, which is why a
// cluster-scoped holder of view:veeam reads the jobs and runs of their cluster
// and none of these.

// veeamServerHistoryRoute is a Veeam server's History.
func veeamServerHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.VeeamServerHistory(),
		Path: veeamScope + "/:id/history",
		Description: "List what happened to one Veeam Backup & Replication server, newest first: the server " +
			"added, changed or removed, its connection tested, a platform mapped to a cluster or unmapped, " +
			"and every job started, stopped, enabled or disabled and every run stopped on it. The runs " +
			"themselves are in the server's run listing. Requires global view:audit and global view:veeam.",
		Resource:   "veeam",
		Parameters: veeamServerParams(nil),
	}
}

// veeamJobHistoryRoute is one Veeam job's History.
func veeamJobHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.VeeamJobHistory(),
		Path: veeamScope + "/:id/jobs/:job_id/history",
		Description: "List what happened to one Veeam job, newest first: started, stopped, put on or taken " +
			"off its schedule, and its runs stopped. The job is Veeam's own id for it. The runs themselves " +
			"are in the server's run listing. Requires global view:audit and global view:veeam.",
		Resource: "veeam",
		Parameters: veeamServerParams(apischema.Properties{
			"job_id": veeamUpstreamIDParam(
				"Job id as VEEAM assigns it (veeam_id in the job listing), not Nexara's row id."),
		}),
	}
}

// veeamSessionHistoryRoute is one Veeam run's History.
func veeamSessionHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.VeeamSessionHistory(),
		Path: veeamScope + "/:id/sessions/:session_id/history",
		Description: "List what happened to one Veeam run, newest first: a stop asked for from Nexara, and " +
			"whether Veeam took it. The run is Veeam's own id for it. Requires global view:audit and " +
			"global view:veeam.",
		Resource:   "veeam",
		Parameters: veeamSessionParams(),
	}
}
