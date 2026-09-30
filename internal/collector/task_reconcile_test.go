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
type reconcileFakeClient struct {
	ProxmoxClient
	statuses map[string]*proxmox.TaskStatus
	errs     map[string]error
}

func (f *reconcileFakeClient) GetTaskStatus(_ context.Context, _ string, upid string) (*proxmox.TaskStatus, error) {
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
// every task on, so the reconcile's membership check lets it be polled.
func memberCluster(q *mockQueries) db.Cluster {
	cluster := db.Cluster{ID: uuid.New()}
	q.nodes[cluster.ID.String()+":pve1"] = db.Node{ClusterID: cluster.ID, Name: "pve1"}
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
		q.runningTasks = []db.TaskHistory{runningRow("UPID:err", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:err": {Status: "stopped", ExitStatus: "command 'x' failed: exit code 1"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 1 || q.reconcileCalls[0].Status != "failed" {
			t.Fatalf("expected one failed reconcile, got %+v", q.reconcileCalls)
		}
	})

	t.Run("still running is left untouched", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:run", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:run": {Status: "running"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 0 {
			t.Fatalf("expected no reconcile calls for a running task, got %d", len(q.reconcileCalls))
		}
	})

	t.Run("status error within grace leaves task running", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:transient", now)}
		client := &reconcileFakeClient{errs: map[string]error{"UPID:transient": errors.New("503 service unavailable")}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, memberCluster(q))

		if len(q.reconcileCalls) != 0 {
			t.Fatalf("expected no reconcile within grace window, got %d", len(q.reconcileCalls))
		}
	})

	t.Run("status error past grace marks task failed", func(t *testing.T) {
		q := newMockQueries()
		stale := now.Add(-(staleTaskGrace + time.Hour))
		q.runningTasks = []db.TaskHistory{runningRow("UPID:vanished", stale)}
		client := &reconcileFakeClient{errs: map[string]error{"UPID:vanished": errors.New("task not found")}}
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

// TestReconcileRunningTasks_NeverPollsANodeTheClusterDoesNotHold: a
// task_history row's node can be what a caller named (POST /api/v1/tasks),
// and GetTaskStatus sends it to Proxmox as /nodes/{node}/tasks/…, which
// pveproxy resolves and dials whatever it names. A row for a node the cluster
// does not hold is never polled; it ages out through the grace path like a
// task Proxmox cannot report on. A lookup that fails polls nothing and
// finalizes nothing — it is not evidence the task is lost.
func TestReconcileRunningTasks_NeverPollsANodeTheClusterDoesNotHold(t *testing.T) {
	now := time.Now()
	stale := now.Add(-(staleTaskGrace + time.Hour))

	for _, tt := range []struct {
		name      string
		started   time.Time
		lookupErr error
		wantExit  []string
	}{
		{name: "a fresh row for a stranger stays running", started: now},
		{name: "a stale row for a stranger is finalized as lost", started: stale, wantExit: []string{vanishedExitStatus}},
		{name: "a failed lookup leaves even a stale row alone", started: stale, lookupErr: errors.New("db unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := newMockQueries()
			q.nodeLookupErr = tt.lookupErr
			row := runningRow("UPID:192.0.2.10:qmstart:100", tt.started)
			row.Node = "192.0.2.10"
			q.runningTasks = []db.TaskHistory{row}
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
