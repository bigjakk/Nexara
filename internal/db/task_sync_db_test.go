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
// entry outlives its removed cluster, detached, and a cluster added again runs
// the same UPIDs, so the answer has to be one cluster's: matched on the UPID
// alone, the re-added cluster's tasks would be skipped for good.
func TestListExistingAuditLogUPIDs_AnswersForOneClusterOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	clusterA, clusterB := taskSyncID(1), taskSyncID(2)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12600000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB})
	}
	purge()
	defer purge()

	for i, c := range []uuid.UUID{clusterA, clusterB} {
		if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, c, fmt.Sprintf("cluster0%d", i+1)); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	upid := func(n int) string { return fmt.Sprintf("UPID:pve-01:%08X:00000001:66000001:qmstart:100:root@pam:", n) }
	// u1 was recorded for A, u2 for B, and u3 by a cluster since removed (no cluster now).
	for i, e := range []struct {
		cluster any
		upid    string
	}{{clusterA, upid(1)}, {clusterB, upid(2)}, {nil, upid(3)}} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, 'vm', '100', 'start', jsonb_build_object('upid', $3::text))`, taskSyncID(10+i), e.cluster, e.upid); err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}

	q := gen.New(pool)
	all := []string{upid(1), upid(2), upid(3), upid(4)}
	for _, tc := range []struct {
		name    string
		cluster uuid.UUID
		want    []string
	}{
		{"the cluster that recorded one", clusterA, []string{upid(1)}},
		{"another cluster that recorded another", clusterB, []string{upid(2)}},
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
