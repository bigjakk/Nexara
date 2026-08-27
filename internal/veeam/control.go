package veeam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// Job control and session inspection — the write side of the integration.
//
// Every call here is ASYNC. A 201 means Veeam accepted the request, not that
// the job has started or stopped: the lab job took ~30s to reach Stopped after
// its stop returned. Callers must treat the returned session as a starting
// state to be refreshed by the poll loop, never as a final one.
//
// Paths are taken from vbr-1.3-rev2.swagger.json, not from Veeam's published
// HTML reference, which strips group prefixes and is wrong about several of
// them.

// JobRun reports what a start or stop actually did.
//
// The three states are distinct and the caller must not collapse them:
//
//   - Session non-nil    — accepted, and here is the run to track.
//   - NoObjects true     — accepted, but the job had nothing to process, so
//                          Veeam created no run at all. The documented 204 on
//                          start.
//   - both zero          — accepted, but the response carried no session
//                          Nexara can key on. Rare and contract-violating, and
//                          NOT a failure: Veeam has acted either way.
type JobRun struct {
	Session   *Session
	NoObjects bool
}


// StartJob starts a backup job and reports what Veeam did with the request.
//
// A JobRun with no session and no error is a real, documented outcome: 204
// means the request was accepted but the job had no objects to process, so no
// run exists. That is not a failure and must not be reported as one — but it
// is also not a run, and an operator who clicked "Run now" needs to be told
// the difference rather than watching for a session that will never appear.
//
// The 201 body is the Veeam analogue of capturing a Proxmox UPID: id, jobId,
// state, platformId and initiatedBy all arrive inline, so the session row can
// be written immediately with no polling.
func (c *Client) StartJob(ctx context.Context, jobID string) (JobRun, error) {
	return c.jobSession(ctx, jobID, "start")
}

// StopJob asks Veeam to stop a job's running session, reporting that session.
//
// Stopping a job that is not running is a 400 ("...job is not currently
// running."), which surfaces as an *APIError — deliberately not swallowed into
// a success, because "stop" silently succeeding on a job that is still running
// somewhere else is the wrong answer to give an operator.
func (c *Client) StopJob(ctx context.Context, jobID string) (JobRun, error) {
	return c.jobSession(ctx, jobID, "stop")
}

// jobSession runs a job action whose success carries a session inline.
//
// ⚠️ A 2xx is ACCEPTED, whatever the body turns out to hold. Once Veeam has
// answered 2xx it has taken the request, and no decoding problem afterwards
// can make that untrue — so a body we cannot use yields a JobRun with no
// session, never an error. Returning an error here would tell an operator that
// a job which is now running did not start, and invite the retry that starts
// it twice; for a stop it would also skip the nexara_stopped flag and raise
// the false failure alert this whole phase exists to prevent.
func (c *Client) jobSession(ctx context.Context, jobID, action string) (JobRun, error) {
	id, err := requireUUID("job id", jobID)
	if err != nil {
		return JobRun{}, err
	}

	body, status, err := c.do(ctx, http.MethodPost, "/api/v1/jobs/"+id+"/"+action)
	if err != nil {
		return JobRun{}, err
	}
	// 204 is the documented "nothing to back up" answer, and only start has
	// one. An empty body on any other 2xx is a server not keeping its
	// contract, so it is accepted-but-untracked rather than a claim about the
	// job's contents.
	if status == http.StatusNoContent {
		return JobRun{NoObjects: true}, nil
	}
	if len(body) == 0 {
		return JobRun{}, nil
	}

	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return JobRun{}, nil
	}
	// A session whose id did not survive the round trip is worse than none:
	// stored, it would key on the zero uuid and collide with every other row
	// that failed the same way. Dropped, not errored — see the note above.
	if _, perr := uuid.Parse(session.ID); perr != nil {
		return JobRun{}, nil
	}
	return JobRun{Session: &session}, nil
}

// EnableJob puts a job back on its schedule. 204, no body.
func (c *Client) EnableJob(ctx context.Context, jobID string) error {
	return c.jobAction(ctx, jobID, "enable")
}

// DisableJob takes a job off its schedule.
//
// Disruptive in a way "disable" undersells: the job stops running, so
// protection silently stops accruing while every existing restore point stays
// exactly where it was. Nothing in Veeam raises an alarm about it, which is
// why the UI confirms this one.
func (c *Client) DisableJob(ctx context.Context, jobID string) error {
	return c.jobAction(ctx, jobID, "disable")
}

// jobAction runs a job action with no meaningful response body.
func (c *Client) jobAction(ctx context.Context, jobID, action string) error {
	id, err := requireUUID("job id", jobID)
	if err != nil {
		return err
	}
	_, _, err = c.do(ctx, http.MethodPost, "/api/v1/jobs/"+id+"/"+action)
	return err
}

// StopSession asks Veeam to stop one running session directly.
//
// Answers 200 with an empty success envelope — no session comes back, unlike
// StopJob. The caller therefore already has to know which session it asked
// about, which is fine: it named it.
func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	id, err := requireUUID("session id", sessionID)
	if err != nil {
		return err
	}
	_, _, err = c.do(ctx, http.MethodPost, "/api/v1/sessions/"+id+"/stop")
	return err
}

// SessionLogRecord is one line of a session's log.
//
// Times are Veeam's own Timestamp, which tolerates the several formats the API
// has been seen to use for a date-time.
type SessionLogRecord struct {
	ID int `json:"id"`
	// Status is ETaskLogRecordStatus: None, Succeeded, Warning or Failed.
	Status      string     `json:"status"`
	StartTime   *Timestamp `json:"startTime"`
	UpdateTime  *Timestamp `json:"updateTime"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
}

// SessionLogs reads a session's log records.
//
// EXPECT THIS TO BE EMPTY for a session that was stopped. Verified live: Veeam
// returns no records at all for a killed session, so an empty log is not
// evidence of a problem with the request — it is the normal shape of a
// cancelled run, and the same reason a cancelled run cannot be told from a
// failed one by its result alone.
//
// Not paginated in the way the listings are: the response is a single
// {totalRecords, records} object rather than the {data, pagination} envelope.
func (c *Client) SessionLogs(ctx context.Context, sessionID string) ([]SessionLogRecord, error) {
	id, err := requireUUID("session id", sessionID)
	if err != nil {
		return nil, err
	}

	var body struct {
		TotalRecords int                `json:"totalRecords"`
		Records      []SessionLogRecord `json:"records"`
	}
	if err := c.get(ctx, "/api/v1/sessions/"+id+"/logs", &body); err != nil {
		return nil, err
	}
	return body.Records, nil
}

// TaskSession is one guest's outcome inside a run.
//
// This is the per-object breakdown Veeam's own console shows and its plain
// listings do not: which guest in a job failed, and when. Nothing else in the
// API answers it — a Proxmox job's object list is unreadable (GET /jobs/{id}
// is a 400) and backup objects are per-guest-per-server, not per-run.
//
// It carries PlatformID, so a task row is cluster-attributable on its own
// without a join back through its session.
//
// ⚠️ There is NO smbios uuid and no objectId here, only Name. Correlating a
// task row to a Nexara guest cannot use the deterministic tier, which is why
// the handler inherits the link from the backup object of the same name rather
// than inventing a second, weaker name match of its own.
type TaskSession struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	// SessionType at TASK level reads "EndpointBackup", not the
	// "PlatformBackupJob" its parent session reports. Verified live. Do not
	// filter task rows on the parent's value.
	SessionType  string        `json:"sessionType"`
	SessionID    string        `json:"sessionId"`
	PlatformID   string        `json:"platformId"`
	PlatformType string        `json:"platformType"`
	State        string        `json:"state"`
	Result       SessionResult `json:"result"`
	Algorithm    string        `json:"algorithm"`
	Progress     Progress      `json:"progress"`
	CreationTime Timestamp     `json:"creationTime"`
	EndTime      *Timestamp    `json:"endTime"`
}

// BackupTaskType is the ETaskSessionType value for a backup task. The others
// are Restore, Antivirus and Replica, none of which belong in a run's
// per-guest breakdown.
const BackupTaskType = "Backup"

// IsBackup reports whether this task is a backup of an object.
func (t TaskSession) IsBackup() bool { return t.Type == BackupTaskType }

// TaskSessions lists one run's per-guest task outcomes.
//
// ⚠️ A RUNNING session returns ZERO rows. Verified against a live run: task
// rows appear only as tasks finish, so this answers "what failed" after the
// fact and cannot drive live per-guest progress. An empty result for a run
// still in flight means "not reported yet", which is a different fact from an
// empty result for a finished one — callers must not render them the same way.
//
// Note this is the PLAIN endpoint and it does carry Proxmox rows, unlike
// /jobs and /backupInfrastructure/proxies, which hide them outside their
// /states variants (§2A). Verified rather than assumed, because that trap is
// two for two elsewhere.
func (c *Client) TaskSessions(ctx context.Context, sessionID string) ([]TaskSession, error) {
	id, err := requireUUID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	return listPaged[TaskSession](ctx, c, "/api/v1/sessions/"+id+"/taskSessions", nil, maxInventoryRows)
}

// requireUUID validates an id before it is concatenated into a request path.
//
// Every one of these ids is a uuid on the Veeam side, and every caller sources
// it from a row Nexara stored — but these are the first paths in this package
// built from a value that reached it through an HTTP handler, so the check is
// here rather than assumed upstream. It also normalises the spelling, so a
// path is never built from an id in a form the server did not give us.
func requireUUID(field, value string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: %s must be a uuid", ErrInvalidInput, field)
	}
	// The canonical spelling, not the caller's. uuid.Parse also accepts braced
	// and urn: forms, and String() folds every one of them back to the single
	// form the server issued — so the path is built from a value that cannot
	// carry a separator, and two spellings of one id cannot become two ids.
	return parsed.String(), nil
}
