package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// A cluster keeps its newest scans and loses the rest, but never its latest
// completed scan, without which the posture card reads "no scans" and 100, nor
// one the scanner is still writing. Not parallel: it shares the migrated test
// database with the package.
func TestPruneCVEScanHistory(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)
	clusters := []uuid.UUID{
		uuid.MustParse("c7e50000-0000-4000-8000-000000000001"),
		uuid.MustParse("c7e50000-0000-4000-8000-000000000002"),
	}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `WITH c AS (
			INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			SELECT id, 'cluster0' || n, 'https://192.0.2.1' || n || ':8006', 'tok', 'enc'
			FROM unnest($1::uuid[]) WITH ORDINALITY AS u(id, n) RETURNING id)
		INSERT INTO nodes (cluster_id, name) SELECT id, 'pve-01' FROM c`, clusters); err != nil {
		t.Fatal(err)
	}
	// seed gives each scan a node row and a vulnerability, so the prune goes
	// through both cascades; it returns the scans newest first.
	seed := func(cluster uuid.UUID, hoursOld int, statuses []string) []uuid.UUID {
		t.Helper()
		ids := make([]uuid.UUID, len(statuses))
		for i, status := range statuses {
			if err := pool.QueryRow(ctx, `WITH s AS (
					INSERT INTO cve_scans (cluster_id, status, created_at)
					VALUES ($1, $2, now() - $3::int * interval '1 hour') RETURNING id),
				n AS (
					INSERT INTO cve_scan_nodes (scan_id, node_id, node_name)
					SELECT s.id, nodes.id, nodes.name FROM s, nodes WHERE nodes.cluster_id = $1
					RETURNING id, scan_id)
				INSERT INTO cve_scan_vulns (scan_id, scan_node_id, cve_id, package_name, current_version)
				SELECT scan_id, id, 'CVE-2026-0001', 'openssl', '3.0.0-1' FROM n RETURNING scan_id`,
				cluster, status, hoursOld+i).Scan(&ids[i]); err != nil {
				t.Fatal(err)
			}
		}
		return ids
	}
	left := func(ids []uuid.UUID) string {
		t.Helper()
		var kept []int64
		if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(u.pos - 1 ORDER BY u.pos), '{}')
			FROM unnest($1::uuid[]) WITH ORDINALITY AS u(id, pos) JOIN cve_scans s ON s.id = u.id`,
			ids).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(kept)
	}
	for _, tt := range []struct {
		name  string
		scans []string // the first cluster's scans by status, newest first
		want  string   // the positions of those left
	}{
		{"the newest two stay", []string{"completed", "failed", "completed", "failed"}, "[0 1]"},
		{"the latest completed stays behind newer failures", []string{"failed", "failed", "completed", "completed"}, "[0 1 2]"},
		{"a scan still being written stays", []string{"completed", "completed", "running", "pending", "failed"}, "[0 1 2 3]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `DELETE FROM cve_scans WHERE cluster_id = ANY($1)`, clusters); err != nil {
				t.Fatal(err)
			}
			// The second cluster's scans are all newer than the first's, so
			// the first keeps any only if each cluster is counted on its own.
			other := seed(clusters[1], 0, []string{"completed", "completed", "completed"})
			mine := seed(clusters[0], 10, tt.scans)
			if _, err := q.PruneCVEScanHistory(ctx, 2); err != nil {
				t.Fatal(err)
			}
			if got := left(mine); got != tt.want {
				t.Errorf("left %s of %v, want %s", got, tt.scans, tt.want)
			}
			if got := left(other); got != "[0 1]" {
				t.Errorf("the other cluster's three completed scans: left %s, want [0 1]", got)
			}
		})
	}
}
