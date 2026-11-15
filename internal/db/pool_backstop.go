package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The server-side backstops on every connection of the app's pool, for work
// that has no deadline of its own. Nothing the app does legitimately comes
// near them: its transactions are a handful of statements with nothing slow
// between them (the longest gap is Register's bcrypt, well under a second), and
// its lock waits are row and advisory locks held by those transactions, or a
// read queued behind a retention drop_chunks for as long as one chart or report
// query runs. A wedged lock or an abandoned transaction surfaces in a minute or
// two instead of holding connections until the pool is gone.
//
// A value set anywhere else wins: DATABASE_URL (?lock_timeout=…), PGOPTIONS,
// ALTER ROLE / ALTER DATABASE, or postgresql.conf. golang-migrate's connection
// never gets these (toPgx5URL strips the two keys from its URL too): a
// migration may wait for a lock or run long.
const (
	defaultLockTimeout                     = "2min"
	defaultIdleInTransactionSessionTimeout = "1min"
)

// newAppPool opens the app's pool with the backstops.
func newAppPool(ctx context.Context, databaseURL string, logger *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := newAppPoolConfig(databaseURL, logger)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// newAppPoolConfig parses databaseURL for the app's pool and adds the
// backstops and the tracer that logs them firing.
func newAppPoolConfig(databaseURL string, logger *slog.Logger) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.AfterConnect = applySessionBackstops
	if cfg.ConnConfig.Tracer == nil {
		cfg.ConnConfig.Tracer = backstopTracer{logger: logger}
	}
	return cfg, nil
}

func applySessionBackstops(ctx context.Context, conn *pgx.Conn) error {
	if err := gen.New(conn).ApplySessionTimeoutDefaults(ctx, gen.ApplySessionTimeoutDefaultsParams{
		LockTimeout:                     defaultLockTimeout,
		IdleInTransactionSessionTimeout: defaultIdleInTransactionSessionTimeout,
	}); err != nil {
		return fmt.Errorf("set the session timeouts: %w", err)
	}
	return nil
}

// backstopTracer logs a statement the backstops ended, once, by its sqlc
// query name (or the head of its text) and never its arguments. The caller
// still gets the error, wrapped as usual: a 503 from a handler that classifies
// database errors (isTransientDBError), a 500 from one that does not. The WARN
// line names the query either way.
type backstopTracer struct{ logger *slog.Logger }

type tracedSQLKey struct{}

func (backstopTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, tracedSQLKey{}, data.SQL)
}

func (t backstopTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	var pgErr *pgconn.PgError
	if data.Err == nil || !errors.As(data.Err, &pgErr) {
		return
	}
	var msg string
	switch pgErr.Code {
	case "55P03":
		msg = "database: a statement waited past lock_timeout for a lock and was cancelled"
	case "25P03":
		// Reported by the transaction's next statement, which is the one named.
		msg = "database: the server ended a transaction left idle past idle_in_transaction_session_timeout"
	default:
		return
	}
	sql, _ := ctx.Value(tracedSQLKey{}).(string)
	t.logger.Warn(msg, "statement", statementLabel(sql), "error", pgErr.Message)
}

// statementLabel names a statement for a log line: an sqlc query's
// "-- name: X" header, otherwise its first line cut to 120 bytes.
func statementLabel(sql string) string {
	sql = strings.TrimSpace(sql)
	first, _, _ := strings.Cut(sql, "\n")
	if name, ok := strings.CutPrefix(first, "-- name: "); ok {
		name, _, _ = strings.Cut(name, " ")
		return name
	}
	if len(first) > 120 {
		first = first[:120]
	}
	return first
}
