package proxmox

// PBSDatastore represents a datastore from GET /api2/json/admin/datastore.
type PBSDatastore struct {
	Name    string `json:"name"`
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

// RestoreParams holds parameters for restoring a VM/CT from a PBS backup.
type RestoreParams struct {
	VMID    int    `json:"vmid"`
	Archive string `json:"archive"`
	Storage string `json:"storage,omitempty"`
	Unique  bool   `json:"unique,omitempty"`
}
