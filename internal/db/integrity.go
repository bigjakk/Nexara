package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RepairIntegrity detects and fixes common data integrity issues that can arise
// from index corruption or concurrent collector instances. It removes duplicate
// rows and reindexes affected tables. Safe to run on every startup.
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

	return nil
}
