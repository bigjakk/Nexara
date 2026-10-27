package collector

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auditkey"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestIngestTask_External covers the Phase 4D behavior: external (non-Nexara)
// tasks are recorded in task_history — running ones as "running" (so the
// reconciler can flip them), finished ones fully-formed — attributed to the
// system user, with skip/dedup honored.
func TestIngestTask_External(t *testing.T) {
	cluster := db.Cluster{ID: uuid.New()}
	start := time.Now().Add(-30 * time.Second)

	t.Run("running task ingested as running", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		task := proxmox.NodeTask{
			UPID: "UPID:pve1:qmsnapshot:100:run", Type: "qmsnapshot",
			ID: "100", Status: "", StartTime: start.Unix(),
		}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, nil); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 1 {
			t.Fatalf("expected 1 task_history insert, got %d", len(q.externalTaskCalls))
		}
		got := q.externalTaskCalls[0]
		if got.Status != "running" {
			t.Errorf("status = %q, want running", got.Status)
		}
		if got.FinishedAt.Valid {
			t.Error("finished_at should be null for a running task")
		}
		if got.ExitStatus != "" {
			t.Errorf("exit_status = %q, want empty", got.ExitStatus)
		}
		if got.UserID != auth.SystemUserID {
			t.Errorf("user_id = %v, want SystemUserID", got.UserID)
		}
		if got.ClusterID != cluster.ID {
			t.Error("cluster_id mismatch")
		}
		if got.Node != "pve1" || got.TaskType != "qmsnapshot" {
			t.Errorf("node/type = %s/%s, want pve1/qmsnapshot", got.Node, got.TaskType)
		}
		if !got.StartedAt.Equal(time.Unix(start.Unix(), 0)) {
			t.Errorf("started_at = %v, want %v", got.StartedAt, time.Unix(start.Unix(), 0))
		}
	})

	t.Run("running task reported as RUNNING is ingested as running, not failed", func(t *testing.T) {
		// Regression: PVE source=all returns in-flight tasks with status="RUNNING"
		// and no endtime. The old `Status != ""` check ran that through
		// classifyTaskExit and stored status=failed/exit=RUNNING, and the
		// reconciler (which only polls status='running' rows) never rescued it.
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		task := proxmox.NodeTask{
			UPID: "UPID:pve1:qmigrate:111:run", Type: "qmigrate",
			ID: "111", Status: "RUNNING", StartTime: start.Unix(), EndTime: 0,
		}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, nil); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 1 {
			t.Fatalf("expected 1 insert, got %d", len(q.externalTaskCalls))
		}
		got := q.externalTaskCalls[0]
		if got.Status != "running" {
			t.Errorf("status = %q, want running (RUNNING must not be classified as failed)", got.Status)
		}
		if got.ExitStatus != "" {
			t.Errorf("exit_status = %q, want empty for a still-running task", got.ExitStatus)
		}
		if got.FinishedAt.Valid {
			t.Error("finished_at should be null for a running task")
		}
	})

	t.Run("finished OK task ingested as completed", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		end := start.Add(10 * time.Second)
		task := proxmox.NodeTask{
			UPID: "UPID:pve1:vzdump:101:done", Type: "vzdump",
			ID: "101", Status: "OK", StartTime: start.Unix(), EndTime: end.Unix(),
		}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, nil); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 1 {
			t.Fatalf("expected 1 insert, got %d", len(q.externalTaskCalls))
		}
		got := q.externalTaskCalls[0]
		if got.Status != "completed" || got.ExitStatus != "OK" {
			t.Errorf("got %s/%s, want completed/OK", got.Status, got.ExitStatus)
		}
		if !got.FinishedAt.Valid || !got.FinishedAt.Time.Equal(time.Unix(end.Unix(), 0)) {
			t.Errorf("finished_at = %v (valid=%v), want %v", got.FinishedAt.Time, got.FinishedAt.Valid, time.Unix(end.Unix(), 0))
		}
	})

	t.Run("failed task ingested as failed", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		task := proxmox.NodeTask{
			UPID: "UPID:pve1:qmigrate:102:fail", Type: "qmigrate",
			ID: "102", Status: "migration aborted", StartTime: start.Unix(), EndTime: start.Add(time.Second).Unix(),
		}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, nil); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 1 || q.externalTaskCalls[0].Status != "failed" {
			t.Fatalf("expected one failed insert, got %+v", q.externalTaskCalls)
		}
		if q.externalTaskCalls[0].ExitStatus != "migration aborted" {
			t.Errorf("exit_status = %q, want 'migration aborted'", q.externalTaskCalls[0].ExitStatus)
		}
	})

	t.Run("skipped task type is not ingested", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		task := proxmox.NodeTask{UPID: "UPID:pve1:vncproxy:x", Type: "vncproxy", Status: ""}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, nil); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 0 {
			t.Fatalf("expected no insert for a skipped type, got %d", len(q.externalTaskCalls))
		}
	})

	t.Run("seenTaskUPIDs unions task_history + audit_log existence", func(t *testing.T) {
		q := newMockQueries()
		q.existingTaskUPIDs = map[string]bool{"a": true, "b": true}
		q.existingAuditUPIDs = map[string]bool{"b": true, "c": true}
		s := &Syncer{queries: q, logger: testLogger()}

		seen, err := s.seenTaskUPIDs(context.Background(), cluster, []string{"a", "b", "c", "d"})
		if err != nil {
			t.Fatalf("seenTaskUPIDs: %v", err)
		}
		for _, want := range []string{"a", "b", "c"} { // union of {a,b} ∪ {b,c}
			if !seen[want] {
				t.Errorf("expected %q in seen set", want)
			}
		}
		if seen["d"] {
			t.Error("d is in neither set and must not be marked seen")
		}

		empty, err := s.seenTaskUPIDs(context.Background(), cluster, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty input: got len %d, err %v", len(empty), err)
		}
	})

	t.Run("already-tracked UPID is deduped", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		task := proxmox.NodeTask{UPID: "UPID:pve1:qmstart:103", Type: "qmstart", ID: "103", Status: "OK", StartTime: start.Unix()}
		seen := map[string]bool{"UPID:pve1:qmstart:103": true}
		if err := s.ingestTask(context.Background(), cluster, "pve1", task, seen); err != nil {
			t.Fatalf("ingestTask: %v", err)
		}
		if len(q.externalTaskCalls) != 0 {
			t.Fatalf("expected dedup (no insert) for an already-tracked UPID, got %d", len(q.externalTaskCalls))
		}
	})
}

// TestIngestTask_FilesObjectTasksUnderTheirIdentity pins the (type, id, action)
// an external task is filed under and its Tasks page description: for an object
// of the cluster page, the identity the handlers record for the same work. An id
// Proxmox could not have minted for the kind is not remapped: the task is filed
// as the generic one it would be had nothing mapped it.
func TestIngestTask_FilesObjectTasksUnderTheirIdentity(t *testing.T) {
	t.Parallel()
	cluster := db.Cluster{ID: uuid.New()}
	for _, tc := range []struct {
		name, taskType, id           string
		wantType, wantID, wantAction string
		wantDescription              string
	}{
		{"OSD destroy is keyed by the number", "cephdestroyosd", "osd.12", "ceph_osd", "12", "osd_destroy", "Destroy Ceph OSD 12"},
		{"OSD create can only name the device", "cephcreateosd", "sdb", "ceph_osd", "sdb", "osd_create", "Create Ceph OSD sdb"},
		{"monitor", "cephcreatemon", "mon.pve-01", "ceph_mon", "pve-01", "mon_create", "Create Ceph monitor pve-01"},
		{"manager", "cephdestroymgr", "mgr.pve-01", "ceph_mgr", "pve-01", "mgr_destroy", "Destroy Ceph manager pve-01"},
		{"pool", "cephsetpool", "pool01", "ceph_pool", "pool01", "update", "Update Ceph pool pool01"},
		{"OSD daemon restart is an OSD action", "srvrestart", "osd.3", "ceph_osd", "3", "osd_restart", "Restart Ceph OSD 3"},
		{"monitor daemon start", "srvstart", "mon.pve-01", "ceph_mon", "pve-01", "mon_start", "Start Ceph MON pve-01"},
		{"a node service is not Ceph", "srvrestart", "pveproxy", "proxmox_task", "pveproxy", "srvrestart", "srvrestart pveproxy"},
		{"certificate renewal is keyed by its node", "acmerenew", "", "certificate", "pve-01", "renewed", "Renew certificate for pve-01"},
		{"and by its node whatever the id says", "acmerenew", "other-node", "certificate", "pve-01", "renewed", "Renew certificate for pve-01"},
		{"SDN apply is the cluster's", "reloadnetworkall", "", "sdn", "cluster", "sdn_applied", "Apply SDN configuration"},
		{"node network apply", "srvreload", "networking", "network", "pve-01", "network_applied", "Apply network configuration on pve-01"},
		{"another service reload stays one", "srvreload", "pveproxy", "node", "pveproxy", "service_reload", "service_reload pveproxy"},

		// Ids that are not Proxmox's for the kind: never an empty or invented key.
		{"an OSD number that is not one", "cephdestroyosd", "osd.abc", "proxmox_task", "osd.abc", "cephdestroyosd", "cephdestroyosd osd.abc"},
		{"an OSD destroy with no id", "cephdestroyosd", "", "proxmox_task", "pve-01", "cephdestroyosd", "cephdestroyosd"},
		{"an id that is only the prefix", "cephdestroymon", "mon.", "proxmox_task", "mon.", "cephdestroymon", "cephdestroymon mon."},
		{"a pool with no name", "cephdestroypool", "", "proxmox_task", "pve-01", "cephdestroypool", "cephdestroypool"},
		{"a pool name with a control character", "cephcreatepool", "pool\n01", "proxmox_task", "pve-01", "cephcreatepool", "cephcreatepool pool?01"},
		{"a pool name past the bound", "cephcreatepool", strings.Repeat("p", auditkey.MaxIDLen+1), "proxmox_task", "pve-01", "cephcreatepool", ""},
		{"a pool name Proxmox would not let be created", "cephcreatepool", "pool/01", "proxmox_task", "pool/01", "cephcreatepool", "cephcreatepool pool/01"},
		{"a manager id outside Proxmox's pattern", "cephcreatemgr", "mgr.pve_01", "proxmox_task", "mgr.pve_01", "cephcreatemgr", "cephcreatemgr mgr.pve_01"},
		{"a pool name with a format character", "cephcreatepool", "pool\u200b01", "proxmox_task", "pve-01", "cephcreatepool", ""},
		{"an OSD made on a device under a directory", "cephcreateosd", "cciss/c0d0", "ceph_osd", "cciss/c0d0", "osd_create", "Create Ceph OSD cciss/c0d0"},
		{"a daemon service task with no daemon", "srvrestart", "osd.", "proxmox_task", "osd.", "srvrestart", "srvrestart osd."},
		{"a daemon service task naming no OSD number", "srvrestart", "osd.abc", "proxmox_task", "osd.abc", "srvrestart", "srvrestart osd.abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := newMockQueries()
			s := &Syncer{queries: q, logger: testLogger()}
			task := proxmox.NodeTask{
				UPID: "UPID:pve-01:" + tc.taskType + ":" + tc.id, Type: tc.taskType, ID: tc.id,
				Status: "OK", StartTime: time.Now().Add(-time.Minute).Unix(), EndTime: time.Now().Unix(),
			}
			if err := s.ingestTask(context.Background(), cluster, "pve-01", task, nil); err != nil {
				t.Fatalf("ingestTask: %v", err)
			}
			if len(q.auditWithSourceCalls) != 1 || len(q.externalTaskCalls) != 1 {
				t.Fatalf("wrote %d audit rows and %d task rows, want 1 of each", len(q.auditWithSourceCalls), len(q.externalTaskCalls))
			}
			got := q.auditWithSourceCalls[0]
			if got.ResourceType != tc.wantType || got.ResourceID != tc.wantID || got.Action != tc.wantAction {
				t.Errorf("filed as (%s, %s, %s), want (%s, %s, %s)",
					got.ResourceType, got.ResourceID, got.Action, tc.wantType, tc.wantID, tc.wantAction)
			}
			// An over-long name is cut in the description (wantDescription is
			// empty there: the cut text is only checked to be bounded).
			desc := q.externalTaskCalls[0].Description
			if tc.wantDescription != "" && desc != tc.wantDescription {
				t.Errorf("described as %q, want %q", desc, tc.wantDescription)
			}
			if len([]rune(desc)) > auditkey.MaxIDLen+64 {
				t.Errorf("the description is %d characters; text taken from a task must be bounded", len([]rune(desc)))
			}
		})
	}
}

// A task run by an API token is recorded under user@realm!tokenid whether the
// node list reported it whole (a running task) or with the token apart (a finished
// one, where Proxmox splits it off the user): one task, one spelling.
func TestIngestTask_RecordsTheTokenATaskRanUnder(t *testing.T) {
	t.Parallel()
	cluster := db.Cluster{ID: uuid.New()}
	for _, tc := range []struct{ name, user, tokenID, want string }{
		{"a finished task, the token apart", "user@pam", "token01", "user@pam!token01"},
		{"a running task, whole", "user@pam!token01", "", "user@pam!token01"},
		{"whole, and the token apart as well", "user@pam!token01", "token01", "user@pam!token01"},
		{"a person", "user@pam", "", "user@pam"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := newMockQueries()
			s := &Syncer{queries: q, logger: testLogger()}
			task := proxmox.NodeTask{
				UPID: "UPID:pve-01:srvstart:pveproxy", Type: "srvstart", ID: "pveproxy", User: tc.user, TokenID: tc.tokenID,
				Status: "OK", StartTime: time.Now().Add(-time.Minute).Unix(),
			}
			if err := s.ingestTask(context.Background(), cluster, "pve-01", task, nil); err != nil {
				t.Fatalf("ingestTask: %v", err)
			}
			var details map[string]any
			if err := json.Unmarshal(q.auditWithSourceCalls[0].Details, &details); err != nil {
				t.Fatalf("details: %v", err)
			}
			if details["proxmox_user"] != tc.want {
				t.Errorf("proxmox_user = %v, want %q", details["proxmox_user"], tc.want)
			}
		})
	}
}

// TestIngestTask_FlagsATaskDisagreeingWithItsUPID: a task whose type or id is not
// the one its UPID carries is still recorded — a slip of ours must never drop a
// real task — as the generic task it would be had nothing mapped it, with the type
// and id its UPID carries, flagged identity_mismatch. A UPID not in the full form
// has no fields to disagree with.
func TestIngestTask_FlagsATaskDisagreeingWithItsUPID(t *testing.T) {
	t.Parallel()
	cluster := db.Cluster{ID: uuid.New()}
	upid := func(taskType, id string) string {
		return "UPID:pve-01:00001A2B:00C0FFEE:6540A1B2:" + taskType + ":" + id + ":root@pam:"
	}
	for _, tc := range []struct {
		name                         string
		task                         proxmox.NodeTask
		wantType, wantID, wantAction string
		wantFlag                     bool
		wantReported                 string // the reported type as kept, cleaned; the task's own when empty
	}{
		{"type and id as the UPID has them", proxmox.NodeTask{UPID: upid("cephdestroyosd", "osd.5"), Type: "cephdestroyosd", ID: "osd.5"}, "ceph_osd", "5", "osd_destroy", false, ""},
		{"a Ceph task carrying a guest task's UPID", proxmox.NodeTask{UPID: upid("qmstart", "100"), Type: "cephdestroyosd", ID: "osd.5"}, "proxmox_task", "100", "qmstart", true, ""},
		{"the same id under another type", proxmox.NodeTask{UPID: upid("qmstart", "100"), Type: "cephdestroyosd", ID: "100"}, "proxmox_task", "100", "qmstart", true, ""},
		{"the same type, another id", proxmox.NodeTask{UPID: upid("qmstart", "100"), Type: "qmstart", ID: "101"}, "proxmox_task", "100", "qmstart", true, ""},
		{"a UPID without the fields", proxmox.NodeTask{UPID: "UPID:pve-01:qmstart:100", Type: "qmstart", ID: "101"}, "vm", "101", "start", false, ""},
		{"a reported type with a control character is kept cleaned", proxmox.NodeTask{UPID: upid("qmstart", "100"), Type: "ceph\ndestroyosd", ID: "100"}, "proxmox_task", "100", "qmstart", true, "ceph?destroyosd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := newMockQueries()
			s := &Syncer{queries: q, logger: testLogger()}
			tc.task.Status, tc.task.StartTime = "OK", time.Now().Add(-time.Minute).Unix()
			if err := s.ingestTask(context.Background(), cluster, "pve-01", tc.task, nil); err != nil {
				t.Fatalf("ingestTask: %v", err)
			}
			if len(q.auditWithSourceCalls) != 1 || len(q.externalTaskCalls) != 1 {
				t.Fatalf("recorded %d audit rows and %d task rows, want 1 of each: a task is never dropped",
					len(q.auditWithSourceCalls), len(q.externalTaskCalls))
			}
			row := q.auditWithSourceCalls[0]
			var details map[string]any
			if err := json.Unmarshal(row.Details, &details); err != nil {
				t.Fatalf("details: %v", err)
			}
			flagged := details["identity_mismatch"] == true
			if row.ResourceType != tc.wantType || row.ResourceID != tc.wantID || row.Action != tc.wantAction || flagged != tc.wantFlag {
				t.Errorf("filed as (%s, %s, %s), flagged %v; want (%s, %s, %s), flagged %v",
					row.ResourceType, row.ResourceID, row.Action, flagged, tc.wantType, tc.wantID, tc.wantAction, tc.wantFlag)
			}
			wantReported := tc.wantReported
			if wantReported == "" {
				wantReported = tc.task.Type
			}
			if tc.wantFlag && (q.externalTaskCalls[0].TaskType != tc.wantAction || details["reported_type"] != wantReported) {
				t.Errorf("the task row's type is %q (want the UPID's %q) and the row keeps the reported type %v (want %q)",
					q.externalTaskCalls[0].TaskType, tc.wantAction, details["reported_type"], wantReported)
			}
		})
	}
}

// An ingested task's audit row is filed under what its UPID's id field names,
// so that a storage's History can find it. imgdel's and prunebackups' ids end
// in the storage; imgcopy (an upload or a volume copy) has none, and download's
// is an encoded file name, so those are the node's — keyed on either, a storage
// row would name a node or a file for a storage.
func TestIngestTask_AuditRowIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, taskType, id                        string
		wantType, wantID, wantStorage, wantAction string
	}{
		{"imgdel of a volume with an owner", "imgdel", "105@store02", "storage", "store02", "store02", "delete_content"},
		{"imgdel of a volume without one", "imgdel", "store02", "storage", "store02", "store02", "delete_content"},
		{"prunebackups of one guest's backups", "prunebackups", "105@store02", "storage", "store02", "store02", "prune_backups"},
		{"imgcopy has no id", "imgcopy", "", "node", "pve-01", "", "image_copy"},
		{"download's id is a file name", "download", "linux01\\x2diso", "node", "pve-01", "", "download"},
		{"an unmapped task keeps its id", "srvstart", "pveproxy", "proxmox_task", "pveproxy", "", "srvstart"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := newMockQueries()
			s := &Syncer{queries: q, logger: testLogger()}
			task := proxmox.NodeTask{
				UPID: "UPID:pve-01:" + tt.taskType, Type: tt.taskType, ID: tt.id,
				Status: "OK", StartTime: 1, EndTime: 2,
			}
			if err := s.ingestTask(context.Background(), db.Cluster{ID: uuid.New()}, "pve-01", task, nil); err != nil {
				t.Fatalf("ingestTask: %v", err)
			}
			if len(q.auditWithSourceCalls) != 1 {
				t.Fatalf("wrote %d audit rows, want 1", len(q.auditWithSourceCalls))
			}
			got := q.auditWithSourceCalls[0]
			if got.ResourceType != tt.wantType || got.ResourceID != tt.wantID || got.Action != tt.wantAction {
				t.Errorf("row is (%s, %q, %s), want (%s, %q, %s)", got.ResourceType, got.ResourceID, got.Action, tt.wantType, tt.wantID, tt.wantAction)
			}
			var details map[string]any
			if err := json.Unmarshal(got.Details, &details); err != nil {
				t.Fatalf("details: %v", err)
			}
			if storage, _ := details["storage"].(string); storage != tt.wantStorage {
				t.Errorf("details.storage = %q, want %q", storage, tt.wantStorage)
			}
		})
	}
}

// ingestSyncFakeClient implements just the two ProxmoxClient methods syncTasks
// touches (GetNodes, GetNodeTasks); the embedded interface stubs the rest.
type ingestSyncFakeClient struct {
	ProxmoxClient
	nodes    []proxmox.NodeListEntry
	tasks    map[string][]proxmox.NodeTask
	taskErrs map[string]error // per-node GetNodeTasks failures
}

func (f *ingestSyncFakeClient) GetNodes(context.Context) ([]proxmox.NodeListEntry, error) {
	return f.nodes, nil
}

func (f *ingestSyncFakeClient) GetNodeTasks(_ context.Context, node string, _ int64, _ int) ([]proxmox.NodeTask, error) {
	if err := f.taskErrs[node]; err != nil {
		return nil, err
	}
	return f.tasks[node], nil
}

// Each ingest pass marks the per-node reloads of an SDN apply once every node's
// list is in — also when a node failed to list, so one slow node does not hold
// back the marking of the rest.
func TestSyncTasksMarksTheNodeReloadsOfAnSDNApply(t *testing.T) {
	t.Parallel()
	cluster := db.Cluster{ID: uuid.New()}
	client := &ingestSyncFakeClient{
		nodes:    []proxmox.NodeListEntry{{Node: "pve1", Status: "online"}, {Node: "pve2", Status: "online"}},
		taskErrs: map[string]error{"pve2": errors.New("timeout")},
	}
	q := newMockQueries()
	s := &Syncer{queries: q, logger: testLogger()}
	s.syncTasks(context.Background(), client, cluster)
	if len(q.sdnApplyLinks) != 1 || q.sdnApplyLinks[0].ClusterID != cluster.ID ||
		time.Since(q.sdnApplyLinks[0].Since) < sdnApplyLinkWindow {
		t.Errorf("node reloads marked with %+v, want one pass for this cluster over the last %v", q.sdnApplyLinks, sdnApplyLinkWindow)
	}
}

// TestSyncTasks_WatermarkGuard verifies the task-sync high-water mark advances
// only when a tick deduped cleanly. A dedup failure must hold `since` back so
// the un-ingested tasks are retried next tick, not skipped past forever by the
// since-filter.
func TestSyncTasks_WatermarkGuard(t *testing.T) {
	cluster := db.Cluster{ID: uuid.New()}
	taskStart := time.Now().Add(-time.Minute).Unix()
	client := &ingestSyncFakeClient{
		nodes: []proxmox.NodeListEntry{{Node: "pve1", Status: "online"}},
		tasks: map[string][]proxmox.NodeTask{
			"pve1": {{UPID: "UPID:pve1:qmsnapshot:100:run", Type: "qmsnapshot", ID: "100", Status: "", StartTime: taskStart}},
		},
	}

	t.Run("clean tick ingests and advances the watermark", func(t *testing.T) {
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		s.syncTasks(context.Background(), client, cluster)
		if len(q.externalTaskCalls) != 1 {
			t.Fatalf("expected 1 task ingested, got %d", len(q.externalTaskCalls))
		}
		if len(q.upsertTaskSyncCalls) != 1 {
			t.Fatalf("expected the watermark to advance once, got %d upserts", len(q.upsertTaskSyncCalls))
		}
		if got := q.upsertTaskSyncCalls[0].LastSyncedAt; got != taskStart {
			t.Errorf("watermark = %d, want %d", got, taskStart)
		}
	})

	t.Run("dedup failure ingests nothing and holds the watermark", func(t *testing.T) {
		q := newMockQueries()
		q.taskHistUPIDErr = errors.New("db unavailable")
		s := &Syncer{queries: q, logger: testLogger()}
		s.syncTasks(context.Background(), client, cluster)
		if len(q.externalTaskCalls) != 0 {
			t.Fatalf("expected no ingestion when dedup fails, got %d", len(q.externalTaskCalls))
		}
		if len(q.upsertTaskSyncCalls) != 0 {
			t.Fatalf("watermark must not advance when dedup fails, got %d upserts", len(q.upsertTaskSyncCalls))
		}
	})

	t.Run("per-node fetch failure ingests the healthy node but holds the watermark", func(t *testing.T) {
		// pve1 lists a task; pve2's task fetch fails. The healthy node's tasks
		// must still ingest, but the cluster watermark must NOT advance — it
		// would skip past anything that started on pve2 before pve1's
		// maxStartTime, and the since-filter would never re-list it.
		twoNodeClient := &ingestSyncFakeClient{
			nodes: []proxmox.NodeListEntry{
				{Node: "pve1", Status: "online"},
				{Node: "pve2", Status: "online"},
			},
			tasks: map[string][]proxmox.NodeTask{
				"pve1": {{UPID: "UPID:pve1:qmsnapshot:100:run", Type: "qmsnapshot", ID: "100", Status: "", StartTime: taskStart}},
			},
			taskErrs: map[string]error{"pve2": errors.New("node unreachable")},
		}
		q := newMockQueries()
		s := &Syncer{queries: q, logger: testLogger()}
		listed, offline := s.syncTasks(context.Background(), twoNodeClient, cluster)
		if len(q.externalTaskCalls) != 1 {
			t.Fatalf("expected the healthy node's task to ingest, got %d", len(q.externalTaskCalls))
		}
		if len(q.upsertTaskSyncCalls) != 0 {
			t.Fatalf("watermark must not advance when a node's task fetch fails, got %d upserts", len(q.upsertTaskSyncCalls))
		}
		// Only pve1's status changes can be decided: pve2's tasks are missing.
		if !slices.Equal(listed, []string{"pve1"}) || len(offline) != 0 {
			t.Errorf("listed, offline = %v, %v; want [pve1], []", listed, offline)
		}

		// The same failure on a node the cluster reports offline holds the
		// watermark too, and is named apart: its guests' changes to and from
		// "unknown" are decided while it stays down.
		twoNodeClient.nodes[1].Status = "offline"
		if listed, offline := s.syncTasks(context.Background(), twoNodeClient, cluster); !slices.Equal(listed, []string{"pve1"}) ||
			!slices.Equal(offline, []string{"pve2"}) {
			t.Errorf("with pve2 offline: listed, offline = %v, %v; want [pve1], [pve2]", listed, offline)
		}
	})
}
