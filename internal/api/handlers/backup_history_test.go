package handlers

import (
	"context"
	"reflect"
	"testing"

	"github.com/bigjakk/nexara/internal/api/apischema"
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
		{StorageHistory(), "audit", "updated", "pool", "", "config"},
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
		{VeeamServerHistory(), "audit", "veeam_object_mapped", "veeam_server", "", "mapping"},
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

// A job's History is held to the actions that are a job's, because the guests'
// and the nodes' backups are filed under the same type and may share the id.
func TestBackupJobHistoryTakesOnlyAJobsActions(t *testing.T) {
	t.Parallel()
	props := apischema.Properties{"job_id": {Type: apischema.String}}
	if err := props.Compile(); err != nil {
		t.Fatal(err)
	}
	params, err := props.Validate(map[string]any{"job_id": "backup-1a2b3c4d"})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := BackupJobHistory().Select(context.Background(), HistoryRequest{Params: params})
	if err != nil || len(sel.Objects) != 1 {
		t.Fatalf("Select = %+v, %v; want one object", sel, err)
	}
	pattern := likeRegexp(sel.Objects[0].ActionLike)
	for action, mine := range map[string]bool{
		"backup_job_created": true, "backup_job_updated": true, "backup_job_deleted": true,
		"backup_job_run_requested": true, "backup_job_run": true,
		"backup": false, "backup_triggered": false, "backup_restored": false,
	} {
		if got := pattern.MatchString(action); got != mine {
			t.Errorf("%s is taken as a job's row: %v, want %v", action, got, mine)
		}
	}
}

// The changes Nexara sees made in Proxmox are shown by the kinds whose objects
// they are — a storage's definition, a PBS server's jobs, a backup job — and by
// no other: a Veeam server is not Proxmox's.
func TestBackupHistoryObservedTypes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind HistoryKind
		want []HistoryObservedType
	}{
		{StorageHistory(), []HistoryObservedType{{Type: "storage"}}},
		{PBSHistory(), []HistoryObservedType{{Type: "pbs_server"}}},
		{BackupJobHistory(), []HistoryObservedType{{Type: "backup"}}},
		{VeeamServerHistory(), nil},
		{VeeamJobHistory(), nil},
		{VeeamSessionHistory(), nil},
	} {
		if !reflect.DeepEqual(tt.kind.ObservedTypes, tt.want) {
			t.Errorf("%s shows the observed changes of %v, want %v", tt.kind.Name, tt.kind.ObservedTypes, tt.want)
		}
	}
}
