package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
)

// TestAuthRoutes_RefuseABadRequest drives the handlers with no database behind them: every
// row is refused before anything is looked up. A missing refresh token (no body, no cookie)
// is an auth state, not a bad request (L1 in the security review for task 2.6).
func TestAuthRoutes_RefuseABadRequest(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{"register: empty body", "/auth/register", `{}`, http.StatusBadRequest},
		{"register: missing password", "/auth/register", `{"email":"test@example.com"}`, http.StatusBadRequest},
		{"register: missing email", "/auth/register", `{"password":"Str0ng!Pass"}`, http.StatusBadRequest},
		{"register: invalid email", "/auth/register", `{"email":"not-an-email","password":"Str0ng!Pass"}`, http.StatusBadRequest},
		{"register: password too short", "/auth/register", `{"email":"test@example.com","password":"S1!a"}`, http.StatusBadRequest},
		{"register: password with no uppercase", "/auth/register", `{"email":"test@example.com","password":"str0ng!pass"}`, http.StatusBadRequest},
		{"register: password with no digit", "/auth/register", `{"email":"test@example.com","password":"Strong!Pass"}`, http.StatusBadRequest},
		{"register: password with no special character", "/auth/register", `{"email":"test@example.com","password":"Str0ngPassw"}`, http.StatusBadRequest},
		{"login: missing password", "/auth/login", `{"email":"test@example.com"}`, http.StatusBadRequest},
		{"login: missing email", "/auth/login", `{"password":"Str0ng!Pass"}`, http.StatusBadRequest},
		{"login: empty body", "/auth/login", `{}`, http.StatusBadRequest},
		{"login: not JSON", "/auth/login", "not json", http.StatusBadRequest},
		{"refresh: no token", "/auth/refresh", `{}`, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, tt.want, raw)
			}
			if !json.Valid(raw) {
				t.Errorf("response is not valid JSON: %s", raw)
			}
		})
	}
}

// TestRegister_StrongPassword_WithNilDB_BypassesHash locks down Finding #17: Register must
// NOT call bcrypt before the count/admin gate. A structurally valid request (good email,
// strong password) fails at the DB step because the handler has pool == nil: 500
// "registration unavailable" right after password-complexity validation, well before
// HashPassword. If a refactor moves HashPassword back above the pool nil check, the
// wall-clock jumps to >>1x a bcrypt call and the calibrated assertion below catches it.
func TestRegister_StrongPassword_WithNilDB_BypassesHash(t *testing.T) {
	// A real work factor, not TestMain's cheapest one: the comparison below needs a bcrypt
	// that clearly outlasts the request.
	defer auth.SetBcryptCostForTesting(10)()
	app := newTestApp(t)

	// Calibrate against the runtime, since bcrypt cost varies with CPU/CI load.
	calStart := time.Now()
	auth.RunDummyBcrypt("Str0ng!Pass")
	bcryptDuration := time.Since(calStart)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(`{"email":"test@example.com","password":"Str0ng!Pass"}`))
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := app.Test(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusInternalServerError, body)
	}

	// The no-DB short-circuit path should be at least 5x faster than a real bcrypt call; if
	// someone reverts the order, this margin disappears immediately.
	if maxAllowed := bcryptDuration / 5; elapsed > maxAllowed {
		t.Errorf("Register took %v vs single bcrypt %v — hash-after-auth-check appears to be reverted (Finding #17)", elapsed, bcryptDuration)
	}
}

// TestRegister_FirstUserAdvisoryLockKeyIsStable locks down the constant so a future edit
// can't silently swap it for a value that collides with another advisory lock holder
// elsewhere in the codebase, or zero it out.
func TestRegister_FirstUserAdvisoryLockKeyIsStable(t *testing.T) {
	if firstUserAdvisoryLockKey == 0 {
		t.Fatal("firstUserAdvisoryLockKey is 0 — a value of 0 risks colliding with default-initialised holders")
	}
	const want int64 = 0x4E455841524131
	if firstUserAdvisoryLockKey != want {
		t.Errorf("firstUserAdvisoryLockKey = %#x, want %#x — changing this means concurrent Register calls in old/new versions of the binary won't serialise against each other during a rolling restart", firstUserAdvisoryLockKey, want)
	}
}

// newTestApp creates a Fiber app with auth handler for unit tests (no DB/Redis).
func newTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := &AuthHandler{
		pool:       nil,
		queries:    nil,
		jwtService: auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour),
	}

	app := fiber.New(fiber.Config{ErrorHandler: authErrorHandler})

	// Register is still a plain fiber.Handler: it is mounted with authOptional and stays out of
	// the registry, because the Permissions vocabulary has no shape for "parse a session if one
	// is presented". See registerAuthEndpoints in internal/api/registry_auth.go.
	app.Post("/auth/register", handler.Register)
	app.Post("/auth/login", withRequestParams(t, authLoginMirror(), nil, handler.Login))
	app.Post("/auth/refresh", withRequestParams(t, authRefreshMirror(), nil, handler.Refresh))

	return app
}

// The two mirrors below are local copies of the parts of these routes' declarations
// (internal/api/registry_auth.go) that the handlers read, for the reason migrationListMirror
// gives: package api imports this package, not the other way round. Login refuses a missing
// or blank credential before the handler sees it, and refresh accepts an empty body because
// the browser path carries the token in a cookie.
func authLoginMirror() apischema.Properties {
	return apischema.Properties{
		"email":    {Type: apischema.String, MinLength: apischema.Ptr(1)},
		"password": {Type: apischema.String, MinLength: apischema.Ptr(1)},
	}
}

func authRefreshMirror() apischema.Properties {
	return apischema.Properties{
		"refresh_token": {Type: apischema.String, Optional: true},
	}
}
