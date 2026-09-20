package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
// It exists because the API layer percent-DECODES this path parameter (see
// pbsTaskUPIDFromParams in internal/api/handlers/backup.go). Before that decode
// the value could not carry a traversal — "%2E%2E" was escaped a second time
// and named a task literally called "%2E%2E" — and after it, "A%2F..%2F..%2Fstatus"
// really does mean "A/../../status". url.PathEscape alone does not stop that:
// it turns the slashes back into "%2F", and the far side decodes those before
// it resolves the path. The capture-server run recorded on
// forbiddenVolumeIDChars (client_storage.go) is the evidence, and it is worth
// stating precisely: it watched a RAW "%2e%2e%2f" arrive byte-for-byte. That
// the far side then resolves it as "../" is the inference, and an escape this
// client produces reaches the same decoder. The request lands on whatever the traversal counts
// out to, a different endpoint reachable from a route granting only
// view:backup. Count the segments before quoting a destination: an earlier
// version of this comment named /nodes/localhost/status, which is one ".." too
// far.
//
// The check belongs HERE rather than at the handler, and rather than in the
// route declaration, for two reasons. The declaration matches the value as it
// ARRIVES, where "%2E%2E" carries no dot, so it cannot see through an escape;
// and this client is the choke point every caller goes through, so a second
// route added later inherits the guard instead of having to remember it.
//
// validatePathSegment is the shared rule the PVE client applies to most values
// of this shape — a pool name, an interface name, a volume group: it refuses
// the empty string, "." and "..", any "/" or "\", and any control character.
//
// A node name is now the same rule: validateNodeName (client.go) delegates
// here too. It was the family's loose end when this note was first written —
// refusing only "", "/" and a ".." SUBSTRING, so it took a bare ".", a
// backslash and any control character — and the bare "." was reachable,
// because extractNodeFromUPID reads the node out of a UPID the API layer has
// already decoded. Nothing on the PBS side ever depended on the weaker rule:
// these routes address the literal node "localhost".
//
// A UPID carries none of those. It is "UPID" followed by colon-separated hex, a
// worker type, a worker id and a user@realm; the colon and the "@" both survive
// the check, and a separator would have to come from the worker id — which no
// recorded UPID carries, nor do the fixtures the registry's own
// TestBackupPathSegmentsAreAnchored calls "a value the API itself hands back".
// A UPID that did carry a slash could not be addressed through one path segment
// in any case, so a refusal here is a clearer failure than a request that
// silently resolves somewhere else.
func validatePBSTaskUPID(upid string) error {
	return validatePathSegment("UPID", upid)
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
