package collector

import (
	"context"
	"errors"
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

func TestReconcileRunningTasks(t *testing.T) {
	now := time.Now()

	t.Run("stopped OK is marked completed", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:pve1:qmmove:110", now)}
		client := &reconcileFakeClient{statuses: map[string]*proxmox.TaskStatus{
			"UPID:pve1:qmmove:110": {Status: "stopped", ExitStatus: "OK"},
		}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, db.Cluster{ID: uuid.New()})

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

		s.reconcileRunningTasks(context.Background(), client, db.Cluster{ID: uuid.New()})

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

		s.reconcileRunningTasks(context.Background(), client, db.Cluster{ID: uuid.New()})

		if len(q.reconcileCalls) != 0 {
			t.Fatalf("expected no reconcile calls for a running task, got %d", len(q.reconcileCalls))
		}
	})

	t.Run("status error within grace leaves task running", func(t *testing.T) {
		q := newMockQueries()
		q.runningTasks = []db.TaskHistory{runningRow("UPID:transient", now)}
		client := &reconcileFakeClient{errs: map[string]error{"UPID:transient": errors.New("503 service unavailable")}}
		s := &Syncer{queries: q, logger: testLogger()}

		s.reconcileRunningTasks(context.Background(), client, db.Cluster{ID: uuid.New()})

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

		s.reconcileRunningTasks(context.Background(), client, db.Cluster{ID: uuid.New()})

		if len(q.reconcileCalls) != 1 || q.reconcileCalls[0].Status != "failed" {
			t.Fatalf("expected vanished task marked failed, got %+v", q.reconcileCalls)
		}
		if q.reconcileCalls[0].ExitStatus != "vanished" {
			t.Fatalf("expected exit_status 'vanished', got %q", q.reconcileCalls[0].ExitStatus)
		}
	})
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
