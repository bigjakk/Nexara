package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	vcCluster  = uuid.MustParse("9c000000-0000-4000-8000-000000000001")
	vcNode     = uuid.MustParse("9c000000-0000-4000-8000-000000000002")
	vcServer   = uuid.MustParse("9c000000-0000-4000-8000-000000000003")
	vcPlatform = uuid.MustParse("9c000000-0000-4000-8000-000000000004")
	vcUnmapped = uuid.MustParse("9c000000-0000-4000-8000-000000000005")
)

// TestVeeamCoverageQueries locks the two properties the coverage view depends
// on and that nothing else would catch:
//
//   - Per-guest AGGREGATION. A guest appears in as many backup objects as it
//     has backups — three on the lab, across daily, weekly and offsite jobs.
//     A query that does not fold them reports the guest three times, each with
//     a third of its restore points and a different "latest backup", and the
//     resulting RPO is simply wrong.
//   - NULL handling where a guest or orphan has NO restore points. That is not
//     an exotic case: it is what a fully pruned backup looks like, and it is
//     the state most worth reporting. sqlc types an aggregate as NOT NULL, so
//     a query written the obvious way scans NULL into a time.Time and 500s.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database).
func TestVeeamCoverageQueries(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = $1`, vcServer)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, vcCluster)
	}
	purge()
	defer purge()

	migrateUp(t, env.Migrate)

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'veeam-coverage-test', 'https://cluster.invalid:8006', 'test@pam!t', 'x')`,
		vcCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'hv01')`, vcNode, vcCluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
		 VALUES ($1, 'veeam-coverage-vbr', 'https://vbr.invalid:9419', 'u', 'x')`, vcServer); err != nil {
		t.Fatalf("seed veeam server: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_platforms (veeam_server_id, platform_id, display_name, cluster_id)
		 VALUES ($1, $2, 'CRJLAB', $3), ($1, $4, 'OTHER', NULL)`,
		vcServer, vcPlatform, vcCluster, vcUnmapped); err != nil {
		t.Fatalf("seed platforms: %v", err)
	}
	for _, g := range []struct {
		vmid int32
		name string
	}{{100, "web01"}, {101, "pruned"}, {102, "manualguest"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
			 VALUES ($1, $2, $3, $4, $5, 'qemu')`,
			uuid.New(), vcCluster, vcNode, g.vmid, g.name); err != nil {
			t.Fatalf("seed guest %d: %v", g.vmid, err)
		}
	}

	base := time.Now().UTC().Truncate(time.Second).Add(-72 * time.Hour)
	newObject := func(name, method string, vmid *int32, platform uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var cluster *uuid.UUID
		if vmid != nil {
			cluster = &vcCluster
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_backup_objects
			   (id, veeam_server_id, veeam_object_id, smbios_uuid, platform_id, name,
			    object_type, cluster_id, vmid, match_method)
			 VALUES ($1, $2, $3, '', $4, $5, 'VM', $6, $7, $8)`,
			id, vcServer, uuid.New(), platform, name, cluster, vmid, method); err != nil {
			t.Fatalf("seed object %s: %v", name, err)
		}
		return id
	}
	addPoint := func(objectID uuid.UUID, at time.Time, size int64, malware string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_restore_points
			   (veeam_server_id, backup_object_id, veeam_id, creation_time, size_bytes, malware_status)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			vcServer, objectID, uuid.New(), at, size, malware); err != nil {
			t.Fatalf("seed restore point: %v", err)
		}
	}
	i32 := func(n int32) *int32 { return &n }

	// web01 appears in three backups — daily, weekly, offsite — exactly as a
	// real guest does. 2 + 1 + 1 points, and the newest is in the weekly.
	daily := newObject("web01", "smbios", i32(100), vcPlatform)
	weekly := newObject("web01", "name", i32(100), vcPlatform)
	offsite := newObject("web01", "smbios", i32(100), vcPlatform)
	addPoint(daily, base, 100, "Clean")
	addPoint(daily, base.Add(24*time.Hour), 200, "Suspicious")
	addPoint(weekly, base.Add(48*time.Hour), 300, "Clean")
	addPoint(offsite, base.Add(12*time.Hour), 400, "Clean")

	// A guest Veeam still knows about but whose points are all gone. "Can
	// restore nothing" is worse than "never backed up" and must not vanish.
	newObject("pruned", "smbios", i32(101), vcPlatform)

	// An operator's own mapping, alongside a weaker automatic one.
	newObject("manualguest", "name", i32(102), vcPlatform)
	newObject("manualguest", "manual", i32(102), vcPlatform)

	// Orphans: mapped platform, resolved to no guest. One still holds points,
	// one has had them all pruned — the NULL case.
	withPoints := newObject("deleted-vm", "none", nil, vcPlatform)
	addPoint(withPoints, base, 500, "Clean")
	newObject("emptied-orphan", "none", nil, vcPlatform)
	// Unattributable rather than orphaned: nothing is known about where it
	// should live, so blaming the missing mapping on the data would be wrong.
	newObject("unmapped-platform-object", "none", nil, vcUnmapped)

	q := gen.New(pool)

	// --- per-cluster protection --------------------------------------------
	rows, err := q.ListVeeamGuestProtectionForCluster(ctx, vcCluster)
	if err != nil {
		t.Fatalf("ListVeeamGuestProtectionForCluster: %v", err)
	}
	byVmid := map[int32]gen.ListVeeamGuestProtectionForClusterRow{}
	for _, r := range rows {
		if _, dup := byVmid[r.Vmid]; dup {
			t.Fatalf("vmid %d reported twice — the query is not folding a guest's backups", r.Vmid)
		}
		byVmid[r.Vmid] = r
	}
	if len(byVmid) != 3 {
		t.Fatalf("guests = %d (%v), want 3", len(byVmid), byVmid)
	}

	web := byVmid[100]
	if web.RestorePointCount != 4 {
		t.Errorf("web01 restore points = %d, want 4 summed across its three backups", web.RestorePointCount)
	}
	if web.RestorePointBytes != 1000 {
		t.Errorf("web01 bytes = %d, want 1000", web.RestorePointBytes)
	}
	if !web.LatestRestorePoint.Valid || !web.LatestRestorePoint.Time.Equal(base.Add(48*time.Hour)) {
		t.Errorf("web01 latest = %v, want the weekly's point at %v", web.LatestRestorePoint, base.Add(48*time.Hour))
	}
	// The NEWEST point is Clean; an older Suspicious one it superseded is
	// history, and reporting it would keep a resolved finding alive forever.
	if web.LatestMalwareStatus != "Clean" {
		t.Errorf("web01 malware = %q, want Clean from the newest point", web.LatestMalwareStatus)
	}
	// smbios outranks the name match on its weekly object. Reporting the
	// weakest would flag a deterministically matched guest as a guess.
	if web.MatchMethod != "smbios" {
		t.Errorf("web01 match_method = %q, want smbios", web.MatchMethod)
	}

	pruned := byVmid[101]
	if pruned.RestorePointCount != 0 {
		t.Errorf("pruned restore points = %d, want 0", pruned.RestorePointCount)
	}
	if pruned.LatestRestorePoint.Valid {
		t.Errorf("pruned latest = %v, want NULL", pruned.LatestRestorePoint)
	}

	if got := byVmid[102].MatchMethod; got != "manual" {
		t.Errorf("manualguest match_method = %q, want manual to outrank the automatic name match", got)
	}

	// --- per-guest summary --------------------------------------------------
	summary, err := q.GetVeeamGuestProtection(ctx, gen.GetVeeamGuestProtectionParams{
		ClusterID: vcCluster, Vmid: 100,
	})
	if err != nil {
		t.Fatalf("GetVeeamGuestProtection: %v", err)
	}
	if summary.ObjectCount != 3 || summary.RestorePointCount != 4 {
		t.Errorf("web01 summary = %+v, want 3 objects and 4 points", summary)
	}

	// A guest Veeam has NEVER seen still has to answer, because :one otherwise
	// errors and the VM detail card 500s for every unprotected guest.
	none, err := q.GetVeeamGuestProtection(ctx, gen.GetVeeamGuestProtectionParams{
		ClusterID: vcCluster, Vmid: 999,
	})
	if err != nil {
		t.Fatalf("GetVeeamGuestProtection for an unknown guest: %v", err)
	}
	if none.ObjectCount != 0 || none.RestorePointCount != 0 || none.LatestRestorePoint.Valid {
		t.Errorf("unknown guest summary = %+v, want empty", none)
	}
	if none.MatchMethod != "none" {
		t.Errorf("unknown guest match_method = %q, want none", none.MatchMethod)
	}

	// --- orphans ------------------------------------------------------------
	orphans, err := q.ListVeeamOrphanedObjects(ctx, vcServer)
	if err != nil {
		// This is the scan that a max()-based query fails on: sqlc types the
		// aggregate NOT NULL and the emptied orphan's NULL blows up here.
		t.Fatalf("ListVeeamOrphanedObjects: %v", err)
	}
	names := map[string]gen.ListVeeamOrphanedObjectsRow{}
	for _, o := range orphans {
		names[o.Name] = o
	}
	if len(names) != 2 {
		t.Fatalf("orphans = %v, want exactly the two on the mapped platform", names)
	}
	if _, ok := names["unmapped-platform-object"]; ok {
		t.Error("an object on an UNMAPPED platform was reported as orphaned; it is merely unattributed")
	}
	if o := names["deleted-vm"]; !o.LatestRestorePoint.Valid || o.RestorePointBytes != 500 {
		t.Errorf("deleted-vm = %+v, want a latest point and 500 bytes", o)
	}
	if o := names["emptied-orphan"]; o.LatestRestorePoint.Valid || o.RestorePointBytes != 0 {
		t.Errorf("emptied-orphan = %+v, want NULL latest and 0 bytes", o)
	}

	// --- the manual override captures the pinned guest's identity ----------
	//
	// Without this the pin is just (cluster_id, vmid), and Proxmox reuses a
	// VMID as soon as its guest is destroyed — so the next occupant inherits
	// the old machine's restore points and reports as protected, permanently,
	// because a manual pin is exempt from re-correlation.
	if _, err := pool.Exec(ctx,
		`INSERT INTO guest_smbios (cluster_id, vmid, smbios_uuid)
		 VALUES ($1, 100, '413babd1-94db-4f46-83ed-7c1ba5ab644e')`, vcCluster); err != nil {
		t.Fatalf("seed guest_smbios: %v", err)
	}
	pinned, err := q.SetVeeamBackupObjectGuest(ctx, gen.SetVeeamBackupObjectGuestParams{
		VeeamServerID: vcServer,
		ID:            withPoints,
		ClusterID:     pgUUID(vcCluster),
		Vmid:          pgInt4(100),
	})
	if err != nil {
		t.Fatalf("SetVeeamBackupObjectGuest: %v", err)
	}
	if pinned.MatchMethod != "manual" {
		t.Errorf("match_method = %q, want manual", pinned.MatchMethod)
	}
	if pinned.ManualGuestKey != "413babd1-94db-4f46-83ed-7c1ba5ab644e" {
		t.Errorf("manual_guest_key = %q, want the pinned guest's smbios uuid", pinned.ManualGuestKey)
	}

	// Clearing hands the row back to automatic resolution and drops the key
	// with it, so a later pin cannot be checked against a guest it was never
	// pinned to.
	cleared, err := q.SetVeeamBackupObjectGuest(ctx, gen.SetVeeamBackupObjectGuestParams{
		VeeamServerID: vcServer,
		ID:            withPoints,
	})
	if err != nil {
		t.Fatalf("SetVeeamBackupObjectGuest clear: %v", err)
	}
	if cleared.MatchMethod != "none" || cleared.ManualGuestKey != "" || cleared.ClusterID.Valid {
		t.Errorf("cleared = %+v, want an unmapped row with no key", cleared)
	}
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
func pgInt4(n int32) pgtype.Int4      { return pgtype.Int4{Int32: n, Valid: true} }
