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
// so does the cluster of the node its NUT server runs on. A command upsd took
// marks the account only while the server still holds it, and a rolling update
// rebooting that node takes its UPSes down for the update. Skipped unless
// NEXARA_TEST_DB_URL names a throwaway database.
func TestUpsCommandQueries(t *testing.T) {
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

	q := gen.New(pool)
	got, err := q.ListUpsFedClusters(ctx, upsCmdDevice)
	if want := []uuid.UUID{upsCmdHostCluster, upsCmdFedCluster}; err != nil || !slices.Equal(got, want) {
		t.Errorf("ListUpsFedClusters = %v, %v; want the host's cluster and the removed feed's", got, err)
	}

	// The fixture's server stores no account. One saved while the command was
	// out is an account upsd has not checked.
	for _, tc := range []struct {
		name, username string
		want           bool
	}{
		{"an account saved while the command was out", "someone-else", false},
		{"the account the command went to", "", true},
	} {
		arg := gen.SetNutServerCommandsVerifiedParams{ID: upsCmdServer, Host: "192.0.2.40", Port: 3493, Username: tc.username}
		if err := q.SetNutServerCommandsVerified(ctx, arg); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var marked bool
		if err := pool.QueryRow(ctx, `SELECT commands_verified_at IS NOT NULL FROM nut_servers WHERE id = $1`, upsCmdServer).Scan(&marked); err != nil || marked != tc.want {
			t.Errorf("%s: marked = %v (%v), want %v", tc.name, marked, err, tc.want)
		}
	}

	// A UPS whose NUT server's node a running rolling update is rebooting is
	// down for the update, a paused one's is not, and once the node is back it
	// stays so until Nexara has read it; another cluster's node of the same
	// name is not its server's.
	var job uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO rolling_update_jobs (cluster_id, status, created_by)
		VALUES ($1, 'running', '00000000-0000-0000-0000-000000000001') RETURNING id`, upsCmdHostCluster).Scan(&job); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		sql  string
		args []any
		want []uuid.UUID
	}{
		{`INSERT INTO rolling_update_nodes (job_id, node_name, step) VALUES ($1, 'pve-01', 'rebooting')`, []any{job},
			[]uuid.UUID{upsCmdDevice}},
		{`UPDATE rolling_update_jobs SET status = 'paused' WHERE id = $1`, []any{job}, nil},
		{`WITH r AS (UPDATE rolling_update_jobs SET status = 'running' WHERE id = $1)
			UPDATE rolling_update_nodes SET step = 'completed', reboot_completed_at = now() WHERE job_id = $1`, []any{job},
			[]uuid.UUID{upsCmdDevice}},
		{`UPDATE ups_devices SET last_reading_at = now() WHERE id = $1`, []any{upsCmdDevice}, nil},
		{`WITH j AS (INSERT INTO rolling_update_jobs (cluster_id, status, created_by)
			VALUES ($1, 'running', '00000000-0000-0000-0000-000000000001') RETURNING id)
			INSERT INTO rolling_update_nodes (job_id, node_name, step) SELECT id, 'pve-01', 'rebooting' FROM j`,
			[]any{upsCmdFedCluster}, nil},
	} {
		if _, err := pool.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
		if got, err := q.ListUpsOnRollingUpdateHosts(ctx); err != nil || !slices.Equal(got, step.want) {
			t.Errorf("after %q: %v, %v; want %v", step.sql, got, err, step.want)
		}
	}
}
