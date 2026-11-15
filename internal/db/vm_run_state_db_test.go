package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	runStateClusterID = uuid.MustParse("b5000000-0000-4000-8000-00000000000c")
	runStateNodeID    = uuid.MustParse("b5000000-0000-4000-8000-0000000000b1")
)

// The health panel's I/O-error read once keyed on lock_state = 'io-error', a
// lock Proxmox never sets, so it listed nothing; it reads QEMU's run state
// now. A finished power task (UpdateVMStatus) leaves no halted state behind.
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database.
func TestVMRunState_IOErrorHealthAndTaskWrite(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, runStateClusterID)
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	        VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')`, runStateClusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		runStateNodeID, runStateClusterID); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	q := gen.New(pool)
	upsert := func(vmid int32, name, lock, runState string) gen.Vm {
		t.Helper()
		v, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: runStateClusterID, NodeID: runStateNodeID,
			Vmid: vmid, Name: name, Type: "qemu", Status: "running", LockState: lock, RunState: runState})
		if err != nil {
			t.Fatalf("UpsertVM %s: %v", name, err)
		}
		return v
	}
	halted := upsert(101, "linux01", "", "io-error")
	upsert(102, "linux02", "backup", "")

	rows, err := q.ListIOErrorGuests(ctx)
	if err != nil {
		t.Fatalf("ListIOErrorGuests: %v", err)
	}
	var names []string
	for _, r := range rows {
		if r.ClusterID == runStateClusterID {
			names = append(names, r.Name)
		}
	}
	if len(names) != 1 || names[0] != "linux01" {
		t.Errorf("ListIOErrorGuests = %v, want [linux01]", names)
	}

	if err := q.UpdateVMStatus(ctx, gen.UpdateVMStatusParams{ID: halted.ID, Status: "running"}); err != nil {
		t.Fatalf("UpdateVMStatus: %v", err)
	}
	if got, err := q.GetVM(ctx, halted.ID); err != nil || got.RunState != "" {
		t.Errorf("after UpdateVMStatus: run_state = %q (err %v), want empty", got.RunState, err)
	}
}
