package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These tests need no database, so they run everywhere. What a statement MEANS
// to Postgres — which interleavings of a refresh and a revoke end with which
// rows — is pinned against a real one in internal/db/session_rotation_db_test.go,
// which skips without NEXARA_TEST_DB_URL. What is pinned here is the Go half: how
// a statement's answer becomes a decision, and which statements each entry point
// is allowed to send.

// sessionStatement is one statement a test double saw, with its arguments.
type sessionStatement struct {
	sql  string
	args []any
}

// fakeSessionDB is a db.DBTX that answers only the session statements the
// functions under test send, and fails anything else so a new statement cannot
// slip in unnoticed.
//
// The two lookups hit when their hash argument matches the row they were given,
// which is how Postgres would behave; it does NOT evaluate the revoked flag or
// the window, which is the SQL's job and is tested there.
type fakeSessionDB struct {
	mu sync.Mutex

	current     *db.Session // what GetSessionByTokenHash finds, by TokenHash
	currentErr  error       // if set, GetSessionByTokenHash fails with it
	previous    *db.Session // what GetSessionByPreviousTokenHash finds, by PreviousTokenHash
	previousErr error       // if set, GetSessionByPreviousTokenHash fails with it
	rotateRows  int64       // rows RotateSessionToken reports changing
	rotateErr   error       // if set, RotateSessionToken fails with it

	listed       []db.Session // what ListUserSessions returns
	listErr      error        // if set, ListUserSessions fails with it
	revokeAllErr error        // if set, RevokeAllUserSessions fails with it
	bumpErr      error        // if set, BumpUserAuthEpoch fails with it

	// created is what CreateSessionAtEpoch returns, and createErr, if set, what it
	// fails with instead: pgx.ErrNoRows is the refusal (the insert's WHERE matched
	// nothing), anything else is the database failing.
	created   db.Session
	createErr error

	calls []sessionStatement
}

func (f *fakeSessionDB) record(sql string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionStatement{sql: sql, args: args})
}

// named returns the statements sent for the query with this sqlc name.
func (f *fakeSessionDB) named(name string) []sessionStatement {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sessionStatement
	for _, c := range f.calls {
		if strings.Contains(c.sql, "-- name: "+name+" ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeSessionDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.record(sql, args)
	if strings.Contains(sql, "-- name: RotateSessionToken ") {
		if f.rotateErr != nil {
			return pgconn.CommandTag{}, f.rotateErr
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.rotateRows)), nil
	}
	if strings.Contains(sql, "-- name: BumpUserAuthEpoch ") {
		if f.bumpErr != nil {
			return pgconn.CommandTag{}, f.bumpErr
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	if strings.Contains(sql, "-- name: RevokeAllUserSessions ") {
		if f.revokeAllErr != nil {
			return pgconn.CommandTag{}, f.revokeAllErr
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", sql)
}

func (f *fakeSessionDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.record(sql, args)
	if strings.Contains(sql, "-- name: ListUserSessions ") {
		if f.listErr != nil {
			return nil, f.listErr
		}
		return &sessionRows{sessions: f.listed}, nil
	}
	return nil, fmt.Errorf("unexpected Query: %s", sql)
}

func (f *fakeSessionDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.record(sql, args)
	switch {
	case strings.Contains(sql, "-- name: CreateSessionAtEpoch "):
		if f.createErr != nil {
			return sessionRow{err: f.createErr}
		}
		return sessionRow{session: f.created}
	case strings.Contains(sql, "-- name: GetSessionByTokenHash "):
		if f.currentErr != nil {
			return sessionRow{err: f.currentErr}
		}
		if f.current != nil && args[0] == f.current.TokenHash {
			return sessionRow{session: *f.current}
		}
		return sessionRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "-- name: GetSessionByPreviousTokenHash "):
		if f.previousErr != nil {
			return sessionRow{err: f.previousErr}
		}
		if f.previous != nil && args[0] == f.previous.PreviousTokenHash.String {
			return sessionRow{session: *f.previous}
		}
		return sessionRow{err: pgx.ErrNoRows}
	}
	return sessionRow{err: fmt.Errorf("unexpected QueryRow: %s", sql)}
}

// sessionRow replays a db.Session — or an error — through pgx.Row, assigning
// positionally in the generated struct's field order, which is the order the
// sessions SELECTs scan in. A column added in the middle reports a type mismatch
// rather than shifting a value into the wrong field.
type sessionRow struct {
	session db.Session
	err     error
}

func (r sessionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	row := reflect.ValueOf(r.session)
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

// sessionRows replays db.Sessions through pgx.Rows, positionally like sessionRow.
// The embedded pgx.Rows is nil on purpose: the generated :many code uses only what
// is implemented here, and anything else panics.
type sessionRows struct {
	pgx.Rows
	sessions []db.Session
	pos      int
}

func (r *sessionRows) Next() bool {
	if r.pos >= len(r.sessions) {
		return false
	}
	r.pos++
	return true
}

func (r *sessionRows) Scan(dest ...any) error {
	return sessionRow{session: r.sessions[r.pos-1]}.Scan(dest...)
}

func (r *sessionRows) Close()     {}
func (r *sessionRows) Err() error { return nil }

// rotatedSession is a live session that a refresh rotated a minute ago:
// presenting currentToken is the session's own cookie, previousToken is the
// cookie it had before.
func rotatedSession(currentToken, previousToken string) db.Session {
	return db.Session{
		ID:                uuid.New(),
		UserID:            uuid.New(),
		TokenHash:         HashToken(currentToken),
		ExpiresAt:         time.Now().Add(time.Hour),
		UserRole:          "admin",
		PreviousTokenHash: pgtype.Text{String: HashToken(previousToken), Valid: true},
		RotatedAt:         pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}
}

// TestRotateRefreshToken pins how the rotation's row count becomes a decision,
// and what it asks Postgres to rotate.
//
// The refusal row is the one the handler's 401 hangs on: RotateSessionToken
// reports 0 rows when the session was revoked, expired, or rotated by another
// refresh between validation and now, and that has to come back as
// ErrInvalidToken rather than as success. A database FAILURE must not be
// dressed as that refusal — the handler answers the two differently (a refusal
// against a 500 and a cookie left alone).
func TestRotateRefreshToken(t *testing.T) {
	boom := errors.New("connection reset by peer")

	tests := []struct {
		name       string
		rows       int64
		execErr    error
		wantErr    bool
		wantTarget error // errors.Is target, when wantErr
		notTarget  error // errors.Is must be false for this, when wantErr
	}{
		{name: "one row changed: rotated", rows: 1},
		{name: "no row changed: refused as an invalid token", rows: 0, wantErr: true, wantTarget: ErrInvalidToken},
		{name: "database failure: an error, not a refusal", execErr: boom, wantErr: true, wantTarget: boom, notTarget: ErrInvalidToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSessionDB{rotateRows: tt.rows, rotateErr: tt.execErr}
			session := rotatedSession("current-refresh-token", "previous-refresh-token")

			err := RotateRefreshToken(context.Background(), db.New(fake), session, "new-token-hash", "viewer")

			if (err != nil) != tt.wantErr {
				t.Fatalf("RotateRefreshToken() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, tt.wantTarget) {
					t.Errorf("error = %v, want one wrapping %v", err, tt.wantTarget)
				}
				if tt.notTarget != nil && errors.Is(err, tt.notTarget) {
					t.Errorf("error = %v must not read as %v", err, tt.notTarget)
				}
			}

			calls := fake.named("RotateSessionToken")
			if len(calls) != 1 {
				t.Fatalf("RotateSessionToken sent %d times, want exactly 1", len(calls))
			}
			// sqlc numbers the arguments in the order they first appear in the
			// statement: new hash, role, id, then the hash the rotation is
			// conditional on. A swapped pair would rotate to the OLD hash, or
			// condition on the NEW one and refuse every refresh.
			want := []any{"new-token-hash", "viewer", session.ID, session.TokenHash}
			if !reflect.DeepEqual(calls[0].args, want) {
				t.Errorf("rotation args = %v, want %v (new hash, role, id, the validated hash)", calls[0].args, want)
			}
		})
	}
}

// TestValidateRefreshToken_ThreeAnswers pins the rule every caller depends on: a
// lookup that FAILS is not an invalid token. ErrInvalidToken means "no live
// session holds this token" — no row, revoked, or expired — and nothing else may
// come back as it. Read the other way, a refresh would clear a good cookie and
// sign the user out over a database blip, and a sign-out would report itself done
// without having looked.
//
// The control is the first row: the same double, answering the lookup, validates.
func TestValidateRefreshToken_ThreeAnswers(t *testing.T) {
	boom := errors.New("connection reset by peer")
	live := rotatedSession("current-refresh-token", "previous-refresh-token")
	revoked := live
	revoked.IsRevoked = true
	expired := live
	expired.ExpiresAt = time.Now().Add(-time.Hour)

	tests := []struct {
		name       string
		fake       *fakeSessionDB
		wantFound  bool
		wantTarget error // errors.Is target, when not found
		notTarget  error // errors.Is must be false for this, when not found
	}{
		{name: "a live session", fake: &fakeSessionDB{current: &live}, wantFound: true},
		{name: "no row", fake: &fakeSessionDB{}, wantTarget: ErrInvalidToken},
		{name: "a revoked session", fake: &fakeSessionDB{current: &revoked}, wantTarget: ErrInvalidToken},
		{name: "an expired session", fake: &fakeSessionDB{current: &expired}, wantTarget: ErrInvalidToken},
		{
			name: "the lookup fails", fake: &fakeSessionDB{currentErr: boom},
			wantTarget: boom, notTarget: ErrInvalidToken,
		},
		{
			name: "the lookup fails the way a cancelled request's does", fake: &fakeSessionDB{currentErr: context.Canceled},
			wantTarget: context.Canceled, notTarget: ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSessionManager(db.New(tt.fake), nil)

			got, err := sm.ValidateRefreshToken(context.Background(), "current-refresh-token")

			if tt.wantFound {
				if err != nil || got.ID != live.ID {
					t.Fatalf("ValidateRefreshToken = %v, %v, want the session", got.ID, err)
				}
				return
			}
			if !errors.Is(err, tt.wantTarget) {
				t.Fatalf("ValidateRefreshToken error = %v, want one wrapping %v", err, tt.wantTarget)
			}
			if tt.notTarget != nil && errors.Is(err, tt.notTarget) {
				t.Errorf("a failed lookup read as %v, which callers treat as 'no such session': %v", tt.notTarget, err)
			}
		})
	}
}

// TestFindSessionForLogout pins the lookup a sign-out uses: the session's
// current token, and the one it had before its last rotation — and nothing else.
//
// "previous token" is the race this exists for: a sign-out sent while a refresh
// was in flight carries the cookie the refresh was about to replace. Without the
// second lookup that sign-out found no session, revoked nothing and answered
// success. The window itself is a predicate in the SQL and is exercised against
// Postgres; here what matters is that the lookup is made, that it is made with
// the HASH of the token and the named window, that the answer says which token
// matched, that a session which expired since is not revoked — and that a lookup
// which could not be made is reported as that, from EITHER of the two, instead of
// falling through to "no session".
func TestFindSessionForLogout(t *testing.T) {
	const current, previous = "current-refresh-token", "previous-refresh-token"
	infra := errors.New("connection reset by peer")

	expired := rotatedSession(current, previous)
	expired.ExpiresAt = time.Now().Add(-time.Minute)

	tests := []struct {
		name         string
		token        string
		session      db.Session
		currentErr   error
		previousErr  error
		wantFound    bool
		wantVia      bool  // viaPrevious, when found
		wantTarget   error // errors.Is target, when not found
		notTarget    error // errors.Is must be false for this, when not found
		wantPrevious bool  // whether the previous-token lookup should have been sent
	}{
		{name: "the current token", token: current, session: rotatedSession(current, previous), wantFound: true},
		{name: "the previous token", token: previous, session: rotatedSession(current, previous), wantFound: true, wantVia: true, wantPrevious: true},
		{name: "a token the session never had", token: "someone-elses-token", session: rotatedSession(current, previous), wantTarget: ErrInvalidToken, wantPrevious: true},
		{name: "the previous token of a session that has since expired", token: previous, session: expired, wantTarget: ErrInvalidToken, wantPrevious: true},
		{
			name: "a failing previous-token lookup is an error, not 'no session'", token: previous,
			session: rotatedSession(current, previous), previousErr: infra,
			wantTarget: infra, notTarget: ErrInvalidToken, wantPrevious: true,
		},
		{
			// The case a first lookup that "fell through" would turn into a silent
			// no-session: the current lookup FAILS while the previous one would
			// have answered. The answer must be the failure, and the second
			// lookup must not even be sent.
			name: "a failing current-token lookup is an error, and does not fall through", token: previous,
			session: rotatedSession(current, previous), currentErr: infra,
			wantTarget: infra, notTarget: ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := tt.session
			fake := &fakeSessionDB{current: &session, currentErr: tt.currentErr, previous: &session, previousErr: tt.previousErr}
			sm := NewSessionManager(db.New(fake), nil)

			got, via, err := sm.FindSessionForLogout(context.Background(), tt.token)

			if tt.wantFound {
				if err != nil {
					t.Fatalf("FindSessionForLogout() error = %v, want the session", err)
				}
				if got.ID != session.ID {
					t.Errorf("found session %v, want %v", got.ID, session.ID)
				}
				if via != tt.wantVia {
					t.Errorf("viaPrevious = %t, want %t", via, tt.wantVia)
				}
			} else {
				if !errors.Is(err, tt.wantTarget) {
					t.Fatalf("FindSessionForLogout() error = %v, want one wrapping %v", err, tt.wantTarget)
				}
				if tt.notTarget != nil && errors.Is(err, tt.notTarget) {
					t.Errorf("error = %v must not read as %v, which callers treat as 'nothing to revoke'", err, tt.notTarget)
				}
				if via {
					t.Error("viaPrevious is true with no session found")
				}
			}

			prev := fake.named("GetSessionByPreviousTokenHash")
			if tt.wantPrevious && len(prev) != 1 {
				t.Fatalf("previous-token lookup sent %d times, want 1", len(prev))
			}
			if !tt.wantPrevious && len(prev) != 0 {
				t.Errorf("previous-token lookup sent %d times, want 0", len(prev))
			}
			if len(prev) == 1 {
				// The hash, never the raw token — and the named window, not a
				// literal that could drift from the constant's rationale.
				want := []any{HashToken(tt.token), PreviousTokenRevocationWindow.Seconds()}
				if !reflect.DeepEqual(prev[0].args, want) {
					t.Errorf("previous-token lookup args = %v, want %v (hash of the token, the window in seconds)", prev[0].args, want)
				}
			}
		})
	}
}

// TestValidateRefreshTokenNeverMatchesThePreviousToken is the half of the rule
// that makes the grace window safe: the lookup behind Refresh and behind the
// session list's is_current must not honour a rotated-away token, however
// recently it was rotated. If it did, every token a session ever had would stay
// a valid credential for the length of the window.
//
// The positive control is FindSessionForLogout on the SAME double: it finds the
// session by the previous token, so the double really does offer one — a
// ValidateRefreshToken that did not fall back would otherwise pass because there
// was nothing to fall back to.
func TestValidateRefreshTokenNeverMatchesThePreviousToken(t *testing.T) {
	const current, previous = "current-refresh-token", "previous-refresh-token"
	session := rotatedSession(current, previous)
	fake := &fakeSessionDB{current: &session, previous: &session}
	sm := NewSessionManager(db.New(fake), nil)

	if _, _, err := sm.FindSessionForLogout(context.Background(), previous); err != nil {
		t.Fatalf("control: FindSessionForLogout(previous) = %v; the double offers no previous-token match, so this test proves nothing", err)
	}
	fake.calls = nil

	if _, err := sm.ValidateRefreshToken(context.Background(), previous); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ValidateRefreshToken(previous) error = %v, want ErrInvalidToken: a rotated-away token must not authenticate a refresh", err)
	}
	if n := len(fake.named("GetSessionByPreviousTokenHash")); n != 0 {
		t.Errorf("ValidateRefreshToken sent the previous-token lookup %d times; it may only ever read the current hash", n)
	}

	if _, err := sm.ValidateRefreshToken(context.Background(), current); err != nil {
		t.Errorf("ValidateRefreshToken(current) error = %v, want the session", err)
	}
}

// captureDefaultLog routes the default slog logger to a buffer for the test.
// Sequential tests only: it swaps a process-wide logger.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return &buf
}

// TestRefusalSparesCookie pins the one question it answers — does this refusal
// leave the cookie alone — and, as much, what it is NOT allowed to be.
//
// It says yes only for a live session's immediate predecessor token. Every other
// shape of stale token — no session at all, an expired session, a lookup that
// failed — says no, which is the 401 that clears the cookie. The lookup that
// fails is the one worth a row of its own, because it is the only place in this
// file where "could not look" is allowed to read as "no": the caller already
// knows the token is not current, so a refusal is certain and the conservative
// one wins. It is logged.
//
// The window handed to the query is the short tolerance, never the logout
// window: a mix-up would make a token rotated a minute ago read as "just
// replaced". The match is never a session: the method returns a bool.
func TestRefusalSparesCookie(t *testing.T) {
	const current, previous = "current-refresh-token", "previous-refresh-token"
	infra := errors.New("connection reset by peer")

	expired := rotatedSession(current, previous)
	expired.ExpiresAt = time.Now().Add(-time.Minute)

	tests := []struct {
		name        string
		token       string
		session     db.Session
		previousErr error
		want        bool
		wantLogged  bool
	}{
		{name: "the token a live session just replaced", token: previous, session: rotatedSession(current, previous), want: true},
		{name: "the session's own current token is not a predecessor", token: current, session: rotatedSession(current, previous)},
		{name: "a token no session holds", token: "someone-elses-token", session: rotatedSession(current, previous)},
		{name: "the replaced token of a session that has since expired", token: previous, session: expired},
		{name: "a lookup that fails refuses it as stale, and says so", token: previous, session: rotatedSession(current, previous), previousErr: infra, wantLogged: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLog(t)
			session := tt.session
			fake := &fakeSessionDB{current: &session, previous: &session, previousErr: tt.previousErr}
			sm := NewSessionManager(db.New(fake), nil)

			if got := sm.RefusalSparesCookie(context.Background(), tt.token); got != tt.want {
				t.Errorf("RefusalSparesCookie = %t, want %t", got, tt.want)
			}

			prev := fake.named("GetSessionByPreviousTokenHash")
			if len(prev) != 1 {
				t.Fatalf("previous-token lookup sent %d times, want 1", len(prev))
			}
			if want := []any{HashToken(tt.token), ConcurrentRefreshTolerance.Seconds()}; !reflect.DeepEqual(prev[0].args, want) {
				t.Errorf("lookup args = %v, want %v (hash of the token, the TOLERANCE in seconds)", prev[0].args, want)
			}
			if n := len(fake.named("GetSessionByTokenHash")); n != 0 {
				t.Errorf("sent the current-token lookup %d times; this decides a refusal, it never authenticates", n)
			}
			if logged := strings.Contains(logs.String(), "could not check whether the refused token was superseded"); logged != tt.wantLogged {
				t.Errorf("failure logged = %t, want %t (log: %q)", logged, tt.wantLogged, logs.String())
			}
			// A true answer is logged at Info with the session id; no other is.
			if info := strings.Contains(logs.String(), "answering 409 and sparing the cookie"); info != tt.want {
				t.Errorf("superseded refusal logged = %t, want %t (log: %q)", info, tt.want, logs.String())
			}
			if tt.want && !strings.Contains(logs.String(), session.ID.String()) {
				t.Errorf("the superseded line does not name the session %v: %q", session.ID, logs.String())
			}
			if strings.Contains(logs.String(), tt.token) || strings.Contains(logs.String(), HashToken(tt.token)) {
				t.Errorf("the log carries the token or its hash: %q", logs.String())
			}
		})
	}
}

// TestSessionWindowsAreBounded pins the SIZE of the two windows, which nothing
// else does: every other test is written relative to the constants, so a window
// of a day would pass them all.
//
// The sign-out grace only has to cover one sign-out request that left the browser
// carrying a cookie a refresh was replacing — a response and a request in flight
// — so it is minutes at most; the bound is the top of the range the design allows
// (see PreviousTokenRevocationWindow). The tolerance behind the 409 has to cover
// one in-flight race and answers only a question about the SHAPE of a refusal, so
// it is tens of seconds at most, and shorter than the grace — which is also
// checked, since the point of two constants is that they are not the same size.
func TestSessionWindowsAreBounded(t *testing.T) {
	if w := PreviousTokenRevocationWindow; w <= 0 || w > 5*time.Minute {
		t.Errorf("PreviousTokenRevocationWindow = %v, want 0 < window <= 5m: a rotated-away token may sign its session out for as "+
			"long as this lasts, and it only has to outlive a response in flight", w)
	}
	if tol := ConcurrentRefreshTolerance; tol <= 0 || tol > 30*time.Second {
		t.Errorf("ConcurrentRefreshTolerance = %v, want 0 < tolerance <= 30s: it only has to cover one in-flight race, and for "+
			"as long as it lasts a refusal of the replaced token reads differently from any other", tol)
	}
	if ConcurrentRefreshTolerance >= PreviousTokenRevocationWindow {
		t.Errorf("ConcurrentRefreshTolerance (%v) is not shorter than PreviousTokenRevocationWindow (%v); the tolerance is "+
			"deliberately the narrower of the two", ConcurrentRefreshTolerance, PreviousTokenRevocationWindow)
	}
}

// TestRevokeAllUserSessionsIn pins the revoke every sign-out-everywhere, password
// change and deactivation runs: three statements, in this order — bump the user's
// auth epoch, list the live sessions, revoke every session — all through the Queries
// it is handed (the transaction's, for a caller that holds one), saying which
// sessions were live so that their Redis rows can be deleted once the transaction
// has committed. It must send nothing anywhere else: the manager's own pool-bound
// queries and Redis are exactly what a caller holding a transaction must not touch.
//
// The ORDER is the property. The bump is first because it waits for the row lock of
// any sign-in in the middle of its insert, so the listing and the revoke that follow
// — each with a snapshot of its own — see every session created against the old
// epoch; bump after the revoke, or bump and revoke as one statement, leaves a session
// that committed in between live (see CreateSessionAtEpoch and
// TestRevokeAll_TheWrongShapesLeaveASessionLive against Postgres).
//
// A failure of any statement is returned wrapped, with no ids: the caller rolls
// back, and a list of sessions whose revoke did not happen must not be cleaned up.
// A failed bump stops everything — the revoke must not run on an epoch that did not
// move.
func TestRevokeAllUserSessionsIn(t *testing.T) {
	userID := uuid.New()
	boom := errors.New("connection reset by peer")
	first, second := rotatedSession("a", "b"), rotatedSession("c", "d")

	t.Run("it bumps the epoch, lists the live sessions, then revokes all, and returns the listed ids", func(t *testing.T) {
		tx := &fakeSessionDB{listed: []db.Session{first, second}}
		pool := &fakeSessionDB{}
		_ = NewSessionManager(db.New(pool), nil) // a manager over the pool exists; it must stay unused

		got, err := RevokeAllUserSessionsIn(context.Background(), db.New(tx), userID)

		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if want := []uuid.UUID{first.ID, second.ID}; !reflect.DeepEqual(got, want) {
			t.Errorf("ids = %v, want %v", got, want)
		}
		bump, list, revoke := tx.named("BumpUserAuthEpoch"), tx.named("ListUserSessions"), tx.named("RevokeAllUserSessions")
		if len(bump) != 1 || len(list) != 1 || len(revoke) != 1 || len(tx.calls) != 3 {
			t.Fatalf("the transaction saw %d statements (bump %d, list %d, revoke %d), want exactly the three",
				len(tx.calls), len(bump), len(list), len(revoke))
		}
		// Bump, then list, then revoke — by position, not by count.
		if tx.calls[0].sql != bump[0].sql {
			t.Error("the first statement is not the epoch bump: a session inserted while the revoke-all waited for its row lock would not be refused or revoked")
		}
		if tx.calls[1].sql != list[0].sql {
			t.Error("the listing is not second: the live sessions would be read before the bump has waited for the sign-ins in flight, or after they were ended")
		}
		if tx.calls[2].sql != revoke[0].sql {
			t.Error("the revoke is not last: a session committed between the revoke and the bump would stay live")
		}
		for _, c := range []sessionStatement{bump[0], list[0], revoke[0]} {
			if !reflect.DeepEqual(c.args, []any{userID}) {
				t.Errorf("statement args = %v, want the user id", c.args)
			}
		}
		if len(pool.calls) != 0 {
			t.Errorf("%d statements went to the pool", len(pool.calls))
		}
	})

	t.Run("a user with no live sessions still has the epoch bumped and every session revoked, and gets an empty list", func(t *testing.T) {
		tx := &fakeSessionDB{}

		got, err := RevokeAllUserSessionsIn(context.Background(), db.New(tx), userID)

		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got (%#v, %v), want an empty non-nil list and no error", got, err)
		}
		if len(tx.named("BumpUserAuthEpoch")) != 1 {
			t.Error("the epoch was not bumped: an in-flight sign-in would not be refused")
		}
		if len(tx.named("RevokeAllUserSessions")) != 1 {
			t.Error("the revoke was not sent")
		}
	})

	t.Run("a failed bump is an error, wrapped, and nothing else is sent", func(t *testing.T) {
		tx := &fakeSessionDB{listed: []db.Session{first}, bumpErr: boom}

		got, err := RevokeAllUserSessionsIn(context.Background(), db.New(tx), userID)

		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "auth epoch") {
			t.Errorf("error = %v, want one wrapping the cause and naming the epoch", err)
		}
		if got != nil {
			t.Errorf("ids = %v after a failure, want none", got)
		}
		if n := len(tx.named("ListUserSessions")) + len(tx.named("RevokeAllUserSessions")); n != 0 {
			t.Errorf("%d statements were sent after the bump failed: the revoke must not run on an epoch that did not move", n)
		}
	})

	t.Run("a failed listing is an error, wrapped, and nothing is revoked", func(t *testing.T) {
		tx := &fakeSessionDB{listErr: boom}

		got, err := RevokeAllUserSessionsIn(context.Background(), db.New(tx), userID)

		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "listing user sessions") {
			t.Errorf("error = %v, want one wrapping the cause and naming the listing", err)
		}
		if got != nil {
			t.Errorf("ids = %v after a failure, want none", got)
		}
		if n := len(tx.named("RevokeAllUserSessions")); n != 0 {
			t.Errorf("the revoke was sent %d times after the listing failed", n)
		}
	})

	t.Run("a failed revoke is an error, wrapped, with no ids to clean up", func(t *testing.T) {
		tx := &fakeSessionDB{listed: []db.Session{first}, revokeAllErr: boom}

		got, err := RevokeAllUserSessionsIn(context.Background(), db.New(tx), userID)

		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "revoking all sessions") {
			t.Errorf("error = %v, want one wrapping the cause and naming the revoke", err)
		}
		if got != nil {
			t.Errorf("ids = %v after a failed revoke, want none: the sessions are still live", got)
		}
	})
}

// countingHook counts the Redis commands of one name.
type countingHook struct {
	name string
	mu   sync.Mutex
	n    int
}

func (h *countingHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *countingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), h.name) {
			h.mu.Lock()
			h.n++
			h.mu.Unlock()
		}
		return next(ctx, cmd)
	}
}

func (h *countingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *countingHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

// TestForgetSessions pins the Redis cleanup that follows a committed revoke: the
// rows of exactly the sessions it is given are deleted, in ONE command however many
// there are (so one timeout bounds it, not one per session), nothing else is
// touched, and an empty list sends nothing at all.
func TestForgetSessions(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	dels := &countingHook{name: "del"}
	rdb.AddHook(dels)
	sm := NewSessionManager(db.New(&fakeSessionDB{}), rdb)

	a, b, other := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{a, b, other} {
		if err := mr.Set(redisKey(id.String()), "{}"); err != nil {
			t.Fatal(err)
		}
	}

	sm.ForgetSessions(context.Background(), nil)
	if dels.count() != 0 {
		t.Errorf("an empty list sent %d DEL commands", dels.count())
	}

	sm.ForgetSessions(context.Background(), []uuid.UUID{a, b})

	if dels.count() != 1 {
		t.Errorf("two sessions took %d DEL commands, want one", dels.count())
	}
	if mr.Exists(redisKey(a.String())) || mr.Exists(redisKey(b.String())) {
		t.Error("a session's row survived")
	}
	if !mr.Exists(redisKey(other.String())) {
		t.Error("a row that was not named was deleted")
	}
}
