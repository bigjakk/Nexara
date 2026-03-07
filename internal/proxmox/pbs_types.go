package proxmox

// PBSDatastore represents a datastore from GET /api2/json/admin/datastore.
type PBSDatastore struct {
	Name    string `json:"store"`
	Path    string `json:"path,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// PBSDatastoreStatus represents usage from GET /api2/json/status/datastore-usage.
type PBSDatastoreStatus struct {
	Store string `json:"store"`
	Total int64  `json:"total"`
	Used  int64  `json:"used"`
	Avail int64  `json:"avail"`
}

// PBSGCStatus represents the garbage collection status.
type PBSGCStatus struct {
	UPID string `json:"upid,omitempty"`
}

// PBSBackupGroup represents a backup group from GET /api2/json/admin/datastore/{store}/groups.
type PBSBackupGroup struct {
	BackupType string `json:"backup-type"`
	BackupID   string `json:"backup-id"`
	LastBackup int64  `json:"last-backup"`
	BackupCount int   `json:"backup-count"`
	Owner      string `json:"owner,omitempty"`
}

// PBSSnapshot represents a snapshot from GET /api2/json/admin/datastore/{store}/snapshots.
type PBSSnapshot struct {
	BackupType string `json:"backup-type"`
	BackupID   string `json:"backup-id"`
	BackupTime int64  `json:"backup-time"`
	Size       int64  `json:"size,omitempty"`
	Verification *PBSVerificationStatus `json:"verification,omitempty"`
	Protected  bool   `json:"protected,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Owner      string `json:"owner,omitempty"`
}

// PBSVerificationStatus represents snapshot verification info.
type PBSVerificationStatus struct {
	State   string `json:"state"`
	UPID    string `json:"upid,omitempty"`
}

// PBSSyncJob represents a sync job from GET /api2/json/admin/sync.
type PBSSyncJob struct {
	ID          string `json:"id"`
	Store       string `json:"store"`
	Remote      string `json:"remote,omitempty"`
	RemoteStore string `json:"remote-store,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	LastRunState string `json:"last-run-state,omitempty"`
	LastRunEndtime int64 `json:"last-run-endtime,omitempty"`
	NextRun     int64  `json:"next-run,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

// PBSVerifyJob represents a verify job from GET /api2/json/admin/verify.
type PBSVerifyJob struct {
	ID           string `json:"id"`
	Store        string `json:"store"`
	Schedule     string `json:"schedule,omitempty"`
	LastRunState string `json:"last-run-state,omitempty"`
	LastRunEndtime int64 `json:"last-run-endtime,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

// PBSTask represents a task from GET /api2/json/nodes/localhost/tasks.
type PBSTask struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	PID       int    `json:"pid"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime,omitempty"`
	Status    string `json:"status,omitempty"`
	Type      string `json:"worker_type"`
	User      string `json:"user"`
}

// PBSTaskStatus represents a detailed task status.
type PBSTaskStatus struct {
	UPID       string `json:"upid"`
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus,omitempty"`
	Type       string `json:"worker_type"`
	StartTime  int64  `json:"starttime"`
	EndTime    int64  `json:"endtime,omitempty"`
}

// PBSTaskLogEntry represents a single log line from a PBS task.
type PBSTaskLogEntry struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// PBSPruneParams holds parameters for a prune operation.
type PBSPruneParams struct {
	BackupType  string `json:"backup_type"`
	BackupID    string `json:"backup_id"`
	DryRun      bool   `json:"dry_run"`
	KeepLast    int    `json:"keep_last"`
	KeepDaily   int    `json:"keep_daily"`
	KeepWeekly  int    `json:"keep_weekly"`
	KeepMonthly int    `json:"keep_monthly"`
	KeepYearly  int    `json:"keep_yearly"`
}

// PBSPruneResult represents a single entry from a prune operation result.
type PBSPruneResult struct {
	BackupType string `json:"backup-type"`
	BackupID   string `json:"backup-id"`
	BackupTime int64  `json:"backup-time"`
	Keep       bool   `json:"keep"`
	Protected  bool   `json:"protected,omitempty"`
}

// PBSDatastoreRRDEntry represents a single RRD data point from a PBS datastore.
type PBSDatastoreRRDEntry struct {
	Time       float64  `json:"time"`
	Total      *float64 `json:"total,omitempty"`
	Used       *float64 `json:"used,omitempty"`
	Available  *float64 `json:"available,omitempty"`
	ReadBytes  *float64 `json:"read_bytes,omitempty"`
	WriteBytes *float64 `json:"write_bytes,omitempty"`
	ReadIOs    *float64 `json:"read_ios,omitempty"`
	WriteIOs   *float64 `json:"write_ios,omitempty"`
	IOTicks    *float64 `json:"io_ticks,omitempty"`
}

// PBSDatastoreConfig represents the full configuration of a PBS datastore.
type PBSDatastoreConfig struct {
	Name           string `json:"name"`
	Path           string `json:"path,omitempty"`
	Comment        string `json:"comment,omitempty"`
	GCSchedule     string `json:"gc-schedule,omitempty"`
	PruneSchedule  string `json:"prune-schedule,omitempty"`
	KeepLast       int    `json:"keep-last,omitempty"`
	KeepDaily      int    `json:"keep-daily,omitempty"`
	KeepWeekly     int    `json:"keep-weekly,omitempty"`
	KeepMonthly    int    `json:"keep-monthly,omitempty"`
	KeepYearly     int    `json:"keep-yearly,omitempty"`
	NotifyUser     string `json:"notify-user,omitempty"`
	Notify         string `json:"notify,omitempty"`
	VerifyNew      bool   `json:"verify-new,omitempty"`
	MaintenanceMode string `json:"maintenance-mode,omitempty"`
}

// RestoreParams holds parameters for restoring a VM/CT from a PBS backup.
type RestoreParams struct {
	VMID    int    `json:"vmid"`
	Archive string `json:"archive"`
	Storage string `json:"storage,omitempty"`
	Unique  bool   `json:"unique,omitempty"`
	Force   bool   `json:"force,omitempty"`
}
