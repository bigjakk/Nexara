package migration

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A run that outlives a heartbeat interval stamps its job, or every instance's
// sweep would take a live migration for abandoned (ReconcileLapsed, tested in
// internal/db). Execute is held at its cluster read until a stamp is seen.
func TestExecute_StampsItsJobsHeartbeatWhileItRuns(t *testing.T) {
	t.Parallel()
	src := uuid.New()
	job := db.MigrationJob{ID: uuid.New(), SourceClusterID: src, TargetClusterID: src, SourceNode: "pve-01",
		TargetNode: "pve-02", Vmid: 100, VmType: VMTypeQEMU, MigrationType: TypeIntraCluster, MigrationMode: ModeLive}
	d := &jobDB{t: t, job: job, members: map[uuid.UUID][]string{src: {"pve-01", "pve-02"}},
		clusterErr: pgx.ErrNoRows, holdCluster: make(chan struct{}), beat: make(chan struct{}, 1)}
	o := NewOrchestrator(db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	o.beatEvery = time.Millisecond

	done := make(chan struct{})
	go func() {
		defer close(done)
		o.Execute(context.Background(), job.ID, uuid.New())
	}()
	select {
	case <-d.beat:
	case <-time.After(10 * time.Second):
		t.Fatal("no heartbeat while the run was held")
	}
	close(d.holdCluster)
	<-done

	d.mu.Lock()
	defer d.mu.Unlock()
	if args := d.execs["BeatMigrationJob"]; len(args) != 1 || args[0] != job.ID {
		t.Errorf("BeatMigrationJob(%v), want the job's id", args)
	}
}
