package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestAuthMiddlewares_AdmitOnlyASessionsAccessToken: authRequired and authOptional
// authenticate a caller with ONE kind of token — an interactive session's access
// token, as Claims.IsSession says — and with nothing else a JWT secret signed: not
// a console token, not a WebSocket hub token, and not claims that fit no kind (both
// markers at once, a hub scope this code does not issue), which an exclusion of "the
// two markers it knew of" would have let through as a session. The refusal of each
// names what it was where it can, and authOptional treats it as no caller at all.
func TestAuthMiddlewares_AdmitOnlyASessionsAccessToken(t *testing.T) {
	fake, _, _ := newChainDB(t)
	const secret = "token-kind-test-secret"
	jwtSvc := auth.NewJWTService(secret, 15*time.Minute, 7*24*time.Hour)
	srv := &Server{queries: db.New(fake), jwtService: jwtSvc}

	user := uuid.New()
	mint := func(scope *auth.ConsoleScope, wsScope string) string {
		t.Helper()
		claims := auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ID: uuid.NewString(), Subject: user.String(), Issuer: "nexara",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), IssuedAt: jwt.NewNumericDate(time.Now()),
			},
			UserID: user, Email: chainEmail, Role: "admin", ConsoleScope: scope, WSScope: wsScope,
		}
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return signed
	}
	scope := &auth.ConsoleScope{ClusterID: "cluster01", Node: "pve-01", Type: "node_shell"}
	access, _, err := jwtSvc.GenerateAccessToken(user, chainEmail, "admin")
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	console, _, err := jwtSvc.GenerateConsoleToken(user, chainEmail, "admin", *scope, time.Minute)
	if err != nil {
		t.Fatalf("console token: %v", err)
	}
	hub, _, err := jwtSvc.GenerateWSHubToken(user, chainEmail, "admin", time.Minute)
	if err != nil {
		t.Fatalf("hub token: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	whoami := func(c fiber.Ctx) error {
		return c.SendString(fmt.Sprintf("%v|%v", c.Locals("user_id"), c.Locals(handlers.LocalsAuthMethod)))
	}
	app.Get("/required", srv.authRequired(), whoami)
	app.Get("/optional", srv.authOptional(), whoami)

	get := func(path, token string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	messageOf := func(body string) string {
		var env ErrorResponse
		_ = json.Unmarshal([]byte(body), &env)
		return env.Message
	}

	session := user.String() + "|" + handlers.AuthMethodSession
	for _, path := range []string{"/required", "/optional"} {
		if status, body := get(path, access); status != http.StatusOK || body != session {
			t.Errorf("%s with an access token = %d %q, want 200 %q", path, status, body, session)
		}
	}

	for _, tc := range []struct {
		name, token, message string
	}{
		{"a console token", console, "Console-scoped token cannot be used for API requests"},
		{"a WebSocket hub token", hub, "WS-scoped token cannot be used for API requests"},
		{"both markers at once", mint(scope, auth.WSScopeHub), "Token cannot be used for API requests"},
		{"a hub scope this code does not issue", mint(nil, "elsewhere"), "Token cannot be used for API requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := get("/required", tc.token)
			if status != http.StatusUnauthorized || messageOf(body) != tc.message {
				t.Errorf("authRequired = %d %q, want 401 %q", status, messageOf(body), tc.message)
			}
			// authOptional lets the request through as nobody: no user, and no method.
			if status, body := get("/optional", tc.token); status != http.StatusOK || body != "<nil>|<nil>" {
				t.Errorf("authOptional = %d %q, want 200 \"<nil>|<nil>\": a token that is not a session authenticates no one", status, body)
			}
		})
	}
}
