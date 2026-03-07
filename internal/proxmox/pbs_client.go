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

// GetTaskLog returns log lines for a PBS task.
func (c *PBSClient) GetTaskLog(ctx context.Context, upid string) ([]PBSTaskLogEntry, error) {
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
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
func (c *PBSClient) GetDatastoreConfig(ctx context.Context, store string) (*PBSDatastoreConfig, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	path := "/admin/datastore/" + url.PathEscape(store)
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
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/localhost/tasks/" + url.PathEscape(upid) + "/status"
	var status PBSTaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get PBS task status: %w", err)
	}
	return &status, nil
}
