package collector

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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
