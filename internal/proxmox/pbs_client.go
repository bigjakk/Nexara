package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PBSClient communicates with a single Proxmox Backup Server.
type PBSClient struct {
	*apiClient
}

// NewPBSClient creates a PBSClient from the given config.
func NewPBSClient(cfg ClientConfig) (*PBSClient, error) {
	ac, err := newAPIClient(cfg, "PBSAPIToken")
	if err != nil {
		return nil, err
	}
	return &PBSClient{apiClient: ac}, nil
}

// GetDatastores returns all configured datastores.
func (c *PBSClient) GetDatastores(ctx context.Context) ([]PBSDatastore, error) {
	var stores []PBSDatastore
	if err := c.do(ctx, "/admin/datastore", &stores); err != nil {
		return nil, fmt.Errorf("get PBS datastores: %w", err)
	}
	return stores, nil
}

// GetDatastoreStatus returns usage status for all datastores.
func (c *PBSClient) GetDatastoreStatus(ctx context.Context) ([]PBSDatastoreStatus, error) {
	var status []PBSDatastoreStatus
	if err := c.do(ctx, "/status/datastore-usage", &status); err != nil {
		return nil, fmt.Errorf("get PBS datastore status: %w", err)
	}
	return status, nil
}

// TriggerGC triggers garbage collection on a datastore and returns the task UPID.
func (c *PBSClient) TriggerGC(ctx context.Context, store string) (string, error) {
	if store == "" {
		return "", fmt.Errorf("store name is required")
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/gc"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("trigger GC on %s: %w", store, err)
	}
	return upid, nil
}

// GetBackupGroups returns backup groups for a datastore.
func (c *PBSClient) GetBackupGroups(ctx context.Context, store string) ([]PBSBackupGroup, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/groups"
	var groups []PBSBackupGroup
	if err := c.do(ctx, path, &groups); err != nil {
		return nil, fmt.Errorf("get backup groups on %s: %w", store, err)
	}
	return groups, nil
}

// GetSnapshots returns all snapshots for a datastore.
func (c *PBSClient) GetSnapshots(ctx context.Context, store string) ([]PBSSnapshot, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/snapshots"
	var snaps []PBSSnapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("get snapshots on %s: %w", store, err)
	}
	return snaps, nil
}

// DeleteSnapshot deletes a specific snapshot.
func (c *PBSClient) DeleteSnapshot(ctx context.Context, store, backupType, backupID string, backupTime int64) error {
	if store == "" {
		return fmt.Errorf("store name is required")
	}
	params := url.Values{}
	params.Set("backup-type", backupType)
	params.Set("backup-id", backupID)
	params.Set("backup-time", strconv.FormatInt(backupTime, 10))
	path := "/admin/datastore/" + url.PathEscape(store) + "/snapshots?" + params.Encode()
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete snapshot on %s: %w", store, err)
	}
	return nil
}

// GetSyncJobs returns all configured sync jobs.
func (c *PBSClient) GetSyncJobs(ctx context.Context) ([]PBSSyncJob, error) {
	var jobs []PBSSyncJob
	if err := c.do(ctx, "/admin/sync", &jobs); err != nil {
		return nil, fmt.Errorf("get PBS sync jobs: %w", err)
	}
	return jobs, nil
}

// RunSyncJob triggers a sync job by ID and returns the task UPID.
func (c *PBSClient) RunSyncJob(ctx context.Context, jobID string) (string, error) {
	if jobID == "" {
		return "", fmt.Errorf("job ID is required")
	}
	path := "/admin/sync/" + url.PathEscape(jobID) + "/run"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("run sync job %s: %w", jobID, err)
	}
	return upid, nil
}

// GetPruneJobs returns every prune job configured on the server, with its
// last/next run.
//
// Returns every job and lets callers narrow it. /admin/prune does accept an
// optional `store`, but /config/prune rejects it outright ("schema does not
// allow additional properties"), so filtering locally keeps this independent
// of which of the two a future PBS serves — and the one consumer needs the
// full list anyway, to avoid a round-trip per datastore.
func (c *PBSClient) GetPruneJobs(ctx context.Context) ([]PBSPruneJob, error) {
	var jobs []PBSPruneJob
	if err := c.do(ctx, "/admin/prune", &jobs); err != nil {
		return nil, fmt.Errorf("get PBS prune jobs: %w", err)
	}
	return jobs, nil
}

// GetVerifyJobs returns all configured verify jobs.
func (c *PBSClient) GetVerifyJobs(ctx context.Context) ([]PBSVerifyJob, error) {
	var jobs []PBSVerifyJob
	if err := c.do(ctx, "/admin/verify", &jobs); err != nil {
		return nil, fmt.Errorf("get PBS verify jobs: %w", err)
	}
	return jobs, nil
}

// RunVerifyJob triggers a verify job by ID and returns the task UPID.
func (c *PBSClient) RunVerifyJob(ctx context.Context, jobID string) (string, error) {
	if jobID == "" {
		return "", fmt.Errorf("job ID is required")
	}
	path := "/admin/verify/" + url.PathEscape(jobID) + "/run"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("run verify job %s: %w", jobID, err)
	}
	return upid, nil
}

// ProtectSnapshot sets or clears the protected flag on a snapshot.
func (c *PBSClient) ProtectSnapshot(ctx context.Context, store, backupType, backupID string, backupTime int64, protect bool) error {
	if store == "" {
		return fmt.Errorf("store name is required")
	}
	qp := url.Values{}
	qp.Set("backup-type", backupType)
	qp.Set("backup-id", backupID)
	qp.Set("backup-time", strconv.FormatInt(backupTime, 10))
	if protect {
		qp.Set("protected", "true")
	} else {
		qp.Set("protected", "false")
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/protected?" + qp.Encode()
	if err := c.doPut(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("protect snapshot on %s: %w", store, err)
	}
	return nil
}

// UpdateSnapshotNotes updates the comment/notes on a snapshot.
func (c *PBSClient) UpdateSnapshotNotes(ctx context.Context, store, backupType, backupID string, backupTime int64, comment string) error {
	if store == "" {
		return fmt.Errorf("store name is required")
	}
	qp := url.Values{}
	qp.Set("backup-type", backupType)
	qp.Set("backup-id", backupID)
	qp.Set("backup-time", strconv.FormatInt(backupTime, 10))
	qp.Set("notes", comment)
	path := "/admin/datastore/" + url.PathEscape(store) + "/notes?" + qp.Encode()
	if err := c.doPut(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("update snapshot notes on %s: %w", store, err)
	}
	return nil
}

// validatePBSTaskUPID guards a caller-supplied UPID that becomes exactly one
// segment of /nodes/localhost/tasks/{upid}/…
//
// The API layer percent-DECODES this path parameter (pbsTaskUPIDFromParams in
// internal/api/handlers/backup.go), so what arrives here is the value the
// caller meant rather than the escaped text the route declaration matched. The
// check belongs HERE rather than at the handler, and rather than in the route
// declaration, for two reasons. The declaration matches the value as it
// ARRIVES, where "A%2F.." carries no slash, so it cannot see through an escape;
// and this client is the choke point every caller goes through, so a second
// route added later inherits the guard instead of having to remember it.
//
// It refuses the empty string, a "/", a control character, and a "." or ".."
// segment — bare, or standing between backslashes — and it is deliberately NOT
// validatePathSegment, which refuses every backslash. PBS writes the worker id through escape_id
// (proxmox-schema src/upid.rs): "/" becomes "-", and every other byte outside
// [A-Za-z0-9_.], plus a LEADING ".", becomes "\xNN". A backup task's worker id
// is "<store>:<type>/<id>" (proxmox-backup src/api2/backup/mod.rs), so it goes
// out as "datastore01\x3avm-100"; a verification job's is "<store>:<job id>",
// so "datastore01\x3av\x2d0001"; a GC on a datastore with a dash in its name is
// "datastore\x2d01". A backslash is in most real PBS UPIDs, and the user field
// can carry a raw one too — PBS allows any of [^\s:/[:cntrl:]] in a user name —
// so do not tighten this to "\x" followed by two hex digits. The first version
// of this guard delegated to validatePathSegment and answered 400 for every
// one of them, which left the task log and status routes working only for
// tasks whose worker id needed no escaping. Passing it is safe: url.PathEscape
// sends it as "%5C", and PBS decodes that back into the literal UPID it minted.
//
// None of this is the pveproxy exposure validatePathSegment exists for — there
// an escaped separator is decoded before the path is split, and a dot segment
// is taken literally rather than refused — because PBS does not decode before
// it splits the path and checks it for dots. proxmox-rest-server's
// normalize_path (proxmox repo, proxmox-rest-server/src/lib.rs) splits the RAW
// path on "/" and refuses any component that starts with ".", and only then
// does proxmox-router (Router::find_route) percent-decode each component on
// its own — so on PBS an escaped "%2F" stays inside the segment it arrived in,
// and a bare ".." is an error rather than a step upward. That is read from
// upstream source, not observed on a live server. It is why the refusals here
// are defence in depth rather than the only thing between a view:backup
// caller and another PBS endpoint: they give a value no PBS minted a clear
// local 400 instead of a PBS error, and they keep holding behind a reverse
// proxy that normalises the path before PBS sees it — including one that
// reads "\" as a separator, which is what the dot pieces between backslashes
// are refused for.
//
// That last refusal has a known cost, accepted on purpose. escape_id never
// writes such a piece — every backslash it writes is followed by an "x" — but
// PBS writes the USER field raw, and a PBS user name may be any run of
// [^\s:/[:cntrl:]]: a user called "a\..\b@pbs" is legal, and every task that
// user starts carries the piece. Exempting the user field would not narrow the
// defence, it would remove it, since a caller-supplied value puts its
// traversal wherever the exemption lies. So such a user's task logs cannot be
// read through Nexara; TestPBSClientTaskReadsRefuseAPathThatIsNotOneSegment
// pins the choice.
func validatePBSTaskUPID(upid string) error {
	if upid == "" {
		return fmt.Errorf("%w: UPID is required", ErrInvalidInput)
	}
	if strings.Contains(upid, "/") {
		return fmt.Errorf("%w: UPID %q must not contain a path separator", ErrInvalidInput, upid)
	}
	if hasControlChar(upid) {
		return fmt.Errorf("%w: UPID %q contains a control character", ErrInvalidInput, upid)
	}
	// A value with no backslash splits into one piece, itself, so this is
	// also the refusal of a bare "." or "..".
	for _, piece := range strings.Split(upid, `\`) {
		if piece == "." || piece == ".." {
			return fmt.Errorf("%w: UPID %q has a \".\" or \"..\" segment", ErrInvalidInput, upid)
		}
	}
	return nil
}

// GetTaskLog returns log lines for a PBS task.
func (c *PBSClient) GetTaskLog(ctx context.Context, upid string) ([]PBSTaskLogEntry, error) {
	if err := validatePBSTaskUPID(upid); err != nil {
		return nil, err
	}
	path := "/nodes/localhost/tasks/" + url.PathEscape(upid) + "/log?start=0&limit=5000"
	var entries []PBSTaskLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get PBS task log: %w", err)
	}
	return entries, nil
}

// PruneDatastore runs or dry-runs a prune operation on a datastore.
func (c *PBSClient) PruneDatastore(ctx context.Context, store string, params PBSPruneParams) ([]PBSPruneResult, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	form := url.Values{}
	if params.BackupType != "" {
		form.Set("backup-type", params.BackupType)
	}
	if params.BackupID != "" {
		form.Set("backup-id", params.BackupID)
	}
	if params.DryRun {
		form.Set("dry-run", "true")
	}
	if params.KeepLast > 0 {
		form.Set("keep-last", strconv.Itoa(params.KeepLast))
	}
	if params.KeepDaily > 0 {
		form.Set("keep-daily", strconv.Itoa(params.KeepDaily))
	}
	if params.KeepWeekly > 0 {
		form.Set("keep-weekly", strconv.Itoa(params.KeepWeekly))
	}
	if params.KeepMonthly > 0 {
		form.Set("keep-monthly", strconv.Itoa(params.KeepMonthly))
	}
	if params.KeepYearly > 0 {
		form.Set("keep-yearly", strconv.Itoa(params.KeepYearly))
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/prune"
	var results []PBSPruneResult
	if err := c.doPost(ctx, path, form, &results); err != nil {
		return nil, fmt.Errorf("prune datastore %s: %w", store, err)
	}
	return results, nil
}

// GetDatastoreConfig returns the full configuration of a PBS datastore.
//
// /config/datastore/{store}, not /admin/datastore/{store}: the latter is a
// directory-only router node with no GET of its own, so it answers with the
// list of subdirs below it — [{"subdir":"catalog"},{"subdir":"gc"},…]. Decoding
// that array into this struct failed on "cannot unmarshal array", which
// mapProxmoxError has no case for, so it surfaced as a 500 and the datastore
// config card rendered nothing.
//
// Every other admin/datastore call here names a leaf below the node (or, for
// GetDatastores, the collection above it). This is the only config read, and
// datastore.cfg is not served from /admin.
func (c *PBSClient) GetDatastoreConfig(ctx context.Context, store string) (*PBSDatastoreConfig, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	path := "/config/datastore/" + url.PathEscape(store)
	var config PBSDatastoreConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get datastore config for %s: %w", store, err)
	}
	return &config, nil
}

// GetDatastoreRRD returns RRD performance data for a datastore.
// timeframe: "hour", "day", "week", "month"
// cf: "AVERAGE" or "MAX"
func (c *PBSClient) GetDatastoreRRD(ctx context.Context, store, timeframe, cf string) ([]PBSDatastoreRRDEntry, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	if timeframe == "" {
		timeframe = "hour"
	}
	if cf == "" {
		cf = "AVERAGE"
	}
	path := "/admin/datastore/" + url.PathEscape(store) + "/rrd?timeframe=" + url.QueryEscape(timeframe) + "&cf=" + url.QueryEscape(cf)
	var entries []PBSDatastoreRRDEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get datastore RRD for %s: %w", store, err)
	}
	return entries, nil
}

// GetTasks returns recent tasks from the PBS node.
func (c *PBSClient) GetTasks(ctx context.Context, limit int) ([]PBSTask, error) {
	path := "/nodes/localhost/tasks"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var tasks []PBSTask
	if err := c.do(ctx, path, &tasks); err != nil {
		return nil, fmt.Errorf("get PBS tasks: %w", err)
	}
	return tasks, nil
}

// GetTaskStatus returns the status of a specific task.
func (c *PBSClient) GetTaskStatus(ctx context.Context, upid string) (*PBSTaskStatus, error) {
	if err := validatePBSTaskUPID(upid); err != nil {
		return nil, err
	}
	path := "/nodes/localhost/tasks/" + url.PathEscape(upid) + "/status"
	var status PBSTaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get PBS task status: %w", err)
	}
	return &status, nil
}
