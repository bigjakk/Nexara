package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// allowedIntegrityTables is the compile-time allowlist of tables that
// RepairIntegrity may operate on. This prevents any future code change
// from accidentally introducing SQL injection via dynamic table names.
var allowedIntegrityTables = map[string]bool{
	"vms":           true,
	"storage_pools": true,
	"nodes":         true,
	"node_metrics":  true,
	"vm_metrics":    true,
}

// RepairOptions controls the optional, expensive parts of RepairIntegrity.
type RepairOptions struct {
	// ReindexHypertables runs `REINDEX TABLE` on `node_metrics` and
	// `vm_metrics`. This rewrites every chunk's index, holds
	// AccessExclusiveLock on the entire hypertable, and can take hours on
	// large installs. Must be invoked explicitly (e.g. via the
	// `nexara repair-integrity` CLI subcommand) — never on startup.
	ReindexHypertables bool
}

// dedupTarget describes one inventory table's natural unique key and the
// column used to pick which duplicate to keep when one is found.
type dedupTarget struct {
	table      string
	uniqueCols string // columns that should be unique together
	orderCol   string // keep the row with the latest value of this column
}

// inventoryDedupTargets is the list of inventory tables RepairIntegrity
// dedups on every startup. Exposed at package level so tests can assert
// the column lists match the schema's UNIQUE constraints.
var inventoryDedupTargets = []dedupTarget{
	{table: "vms", uniqueCols: "cluster_id, vmid", orderCol: "last_seen_at"},
	{table: "storage_pools", uniqueCols: "cluster_id, node_id, storage", orderCol: "updated_at"},
	{table: "nodes", uniqueCols: "cluster_id, name", orderCol: "last_seen_at"},
}

// hypertableReindexTargets is the list of metric hypertables whose indexes
// are rebuilt by the opt-in hypertable REINDEX pass.
var hypertableReindexTargets = []string{"node_metrics", "vm_metrics"}

// dedupQuery builds the DELETE that removes duplicate rows from an inventory
// table, keeping the row with the latest orderCol per uniqueCols group. The
// caller must verify table is allowlisted before passing it in — we do not
// quote identifiers here because they are compile-time constants.
func dedupQuery(t dedupTarget) string {
	return fmt.Sprintf(`
			DELETE FROM %s WHERE ctid NOT IN (
				SELECT DISTINCT ON (%s) ctid
				FROM %s
				ORDER BY %s, %s DESC
			)`, t.table, t.uniqueCols, t.table, t.uniqueCols, t.orderCol)
}

// RepairIntegrity detects and fixes common data integrity issues that can arise
// from index corruption or concurrent collector instances. It removes duplicate
// rows from inventory tables and (optionally) reindexes hypertables.
//
// The dedup pass is cheap and safe to run on every startup: each affected
// table has a UNIQUE constraint that should make duplicates impossible by
// insertion, so the DELETE is a no-op in normal operation.
//
// The hypertable REINDEX is gated by opts.ReindexHypertables because it is
// AccessExclusiveLock-blocking and slow.
func RepairIntegrity(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, opts RepairOptions) error {
	totalDeleted := int64(0)

	for _, t := range inventoryDedupTargets {
		// SAFETY: All table/column names are compile-time constants defined above.
		// Verify against the allowlist to guard against future misuse.
		if !allowedIntegrityTables[t.table] {
			return fmt.Errorf("integrity repair: disallowed table %q", t.table)
		}
		query := dedupQuery(t)

		tag, err := pool.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("dedup %s: %w", t.table, err)
		}

		if tag.RowsAffected() > 0 {
			logger.Warn("removed duplicate rows",
				"table", t.table,
				"count", tag.RowsAffected(),
			)
			totalDeleted += tag.RowsAffected()

			// Reindex the table after removing duplicates. These are
			// non-hypertable inventory tables — the operation is fast
			// (seconds, not hours) and only runs when duplicates were
			// actually detected.
			if _, err := pool.Exec(ctx, fmt.Sprintf("REINDEX TABLE %s", t.table)); err != nil {
				logger.Error("failed to reindex table", "table", t.table, "error", err)
			}
		}
	}

	if totalDeleted > 0 {
		logger.Info("integrity repair complete", "total_duplicates_removed", totalDeleted)
	}

	if !opts.ReindexHypertables {
		return nil
	}

	// Validate and repair hypertable indexes. TimescaleDB chunk indexes are
	// especially prone to corruption after unclean shutdowns. REINDEX holds
	// AccessExclusiveLock on the entire hypertable for the duration — only
	// run when explicitly requested.
	for _, ht := range hypertableReindexTargets {
		if !allowedIntegrityTables[ht] {
			return fmt.Errorf("integrity repair: disallowed hypertable %q", ht)
		}
		logger.Info("reindexing hypertable (this may take minutes to hours and blocks writes)", "table", ht)
		if _, err := pool.Exec(ctx, fmt.Sprintf("REINDEX TABLE %s", ht)); err != nil {
			logger.Error("failed to reindex hypertable — index may be corrupted",
				"table", ht,
				"error", err,
			)
		} else {
			logger.Info("reindexed hypertable", "table", ht)
		}
	}

	return nil
}
