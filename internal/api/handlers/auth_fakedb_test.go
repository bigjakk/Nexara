package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The pgx-facing half of the two auth harnesses: raceStore (one session and its user, a
// pool that can be starved) and epochStore (many users and sessions, auth epochs). A store
// embeds authFakeDB and implements authFakeModel, the statements it understands, applying
// the predicates the SQL does so that what a handler sends decides the outcome. Nothing here
// is evidence about the SQL itself: internal/db pins that against Postgres, and a database
// test cannot live in this package (CI migrates the shared database down to zero while
// packages run in parallel).

// authFakeStmt is one statement a handler sent. name is the sqlc query name, or the first
// word of a raw statement such as the advisory lock.
type authFakeStmt struct {
	name string
	args []any
	inTx bool
	sql  string
}

// authFakeAuditRow is an audit row as InsertAuditLog received it; actor is the zero UUID
// when the row names nobody.
type authFakeAuditRow struct {
	actor        uuid.UUID
	resourceType string
	resourceID   string
	action       string
}

func (r authFakeAuditRow) String() string {
	return fmt.Sprintf("{actor %s: %s %s %s}", r.actor, r.action, r.resourceType, r.resourceID)
}

// authFakeModel is the data half of a store. Its methods run with authFakeDB.mu held;
// remember registers the inverse of a write, replayed if the statement's transaction does
// not commit (a pool statement commits as it runs, so its inverse is dropped).
type authFakeModel interface {
	doExec(name string, args []any, remember func(undo func())) (pgconn.CommandTag, error)
	doQueryRow(name string, args []any, remember func(undo func())) pgx.Row
	doQuery(name string, args []any) (pgx.Rows, error)
}

// authFakeDB is the statement log, hooks and transaction accounting of a store.
type authFakeDB struct {
	mu    sync.Mutex // guards this struct and the model embedding it
	model authFakeModel

	stmts        []authFakeStmt
	audits       []string // the action of each audit row written
	auditDetails []string // and its details, in the same order
	auditRows    []authFakeAuditRow

	// A transaction holds a connection from Begin until it commits or rolls back.
	// poolWhileTx names every statement sent to the POOL while one was open: a request
	// that asks for a second connection while it holds one is how a pool of N deadlocks
	// under N+1 such requests. The count is global, so only meaningful where requests
	// do not overlap.
	openTx, committed, rolledBack int
	poolWhileTx                   []string
	commitAttempted               bool

	// commitErr is what a COMMIT answers; commitLands says it landed anyway (the answer
	// was lost). The handler cannot tell the two apart; the store shows which it was.
	commitErr   error
	commitLands bool
	rollbackErr error // what Rollback answers

	// lockWait models a statement waiting for a row lock. It runs, store unlocked, ahead
	// of a statement ("Commit" for a commit) and returns nil when the wait is over or the
	// context's error when that ended first, as a real wait does.
	lockWait func(ctx context.Context, name string) error
	// afterPool runs, store unlocked, once a POOL statement has completed and given its
	// connection back.
	afterPool func(name string)
	// A statement of a name in failOn fails with the error; one in stallOn waits for its
	// context to end and fails with that. stalledFor is how long each stall lasted.
	failOn     map[string]error
	stallOn    map[string]bool
	stalledFor map[string][]time.Duration
}

// authSQLName is the sqlc query name of a statement, or the first word of one that has
// none.
func authSQLName(sql string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(sql, "-- name: "), " ")
	if i := strings.IndexAny(name, "\n\t("); i >= 0 {
		name = name[:i]
	}
	return name
}

// run records a statement, applies the failure and stall hooks, and calls do with the lock
// held. tx is nil for a statement on the pool.
func (d *authFakeDB) run(ctx context.Context, sql string, args []any, tx *authFakeTx, do func(name string, remember func(func())) error) error {
	name := authSQLName(sql)
	d.mu.Lock()
	d.stmts = append(d.stmts, authFakeStmt{name: name, args: args, inTx: tx != nil, sql: sql})
	if tx == nil && d.openTx > 0 {
		d.poolWhileTx = append(d.poolWhileTx, name)
	}
	if d.stallOn[name] {
		d.mu.Unlock()
		began := time.Now()
		<-ctx.Done()
		d.mu.Lock()
		d.stalledFor[name] = append(d.stalledFor[name], time.Since(began))
		d.mu.Unlock()
		return ctx.Err()
	}
	defer d.mu.Unlock()
	if err := d.failOn[name]; err != nil {
		return err
	}
	remember := func(func()) {}
	if tx != nil {
		remember = func(undo func()) { tx.undo = append(tx.undo, undo) }
	}
	return do(name, remember)
}

func (d *authFakeDB) exec(ctx context.Context, sql string, args []any, tx *authFakeTx) (tag pgconn.CommandTag, err error) {
	err = d.run(ctx, sql, args, tx, func(name string, remember func(func())) (e error) {
		tag, e = d.model.doExec(name, args, remember)
		return e
	})
	return tag, err
}

func (d *authFakeDB) query(ctx context.Context, sql string, args []any, tx *authFakeTx) (rows pgx.Rows, err error) {
	err = d.run(ctx, sql, args, tx, func(name string, _ func(func())) (e error) {
		rows, e = d.model.doQuery(name, args)
		return e
	})
	return rows, err
}

func (d *authFakeDB) queryRow(ctx context.Context, sql string, args []any, tx *authFakeTx) pgx.Row {
	var row pgx.Row
	if err := d.run(ctx, sql, args, tx, func(name string, remember func(func())) error {
		row = d.model.doQueryRow(name, args, remember)
		return nil
	}); err != nil {
		return authFakeRow{err: err}
	}
	return row
}

// admit is the front of a statement on a transaction: a context that has ended is refused,
// then the statement waits for its row lock.
func (d *authFakeDB) admit(ctx context.Context, sql string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.waitLock(ctx, authSQLName(sql))
}

func (d *authFakeDB) waitLock(ctx context.Context, name string) error {
	d.mu.Lock()
	hook := d.lockWait
	d.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(ctx, name)
}

func (d *authFakeDB) poolStatementDone(sql string) {
	d.mu.Lock()
	hook := d.afterPool
	d.mu.Unlock()
	if hook != nil {
		hook(authSQLName(sql))
	}
}

// insertAudit records an InsertAuditLog: cluster_id, user_id, resource_type, resource_id,
// action, details. Called by the model, with the lock held.
func (d *authFakeDB) insertAudit(args []any) (pgconn.CommandTag, error) {
	row := authFakeAuditRow{resourceType: args[2].(string), resourceID: args[3].(string), action: args[4].(string)}
	if actor, ok := args[1].(pgtype.UUID); ok && actor.Valid {
		row.actor = uuid.UUID(actor.Bytes)
	}
	details, _ := args[5].(json.RawMessage)
	d.audits = append(d.audits, row.action)
	d.auditDetails = append(d.auditDetails, string(details))
	d.auditRows = append(d.auditRows, row)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

// authLocked reads under the store's lock.
func authLocked[T any](d *authFakeDB, read func() T) T {
	d.mu.Lock()
	defer d.mu.Unlock()
	return read()
}

// statements are every statement sent so far, in order.
func (d *authFakeDB) statements() []authFakeStmt {
	return authLocked(d, func() []authFakeStmt { return append([]authFakeStmt(nil), d.stmts...) })
}

// named are the statements sent for one query, in order.
func (d *authFakeDB) named(name string) []authFakeStmt {
	var out []authFakeStmt
	for _, st := range d.statements() {
		if st.name == name {
			out = append(out, st)
		}
	}
	return out
}

func (d *authFakeDB) auditActions() []string {
	return authLocked(d, func() []string { return append([]string(nil), d.audits...) })
}

func (d *authFakeDB) auditDetailsWritten() []string {
	return authLocked(d, func() []string { return append([]string(nil), d.auditDetails...) })
}

func (d *authFakeDB) auditRowsWritten() []authFakeAuditRow {
	return authLocked(d, func() []authFakeAuditRow { return append([]authFakeAuditRow(nil), d.auditRows...) })
}

func (d *authFakeDB) txCounts() (committed, rolledBack, open int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.committed, d.rolledBack, d.openTx
}

func (d *authFakeDB) openTransactions() int {
	_, _, open := d.txCounts()
	return open
}

func (d *authFakeDB) poolStatementsWhileTx() []string {
	return authLocked(d, func() []string { return append([]string(nil), d.poolWhileTx...) })
}

// stallWaits is how long each stalled statement of this name waited for its context.
func (d *authFakeDB) stallWaits(name string) []time.Duration {
	return authLocked(d, func() []time.Duration { return append([]time.Duration(nil), d.stalledFor[name]...) })
}

// authConnectionComplaints is the check every race-harness test inherits at its end: no
// statement went to the pool while a transaction was open, and none was left open.
func authConnectionComplaints(d *authFakeDB) []string {
	var out []string
	if got := d.poolStatementsWhileTx(); len(got) != 0 {
		out = append(out, fmt.Sprintf("the handler queried the POOL (%v) while its own transaction still held a connection: "+
			"with the pool at its limit that is a deadlock, every connection held by a request waiting for another", got))
	}
	if n := d.openTransactions(); n != 0 {
		out = append(out, fmt.Sprintf("%d transactions were left open: their connections never went back to the pool", n))
	}
	return out
}

// authFakeRow replays one generated model, or an error, through pgx.Row, assigning
// positionally in the struct's field order, which is the order the SELECTs scan in: a
// column added in the middle reports a type mismatch instead of shifting a value.
type authFakeRow struct {
	value any
	err   error
}

func (r authFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return authScanStruct(r.value, dest)
}

func authScanStruct(value any, dest []any) error {
	row := reflect.ValueOf(value)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s: the select column order changed", i, d, row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// authFakeScalar replays one column, or an error, for the statements that select a single
// value instead of a model.
type authFakeScalar struct {
	value any
	err   error
}

func (r authFakeScalar) Scan(dest ...any) error {
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

// authFakeRows replays generated models through pgx.Rows. The embedded pgx.Rows is nil on
// purpose: the generated :many code uses only what is implemented here.
type authFakeRows struct {
	pgx.Rows
	items []any
	pos   int
}

func (r *authFakeRows) Next() bool {
	if r.pos >= len(r.items) {
		return false
	}
	r.pos++
	return true
}

func (r *authFakeRows) Scan(dest ...any) error { return authScanStruct(r.items[r.pos-1], dest) }
func (r *authFakeRows) Close()                 {}
func (r *authFakeRows) Err() error             { return nil }

// authFakeGate models the connections of a pool of fixed size: acquire waits for a free
// one or for the context to end, as pgxpool's does, and waiters are served in arrival
// order. A nil gate is a pool with no limit.
type authFakeGate struct {
	tokens chan struct{}

	mu      sync.Mutex
	waiting int
	changed chan struct{} // closed, and replaced, whenever the pool's state changes
}

func authNewFakeGate(size int) *authFakeGate {
	return &authFakeGate{tokens: make(chan struct{}, size), changed: make(chan struct{})}
}

// note records a change of state: delta more callers are waiting, and whoever holds
// changes() is woken.
func (g *authFakeGate) note(delta int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting += delta
	close(g.changed)
	g.changed = make(chan struct{})
}

// changes is a channel closed at the next change of state. Take it BEFORE reading the
// state it is meant to wake you for.
func (g *authFakeGate) changes() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.changed
}

func (g *authFakeGate) acquire(ctx context.Context) error {
	// A context that has ended gets nothing, even from an idle pool, as in pgxpool.
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil {
		return nil
	}
	select {
	case g.tokens <- struct{}{}:
		g.note(0)
		return nil
	default:
	}
	g.note(1)
	defer g.note(-1)
	select {
	case g.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *authFakeGate) release() {
	if g != nil {
		<-g.tokens
		g.note(0)
	}
}

func (g *authFakeGate) inUse() int {
	if g == nil {
		return 0
	}
	return len(g.tokens)
}

// waiters is how many callers are waiting for a connection right now.
func (g *authFakeGate) waiters() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waiting
}

// authFakeConn is the pool-bound path, what h.queries and the SessionManager use: each
// statement takes a connection of the gate's pool for as long as it runs.
type authFakeConn struct {
	store *authFakeDB
	gate  *authFakeGate
}

// pooled runs one statement on a connection; it returns the error of getting one.
func (c authFakeConn) pooled(ctx context.Context, sql string, run func()) error {
	if err := c.gate.acquire(ctx); err != nil {
		return err
	}
	err := func() error {
		defer c.gate.release()
		if err := c.store.waitLock(ctx, authSQLName(sql)); err != nil {
			return err
		}
		run()
		return nil
	}()
	c.store.poolStatementDone(sql)
	return err
}

func (c authFakeConn) Exec(ctx context.Context, sql string, args ...any) (tag pgconn.CommandTag, err error) {
	if gateErr := c.pooled(ctx, sql, func() { tag, err = c.store.exec(ctx, sql, args, nil) }); gateErr != nil {
		return pgconn.CommandTag{}, gateErr
	}
	return tag, err
}

func (c authFakeConn) Query(ctx context.Context, sql string, args ...any) (rows pgx.Rows, err error) {
	if gateErr := c.pooled(ctx, sql, func() { rows, err = c.store.query(ctx, sql, args, nil) }); gateErr != nil {
		return nil, gateErr
	}
	return rows, err
}

func (c authFakeConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	var row pgx.Row
	if gateErr := c.pooled(ctx, sql, func() { row = c.store.queryRow(ctx, sql, args, nil) }); gateErr != nil {
		return authFakeRow{err: gateErr}
	}
	return row
}

// authFakeTx is a handler's transaction. The embedded pgx.Tx is nil on purpose: a handler
// may use only what is implemented here. It holds one connection from Begin until the
// FIRST of Commit and Rollback (a second answers pgx.ErrTxClosed, as pgx's does), and its
// writes are undone, newest first, when it rolls back or its commit does not land.
type authFakeTx struct {
	pgx.Tx
	store *authFakeDB
	gate  *authFakeGate

	mu                            sync.Mutex
	closed, committed, rolledBack bool
	undo                          []func() // guarded by store.mu

	// What the context Rollback was given looked like when it arrived: a rollback on the
	// request's own context, spent by the time most rollbacks are needed, cannot happen.
	rollbackCalled   bool
	rollbackCtxErr   error
	rollbackHasLimit bool
	rollbackLeft     time.Duration
}

func (t *authFakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.store.admit(ctx, sql); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.store.exec(ctx, sql, args, t)
}

func (t *authFakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.store.admit(ctx, sql); err != nil {
		return nil, err
	}
	return t.store.query(ctx, sql, args, t)
}

func (t *authFakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.store.admit(ctx, sql); err != nil {
		return authFakeRow{err: err}
	}
	return t.store.queryRow(ctx, sql, args, t)
}

// finish closes the transaction and gives its connection back, keeping its writes or
// undoing them. t.mu is held.
func (t *authFakeTx) finish(keep bool) {
	d := t.store
	d.mu.Lock()
	t.closed = true
	d.openTx--
	if keep {
		t.committed = true
		d.committed++
	} else {
		for i := len(t.undo) - 1; i >= 0; i-- {
			t.undo[i]()
		}
		d.rolledBack++
	}
	t.undo = nil
	d.mu.Unlock()
	t.gate.release()
}

func (t *authFakeTx) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	d := t.store
	d.mu.Lock()
	d.commitAttempted = true
	d.mu.Unlock()
	// pgx closes the transaction whether or not the commit succeeds. One whose context
	// had ended before it started was never sent, and pgconn says so; one whose context
	// ends while it waits was sent, and says only that its context ended.
	if err := ctx.Err(); err != nil {
		t.finish(false)
		return &neverSentError{err: err}
	}
	if err := d.waitLock(ctx, "Commit"); err != nil {
		t.finish(false)
		return err
	}
	d.mu.Lock()
	err, lands := d.commitErr, d.commitLands
	d.mu.Unlock()
	t.finish(err == nil || lands)
	return err
}

func (t *authFakeTx) Rollback(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.rollbackCalled, t.rollbackCtxErr = true, ctx.Err()
	if at, ok := ctx.Deadline(); ok {
		t.rollbackHasLimit, t.rollbackLeft = true, time.Until(at)
	}
	t.rolledBack = true
	t.finish(false)
	return authLocked(t.store, func() error { return t.store.rollbackErr })
}

func (t *authFakeTx) state() (committed, rolledBack bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.committed, t.rolledBack
}

// rollbackContext reports whether Rollback was called, whether its context had a deadline
// and how long that had left, and its Err().
func (t *authFakeTx) rollbackContext() (called, hasDeadline bool, left time.Duration, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rollbackCalled, t.rollbackHasLimit, t.rollbackLeft, t.rollbackCtxErr
}

// authFakePool is the pool's Begin: one authFakeTx per call, each holding a connection of
// the gate's pool.
type authFakePool struct {
	store *authFakeDB
	gate  *authFakeGate

	// beforeBegin runs ahead of a connection being acquired, afterBegin once one has been,
	// while the request holds it: tests line concurrent requests up with them, and take the
	// pool's connections away.
	beforeBegin func()
	afterBegin  func()
	beginErr    error // Begin fails with it, as a pool that cannot connect does

	mu                sync.Mutex
	txs               []*authFakeTx
	attempts          int // calls to Begin, whether or not they got a connection
	lastBeginDeadline time.Time
	txOptions         []pgx.TxOptions // of every BeginTx, in order
}

func (p *authFakePool) Begin(ctx context.Context) (pgx.Tx, error) {
	p.mu.Lock()
	p.attempts++
	p.lastBeginDeadline, _ = ctx.Deadline()
	p.mu.Unlock()
	if p.beforeBegin != nil {
		p.beforeBegin()
	}
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if err := p.gate.acquire(ctx); err != nil {
		return nil, err
	}
	tx := &authFakeTx{store: p.store, gate: p.gate}
	p.store.mu.Lock()
	p.store.openTx++
	p.store.mu.Unlock()
	p.mu.Lock()
	p.txs = append(p.txs, tx)
	p.mu.Unlock()
	if p.afterBegin != nil {
		p.afterBegin()
	}
	return tx, nil
}

// BeginTx is Begin with the options it was asked for recorded: a transaction whose
// correctness depends on its isolation level names it, and a test reads what it named.
func (p *authFakePool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	p.mu.Lock()
	p.txOptions = append(p.txOptions, opts)
	p.mu.Unlock()
	return p.Begin(ctx)
}

func (p *authFakePool) beginOptions() []pgx.TxOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pgx.TxOptions(nil), p.txOptions...)
}

func (p *authFakePool) beginAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts
}

// beginDeadline is the deadline the latest Begin's context carried, zero if none.
func (p *authFakePool) beginDeadline() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastBeginDeadline
}

// txCount is how many transactions have been begun on this pool.
func (p *authFakePool) txCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.txs)
}

func (p *authFakePool) only(t *testing.T) *authFakeTx {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.txs) != 1 {
		t.Fatalf("handler began %d transactions, want exactly 1", len(p.txs))
	}
	return p.txs[0]
}

// neverSentError is the shape pgconn gives a statement whose context had already ended when
// it was about to be sent: SafeToRetry, and it unwraps to the context's own error. Nothing
// went to the server. internal/db has the real thing against Postgres
// (TestAnUnsentCommitIsSafeToRetryAndTheServerRollsBack).
type neverSentError struct{ err error }

func (e *neverSentError) Error() string     { return "timeout: context already done: " + e.err.Error() }
func (e *neverSentError) SafeToRetry() bool { return true }
func (e *neverSentError) Unwrap() error     { return e.err }

// connClosedError is the shape pgx gives a COMMIT whose connection closes while its reply
// is awaited: SafeToRetry although the COMMIT WAS sent and the server may have executed
// it, with no context error (TestALostCommitReplyLooksSafeToRetryAndHasLanded in
// internal/db).
type connClosedError struct{}

func (connClosedError) Error() string     { return "conn closed" }
func (connClosedError) SafeToRetry() bool { return true }
func (connClosedError) Unwrap() error     { return pgconn.ErrConnClosed }

// The two kinds of database failure the handlers tell apart (isTransientDBError): the
// database being unreachable, a connection reset under a read as pgx reports it, and a
// failure that is not, a defect.
var (
	errRaceTransient = fmt.Errorf("failed to receive message: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})
	errRaceBug       = errors.New("unexpected database failure")
)

// authCounter is the smallest model an authFakeDB can run: one number, bumped by a write that
// can be undone.
type authCounter struct{ n int }

func (c *authCounter) doExec(_ string, _ []any, remember func(func())) (pgconn.CommandTag, error) {
	c.n++
	remember(func() { c.n-- })
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (c *authCounter) doQueryRow(string, []any, func(func())) pgx.Row {
	return authFakeScalar{value: c.n}
}

func (*authCounter) doQuery(string, []any) (pgx.Rows, error) { return &authFakeRows{}, nil }

// TestAuthFakeDB_BehavesLikeThePostgresItStandsFor holds the stand-in both auth harnesses are
// built on to pgx and Postgres where their tests lean on it: a check made against a stand-in
// more forgiving than the real thing proves nothing. Each row says what the real one does.
// That the two models' predicates are the SQL's own is pinned against Postgres in internal/db.
func TestAuthFakeDB_BehavesLikeThePostgresItStandsFor(t *testing.T) {
	const bump = "-- name: Bump :exec"
	ctx := context.Background()
	ended, cancel := context.WithCancel(ctx)
	cancel()

	type world struct {
		d    *authFakeDB
		n    *authCounter
		gate *authFakeGate
		pool *authFakePool
		conn authFakeConn
	}
	count := func(w *world) int { return authLocked(w.d, func() int { return w.n.n }) }
	begin := func(t *testing.T, w *world) pgx.Tx {
		t.Helper()
		tx, err := w.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		return tx
	}
	write := func(t *testing.T, exec func(context.Context, string, ...any) (pgconn.CommandTag, error)) {
		t.Helper()
		if _, err := exec(ctx, bump); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	ok := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%v", err)
		}
	}
	// checks is what the end-of-test check says: nothing, or exactly one complaint naming sub.
	checks := func(t *testing.T, w *world, sub string) {
		t.Helper()
		got := authConnectionComplaints(w.d)
		if (sub == "") != (len(got) == 0) || len(got) > 1 || (sub != "" && !strings.Contains(got[0], sub)) {
			t.Errorf("the end-of-test check gave %v, want %q", got, sub)
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, w *world)
	}{
		{"a pool statement made while a transaction is open is caught, and stays caught", func(t *testing.T, w *world) {
			write(t, w.conn.Exec)
			checks(t, w, "")
			tx := begin(t, w)
			write(t, tx.Exec)
			checks(t, w, "left open")
			write(t, w.conn.Exec)
			ok(t, tx.Rollback(ctx))
			checks(t, w, "POOL ([Bump])")
		}},
		{"a transaction gives its connection back once, and answers pgx.ErrTxClosed after", func(t *testing.T, w *world) {
			tx := begin(t, w)
			held := w.gate.inUse() + w.d.openTransactions()
			ok(t, tx.Rollback(ctx))
			again, after := tx.Rollback(ctx), tx.Commit(ctx)
			if left := w.gate.inUse() + w.d.openTransactions(); held != 2 || left != 0 || !errors.Is(again, pgx.ErrTxClosed) || !errors.Is(after, pgx.ErrTxClosed) {
				t.Errorf("connections and open transactions: %d held, %d after; a second rollback answered %v, a commit after it %v; want 2, 0, ErrTxClosed twice", held, left, again, after)
			}
		}},
		{"a rollback undoes the writes and a commit keeps them", func(t *testing.T, w *world) {
			tx := begin(t, w)
			write(t, tx.Exec)
			ok(t, tx.Rollback(ctx))
			rolled := count(w)
			tx = begin(t, w)
			write(t, tx.Exec)
			ok(t, tx.Commit(ctx))
			if rolled != 0 || count(w) != 1 {
				t.Errorf("writes left after a rollback and after a commit: %d and %d, want 0 and 1", rolled, count(w))
			}
		}},
		{"a COMMIT whose reply was lost has landed or not as the store says, and looks safe to retry", func(t *testing.T, w *world) {
			for _, lands := range []bool{true, false} {
				before := count(w)
				w.d.commitErr, w.d.commitLands = connClosedError{}, lands
				tx := begin(t, w)
				write(t, tx.Exec)
				err := tx.Commit(ctx)
				if kept := count(w)-before == 1; kept != lands || !errors.Is(err, pgconn.ErrConnClosed) || !pgconn.SafeToRetry(err) {
					t.Errorf("a lost reply, landing = %v: answered %v, write kept = %v", lands, err, kept)
				}
			}
		}},
		{"a COMMIT on an ended context was never sent, undoes the writes and closes the transaction", func(t *testing.T, w *world) {
			tx := begin(t, w)
			write(t, tx.Exec)
			err, again := tx.Commit(ended), tx.Commit(ctx)
			if !errors.Is(err, context.Canceled) || !pgconn.SafeToRetry(err) || !errors.Is(again, pgx.ErrTxClosed) || count(w) != 0 || w.gate.inUse() != 0 {
				t.Errorf("a commit on an ended context answered %v, then %v; %d writes left, %d connections held", err, again, count(w), w.gate.inUse())
			}
		}},
		{"an ended context is refused by a statement, a transaction and an idle pool, and nothing is written", func(t *testing.T, w *world) {
			tx := begin(t, w)
			_, onTx := tx.Exec(ended, bump)
			_, onPool := w.conn.Exec(ended, bump)
			ok(t, tx.Rollback(ctx))
			_, begun := w.pool.Begin(ended)
			for _, err := range []error{onTx, onPool, begun} {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("a call on an ended context answered %v, want the context's error", err)
				}
			}
			if count(w) != 0 {
				t.Errorf("%d writes made on an ended context", count(w))
			}
		}},
		{"a stalled statement waits for its context to end, answers with that context's error and writes nothing", func(t *testing.T, w *world) {
			w.d.stallOn, w.d.stalledFor = map[string]bool{"Bump": true}, map[string][]time.Duration{}
			short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
			defer stop()
			_, err := w.conn.Exec(short, bump)
			// short.Err() is read once the call has returned: a stall that did not wait for its
			// context would be back before it ended, whatever the load.
			if !errors.Is(err, context.DeadlineExceeded) || short.Err() == nil || len(w.d.stallWaits("Bump")) != 1 || count(w) != 0 || w.gate.inUse() != 0 {
				t.Errorf("a stalled statement answered %v with its context's error = %v, %d stalls recorded, %d writes, %d connections held; want the deadline error of an ended context, 1, 0 and 0",
					err, short.Err(), len(w.d.stallWaits("Bump")), count(w), w.gate.inUse())
			}
		}},
		{"a write landing between two statements is seen by the second; a session needs an active account at the epoch read", func(t *testing.T, _ *world) {
			user := epochUser(t, epochLoginEmail, 3)
			store := newEpochStore(user)
			store.after["GetUserByID"] = func(s *epochStore) { // a revoke-all landing right behind the first read
				s.users[user.ID].AuthEpoch++
				delete(s.after, "GetUserByID")
			}
			q := db.New(authFakeConn{store: &store.authFakeDB})
			first, _ := q.GetUserByID(ctx, user.ID)
			second, _ := q.GetUserByID(ctx, user.ID)
			insert := func(epoch int64) error {
				_, err := q.CreateSessionAtEpoch(ctx, db.CreateSessionAtEpochParams{TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour), UserID: user.ID, Epoch: epoch})
				return err
			}
			stale, current := insert(3), insert(4)
			store.mutate(user.ID, func(u *db.User) { u.IsActive = false })
			inactive := insert(4)
			if first.AuthEpoch != 3 || second.AuthEpoch != 4 || !errors.Is(stale, pgx.ErrNoRows) || current != nil ||
				!errors.Is(inactive, pgx.ErrNoRows) || len(store.sessionsOf(user.ID)) != 1 {
				t.Errorf("reads saw epochs %d and %d, want 3 and 4; inserts at the stale epoch, the current one and for an inactive account answered %v, %v, %v, with %d sessions, want ErrNoRows, nil, ErrNoRows, 1",
					first.AuthEpoch, second.AuthEpoch, stale, current, inactive, len(store.sessionsOf(user.ID)))
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, gate := &authCounter{}, authNewFakeGate(2)
			d := &authFakeDB{model: n}
			tt.run(t, &world{d, n, gate, &authFakePool{store: d, gate: gate}, authFakeConn{store: d, gate: gate}})
		})
	}
}
