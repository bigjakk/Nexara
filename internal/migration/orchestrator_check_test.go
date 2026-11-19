package migration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A check ran whatever the job's status, answered with a report it never
// saved, and one that could not run left the job "checking", which no cancel
// takes. Which statuses each write matches is the SQL's, tested in internal/db
// (TestMigrationJobCheck_WritesOnlyOverItsOwnStatus).
func TestRunPreFlight_StopsWhereItsWriteMatchesNothing(t *testing.T) {
	t.Parallel()
	src := uuid.New()
	job := db.MigrationJob{ID: uuid.New(), SourceClusterID: src, TargetClusterID: src, SourceNode: "pve-01",
		TargetNode: "pve-02", Vmid: 100, VmType: VMTypeQEMU, MigrationType: TypeIntraCluster}
	for _, tt := range []struct {
		name       string
		unmatched  string                 // the write that matches no row
		members    map[uuid.UUID][]string // none: the node check fails and its report is saved
		clusterErr error
		want       error
		last       string // the last statement sent
	}{
		{name: "a job that has started", unmatched: "BeginMigrationJobCheck",
			want: ErrNotCheckable, last: "BeginMigrationJobCheck"},
		{name: "a job started while it was checked", unmatched: "SaveMigrationJobChecks",
			want: ErrChangedWhileChecked, last: "SaveMigrationJobChecks"},
		{name: "a check that could not read the cluster", members: map[uuid.UUID][]string{src: {"pve-01", "pve-02"}},
			clusterErr: pgx.ErrNoRows, want: pgx.ErrNoRows, last: "AbandonMigrationJobCheck"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &jobDB{t: t, job: job, members: tt.members, clusterErr: tt.clusterErr,
				unmatched: map[string]bool{tt.unmatched: true}}
			o := NewOrchestrator(db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			report, err := o.RunPreFlight(context.Background(), job.ID)
			if report != nil || !errors.Is(err, tt.want) {
				t.Errorf("RunPreFlight = %+v, %v; want no report and %v", report, err, tt.want)
			}
			if last := d.names[len(d.names)-1]; last != tt.last {
				t.Errorf("statements %v, want the last to be %s", d.names, tt.last)
			}
		})
	}
}
