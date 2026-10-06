package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	fiberrecover "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// POST /auth/change-password through the pieces that surround the handler: the per-IP
// cap in the middleware chain, the authentication middleware with a real API key behind
// it, the registry's declaration and error envelope, and the wiring registerAuth gives
// the handler. The handler's own rules are pinned in internal/api/handlers
// (password_lockout_test.go); here, that they are reached the way a request reaches
// them. That an API key is refused on every InteractiveOnly route is
// registry_interactive_only_test.go's subject; this file keeps the change-password
// instance, through the real handler.

const (
	chainPassword    = "Old-Passw0rd-Example!"
	chainNewPassword = "New-Passw0rd-Example!"
	chainEmail       = "alice@example.com"
)

var (
	chainWrongBody = fmt.Sprintf(`{"old_password":%q,"new_password":%q}`, chainPassword+"-wrong", chainNewPassword)
	chainRightBody = fmt.Sprintf(`{"old_password":%q,"new_password":%q}`, chainPassword, chainNewPassword)
)

// requireLoginsAuthBudget holds one route to the per-IP cap authLimitedPaths gives it:
// every spelling Fiber routes to it spends the auth budget, and it is the SAME budget as
// login's, so the routes cannot multiply what one address may try. newTestServer sets
// RateLimitMax=100 and the auth cap is 15, so a 429 inside this many requests can only
// have come from the auth limiter.
func requireLoginsAuthBudget(t *testing.T, route string, spellings []string) {
	t.Helper()
	const limit = 15

	t.Run("every spelling is capped", func(t *testing.T) {
		for _, path := range spellings {
			t.Run(path, func(t *testing.T) {
				s := newTestServer(t)
				var statuses []int
				for i := 0; i < limit+1; i++ {
					resp, err := s.app.Test(httptest.NewRequest(http.MethodPost, path, nil))
					if err != nil {
						t.Fatalf("Test: %v", err)
					}
					statuses = append(statuses, resp.StatusCode)
					_ = resp.Body.Close()
				}
				// No early return on 404: the limiter is app-level and counts a request
				// whether or not a route matches.
				for i, status := range statuses[:limit] {
					if status == http.StatusTooManyRequests {
						t.Errorf("request %d of %d was limited: the cap is %d a minute", i+1, limit+1, limit)
					}
				}
				if statuses[limit] != http.StatusTooManyRequests {
					t.Errorf("request %d = %d, want 429: %s passed the middleware stack more than %d times without hitting the auth cap (statuses %v)",
						limit+1, statuses[limit], path, limit, statuses)
				}
			})
		}
	})

	t.Run("it is login's budget", func(t *testing.T) {
		s := newTestServer(t)
		var statuses []int
		for i := 0; i < limit+1; i++ {
			// Eight of each is sixteen requests: over the cap only if the two paths draw
			// on one bucket.
			path := "/api/v1/auth/login"
			if i%2 == 1 {
				path = route
			}
			resp, err := s.app.Test(httptest.NewRequest(http.MethodPost, path, nil))
			if err != nil {
				t.Fatalf("Test: %v", err)
			}
			statuses = append(statuses, resp.StatusCode)
			_ = resp.Body.Close()
		}
		if statuses[limit] != http.StatusTooManyRequests || slices.Contains(statuses[:limit], http.StatusTooManyRequests) {
			t.Errorf("statuses = %v, want the first %d unlimited and the next 429: login and %s must share one bucket", statuses, limit, route)
		}
	})
}

// TestChangePasswordHasAPerIPCapThatLoginShares: before this route was listed in
// authLimitedPaths it had no per-IP cap at all (everything under /api/v1/auth/ is exempt
// from the general limiter), so a stolen token could guess the current password as fast
// as bcrypt allowed. Every spelling Fiber routes to it must spend the auth budget, and it
// is the SAME budget as login's: two oracles for one password get 15 guesses a minute
// between them, not 15 each.
func TestChangePasswordHasAPerIPCapThatLoginShares(t *testing.T) {
	requireLoginsAuthBudget(t, "/api/v1/auth/change-password", []string{
		"/api/v1/auth/change-password",
		"/api/v1/auth/change-password/",
		"/API/v1/auth/change-password",
		"/api/V1/Auth/Change-Password",
	})
}

// chainDB is the database behind these requests: an API key, a user, and the two writes
// a request here can reach. It records every statement by name and answers any other with
// an error, so a handler that reaches for something it should not has said so.
type chainDB struct {
	mu      sync.Mutex
	names   []string
	audits  []string
	keyHash string
	key     db.GetAPIKeyByHashRow
	user    db.User

	// stamped is closed by the first UpdateAPIKeyLastUsed, which authenticateAPIKey
	// sends from a goroutine of its own: waitForKeyStamp waits on it, and nothing
	// polls.
	stamped     chan struct{}
	stampedOnce sync.Once
}

func stmtName(sql string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(sql, "-- name: "), " ")
	return name
}

func (f *chainDB) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
}

func (f *chainDB) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.names)
}

func (f *chainDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	name := stmtName(sql)
	f.record(name)
	switch name {
	case "UpdateAPIKeyLastUsed":
		f.stampedOnce.Do(func() { close(f.stamped) })
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "InsertAuditLog":
		f.mu.Lock()
		f.audits = append(f.audits, args[4].(string))
		f.mu.Unlock()
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec %s", name)
}

func (f *chainDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	name := stmtName(sql)
	f.record(name)
	return nil, fmt.Errorf("unexpected Query %s", name)
}

func (f *chainDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	name := stmtName(sql)
	f.record(name)
	switch name {
	case "GetAPIKeyByHash":
		if args[0] == f.keyHash {
			return structRow{f.key}
		}
		return failedRow{pgx.ErrNoRows}
	case "GetUserByID":
		return structRow{f.user}
	}
	return failedRow{fmt.Errorf("unexpected QueryRow %s", name)}
}

// chain is the real authentication middleware, the real declaration of the auth
// endpoints and the real error handler in front of a real AuthHandler, with a fake
// database behind them.
type chain struct {
	app  *fiber.App
	db   *chainDB
	jwt  *auth.JWTService
	uid  uuid.UUID
	rdb  *redis.Client
	mr   *miniredis.Miniredis
	sess string // a valid access token for the user
	key  string // an API key for the user
}

// newChainDB is the fixtures a chain needs: a local user whose password is
// chainPassword, and an API key that belongs to them.
func newChainDB(t *testing.T) (fake *chainDB, uid uuid.UUID, apiKey string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(chainPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	uid = uuid.New()
	apiKey = "nxra_" + strings.Repeat("k", 43)
	fake = &chainDB{
		stamped: make(chan struct{}),
		keyHash: auth.HashToken(apiKey),
		key: db.GetAPIKeyByHashRow{
			ID: uuid.New(), UserID: uid, Name: "ci", KeyPrefix: apiKey[:12], KeyHash: auth.HashToken(apiKey),
			CreatedAt: time.Now(), UserEmail: chainEmail, UserRole: "admin", UserIsActive: true,
		},
		user: db.User{
			ID: uid, Email: chainEmail, PasswordHash: string(hash), DisplayName: "Alice", IsActive: true,
			Role: "admin", AuthSource: "local",
		},
	}
	return fake, uid, apiKey
}

// waitForKeyStamp waits for authenticateAPIKey's last-used stamp, which it writes from a
// goroutine, so that what a test reads of the statements afterwards is the whole of what
// a request that authenticated by key did. It waits on the stamp itself, not on the clock.
func (f *chainDB) waitForKeyStamp(t *testing.T) {
	t.Helper()
	select {
	case <-f.stamped:
	case <-time.After(10 * time.Second):
		t.Fatal("the API key's last-used stamp was never written")
	}
}

// newChain builds it. The handler is the one registerAuth builds, given the Redis the
// server was given.
func newChain(t *testing.T) *chain {
	t.Helper()
	fake, uid, apiKey := newChainDB(t)
	queries := db.New(fake)
	jwtSvc := auth.NewJWTService("chain-test-secret", 15*time.Minute, 7*24*time.Hour)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	s := &Server{queries: queries, jwtService: jwtSvc}
	s.registerAuth(&serverDeps{queries: queries, jwt: jwtSvc, sessionMgr: auth.NewSessionManager(queries, rdb), rdb: rdb})
	if s.authHandler == nil {
		t.Fatal("registerAuth built no auth handler")
	}

	reg := NewRegistry()
	registerAuthEndpoints(reg, s.authHandler, s.logoutAllLimiter(), s.sessionRevokeLimiter())
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	// This chain has no pool, so a request that gets as far as the handler's transaction
	// panics on it; recovered, that is a 500 a test asserts on, and not a crash.
	app.Use(fiberrecover.New())
	mountRegistry(app, reg, s.authRequired(), everyNodeIsAMember())

	token, _, err := jwtSvc.GenerateAccessToken(uid, chainEmail, "admin")
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	return &chain{app: app, db: fake, jwt: jwtSvc, uid: uid, rdb: rdb, mr: mr, sess: token, key: apiKey}
}

// change sends POST /auth/change-password with the bearer token and body.
func (c *chain) change(t *testing.T, bearer, body string) (*http.Response, ErrorResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+bearer)
	resp, err := c.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var env ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp, env
}

// TestChangePassword_AnAPIKeyIsRefusedThroughTheRealMiddleware pins the change-password
// instance of the InteractiveOnly rule through the real handler: a real nxra_ key, found
// by the real authRequired through GetAPIKeyByHash, on the registry registerAuthEndpoints
// fills. The answer is the gate's 403, and the database saw only the key's lookup and
// its last-used stamp (the handler never ran, so no password was checked and Redis
// counts no attempt). The control is the same request with an access token, which gets as
// far as the password and is told it is wrong.
func TestChangePassword_AnAPIKeyIsRefusedThroughTheRealMiddleware(t *testing.T) {
	for _, tc := range []struct{ name, body string }{{"the right password", chainRightBody}, {"a wrong password", chainWrongBody}} {
		t.Run(tc.name, func(t *testing.T) {
			c := newChain(t)

			resp, env := c.change(t, c.key, tc.body)

			if resp.StatusCode != http.StatusForbidden || env.Error != "forbidden" {
				t.Fatalf("status = %d, error = %q (%q), want 403 forbidden", resp.StatusCode, env.Error, env.Message)
			}
			if env.Message != interactiveOnlyMessage {
				t.Errorf("message = %q, want the registry's interactive-only message %q", env.Message, interactiveOnlyMessage)
			}
			c.db.waitForKeyStamp(t)
			if got := c.db.statements(); !slices.Equal(got, []string{"GetAPIKeyByHash", "UpdateAPIKeyLastUsed"}) {
				t.Errorf("statements = %v, want only the key's lookup and stamp: the handler must not have run", got)
			}
			if keys := c.mr.Keys(); len(keys) != 0 {
				t.Errorf("Redis holds %v: a refused API key must not count an attempt", keys)
			}
		})
	}

	t.Run("control: an access token is not refused for how it authenticated", func(t *testing.T) {
		c := newChain(t)
		resp, env := c.change(t, c.sess, chainWrongBody)
		if resp.StatusCode != http.StatusForbidden || env.Message != "Current password is incorrect" {
			t.Fatalf("status = %d (%q), want 403 Current password is incorrect: the request must reach the password check", resp.StatusCode, env.Message)
		}
		if got := c.db.statements(); !slices.Contains(got, "GetUserByID") {
			t.Errorf("statements = %v, want the account to have been read", got)
		}
	})
}

// TestChangePassword_TheLockoutIsServedThroughTheRealChain drives the lockout the way a
// client meets it: the real authentication middleware, the registry, the real error
// handler. Four wrong passwords are 403; the fifth is the lock, a 429 whose Retry-After
// survives the error handler and whose body is the API's error envelope; and the right
// password is then refused the same. The handler is the one registerAuth builds, so the
// Redis counts that appear here, and only these keys, prove registerAuth gave it the
// Redis the server was given: a handler left to its own memory would lock too, and
// write none.
func TestChangePassword_TheLockoutIsServedThroughTheRealChain(t *testing.T) {
	c := newChain(t)

	for i := 1; i < 5; i++ {
		resp, env := c.change(t, c.sess, chainWrongBody)
		// 403, not 401: the SPA's client takes every 401 for an expired access token,
		// refreshes the session and sends the request again, so a wrong password
		// answered 401 would be sent twice and count as two attempts.
		if resp.StatusCode != http.StatusForbidden || env.Error != "forbidden" || env.Message != "Current password is incorrect" {
			t.Fatalf("wrong attempt %d = %d, error %q (%q), want 403 forbidden: Current password is incorrect", i, resp.StatusCode, env.Error, env.Message)
		}
		// One request is one attempt: what the client sent once, the server counted once.
		if n, err := c.rdb.Get(context.Background(), "pwchange:user:fail:"+c.uid.String()).Int(); err != nil || n != i {
			t.Fatalf("the attempt counter is %d (%v) after %d wrong request(s), want %d", n, err, i, i)
		}
	}
	if got := c.mr.Keys(); !slices.Equal(got, []string{"pwchange:user:fail:" + c.uid.String()}) {
		t.Fatalf("Redis keys after four wrong attempts = %v, want only the account's attempt counter: registerAuth did not wire Redis to the handler", got)
	}

	resp, env := c.change(t, c.sess, chainWrongBody)
	if resp.StatusCode != http.StatusTooManyRequests || env.Error != "too_many_requests" {
		t.Fatalf("the fifth wrong attempt = %d, error %q (%q), want 429 too_many_requests", resp.StatusCode, env.Error, env.Message)
	}
	if got := resp.Header.Get("Retry-After"); got != "1800" {
		t.Errorf("Retry-After = %q, want 1800: the header must survive the error handler", got)
	}
	if ct := resp.Header.Get(fiber.HeaderContentType); !strings.HasPrefix(ct, fiber.MIMEApplicationJSON) {
		t.Errorf("Content-Type = %q, want the JSON error envelope", ct)
	}
	if !strings.Contains(env.Message, "30 more minutes") {
		t.Errorf("message = %q, want the time the lock has left", env.Message)
	}
	if got := c.mr.Keys(); !slices.Equal(got, []string{"pwchange:user:lock:" + c.uid.String()}) {
		t.Errorf("Redis keys while locked = %v, want only the lock", got)
	}

	// Locked: the right password, which would otherwise be answered by a transaction
	// this chain has no pool for, is refused first.
	resp, env = c.change(t, c.sess, chainRightBody)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "1800" {
		t.Errorf("the right password while locked = %d, Retry-After %q (%q), want 429 and 1800", resp.StatusCode, resp.Header.Get("Retry-After"), env.Message)
	}

	c.db.mu.Lock()
	audits := slices.Clone(c.db.audits)
	c.db.mu.Unlock()
	if !slices.Equal(audits, []string{"password_change_locked"}) {
		t.Errorf("audit rows = %v, want exactly one lockout row", audits)
	}
	if got := c.db.statements(); slices.Contains(got, "UpdatePassword") {
		t.Errorf("statements = %v: a locked account's password was written", got)
	}
}
