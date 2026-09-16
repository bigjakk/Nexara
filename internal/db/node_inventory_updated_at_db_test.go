package db

import (
	"context"
	"encoding/json"
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
// updated_at only on a real change.
//
// That comparison later grew a second job: it also gates whether the row is
// written AT ALL. Bumping last_seen_at every tick rewrote every row of static
// hardware on every sweep, and on an NFS-backed data directory each of those
// tuples cost a WAL flush and an fsync. The upsert now writes only on a content
// change or an expired heartbeat. Five properties:
//
//  1. A re-sync that changes nothing, inside the heartbeat window, writes
//     nothing — neither updated_at nor last_seen_at moves.
//  2. Once the heartbeat window expires, an unchanged re-sync advances
//     last_seen_at only, so the grace-windowed prune never deletes a live row.
//  3. A re-sync that changes content writes immediately and moves updated_at.
//  4. A batch repeating one conflict key does not error.
//  5. None of these tables grows a set_updated_at trigger, which would
//     override the CASE and make properties 1-3 unenforceable.
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

	// Property 5 first: the guard only works on tables without the trigger, so
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

	type ifaceRow struct {
		Iface       string `json:"iface"`
		IfaceType   string `json:"iface_type"`
		Active      bool   `json:"active"`
		Autostart   bool   `json:"autostart"`
		Method      string `json:"method"`
		Method6     string `json:"method6"`
		Address     string `json:"address"`
		Netmask     string `json:"netmask"`
		Gateway     string `json:"gateway"`
		Cidr        string `json:"cidr"`
		BridgePorts string `json:"bridge_ports"`
		Comments    string `json:"comments"`
		Mtu         int32  `json:"mtu"`
	}

	base := ifaceRow{
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

	// heartbeatNever is long enough that a row written moments ago is always
	// inside the window, so only a content change can trigger a write.
	const heartbeatNever = 3600
	// heartbeatNow expires the window immediately: last_seen_at < now() - 0s is
	// true for any earlier write, so the heartbeat branch always fires.
	const heartbeatNow = 0

	upsert := func(t *testing.T, heartbeatSeconds int32, rows ...ifaceRow) {
		t.Helper()
		payload, err := json.Marshal(rows)
		if err != nil {
			t.Fatalf("marshal interfaces: %v", err)
		}
		if err := q.UpsertNodeNetworkInterfaces(ctx, gen.UpsertNodeNetworkInterfacesParams{
			NodeID:           invUpdNode,
			ClusterID:        invUpdCluster,
			Interfaces:       payload,
			HeartbeatSeconds: heartbeatSeconds,
		}); err != nil {
			t.Fatalf("upsert interfaces: %v", err)
		}
	}

	read := func(t *testing.T) (updatedAt, lastSeenAt time.Time, mtu int32) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT updated_at, last_seen_at, mtu FROM node_network_interfaces
			 WHERE node_id = $1 AND iface = $2`, invUpdNode, base.Iface,
		).Scan(&updatedAt, &lastSeenAt, &mtu); err != nil {
			t.Fatalf("read interface: %v", err)
		}
		return updatedAt, lastSeenAt, mtu
	}

	upsert(t, heartbeatNever, base)
	firstUpdated, firstSeen, _ := read(t)

	// Property 1: an unchanged re-sync inside the heartbeat window writes
	// NOTHING. This is the whole point of the gate — the collector re-reads
	// static hardware every tick, and on an NFS-backed data directory each
	// dirtied tuple costs a WAL flush and an fsync. last_seen_at holding still
	// here is the observable proof that no tuple was written.
	upsert(t, heartbeatNever, base)
	unchangedUpdated, unchangedSeen, _ := read(t)
	if !unchangedSeen.Equal(firstSeen) {
		t.Errorf("unchanged re-sync inside the heartbeat window advanced last_seen_at: %s -> %s; it must not write at all",
			firstSeen, unchangedSeen)
	}
	if !unchangedUpdated.Equal(firstUpdated) {
		t.Errorf("unchanged re-sync moved updated_at: %s -> %s; it must track content changes, not polls",
			firstUpdated, unchangedUpdated)
	}

	// Property 2: once the heartbeat window has expired, an unchanged re-sync
	// does write, advancing last_seen_at only. Without this the grace-windowed
	// prune would eventually delete rows the node is still reporting.
	upsert(t, heartbeatNow, base)
	beatUpdated, beatSeen, _ := read(t)
	if !beatSeen.After(firstSeen) {
		t.Errorf("expired heartbeat did not advance last_seen_at: %s -> %s; the stale prune would delete live rows",
			firstSeen, beatSeen)
	}
	if !beatUpdated.Equal(firstUpdated) {
		t.Errorf("heartbeat-only refresh moved updated_at: %s -> %s", firstUpdated, beatUpdated)
	}

	// Property 3: a content change writes immediately even when the heartbeat
	// window has not expired, and moves updated_at. MTU is the newest content
	// column, so it doubles as a check that the column reaches the comparison.
	changed := base
	changed.Mtu = 9000
	upsert(t, heartbeatNever, changed)
	afterUpdated, afterSeen, afterMtu := read(t)
	if !afterUpdated.After(beatUpdated) {
		t.Errorf("content change did not move updated_at: %s -> %s", beatUpdated, afterUpdated)
	}
	if !afterSeen.After(beatSeen) {
		t.Errorf("content change did not advance last_seen_at: %s -> %s", beatSeen, afterSeen)
	}
	if afterMtu != 9000 {
		t.Errorf("mtu = %d, want 9000", afterMtu)
	}

	// Property 4: a batch carrying the same conflict key twice must not error.
	// Postgres refuses an ON CONFLICT DO UPDATE that would touch one row twice
	// in a single statement — a hazard the old per-row loop could not hit, so
	// the set-based upsert dedupes with DISTINCT ON. A Proxmox response that
	// repeated an interface would otherwise abort the whole node's sync.
	dup := base
	dup.Mtu = 1234
	upsert(t, heartbeatNow, base, dup)
	var rowCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM node_network_interfaces WHERE node_id = $1 AND iface = $2`,
		invUpdNode, base.Iface).Scan(&rowCount); err != nil {
		t.Fatalf("count interfaces: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("duplicate key in one batch produced %d rows, want 1", rowCount)
	}
}
