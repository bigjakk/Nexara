package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	vaCluster  = uuid.MustParse("9a000000-0000-4000-8000-000000000001")
	vaNode     = uuid.MustParse("9a000000-0000-4000-8000-000000000002")
	vaServer   = uuid.MustParse("9a000000-0000-4000-8000-000000000003")
	vaPlatform = uuid.MustParse("9a000000-0000-4000-8000-000000000004")
)

// TestVeeamAlertStats locks the three statistics the Veeam alert metrics fire
// on. Each has a failure mode that produces a plausible number rather than an
// error, which is the worst kind for an alert: it does not break, it lies.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database).
func TestVeeamAlertStats(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = $1`, vaServer)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, vaCluster)
	}
	purge()
	defer purge()

	migrateUp(t, env.Migrate)

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'veeam-alert-test', 'https://cluster.invalid:8006', 'test@pam!t', 'x')`,
		vaCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'hv01')`, vaNode, vaCluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
		 VALUES ($1, 'veeam-alert-vbr', 'https://vbr.invalid:9419', 'u', 'x')`, vaServer); err != nil {
		t.Fatalf("seed veeam server: %v", err)
	}

	newObject := func(name string, vmid int32) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_backup_objects
			   (id, veeam_server_id, veeam_object_id, platform_id, name, cluster_id, vmid, match_method)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, 'smbios')`,
			id, vaServer, uuid.New(), vaPlatform, name, vaCluster, vmid); err != nil {
			t.Fatalf("seed object %s: %v", name, err)
		}
		return id
	}
	addPoint := func(objectID uuid.UUID, ago time.Duration, malware string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_restore_points
			   (veeam_server_id, backup_object_id, veeam_id, creation_time, malware_status)
			 VALUES ($1, $2, $3, now() - $4::interval, $5)`,
			vaServer, objectID, uuid.New(), ago.String(), malware); err != nil {
			t.Fatalf("seed point: %v", err)
		}
	}

	fresh := newObject("fresh", 100)
	addPoint(fresh, 2*time.Hour, "Clean")
	overdue := newObject("overdue", 101)
	addPoint(overdue, 100*time.Hour, "Clean")
	// A guest Veeam knows about whose points are all gone. Worse than any RPO,
	// but with no age to measure — it must be COUNTED, not silently dropped,
	// and it must not be mistaken for the worst RPO.
	newObject("unrecoverable", 102)

	q := gen.New(pool)

	// --- RPO ----------------------------------------------------------------
	rpo, err := q.GetClusterVeeamRPOStats(ctx, gen.GetClusterVeeamRPOStatsParams{
		ClusterID: vaCluster, ThresholdHours: 24,
	})
	if err != nil {
		t.Fatalf("GetClusterVeeamRPOStats: %v", err)
	}
	if rpo.WorstVmid != 101 {
		t.Errorf("worst vmid = %d, want 101 (the 100h-old backup)", rpo.WorstVmid)
	}
	if rpo.WorstRpoHours < 99 || rpo.WorstRpoHours > 101 {
		t.Errorf("worst rpo = %.1fh, want ~100", rpo.WorstRpoHours)
	}
	if rpo.OverCount != 1 {
		t.Errorf("over_count = %d, want 1", rpo.OverCount)
	}
	if rpo.UnrecoverableCount != 1 {
		t.Errorf("unrecoverable_count = %d, want 1", rpo.UnrecoverableCount)
	}
	if rpo.ProtectedCount != 3 {
		t.Errorf("protected_count = %d, want 3", rpo.ProtectedCount)
	}

	// A cluster Veeam protects nothing on must still ANSWER — the engine reads
	// protected_count 0 as "nothing to evaluate", which is also what resolves a
	// firing alert after the last job is removed. ErrNoRows here would instead
	// surface as a rule error once per tick, forever.
	empty, err := q.GetClusterVeeamRPOStats(ctx, gen.GetClusterVeeamRPOStatsParams{
		ClusterID: uuid.New(), ThresholdHours: 24,
	})
	if err != nil {
		t.Fatalf("GetClusterVeeamRPOStats for an unprotected cluster: %v", err)
	}
	if empty.ProtectedCount != 0 || empty.WorstRpoHours != 0 {
		t.Errorf("unprotected cluster = %+v, want zeroes", empty)
	}

	// The guest with a backup object but no points reports has_restore_point
	// false rather than an RPO of zero, which would read as "backed up
	// seconds ago" — the exact opposite of the truth.
	guest, err := q.GetGuestVeeamRPO(ctx, gen.GetGuestVeeamRPOParams{ClusterID: vaCluster, Vmid: 102})
	if err != nil {
		t.Fatalf("GetGuestVeeamRPO: %v", err)
	}
	if guest.HasRestorePoint || guest.ProtectedCount != 1 {
		t.Errorf("unrecoverable guest = %+v, want an object but no restore point", guest)
	}

	// --- malware ------------------------------------------------------------
	//
	// An OLD Infected point superseded by a newer Clean one is history. Reading
	// the worst point rather than the newest would keep a resolved finding
	// firing forever, with nothing an operator could do to clear it.
	scanned := newObject("scanned", 103)
	addPoint(scanned, 48*time.Hour, "Infected")
	addPoint(scanned, 1*time.Hour, "Clean")
	suspicious := newObject("suspicious", 104)
	addPoint(suspicious, 1*time.Hour, "Suspicious")

	// Inclusive, because the realistic rule is ">= 2" — catch Suspicious and
	// worse. The flag mirrors the rule's own operator so the "N over
	// threshold" figure in the message counts what actually fired.
	mal, err := q.GetClusterVeeamMalwareStats(ctx, gen.GetClusterVeeamMalwareStatsParams{
		ClusterID: vaCluster, ThresholdSeverity: 2, Inclusive: true,
	})
	if err != nil {
		t.Fatalf("GetClusterVeeamMalwareStats: %v", err)
	}
	if mal.WorstVmid != 104 || mal.WorstStatus != "Suspicious" || mal.WorstSeverity != 2 {
		t.Errorf("worst = %+v, want vmid 104 Suspicious(2) — the superseded Infected point is history", mal)
	}
	if mal.OverCount != 1 {
		t.Errorf("over_count = %d, want 1", mal.OverCount)
	}
	// A ">" rule at the same threshold counts nothing: Suspicious is not
	// worse than Suspicious.
	strict, err := q.GetClusterVeeamMalwareStats(ctx, gen.GetClusterVeeamMalwareStatsParams{
		ClusterID: vaCluster, ThresholdSeverity: 2,
	})
	if err != nil {
		t.Fatalf("GetClusterVeeamMalwareStats strict: %v", err)
	}
	if strict.OverCount != 0 {
		t.Errorf("strict over_count = %d, want 0", strict.OverCount)
	}

	one, err := q.GetGuestVeeamMalware(ctx, gen.GetGuestVeeamMalwareParams{ClusterID: vaCluster, Vmid: 103})
	if err != nil {
		t.Fatalf("GetGuestVeeamMalware: %v", err)
	}
	if !one.Scanned || one.Status != "Clean" || one.Severity != 0 {
		t.Errorf("guest 103 = %+v, want the NEWEST point's Clean verdict", one)
	}

	// --- repository usage ---------------------------------------------------
	seedRepo := func(name string, capacity, used int64) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_repositories (veeam_server_id, veeam_id, name, capacity_bytes, used_bytes)
			 VALUES ($1, $2, $3, $4, $5)`, vaServer, uuid.New(), name, capacity, used); err != nil {
			t.Fatalf("seed repo %s: %v", name, err)
		}
	}
	seedRepo("nas-01", 1000, 900)
	seedRepo("nas-02", 1000, 100)
	// Veeam reports zero capacity for targets whose size it cannot measure —
	// an object store, on the lab. Including it is both a division by zero and
	// a meaningless 0%-full reading that would sort ahead of nothing but would
	// inflate measured_count and make the alert's "N of M" wrong.
	seedRepo("object-store", 0, 5000)

	repo, err := q.GetVeeamRepositoryUsageStats(ctx, gen.GetVeeamRepositoryUsageStatsParams{
		ThresholdPercent: 80,
	})
	if err != nil {
		t.Fatalf("GetVeeamRepositoryUsageStats: %v", err)
	}
	if repo.FullestName != "nas-01" || repo.FullestPercent != 90 {
		t.Errorf("fullest = %+v, want nas-01 at 90%%", repo)
	}
	if repo.OverCount != 1 {
		t.Errorf("over_count = %d, want 1", repo.OverCount)
	}

	// The count uses the RULE's comparison. Hardcoding ">" made a >= rule on a
	// repository sitting exactly at its threshold fire with a message reading
	// "0 of 2 repositories over threshold" — and that figure is what an
	// on-call reader acts on.
	exact, err := q.GetVeeamRepositoryUsageStats(ctx, gen.GetVeeamRepositoryUsageStatsParams{
		ThresholdPercent: 90,
	})
	if err != nil {
		t.Fatalf("GetVeeamRepositoryUsageStats exclusive: %v", err)
	}
	if exact.OverCount != 0 {
		t.Errorf("exclusive over_count = %d, want 0 — 90 is not > 90", exact.OverCount)
	}
	exactInclusive, err := q.GetVeeamRepositoryUsageStats(ctx, gen.GetVeeamRepositoryUsageStatsParams{
		ThresholdPercent: 90, Inclusive: true,
	})
	if err != nil {
		t.Fatalf("GetVeeamRepositoryUsageStats inclusive: %v", err)
	}
	if exactInclusive.OverCount != 1 {
		t.Errorf("inclusive over_count = %d, want 1 — 90 is >= 90", exactInclusive.OverCount)
	}
	if repo.MeasuredCount != 2 {
		t.Errorf("measured_count = %d, want 2 — the zero-capacity target is not measurable", repo.MeasuredCount)
	}
}
