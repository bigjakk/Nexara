package db

import (
	"strings"
	"testing"
)

// TestInventoryDedupTargetsMatchSchema asserts the dedup column lists for
// inventory tables match the natural-key UNIQUE constraints declared in
// migrations/000004_inventory.up.sql. The bug this guards against is the
// previous storage_pools entry that used `id` (the primary key) as the
// dedup target, making the DELETE a no-op against actual duplicates.
func TestInventoryDedupTargetsMatchSchema(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		"vms":           "cluster_id, vmid",
		"storage_pools": "cluster_id, node_id, storage",
		"nodes":         "cluster_id, name",
	}

	if len(inventoryDedupTargets) != len(expected) {
		t.Fatalf("inventoryDedupTargets has %d entries, want %d", len(inventoryDedupTargets), len(expected))
	}

	for _, target := range inventoryDedupTargets {
		want, ok := expected[target.table]
		if !ok {
			t.Errorf("unexpected dedup target table: %q", target.table)
			continue
		}
		if target.uniqueCols != want {
			t.Errorf("table %q: uniqueCols = %q, want %q (must match the UNIQUE constraint in the schema, not the primary key)",
				target.table, target.uniqueCols, want)
		}
		if !allowedIntegrityTables[target.table] {
			t.Errorf("table %q is in inventoryDedupTargets but not in allowedIntegrityTables", target.table)
		}
	}
}

// TestHypertableReindexTargetsAllowlisted ensures every hypertable scheduled
// for the opt-in REINDEX pass is on the safety allowlist.
func TestHypertableReindexTargetsAllowlisted(t *testing.T) {
	t.Parallel()

	if len(hypertableReindexTargets) == 0 {
		t.Fatal("hypertableReindexTargets is empty; expected node_metrics and vm_metrics")
	}
	for _, ht := range hypertableReindexTargets {
		if !allowedIntegrityTables[ht] {
			t.Errorf("hypertable %q not in allowedIntegrityTables", ht)
		}
	}
}

// TestDedupQueryShape verifies the generated DELETE keeps the row with the
// LATEST orderCol value per uniqueCols group. Regression check that the
// ORDER BY direction stays DESC — flipping it would silently drop fresh
// rows in favour of stale ones.
func TestDedupQueryShape(t *testing.T) {
	t.Parallel()

	q := dedupQuery(dedupTarget{
		table:      "storage_pools",
		uniqueCols: "cluster_id, node_id, storage",
		orderCol:   "updated_at",
	})

	for _, want := range []string{
		"DELETE FROM storage_pools",
		"DISTINCT ON (cluster_id, node_id, storage)",
		"ORDER BY cluster_id, node_id, storage, updated_at DESC",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q\nfull query:\n%s", want, q)
		}
	}
}

// TestRepairOptionsZeroValueSkipsHypertable documents the contract that the
// zero value of RepairOptions (the value used on every startup) does NOT
// trigger the AccessExclusiveLock-holding hypertable REINDEX.
func TestRepairOptionsZeroValueSkipsHypertable(t *testing.T) {
	t.Parallel()

	var opts RepairOptions
	if opts.ReindexHypertables {
		t.Fatal("RepairOptions{} must not enable ReindexHypertables; the startup path relies on this default")
	}
}
