package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// historyIndexes are the audit_log indexes the History's generic selector
// reads (queries/history.sql, ListHistory): an object's own rows by
// (resource_type, resource_id) within a time window, and the rows naming it in
// a details key, through details @> {key: value}. Without them the selector
// works, reading every row of the type instead.
//
// They are built here, after startup, rather than in migration 000117: see
// that migration's header. Infrastructure DDL like EnsureSchema's, so it is
// written out rather than generated.
var historyIndexes = []struct{ name, columns string }{
	{"idx_audit_log_resource_created", "(resource_type, resource_id, created_at)"},
	{"idx_audit_log_details", "USING gin (details jsonb_path_ops)"},
}

// historyIndexLock is the session advisory lock one replica holds while it
// builds them; the others find it taken and leave the build to it.
const historyIndexLock int64 = 0x4E58484953544958 // ASCII "NXHISTIX"

// EnsureHistoryIndexes builds whichever of historyIndexes is missing, once, on
// one replica: the one that takes historyIndexLock. CREATE INDEX CONCURRENTLY
// blocks no audit write, and an INVALID index — what a concurrent build that
// was interrupted leaves behind — is dropped, concurrently, and built again.
//
// Run it in a goroutine after EnsureSchema. It uses a connection of its own,
// closed at the end, so the lock and the session's settings leave with it.
func EnsureHistoryIndexes(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	pooled, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection for the history indexes: %w", err)
	}
	conn := pooled.Hijack()
	defer func() { _ = conn.Close(context.Background()) }()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", historyIndexLock).Scan(&locked); err != nil {
		return fmt.Errorf("take the history index lock: %w", err)
	}
	if !locked {
		logger.Info("history indexes: another instance is building them")
		return nil
	}
	// A build is as long as the table is large: no statement timeout cuts it.
	if _, err := conn.Exec(ctx, "SET statement_timeout = 0", pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("clear the statement timeout: %w", err)
	}
	for _, ix := range historyIndexes {
		var valid bool
		err := conn.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = $1 AND c.relnamespace = 'public'::regnamespace`, ix.name).Scan(&valid)
		switch {
		case err == nil && valid:
			continue
		case err == nil:
			logger.Warn("history indexes: dropping an interrupted build", "index", ix.name)
			if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY IF EXISTS "+ix.name, pgx.QueryExecModeSimpleProtocol); err != nil {
				return fmt.Errorf("drop the invalid index %s: %w", ix.name, err)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("look up the index %s: %w", ix.name, err)
		}
		start := time.Now()
		logger.Info("history indexes: building", "index", ix.name)
		if _, err := conn.Exec(ctx, "CREATE INDEX CONCURRENTLY IF NOT EXISTS "+ix.name+" ON audit_log "+ix.columns,
			pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("build the index %s: %w", ix.name, err)
		}
		logger.Info("history indexes: built", "index", ix.name, "took", time.Since(start).Round(time.Millisecond))
	}
	return nil
}
