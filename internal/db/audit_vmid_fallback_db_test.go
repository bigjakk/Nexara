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

// A row that names its guest by VMID only (a backup, a console token, a
// guest-tools policy) reads back with the guest's name; the readers showed
// "9101" or a raw UUID. A guest's own row whose vms.id is gone stays unnamed
// (000084), and so does a row older than the guest now holding its VMID:
// either may name another guest.
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
	rows := []struct {
		action, resourceType, resourceID, want string
		older                                  bool // written before Nexara first saw the guest
	}{
		{"backup_triggered", "backup", "9101", "linux01", false},
		{"guest_tools_policy_update", "vm", "9101", "linux01", false},
		{"migrate", "vm", uuid.NewString(), "", false}, // a vms.id that churned or was destroyed
		{"console_token_mint", "auth", uuid.NewString(), "", true},
	}
	for _, r := range rows {
		if err := q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
			ClusterID: pgtype.UUID{Bytes: nameByVMIDCluster, Valid: true}, UserID: pgtype.UUID{Bytes: systemUserID, Valid: true},
			ResourceType: r.resourceType, ResourceID: r.resourceID, Action: r.action, Details: []byte(`{"vmid":"9101"}`),
		}); err != nil {
			t.Fatalf("insert %s: %v", r.action, err)
		}
		if r.older {
			if _, err := pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '1 hour'
				WHERE cluster_id = $1 AND action = $2`, nameByVMIDCluster, r.action); err != nil {
				t.Fatalf("age %s: %v", r.action, err)
			}
		}
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
	read := map[string]map[string]string{"the Activity feed": {}, "the audit log": {}}
	for _, e := range recent {
		if e.ResourceVmid == 9101 {
			read["the Activity feed"][e.Action] = e.ResourceName
		}
	}
	for _, e := range advanced {
		if e.ResourceVmid == 9101 {
			read["the audit log"][e.Action] = e.ResourceName
		}
	}
	for reader, names := range read {
		for _, r := range rows {
			if got, ok := names[r.action]; !ok || got != r.want {
				t.Errorf("%s, %s row: name %q (listed %v); want %q with vmid 9101", reader, r.action, got, ok, r.want)
			}
		}
	}
}
