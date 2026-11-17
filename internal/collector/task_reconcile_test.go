package collector

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// reconcileFakeClient implements only GetTaskStatus; the embedded interface
// supplies (nil) stubs for every other method, which would panic if called.
// Like Proxmox, it knows a task only on the node its UPID names.
type reconcileFakeClient struct {
	ProxmoxClient
	statuses map[string]*proxmox.TaskStatus
	errs     map[string]error
}

func holdsTask(node, upid string) bool {
	taskNode, err := proxmox.TaskNode(upid)
	return err == nil && taskNode == node
}

func (f *reconcileFakeClient) GetTaskStatus(_ context.Context, node string, upid string) (*proxmox.TaskStatus, error) {
	if !holdsTask(node, upid) {
		return nil, errors.New("no such task")
	}
	if err := f.errs[upid]; err != nil {
		return nil, err
	}
	if st, ok := f.statuses[upid]; ok {
		return st, nil
	}
	return nil, errors.New("task not found")
}

func runningRow(upid string, started time.Time) db.TaskHistory {
	return db.TaskHistory{
		ID:        uuid.New(),
		ClusterID: uuid.New(),
		Upid:      upid,
		Node:      "pve1",
		Status:    "running",
		StartedAt: started,
	}
}

// memberCluster is a cluster whose nodes include the "pve1" runningRow puts
// every task on, so the reconcile's membership check lets it be polled, and
// "pve2".
func memberCluster(q *mockQueries) db.Cluster {
	cluster := db.Cluster{ID: uuid.New()}
	for _, name := range []string{"pve1", "pve2"} {
		q.nodes[cluster.ID.String()+":"+name] = db.Node{ClusterID: cluster.ID, Name: name}
	}
	return cluster
}

func TestReconcileRunningTasks(t *testing.T) {
	now := time.Now()

	t.Run("stopped OK is marked completed", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:qmmove:110", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:pve1:qmmove:110": {Status: "stopped", ExitStatus: "OK"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 1 {
			t.Fatalf("expected 1 reconcile call, got %d", len(q.reconcileCalls))
		}
		got := q.reconcileCalls[0]
		if got.Status != "completed" || got.ExitStatus != "OK" {
			t.Fatalf("expected completed/OK, got %s/%s", got.Status, got.ExitStatus)
		}
		if !got.FinishedAt.Valid {
			t.Fatal("expected finished_at to be set")
		}
	})

	t.Run("stopped with error is marked failed", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:err", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:pve1:err": {Status: "stopped", ExitStatus: "command 'x' failed: exit code 1"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 1 || q.reconcileCalls[0].Status != "failed" {
			t.Fatalf("expected one failed reconcile, got %+v", q.reconcileCalls)
		}
	})

	t.Run("still running is left untouched", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:run", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:pve1:run": {Status: "running"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 0 {
			t.Fatalf("expected no reconcile calls for a running task, got %d", len(q.reconcileCalls))
		}
	})

	t.Run("status error within grace leaves task running", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:transient", now)}
		client := &reconcileFakeClient{errs: map[string]error{"UPID:pve1:transient": errors.New("503 service unavailable")}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 0 {
			t.Fatalf("expected no reconcile within grace window, got %d", len(q.reconcileCalls))
		}
	})

	t.Run("status error past grace marks task failed", func(t *testing.T) {
		q := newMockQueries()
		stale := now.Add(-(staleTaskGrace + time.Hour))
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:vanished", stale)}
		client := &reconcileFakeClient{errs: map[string]error{"UPID:pve1:vanished": errors.New("task not found")}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 1 || q.reconcileCalls[0].Status != "failed" {
			t.Fatalf("expected vanished task marked failed, got %+v", q.reconcileCalls)
		}
		if q.reconcileCalls[0].ExitStatus != "vanished" {
			t.Fatalf("expected exit_status 'vanished', got %q", q.reconcileCalls[0].ExitStatus)
		}
	})
}

// endTimeReconcileClient also answers FindNodeTask, with the end time the
// node task list records for a task.
type endTimeReconcileClient struct {
	reconcileFakeClient
	ended map[string]int64
}

func (f *endTimeReconcileClient) FindNodeTask(_ context.Context, node string, upid string) (*proxmox.NodeTask, error) {
	if end, ok := f.ended[upid]; ok && holdsTask(node, upid) {
		return &proxmox.NodeTask{UPID: upid, EndTime: end}, nil
	}
	return nil, nil
}

// TestReconcileRunningTasks_FinishedAtIsTheTasksOwnEnd: a row closed late
// records when Proxmox says the task ended, not when the poll noticed; with
// no end time from Proxmox, now.
func TestReconcileRunningTasks_FinishedAtIsTheTasksOwnEnd(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	ended := started.Add(time.Hour)
	q := newMockQueries()
	q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:a", started), runningRow("UPID:pve1:b", started)}
	stopped := &proxmox.TaskStatus{Status: "stopped", ExitStatus: "OK"}
	client := &endTimeReconcileClient{
		reconcileFakeClient: reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{"UPID:pve1:a": stopped, "UPID:pve1:b": stopped}},
		ended:               map[string]int64{"UPID:pve1:a": ended.Unix()},
	}
	s := &Syncer{queries: q, logger: testLogger()}
	before := time.Now()

	s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

	got := map[string]time.Time{}
	for _, c := range q.reconcileCalls {
		got[c.Upid] = c.FinishedAt.Time
	}
	if !got["UPID:pve1:a"].Equal(ended) {
		t.Errorf("finished_at = %v, want the task's end %v", got["UPID:pve1:a"], ended)
	}
	if got["UPID:pve1:b"].Before(before) {
		t.Errorf("finished_at = %v with no end time from Proxmox, want now", got["UPID:pve1:b"])
	}
}

// TestReconcileRunningTasks_AsksTheNodeTheUPIDNames: an upload's worker runs
// on the node that took the request, not the one the row records (pve-storage
// Status.pm upload has no proxyto), and only that node holds the task. The row
// closes with that node's verdict and end time.
func TestReconcileRunningTasks_AsksTheNodeTheUPIDNames(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-time.Hour).Truncate(time.Second)
	upid := "UPID:pve1:0000A1B2:00000001:6A8CE407:imgcopy::root@pam:"
	q := newMockQueries()
	row := runningRow(upid, started)
	row.Node = "pve2"
	q.runningTasks = []db.TaskHistory{row}
	client := &endTimeReconcileClient{
		reconcileFakeClient: reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			upid: {Status: "stopped", ExitStatus: "OK"},
		}},
		ended: map[string]int64{upid: started.Add(time.Minute).Unix()},
	}
	s := &Syncer{queries: q, logger: testLogger()}

	s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

	if len(q.reconcileCalls) != 1 || q.reconcileCalls[0].Status != "completed" {
		t.Fatalf("reconciled %+v, want one completed", q.reconcileCalls)
	}
	if got := q.reconcileCalls[0].FinishedAt.Time; !got.Equal(started.Add(time.Minute)) {
		t.Errorf("finished_at = %v, want the end time pve1 records", got)
	}
}

// TestReconcileRunningTasks_NeverPollsANodeTheClusterDoesNotHold: the node
// polled is the one the UPID names, and a UPID can be what a caller filed
// (POST /api/v1/tasks); GetTaskStatus sends it to Proxmox as
// /nodes/{node}/tasks/…, which pveproxy resolves and dials whatever it names.
// A UPID naming a stranger, or no node at all, is never polled, whatever the
// row's own node; it ages out through the grace path like a task Proxmox
// cannot report on. A lookup that fails polls nothing and finalizes nothing.
func TestReconcileRunningTasks_NeverPollsANodeTheClusterDoesNotHold(t *testing.T) {
	now := time.Now()
	stale := now.Add(-(staleTaskGrace + time.Hour))
	stranger := "UPID:pve9:0000A1B2:00000001:6A8CE407:qmstart:100:root@pam:"

	for _, tt := range []struct {
		name      string
		upid      string
		started   time.Time
		lookupErr error
		wantExit  []string
	}{
		{name: "a fresh row for a stranger stays running", upid: stranger, started: now},
		{name: "a stale row for a stranger is finalized as lost", upid: stranger, started: stale, wantExit: []string{vanishedExitStatus}},
		{name: "a failed lookup leaves even a stale row alone", upid: stranger, started: stale, lookupErr: errors.New("db unavailable")},
		{name: "a UPID naming no node is finalized as lost", upid: "UPID:192.0.2.10:qmstart:100", started: stale, wantExit: []string{vanishedExitStatus}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := newMockQueries()
			q.nodeLookupErr = tt.lookupErr
			q.runningTasks = []db.TaskHistory{runningRow(tt.upid, tt.started)} // on member pve1
			client := &countingReconcileClient{}
			s := &Syncer{queries: q, logger: testLogger()}

			s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

			if client.calls != 0 {
				t.Errorf("GetTaskStatus was called %d times for a node the cluster does not hold", client.calls)
			}
			exits := make([]string, 0, len(q.reconcileCalls))
			for _, c := range q.reconcileCalls {
				exits = append(exits, c.ExitStatus)
			}
			if !slices.Equal(exits, tt.wantExit) {
				t.Errorf("finalized with %v, want %v", exits, tt.wantExit)
			}
		})
	}
}

// countingReconcileClient counts GetTaskStatus calls and answers each as a
// finished task, so a call that should not have happened would also finalize.
type countingReconcileClient struct {
	ProxmoxClient
	calls int
}

func (c *countingReconcileClient) GetTaskStatus(context.Context, string, string) (*proxmox.TaskStatus, error) {
	c.calls++
	return &proxmox.TaskStatus{Status: "stopped", ExitStatus: "OK"}, nil
}

func TestClassifyTaskExit(t *testing.T) {
	cases := []struct {
		in         string
		wantStatus string
	}{
		{"", "completed"},
		{"OK", "completed"},
		{"WARNINGS: 2", "completed"},
		{"OK (with warnings)", "completed"}, // must agree with proxmox.TaskSucceeded
		{"  OK  ", "completed"},             // trimmed/cased via the canonical helper
		{"error: boom", "failed"},
	}
	for _, tc := range cases {
		if got, _ := classifyTaskExit(tc.in); got != tc.wantStatus {
			t.Errorf("classifyTaskExit(%q) = %q, want %q", tc.in, got, tc.wantStatus)
		}
	}
}
