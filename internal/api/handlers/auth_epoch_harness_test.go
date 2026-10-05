package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The handler half of "no session from a credential check that a revoke-all has
// since invalidated": the real Login, Register, OIDCTokenExchange, VerifyLogin and
// UserHandler.Update, through a real Fiber app, the real SessionManager over
// miniredis, and a stand-in for the users and sessions tables.
//
// The stand-in (epochStore) applies the same predicates the SQL does — the session
// insert matches only a user that is active and still holds the epoch it is given,
// the bump moves the epoch by one, a transaction's writes are undone when it rolls
// back — so what the handler sends is what decides the outcome, and an epoch passed
// from the wrong place shows up as a refusal here rather than passing a test that
// returned canned rows. It is NOT evidence about the SQL: that those predicates are
// the ones queries/sessions.sql states, and that the interleavings come out as
// claimed under READ COMMITTED, is pinned against Postgres in
// internal/db/session_epoch_db_test.go. (It does not model FOR SHARE: a stand-in
// without row locks cannot, and the lock is what that file proves.)
//
// Tests move the world between two statements of one request with the after hooks:
// a writer landing in the gap between the credential check and the session insert,
// which is the whole of what the change is about.

// epochStatement is one statement a handler sent.
type epochStatement struct {
	name string
	args []any
	inTx bool
	// sql is the statement's text, for the few tests that must tell statements apart
	// that have no sqlc name: the advisory lock Register takes is a raw statement, and
	// "SELECT" is all its name says.
	sql string
}

// epochAuditRow is an audit row as InsertAuditLog received it: who the row says acted
// (the zero UUID when it names nobody), on what, and what happened.
type epochAuditRow struct {
	actor        uuid.UUID
	resourceType string
	resourceID   string
	action       string
}

// String keeps a failing comparison readable: fmt cannot call String on the actor of
// an unexported field, and would print its sixteen bytes.
func (r epochAuditRow) String() string {
	return fmt.Sprintf("{actor %s: %s %s %s}", r.actor, r.action, r.resourceType, r.resourceID)
}

// epochStore is the users table and the sessions table as the handlers see them.
// Every field is guarded by mu; the after hooks are called with it held.
type epochStore struct {
	mu sync.Mutex

	users    map[uuid.UUID]*db.User
	sessions []db.Session // the rows CreateSessionAtEpoch inserted

	// recoveryCodes are a user's unspent recovery codes (id and bcrypt hash), and
	// spentRecoveryCodes how many DeleteRecoveryCode removed.
	recoveryCodes      map[uuid.UUID][]db.ListRecoveryCodesRow
	spentRecoveryCodes int

	// after runs once a statement of this name has produced its answer, inside it —
	// a writer landing right behind a read. It mutates the store directly.
	after map[string]func(*epochStore)
	// failOn makes every statement of this name fail with the error; stallOn makes
	// it wait for its context to end and fail with that, which is what a statement
	// stuck on a lock or a pool with no free connection does to a caller that bounded
	// itself.
	failOn  map[string]error
	stallOn map[string]bool
	// stalledFor is how long each stalled statement actually waited for its context to
	// end, by statement name. A test that bounds a follow-up reads it to see where the
	// stall was cut, which a request's own duration cannot say: a registration's
	// includes hashing a password.
	stalledFor map[string][]time.Duration

	// commitErr, when set, is what a transaction's COMMIT answers; commitLands says
	// whether it nevertheless landed (the server committed and the answer was lost).
	commitErr   error
	commitLands bool

	committed, rolledBack int
	openTx                int
	poolWhileTx           []string // statements that went to the POOL while a transaction was open

	stmts        []epochStatement
	audits       []string
	auditDetails []string
	auditRows    []epochAuditRow
}

func newEpochStore(users ...db.User) *epochStore {
	s := &epochStore{
		users:         map[uuid.UUID]*db.User{},
		recoveryCodes: map[uuid.UUID][]db.ListRecoveryCodesRow{},
		after:         map[string]func(*epochStore){},
		failOn:        map[string]error{},
		stallOn:       map[string]bool{},
		stalledFor:    map[string][]time.Duration{},
	}
	for _, u := range users {
		u := u
		s.users[u.ID] = &u
	}
	return s
}

func (s *epochStore) user(id uuid.UUID) db.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		return *u
	}
	return db.User{}
}

// mutate changes a user's row the way another request committing would.
func (s *epochStore) mutate(id uuid.UUID, fn func(*db.User)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		fn(u)
	}
}

func (s *epochStore) bump(id uuid.UUID) { s.mutate(id, func(u *db.User) { u.AuthEpoch++ }) }

func (s *epochStore) sessionsOf(id uuid.UUID) []db.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.Session
	for _, sess := range s.sessions {
		if sess.UserID == id {
			out = append(out, sess)
		}
	}
	return out
}

func (s *epochStore) liveSessionsOf(id uuid.UUID) []db.Session {
	var out []db.Session
	for _, sess := range s.sessionsOf(id) {
		if !sess.IsRevoked {
			out = append(out, sess)
		}
	}
	return out
}

func (s *epochStore) named(name string) []epochStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []epochStatement
	for _, st := range s.stmts {
		if st.name == name {
			out = append(out, st)
		}
	}
	return out
}

// inTxStatements are the statements that ran on a transaction, in the order they ran.
func (s *epochStore) inTxStatements() []epochStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []epochStatement
	for _, st := range s.stmts {
		if st.inTx {
			out = append(out, st)
		}
	}
	return out
}

func (s *epochStore) auditActions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.audits...)
}

func (s *epochStore) auditRowsWritten() []epochAuditRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]epochAuditRow(nil), s.auditRows...)
}

func (s *epochStore) auditDetailsWritten() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auditDetails...)
}

func (s *epochStore) recoveryCodesOf(id uuid.UUID) []db.ListRecoveryCodesRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]db.ListRecoveryCodesRow(nil), s.recoveryCodes[id]...)
}

func (s *epochStore) spentRecoveryCodeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spentRecoveryCodes
}

func (s *epochStore) txCounts() (committed, rolledBack, open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed, s.rolledBack, s.openTx
}

func (s *epochStore) poolStatementsWhileTx() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.poolWhileTx...)
}

func epochSQLName(sql string) string {
	name := sqlName(sql)
	// A statement that is not an sqlc query (the advisory lock Register takes) has
	// no -- name: line; its first word stands in.
	if i := strings.IndexAny(name, " \n\t("); i >= 0 {
		name = name[:i]
	}
	return name
}

// enter records the statement and decides, before it runs, whether it fails or
// stalls. A stalled statement waits for ctx and returns its error.
func (s *epochStore) enter(ctx context.Context, sql string, args []any, inTx bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := epochSQLName(sql)
	s.mu.Lock()
	s.stmts = append(s.stmts, epochStatement{name: name, args: args, inTx: inTx, sql: sql})
	if !inTx && s.openTx > 0 {
		s.poolWhileTx = append(s.poolWhileTx, name)
	}
	err, stall := s.failOn[name], s.stallOn[name]
	s.mu.Unlock()
	if stall {
		began := time.Now()
		<-ctx.Done()
		s.mu.Lock()
		s.stalledFor[name] = append(s.stalledFor[name], time.Since(began))
		s.mu.Unlock()
		return name, ctx.Err()
	}
	return name, err
}

// stallWaits is how long each stalled statement of this name waited for its context
// to end.
func (s *epochStore) stallWaits(name string) []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.stalledFor[name]...)
}

// requireStallCutShort fails unless a stalled statement of this name was reached and
// was cut at the follow-up bound a test sets — a tenth of a second — and not at a
// longer one. The production bound is five seconds: a stall that lasted that long is a
// follow-up whose bound the test could not shorten, or one that was never bounded.
func requireStallCutShort(t *testing.T, store *epochStore, name string) {
	t.Helper()
	waits := store.stallWaits(name)
	if len(waits) == 0 {
		t.Errorf("no %s statement stalled: the premise of the test is gone", name)
		return
	}
	for _, w := range waits {
		if w > 2*time.Second {
			t.Errorf("a stalled %s waited %v with a 100 ms follow-up bound: the follow-up held the answer", name, w)
		}
	}
}

func (s *epochStore) afterHook(name string) {
	if fn := s.after[name]; fn != nil {
		fn(s)
	}
}

// exec runs a write. undo is the log of the transaction the statement belongs to,
// nil for a statement on the pool, which commits as it runs.
func (s *epochStore) exec(ctx context.Context, sql string, args []any, inTx bool, undo *[]func()) (pgconn.CommandTag, error) {
	name, err := s.enter(ctx, sql, args, inTx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remember := func(restore func()) {
		if undo != nil {
			*undo = append(*undo, restore)
		}
	}
	defer s.afterHook(name)

	switch name {
	case "SELECT": // pg_advisory_xact_lock
		return pgconn.NewCommandTag("SELECT 1"), nil
	case "BumpUserAuthEpoch":
		u, ok := s.users[args[0].(uuid.UUID)]
		if !ok {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		was := u.AuthEpoch
		remember(func() { u.AuthEpoch = was })
		u.AuthEpoch++
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeAllUserSessions":
		id := args[0].(uuid.UUID)
		for i := range s.sessions {
			if s.sessions[i].UserID == id && !s.sessions[i].IsRevoked {
				i := i
				remember(func() { s.sessions[i].IsRevoked = false })
				s.sessions[i].IsRevoked = true
			}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeAllUserRoles":
		return pgconn.NewCommandTag("DELETE 0"), nil
	case "DeleteRecoveryCode":
		id := args[0].(uuid.UUID)
		for uid, codes := range s.recoveryCodes {
			for i, rc := range codes {
				if rc.ID == id {
					s.recoveryCodes[uid] = append(append([]db.ListRecoveryCodesRow(nil), codes[:i]...), codes[i+1:]...)
					s.spentRecoveryCodes++
					return pgconn.NewCommandTag("DELETE 1"), nil
				}
			}
		}
		return pgconn.NewCommandTag("DELETE 0"), nil
	case "InsertAuditLog":
		// cluster_id, user_id, resource_type, resource_id, action, details
		s.audits = append(s.audits, args[4].(string))
		details, _ := args[5].(json.RawMessage)
		s.auditDetails = append(s.auditDetails, string(details))
		row := epochAuditRow{resourceType: args[2].(string), resourceID: args[3].(string), action: args[4].(string)}
		if actor, ok := args[1].(pgtype.UUID); ok && actor.Valid {
			row.actor = uuid.UUID(actor.Bytes)
		}
		s.auditRows = append(s.auditRows, row)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", name)
	}
}

func (s *epochStore) queryRow(ctx context.Context, sql string, args []any, inTx bool, undo *[]func()) pgx.Row {
	name, err := s.enter(ctx, sql, args, inTx)
	if err != nil {
		return raceRow{err: err}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remember := func(restore func()) {
		if undo != nil {
			*undo = append(*undo, restore)
		}
	}
	defer s.afterHook(name)

	switch name {
	case "GetUserByEmail":
		for _, u := range s.users {
			if u.Email == args[0].(string) {
				return raceRow{value: *u}
			}
		}
		return raceRow{err: pgx.ErrNoRows}
	case "GetUserByID":
		if u, ok := s.users[args[0].(uuid.UUID)]; ok {
			return raceRow{value: *u}
		}
		return raceRow{err: pgx.ErrNoRows}
	case "CountUsers":
		return raceScalarRow{value: int64(len(s.users))}
	case "CreateUser":
		// email, password_hash, display_name, is_active, totp_secret, role
		u := &db.User{
			ID: uuid.New(), Email: args[0].(string), PasswordHash: args[1].(string), DisplayName: args[2].(string),
			IsActive: args[3].(bool), TotpSecret: args[4].(pgtype.Text), Role: args[5].(string), AuthSource: "local",
		}
		s.users[u.ID] = u
		remember(func() { delete(s.users, u.ID) })
		return raceRow{value: *u}
	case "AssignUserRole":
		return raceRow{value: db.UserRole{}}
	case "GetUserByEmailAndSource":
		for _, u := range s.users {
			if u.Email == args[0].(string) && u.AuthSource == args[1].(string) {
				return raceRow{value: *u}
			}
		}
		return raceRow{err: pgx.ErrNoRows}
	case "CreateLDAPUser", "CreateOIDCUser":
		// email, display_name — active, role 'user', the source of the query.
		source := "ldap"
		if name == "CreateOIDCUser" {
			source = "oidc"
		}
		u := &db.User{
			ID: uuid.New(), Email: args[0].(string), DisplayName: args[1].(string),
			IsActive: true, Role: "user", AuthSource: source,
		}
		s.users[u.ID] = u
		remember(func() { delete(s.users, u.ID) })
		return raceRow{value: *u}
	case "UpdateLDAPUserProfile", "UpdateOIDCUserProfile":
		// id, display_name — only for an account of the query's own source, as the SQL's WHERE.
		source := "ldap"
		if name == "UpdateOIDCUserProfile" {
			source = "oidc"
		}
		u, ok := s.users[args[0].(uuid.UUID)]
		if !ok || u.AuthSource != source {
			return raceRow{err: pgx.ErrNoRows}
		}
		before := *u
		remember(func() { *u = before })
		u.DisplayName = args[1].(string)
		return raceRow{value: *u}
	case "UpdateUserProfile":
		// display_name, is_active, role, id — a PARTIAL update, as the SQL's COALESCE:
		// a field the caller left NULL keeps the value the row holds now.
		u, ok := s.users[args[3].(uuid.UUID)]
		if !ok {
			return raceRow{err: pgx.ErrNoRows}
		}
		before := *u
		remember(func() { *u = before })
		if v := args[0].(pgtype.Text); v.Valid {
			u.DisplayName = v.String
		}
		if v := args[1].(pgtype.Bool); v.Valid {
			u.IsActive = v.Bool
		}
		if v := args[2].(pgtype.Text); v.Valid {
			u.Role = v.String
		}
		return raceRow{value: *u}
	case "CreateSessionAtEpoch":
		// token_hash, user_agent, ip_address, expires_at, device_name, device_type,
		// device_id, user_role, user_id, epoch — and the SQL's WHERE: the user is
		// active and still holds the epoch the check read.
		u, ok := s.users[args[8].(uuid.UUID)]
		if !ok || !u.IsActive || u.AuthEpoch != args[9].(int64) {
			return raceRow{err: pgx.ErrNoRows}
		}
		sess := db.Session{
			ID: uuid.New(), UserID: u.ID, TokenHash: args[0].(string), UserAgent: args[1].(string),
			IpAddress: args[2].(string), ExpiresAt: args[3].(time.Time), DeviceName: args[4].(pgtype.Text),
			DeviceType: args[5].(pgtype.Text), DeviceID: args[6].(pgtype.Text), UserRole: args[7].(string),
			CreatedAt: time.Now(), LastUsedAt: time.Now(),
		}
		s.sessions = append(s.sessions, sess)
		return raceRow{value: sess}
	default:
		return raceRow{err: fmt.Errorf("unexpected QueryRow: %s", name)}
	}
}

func (s *epochStore) query(ctx context.Context, sql string, args []any, inTx bool) (pgx.Rows, error) {
	name, err := s.enter(ctx, sql, args, inTx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.afterHook(name)

	switch name {
	case "ListRecoveryCodes":
		var items []any
		for _, rc := range s.recoveryCodes[args[0].(uuid.UUID)] {
			items = append(items, rc)
		}
		return &raceRows{items: items}, nil
	case "ListUserSessions":
		id := args[0].(uuid.UUID)
		var items []any
		for _, sess := range s.sessions {
			if sess.UserID == id && !sess.IsRevoked && sess.ExpiresAt.After(time.Now()) {
				items = append(items, sess)
			}
		}
		return &raceRows{items: items}, nil
	default:
		return nil, fmt.Errorf("unexpected Query: %s", name)
	}
}

// epochConn is the pool: what h.queries and the SessionManager use.
type epochConn struct{ store *epochStore }

func (c epochConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return c.store.exec(ctx, sql, args, false, nil)
}

func (c epochConn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return c.store.query(ctx, sql, args, false)
}

func (c epochConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.store.queryRow(ctx, sql, args, false, nil)
}

// epochTx is a transaction. The embedded pgx.Tx is nil on purpose: a handler may use
// only what is implemented below, and anything else panics rather than quietly doing
// nothing. Its writes are undone, newest first, when it rolls back or its commit does
// not land.
type epochTx struct {
	pgx.Tx
	store *epochStore

	mu     sync.Mutex
	closed bool
	undo   []func()
}

func (t *epochTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.store.exec(ctx, sql, args, true, &t.undo)
}

func (t *epochTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.store.query(ctx, sql, args, true)
}

func (t *epochTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.store.queryRow(ctx, sql, args, true, &t.undo)
}

func (t *epochTx) undoWrites() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for i := len(t.undo) - 1; i >= 0; i-- {
		t.undo[i]()
	}
	t.undo = nil
}

func (t *epochTx) finish() {
	t.closed = true
	t.store.mu.Lock()
	t.store.openTx--
	t.store.mu.Unlock()
}

func (t *epochTx) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.finish()
	// A COMMIT whose context had already ended is never sent; pgconn says so twice,
	// which neverSentError reproduces.
	if err := ctx.Err(); err != nil {
		t.undoWrites()
		t.store.mu.Lock()
		t.store.rolledBack++
		t.store.mu.Unlock()
		return &neverSentError{err: err}
	}
	t.store.mu.Lock()
	err, lands := t.store.commitErr, t.store.commitLands
	t.store.mu.Unlock()
	if err != nil && !lands {
		t.undoWrites()
		t.store.mu.Lock()
		t.store.rolledBack++
		t.store.mu.Unlock()
		return err
	}
	t.store.mu.Lock()
	t.store.committed++
	t.undo = nil
	t.store.mu.Unlock()
	return err
}

func (t *epochTx) Rollback(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.finish()
	t.undoWrites()
	t.store.mu.Lock()
	t.store.rolledBack++
	t.store.mu.Unlock()
	return nil
}

// epochBeginner is the pool's Begin. A begun transaction holds a "connection" until
// it commits or rolls back, which is what poolWhileTx watches.
type epochBeginner struct {
	store    *epochStore
	beginErr error

	mu        sync.Mutex
	attempts  int
	txOptions []pgx.TxOptions
}

// BeginTx is what UserHandler's deactivation and Register begin their transactions
// with: the same transaction Begin hands out, with the options it was asked for
// recorded.
func (b *epochBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	b.mu.Lock()
	b.txOptions = append(b.txOptions, opts)
	b.mu.Unlock()
	return b.Begin(ctx)
}

// beginOptions are the options of every BeginTx so far, in order.
func (b *epochBeginner) beginOptions() []pgx.TxOptions {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]pgx.TxOptions(nil), b.txOptions...)
}

func (b *epochBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	b.mu.Lock()
	b.attempts++
	b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	b.store.mu.Lock()
	b.store.openTx++
	b.store.mu.Unlock()
	return &epochTx{store: b.store}, nil
}

func (b *epochBeginner) beginAttempts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts
}

// epochOptions are the knobs a few tests turn.
type epochOptions struct {
	dbTimeout       time.Duration // the handlers' bound on deciding database work; 0 is the production bound
	followUpTimeout time.Duration // and on each follow-up; 0 is the production bound
	totp            bool          // wire the TOTP handler into the auth handler
	noSessions      bool          // a UserHandler with no SessionManager
}

type epochApp struct {
	app   *fiber.App
	store *epochStore
	pool  *epochBeginner
	redis *miniredis.Miniredis
	rdb   *redis.Client
	jwt   *auth.JWTService

	auth  *AuthHandler
	totp  *TOTPHandler
	oidc  *OIDCHandler
	users *UserHandler

	// probe is what GET /probe runs; see newEpochApp.
	probe fiber.Handler
}

// The accounts the harness holds, and the fixed password of the local one.
const (
	epochLoginEmail = "alice@example.com"
	epochAdminEmail = "bob@example.com"
)

// epochUser is a local, active account whose password is racePassword.
func epochUser(t *testing.T, email string, epoch int64) db.User {
	t.Helper()
	return db.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: racePasswordHash(t),
		DisplayName:  "Example User",
		IsActive:     true,
		Role:         "user",
		AuthSource:   "local",
		AuthEpoch:    epoch,
	}
}

// epochErrorHandler turns a fiber error into the envelope the API answers with.
func epochErrorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}
	return c.Status(code).JSON(fiber.Map{"error": code, "message": message})
}

func newEpochApp(t *testing.T, store *epochStore, opts epochOptions) *epochApp {
	t.Helper()

	mr := miniredis.RunT(t)
	// No retries and a short dial, so that a test that closes Redis fails fast instead
	// of spending the client's defaults — a second and a half — per command.
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 300 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })

	queries := db.New(epochConn{store: store})
	pool := &epochBeginner{store: store}
	jwtSvc := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)
	sessions := auth.NewSessionManager(queries, rdb)

	ah := &AuthHandler{
		pool:            pool,
		queries:         queries,
		jwtService:      jwtSvc,
		sessionManager:  sessions,
		dbTimeout:       opts.dbTimeout,
		followUpTimeout: opts.followUpTimeout,
	}
	a := &epochApp{store: store, pool: pool, redis: mr, rdb: rdb, jwt: jwtSvc, auth: ah,
		probe: func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }}

	// The SSO handler is the part of the OIDC flow after the identity provider: the
	// exchange and the storage of its code. Callback itself needs a provider.
	a.oidc = &OIDCHandler{queries: queries, rdb: rdb}
	ah.SetOIDCHandler(a.oidc)

	if opts.totp {
		a.totp = &TOTPHandler{queries: queries, totpService: auth.NewTOTPService(totpTestKey), rdb: rdb,
			followUpTimeout: opts.followUpTimeout}
		ah.SetTOTPHandler(a.totp)
		a.totp.SetIssueTokensFn(ah.IssueTokens)
	}

	uh := &UserHandler{
		pool:            pool,
		queries:         queries,
		rbac:            auth.NewRBACEngine(queries, rdb),
		dbTimeout:       opts.dbTimeout,
		followUpTimeout: opts.followUpTimeout,
	}
	if !opts.noSessions {
		uh.sessions = sessions
	}
	a.users = uh

	app := fiber.New(fiber.Config{ErrorHandler: epochErrorHandler})
	// The caller of an admin route, as the auth middleware would have set it up: the
	// header names the acting account, and an admin role is what the stub engine
	// grants every permission to. X-Test-Acting-Role gives that caller another role
	// ("user"): a signed-in caller who is not an administrator, which an anonymous one
	// is not.
	app.Use(func(c fiber.Ctx) error {
		if v := c.Get("X-Test-Acting-User"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "bad X-Test-Acting-User")
			}
			role := c.Get("X-Test-Acting-Role")
			if role == "" {
				role = "admin"
			}
			c.Locals("user_id", id)
			c.Locals("role", role)
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Post("/auth/register", ah.Register)
	app.Post("/auth/login", withRequestParams(t, authLoginMirror(), nil, ah.Login))
	app.Post("/auth/oidc/token-exchange", withRequestParams(t, epochOIDCExchangeMirror(), nil, ah.OIDCTokenExchange))
	if a.totp != nil {
		app.Post("/auth/totp/verify-login", withRequestParams(t, totpVerifyLoginMirror(), nil, a.totp.VerifyLogin))
	}
	app.Put("/users/:id", withRequestParams(t, epochUserUpdateMirror(), []string{"id"}, uh.Update))
	// A route for the tests that drive a function the routes above do not reach — the
	// part of the SSO callback and of the directory login that run after the identity
	// provider or the directory has said yes. The test sets probe, then sends GET /probe.
	app.Get("/probe", func(c fiber.Ctx) error { return a.probe(c) })

	a.app = app
	return a
}

// The mirrors are local copies of the parts of these routes' declarations
// (internal/api/registry_auth.go, registry_users.go) that the handlers read, for
// the reason migrationListMirror gives.
func epochOIDCExchangeMirror() apischema.Properties {
	return apischema.Properties{
		"code": {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(256)},
	}
}

func epochUserUpdateMirror() apischema.Properties {
	return apischema.Properties{
		"id":           {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"display_name": {Type: apischema.String, Optional: true},
		"is_active":    {Type: apischema.Boolean, Optional: true},
		"role":         {Type: apischema.String, Optional: true},
	}
}

// send sends a request and fails the test on a hang rather than waiting for one.
func (a *epochApp) send(t *testing.T, method, path, body string, headers map[string]string, timeout time.Duration) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: timeout, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s did not answer (a hang, if that is the timeout): %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (a *epochApp) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	return a.send(t, http.MethodPost, path, body, nil, 20*time.Second)
}

// epochLoginBody is the body of a password login.
func epochLoginBody(email string) string {
	return `{"email":"` + email + `","password":"` + racePassword + `"}`
}

// epochRequireNothingIssued fails unless the response carries no session at all: no
// access token, no refresh cookie of any kind, and the body says why in a message.
// It is the property every refusal and every failure shares.
func epochRequireNothingIssued(t *testing.T, resp *http.Response, body map[string]any) {
	t.Helper()
	if tok, ok := body["access_token"]; ok && tok != "" {
		t.Errorf("an access token was issued for a session that was not created: %v", body)
	}
	if cookies := refreshCookies(resp); len(cookies) != 0 {
		t.Errorf("a refresh cookie was set for a session that was not created: %+v", cookies)
	}
	if resp.Header.Get("Authorization") != "" {
		t.Errorf("an Authorization header was set: %q", resp.Header.Get("Authorization"))
	}
}

// epochRequireNoLoginAudit fails if any authentication event was audited: a refused
// sign-in is not a login, and nothing records it as one.
func epochRequireNoLoginAudit(t *testing.T, store *epochStore) {
	t.Helper()
	for _, action := range store.auditActions() {
		switch action {
		case "login", "ldap_login", "oidc_login", "register":
			t.Errorf("a %q audit row was written for a session that was not created", action)
		}
	}
}
