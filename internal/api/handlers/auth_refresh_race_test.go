package handlers

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The handler half of the refresh/sign-out race: the real Refresh and Logout through a
// real Fiber app, the real SessionManager over miniredis, and raceStore standing in for
// the sessions table. The store applies the predicates the SQL does (a lookup hits only on
// a matching, unrevoked hash; the rotation only on the session's current hash while it is
// live), so an argument in the wrong place shows up as a refusal here. That these are the
// predicates of queries/sessions.sql, and that the interleavings come out as claimed under
// READ COMMITTED, is pinned against Postgres in internal/db/session_rotation_db_test.go.

// raceStore is the sessions and users tables as the handler sees them: one session and its
// user. A knob named ...Err makes its statement fail with it.
type raceStore struct {
	authFakeDB

	session db.Session
	deleted bool // the session row is gone (its user was deleted: sessions cascade)

	user    db.User
	userErr error
	// userDeleted is the user row being gone: GetUserByID and the read that settles a COMMIT
	// find nothing, an UpdatePassword matches no row.
	userDeleted bool
	// afterCommitUserErr fails the read that settles a commit of unknown outcome
	// (GetPasswordHashForSettle) once a COMMIT has been attempted: the read-back failing too.
	afterCommitUserErr error

	permissions []db.GetUserPermissionsRow
	permsErr    error // GetUserPermissions
	listErr     error // ListUserSessions
	currentErr  error // GetSessionByTokenHash
	previousErr error // GetSessionByPreviousTokenHash

	// beforeRotate runs inside RotateSessionToken, ahead of its WHERE clause: a writer
	// landing in the gap between the refresh's validation and its rotation.
	beforeRotate func(*raceStore)
	rotateErr    error
	revokeErr    error // RevokeSession
	revokeAllErr error // RevokeAllUserSessions
	updatePwErr  error
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

func (s *raceStore) doExec(name string, args []any, remember func(func())) (pgconn.CommandTag, error) {
	switch name {
	case "RotateSessionToken":
		if s.rotateErr != nil {
			return pgconn.CommandTag{}, s.rotateErr
		}
		if s.beforeRotate != nil {
			s.beforeRotate(s)
		}
		// sqlc's argument order: new hash, role, id, then the hash the rotation is conditional on.
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
	case "BumpUserAuthEpoch":
		// A user that is gone matches no row.
		if id, ok := args[0].(uuid.UUID); ok && id == s.user.ID && !s.userDeleted {
			was := s.user.AuthEpoch
			remember(func() { s.user.AuthEpoch = was })
			s.user.AuthEpoch++
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	case "UpdatePassword":
		if s.updatePwErr != nil {
			return pgconn.CommandTag{}, s.updatePwErr
		}
		// sqlc's argument order: the new hash, the user's id, then the hash the update is
		// conditional on. An account that is gone, or whose password is no longer the one the
		// caller verified, matches no row.
		newHash, id, expected := args[0].(string), args[1].(uuid.UUID), args[2].(string)
		if s.userDeleted || id != s.user.ID || expected != s.user.PasswordHash {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		was := s.user.PasswordHash
		remember(func() { s.user.PasswordHash = was })
		s.user.PasswordHash = newHash
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "InsertAuditLog":
		return s.insertAudit(args)
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", name)
	}
}

func (s *raceStore) doQueryRow(name string, args []any, _ func(func())) pgx.Row {
	switch name {
	case "GetSessionByTokenHash":
		if s.currentErr != nil {
			return authFakeRow{err: s.currentErr}
		}
		if !s.deleted && !s.session.IsRevoked && args[0] == s.session.TokenHash {
			return authFakeRow{value: s.session}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "GetSessionByPreviousTokenHash":
		if s.previousErr != nil {
			return authFakeRow{err: s.previousErr}
		}
		window := time.Duration(args[1].(float64) * float64(time.Second))
		prev := s.session.PreviousTokenHash
		if !s.deleted && !s.session.IsRevoked && s.session.ExpiresAt.After(time.Now()) &&
			prev.Valid && args[0] == prev.String &&
			s.session.RotatedAt.Valid && time.Since(s.session.RotatedAt.Time) < window {
			return authFakeRow{value: s.session}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "GetUserByID":
		if s.userErr != nil {
			return authFakeRow{err: s.userErr}
		}
		if s.userDeleted {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		return authFakeRow{value: s.user}
	case "GetPasswordHashForSettle":
		// The locking read that settles a COMMIT of unknown outcome. There are no row locks
		// to wait on here (internal/db pins that the real one waits for the transaction the
		// server is finishing), so it answers what the committed row holds, or what a test
		// made it fail with.
		if s.commitAttempted && s.afterCommitUserErr != nil {
			return authFakeScalar{err: s.afterCommitUserErr}
		}
		if s.userDeleted {
			return authFakeScalar{err: pgx.ErrNoRows}
		}
		return authFakeScalar{value: s.user.PasswordHash}
	default:
		return authFakeRow{err: fmt.Errorf("unexpected QueryRow: %s", name)}
	}
}

// doQuery answers the :many statements, applying the SQL's own predicate: ListUserSessions
// returns the user's live sessions only.
func (s *raceStore) doQuery(name string, args []any) (pgx.Rows, error) {
	switch name {
	case "GetUserPermissions":
		if s.permsErr != nil {
			return nil, s.permsErr
		}
		items := make([]any, 0, len(s.permissions))
		for _, p := range s.permissions {
			items = append(items, p)
		}
		return &authFakeRows{items: items}, nil
	case "ListUserSessions":
		if s.listErr != nil {
			return nil, s.listErr
		}
		var items []any
		if id, ok := args[0].(uuid.UUID); ok && !s.deleted && id == s.session.UserID &&
			!s.session.IsRevoked && s.session.ExpiresAt.After(time.Now()) {
			items = append(items, s.session)
		}
		return &authFakeRows{items: items}, nil
	default:
		return nil, fmt.Errorf("unexpected Query: %s", name)
	}
}

const (
	// The session's current refresh token, what its cookie holds, and the one it had
	// before its last rotation.
	raceCurrentToken  = "current-refresh-token-value"
	racePreviousToken = "previous-refresh-token-value"
)

type authRaceApp struct {
	app     *fiber.App
	handler *AuthHandler
	store   *raceStore
	pool    *authFakePool
	gate    *authFakeGate
	redis   *miniredis.Miniredis
	rdb     *redis.Client
	jwt     *auth.JWTService

	// mirrored receives a value for each completed SET of a session's Redis row: a winning
	// refresh writes it from a goroutine of its own, after the response.
	mirrored chan struct{}
	// afterCtx is what the request's context looked like to the middleware that runs once a
	// handler has returned, by path (guarded by afterMu).
	afterMu  sync.Mutex
	afterCtx map[string]ctxAfterHandler
}

// ctxAfterHandler is the request context as seen once a handler has returned.
type ctxAfterHandler struct {
	err         error // its Err(): non-nil means a cancelled context was left behind
	hasDeadline bool  // it still carries the handler's bound
}

// raceOptions are the knobs a few tests turn; the zero value is the harness every other
// test uses.
type raceOptions struct {
	gateSize  int           // connections in the pool; 0 is a pool with no limit
	dbTimeout time.Duration // the handler's bound on its deciding database work; 0 is the production bound
	// followUpTimeout is the bound on each follow-up of a decision (the audit row, the
	// revoke of a refused account's session, the race check, the Redis cleanup).
	followUpTimeout time.Duration
	concurrent      bool // requests overlap, so the single-request connection accounting does not apply
}

// authMirrorTap signals on done each SET of a session's Redis row that went through.
type authMirrorTap struct{ done chan<- struct{} }

func (authMirrorTap) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h authMirrorTap) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && isSessionMirrorWrite(cmd) {
			select {
			case h.done <- struct{}{}:
			default:
			}
		}
		return err
	}
}

func (authMirrorTap) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// newAuthRaceApp wires Refresh and Logout over a store holding one live admin session that
// rotated a minute ago; tweak edits the store before the handler sees it. Every test that
// uses it gets one check for free, at its end: no request asked the pool for a connection
// while its own transaction still held one, and no transaction was left open.
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
	store.model = store
	if tweak != nil {
		tweak(store)
	}

	mirrored := make(chan struct{}, 16)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb.AddHook(authMirrorTap{done: mirrored}) // first added, so outermost: it fires after any hook a test adds
	t.Cleanup(func() { _ = rdb.Close() })

	var gate *authFakeGate
	if opts.gateSize > 0 {
		gate = authNewFakeGate(opts.gateSize)
	}
	queries := db.New(authFakeConn{store: &store.authFakeDB, gate: gate})
	pool := &authFakePool{store: &store.authFakeDB, gate: gate}
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

	app := fiber.New(fiber.Config{ErrorHandler: authErrorHandler})
	a := &authRaceApp{handler: handler, store: store, pool: pool, gate: gate, redis: mr, rdb: rdb, jwt: jwtSvc,
		mirrored: mirrored, afterCtx: map[string]ctxAfterHandler{}}
	// observe records the request's context once the handler has returned: a handler that
	// installs a bounded context for its helpers must put the old one back.
	observe := func(c fiber.Ctx) error {
		err := c.Next()
		_, hasDeadline := c.Context().Deadline()
		a.afterMu.Lock()
		a.afterCtx[c.Path()] = ctxAfterHandler{err: c.Context().Err(), hasDeadline: hasDeadline}
		a.afterMu.Unlock()
		return err
	}
	app.Post("/auth/refresh", observe, withRequestParams(t, authRefreshMirror(), nil, handler.Refresh))
	// authOptional (Logout) and authRequired (the self-service routes) set user_id in
	// production; the header stands in for both.
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
		t.Cleanup(func() {
			for _, complaint := range authConnectionComplaints(&store.authFakeDB) {
				t.Error(complaint)
			}
		})
	}
	return a
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
	// Waiting for the winner's Redis write keeps it from outliving the test, and checks it
	// still happens.
	if path == "/auth/refresh" && resp.StatusCode == http.StatusOK {
		a.awaitSessionRedisRow(t)
	}
	return resp
}

// postTimed sends a POST and reports how long the answer took. A request that does not
// answer within timeout fails the test: a hang is a failure here, never a wait.
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

// awaitSessionRedisRow waits for the winner's Redis write to complete.
func (a *authRaceApp) awaitSessionRedisRow(t *testing.T) {
	t.Helper()
	select {
	case <-a.mirrored:
	case <-time.After(5 * time.Second):
		t.Fatal("the winner's Redis row never appeared: the write happens after the response, but it must happen")
	}
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

// cookieDeleted reports whether a Set-Cookie tells the browser to drop the cookie: an
// empty value that is already expired or has a non-positive Max-Age.
func cookieDeleted(c *http.Cookie) bool {
	expired := c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now()))
	return c.Value == "" && expired
}

type cookieOutcome int

const (
	cookieNew       cookieOutcome = iota // Set-Cookie carries a fresh refresh token
	cookieCleared                        // Set-Cookie deletes the cookie
	cookieUntouched                      // no Set-Cookie for it at all
)

// authRequireCookie holds the response's refresh cookie to want.
func authRequireCookie(t *testing.T, resp *http.Response, want cookieOutcome) {
	t.Helper()
	cookies := refreshCookies(resp)
	switch want {
	case cookieNew:
		if len(cookies) != 1 || cookies[0].Value == "" || cookieDeleted(cookies[0]) {
			t.Errorf("Set-Cookie = %+v, want one fresh refresh cookie", cookies)
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

// authRequireStatus fails the test unless resp has the status, and returns its decoded body.
func authRequireStatus(t *testing.T, resp *http.Response, want int) map[string]any {
	t.Helper()
	body := decodeObject(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, want, body)
	}
	return body
}

// authRefusedAccounts are the three branches in which Refresh refuses an account and
// revokes its session.
var authRefusedAccounts = []struct {
	name   string
	tweak  func(*raceStore)
	reason string // what the log line of a revoke that failed gives as the reason
	audit  string // the audit action the refusal writes, if any
}{
	{"the user no longer exists", func(s *raceStore) { s.userErr = pgx.ErrNoRows }, "user not found", ""},
	{"the account is disabled", func(s *raceStore) { s.user.IsActive = false }, "account disabled", ""},
	{"the user's role changed", func(s *raceStore) { s.user.Role = "viewer" }, "role changed", "refresh_denied_role_changed"},
}

// raceWinner is the refresh token another request got to rotate to first.
const raceWinner = "the-winning-refreshs-token"

// raceRotatedAgo sets the session's last rotation d ago.
func raceRotatedAgo(d time.Duration) func(*raceStore) {
	return func(s *raceStore) {
		s.session.RotatedAt = pgtype.Timestamptz{Time: time.Now().Add(-d), Valid: true}
	}
}

// raceWinnerRotatesFirst is another refresh presenting this same cookie getting to the row
// first, in the gap between this refresh's validation and its rotation.
func raceWinnerRotatesFirst(s *raceStore) {
	s.beforeRotate = func(s *raceStore) {
		s.session.PreviousTokenHash = pgtype.Text{String: s.session.TokenHash, Valid: true}
		s.session.RotatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		s.session.TokenHash = auth.HashToken(raceWinner)
	}
}

// TestRefresh_RotationOutcomes drives Refresh through the writers that can land between its
// validation and its rotation, and through every shape of stale token, and pins what each
// costs. The first row is the positive control for every refusal below it. A refusal is a
// 401 with the cookie cleared, except the loser of a race to another refresh of a LIVE
// session (a token replaced within auth.ConcurrentRefreshTolerance): 409 refresh_superseded
// with the cookie LEFT ALONE, because the jar may already hold the winner's newer one. A
// lookup that could not be made is neither: 503, cookie and session untouched.
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
		// cookie is the refresh token the request carries; "" means the session's current one.
		cookie string

		wantStatus int
		// wantSlug is the envelope's error code, for the answers the handler writes itself.
		wantSlug   string
		wantCookie cookieOutcome
		// wantStored is the hash the session holds afterwards; "" means the hash of the
		// cookie the response set.
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
			// A session issued before migration 000055 has no role recorded; the rotation must
			// WRITE the user's role or it keeps passing the role-rotation guard for ever.
			name:       "a session issued before its role was recorded refreshes, and records it",
			tweak:      func(s *raceStore) { s.session.UserRole = "" },
			wantStatus: http.StatusOK, wantCookie: cookieNew, wantStored: "",
			wantRotation: true, wantCommitted: true,
		},

		// The writers that land between validation and rotation: refused at the rotation,
		// with the transaction open.
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
			// The loser's request reached the server after the winner had committed, carrying
			// the old cookie because the winner's response had not reached the browser yet.
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
			// The sign-out grace window is not a refresh's: the store DOES hold this token as the
			// previous one, inside that window, so a refresh honouring it would hand out a second cookie.
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
			cookie := cmp.Or(tt.cookie, raceCurrentToken)
			resp := a.post(t, "/auth/refresh", cookie, nil)
			body := authRequireStatus(t, resp, tt.wantStatus)

			if tt.wantSlug != "" {
				if body["error"] != tt.wantSlug {
					t.Errorf("error code = %v, want %q: a client retries on exactly this status and code (body %v)", body["error"], tt.wantSlug, body)
				}
				if msg, _ := body["message"].(string); msg == "" {
					t.Errorf("the %s answer carries no message: %v", tt.wantSlug, body)
				}
			}

			authRequireCookie(t, resp, tt.wantCookie)
			var newToken string
			if cookies := refreshCookies(resp); tt.wantCookie == cookieNew && len(cookies) == 1 {
				newToken = cookies[0].Value
				if newToken == raceCurrentToken {
					t.Error("the refresh set back the cookie it was given instead of rotating it")
				}
			}

			// A token pair comes back if and only if the refresh won, under any name: the whole
			// body is searched by VALUE for what looks like a JWT ("eyJ" is how every one here
			// begins), and a refusal must not be shaped like a success either.
			_, hasAccess := body["access_token"]
			won := tt.wantStatus == http.StatusOK
			if hasAccess != won {
				t.Errorf("response carries access_token = %t, want %t: %v", hasAccess, won, body)
			}
			raw, _ := json.Marshal(body)
			if hasJWT := strings.Contains(string(raw), "eyJ"); hasJWT != won {
				t.Errorf("the body carries what looks like a JWT = %t, want %t: %s", hasJWT, won, raw)
			}
			if !won && (strings.Contains(string(raw), "permissions") || strings.Contains(string(raw), "expires_at")) {
				t.Errorf("the refusal body is shaped like a success: %s", raw)
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
			if got := len(a.store.named("RevokeSession")) > 0; got != tt.wantRevoke {
				t.Errorf("handler revoked the session = %t, want %t", got, tt.wantRevoke)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, tt.wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, tt.wantAudits)
			}

			// Whether the refusal asked "was this a race?", and what it asked with: the hash
			// of the token and the TOLERANCE, never the sign-out window.
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
				if began := a.pool.txCount(); began != 0 {
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

// TestRefresh_ALookupThatCouldNotBeMadeIsLogged pins the other half of the 503: a refresh
// that answered 503 and changed nothing is otherwise invisible. The line names the session
// wherever it is known (the one lookup that fails before that is the exception) and carries
// neither the token nor its hash. The control is a refresh that wins, which logs none.
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

			authRequireStatus(t, a.post(t, "/auth/refresh", raceCurrentToken, nil), http.StatusServiceUnavailable)

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

	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	authRequireStatus(t, b.post(t, "/auth/refresh", raceCurrentToken, nil), http.StatusOK)
	if strings.Contains(quiet.String(), "lookup failed") || strings.Contains(quiet.String(), "transaction start failed") {
		t.Errorf("a healthy refresh logged a failed lookup: %q", quiet.String())
	}
}

// TestRefresh_ALostRaceIsLogged pins what an operator can see of a refresh that lost: a
// refresh refused at the rotation says so, with the session id, and one whose token a
// concurrent refresh replaced says that too. Together the two lines tell "lost to another
// refresh" from "refused because the session was revoked", which look alike from outside.
// Ordinary stale tokens log nothing; they are common. No row logs a token or a hash.
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
			cookie := cmp.Or(tt.cookie, raceCurrentToken)

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

// TestNewAuthHandler_AMissingPoolStaysMissing: stored unconditionally, a nil *pgxpool.Pool
// becomes a NON-nil interface holding a nil pointer, which makes Register's `h.pool == nil`
// guard false and the first Begin dereference nil. The second half is the control: a real
// pool is kept, so a constructor that never assigned would not pass the first half alone.
func TestNewAuthHandler_AMissingPoolStaysMissing(t *testing.T) {
	var none *pgxpool.Pool
	if h := NewAuthHandler(none, nil, nil, nil, nil, nil); h.pool != nil {
		t.Errorf("a nil *pgxpool.Pool became %T in the handler's pool field; it must stay a nil interface so `h.pool == nil` still fires", h.pool)
	}

	real := new(pgxpool.Pool) // never used, never dialled: only its identity is compared
	if h := NewAuthHandler(real, nil, nil, nil, nil, nil); h.pool != txBeginner(real) {
		t.Errorf("the handler's pool field = %v, want the pool it was given", h.pool)
	}
}

// TestLogout_FindsTheSessionByEitherToken pins which cookies end a session, and that the
// ownership check, the audit row, the cookie and the idempotent 200 behave the same for a
// match on the previous token as on the current one. One difference by design: the audit
// row of a previous-token match says so, and says only that (audit details are readable by
// every Viewer). The rows that revoke are the controls for the rows that do not. A lookup
// that could not be made is neither found nor missing: 503, nothing revoked, the cookie
// cleared like every answer but the 403 (a session that is someone else's keeps theirs).
func TestLogout_FindsTheSessionByEitherToken(t *testing.T) {
	window := auth.PreviousTokenRevocationWindow
	other := uuid.New()
	boom := errRaceTransient
	const marked = `{"matched":"previous_token"}`

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
		wantDetails        []string // the details of each audit row, in order
	}{
		{
			name: "the current token", token: raceCurrentToken,
			wantRev: true, want: http.StatusOK, wantAudits: []string{"logout"}, wantDetails: []string{"{}"},
		},
		{
			name: "the previous token, rotated well inside the window", token: racePreviousToken,
			tweak:   raceRotatedAgo(window / 4),
			wantRev: true, want: http.StatusOK, wantPreviousLookup: true, wantAudits: []string{"logout"}, wantDetails: []string{marked},
		},
		{
			name: "the previous token, rotated just inside the window", token: racePreviousToken,
			tweak:   raceRotatedAgo(window - 10*time.Second),
			wantRev: true, want: http.StatusOK, wantPreviousLookup: true, wantAudits: []string{"logout"}, wantDetails: []string{marked},
		},
		{
			name: "the previous token, rotated just outside the window", token: racePreviousToken,
			tweak:   raceRotatedAgo(window + 10*time.Second),
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
			// A first lookup that fell through to the second would hide this: the previous-token
			// lookup would have found the session. 503, nothing revoked, the second never sent.
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
			body := authRequireStatus(t, resp, tt.want)

			if tt.want == http.StatusServiceUnavailable {
				checkLogoutUnconfirmed(t, body)
			}
			// Cleared whatever happened, the 503 included, except on the 403.
			if tt.want == http.StatusForbidden {
				authRequireCookie(t, resp, cookieUntouched)
			} else {
				authRequireCookie(t, resp, cookieCleared)
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

// TestLogout_ALookupThatCouldNotBeMadeIsLogged pins the log half of the 503 that
// TestLogout_FindsTheSessionByEitherToken pins the answer of: the one outcome nobody could
// otherwise find after the fact, for both lookups. The control is a lookup that finds
// nothing, which is NOT logged as a failure.
func TestLogout_ALookupThatCouldNotBeMadeIsLogged(t *testing.T) {
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

			authRequireStatus(t, a.post(t, "/auth/logout", racePreviousToken, nil), http.StatusServiceUnavailable)

			out := logs.String()
			if !strings.Contains(out, "logout: session lookup failed") {
				t.Errorf("the failed lookup left no trace in the log: %q", out)
			}
			if strings.Contains(out, racePreviousToken) || strings.Contains(out, auth.HashToken(racePreviousToken)) {
				t.Errorf("the log line carries the token or its hash: %q", out)
			}
		})
	}

	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	authRequireStatus(t, b.post(t, "/auth/logout", "a-token-nobody-issued", nil), http.StatusOK)
	if strings.Contains(quiet.String(), "logout: session lookup failed") {
		t.Errorf("an unknown token was logged as a failed lookup: %q", quiet.String())
	}
}

// TestLogout_AMatchOnThePreviousTokenIsLogged pins the other half of the audit marker: a
// sign-out that ended its session by the token the session had one rotation ago logs it,
// with the session and user ids and nothing that could be replayed. A sign-out by the
// current token logs no such line.
func TestLogout_AMatchOnThePreviousTokenIsLogged(t *testing.T) {
	const line = "revoked a session by the token it had before its last rotation"

	logs := captureProductionLog(t)
	a := newAuthRaceApp(t, nil)
	authRequireStatus(t, a.post(t, "/auth/logout", racePreviousToken, nil), http.StatusOK)
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

	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	authRequireStatus(t, b.post(t, "/auth/logout", raceCurrentToken, nil), http.StatusOK)
	if got := len(b.store.named("RevokeSession")); got != 1 {
		t.Fatalf("control: RevokeSession sent %d times, want 1: the control did not end a session", got)
	}
	if strings.Contains(quiet.String(), line) {
		t.Errorf("a sign-out by the current token was logged as a previous-token match: %q", quiet.String())
	}
}

// TestAnAbsurdlyLongRefreshTokenIsNoToken pins the bound Refresh and Logout put on the
// token they look at, for the way in that no schema covers (the cookie, and the body behind
// this harness's mirror schema): longer than MaxRefreshTokenLength CHARACTERS, as the
// schema counts, it is treated as no token, before anything is hashed, looked up or logged.
// Refresh then answers the same 401 as for a token no session holds, so a prober learns
// nothing from the difference; Logout still answers 200 and clears the cookie. The rows at
// the bound are the controls for the rows over it: that token IS looked up.
func TestAnAbsurdlyLongRefreshTokenIsNoToken(t *testing.T) {
	const bound = MaxRefreshTokenLength
	if bound != 1024 { // what docs/api-reference.md tells API clients, and the route's schema
		t.Fatalf("MaxRefreshTokenLength = %d; the documented bound is 1024 characters", bound)
	}
	// A recognisable token of n characters, so that the log check can look for it.
	token := func(n int) string {
		const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
		return strings.Repeat(alphabet, n/len(alphabet)+1)[:n]
	}

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

	for _, route := range []string{"/auth/refresh", "/auth/logout"} {
		for _, tt := range tests {
			t.Run(route+" "+tt.name, func(t *testing.T) {
				logs := captureProductionLog(t)
				a := newAuthRaceApp(t, nil)
				reqBody, cookie := "{}", tt.token
				if tt.viaBody {
					reqBody, cookie = `{"refresh_token":"`+tt.token+`"}`, ""
				}

				resp := a.postBody(t, route, reqBody, cookie, nil)

				authRequireCookie(t, resp, cookieCleared)
				if looked := len(a.store.stmts) > 0; looked != tt.wantLookup {
					t.Errorf("statements sent = %d, want a lookup = %t: a token over the bound must reach no query, one at it must", len(a.store.stmts), tt.wantLookup)
				}
				if n := len(a.store.named("RotateSessionToken")) + len(a.store.named("RevokeSession")); n != 0 {
					t.Errorf("a token that belongs to no session changed one (%d writes)", n)
				}
				if route == "/auth/logout" {
					authRequireStatus(t, resp, http.StatusOK)
					return
				}
				got := authRequireStatus(t, resp, http.StatusUnauthorized)
				if msg := got["message"]; msg != "Invalid or expired refresh token" {
					t.Errorf("message = %v, want the one every stale token gets", msg)
				}
				if _, issued := got["access_token"]; issued {
					t.Errorf("a refused refresh issued an access token: %v", got)
				}
				if n := a.pool.txCount(); n != 0 {
					t.Errorf("a refresh that was refused began %d transactions", n)
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
}

// TestCurrentSessionID_NeverAcceptsThePreviousToken pins that the session list's is_current,
// which resolves the caller's own session through the refresh cookie, finds it only by the
// CURRENT token: the token the session had one rotation ago, which FindSessionForLogout
// does match inside the sign-out window, resolves to nothing, as an unknown token does. The
// first row is the control, and the previous-token row checks that FindSessionForLogout
// WOULD match the cookie in this store, so "nothing" is the refusal and not a dead token.
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
