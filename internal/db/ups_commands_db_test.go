package db

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	upsCmdHostCluster = uuid.MustParse("a7100000-0000-4000-8000-000000000001")
	upsCmdFedCluster  = uuid.MustParse("a7100000-0000-4000-8000-000000000002")
	upsCmdServer      = uuid.MustParse("a7100000-0000-4000-8000-000000000003")
	upsCmdDevice      = uuid.MustParse("a7100000-0000-4000-8000-000000000004")
)

// Cutting a UPS's power needs manage:node on every cluster it is known to
// power: a removed feed still counts, or removing it would lift the check, and
// so does the cluster of the node its NUT server runs on. Skipped unless
// NEXARA_TEST_DB_URL names a throwaway database.
func TestListUpsFedClusters(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM nut_servers WHERE id = $1`, upsCmdServer)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{upsCmdHostCluster, upsCmdFedCluster})
	}
	purge()
	defer purge()
	for _, step := range []struct {
		what, sql string
		args      []any
	}{
		{"clusters", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted) VALUES
			($1, 'ups-cmd-host', 'https://pve.invalid:8006', 'root@pam!t', 'enc'),
			($2, 'ups-cmd-fed', 'https://pve2.invalid:8006', 'root@pam!t', 'enc')`, []any{upsCmdHostCluster, upsCmdFedCluster}},
		{"server", `INSERT INTO nut_servers (id, name, host, host_cluster_id, host_node_name)
			VALUES ($1, 'ups-cmd-fixture', '192.0.2.40', $2, 'pve-01')`, []any{upsCmdServer, upsCmdHostCluster}},
		{"device", `INSERT INTO ups_devices (id, server_id, ups_name) VALUES ($1, $2, 'ups01')`, []any{upsCmdDevice, upsCmdServer}},
		{"removed feed", `INSERT INTO ups_feeds (ups_id, cluster_id, node_name, source, dismissed)
			VALUES ($1, $2, 'pve-02', 'manual', TRUE)`, []any{upsCmdDevice, upsCmdFedCluster}},
	} {
		if _, err := pool.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatalf("%s: %v", step.what, err)
		}
	}

	got, err := gen.New(pool).ListUpsFedClusters(ctx, upsCmdDevice)
	if want := []uuid.UUID{upsCmdHostCluster, upsCmdFedCluster}; err != nil || !slices.Equal(got, want) {
		t.Errorf("ListUpsFedClusters = %v, %v; want the host's cluster and the removed feed's", got, err)
	}
}
