package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	memInvCluster = uuid.MustParse("95000000-0000-4000-8000-000000000011")
	memInvNode    = uuid.MustParse("95000000-0000-4000-8000-000000000012")
	memPausedClus = uuid.MustParse("95000000-0000-4000-8000-000000000013")
	memPausedNode = uuid.MustParse("95000000-0000-4000-8000-000000000014")
)

// A failed memory read keeps the node's last good one: the node page and the
// capacity forecast go on showing its modules while the newest attempt's
// reason says what went wrong, and the daily pass sees when it last tried. A
// paused cluster's nodes are never handed to the pass, which logs in as root.
// Not parallel: it seeds the shared test database.
func TestNodeMemoryFailureKeepsTheLastGoodRead(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// nodes and node_memory_inventory cascade from clusters.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{memInvCluster, memPausedClus})
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'memory-fixture', 'https://pve.invalid:8006', 'root@pam!t', 'enc')`, memInvCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name, status) VALUES ($1, $2, 'pve-01', 'online')`, memInvNode, memInvCluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted, is_active)
		 VALUES ($1, 'memory-paused-fixture', 'https://pve2.invalid:8006', 'root@pam!t', 'enc', false)`, memPausedClus); err != nil {
		t.Fatalf("seed paused cluster: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name, status) VALUES ($1, $2, 'pve-02', 'online')`, memPausedNode, memPausedClus); err != nil {
		t.Fatalf("seed paused node: %v", err)
	}

	q := gen.New(pool)
	read := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	if err := q.UpsertNodeMemoryRead(ctx, gen.UpsertNodeMemoryReadParams{
		NodeID: memInvNode, ReadAt: read, InstalledBytes: 32 << 30, SlotsUsed: 1, SlotsTotal: 2,
		MaxCapacityBytes: 1 << 40, Ecc: "Multi-bit ECC", Modules: json.RawMessage(`[{"locator":"A1","size_bytes":34359738368}]`),
	}); err != nil {
		t.Fatalf("store a good read: %v", err)
	}
	failed := read.Add(24 * time.Hour)
	if err := q.RecordNodeMemoryFailure(ctx, gen.RecordNodeMemoryFailureParams{
		NodeID: memInvNode, Status: "error", Reason: "dmidecode failed on this node.", AttemptedAt: failed,
	}); err != nil {
		t.Fatalf("store a failure: %v", err)
	}

	got, err := q.GetNodeMemoryInventory(ctx, memInvNode)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != "error" || got.Reason != "dmidecode failed on this node." || !got.AttemptedAt.Equal(failed) ||
		!got.ReadAt.Valid || !got.ReadAt.Time.Equal(read) || got.InstalledBytes != 32<<30 || got.SlotsTotal != 2 ||
		!strings.Contains(string(got.Modules), `"A1"`) {
		t.Errorf("after a failure = %+v; want the failure's status, reason and time beside the good read", got)
	}
	forecast, err := q.ListNodeMemoryInventoryByCluster(ctx, memInvCluster)
	if err != nil || len(forecast) != 1 || forecast[0].NodeName != "pve-01" || forecast[0].InstalledBytes != 32<<30 {
		t.Errorf("capacity forecast rows = %+v (%v); want the last good read", forecast, err)
	}
	states, err := q.ListNodeMemoryReadState(ctx)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	seen := false
	for _, s := range states {
		switch s.NodeID {
		case memInvNode:
			seen = true
			if s.ReadStatus.String != "error" || !s.AttemptedAt.Time.Equal(failed) {
				t.Errorf("read state = %+v; want the failed attempt", s)
			}
		case memPausedNode:
			t.Errorf("a paused cluster's node is due for a root login: %+v", s)
		}
	}
	if !seen {
		t.Error("the active cluster's node is missing from the read state")
	}
}
