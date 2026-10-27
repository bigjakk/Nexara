package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

func taskSyncID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12600000-0000-4000-8000-%012d", n))
}

// The collector asks which UPIDs it is holding are already recorded. An audit
// entry outlives its removed cluster, under that cluster's id, and a cluster
// added again has a new id but runs the same UPIDs, so the answer has to be one
// cluster's: matched on the UPID alone, the re-added cluster's tasks would be
// skipped for good.
func TestListExistingAuditLogUPIDs_AnswersForOneClusterOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	clusterA, clusterR, clusterN := taskSyncID(1), taskSyncID(2), taskSyncID(3)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12600000-0000-4000-8000-%'`)
		ids := []uuid.UUID{clusterA, clusterR, clusterN}
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = ANY($1)`, ids)
	}
	purge()
	defer purge()

	addCluster := func(id uuid.UUID, name string) {
		if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, id, name); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	addCluster(clusterA, "cluster01")
	addCluster(clusterR, "cluster02")
	upid := func(n int) string { return fmt.Sprintf("UPID:pve-01:%08X:00000001:66000001:qmstart:100:root@pam:", n) }
	// u1 was recorded for A, u2 for R.
	for i, e := range []struct {
		cluster uuid.UUID
		upid    string
	}{{clusterA, upid(1)}, {clusterR, upid(2)}} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, 'vm', '100', 'start', jsonb_build_object('upid', $3::text))`, taskSyncID(10+i), e.cluster, e.upid); err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}
	// R is removed and u2's entry stays under its id; N is the cluster added again, with a new one.
	if _, err := pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, clusterR); err != nil {
		t.Fatalf("remove cluster: %v", err)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE cluster_id = $1`, clusterR).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the removed cluster's entry was deleted with it (kept=%d err=%v): the fixture is not exercising the case", kept, err)
	}
	addCluster(clusterN, "cluster02")

	q := gen.New(pool)
	all := []string{upid(1), upid(2), upid(3)}
	for _, tc := range []struct {
		name    string
		cluster uuid.UUID
		want    []string
	}{
		{"the cluster that recorded one", clusterA, []string{upid(1)}},
		{"a cluster added again, whose predecessor recorded another", clusterN, nil},
	} {
		got, err := q.ListExistingAuditLogUPIDs(ctx, gen.ListExistingAuditLogUPIDsParams{ClusterID: tc.cluster, Upids: all})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: recorded = %v, want %v", tc.name, got, tc.want)
		}
	}
}
