package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	invUpdCluster = uuid.MustParse("95000000-0000-4000-8000-000000000001")
	invUpdNode    = uuid.MustParse("95000000-0000-4000-8000-000000000002")
)

// nodeInventoryTables are the inventory tables whose upserts derive updated_at
// themselves, via a CASE ... IS DISTINCT FROM guard in ON CONFLICT DO UPDATE.
//
// They are exactly the inventory tables with NO set_updated_at trigger. That
// distinction is load-bearing and is asserted below: a BEFORE UPDATE trigger
// fires after the SET clause is computed and overwrites whatever the CASE
// decided, which would silently reduce the guard to dead code.
var nodeInventoryTables = []string{
	"node_disks",
	"node_network_interfaces",
	"node_pci_devices",
}

// TestNodeInventoryUpdatedAt_MovesOnlyOnContentChange is the data lock for the
// updated_at semantics on the node_* inventory tables.
//
// The defect it fixes: these three tables are written by exactly one statement
// each — their collector upsert — and that upsert bumped last_seen_at but never
// touched updated_at. Unlike nodes/vms/storage_pools they carry no
// set_updated_at trigger, and unlike nodes/vms they have no targeted UPDATE
// statements either, so updated_at had no writer at all. It sat at row-insert
// time forever (observed on a live install: updated_at months behind
// last_seen_at on every row).
//
// Setting it unconditionally to now() would have made it a duplicate of
// last_seen_at, so the upsert compares the content columns and moves
// updated_at only on a real change. Three properties:
//
//  1. A re-sync that changes nothing leaves updated_at alone while last_seen_at
//     still advances — the collector polls constantly, and that must not read
//     as the row changing.
//  2. A re-sync that changes content moves updated_at.
//  3. None of these tables grows a set_updated_at trigger, which would
//     override the CASE and make properties 1 and 2 unenforceable.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB).
func TestNodeInventoryUpdatedAt_MovesOnlyOnContentChange(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	migrateUp(t, m)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// nodes and the node_* inventory tables are ON DELETE CASCADE from
		// clusters, so dropping the cluster takes the whole fixture with it.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, invUpdCluster)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'updated-at-fixture', 'https://pve.invalid:8006', 'root@pam!t', 'enc')`,
		invUpdCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-updated-at')`,
		invUpdNode, invUpdCluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	// Property 3 first: the guard only works on tables without the trigger, so
	// establish that before asserting behaviour that depends on it.
	for _, table := range nodeInventoryTables {
		var triggers int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_trigger t
			 JOIN pg_class c ON c.oid = t.tgrelid
			 WHERE NOT t.tgisinternal AND c.relname = $1`, table).Scan(&triggers); err != nil {
			t.Fatalf("count triggers on %s: %v", table, err)
		}
		if triggers != 0 {
			t.Errorf("%s has %d trigger(s); a BEFORE UPDATE set_updated_at trigger "+
				"overwrites the upsert's CASE and makes its updated_at guard dead code",
				table, triggers)
		}
	}

	q := gen.New(pool)

	iface := gen.UpsertNodeNetworkInterfaceParams{
		NodeID:      invUpdNode,
		ClusterID:   invUpdCluster,
		Iface:       "vmbr0",
		IfaceType:   "bridge",
		Active:      true,
		Autostart:   true,
		Method:      "static",
		Address:     "10.0.0.1",
		Netmask:     "255.255.255.0",
		Cidr:        "10.0.0.1/24",
		BridgePorts: "eno1",
		Mtu:         1500,
	}

	first, err := q.UpsertNodeNetworkInterface(ctx, iface)
	if err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	// Property 1: an unchanged re-sync advances last_seen_at only.
	unchanged, err := q.UpsertNodeNetworkInterface(ctx, iface)
	if err != nil {
		t.Fatalf("unchanged re-upsert: %v", err)
	}
	if !unchanged.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("unchanged re-sync moved updated_at: %s -> %s; it must track content changes, not polls",
			first.UpdatedAt, unchanged.UpdatedAt)
	}
	if !unchanged.LastSeenAt.After(first.LastSeenAt) {
		t.Errorf("unchanged re-sync did not advance last_seen_at: %s -> %s",
			first.LastSeenAt, unchanged.LastSeenAt)
	}

	// Property 2: a content change moves updated_at. MTU is the newest content
	// column, so it doubles as a check that the column reaches the comparison.
	changed := iface
	changed.Mtu = 9000
	after, err := q.UpsertNodeNetworkInterface(ctx, changed)
	if err != nil {
		t.Fatalf("changed re-upsert: %v", err)
	}
	if !after.UpdatedAt.After(unchanged.UpdatedAt) {
		t.Errorf("content change did not move updated_at: %s -> %s",
			unchanged.UpdatedAt, after.UpdatedAt)
	}
	if after.Mtu != 9000 {
		t.Errorf("mtu = %d, want 9000", after.Mtu)
	}
}
