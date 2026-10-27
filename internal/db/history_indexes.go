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
// written out rather than generated. def is exactly what pg_get_indexdef
// renders for the index this code builds: an index of the same name that is
// anything else is not ours, and is left alone.
var historyIndexes = []struct{ name, columns, def string }{
	{"idx_audit_log_resource_created", "(resource_type, resource_id, created_at)",
		"CREATE INDEX idx_audit_log_resource_created ON public.audit_log USING btree (resource_type, resource_id, created_at)"},
	{"idx_audit_log_details", "USING gin (details jsonb_path_ops)",
		"CREATE INDEX idx_audit_log_details ON public.audit_log USING gin (details jsonb_path_ops)"},
}

// historyIndexLock is the session advisory lock one replica holds while it
// builds them; the others find it taken and leave the build to it.
const historyIndexLock int64 = 0x4E58484953544958 // ASCII "NXHISTIX"

// historyIndexLockTimeout bounds each wait a concurrent build makes for older
// transactions: behind one left idle in a transaction it would otherwise hold
// its lock on audit_log indefinitely. The build fails instead, and is retried.
const historyIndexLockTimeout = "1min"

// BuildHistoryIndexes runs EnsureHistoryIndexes until it succeeds, waiting
// historyIndexBackoff between failures — a restart of the database mid-build,
// a lock timeout — because the INVALID index a failed build leaves is
// maintained on every audit write until it is rebuilt. Each attempt runs to its
// end even when ctx is done, so a shutdown does not cancel a build and leave
// that index behind; ctx only stops the retries.
func BuildHistoryIndexes(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	for attempt := 0; ; attempt++ {
		err := EnsureHistoryIndexes(context.WithoutCancel(ctx), pool, logger)
		if err == nil {
			return
		}
		wait := historyIndexBackoff(attempt)
		logger.Error("history indexes not built; the History reads without them, slower", "error", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// historyIndexBackoff is the wait after the attempt'th failure: a minute,
// doubling to half an hour.
func historyIndexBackoff(attempt int) time.Duration {
	wait := time.Minute << min(attempt, 5)
	return min(wait, 30*time.Minute)
}

// EnsureHistoryIndexes builds whichever of historyIndexes is missing, once, on
// one replica: the one that takes historyIndexLock. CREATE INDEX CONCURRENTLY
// blocks no audit write, and an INVALID index — what a concurrent build that
// was interrupted leaves behind — is dropped, concurrently, and built again.
// An index of one of these names that is not this one, or not on
// public.audit_log, is refused rather than skipped: the History would read
// without the index it was promised.
//
// It uses a connection of its own, closed at the end, so the lock and the
// session's settings — a fixed search_path, no statement timeout, a lock
// timeout — leave with it.
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
	for _, set := range []string{
		"SET search_path = pg_catalog, public",
		"SET statement_timeout = 0",
		"SET lock_timeout = '" + historyIndexLockTimeout + "'",
	} {
		if _, err := conn.Exec(ctx, set, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("%s: %w", set, err)
		}
	}
	for _, ix := range historyIndexes {
		var (
			def   string
			valid bool
		)
		err := conn.QueryRow(ctx, `SELECT pg_get_indexdef(i.indexrelid), i.indisvalid FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = $1 AND c.relnamespace = 'public'::regnamespace
			  AND i.indrelid = 'public.audit_log'::regclass`, ix.name).Scan(&def, &valid)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			var taken bool
			if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class
				WHERE relname = $1 AND relnamespace = 'public'::regnamespace)`, ix.name).Scan(&taken); err != nil {
				return fmt.Errorf("look up the name %s: %w", ix.name, err)
			}
			if taken {
				return fmt.Errorf("public.%s exists and is not an index on public.audit_log; leaving it alone", ix.name)
			}
		case err != nil:
			return fmt.Errorf("look up the index %s: %w", ix.name, err)
		case def != ix.def:
			return fmt.Errorf("public.%s is %q, not the History's %q; leaving it alone", ix.name, def, ix.def)
		case valid:
			continue
		default:
			logger.Warn("history indexes: dropping an interrupted build", "index", ix.name)
			if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY IF EXISTS public."+ix.name, pgx.QueryExecModeSimpleProtocol); err != nil {
				return fmt.Errorf("drop the invalid index %s: %w", ix.name, err)
			}
		}
		start := time.Now()
		logger.Info("history indexes: building", "index", ix.name)
		if _, err := conn.Exec(ctx, "CREATE INDEX CONCURRENTLY IF NOT EXISTS "+ix.name+" ON public.audit_log "+ix.columns,
			pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("build the index %s: %w", ix.name, err)
		}
		logger.Info("history indexes: built", "index", ix.name, "took", time.Since(start).Round(time.Millisecond))
	}
	return nil
}
