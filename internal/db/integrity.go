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

// RepairIntegrity detects and fixes common data integrity issues that can arise
// from index corruption or concurrent collector instances. It removes duplicate
// rows, reindexes affected tables, and validates hypertable indexes.
// Safe to run on every startup.
func RepairIntegrity(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	type dedupTarget struct {
		table      string
		uniqueCols string // columns that should be unique together
		orderCol   string // keep the row with the latest value of this column
	}

	targets := []dedupTarget{
		{table: "vms", uniqueCols: "cluster_id, vmid", orderCol: "last_seen_at"},
		{table: "storage_pools", uniqueCols: "id", orderCol: "updated_at"},
		{table: "nodes", uniqueCols: "cluster_id, name", orderCol: "last_seen_at"},
	}

	totalDeleted := int64(0)

	for _, t := range targets {
		// SAFETY: All table/column names are compile-time constants defined above.
		// Verify against the allowlist to guard against future misuse.
		if !allowedIntegrityTables[t.table] {
			return fmt.Errorf("integrity repair: disallowed table %q", t.table)
		}
		query := fmt.Sprintf(`
			DELETE FROM %s WHERE ctid NOT IN (
				SELECT DISTINCT ON (%s) ctid
				FROM %s
				ORDER BY %s, %s DESC
			)`, t.table, t.uniqueCols, t.table, t.uniqueCols, t.orderCol)

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

			// Reindex the table after removing duplicates.
			if _, err := pool.Exec(ctx, fmt.Sprintf("REINDEX TABLE %s", t.table)); err != nil {
				logger.Error("failed to reindex table", "table", t.table, "error", err)
			}
		}
	}

	if totalDeleted > 0 {
		logger.Info("integrity repair complete", "total_duplicates_removed", totalDeleted)
	}

	// Validate and repair hypertable indexes. TimescaleDB chunk indexes are
	// especially prone to corruption after unclean shutdowns. We attempt a
	// REINDEX on each metrics hypertable and log any failures (non-fatal).
	hypertables := []string{"node_metrics", "vm_metrics"}
	for _, ht := range hypertables {
		if !allowedIntegrityTables[ht] {
			return fmt.Errorf("integrity repair: disallowed hypertable %q", ht)
		}
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
