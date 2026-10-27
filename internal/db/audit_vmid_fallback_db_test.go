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
	vmidFallbackCluster = uuid.MustParse("12400000-0000-4000-8000-000000000001")
	vmidFallbackNode    = uuid.MustParse("12400000-0000-4000-8000-000000000002")
	vmidFallbackVM      = uuid.MustParse("12400000-0000-4000-8000-000000000003")
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
