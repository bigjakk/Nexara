package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The handler half of the refresh/sign-out race: the real Refresh and Logout,
// through a real Fiber app, the real SessionManager over miniredis, and a
// stand-in for the sessions table.
//
// The stand-in (raceStore) applies the same predicates the SQL does — a lookup
// hits only on a matching, unrevoked hash, the rotation only on the session's
// current hash while it is live — so what the handler sends is what decides the
// outcome, and an argument in the wrong place shows up as a refusal here rather
// than passing a test that returned canned row counts. It is NOT evidence about
// the SQL: that those predicates are the ones queries/sessions.sql states, and
// that the interleavings come out as claimed under READ COMMITTED, is pinned
// against Postgres in internal/db/session_rotation_db_test.go.
//
// That test cannot live in this package. CI exports NEXARA_TEST_DB_URL for the
// whole of `go test ./...`, whose packages run in parallel, and the migration
// chain test in internal/db migrates that shared database down to zero; a DB test
// here would be dropped out from under mid-run.

// raceStatement is one statement the handler sent: the sqlc query name, its
// arguments, and whether it ran on the refresh's transaction.
type raceStatement struct {
	name string
	args []any
	inTx bool
}

// raceStore is the sessions table and users table as the handler sees them.
// Every field is guarded by mu; beforeRotate is called with it held.
type raceStore struct {
	mu sync.Mutex

	session db.Session
	deleted bool // the row is gone (its user was deleted: sessions cascade)

	user    db.User
	userErr error // if set, GetUserByID fails with it
	// userDeleted is the user row being gone: GetUserByID and the read that settles a
	// COMMIT find nothing, and an UpdatePassword for it matches no row. A test sets it
	// between two statements of one request to delete the account in the gap between
	// them.
	userDeleted bool
	// afterCommitUserErr, once a COMMIT has been attempted, makes the read that
	// settles a commit whose outcome is unknown (GetPasswordHashForSettle) fail with
	// it: the read-back failing too.
	afterCommitUserErr error
	commitAttempted    bool

	// permissions are the rows GetUserPermissions returns; permsErr, when set, makes
	// it fail instead. Refresh reads them before it begins its transaction.
	permissions []db.GetUserPermissionsRow
	permsErr    error
	listErr     error // if set, ListUserSessions fails with it

	currentErr  error // if set, GetSessionByTokenHash fails with it
	previousErr error // if set, GetSessionByPreviousTokenHash fails with it

	// beforeRotate runs inside RotateSessionToken, ahead of its WHERE clause. It
	// is a writer landing in the gap between the refresh's validation and its
	// rotation — the gap the whole change exists to close. It mutates the store
	// directly (the lock is held).
	beforeRotate func(*raceStore)
	rotateErr    error // if set, RotateSessionToken fails with it
	revokeErr    error // if set, RevokeSession fails with it
	revokeAllErr error // if set, RevokeAllUserSessions fails with it
	updatePwErr  error // if set, UpdatePassword fails with it
	commitErr    error // if set, committing the transaction fails with it
	// commitLands says, when commitErr is set, whether the commit nevertheless
	// landed — the server committed and the answer was lost. The handler cannot
	// tell the two apart, which is the point: its answer must be the same, and
	// the store shows which one it was.
	commitLands bool
	rollbackErr error // if set, rolling the refresh's transaction back fails with it

	// lockWait models a statement waiting for a row lock another transaction holds.
	// It is called, with the store unlocked, with the statement's context and name
	// — "Commit" for a commit — once the statement has its connection: after a pool
	// statement has acquired one, ahead of a transaction's own. It returns nil when
	// the wait is over and the statement may run, or the context's error when the
	// context ended first, which is what a real wait does and what a stand-in that
	// ignored the context could never show.
	lockWait func(ctx context.Context, name string) error

	// afterPool runs, with the store unlocked, after a statement on the POOL has
	// completed and given its connection back, and is passed the statement's name.
	// It is how a test starts starving the pool between one statement and the next.
	afterPool func(name string)

	// openTx is how many transactions are open — each holds a connection of the
	// pool until it commits or rolls back — and poolWhileTx names every statement
	// that went to the POOL while one was. A request that asks the pool for a
	// second connection while it still holds one is how a pool of N connections
	// deadlocks under N+1 such requests, so a single request must never do it.
	// The count is global, so it is only meaningful where requests do not overlap.
	openTx      int
	poolWhileTx []string

	stmts        []raceStatement
	audits       []string // the action of each audit row written
	auditDetails []string // and its details, in the same order
}

func sqlName(sql string) string {
	rest := strings.TrimPrefix(sql, "-- name: ")
	name, _, _ := strings.Cut(rest, " ")
	return name
}

func (s *raceStore) record(sql string, args []any, inTx bool) string {
	name := sqlName(sql)
	s.stmts = append(s.stmts, raceStatement{name: name, args: args, inTx: inTx})
	if !inTx && s.openTx > 0 {
		s.poolWhileTx = append(s.poolWhileTx, name)
	}
	return name
}

// waitLock runs the lock-wait hook for a statement, if there is one.
func (s *raceStore) waitLock(ctx context.Context, name string) error {
	s.mu.Lock()
	hook := s.lockWait
	s.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(ctx, name)
}

func (s *raceStore) poolStatementDone(sql string) {
	s.mu.Lock()
	hook := s.afterPool
	s.mu.Unlock()
	if hook != nil {
		hook(sqlName(sql))
	}
}

// noteCommitAttempt records that a COMMIT has been asked for, which is what
// afterCommitUserErr waits for.
func (s *raceStore) noteCommitAttempt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitAttempted = true
}

func (s *raceStore) txOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openTx++
}

func (s *raceStore) txClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openTx--
}

func (s *raceStore) openTransactions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openTx
}

func (s *raceStore) poolStatementsWhileTx() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.poolWhileTx...)
}

// exec runs a write. undo is the log of the transaction the statement belongs to,
// nil for a statement on the pool, which commits as it runs: every write that
// changes the store appends the inverse of its change, and a transaction that
// rolls back — or whose commit does not land — replays them in reverse. Without
// it a handler that fails after its first write would look, to a test, as if the
// write had stuck.
func (s *raceStore) exec(sql string, args []any, inTx bool, undo *[]func()) (pgconn.CommandTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// remember arranges for restore to be called if the statement's transaction is
	// undone; a statement on the pool is never undone.
	remember := func(restore func()) {
		if undo != nil {
			*undo = append(*undo, restore)
		}
	}

	switch name := s.record(sql, args, inTx); name {
	case "RotateSessionToken":
		if s.rotateErr != nil {
			return pgconn.CommandTag{}, s.rotateErr
		}
		if s.beforeRotate != nil {
			s.beforeRotate(s)
		}
		// sqlc's argument order: new hash, role, id, then the hash the rotation
		// is conditional on. Mirrors the WHERE clause of RotateSessionToken.
		newHash, role := args[0].(string), args[1].(string)
		id, oldHash := args[2].(uuid.UUID), args[3].(string)
		now := time.Now()
		if s.deleted || id != s.session.ID || oldHash != s.session.TokenHash ||
			s.session.IsRevoked || !s.session.ExpiresAt.After(now) {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		before := s.session
		remember(func() { s.session = before })
		s.session.PreviousTokenHash = pgtype.Text{String: oldHash, Valid: true}
		s.session.RotatedAt = pgtype.Timestamptz{Time: now, Valid: true}
		s.session.TokenHash = newHash
		s.session.UserRole = role
		s.session.LastUsedAt = now
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeSession":
		if s.revokeErr != nil {
			return pgconn.CommandTag{}, s.revokeErr
		}
		if id, ok := args[0].(uuid.UUID); ok && id == s.session.ID && !s.deleted {
			was := s.session.IsRevoked
			remember(func() { s.session.IsRevoked = was })
			s.session.IsRevoked = true
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeAllUserSessions":
		if s.revokeAllErr != nil {
			return pgconn.CommandTag{}, s.revokeAllErr
		}
		if id, ok := args[0].(uuid.UUID); ok && id == s.session.UserID && !s.deleted {
			was := s.session.IsRevoked
			remember(func() { s.session.IsRevoked = was })
			s.session.IsRevoked = true
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "UpdatePassword":
		if s.updatePwErr != nil {
			return pgconn.CommandTag{}, s.updatePwErr
		}
		// sqlc's argument order: the new hash, the user's id, then the hash the update
		// is conditional on. Mirrors the WHERE clause of UpdatePassword: an account
		// that is gone, or whose password is no longer the one the caller verified,
		// matches no row. (The SQL itself is exercised against Postgres in
		// internal/db, including the interleaving.)
		newHash, id, expected := args[0].(string), args[1].(uuid.UUID), args[2].(string)
		if s.userDeleted || id != s.user.ID || expected != s.user.PasswordHash {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		was := s.user.PasswordHash
		remember(func() { s.user.PasswordHash = was })
		s.user.PasswordHash = newHash
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "InsertAuditLog":
		// cluster_id, user_id, resource_type, resource_id, action, details
		s.audits = append(s.audits, args[4].(string))
		details, _ := args[5].(json.RawMessage)
		s.auditDetails = append(s.auditDetails, string(details))
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", name)
	}
}

func (s *raceStore) queryRow(sql string, args []any, inTx bool) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch name := s.record(sql, args, inTx); name {
	case "GetSessionByTokenHash":
		if s.currentErr != nil {
			return raceRow{err: s.currentErr}
		}
		if !s.deleted && !s.session.IsRevoked && args[0] == s.session.TokenHash {
			return raceRow{value: s.session}
		}
		return raceRow{err: pgx.ErrNoRows}
	case "GetSessionByPreviousTokenHash":
		if s.previousErr != nil {
			return raceRow{err: s.previousErr}
		}
		window := time.Duration(args[1].(float64) * float64(time.Second))
		prev := s.session.PreviousTokenHash
		if !s.deleted && !s.session.IsRevoked && s.session.ExpiresAt.After(time.Now()) &&
			prev.Valid && args[0] == prev.String &&
			s.session.RotatedAt.Valid && time.Since(s.session.RotatedAt.Time) < window {
			return raceRow{value: s.session}
		}
		return raceRow{err: pgx.ErrNoRows}
	case "GetUserByID":
		if s.userErr != nil {
			return raceRow{err: s.userErr}
		}
		if s.userDeleted {
			return raceRow{err: pgx.ErrNoRows}
		}
		return raceRow{value: s.user}
	case "GetPasswordHashForSettle":
		// The locking read that settles a COMMIT whose outcome is unknown. The stand-in
		// has no row locks to wait on — that the real statement waits for a transaction
		// the server is still finishing is pinned against Postgres, in internal/db — so it
		// answers what the committed row holds, or what a test made it fail with.
		if s.commitAttempted && s.afterCommitUserErr != nil {
			return raceScalarRow{err: s.afterCommitUserErr}
		}
		if s.userDeleted {
			return raceScalarRow{err: pgx.ErrNoRows}
		}
		return raceScalarRow{value: s.user.PasswordHash}
	default:
		return raceRow{err: fmt.Errorf("unexpected QueryRow: %s", name)}
	}
}

// query answers the :many statements the handlers under test send. Like the
// others it applies the SQL's own predicate: ListUserSessions returns the user's
// live sessions only.
func (s *raceStore) query(sql string, args []any, inTx bool) (pgx.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch name := s.record(sql, args, inTx); name {
	case "GetUserPermissions":
		if s.permsErr != nil {
			return nil, s.permsErr
		}
		items := make([]any, 0, len(s.permissions))
		for _, p := range s.permissions {
			items = append(items, p)
		}
		return &raceRows{items: items}, nil
	case "ListUserSessions":
		if s.listErr != nil {
			return nil, s.listErr
		}
		var items []any
		if id, ok := args[0].(uuid.UUID); ok && !s.deleted && id == s.session.UserID &&
			!s.session.IsRevoked && s.session.ExpiresAt.After(time.Now()) {
			items = append(items, s.session)
		}
		return &raceRows{items: items}, nil
	default:
		return nil, fmt.Errorf("unexpected Query: %s", name)
	}
}

// statements returns every statement sent so far, in order.
func (s *raceStore) statements() []raceStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]raceStatement(nil), s.stmts...)
}

// named returns the statements sent for one query, in order.
func (s *raceStore) named(name string) []raceStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []raceStatement
	for _, st := range s.stmts {
		if st.name == name {
			out = append(out, st)
		}
	}
	return out
}

func (s *raceStore) snapshot() db.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *raceStore) snapshotUser() db.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user
}

func (s *raceStore) auditActions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.audits...)
}

func (s *raceStore) auditDetailsWritten() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auditDetails...)
}

// raceScalarRow replays one column — or an error — through pgx.Row, for the
// statements that select a single value instead of a model.
type raceScalarRow struct {
	value any
	err   error
}

func (r raceScalarRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("scan got %d destinations, want 1", len(dest))
	}
	ptr, val := reflect.ValueOf(dest[0]), reflect.ValueOf(r.value)
	if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != val.Type() {
		return fmt.Errorf("scan destination is %T, want *%s", dest[0], val.Type())
	}
	ptr.Elem().Set(val)
	return nil
}

// raceRow replays one generated model — or an error — through pgx.Row,
// assigning positionally in the struct's field order, which is the order the
// SELECTs scan in. A column added in the middle reports a type mismatch rather
// than shifting a value into the wrong field.
type raceRow struct {
	value any
	err   error
}

func (r raceRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanStruct(r.value, dest)
}

// scanStruct assigns a generated model's fields to dest, positionally.
func scanStruct(value any, dest []any) error {
	row := reflect.ValueOf(value)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s — the select column order changed",
				i, d, row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// raceRows replays generated models through pgx.Rows, one per Next. The embedded
// pgx.Rows is nil on purpose: the generated :many code uses only what is
// implemented here, and anything else panics.
type raceRows struct {
	pgx.Rows
	items []any
	pos   int
}

func (r *raceRows) Next() bool {
	if r.pos >= len(r.items) {
		return false
	}
	r.pos++
	return true
}

func (r *raceRows) Scan(dest ...any) error { return scanStruct(r.items[r.pos-1], dest) }
func (r *raceRows) Close()                 {}
func (r *raceRows) Err() error             { return nil }

// raceGate models the connections of a pool of fixed size. acquire takes one,
// waiting until one is free or the context ends — which is what pgxpool's Acquire
// does — and release gives it back. Waiters are served in the order they arrived,
// and a connection given back goes straight to the first of them. A nil gate is a
// pool with no limit.
type raceGate struct {
	tokens chan struct{}

	mu      sync.Mutex
	waiting int
}

func newRaceGate(size int) *raceGate { return &raceGate{tokens: make(chan struct{}, size)} }

func (g *raceGate) acquire(ctx context.Context) error {
	// A context that has already ended gets nothing, even from an idle pool: that
	// is how pgxpool behaves, and it is what makes a handler that spent its bound
	// elsewhere fail its next statement at once.
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil {
		return nil
	}
	select {
	case g.tokens <- struct{}{}:
		return nil
	default:
	}
	g.mu.Lock()
	g.waiting++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.waiting--
		g.mu.Unlock()
	}()
	select {
	case g.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waiters is how many callers are waiting for a connection right now.
func (g *raceGate) waiters() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waiting
}

func (g *raceGate) release() {
	if g != nil {
		<-g.tokens
	}
}

func (g *raceGate) inUse() int {
	if g == nil {
		return 0
	}
	return len(g.tokens)
}

// raceConn is the pool-bound path: what h.queries and the SessionManager use. Each
// statement takes a connection of the gate's pool for as long as it runs.
type raceConn struct {
	store *raceStore
	gate  *raceGate
}

func (c raceConn) Exec(ctx context.Context, sql string, args ...any) (tag pgconn.CommandTag, err error) {
	ran := false
	func() {
		if err = c.gate.acquire(ctx); err != nil {
			return
		}
		defer c.gate.release()
		ran = true
		if err = c.store.waitLock(ctx, sqlName(sql)); err != nil {
			return
		}
		tag, err = c.store.exec(sql, args, false, nil)
	}()
	if ran {
		c.store.poolStatementDone(sql)
	}
	return tag, err
}

func (c raceConn) Query(ctx context.Context, sql string, args ...any) (rows pgx.Rows, err error) {
	ran := false
	func() {
		if err = c.gate.acquire(ctx); err != nil {
			return
		}
		defer c.gate.release()
		ran = true
		if err = c.store.waitLock(ctx, sqlName(sql)); err != nil {
			return
		}
		rows, err = c.store.query(sql, args, false)
	}()
	if ran {
		c.store.poolStatementDone(sql)
	}
	return rows, err
}

func (c raceConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	ran := false
	row := func() pgx.Row {
		if err := c.gate.acquire(ctx); err != nil {
			return raceRow{err: err}
		}
		defer c.gate.release()
		ran = true
		if err := c.store.waitLock(ctx, sqlName(sql)); err != nil {
			return raceRow{err: err}
		}
		return c.store.queryRow(sql, args, false)
	}()
	if ran {
		c.store.poolStatementDone(sql)
	}
	return row
}

// raceTx is the refresh's transaction. The embedded pgx.Tx is nil on purpose:
// Refresh may use only what is implemented below, and anything else panics
// rather than quietly doing nothing.
//
// It holds one connection of the pool from Begin until the FIRST of Commit and
// Rollback — a second of either answers pgx.ErrTxClosed, as pgx's does, and gives
// nothing back twice.
type raceTx struct {
	pgx.Tx
	store *raceStore
	gate  *raceGate

	mu         sync.Mutex
	closed     bool
	committed  bool
	rolledBack bool

	// undo is the inverse of every write the transaction has made, guarded by
	// store.mu (exec appends to it with the store locked).
	undo []func()

	// What the context Rollback was given looked like when it arrived (guarded by
	// mu). A rollback that runs on the request's own context — spent, by the time
	// most rollbacks are needed — is a rollback that cannot happen.
	rollbackCalled   bool
	rollbackCtxErr   error
	rollbackHasLimit bool
	rollbackLeft     time.Duration
}

// rollbackContext reports what the context passed to Rollback was like when it
// arrived: whether Rollback was called at all, whether it had a deadline and how
// long that deadline had left, and its Err().
func (t *raceTx) rollbackContext() (called, hasDeadline bool, left time.Duration, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rollbackCalled, t.rollbackHasLimit, t.rollbackLeft, t.rollbackCtxErr
}

// undoWrites replays the transaction's undo log, newest first, and empties it.
func (t *raceTx) undoWrites() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for i := len(t.undo) - 1; i >= 0; i-- {
		t.undo[i]()
	}
	t.undo = nil
}

// keepWrites makes the transaction's writes permanent: it empties the undo log.
func (t *raceTx) keepWrites() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	t.undo = nil
}

// Like pgconn's, the transaction's statements refuse a context that has already
// ended, and wait for a row lock only as long as their context lasts.
func (t *raceTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	if err := t.store.waitLock(ctx, sqlName(sql)); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.store.exec(sql, args, true, &t.undo)
}

func (t *raceTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := t.store.waitLock(ctx, sqlName(sql)); err != nil {
		return nil, err
	}
	return t.store.query(sql, args, true)
}

func (t *raceTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := ctx.Err(); err != nil {
		return raceRow{err: err}
	}
	if err := t.store.waitLock(ctx, sqlName(sql)); err != nil {
		return raceRow{err: err}
	}
	return t.store.queryRow(sql, args, true)
}

// closeLocked is the one place the connection goes back: t.mu is held.
func (t *raceTx) closeLocked() {
	t.closed = true
	t.store.txClosed()
	t.gate.release()
}

func (t *raceTx) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.store.noteCommitAttempt()
	// pgx closes the transaction whether or not the commit succeeds, and a commit
	// whose context has ended — before it started or while it waited — fails. One
	// whose context had ended before it started was never sent, and pgconn says so
	// (neverSentError); one whose context ends while it waits was sent, and says only
	// that its context ended.
	if err := ctx.Err(); err != nil {
		t.closeLocked()
		t.undoWrites()
		return &neverSentError{err: err}
	}
	if err := t.store.waitLock(ctx, "Commit"); err != nil {
		t.closeLocked()
		t.undoWrites()
		return err
	}
	t.closeLocked()
	t.store.mu.Lock()
	err, lands := t.store.commitErr, t.store.commitLands
	t.store.mu.Unlock()
	if err != nil {
		if lands {
			t.keepWrites()
			t.committed = true
		} else {
			t.undoWrites()
		}
		return err
	}
	t.keepWrites()
	t.committed = true
	return nil
}

func (t *raceTx) Rollback(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.rollbackCalled = true
	t.rollbackCtxErr = ctx.Err()
	if at, ok := ctx.Deadline(); ok {
		t.rollbackHasLimit, t.rollbackLeft = true, time.Until(at)
	}
	t.rolledBack = true
	t.closeLocked()
	t.undoWrites()
	t.store.mu.Lock()
	err := t.store.rollbackErr
	t.store.mu.Unlock()
	return err
}

func (t *raceTx) state() (committed, rolledBack bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.committed, t.rolledBack
}

// raceBeginner is the pool: it hands the handler one raceTx per Begin, each
// holding a connection of the gate's pool.
type raceBeginner struct {
	store *raceStore
	gate  *raceGate

	// beforeBegin runs ahead of a connection being acquired, and afterBegin once one
	// has been, while the request holds it. Tests use them to line concurrent
	// requests up, and to take the pool's connections away.
	beforeBegin func()
	afterBegin  func()
	beginErr    error // if set, Begin fails with it, as a pool that cannot connect does

	mu       sync.Mutex
	txs      []*raceTx
	attempts int // calls to Begin, whether or not they got a connection
	// lastBeginDeadline is the deadline the context of the latest Begin carried.
	lastBeginDeadline time.Time
}

// beginDeadline reports the deadline the context of the latest Begin carried, zero
// if it carried none.
func (b *raceBeginner) beginDeadline() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastBeginDeadline
}

// beginAttempts is how many times a transaction was asked for.
func (b *raceBeginner) beginAttempts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts
}

func (b *raceBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	b.mu.Lock()
	b.attempts++
	b.lastBeginDeadline, _ = ctx.Deadline()
	b.mu.Unlock()
	if b.beforeBegin != nil {
		b.beforeBegin()
	}
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	if err := b.gate.acquire(ctx); err != nil {
		return nil, err
	}
	tx := &raceTx{store: b.store, gate: b.gate}
	b.store.txOpened()
	b.mu.Lock()
	b.txs = append(b.txs, tx)
	b.mu.Unlock()
	if b.afterBegin != nil {
		b.afterBegin()
	}
	return tx, nil
}

// txCount is how many transactions have been begun on this pool.
func (b *raceBeginner) txCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.txs)
}

func (b *raceBeginner) only(t *testing.T) *raceTx {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.txs) != 1 {
		t.Fatalf("handler began %d transactions, want exactly 1", len(b.txs))
	}
	return b.txs[0]
}

// The two kinds of database failure the handlers tell apart (see
// isTransientDBError). errRaceTransient is the database being unreachable: a
// connection reset under a read, as pgx reports it. errRaceBug is a failure that is
// not — a defect — and is the other answer.
// neverSentError is the shape pgconn gives a statement whose context had already
// ended when it was about to be sent: errTimeout wrapping contextAlreadyDoneError,
// which answers SafeToRetry AND unwraps to the context's own error — nothing went to
// the server. The stand-in returns it from a COMMIT in that position, and the commit
// classification tests use it for the same shape. internal/db has the real thing
// against Postgres (TestAnUnsentCommitIsSafeToRetryAndTheServerRollsBack), which is
// what keeps this double honest.
type neverSentError struct{ err error }

func (e *neverSentError) Error() string     { return "timeout: context already done: " + e.err.Error() }
func (e *neverSentError) SafeToRetry() bool { return true }
func (e *neverSentError) Unwrap() error     { return e.err }

// connClosedError is the shape pgx 5.10 gives a COMMIT whose connection closes while
// its reply is awaited: a connLockError "conn closed", which answers SafeToRetry
// although the COMMIT WAS sent and the server may have executed it, and carries no
// context error. It is what a proxy that lets Postgres commit and drops the reply
// produces, against the real driver, in
// TestALostCommitReplyLooksSafeToRetryAndHasLanded in internal/db.
type connClosedError struct{}

func (connClosedError) Error() string     { return "conn closed" }
func (connClosedError) SafeToRetry() bool { return true }
func (connClosedError) Unwrap() error     { return pgconn.ErrConnClosed }

var (
	errRaceTransient = fmt.Errorf("failed to receive message: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})
	errRaceBug       = errors.New("unexpected database failure")
)

const (
	// The session's current refresh token — what its cookie holds — and the one
	// it had before its last rotation.
	raceCurrentToken  = "current-refresh-token-value"
	racePreviousToken = "previous-refresh-token-value"
)

type authRaceApp struct {
	app     *fiber.App
	handler *AuthHandler
	store   *raceStore
	pool    *raceBeginner
	gate    *raceGate
	redis   *miniredis.Miniredis
	rdb     *redis.Client
	jwt     *auth.JWTService

	// afterMu guards afterCtx: what the request's context looked like to the
	// middleware that runs once a handler has returned, by path.
	afterMu  sync.Mutex
	afterCtx map[string]ctxAfterHandler
}

// ctxAfterHandler is the request context as seen once a handler has returned.
type ctxAfterHandler struct {
	err         error // its Err(): non-nil means a cancelled context was left behind
	hasDeadline bool  // it still carries the handler's bound
}

// raceOptions are the knobs a few tests turn; the zero value is the harness every
// other test uses.
type raceOptions struct {
	gateSize  int           // connections in the pool; 0 is a pool with no limit
	dbTimeout time.Duration // the handler's bound on its deciding database work; 0 is the production bound
	// followUpTimeout is the handler's bound on each follow-up of a decision (the
	// audit row, the revoke of a refused account's session, the race check, the
	// Redis cleanup); 0 is the production bound.
	followUpTimeout time.Duration
	concurrent      bool // requests overlap, so the single-request connection accounting does not apply
}

// newAuthRaceApp wires Refresh and Logout over a store holding one live admin
// session that rotated a minute ago. tweak edits the store before the handler
// sees it.
//
// Every test that uses it gets one check for free, at its end: that no request
// asked the pool for a connection while its own transaction still held one, and
// that no transaction was left open. See raceStore.openTx for why.
func newAuthRaceApp(t *testing.T, tweak func(*raceStore)) *authRaceApp {
	t.Helper()
	return newAuthRaceAppWith(t, tweak, raceOptions{})
}

func newAuthRaceAppWith(t *testing.T, tweak func(*raceStore), opts raceOptions) *authRaceApp {
	t.Helper()

	now := time.Now()
	userID := uuid.New()
	store := &raceStore{
		session: db.Session{
			ID:                uuid.New(),
			UserID:            userID,
			TokenHash:         auth.HashToken(raceCurrentToken),
			UserAgent:         "Mozilla/5.0",
			IpAddress:         "192.0.2.10",
			CreatedAt:         now.Add(-time.Hour),
			ExpiresAt:         now.Add(24 * time.Hour),
			LastUsedAt:        now.Add(-time.Minute),
			UserRole:          "admin",
			PreviousTokenHash: pgtype.Text{String: auth.HashToken(racePreviousToken), Valid: true},
			RotatedAt:         pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
		},
		user: db.User{
			ID:          userID,
			Email:       "admin@example.com",
			DisplayName: "Admin",
			IsActive:    true,
			Role:        "admin",
			AuthSource:  "local",
		},
		permissions: []db.GetUserPermissionsRow{
			{Action: "view", Resource: "cluster"},
			{Action: "manage", Resource: "node"},
		},
	}
	if tweak != nil {
		tweak(store)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	var gate *raceGate
	if opts.gateSize > 0 {
		gate = newRaceGate(opts.gateSize)
	}
	queries := db.New(raceConn{store: store, gate: gate})
	pool := &raceBeginner{store: store, gate: gate}
	jwtSvc := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)
	handler := &AuthHandler{
		pool:            pool,
		queries:         queries,
		jwtService:      jwtSvc,
		sessionManager:  auth.NewSessionManager(queries, rdb),
		rbac:            auth.NewRBACEngine(queries, rdb),
		dbTimeout:       opts.dbTimeout,
		followUpTimeout: opts.followUpTimeout,
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			message := "Internal Server Error"
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
				message = e.Message
			}
			return c.Status(code).JSON(fiber.Map{"error": code, "message": message})
		},
	})
	a := &authRaceApp{handler: handler, store: store, pool: pool, gate: gate, redis: mr, rdb: rdb, jwt: jwtSvc,
		afterCtx: map[string]ctxAfterHandler{}}
	// observe records the request's context once the handler has returned. A
	// handler that installs a bounded context for its helpers must put the old one
	// back: whatever runs after it reads c.Context() too.
	observe := func(c fiber.Ctx) error {
		err := c.Next()
		_, hasDeadline := c.Context().Deadline()
		a.afterMu.Lock()
		a.afterCtx[c.Path()] = ctxAfterHandler{err: c.Context().Err(), hasDeadline: hasDeadline}
		a.afterMu.Unlock()
		return err
	}
	app.Post("/auth/refresh", observe, withRequestParams(t, authRefreshMirror(), nil, handler.Refresh))
	// Logout is mounted with authOptional in production, which sets user_id when
	// a valid access token is presented. The header stands in for that, and for
	// authRequired on the self-service routes below.
	actingUser := func(c fiber.Ctx) error {
		if v := c.Get("X-Test-Acting-User"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "bad X-Test-Acting-User")
			}
			c.Locals("user_id", id)
		}
		return c.Next()
	}
	app.Post("/auth/logout", observe, actingUser, handler.Logout)
	app.Post("/auth/logout-all", observe, actingUser, withRequestParams(t, apischema.Properties{}, nil, handler.LogoutAll))
	app.Post("/auth/change-password", observe, actingUser, withRequestParams(t, apischema.Properties{
		"old_password": {Type: apischema.String},
		"new_password": {Type: apischema.String},
	}, nil, handler.ChangePassword))

	a.app = app
	if !opts.concurrent {
		t.Cleanup(func() { checkConnectionAccounting(t, store) })
	}
	return a
}

// checkConnectionAccounting is the check every harness user inherits at the end of
// its test: no statement went to the pool while a transaction was open, and no
// transaction was left open. It takes a testing.TB so that its own test can hand
// it a recorder and show that it fails when it should.
func checkConnectionAccounting(t testing.TB, store *raceStore) {
	t.Helper()
	if got := store.poolStatementsWhileTx(); len(got) != 0 {
		t.Errorf("the handler queried the POOL (%v) while its own transaction still held a connection: with the pool "+
			"at its limit that is a deadlock — every connection held by a request waiting for another", got)
	}
	if n := store.openTransactions(); n != 0 {
		t.Errorf("%d transactions were left open: their connections never went back to the pool", n)
	}
}

// post sends a POST carrying cookieToken as the refresh cookie ("" sends none).
func (a *authRaceApp) post(t *testing.T, path, cookieToken string, headers map[string]string) *http.Response {
	t.Helper()
	return a.postBody(t, path, "{}", cookieToken, headers)
}

// postBody is post with a JSON body of the caller's choosing.
func (a *authRaceApp) postBody(t *testing.T, path, body, cookieToken string, headers map[string]string) *http.Response {
	t.Helper()
	resp, _ := a.postTimed(t, path, body, cookieToken, headers, time.Second)
	// A winning refresh writes its Redis row from a goroutine of its own, after the
	// response. Waiting for it here keeps that write from outliving the test that
	// caused it, and is the check that it still happens.
	if path == "/auth/refresh" && resp.StatusCode == http.StatusOK {
		a.awaitSessionRedisRow(t)
	}
	return resp
}

// postTimed sends a POST and reports how long the answer took. A request that
// does not answer within timeout fails the test: a hang is a failure here, never
// a wait.
func (a *authRaceApp) postTimed(t *testing.T, path, body, cookieToken string, headers map[string]string, timeout time.Duration) (*http.Response, time.Duration) {
	t.Helper()
	req := raceRequest(path, body, cookieToken, headers)
	start := time.Now()
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: timeout, FailOnTimeout: true})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("request failed after %v (a hang, if that is the timeout): %v", elapsed, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, elapsed
}

func raceRequest(path, body, cookieToken string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if cookieToken != "" {
		req.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: cookieToken})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// awaitSessionRedisRow waits for the winner's Redis row to appear.
func (a *authRaceApp) awaitSessionRedisRow(t *testing.T) {
	t.Helper()
	key := "nexara:session:" + a.store.snapshot().ID.String()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a.redis.Exists(key) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the winner's Redis row %s never appeared: the write happens after the response, but it must happen", key)
}

func refreshCookies(resp *http.Response) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == RefreshCookieName {
			out = append(out, c)
		}
	}
	return out
}

// cookieDeleted reports whether a Set-Cookie tells the browser to drop the
// cookie: an empty value that is already expired or has a non-positive Max-Age.
func cookieDeleted(c *http.Cookie) bool {
	expired := c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now()))
	return c.Value == "" && expired
}

func decodeObject(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body is not a JSON object: %v: %s", err, raw)
	}
	return out
}

// raceWinner is the refresh token another request got to rotate to first.
const raceWinner = "the-winning-refreshs-token"

// raceRotatedAgo sets the session's last rotation d ago.
func raceRotatedAgo(d time.Duration) func(*raceStore) {
	return func(s *raceStore) {
		s.session.RotatedAt = pgtype.Timestamptz{Time: time.Now().Add(-d), Valid: true}
	}
}

// raceWinnerRotatesFirst is another refresh presenting this same cookie getting
// to the row first, in the gap between this refresh's validation and its
// rotation.
func raceWinnerRotatesFirst(s *raceStore) {
	s.beforeRotate = func(s *raceStore) {
		s.session.PreviousTokenHash = pgtype.Text{String: s.session.TokenHash, Valid: true}
		s.session.RotatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		s.session.TokenHash = auth.HashToken(raceWinner)
	}
}

type cookieOutcome int

const (
	cookieNew       cookieOutcome = iota // Set-Cookie carries a fresh refresh token
	cookieCleared                        // Set-Cookie deletes the cookie
	cookieUntouched                      // no Set-Cookie for it at all
)

// TestRefresh_RotationOutcomes drives Refresh through the writers that can land
// between its validation and its rotation, and through every shape of stale
// token, and pins what each one costs it.
//
// The first row is the positive control for every refusal below it: on the same
// harness, with nothing interfering, the refresh succeeds, mints an access token,
// sets a new cookie and writes the Redis row. The refusal rows assert the
// ABSENCE of exactly those things, and an absence proves nothing unless the
// presence is shown to be possible.
//
// How a refusal is ANSWERED is the second thing pinned. It is 401 with the cookie
// cleared, unless the token is the one a LIVE session replaced within
// auth.ConcurrentRefreshTolerance — the loser of a race to another refresh of the
// same session — which is 409 refresh_superseded with the cookie LEFT ALONE: the
// browser's jar may already hold the winner's newer cookie, and a Set-Cookie
// deletion cannot be made conditional on what it holds. Rows come in pairs around
// each edge of that: inside the tolerance and outside it, a live session and a
// revoked or expired one, a token one rotation old and one older, and the loser
// refused at validation (the winner had already committed) and at the rotation
// (it had not).
//
// The third thing is the lookup that COULD NOT BE MADE — the session's or the
// user's. It is neither a stale token nor a missing user: 503, with the cookie
// and the session left exactly as they were. Answered as a refusal it would clear
// a good cookie and sign the user out over a database blip.
//
// Each interference is the state a real writer leaves behind: Logout, LogoutAll,
// DELETE /auth/sessions/:id, a password change and an admin deactivation all end
// in is_revoked (the SQL-level tests drive each of them against Postgres); a
// second refresh presenting the same cookie leaves a different current hash and
// the old one as the previous; an expiry and a cascade delete are what they say.
//
// Refresh asks "was this refusal a race?" through SessionManager.RefusalSparesCookie
// and nothing else may use the previous-token lookup to decide anything about a
// refresh; every row says whether that question was asked, and what it was
// asked with.
func TestRefresh_RotationOutcomes(t *testing.T) {
	boom := errRaceTransient // a check or a lookup that could not be made
	bug := errRaceBug        // a write that fails for a reason that is not the database being away
	tolerance := auth.ConcurrentRefreshTolerance
	winner := raceWinner

	rotatedAgo := raceRotatedAgo
	winnerRotatesFirst := raceWinnerRotatesFirst
	both := func(fns ...func(*raceStore)) func(*raceStore) {
		return func(s *raceStore) {
			for _, f := range fns {
				f(s)
			}
		}
	}
	revoked := func(s *raceStore) { s.session.IsRevoked = true }
	expired := func(s *raceStore) { s.session.ExpiresAt = time.Now().Add(-time.Hour) }

	tests := []struct {
		name  string
		tweak func(*raceStore)
		// cookie is the refresh token the request carries; "" means the
		// session's current one.
		cookie string

		wantStatus int
		// wantSlug is the envelope's error code, for the answers the handler
		// writes itself rather than through errorHandler.
		wantSlug   string
		wantCookie cookieOutcome
		// wantStored is the hash the session holds afterwards. "" means the hash
		// of the cookie the response set.
		wantStored string

		wantNoTx      bool     // validation refused it, so no transaction was ever begun
		wantRotation  bool     // RotateSessionToken was sent, on the transaction
		wantCommitted bool     // the transaction committed (otherwise it rolled back)
		wantRevoke    bool     // the handler revoked the session itself
		wantAudits    []string // audit actions written
		wantAsked     bool     // the refusal asked whether the token was superseded
	}{
		{
			name:       "nothing interferes",
			wantStatus: http.StatusOK, wantCookie: cookieNew, wantStored: "",
			wantRotation: true, wantCommitted: true,
		},

		{
			// A session issued before migration 000055 has no role recorded, and the
			// role-rotation guard accepts that once. The rotation must then WRITE
			// the user's role, or the session keeps passing the guard for ever. Every
			// other row has the session's role equal to the user's, which cannot tell
			// a rotation that writes the role from one that does not.
			name:       "a session issued before its role was recorded refreshes, and records it",
			tweak:      func(s *raceStore) { s.session.UserRole = "" },
			wantStatus: http.StatusOK, wantCookie: cookieNew, wantStored: "",
			wantRotation: true, wantCommitted: true,
		},

		// The writers that land between validation and rotation: refused at the
		// rotation, with the transaction open.
		{
			name:       "the session is revoked after the refresh validated",
			tweak:      func(s *raceStore) { s.beforeRotate = func(s *raceStore) { s.session.IsRevoked = true } },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRotation: true, wantAsked: true,
		},
		{
			name:       "another refresh with the same cookie rotates first",
			tweak:      winnerRotatesFirst,
			wantStatus: http.StatusConflict, wantSlug: "refresh_superseded", wantCookie: cookieUntouched, wantStored: auth.HashToken(winner),
			wantRotation: true, wantAsked: true,
		},
		{
			name: "another refresh rotates first and the session is then revoked, as by a sign-out",
			tweak: both(winnerRotatesFirst, func(s *raceStore) {
				inner := s.beforeRotate
				s.beforeRotate = func(s *raceStore) { inner(s); s.session.IsRevoked = true }
			}),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(winner),
			wantRotation: true, wantAsked: true,
		},
		{
			name:       "another refresh rotates first and the check that it did cannot be made",
			tweak:      both(winnerRotatesFirst, func(s *raceStore) { s.previousErr = boom }),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(winner),
			wantRotation: true, wantAsked: true,
		},
		{
			name: "the session expires after the refresh validated",
			tweak: func(s *raceStore) {
				s.beforeRotate = func(s *raceStore) { s.session.ExpiresAt = time.Now().Add(-time.Second) }
			},
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRotation: true, wantAsked: true,
		},
		{
			name:       "the session is deleted after the refresh validated",
			tweak:      func(s *raceStore) { s.beforeRotate = func(s *raceStore) { s.deleted = true } },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRotation: true, wantAsked: true,
		},
		{
			name:       "the database fails rotating: a failure, not a refusal",
			tweak:      func(s *raceStore) { s.rotateErr = bug },
			wantStatus: http.StatusInternalServerError, wantCookie: cookieUntouched, wantStored: auth.HashToken(raceCurrentToken),
			wantRotation: true,
		},

		// Refused at validation: the token is not the session's current one.
		{
			// The loser whose request reached the server after the winner had
			// committed — it carried the old cookie because the winner's response
			// had not reached the browser yet.
			name: "the token was replaced a moment ago by another refresh", cookie: racePreviousToken,
			tweak:      rotatedAgo(2 * time.Second),
			wantStatus: http.StatusConflict, wantSlug: "refresh_superseded", wantCookie: cookieUntouched, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "the token was replaced just inside the tolerance", cookie: racePreviousToken,
			tweak:      rotatedAgo(tolerance - 3*time.Second),
			wantStatus: http.StatusConflict, wantSlug: "refresh_superseded", wantCookie: cookieUntouched, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "the token was replaced just outside the tolerance", cookie: racePreviousToken,
			tweak:      rotatedAgo(tolerance + 5*time.Second),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			// The grace window belongs to sign-out alone, and a token this old is
			// nowhere near a race. The store DOES hold it as the session's previous
			// token, inside the sign-out window — the shape in which a refresh that
			// honoured it would hand out a second working cookie.
			name: "the token was replaced a minute ago", cookie: racePreviousToken,
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "the replaced token's session was revoked, as by a sign-out", cookie: racePreviousToken,
			tweak:      both(rotatedAgo(2*time.Second), revoked),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "the replaced token's session has expired", cookie: racePreviousToken,
			tweak:      both(rotatedAgo(2*time.Second), expired),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "a token older than the one the session replaced", cookie: "an-older-refresh-token",
			tweak:      rotatedAgo(2 * time.Second),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name: "the token was replaced a moment ago and the check that it was cannot be made", cookie: racePreviousToken,
			tweak:      both(rotatedAgo(2*time.Second), func(s *raceStore) { s.previousErr = boom }),
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},
		{
			name:       "the session was already revoked when the refresh arrived",
			tweak:      revoked,
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true, wantAsked: true,
		},

		// A lookup that could not be made says nothing about the token.
		{
			name:       "the session lookup fails",
			tweak:      func(s *raceStore) { s.currentErr = boom },
			wantStatus: http.StatusServiceUnavailable, wantCookie: cookieUntouched, wantStored: auth.HashToken(raceCurrentToken),
			wantNoTx: true,
		},
		{
			name:       "the user lookup fails",
			tweak:      func(s *raceStore) { s.userErr = boom },
			wantStatus: http.StatusServiceUnavailable, wantCookie: cookieUntouched, wantStored: auth.HashToken(raceCurrentToken),
		},

		// What the transaction's guards did before, and still do.
		{
			name:       "the user's role changed since the session was issued",
			tweak:      func(s *raceStore) { s.user.Role = "viewer" },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRevoke: true, wantAudits: []string{"refresh_denied_role_changed"},
		},
		{
			name:       "the account is disabled",
			tweak:      func(s *raceStore) { s.user.IsActive = false },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRevoke: true,
		},
		{
			name:       "the user no longer exists",
			tweak:      func(s *raceStore) { s.userErr = pgx.ErrNoRows },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantStored: auth.HashToken(raceCurrentToken),
			wantRevoke: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tt.tweak)
			presented := auth.HashToken(raceCurrentToken)
			cookie := tt.cookie
			if cookie == "" {
				cookie = raceCurrentToken
			}
			resp := a.post(t, "/auth/refresh", cookie, nil)
			body := decodeObject(t, resp)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, tt.wantStatus, body)
			}
			if tt.wantSlug != "" {
				if body["error"] != tt.wantSlug {
					t.Errorf("error code = %v, want %q: a client retries on exactly this status and code (body %v)", body["error"], tt.wantSlug, body)
				}
				if msg, _ := body["message"].(string); msg == "" {
					t.Errorf("the %s answer carries no message: %v", tt.wantSlug, body)
				}
			}

			// The cookie.
			cookies := refreshCookies(resp)
			var newToken string
			switch tt.wantCookie {
			case cookieNew:
				if len(cookies) != 1 || cookies[0].Value == "" || cookieDeleted(cookies[0]) {
					t.Fatalf("Set-Cookie = %+v, want one fresh refresh cookie", cookies)
				}
				newToken = cookies[0].Value
				if newToken == raceCurrentToken {
					t.Error("the refresh set back the cookie it was given instead of rotating it")
				}
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
				}
			case cookieUntouched:
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the refresh cookie left alone", cookies)
				}
			}

			// What came back: a token pair if and only if the refresh won.
			_, hasAccess := body["access_token"]
			if won := tt.wantStatus == http.StatusOK; hasAccess != won {
				t.Errorf("response carries access_token = %t, want %t: %v", hasAccess, won, body)
			}
			if tt.wantStatus == http.StatusOK {
				claims, err := a.jwt.ValidateAccessToken(body["access_token"].(string))
				if err != nil {
					t.Fatalf("the access token does not validate: %v", err)
				}
				if claims.UserID != a.store.user.ID {
					t.Errorf("access token is for %v, want %v", claims.UserID, a.store.user.ID)
				}
				if rt, _ := body["refresh_token"].(string); rt != "" {
					t.Errorf("refresh_token = %q in the body; it travels only in the cookie", rt)
				}
			}

			// What the session holds afterwards.
			stored := a.store.snapshot()
			wantStored := tt.wantStored
			if wantStored == "" {
				wantStored = auth.HashToken(newToken)
				if stored.PreviousTokenHash.String != presented {
					t.Errorf("previous_token_hash = %q, want the hash that was presented, %q", stored.PreviousTokenHash.String, presented)
				}
			}
			if stored.TokenHash != wantStored {
				t.Errorf("stored token hash = %q, want %q", stored.TokenHash, wantStored)
			}
			if tt.wantStatus == http.StatusOK && stored.UserRole != a.store.user.Role {
				t.Errorf("stored user_role = %q after a winning refresh, want the user's role %q: the rotation is what "+
					"fills in the role of a session that predates it", stored.UserRole, a.store.user.Role)
			}

			// The Redis row is written for a winner and for nobody else.
			key := "nexara:session:" + stored.ID.String()
			if tt.wantStatus == http.StatusOK {
				raw, err := a.redis.Get(key)
				if err != nil {
					t.Fatalf("no Redis row for the rotated session: %v", err)
				}
				if !strings.Contains(raw, auth.HashToken(newToken)) {
					t.Errorf("Redis row %s does not carry the new hash", raw)
				}
			} else if keys := a.redis.Keys(); len(keys) != 0 {
				t.Errorf("a refresh that issued nothing wrote Redis rows %v", keys)
			}

			// The statements.
			rotations := a.store.named("RotateSessionToken")
			if got := len(rotations) > 0; got != tt.wantRotation {
				t.Errorf("rotation sent = %t, want %t", got, tt.wantRotation)
			}
			for _, r := range rotations {
				if !r.inTx {
					t.Error("the rotation ran outside the refresh's transaction")
				}
			}
			revokes := a.store.named("RevokeSession")
			if got := len(revokes) > 0; got != tt.wantRevoke {
				t.Errorf("handler revoked the session = %t, want %t", got, tt.wantRevoke)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, tt.wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, tt.wantAudits)
			}

			// Whether the refusal asked "was this a race?", and what it asked with:
			// the hash of the token and the TOLERANCE, never the sign-out window. A
			// refresh that is not refused never asks, and none of the answers it
			// gets issues anything — the rows above hold every one of them to that.
			asked := a.store.named("GetSessionByPreviousTokenHash")
			if got := len(asked) > 0; got != tt.wantAsked {
				t.Errorf("asked whether the token was superseded = %t, want %t", got, tt.wantAsked)
			}
			if len(asked) == 1 {
				want := []any{auth.HashToken(cookie), tolerance.Seconds()}
				if !reflect.DeepEqual(asked[0].args, want) {
					t.Errorf("superseded check args = %v, want %v (the hash, and the tolerance in seconds)", asked[0].args, want)
				}
			}
			if len(asked) > 1 {
				t.Errorf("asked whether the token was superseded %d times, want at most once", len(asked))
			}

			if tt.wantNoTx {
				a.pool.mu.Lock()
				began := len(a.pool.txs)
				a.pool.mu.Unlock()
				if began != 0 {
					t.Errorf("a refresh that validation refused began %d transactions, want 0", began)
				}
				return
			}
			committed, rolledBack := a.pool.only(t).state()
			if committed != tt.wantCommitted {
				t.Errorf("transaction committed = %t, want %t", committed, tt.wantCommitted)
			}
			if !committed && !rolledBack {
				t.Error("the transaction was left open: neither committed nor rolled back")
			}
		})
	}
}

// TestRefresh_ALookupThatCouldNotBeMadeIsLogged pins the other half of the 503: it
// is logged, because a refresh that answered 503 and changed nothing is otherwise
// invisible — and the log line carries neither the token nor its hash. It carries
// the session id wherever the session is known, because that is what lets an
// operator find the session that was being refreshed when the database stopped
// answering; the one lookup that fails before the session is known is the one
// line without it. The control is the first loop's own: every read is made
// through the same harness that answers 200.
func TestRefresh_ALookupThatCouldNotBeMadeIsLogged(t *testing.T) {
	boom := errRaceTransient
	for _, tc := range []struct {
		name          string
		tweak         func(*raceStore)
		begin         error
		want          string
		wantSessionID bool
	}{
		{"the session lookup", func(s *raceStore) { s.currentErr = boom }, nil, "refresh: session lookup failed", false},
		{"the permission lookup", func(s *raceStore) { s.permsErr = boom }, nil, "refresh: permission lookup failed", true},
		{"the transaction start", nil, boom, "refresh: transaction start failed", true},
		{"the user lookup", func(s *raceStore) { s.userErr = boom }, nil, "refresh: user lookup failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tc.tweak)
			a.pool.beginErr = tc.begin

			resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)

			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", resp.StatusCode)
			}
			out := logs.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("the failed lookup left no trace in the log: %q", out)
			}
			if got := strings.Contains(out, a.store.session.ID.String()); got != tc.wantSessionID {
				t.Errorf("the log line names the session = %t, want %t: %q", got, tc.wantSessionID, out)
			}
			if strings.Contains(out, raceCurrentToken) || strings.Contains(out, auth.HashToken(raceCurrentToken)) {
				t.Errorf("the log line carries the token or its hash: %q", out)
			}
		})
	}

	// Control: a refresh that wins logs none of these lines.
	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	if resp := b.post(t, "/auth/refresh", raceCurrentToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("control status = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(quiet.String(), "lookup failed") || strings.Contains(quiet.String(), "transaction start failed") {
		t.Errorf("a healthy refresh logged a failed lookup: %q", quiet.String())
	}
}

// TestRefresh_ALostRaceIsLogged pins what an operator can see of a refresh that
// lost: a refresh refused at the rotation says so, with the session id, and one
// whose token a concurrent refresh replaced says that too. The two lines together
// are what tell "lost to another refresh" from "refused because the session was
// revoked" — which look the same from outside, as a failed refresh. Ordinary
// stale tokens log nothing; they are common.
//
// Info, not Debug, because a lost race is rare and benign and is exactly the line
// worth having when someone asks why a refresh failed, without enabling debug
// logging first; and no row logs a token or a hash.
func TestRefresh_ALostRaceIsLogged(t *testing.T) {
	const lost = "refresh: lost the rotation"
	const superseded = "was replaced by a concurrent refresh moments ago"

	tests := []struct {
		name           string
		cookie         string
		tweak          func(*raceStore)
		wantLost       bool
		wantSuperseded bool
	}{
		{name: "another refresh rotates first: lost the rotation, and superseded", tweak: raceWinnerRotatesFirst, wantLost: true, wantSuperseded: true},
		{
			name:     "the session is revoked after validation: lost the rotation, not superseded",
			tweak:    func(s *raceStore) { s.beforeRotate = func(s *raceStore) { s.session.IsRevoked = true } },
			wantLost: true,
		},
		{
			name: "refused at validation, the token replaced a moment ago: superseded only", cookie: racePreviousToken,
			tweak: raceRotatedAgo(2 * time.Second), wantSuperseded: true,
		},
		{name: "an ordinary stale token: neither", cookie: "an-older-refresh-token"},
		{name: "a refresh that wins: neither"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tt.tweak)
			cookie := tt.cookie
			if cookie == "" {
				cookie = raceCurrentToken
			}

			a.post(t, "/auth/refresh", cookie, nil)

			out := logs.String()
			if got := strings.Contains(out, lost); got != tt.wantLost {
				t.Errorf("logged a lost rotation = %t, want %t: %q", got, tt.wantLost, out)
			}
			if got := strings.Contains(out, superseded); got != tt.wantSuperseded {
				t.Errorf("logged a superseded refusal = %t, want %t: %q", got, tt.wantSuperseded, out)
			}
			if (tt.wantLost || tt.wantSuperseded) && !strings.Contains(out, a.store.session.ID.String()) {
				t.Errorf("the log line does not name the session: %q", out)
			}
			for _, secret := range []string{
				raceCurrentToken, racePreviousToken, raceWinner, cookie,
				auth.HashToken(raceCurrentToken), auth.HashToken(racePreviousToken), auth.HashToken(raceWinner),
			} {
				if strings.Contains(out, secret) {
					t.Errorf("the log carries a token or a hash (%q): %q", secret, out)
				}
			}
		})
	}
}

// TestNewAuthHandler_AMissingPoolStaysMissing pins the one subtle line the
// transaction seam needed. The pool field is an interface so a test can stand in
// for it, and NewAuthHandler still takes a *pgxpool.Pool: stored unconditionally,
// a nil pointer there becomes a NON-nil interface holding a nil pointer, which
// makes Register's `h.pool == nil` guard — the "registration unavailable" answer
// the no-database unit tests rely on — false, and the first Begin dereferences
// nil instead. The second half is the control: a real pool is kept, so a
// constructor that simply never assigned would not pass the first half alone.
func TestNewAuthHandler_AMissingPoolStaysMissing(t *testing.T) {
	var none *pgxpool.Pool
	if h := NewAuthHandler(none, nil, nil, nil, nil, nil); h.pool != nil {
		t.Errorf("a nil *pgxpool.Pool became %T in the handler's pool field; it must stay a nil interface so `h.pool == nil` still fires", h.pool)
	}

	// Never used, never dialled: only its identity is compared.
	real := new(pgxpool.Pool)
	if h := NewAuthHandler(real, nil, nil, nil, nil, nil); h.pool != txBeginner(real) {
		t.Errorf("the handler's pool field = %v, want the pool it was given", h.pool)
	}
}

// TestRefresh_ARefusalCarriesNoTokenUnderAnyName covers the sharpest form of the
// original bug on the wire: not just "the status was wrong" but "the body held a
// usable access token". TestRefresh_RotationOutcomes checks the access_token
// field by name; this checks the whole body by VALUE, so a token that came back
// under a different field would still fail. The positive control is the same
// harness answering a refresh that wins, whose body does contain one.
func TestRefresh_ARefusalCarriesNoTokenUnderAnyName(t *testing.T) {
	bodyOf := func(a *authRaceApp) (int, string) {
		t.Helper()
		resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return resp.StatusCode, string(raw)
	}
	// "eyJ" is how every JWT here begins: base64 of `{"`, the start of its header.
	const jwtPrefix = "eyJ"

	status, body := bodyOf(newAuthRaceApp(t, nil))
	if status != http.StatusOK || !strings.Contains(body, jwtPrefix) {
		t.Fatalf("control: a winning refresh answered %d with %q; the harness cannot show a token in a body, so this test proves nothing", status, body)
	}

	status, body = bodyOf(newAuthRaceApp(t, func(s *raceStore) {
		s.beforeRotate = func(s *raceStore) { s.session.IsRevoked = true }
	}))
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", status, body)
	}
	if strings.Contains(body, jwtPrefix) {
		t.Errorf("the refusal body carries what looks like a JWT: %s", body)
	}
	if strings.Contains(body, "permissions") || strings.Contains(body, "expires_at") {
		t.Errorf("the refusal body is shaped like a success: %s", body)
	}
}

// TestLogout_FindsTheSessionByEitherToken pins which cookies end a session, and
// that everything around the lookup — the ownership check, the audit row, the
// cookie, the idempotent 200 — behaves the same for a match on the previous
// token as for a match on the current one, with one difference by design: the
// audit row of a sign-out that matched the PREVIOUS token says so, and says only
// that (audit details are readable by every Viewer).
//
// The rows that revoke are the positive controls for the rows that do not: a
// sign-out that revoked nothing looks exactly like one that was never wired up,
// so the "nothing revoked" claims only count beside rows that show the same
// harness revoking.
//
// The 503 rows are the third answer: a lookup that could not be made — the
// current-token lookup or the previous-token one — is not "no such session". It
// revokes nothing and says so, with the cookie cleared like every answer but the
// 403: a session that is someone else's leaves their cookie where it is
// (auth_logout_owner_test.go has the rest of that rule).
func TestLogout_FindsTheSessionByEitherToken(t *testing.T) {
	window := auth.PreviousTokenRevocationWindow
	other := uuid.New()
	boom := errRaceTransient
	const marked = `{"matched":"previous_token"}`

	rotatedAgo := func(d time.Duration) func(*raceStore) {
		return func(s *raceStore) {
			s.session.RotatedAt = pgtype.Timestamptz{Time: time.Now().Add(-d), Valid: true}
		}
	}

	tests := []struct {
		name    string
		tweak   func(*raceStore)
		token   string
		actor   string // "" = no access token; "owner"; "other"
		wantRev bool
		want    int
		// wantPreviousLookup is whether the previous-token lookup should run.
		wantPreviousLookup bool
		wantAudits         []string
		// wantDetails is the details of each audit row, in order.
		wantDetails []string
	}{
		{
			name: "the current token", token: raceCurrentToken,
			wantRev: true, want: http.StatusOK, wantAudits: []string{"logout"}, wantDetails: []string{"{}"},
		},
		{
			name: "the previous token, rotated well inside the window", token: racePreviousToken,
			tweak:   rotatedAgo(window / 4),
			wantRev: true, want: http.StatusOK, wantPreviousLookup: true, wantAudits: []string{"logout"}, wantDetails: []string{marked},
		},
		{
			name: "the previous token, rotated just inside the window", token: racePreviousToken,
			tweak:   rotatedAgo(window - 10*time.Second),
			wantRev: true, want: http.StatusOK, wantPreviousLookup: true, wantAudits: []string{"logout"}, wantDetails: []string{marked},
		},
		{
			name: "the previous token, rotated just outside the window", token: racePreviousToken,
			tweak:   rotatedAgo(window + 10*time.Second),
			wantRev: false, want: http.StatusOK, wantPreviousLookup: true,
		},
		{
			name: "the previous token of a session that is already revoked", token: racePreviousToken,
			tweak:   func(s *raceStore) { s.session.IsRevoked = true },
			wantRev: false, want: http.StatusOK, wantPreviousLookup: true,
		},
		{
			name: "a token that belongs to no session", token: "a-token-nobody-issued",
			wantRev: false, want: http.StatusOK, wantPreviousLookup: true,
		},
		{
			name: "no cookie at all", token: "",
			wantRev: false, want: http.StatusOK,
		},
		{
			name: "the current token, access token of the owner", token: raceCurrentToken, actor: "owner",
			wantRev: true, want: http.StatusOK, wantAudits: []string{"logout"}, wantDetails: []string{"{}"},
		},
		{
			name: "the current token, access token of someone else", token: raceCurrentToken, actor: "other",
			wantRev: false, want: http.StatusForbidden,
		},
		{
			name: "the previous token, access token of the owner", token: racePreviousToken, actor: "owner",
			wantRev: true, want: http.StatusOK, wantPreviousLookup: true, wantAudits: []string{"logout"}, wantDetails: []string{marked},
		},
		{
			name: "the previous token, access token of someone else", token: racePreviousToken, actor: "other",
			wantRev: false, want: http.StatusForbidden, wantPreviousLookup: true,
		},
		{
			// The failure a first lookup that fell through to the second would have
			// hidden: the current-token lookup fails, and the previous-token one
			// would have found the session. 503, nothing revoked, and the second
			// lookup never sent.
			name: "the current-token lookup fails", token: racePreviousToken,
			tweak:   func(s *raceStore) { s.currentErr = boom },
			wantRev: false, want: http.StatusServiceUnavailable,
		},
		{
			name: "the previous-token lookup fails", token: racePreviousToken,
			tweak:   func(s *raceStore) { s.previousErr = boom },
			wantRev: false, want: http.StatusServiceUnavailable, wantPreviousLookup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tt.tweak)
			headers := map[string]string{}
			switch tt.actor {
			case "owner":
				headers["X-Test-Acting-User"] = a.store.user.ID.String()
			case "other":
				headers["X-Test-Acting-User"] = other.String()
			}

			resp := a.post(t, "/auth/logout", tt.token, headers)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == http.StatusServiceUnavailable {
				checkLogoutUnconfirmed(t, decodeObject(t, resp))
			}
			// Cleared whatever happened, the 503 included, except on the 403: that
			// session, and the cookie that named it, are someone else's.
			cookies := refreshCookies(resp)
			if tt.want == http.StatusForbidden {
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone: the session is someone else's", cookies)
				}
			} else if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
			}

			revokes := a.store.named("RevokeSession")
			if got := len(revokes) > 0; got != tt.wantRev {
				t.Fatalf("session revoked = %t, want %t", got, tt.wantRev)
			}
			if tt.wantRev && revokes[0].args[0] != a.store.session.ID {
				t.Errorf("revoked session %v, want %v", revokes[0].args[0], a.store.session.ID)
			}
			if got := a.store.snapshot().IsRevoked; tt.wantRev && !got {
				t.Error("the session is still live after a sign-out that revoked it")
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, tt.wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, tt.wantAudits)
			}
			if got := a.store.auditDetailsWritten(); !reflect.DeepEqual(got, tt.wantDetails) {
				t.Errorf("audit details = %v, want %v", got, tt.wantDetails)
			}

			prev := a.store.named("GetSessionByPreviousTokenHash")
			if got := len(prev) > 0; got != tt.wantPreviousLookup {
				t.Errorf("previous-token lookup sent = %t, want %t", got, tt.wantPreviousLookup)
			}
			if len(prev) == 1 {
				want := []any{auth.HashToken(tt.token), window.Seconds()}
				if !reflect.DeepEqual(prev[0].args, want) {
					t.Errorf("previous-token lookup args = %v, want %v (the hash, and the named window in seconds)", prev[0].args, want)
				}
			}
			if tt.token == "" {
				if n := len(a.store.stmts); n != 0 {
					t.Errorf("a sign-out with no cookie sent %d statements, want none", n)
				}
			}
		})
	}
}

// TestLogout_ALookupThatCouldNotBeMadeIsAnErrorAndIsLogged pins the third outcome
// of the sign-out lookup: neither "found" nor "no such session" but "could not
// look". It answers 503 with the cookie cleared and nothing revoked — a 200 would
// tell a signed-in user they are signed out when nothing was checked — and it is
// logged, because that is the one outcome nobody could otherwise find after the
// fact. Both lookups are covered: the current token's, and the previous token's.
func TestLogout_ALookupThatCouldNotBeMadeIsAnErrorAndIsLogged(t *testing.T) {
	boom := errRaceTransient
	for _, tc := range []struct {
		name  string
		tweak func(*raceStore)
	}{
		{"the current-token lookup", func(s *raceStore) { s.currentErr = boom }},
		{"the previous-token lookup", func(s *raceStore) { s.previousErr = boom }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tc.tweak)

			resp := a.post(t, "/auth/logout", racePreviousToken, nil)

			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", resp.StatusCode)
			}
			checkLogoutUnconfirmed(t, decodeObject(t, resp))
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
			}
			if n := len(a.store.named("RevokeSession")); n != 0 {
				t.Errorf("RevokeSession sent %d times for a lookup that failed", n)
			}
			if out := logs.String(); !strings.Contains(out, "logout: session lookup failed") {
				t.Errorf("the failed lookup left no trace in the log: %q", out)
			}
			if out := logs.String(); strings.Contains(out, racePreviousToken) || strings.Contains(out, auth.HashToken(racePreviousToken)) {
				t.Errorf("the log line carries the token or its hash: %q", out)
			}
		})
	}

	// Control: a lookup that finds nothing is NOT logged as a failure and answers
	// 200, so the lines above mean a lookup failed rather than that sign-out ran.
	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	if resp := b.post(t, "/auth/logout", "a-token-nobody-issued", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("control status = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(quiet.String(), "logout: session lookup failed") {
		t.Errorf("an unknown token was logged as a failed lookup: %q", quiet.String())
	}
}

// TestLogout_AMatchOnThePreviousTokenIsLogged pins the other half of the audit
// marker: when a sign-out ended its session by the token the session had one
// rotation ago, the log says so — with the session and user ids, which the audit
// row carries too, and with nothing that could be replayed. A sign-out by the
// current token logs no such line.
func TestLogout_AMatchOnThePreviousTokenIsLogged(t *testing.T) {
	const line = "revoked a session by the token it had before its last rotation"

	logs := captureProductionLog(t)
	a := newAuthRaceApp(t, nil)
	if resp := a.post(t, "/auth/logout", racePreviousToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := logs.String()
	if !strings.Contains(out, line) {
		t.Fatalf("a sign-out by the previous token left no trace in the log: %q", out)
	}
	if !strings.Contains(out, a.store.session.ID.String()) || !strings.Contains(out, a.store.user.ID.String()) {
		t.Errorf("the log line names neither the session nor its user: %q", out)
	}
	if strings.Contains(out, racePreviousToken) || strings.Contains(out, raceCurrentToken) ||
		strings.Contains(out, auth.HashToken(racePreviousToken)) || strings.Contains(out, auth.HashToken(raceCurrentToken)) {
		t.Errorf("the log line carries a token or a hash: %q", out)
	}

	// Control: the same sign-out by the CURRENT token logs no such line.
	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	if resp := b.post(t, "/auth/logout", raceCurrentToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("control status = %d, want 200", resp.StatusCode)
	}
	if got := len(b.store.named("RevokeSession")); got != 1 {
		t.Fatalf("control: RevokeSession sent %d times, want 1 — the control did not end a session", got)
	}
	if strings.Contains(quiet.String(), line) {
		t.Errorf("a sign-out by the current token was logged as a previous-token match: %q", quiet.String())
	}
}

// TestLogout_AnAbsurdlyLongTokenIsNoToken pins the bound on what Logout will look
// at: a refresh token longer than MaxRefreshTokenLength characters — the bound
// /auth/refresh's own schema enforces — is treated as no token. It still answers
// 200 and still clears the cookie, and nothing is hashed, looked up or logged for
// it.
//
// Each side of the edge is a row, so the bound cannot move unnoticed in either
// direction, and the rows at the bound are the controls for the rows over it: the
// same token a character shorter IS looked up. The bound counts CHARACTERS, as
// the schema's does, so the multi-byte rows sit a few hundred bytes over the
// byte count either way. Both ways a token arrives are covered, the cookie and
// the body.
func TestLogout_AnAbsurdlyLongTokenIsNoToken(t *testing.T) {
	const bound = MaxRefreshTokenLength
	// Every row below is relative to the constant, so the constant itself is
	// pinned here: 1024 characters is what docs/api-reference.md tells API clients,
	// and what /auth/refresh's schema is declared with.
	if bound != 1024 {
		t.Fatalf("MaxRefreshTokenLength = %d; the documented bound is 1024 characters", bound)
	}
	body := func(token string) string { return `{"refresh_token":"` + token + `"}` }

	tests := []struct {
		name       string
		body       string
		cookie     string
		wantLookup bool
	}{
		{name: "a cookie of exactly the bound", cookie: strings.Repeat("a", bound), wantLookup: true},
		{name: "a cookie one character over the bound", cookie: strings.Repeat("a", bound+1)},
		{name: "a body token of exactly the bound", body: body(strings.Repeat("a", bound)), wantLookup: true},
		{name: "a body token one character over the bound", body: body(strings.Repeat("a", bound+1))},
		{name: "a body token of exactly the bound in multi-byte characters", body: body(strings.Repeat("é", bound)), wantLookup: true},
		{name: "a body token one multi-byte character over the bound", body: body(strings.Repeat("é", bound+1))},
		{name: "a body token a megabyte long", body: body(strings.Repeat("a", 1<<20))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, nil)
			reqBody := tt.body
			if reqBody == "" {
				reqBody = "{}"
			}

			resp := a.postBody(t, "/auth/logout", reqBody, tt.cookie, nil)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
			}
			got := len(a.store.stmts) > 0
			if got != tt.wantLookup {
				t.Errorf("statements sent = %d, want a lookup = %t: a token over the bound must reach no query, one at it must", len(a.store.stmts), tt.wantLookup)
			}
			if n := len(a.store.named("RevokeSession")); n != 0 {
				t.Errorf("RevokeSession sent %d times for a token that belongs to no session", n)
			}
		})
	}
}

// TestRefresh_AnAbsurdlyLongTokenIsAnInvalidToken pins the bound Refresh puts on
// the token it will look at, for the one way in that no schema covers: the cookie.
// The route's schema bounds the body parameter; a cookie of any size that fits
// the request headers is otherwise hashed and looked up twice, for nothing. A
// token longer than MaxRefreshTokenLength characters is refused as the invalid
// token it is — the same 401, the same message and the same cleared cookie as a
// token no session holds, so a prober learns nothing from the difference — before
// anything is hashed, looked up or logged.
//
// Each side of the edge is a row, so the bound cannot move unnoticed in either
// direction, and the rows at the bound are the controls for the rows over it: the
// same token a character shorter IS looked up, which is what shows that "no
// statements" means refused and not merely quiet. The bound counts CHARACTERS, as
// the schema's does. Both ways a token arrives are covered; the body rows exercise
// the handler's own bound, because the mirror schema this harness runs behind
// does not declare the route's (in production a body token over the bound is
// refused by the schema with a 400 before the handler is reached).
func TestRefresh_AnAbsurdlyLongTokenIsAnInvalidToken(t *testing.T) {
	const bound = MaxRefreshTokenLength
	if bound != 1024 {
		t.Fatalf("MaxRefreshTokenLength = %d; the documented bound is 1024 characters", bound)
	}
	// A recognisable token of n characters, so that the log check below can look
	// for it.
	token := func(n int) string {
		const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
		return strings.Repeat(alphabet, n/len(alphabet)+1)[:n]
	}
	body := func(tok string) string { return `{"refresh_token":"` + tok + `"}` }

	tests := []struct {
		name       string
		token      string
		viaBody    bool // in the request body; otherwise in the cookie
		wantLookup bool
	}{
		{name: "a cookie of exactly the bound", token: token(bound), wantLookup: true},
		{name: "a cookie one character over the bound", token: token(bound + 1)},
		{name: "a cookie of three thousand characters", token: token(3000)},
		{name: "a body token of exactly the bound", token: token(bound), viaBody: true, wantLookup: true},
		{name: "a body token one character over the bound", token: token(bound + 1), viaBody: true},
		{name: "a body token of exactly the bound in multi-byte characters", token: strings.Repeat("é", bound), viaBody: true, wantLookup: true},
		{name: "a body token one multi-byte character over the bound", token: strings.Repeat("é", bound+1), viaBody: true},
		{name: "a body token a megabyte long", token: token(1 << 20), viaBody: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, nil)
			reqBody, cookie := "{}", tt.token
			if tt.viaBody {
				reqBody, cookie = body(tt.token), ""
			}

			resp := a.postBody(t, "/auth/refresh", reqBody, cookie, nil)
			got := decodeObject(t, resp)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, got)
			}
			if msg := got["message"]; msg != "Invalid or expired refresh token" {
				t.Errorf("message = %v, want the one every stale token gets", msg)
			}
			if _, issued := got["access_token"]; issued {
				t.Errorf("a refused refresh issued an access token: %v", got)
			}
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
			}

			looked := len(a.store.stmts) > 0
			if looked != tt.wantLookup {
				t.Errorf("statements sent = %d, want a lookup = %t: a token over the bound must reach no query, one at it must", len(a.store.stmts), tt.wantLookup)
			}
			if n := a.pool.txCount(); n != 0 {
				t.Errorf("a refresh that was refused began %d transactions", n)
			}
			if n := len(a.store.named("RotateSessionToken")) + len(a.store.named("RevokeSession")); n != 0 {
				t.Errorf("a token that belongs to no session changed one (%d writes)", n)
			}
			if !tt.wantLookup {
				head := string([]rune(tt.token)[:32])
				for _, secret := range []string{head, auth.HashToken(tt.token)} {
					if strings.Contains(logs.String(), secret) {
						t.Errorf("the log carries the refused token or its hash: %q", logs.String())
					}
				}
			}
		})
	}
}

// TestCurrentSessionID_NeverAcceptsThePreviousToken pins the other half of the
// guard above, in behaviour: the session list's is_current, which resolves the
// caller's own session through the refresh cookie, finds it only by the CURRENT
// token. A cookie holding the token the session had one rotation ago — which
// FindSessionForLogout does match, inside the sign-out window — resolves to
// nothing, exactly as an unknown token does.
//
// The first row is the control for the rest: the same harness, with the current
// token, does resolve the session. The previous-token row carries its own control
// too: before asking currentSessionID, it checks that FindSessionForLogout WOULD
// match the cookie in this very store, so that "nothing" is the refusal and not an
// artifact of a token that was never live.
func TestCurrentSessionID_NeverAcceptsThePreviousToken(t *testing.T) {
	tests := []struct {
		name         string
		cookie       string
		wantResolved bool
	}{
		{name: "the current token", cookie: raceCurrentToken, wantResolved: true},
		{name: "the token the session had one rotation ago", cookie: racePreviousToken},
		{name: "a token no session holds", cookie: "a-token-nobody-issued"},
		{name: "no cookie at all", cookie: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, raceRotatedAgo(2*time.Second))
			a.app.Get("/test/current-session", func(c fiber.Ctx) error {
				return c.JSON(fiber.Map{"id": a.handler.currentSessionID(c)})
			})

			if tt.cookie == racePreviousToken {
				session, viaPrevious, err := a.handler.sessionManager.FindSessionForLogout(context.Background(), racePreviousToken)
				if err != nil || !viaPrevious || session.ID != a.store.session.ID {
					t.Fatalf("control: FindSessionForLogout(previous token) = (%v, %t, %v); the token is not live as a previous token "+
						"in this store, so a refusal below would prove nothing", session.ID, viaPrevious, err)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/test/current-session", nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: tt.cookie})
			}
			resp, err := a.app.Test(req, fiber.TestConfig{Timeout: time.Second, FailOnTimeout: true})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			got := decodeObject(t, resp)

			want := uuid.Nil.String()
			if tt.wantResolved {
				want = a.store.session.ID.String()
			}
			if got["id"] != want {
				t.Errorf("currentSessionID = %v, want %v", got["id"], want)
			}
		})
	}
}
