package handlers

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/puddle/v2"
)

// isTransientDBError reports whether err is the database being unreachable, too
// busy or on its way down — a failure in which nothing was decided (or, for a
// commit, the outcome is unknown) and the same request may well succeed a moment
// later — as opposed to a defect in the request or in the code, which fails the
// same way next time.
//
// The auth handlers answer the first kind 503 and the second 500, with the
// cookie and the session untouched either way. They used to answer a 503 for
// every failed lookup and for a write only when the bound ran out, so a
// connection reset in the middle of a rotation was a 500 while the same reset in
// the lookup before it was a 503; one classifier now decides both.
//
// It recognises, and only these:
//   - the caller's own bound running out, or its context being cancelled: the
//     call was abandoned, not refused;
//   - the connection going away or never coming: a reset, a closed or refused
//     connection, an EOF in the middle of a message, a dial that failed or timed
//     out (any net.Error, a pgconn.ConnectError), and whatever pgconn marks
//     SafeToRetry (a lock failure on a closed connection, a context already done
//     — and, because the one test covers them all, the "conn busy" error that
//     means a connection was used from two places at once, a programming error that
//     these handlers cannot reach, since each request owns its connection). That
//     test is here to choose 503 over 500 and nothing more: SafeToRetry is NOT proof
//     that the statement was never executed. pgx 5.10 can report a connection that
//     drops while a statement's reply is awaited the same way — a COMMIT's is a
//     "conn closed" error — and the server may well have executed it. Whether a
//     COMMIT was never sent is commitOutcomeUnknown's question, and it asks it of
//     SafeToRetry together with a context error, never of SafeToRetry alone;
//   - the pool itself being closed, which is what a request that arrives while the
//     server shuts down meets (puddle.ErrClosedPool, which pgxpool returns as it
//     is: it has no sentinel of its own);
//   - the server saying it cannot serve now (see transientSQLState).
//
// Everything else — a constraint violation, an undefined column, a scan that does
// not fit, pgx.ErrNoRows where a row was required — is a bug or a bad request and
// stays a 500.
func isTransientDBError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	if errors.Is(err, puddle.ErrClosedPool) {
		return true
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return transientSQLState(pgErr.Code)
	}
	return false
}

// transientSQLState reports whether a SQLSTATE says the server cannot serve the
// request now rather than that the request is wrong.
func transientSQLState(code string) bool {
	// Class 08, connection exception; class 53, insufficient resources (out of
	// memory, disk full, too many connections).
	if strings.HasPrefix(code, "08") || strings.HasPrefix(code, "53") {
		return true
	}
	switch code {
	case "57P01", // admin_shutdown
		"57P02", // crash_shutdown
		"57P03", // cannot_connect_now
		"57P05", // idle_session_timeout: the server closed a session that sat idle
		"57014", // query_canceled: a statement timeout, or a cancel request
		"40001", // serialization_failure
		"40P01", // deadlock_detected
		"55P03", // lock_not_available: a lock_timeout
		"25006", // read_only_sql_transaction: a write that reached a replica after a failover
		"25P03": // idle_in_transaction_session_timeout: the server ended a transaction left idle
		return true
	}
	return false
}

// commitOutcomeUnknown reports whether a failed COMMIT may nevertheless have
// landed. Three kinds of failure say that it did not:
//
//   - The COMMIT never went out because its context had already ended — the
//     statements before it used up the whole bound. pgconn refuses such a statement
//     without sending anything, and says so twice: SafeToRetry, and the context's
//     own error (an errTimeout around a contextAlreadyDoneError, which unwraps to
//     context.DeadlineExceeded or context.Canceled). The server never saw a COMMIT,
//     and pgx closes the connection of a transaction it could not commit, so the
//     server rolls the transaction back. BOTH halves are required, because neither
//     proves it alone. A context error alone is also what a deadline that ran out
//     while the reply was awaited looks like — the case that may have landed.
//     SafeToRetry alone is also what pgx reports for a connection that drops while
//     the reply is awaited: a "conn closed" error (a *pgconn.connLockError, which
//     says SafeToRetry because a lock failure happens before the connection is
//     used) although the server may have executed the COMMIT, as a proxy that lets
//     Postgres commit and drops the reply shows, with the row committed
//     (TestALostCommitReplyLooksSafeToRetryAndHasLanded, in internal/db, pins it).
//     This has to be asked first: to anything below, a never-sent COMMIT looks
//     exactly like one that was cut off.
//   - The server answered. A *pgconn.PgError is the server's own reply to the
//     COMMIT, and a COMMIT it answers with an error is one it did not perform: in
//     PostgreSQL a transaction either commits or is aborted, and an error during
//     the commit (a serialization failure, a deadlock, a failed deferred
//     constraint) aborts it — there is no half-committed state to report. The same
//     goes for pgx.ErrTxCommitRollback, the server answering COMMIT with ROLLBACK.
//   - The transaction was already closed (pgx.ErrTxClosed), so nothing was sent.
//
// Anything else — a reset or an EOF after the COMMIT went out, a deadline that ran
// out while the reply was awaited, an error nobody classified — leaves the
// outcome unknown: the server may have committed and the answer been lost.
//
// A caller that must tell its user what happened uses it to choose between "it was
// not done" and "it may have been done": a retry of the first is safe, a retry of
// the second can find the work already in place.
func commitOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	if pgconn.SafeToRetry(err) && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return false
	}
	return !errors.Is(err, pgx.ErrTxCommitRollback) && !errors.Is(err, pgx.ErrTxClosed)
}
