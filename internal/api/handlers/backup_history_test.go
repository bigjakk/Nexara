package handlers

import (
	"testing"
)

// The rules of the storage, PBS, Veeam and backup-job kinds put the rows they
// read where their filters say: one row for each category, and for each rule
// that is not a plain verb.
func TestBackupHistoryCategories(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind                             HistoryKind
		entry, action, typ, worker, want string
	}{
		{StorageHistory(), "audit", "create", "storage", "", "lifecycle"},
		{StorageHistory(), "audit", "create_lvm", "node", "", "lifecycle"},
		{StorageHistory(), "audit", "update", "storage", "", "config"},
		{StorageHistory(), "observed", "changed", "storage", "", "config"},
		{StorageHistory(), "audit", "delete_content", "storage", "", "content"},
		{StorageHistory(), "audit", "disk_move_running", "vm", "", "disks"},
		{StorageHistory(), "audit", "volume_move", "container", "", "disks"},
		{StorageHistory(), "audit", "backup_restored", "backup", "qmrestore", "backup"},
		{StorageHistory(), "audit", "something_new", "storage", "", "other"},
		{PBSHistory(), "audit", "pbs_credential_redirect_refused", "pbs_server", "", "server"},
		{PBSHistory(), "audit", "snapshot_unprotected", "pbs_server", "", "snapshots"},
		{PBSHistory(), "audit", "datastore_pruned", "pbs_server", "", "maintenance"},
		{PBSHistory(), "audit", "verify_job_triggered", "pbs_server", "", "jobs"},
		{PBSHistory(), "observed", "changed", "pbs_server", "", "jobs"},
		{PBSHistory(), "audit", "backup_restored", "backup", "qmrestore", "restore"},
		{VeeamServerHistory(), "audit", "veeam_server_updated", "veeam_server", "", "server"},
		{VeeamServerHistory(), "audit", "veeam_server_connect_failed", "veeam_server", "", "connection"},
		{VeeamServerHistory(), "audit", "veeam_platform_unmapped", "veeam_server", "", "mapping"},
		{VeeamServerHistory(), "audit", "veeam_job_stop_failed", "veeam_job", "", "jobs"},
		{VeeamServerHistory(), "audit", "veeam_session_stopped", "veeam_session", "", "jobs"},
		{VeeamJobHistory(), "audit", "veeam_job_disabled_failed", "veeam_job", "", "schedule"},
		{VeeamJobHistory(), "audit", "veeam_job_start_requested", "veeam_job", "", "runs"},
		{VeeamJobHistory(), "audit", "veeam_session_stop_failed", "veeam_session", "", "runs"},
		{VeeamSessionHistory(), "audit", "veeam_session_stopped", "veeam_session", "", "stop"},
		{BackupJobHistory(), "audit", "backup_job_updated", "backup", "", "config"},
		{BackupJobHistory(), "observed", "changed", "backup", "", "config"},
		{BackupJobHistory(), "audit", "backup_job_run_requested", "backup", "", "runs"},
		{BackupJobHistory(), "audit", "something_new", "backup", "vzdump", "runs"},
	} {
		if got := categoryOf(tt.kind, tt.entry, tt.action, tt.typ, tt.worker); got != tt.want {
			t.Errorf("%s: %s %s %s %s is %q, want %q", tt.kind.Name, tt.entry, tt.typ, tt.action, tt.worker, got, tt.want)
		}
	}
}
