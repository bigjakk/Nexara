package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/migrations"
)

// TestMigration068_RekeyPreservesAndDecouplesMembership is the data-preservation
// + regression lock for migration 000068, which re-keys vm_folder_memberships
// from the ephemeral surrogate vms.id to the stable Proxmox identity
// (cluster_id, vmid) and removes the ON DELETE CASCADE to vms.
//
// Two properties must hold and are both asserted end-to-end:
//
//  1. Preservation — an existing assignment seeded under the old surrogate key
//     survives the up migration, re-keyed to (cluster_id, vmid).
//  2. Decoupling (the actual bug fix) — once re-keyed, deleting and re-inserting
//     the vms row (exactly what the collector's stale prune did during a live
//     migration's cutover window, minting a fresh vms.id) no longer drops the
//     membership. Under the old schema the vm_id ON DELETE CASCADE nuked it and
//     the guest silently fell back to "Discovered"; now it must stay put.
//
// The down migration is also round-tripped: vm_id is restored and re-derived
// from the current vms row, so a rollback loses no assignments.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB, since this round-trips the schema up and down).
func TestMigration068_RekeyPreservesAndDecouplesMembership(t *testing.T) {
	dbURL := os.Getenv("NEXARA_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("NEXARA_TEST_DB_URL not set; skipping migration round-trip test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	defer pool.Close()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("init iofs source: %v", err)
	}
	defer src.Close()

	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(dbURL))
	if err != nil {
		t.Fatalf("init migrate: %v", err)
	}
	defer m.Close()

	userID := uuid.MustParse("68686868-6868-4868-8868-686868686868")
	clusterID := uuid.MustParse("68000000-0000-4000-8000-000000000001")
	nodeID := uuid.MustParse("68000000-0000-4000-8000-000000000002")
	vmOldID := uuid.MustParse("68000000-0000-4000-8000-00000000000a")
	vmNewID := uuid.MustParse("68000000-0000-4000-8000-00000000000b")
	folderID := uuid.MustParse("68000000-0000-4000-8000-0000000000f0")
	const vmid = 100

	// Deleting the cluster cascades nodes, vms, folders and memberships in both
	// the 67 and 68 schema shapes, so cleanup is version-agnostic.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM clusters WHERE id = $1`, clusterID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Step 1: migrate up to 67 — vm_folder_memberships is still surrogate-keyed.
	if err := m.Migrate(67); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 67: %v", err)
	}

	// Step 2: seed a guest assigned to a folder under the old surrogate key.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '') ON CONFLICT (id) DO NOTHING`,
		userID, "migration-068-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'migration-068-test', 'https://invalid.local', 'tok', 'enc')
		 ON CONFLICT (id) DO NOTHING`, clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve1') ON CONFLICT (id) DO NOTHING`,
		nodeID, clusterID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vms (id, cluster_id, node_id, vmid, type) VALUES ($1, $2, $3, $4, 'qemu')`,
		vmOldID, clusterID, nodeID, vmid); err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vm_folders (id, cluster_id, name) VALUES ($1, $2, 'CRJLAB')`,
		folderID, clusterID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vm_folder_memberships (vm_id, folder_id) VALUES ($1, $2)`,
		vmOldID, folderID); err != nil {
		t.Fatalf("seed membership (surrogate key): %v", err)
	}

	// Step 3: migrate up to 68. vm_id is gone and the assignment is preserved,
	// now keyed by (cluster_id, vmid).
	if err := m.Migrate(68); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 68: %v", err)
	}

	if hasColumn(ctx, t, pool, "vm_folder_memberships", "vm_id") {
		t.Fatalf("vm_id column still exists after migrate up to 68")
	}

	var gotFolder uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT folder_id FROM vm_folder_memberships WHERE cluster_id = $1 AND vmid = $2`,
		clusterID, vmid).Scan(&gotFolder); err != nil {
		t.Fatalf("read membership post-up (backfill lost the assignment?): %v", err)
	}
	if gotFolder != folderID {
		t.Fatalf("membership folder after up: got %v, want %v", gotFolder, folderID)
	}

	// Step 3b — THE REGRESSION LOCK. Reproduce the collector churn that caused
	// the bug: delete the vms row (as the stale prune did) ...
	if _, err := pool.Exec(ctx, `DELETE FROM vms WHERE id = $1`, vmOldID); err != nil {
		t.Fatalf("churn: delete vms row: %v", err)
	}
	var survives int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM vm_folder_memberships WHERE cluster_id = $1 AND vmid = $2`,
		clusterID, vmid).Scan(&survives); err != nil {
		t.Fatalf("probe membership after vms delete: %v", err)
	}
	if survives != 1 {
		t.Fatalf("membership did NOT survive vms-row deletion (got %d, want 1) — re-key failed to decouple lifetime from vms.id", survives)
	}
	// ... then re-insert the same guest with a brand-new surrogate id.
	if _, err := pool.Exec(ctx,
		`INSERT INTO vms (id, cluster_id, node_id, vmid, type) VALUES ($1, $2, $3, $4, 'qemu')`,
		vmNewID, clusterID, nodeID, vmid); err != nil {
		t.Fatalf("churn: reinsert vms row with new id: %v", err)
	}
	// The List join must resolve the surviving membership to the NEW id — i.e.
	// the guest stays in CRJLAB instead of falling back to "Discovered".
	var joinedVMID, joinedFolder uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT v.id, m.folder_id
		 FROM vm_folder_memberships m
		 JOIN vms v ON v.cluster_id = m.cluster_id AND v.vmid = m.vmid
		 WHERE m.cluster_id = $1 AND m.vmid = $2`,
		clusterID, vmid).Scan(&joinedVMID, &joinedFolder); err != nil {
		t.Fatalf("list-join after churn (guest fell back to Discovered): %v", err)
	}
	if joinedVMID != vmNewID || joinedFolder != folderID {
		t.Fatalf("after churn: join gave (vm=%v, folder=%v), want (vm=%v, folder=%v)",
			joinedVMID, joinedFolder, vmNewID, folderID)
	}

	// Step 4: migrate back down to 67. vm_id is restored and re-derived from the
	// current vms row, so a rollback preserves the assignment.
	if err := m.Migrate(67); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 67: %v", err)
	}
	if !hasColumn(ctx, t, pool, "vm_folder_memberships", "vm_id") {
		t.Fatalf("vm_id column not restored after migrate down to 67")
	}
	var restoredVMID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT vm_id FROM vm_folder_memberships WHERE folder_id = $1`,
		folderID).Scan(&restoredVMID); err != nil {
		t.Fatalf("read restored vm_id post-down: %v", err)
	}
	if restoredVMID != vmNewID {
		t.Fatalf("restored vm_id after down: got %v, want %v (the current vms row)", restoredVMID, vmNewID)
	}
}

// hasColumn reports whether a column exists on a table.
func hasColumn(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists); err != nil {
		t.Fatalf("probe column %s.%s: %v", table, column, err)
	}
	return exists
}
