package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// POST /auth/logout-all and DELETE /auth/sessions/:id are the two routes that end
// sessions, and each is capped per USER, on its own route: Server.logoutAllLimiter
// (5 a minute) and Server.sessionRevokeLimiter (30 a minute). Neither is in the
// per-address bucket login shares (authLimitedPaths).
//
// The difference is who can spend them. The per-address bucket runs before
// authentication, so anyone can drain it without a credential — from one address,
// or from every client behind a proxy while TRUSTED_PROXIES is unset — and
// sign-out everywhere is the owner's remedy for a stolen token: the request that
// must not be refused because a stranger sent fifteen logins. A route limiter runs
// after authRequired and the InteractiveOnly gate, so only the account's own
// authenticated sessions spend its bucket, and an API key — refused by the gate,
// which is ahead of the limiter — spends none. And ending one session is signing out
// everywhere one device at a time, so without a cap of its own the second route
// would be the way round the first.
//
// What these tests drive is everything real but the handlers: the whole app-level
// middleware stack (setupMiddleware, its per-address limiters included), the
// registry's own declaration of each route with its own limiter and gate, the real
// authRequired with real access tokens and a real nxra_ key, and the real error
// handler. A handler is a recorder that does what the real ones do on success — end
// the sessions and clear the refresh cookie — so that "a refused call revokes nothing
// and leaves the cookie in place" is something to count and to look for in the
// headers, not something to infer.

const (
	logoutAllURL    = "/api/v1/auth/logout-all"
	sessionRevokeID = "d4e5f6a7-0000-4000-8000-000000000066"
	sessionURL      = "/api/v1/auth/sessions/" + sessionRevokeID
)

// the two client addresses the harness can tell apart (X-Forwarded-For, honoured from
// the test connection, whose own address is the proxy)
const (
	addressA = "192.0.2.10"
	addressB = "192.0.2.20"
)

// endSessionsServer is that stack with the two routes mounted.
type endSessionsServer struct {
	app     *fiber.App
	owner   string // an access token of the owner of apiKey
	other   string // an access token of someone else
	apiKey  string // an nxra_ key of the owner
	ownerID uuid.UUID
	otherID uuid.UUID

	mu      sync.Mutex
	reached map[string]int // "route|user" -> how many times the recorder ran
}

func newEndSessionsServer(t *testing.T) *endSessionsServer {
	t.Helper()
	cfg := &config.Config{
		APIPort:             8080,
		LogLevel:            "info",
		CORSAllowOrigins:    "*",
		RateLimitMax:        100,
		RateLimitExpiration: time.Minute,
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     7 * 24 * time.Hour,
		CompressionEnabled:  true,
		// The test connection's own address is 0.0.0.0, so trusting it as the proxy lets
		// each request claim a client address of its own in X-Forwarded-For, which is how
		// the tests below send one user from two addresses.
		ProxyHeader:    fiber.HeaderXForwardedFor,
		TrustedProxies: []string{"0.0.0.0"},
	}
	fake, uid, apiKey := newChainDB(t)
	jwtSvc := auth.NewJWTService("session-limiters-test-secret", 15*time.Minute, 7*24*time.Hour)
	otherID := uuid.New()
	ownerToken, _, err := jwtSvc.GenerateAccessToken(uid, chainEmail, "admin")
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	otherToken, _, err := jwtSvc.GenerateAccessToken(otherID, "bob@example.com", "viewer")
	if err != nil {
		t.Fatalf("access token: %v", err)
	}

	s := &Server{config: cfg, queries: db.New(fake), jwtService: jwtSvc}
	s.app = fiber.New(buildFiberConfig(cfg))
	s.setupMiddleware()

	h := &endSessionsServer{
		app: s.app, owner: ownerToken, other: otherToken, apiKey: apiKey,
		ownerID: uid, otherID: otherID, reached: map[string]int{},
	}
	reg := NewRegistry()
	declared := declaredByKey(t)
	for _, route := range []string{"POST " + logoutAllURL, "DELETE /api/v1/auth/sessions/:id"} {
		e, ok := declared[route]
		if !ok {
			t.Fatalf("the registry does not declare %s", route)
		}
		if e.RateLimiter == nil {
			t.Fatalf("the registry declares no limiter on %s", route)
		}
		e.Handler = func(c fiber.Ctx, _ *apischema.Params) error {
			uid, _ := c.Locals("user_id").(uuid.UUID)
			h.mu.Lock()
			h.reached[route+"|"+uid.String()]++
			h.mu.Unlock()
			// What the real handlers do once the sessions are ended.
			c.Cookie(&fiber.Cookie{Name: "refresh_token", Value: "", MaxAge: -1, Path: handlers.RefreshCookiePath, HTTPOnly: true})
			return c.SendStatus(fiber.StatusNoContent)
		}
		reg.Register(e)
	}
	mountRegistry(s.app, reg, s.authRequired(), everyNodeIsAMember())
	return h
}

// call sends one request with the bearer, from the client address from ("" is the
// connection's own).
func (h *endSessionsServer) call(t *testing.T, method, path, bearer, from string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if bearer != "" {
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+bearer)
	}
	if from != "" {
		req.Header.Set(fiber.HeaderXForwardedFor, from)
	}
	resp, err := h.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (h *endSessionsServer) logoutAll(t *testing.T, bearer, from string) *http.Response {
	t.Helper()
	return h.call(t, http.MethodPost, logoutAllURL, bearer, from)
}

func (h *endSessionsServer) revokeSession(t *testing.T, bearer, from string) *http.Response {
	t.Helper()
	return h.call(t, http.MethodDelete, sessionURL, bearer, from)
}

func (h *endSessionsServer) reachedBy(route string, id uuid.UUID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reached[route+"|"+id.String()]
}

// requireServed holds what a call within its cap gets: 204, and the cookie cleared —
// the control that the recorder's Set-Cookie is visible in a response, which is what
// makes its absence from a refusal mean something.
func requireServed(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("%s = %d, want 204", what, resp.StatusCode)
	}
	if resp.Header.Get(fiber.HeaderSetCookie) == "" {
		t.Fatalf("%s carries no Set-Cookie: the recorder's cookie is not visible, so the refusals below prove nothing about it", what)
	}
}

// requireRefusedByTheCap holds what every refusal by a cap has in common: 429; a
// Retry-After of very nearly the whole minute, which is what a cap PER MINUTE says
// (a window of a second would say 1, and be no cap at all); and no cookie touched.
func requireRefusedByTheCap(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("%s = %d, want 429", what, resp.StatusCode)
	}
	if secs, err := strconv.Atoi(resp.Header.Get(fiber.HeaderRetryAfter)); err != nil || secs < 55 || secs > 60 {
		t.Errorf("%s: Retry-After = %q, want a whole number of seconds from 55 to 60: the cap is per minute", what, resp.Header.Get(fiber.HeaderRetryAfter))
	}
	if got := resp.Header.Get(fiber.HeaderSetCookie); got != "" {
		t.Errorf("%s set a cookie (%q): a call the cap refuses must leave the refresh cookie in place", what, got)
	}
}

// TestLogoutAll_IsCappedPerUser: five calls a minute for one user, the sixth is
// refused 429 and never reaches the handler — nothing is revoked, and the refresh
// cookie is left in place — and another user behind the same address still has all
// of theirs.
func TestLogoutAll_IsCappedPerUser(t *testing.T) {
	h := newEndSessionsServer(t)
	route := "POST " + logoutAllURL

	for i := 1; i <= logoutAllRateLimit; i++ {
		requireServed(t, fmt.Sprintf("call %d by the owner", i), h.logoutAll(t, h.owner, ""))
	}
	for i := logoutAllRateLimit + 1; i <= logoutAllRateLimit+2; i++ {
		requireRefusedByTheCap(t, fmt.Sprintf("call %d by the owner", i), h.logoutAll(t, h.owner, ""))
	}
	if n := h.reachedBy(route, h.ownerID); n != logoutAllRateLimit {
		t.Errorf("the handler ran %d times for the owner, want %d: the limiter must refuse before it", n, logoutAllRateLimit)
	}

	// Per user, not per address: everything here comes from one address.
	requireServed(t, "another user's first call", h.logoutAll(t, h.other, ""))
}

// TestLogoutAll_OneBucketAcrossAddresses: the cap is the user's, from every address
// together — spread over two addresses, the sixth call is still refused. A bucket per
// user AND address would give each address five.
func TestLogoutAll_OneBucketAcrossAddresses(t *testing.T) {
	h := newEndSessionsServer(t)
	requireTheHarnessTellsAddressesApart(t, h)

	for i := 0; i < 3; i++ {
		requireServed(t, "a call from address A", h.logoutAll(t, h.owner, addressA))
	}
	for i := 0; i < logoutAllRateLimit-3; i++ {
		requireServed(t, "a call from address B", h.logoutAll(t, h.owner, addressB))
	}
	requireRefusedByTheCap(t, "the call after the cap, from address A", h.logoutAll(t, h.owner, addressA))
	requireRefusedByTheCap(t, "the call after the cap, from address B", h.logoutAll(t, h.owner, addressB))
}

// requireTheHarnessTellsAddressesApart is the control for every test that sends one
// user from two addresses: the per-address login bucket, drained from address A, is
// still whole for address B. If X-Forwarded-For did not change the address the
// limiters see, "one bucket across addresses" would hold of any key at all.
func requireTheHarnessTellsAddressesApart(t *testing.T, h *endSessionsServer) {
	t.Helper()
	var last int
	for range 16 {
		last = h.call(t, http.MethodPost, "/api/v1/auth/login", "", addressA).StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("the sixteenth login from address A = %d, want 429: the per-address bucket cannot be drained, so the harness cannot show an address", last)
	}
	if got := h.call(t, http.MethodPost, "/api/v1/auth/login", "", addressB).StatusCode; got == http.StatusTooManyRequests {
		t.Fatal("a login from address B was refused although only address A sent any: the two addresses are one to the limiters, so a test of one user from two proves nothing")
	}
}

// TestLogoutAll_AnAnonymousLoginFloodDoesNotDrainIt: the whole reason the cap is per
// user. Twenty logins with no credentials from the owner's own address empty the
// per-address bucket login lives in — the control: the sixteenth is refused — and
// the owner's sign-out everywhere is still answered, as often as its own cap
// allows.
func TestLogoutAll_AnAnonymousLoginFloodDoesNotDrainIt(t *testing.T) {
	h := newEndSessionsServer(t)

	statuses := make([]int, 0, 20)
	for range 20 {
		statuses = append(statuses, h.call(t, http.MethodPost, "/api/v1/auth/login", "", "").StatusCode)
	}
	if statuses[15] != http.StatusTooManyRequests {
		t.Fatalf("login statuses = %v: the sixteenth must be refused, or the flood drained nothing and this test proved nothing", statuses)
	}

	for i := 1; i <= logoutAllRateLimit; i++ {
		requireServed(t, fmt.Sprintf("call %d by the owner after the flood", i), h.logoutAll(t, h.owner, ""))
	}
	requireRefusedByTheCap(t, "the call after the cap", h.logoutAll(t, h.owner, ""))
}

// TestLogoutAll_ARefusedAPIKeyDoesNotSpendTheOwnersBucket: a leaked key is refused
// by the InteractiveOnly gate, which runs ahead of the limiter, so a loop of it can
// neither reach the handler nor use up the five calls its owner has. With the
// limiter first, the key — which authenticates as its owner — would drain the
// owner's bucket and refuse the owner's sign-out everywhere.
func TestLogoutAll_ARefusedAPIKeyDoesNotSpendTheOwnersBucket(t *testing.T) {
	h := newEndSessionsServer(t)
	route := "POST " + logoutAllURL

	for i := 1; i <= 10; i++ {
		if resp := h.logoutAll(t, h.apiKey, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("call %d with the owner's API key = %d, want 403 from the gate", i, resp.StatusCode)
		}
	}
	if n := h.reachedBy(route, h.ownerID); n != 0 {
		t.Fatalf("the handler ran %d times for an API key", n)
	}
	for i := 1; i <= logoutAllRateLimit; i++ {
		requireServed(t, fmt.Sprintf("call %d by the owner's session after ten refused key calls", i), h.logoutAll(t, h.owner, ""))
	}
	requireRefusedByTheCap(t, "the call after the cap", h.logoutAll(t, h.owner, ""))
}

// TestSessionRevoke_IsCappedPerUser: ending ONE session is capped too — a loop of it
// is sign-out everywhere one device at a time, which a stolen token could run without
// ever meeting logout-all's cap. Thirty a minute for one user, the thirty-first is
// refused 429 and never reaches the handler, and another user is unaffected.
func TestSessionRevoke_IsCappedPerUser(t *testing.T) {
	h := newEndSessionsServer(t)
	route := "DELETE /api/v1/auth/sessions/:id"

	for i := 1; i <= sessionRevokeRateLimit; i++ {
		requireServed(t, fmt.Sprintf("call %d by the owner", i), h.revokeSession(t, h.owner, ""))
	}
	for i := sessionRevokeRateLimit + 1; i <= sessionRevokeRateLimit+2; i++ {
		requireRefusedByTheCap(t, fmt.Sprintf("call %d by the owner", i), h.revokeSession(t, h.owner, ""))
	}
	if n := h.reachedBy(route, h.ownerID); n != sessionRevokeRateLimit {
		t.Errorf("the handler ran %d times for the owner, want %d: the limiter must refuse before it", n, sessionRevokeRateLimit)
	}
	requireServed(t, "another user's first call", h.revokeSession(t, h.other, ""))
}

// TestSessionRevoke_OneBucketAcrossAddresses: as for logout-all, the cap is the
// user's from every address together.
func TestSessionRevoke_OneBucketAcrossAddresses(t *testing.T) {
	h := newEndSessionsServer(t)
	requireTheHarnessTellsAddressesApart(t, h)

	for i := 0; i < sessionRevokeRateLimit/2; i++ {
		requireServed(t, "a call from address A", h.revokeSession(t, h.owner, addressA))
	}
	for i := 0; i < sessionRevokeRateLimit-sessionRevokeRateLimit/2; i++ {
		requireServed(t, "a call from address B", h.revokeSession(t, h.owner, addressB))
	}
	requireRefusedByTheCap(t, "the call after the cap, from address A", h.revokeSession(t, h.owner, addressA))
	requireRefusedByTheCap(t, "the call after the cap, from address B", h.revokeSession(t, h.owner, addressB))
}

// TestSessionRevoke_ARefusedAPIKeyDoesNotSpendTheOwnersBucket: the same ordering,
// for the same reason as for logout-all — the gate before the limiter, so a leaked
// key's loop of revokes neither reaches the handler nor uses up the owner's thirty.
func TestSessionRevoke_ARefusedAPIKeyDoesNotSpendTheOwnersBucket(t *testing.T) {
	h := newEndSessionsServer(t)
	route := "DELETE /api/v1/auth/sessions/:id"

	for i := 1; i <= 2*sessionRevokeRateLimit; i++ {
		if resp := h.revokeSession(t, h.apiKey, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("call %d with the owner's API key = %d, want 403 from the gate", i, resp.StatusCode)
		}
	}
	if n := h.reachedBy(route, h.ownerID); n != 0 {
		t.Fatalf("the handler ran %d times for an API key", n)
	}
	for i := 1; i <= sessionRevokeRateLimit; i++ {
		requireServed(t, fmt.Sprintf("call %d by the owner's session after %d refused key calls", i, 2*sessionRevokeRateLimit), h.revokeSession(t, h.owner, ""))
	}
	requireRefusedByTheCap(t, "the call after the cap", h.revokeSession(t, h.owner, ""))
}

// TestSessionEndingRoutesHaveBudgetsOfTheirOwn: ending one session and ending every
// session are different budgets, in both directions. Spending all of one leaves the
// other whole — a handful of single revokes must not refuse the owner's sign-out
// everywhere, and a spent sign-out everywhere must not stop a person ending one
// device — and a spent one stays spent.
func TestSessionEndingRoutesHaveBudgetsOfTheirOwn(t *testing.T) {
	h := newEndSessionsServer(t)

	// The owner spends sign-out everywhere; ending single sessions is untouched.
	for range logoutAllRateLimit {
		requireServed(t, "a sign-out everywhere by the owner", h.logoutAll(t, h.owner, ""))
	}
	requireRefusedByTheCap(t, "the sign-out everywhere after the cap", h.logoutAll(t, h.owner, ""))
	for i := 1; i <= sessionRevokeRateLimit; i++ {
		requireServed(t, fmt.Sprintf("session revoke %d by the owner, with sign-out everywhere spent", i), h.revokeSession(t, h.owner, ""))
	}
	requireRefusedByTheCap(t, "the session revoke after its cap", h.revokeSession(t, h.owner, ""))
	requireRefusedByTheCap(t, "the sign-out everywhere, still spent", h.logoutAll(t, h.owner, ""))

	// The other user spends session revokes; their sign-out everywhere is untouched.
	for range sessionRevokeRateLimit {
		requireServed(t, "a session revoke by the other user", h.revokeSession(t, h.other, ""))
	}
	requireRefusedByTheCap(t, "the other user's session revoke after the cap", h.revokeSession(t, h.other, ""))
	requireServed(t, "the other user's sign-out everywhere, with session revokes spent", h.logoutAll(t, h.other, ""))
}

// TestSessionLimiterKeys holds the two key functions to what the caps are: one
// bucket per user however many addresses the user comes from, a bucket of one's own
// for each user, none shared between the two limiters, and — for a request that has
// no user, which does not reach a limiter after authentication — the address, so
// that it still has a bucket. A key of the user AND the address would give a user
// one bucket per address, and a loop only has to change address.
func TestSessionLimiterKeys(t *testing.T) {
	cfg := &config.Config{ProxyHeader: fiber.HeaderXForwardedFor, TrustedProxies: []string{"0.0.0.0"}}
	app := fiber.New(buildFiberConfig(cfg))
	keys := map[string]func(fiber.Ctx) string{
		"logout-all":     logoutAllLimiterKey,
		"session-revoke": sessionRevokeLimiterKey,
	}
	for name, key := range keys {
		app.Get("/"+name, func(c fiber.Ctx) error {
			if who := c.Get("X-Test-User"); who != "" {
				c.Locals("user_id", uuid.MustParse(who))
			}
			return c.SendString(key(c))
		})
	}
	keyOf := func(route, user, from string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/"+route, nil)
		if user != "" {
			req.Header.Set("X-Test-User", user)
		}
		req.Header.Set(fiber.HeaderXForwardedFor, from)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("GET /%s: %v", route, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw := make([]byte, 256)
		n, _ := resp.Body.Read(raw)
		return string(raw[:n])
	}

	alice, bob := uuid.NewString(), uuid.NewString()
	for name := range keys {
		if a, b := keyOf(name, alice, addressA), keyOf(name, alice, addressB); a != b {
			t.Errorf("%s: one user has two keys, %q from one address and %q from another: a loop only has to change address", name, a, b)
		}
		if a, b := keyOf(name, alice, addressA), keyOf(name, bob, addressA); a == b {
			t.Errorf("%s: two users share the key %q: one would spend the other's bucket", name, a)
		}
		if a, b := keyOf(name, "", addressA), keyOf(name, "", addressB); a == b {
			t.Errorf("%s: a request with no user has the same key %q from two addresses: it should fall back to the address", name, a)
		}
		if anon, user := keyOf(name, "", addressA), keyOf(name, alice, addressA); anon == user {
			t.Errorf("%s: a request with no user shares a key with a user: %q", name, anon)
		}
	}
	if a, b := keyOf("logout-all", alice, addressA), keyOf("session-revoke", alice, addressA); a == b {
		t.Errorf("the two limiters use one key, %q, for one user", a)
	}
}

// TestDocs_TheSessionEndingCapsAreStatedWhereTheyAreDocumented keeps the figures the
// limiters have — logoutAllRateLimit, sessionRevokeRateLimit — in the three places
// that tell a client: each route's declaration (what /api/v1/api-docs is rendered
// from), the rate-limit table of the API reference, and the reference's account of
// sign-out everywhere. In each of them the answer to a call over the cap is stated
// too: 429 with a Retry-After, nothing revoked, the refresh cookie left in place
// (TestLogoutAll_IsCappedPerUser and TestSessionRevoke_IsCappedPerUser hold the
// last two to the chain). The figures follow the constants, so changing one fails
// here until the prose does; and each place is read on its own, so a sentence that
// survives in one cannot stand in for the others.
func TestDocs_TheSessionEndingCapsAreStatedWhereTheyAreDocumented(t *testing.T) {
	raw, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	lines := strings.Split(string(raw), "\n")
	tableRow := func(label string) string {
		for _, line := range lines {
			if strings.HasPrefix(line, "| "+label+" |") {
				return line
			}
		}
		return ""
	}
	const refused = "revokes nothing and leaves the refresh cookie in place"

	for _, tc := range []struct {
		method, path, tableLabel string
		cap                      int
	}{
		{fiber.MethodPost, logoutAllURL, "Sign out everywhere", logoutAllRateLimit},
		{fiber.MethodDelete, "/api/v1/auth/sessions/:id", "End one session", sessionRevokeRateLimit},
	} {
		desc := strings.Join(strings.Fields(declaredEndpoint(t, tc.method, tc.path).Description), " ")
		for _, want := range []string{fmt.Sprintf("at most %d calls a minute", tc.cap), "answered 429 with a Retry-After header", refused} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s %s: the declaration does not say %q, which the limiter makes true", tc.method, tc.path, want)
			}
		}

		row := tableRow(tc.tableLabel)
		if want := fmt.Sprintf("| %s | %d/min per user |", tc.tableLabel, tc.cap); !strings.HasPrefix(row, want) {
			t.Errorf("the rate-limit table of %s has no row starting %q (the row is %q): the reference and the limiter for %s %s have drifted",
				apiReferencePath(), want, row, tc.method, tc.path)
		}
		if !strings.Contains(row, "leaves the refresh cookie in place") {
			t.Errorf("the %q row of the rate-limit table does not say a call over the cap leaves the refresh cookie in place", tc.tableLabel)
		}
	}

	if got := referenceSentence(t, fmt.Sprintf("Each user may make at most %d calls a minute to it", logoutAllRateLimit)); !strings.Contains(got, refused) {
		t.Errorf("the reference's account of sign-out everywhere gives its cap but not what a refused call leaves alone: %q", got)
	}
	if got := referenceSentence(t, "`DELETE /auth/sessions/:id`, which ends one session,"); !strings.Contains(got, fmt.Sprintf("is capped at %d a minute per user", sessionRevokeRateLimit)) ||
		!strings.Contains(got, "refuses in the same way") {
		t.Errorf("the reference's account of ending one session does not give its cap, %d a minute per user, and say it refuses in the same way: %q", sessionRevokeRateLimit, got)
	}
}
