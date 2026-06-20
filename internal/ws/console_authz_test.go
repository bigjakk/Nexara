package ws

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestAuthMiddleware_ConsolePathScopeEnforcement covers the per-cluster RBAC
// fix for /ws/console and /ws/vnc (security review finding #1) AND the
// hub-scope-required rule for the generic /ws path (remediation 2.7).
//
// Rules enforced:
//   - /ws/console and /ws/vnc REQUIRE claims.ConsoleScope != nil with
//     scope fields matching the upgrade query parameters.
//   - /ws REQUIRES claims.WSScope == "hub"; rejects regular access tokens
//     and console-scoped tokens.
//   - Tokens on either path may arrive in `?token=` (legacy) OR in
//     `Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>`.
func TestAuthMiddleware_ConsolePathScopeEnforcement(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	// We only need a Server with the JWT service and logger wired up; the
	// Fiber app and Hub aren't exercised. Construct directly to avoid the
	// "no RBAC engine" startup warning that NewServer would log.
	server := &Server{jwt: jwtSvc, logger: logger}

	// Sentinel handler that records reaching past the middleware. Using a
	// regular HTTP handler (not websocket.New) sidesteps the WS hijack
	// machinery that app.Test can't fully drive — the middleware is what
	// we're actually testing here.
	var reached bool
	passHandler := func(c fiber.Ctx) error {
		reached = true
		return c.SendStatus(fiber.StatusOK)
	}

	app := fiber.New()
	app.Use("/ws/console", server.authMiddleware)
	app.Get("/ws/console", passHandler)
	app.Use("/ws/vnc", server.authMiddleware)
	app.Get("/ws/vnc", passHandler)
	app.Use("/ws", server.authMiddleware)
	app.Get("/ws", passHandler)

	userID := uuid.New()

	accessToken, _, err := jwtSvc.GenerateAccessToken(userID, "user@example.com", "admin")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	hubToken, _, err := jwtSvc.GenerateWSHubToken(userID, "user@example.com", "admin", 60*time.Second)
	if err != nil {
		t.Fatalf("generate hub token: %v", err)
	}

	const targetCluster = "550e8400-e29b-41d4-a716-446655440000"
	const otherCluster = "11111111-2222-3333-4444-555555555555"

	consoleScope := auth.ConsoleScope{
		ClusterID: targetCluster,
		Node:      "pve1",
		Type:      "node_shell",
	}
	consoleToken, _, err := jwtSvc.GenerateConsoleToken(
		userID, "user@example.com", "admin", consoleScope, 60*time.Second,
	)
	if err != nil {
		t.Fatalf("generate console token: %v", err)
	}

	vmVNCScope := auth.ConsoleScope{
		ClusterID: targetCluster,
		Node:      "pve1",
		VMID:      100,
		Type:      "vm_vnc",
	}
	vmVNCToken, _, err := jwtSvc.GenerateConsoleToken(
		userID, "user@example.com", "admin", vmVNCScope, 60*time.Second,
	)
	if err != nil {
		t.Fatalf("generate vm vnc console token: %v", err)
	}

	// Expired tokens — minted with a negative TTL so `exp` is already in the
	// past at validation time. Closes the A5 coverage gap: until 3.10, no
	// test exercised the validator's expiry branch on the WS upgrade path,
	// which is exactly the path Finding #1 lived in.
	expiredConsoleToken, _, err := jwtSvc.GenerateConsoleToken(
		userID, "user@example.com", "admin", consoleScope, -1*time.Minute,
	)
	if err != nil {
		t.Fatalf("generate expired console token: %v", err)
	}
	expiredVMVNCToken, _, err := jwtSvc.GenerateConsoleToken(
		userID, "user@example.com", "admin", vmVNCScope, -1*time.Minute,
	)
	if err != nil {
		t.Fatalf("generate expired vm vnc console token: %v", err)
	}
	expiredHubToken, _, err := jwtSvc.GenerateWSHubToken(
		userID, "user@example.com", "admin", -1*time.Minute,
	)
	if err != nil {
		t.Fatalf("generate expired hub token: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		token      string
		extra      string // query string fragment appended after token=...
		// useSubprotocol — when true, send the token via Sec-WebSocket-Protocol
		// instead of as a query param. Both paths should accept the same tokens.
		useSubprotocol bool
		wantStatus     int
		wantReach      bool
	}{
		{
			name:       "regular access token rejected on /ws/console",
			path:       "/ws/console",
			token:      accessToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "regular access token rejected on /ws/vnc",
			path:       "/ws/vnc",
			token:      accessToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=100",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "scoped console token rejected on generic /ws",
			path:       "/ws",
			token:      consoleToken,
			extra:      "",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "regular access token rejected on /ws (was previously allowed)",
			path:       "/ws",
			token:      accessToken,
			extra:      "",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "hub-scoped token allowed on /ws (URL token path)",
			path:       "/ws",
			token:      hubToken,
			extra:      "",
			wantStatus: fiber.StatusOK,
			wantReach:  true,
		},
		{
			name:           "hub-scoped token allowed on /ws (subprotocol path)",
			path:           "/ws",
			token:          hubToken,
			extra:          "",
			useSubprotocol: true,
			wantStatus:     fiber.StatusOK,
			wantReach:      true,
		},
		{
			name:       "hub-scoped token rejected on /ws/console",
			path:       "/ws/console",
			token:      hubToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "hub-scoped token rejected on /ws/vnc",
			path:       "/ws/vnc",
			token:      hubToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=100",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "scoped node_shell token allowed on /ws/console with matching params",
			path:       "/ws/console",
			token:      consoleToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusOK,
			wantReach:  true,
		},
		{
			name:           "scoped node_shell token allowed on /ws/console via subprotocol",
			path:           "/ws/console",
			token:          consoleToken,
			extra:          "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			useSubprotocol: true,
			wantStatus:     fiber.StatusOK,
			wantReach:      true,
		},
		{
			name:       "scoped vm_vnc token allowed on /ws/vnc with matching params",
			path:       "/ws/vnc",
			token:      vmVNCToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=100",
			wantStatus: fiber.StatusOK,
			wantReach:  true,
		},
		{
			name:       "scoped console token with cross-cluster mismatch rejected",
			path:       "/ws/console",
			token:      consoleToken,
			extra:      "&cluster_id=" + otherCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "scoped console token with node mismatch rejected",
			path:       "/ws/console",
			token:      consoleToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve2&type=node_shell",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "scoped vm_vnc token with vmid mismatch rejected",
			path:       "/ws/vnc",
			token:      vmVNCToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=999",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		// Expired-token cases: ValidateAccessToken returns
		// jwt.ErrTokenExpired, the middleware maps that to 401 + "invalid
		// token" (same shape as a malformed-signature reject). All three
		// scoped paths exercise this so a future jwt-lib upgrade or claim-
		// validation refactor can't quietly drop the exp check on one path.
		{
			name:       "expired scoped console token rejected on /ws/console",
			path:       "/ws/console",
			token:      expiredConsoleToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "expired scoped vnc token rejected on /ws/vnc",
			path:       "/ws/vnc",
			token:      expiredVMVNCToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=100",
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "expired hub token rejected on /ws",
			path:       "/ws",
			token:      expiredHubToken,
			extra:      "",
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "missing token on /ws/console returns 401",
			path:       "/ws/console",
			token:      "",
			extra:      "&cluster_id=" + targetCluster,
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "missing token on /ws/vnc returns 401",
			path:       "/ws/vnc",
			token:      "",
			extra:      "&cluster_id=" + targetCluster,
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "missing token on /ws returns 401",
			path:       "/ws",
			token:      "",
			extra:      "",
			wantStatus: fiber.StatusUnauthorized,
			wantReach:  false,
		},
		// Case-insensitive routing bypass: Fiber's default CaseSensitive=false
		// routes /WS/Console to the /ws/console handler, but c.Path() returns
		// the literal request path. A strict path-equality scope check would
		// fall into the "no scope required" branch and accept a regular
		// access token. The middleware must compare case-insensitively.
		{
			name:       "regular access token rejected on case-variant /WS/Console",
			path:       "/WS/Console",
			token:      accessToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&type=node_shell",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "regular access token rejected on case-variant /WS/VNC",
			path:       "/WS/VNC",
			token:      accessToken,
			extra:      "&cluster_id=" + targetCluster + "&node=pve1&vmid=100",
			wantStatus: fiber.StatusForbidden,
			wantReach:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached = false

			var url string
			if tt.useSubprotocol {
				// No query token; everything except the token rides in the
				// query string for scope validation.
				url = tt.path + "?_=1" + tt.extra
			} else {
				url = tt.path + "?token=" + tt.token + tt.extra
			}
			req := httptest.NewRequest(fiber.MethodGet, url, nil)
			// authMiddleware short-circuits with 426 when the request isn't a
			// WS upgrade, so emulate the upgrade headers gorilla/websocket
			// sends. The downstream pass-handler is a plain HTTP handler so
			// no real upgrade happens; it just returns 200 if the middleware
			// let the request through.
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Sec-WebSocket-Version", "13")
			if tt.useSubprotocol && tt.token != "" {
				req.Header.Set(
					"Sec-WebSocket-Protocol",
					"nexara.token, nexara.token."+tt.token,
				)
			}

			resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if reached != tt.wantReach {
				t.Errorf("reached = %v, want %v (middleware should %s the request)",
					reached, tt.wantReach,
					map[bool]string{true: "allow", false: "block"}[tt.wantReach],
				)
			}
		})
	}
}

// TestTokenFromSubprotocol is a focused unit test on the header parser. The
// integration cases above exercise the happy path; this covers the edge
// cases (missing prefix, whitespace tolerance, malformed entries).
func TestTokenFromSubprotocol(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{"only static negotiation entry", "nexara.token", ""},
		{"static + token entry", "nexara.token, nexara.token.abc.def.ghi", "abc.def.ghi"},
		{"reversed order", "nexara.token.abc.def.ghi, nexara.token", "abc.def.ghi"},
		{"extra whitespace around entries tolerated", "  nexara.token  ,  nexara.token.abc.def.ghi  ", "abc.def.ghi"},
		{"unrelated subprotocol ignored", "graphql-ws, nexara.token.xyz", "xyz"},
		{"no token entry returns empty", "graphql-ws, mqtt", ""},
		{"prefix-only entry has empty token", "nexara.token.", ""},
		// Stricter parser: M2 hardening from the security review.
		{"empty entry between commas", "nexara.token,,nexara.token.abc.def", "abc.def"},
		{"case-different prefix rejected", "Nexara.Token.abc.def", ""},
		{"whitespace inside JWT segment rejected", "nexara.token.abc def.ghi", ""},
		// Trailing whitespace on the ENTRY is stripped by TrimSpace; only
		// whitespace inside the JWT itself is rejected. Documenting the
		// boundary so a future reader doesn't accidentally tighten the
		// outer trim.
		{"trailing whitespace on entry trimmed", "nexara.token.abc ", "abc"},
		{"control char inside JWT segment rejected", "nexara.token.abc\tdef", ""},
		{"non-base64url char inside JWT rejected", "nexara.token.abc=def", ""},
		// Skipping a malformed entry should still let a later valid one win.
		{"malformed first, valid second", "nexara.token.abc def, nexara.token.xyz123", "xyz123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenFromSubprotocol(tt.header)
			if got != tt.want {
				t.Errorf("tokenFromSubprotocol(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// TestTokenFromSubprotocolHeader_MultiLine covers the M1 finding: clients
// MAY split a comma-separated header across multiple `Sec-WebSocket-Protocol:`
// lines (RFC 7230 §3.2.2). Fasthttp stores each as a distinct kv entry; the
// helper joins them before delegating to the parser.
func TestTokenFromSubprotocolHeader_MultiLine(t *testing.T) {
	app := fiber.New()
	var got string
	app.Get("/probe", func(c fiber.Ctx) error {
		got = tokenFromSubprotocolHeader(c)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(fiber.MethodGet, "/probe", nil)
	// Two distinct lines — fasthttp will treat as two kv entries.
	req.Header.Add("Sec-WebSocket-Protocol", "nexara.token")
	req.Header.Add("Sec-WebSocket-Protocol", "nexara.token.abc.def.ghi")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if got != "abc.def.ghi" {
		t.Errorf("multi-line subprotocol parse = %q, want %q", got, "abc.def.ghi")
	}
}
