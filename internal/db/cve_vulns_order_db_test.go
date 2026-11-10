package db

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// A scan's vulnerability list comes highest risk first, the order of the Risk
// column the table shows. It was ordered by severity, the Debian urgency, which
// put a critical-urgency CVE scored low above one the KEV list floors to
// critical risk.
//
// Not parallel: it shares the migrated test database with the package.
func TestListCVEScanVulnsOrder(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)

	cluster := uuid.MustParse("c7000000-0000-4000-8000-000000000001")
	node := uuid.MustParse("c7000000-0000-4000-8000-000000000002")
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, cluster) // cascades to the scan
	}
	purge()
	defer purge()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'cluster01', 'https://192.0.2.1:8006', 'tok', 'enc')`, []any{cluster}},
		{`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`, []any{node, cluster}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	scan, err := q.InsertCVEScan(ctx, gen.InsertCVEScanParams{ClusterID: cluster, Status: "completed", TotalNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	scanNode, err := q.InsertCVEScanNode(ctx, gen.InsertCVEScanNodeParams{ScanID: scan.ID, NodeID: node, NodeName: "pve-01", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		cve, pkg, severity, riskSeverity string
		risk                             float32
	}{
		{"CVE-2026-0001", "libfoo1", "critical", "low", 1.0},
		{"CVE-2026-0002", "libbar1", "low", "critical", 7.5},
		// A tie on risk goes by CVE, then package.
		{"CVE-2026-0004", "libaaa1", "medium", "high", 5.0},
		{"CVE-2026-0003", "libbaz1", "medium", "high", 5.0},
	} {
		if _, err := q.InsertCVEScanVuln(ctx, gen.InsertCVEScanVulnParams{
			ScanID: scan.ID, ScanNodeID: scanNode.ID, CveID: v.cve, PackageName: v.pkg, CurrentVersion: "1.0-1",
			Severity: v.severity, RiskScore: v.risk, RiskSeverity: v.riskSeverity, SsvcLabel: "track",
		}); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"CVE-2026-0002 libbar1", "CVE-2026-0003 libbaz1", "CVE-2026-0004 libaaa1", "CVE-2026-0001 libfoo1"}
	for _, tc := range []struct {
		name string
		list func() ([]gen.CveScanVuln, error)
	}{
		{"ListCVEScanVulns", func() ([]gen.CveScanVuln, error) { return q.ListCVEScanVulns(ctx, scan.ID) }},
		{"ListCVEScanVulnsByNode", func() ([]gen.CveScanVuln, error) {
			return q.ListCVEScanVulnsByNode(ctx, gen.ListCVEScanVulnsByNodeParams{ScanID: scan.ID, ScanNodeID: scanNode.ID})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := tc.list()
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				got = append(got, r.CveID+" "+r.PackageName)
			}
			if !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}
