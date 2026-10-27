package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// historyIndexes are the audit_log indexes the History's selectors read
// (queries/history.sql, ListHistory): an object's own rows by (resource_type,
// resource_id) within a time window, the rows naming it in a details key,
// through details @> {key: value}, and a cluster's own rows newest first, for
// its History. Without them the selectors work, reading every row of the type
// — or of every cluster — instead.
//
// They are built here, after startup, rather than in migration 000117: see
// that migration's header. Infrastructure DDL like EnsureSchema's, so it is
// written out rather than generated. An index of one of these names is ours
// when it has the access method, the key columns — as pg_get_indexdef renders
// each — the operator classes ("" for the type's default) and the predicate
// declared here: anything else is not ours, and is left alone. Not the whole
// rendered definition, which a reloption or a later Postgres's rendering
// changes without changing what the index serves.
var historyIndexes = []historyIndex{
	{name: "idx_audit_log_resource_created", columns: "(resource_type, resource_id, created_at)",
		method: "btree", keys: []string{"resource_type", "resource_id", "created_at"}, opclasses: []string{"", "", ""}},
	{name: "idx_audit_log_details", columns: "USING gin (details jsonb_path_ops)",
		method: "gin", keys: []string{"details"}, opclasses: []string{"jsonb_path_ops"}},
	{name: "idx_audit_log_cluster_created", columns: "(cluster_id, created_at)",
		method: "btree", keys: []string{"cluster_id", "created_at"}, opclasses: []string{"", ""}},
}

// historyIndex is one of historyIndexes: columns is what CREATE INDEX builds
// it from, and the rest is what an index of its name must be to be it.
type historyIndex struct {
	name, columns, method string
	keys, opclasses       []string
	predicate             string
}

// historyIndexLock is the session advisory lock one replica holds while it
// builds them; the others find it taken and leave the build to it.
const historyIndexLock int64 = 0x4E58484953544958 // ASCII "NXHISTIX"

// historyIndexLockTimeout bounds each wait a concurrent build makes for older
// transactions: behind one left idle in a transaction it would otherwise hold
// its lock on audit_log indefinitely. The build fails instead, and is retried.
// A timeout at the last of those waits, after the whole build, leaves the
// index INVALID, which the next attempt drops and builds in full again.
//
// A build outlives the process that started it — Postgres runs it to its end
// — so a migration that locks audit_log, started within minutes of a previous
// start and during its build, waits for it. Accepted rather than given a lock
// timeout in the migrations: a migration that times out leaves the database
// dirty, which no start gets past without an operator.
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
			found     historyIndex
			valid     bool
			predicate string
		)
		err := conn.QueryRow(ctx, `SELECT am.amname,
				array(SELECT pg_get_indexdef(i.indexrelid, k, true) FROM generate_series(1, i.indnkeyatts) AS k ORDER BY k),
				array(SELECT CASE WHEN o.opcdefault THEN '' ELSE o.opcname END
				      FROM unnest(i.indclass::oid[]) WITH ORDINALITY AS c(opclass, n)
				      JOIN pg_opclass o ON o.oid = c.opclass ORDER BY c.n),
				COALESCE(pg_get_expr(i.indpred, i.indrelid, true), ''), i.indisvalid
			FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			JOIN pg_am am ON am.oid = c.relam
			WHERE c.relname = $1 AND c.relnamespace = 'public'::regnamespace
			  AND i.indrelid = 'public.audit_log'::regclass`, ix.name).
			Scan(&found.method, &found.keys, &found.opclasses, &predicate, &valid)
		found.predicate = predicate
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
		case !found.is(ix):
			return fmt.Errorf("public.%s is %s %v %v %q, not the History's %s %v %v %q; leaving it alone", ix.name,
				found.method, found.keys, found.opclasses, found.predicate, ix.method, ix.keys, ix.opclasses, ix.predicate)
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

// is reports whether an index found under ix's name is ix: the same access
// method, key columns, operator classes and predicate.
func (f historyIndex) is(ix historyIndex) bool {
	return f.method == ix.method && slices.Equal(f.keys, ix.keys) && slices.Equal(f.opclasses, ix.opclasses) &&
		f.predicate == ix.predicate
}
