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
	vmidFallbackCluster = uuid.MustParse("118f0000-0000-4000-8000-000000000001")
	vmidFallbackNode    = uuid.MustParse("118f0000-0000-4000-8000-000000000002")
	vmidFallbackVM      = uuid.MustParse("118f0000-0000-4000-8000-000000000003")
)

// The audit insert stamps a vmid from the vms row whose id the row's resource_id
// spells, for the kinds whose handlers record a vms.id: a guest, a container, a
// folder membership. For any other kind the id is a name somebody chose — a pool,
// an alias — and one spelled like a guest's UUID must not file under that guest.
// Both inserts; the collector's carries the Proxmox-sourced rows.
func TestAuditInsertStampsAVMIDFromAVMSRowOnlyForGuestKinds(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The audit rows go first: a cluster's delete leaves them (000122).
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = $1`, vmidFallbackCluster)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, vmidFallbackCluster)
	}
	purge()
	defer purge()

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'cluster01', 'https://192.0.2.10:8006', 'user@pam!test', 'x')`, []any{vmidFallbackCluster}},
		{`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`, []any{vmidFallbackNode, vmidFallbackCluster}},
		{`INSERT INTO vms (id, cluster_id, node_id, vmid, type) VALUES ($1, $2, $3, 9101, 'qemu')`,
			[]any{vmidFallbackVM, vmidFallbackCluster, vmidFallbackNode}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	guestID := vmidFallbackVM.String()
	stamped := pgtype.Int4{Int32: 9101, Valid: true}
	for _, tc := range []struct {
		name         string
		source       string // "" is the plain insert; else the sourced insert, as that source
		resourceType string
		want         pgtype.Int4
	}{
		{"a folder membership", "", "vm_folder_membership", stamped},
		{"a pool named like the guest's id", "", "ceph_pool", pgtype.Int4{}},
		{"the sourced insert, for Nexara's own guest row", "nexara", "vm", stamped},
		{"ingested from Proxmox, a guest kind named like the guest's id", "proxmox", "vm", pgtype.Int4{}},
	} {
		action := "vmid-fallback-" + tc.name
		var err error
		if tc.source != "" {
			err = q.InsertAuditLogWithSource(ctx, gen.InsertAuditLogWithSourceParams{
				ClusterID: pgtype.UUID{Bytes: vmidFallbackCluster, Valid: true}, UserID: pgtype.UUID{Bytes: systemUserID, Valid: true},
				ResourceType: tc.resourceType, ResourceID: guestID, Action: action, Details: []byte(`{}`),
				Source: tc.source, CreatedAt: time.Now(),
			})
		} else {
			err = q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
				ClusterID: pgtype.UUID{Bytes: vmidFallbackCluster, Valid: true}, UserID: pgtype.UUID{Bytes: systemUserID, Valid: true},
				ResourceType: tc.resourceType, ResourceID: guestID, Action: action, Details: []byte(`{}`),
			})
		}
		if err != nil {
			t.Fatalf("%s: insert: %v", tc.name, err)
		}
		var got pgtype.Int4
		if err := pool.QueryRow(ctx, `SELECT vmid FROM audit_log WHERE cluster_id = $1 AND action = $2`,
			vmidFallbackCluster, action).Scan(&got); err != nil {
			t.Fatalf("%s: read: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: vmid = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

var (
	nameByVMIDCluster = uuid.MustParse("118f0000-0000-4000-8000-000000000011")
	nameByVMIDNode    = uuid.MustParse("118f0000-0000-4000-8000-000000000012")
	nameByVMIDVM      = uuid.MustParse("118f0000-0000-4000-8000-000000000013")
)

// A row that names its guest by VMID only — a backup ("9101"), a restore, a
// console token (a user's id) — read back with the guest's name. Both readers
// matched a name only on the vms.id a guest's own rows carry, so the Activity
// panel and the audit log showed "9101", "9101/vm" or a raw UUID instead.
func TestAuditReadsNameAGuestRecordedByVMIDOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = $1`, nameByVMIDCluster)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, nameByVMIDCluster)
	}
	purge()
	defer purge()

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'cluster02', 'https://192.0.2.11:8006', 'user@pam!test', 'x')`, []any{nameByVMIDCluster}},
		{`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-02')`, []any{nameByVMIDNode, nameByVMIDCluster}},
		{`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type) VALUES ($1, $2, $3, 9101, 'linux01', 'qemu')`,
			[]any{nameByVMIDVM, nameByVMIDCluster, nameByVMIDNode}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		ClusterID: pgtype.UUID{Bytes: nameByVMIDCluster, Valid: true}, UserID: pgtype.UUID{Bytes: systemUserID, Valid: true},
		ResourceType: "backup", ResourceID: "9101", Action: "backup_triggered", Details: []byte(`{"vmid":"9101"}`),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	scope := []uuid.UUID{nameByVMIDCluster}
	recent, err := q.ListRecentAuditLogEnriched(ctx, scope)
	if err != nil {
		t.Fatalf("ListRecentAuditLogEnriched: %v", err)
	}
	advanced, err := q.ListAuditLogAdvanced(ctx, gen.ListAuditLogAdvancedParams{
		Limit: 10, ClusterID: pgtype.UUID{Bytes: nameByVMIDCluster, Valid: true}, AccessibleClusterIds: scope,
	})
	if err != nil {
		t.Fatalf("ListAuditLogAdvanced: %v", err)
	}
	type read struct {
		reader string
		n      int
		name   string
		vmid   int32
	}
	reads := []read{{reader: "the Activity feed", n: len(recent)}, {reader: "the audit log", n: len(advanced)}}
	if len(recent) > 0 {
		reads[0].name, reads[0].vmid = recent[0].ResourceName, recent[0].ResourceVmid
	}
	if len(advanced) > 0 {
		reads[1].name, reads[1].vmid = advanced[0].ResourceName, advanced[0].ResourceVmid
	}
	for _, got := range reads {
		if got.n != 1 || got.name != "linux01" || got.vmid != 9101 {
			t.Errorf("%s: %d rows, name %q, vmid %d; want 1 row named linux01, vmid 9101", got.reader, got.n, got.name, got.vmid)
		}
	}
}
