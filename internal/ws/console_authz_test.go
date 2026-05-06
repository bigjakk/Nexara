package ws

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestAuthMiddleware_ConsolePathScopeEnforcement covers the per-cluster RBAC
// fix for /ws/console and /ws/vnc (security review finding #1).
//
// Before the fix, authMiddleware accepted any valid JWT on every WS path; a
// regular access token + cluster_id query param could open a root shell on
// any Proxmox node. The mint endpoint POST /api/v1/auth/console-token does
// the per-cluster RBAC check, but nothing required the scoped flow be used.
//
// The fix:
//   - /ws/console and /ws/vnc now REQUIRE claims.ConsoleScope != nil.
//   - /ws (the generic hub channel) now REJECTS scoped tokens — they must
//     only be used on the path they were minted for.
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
	passHandler := func(c *fiber.Ctx) error {
		reached = true
		return c.SendStatus(fiber.StatusOK)
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
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

	const targetCluster = "550e8400-e29b-41d4-a716-446655440000"
	const otherCluster = "11111111-2222-3333-4444-555555555555"

	consoleScope := auth.ConsoleScope{
		ClusterID: targetCluster,
		Node:      "pve1",
		Type:      "node_shell",
	}
	consoleToken, _, err := jwtSvc.GenerateConsoleToken(
		userID, "user@example.com", "admin", consoleScope, 5*time.Minute,
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
		userID, "user@example.com", "admin", vmVNCScope, 5*time.Minute,
	)
	if err != nil {
		t.Fatalf("generate vm vnc console token: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		token      string
		extra      string // query string fragment after token=...
		wantStatus int
		wantReach  bool
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
			name:       "regular access token allowed on /ws",
			path:       "/ws",
			token:      accessToken,
			extra:      "",
			wantStatus: fiber.StatusOK,
			wantReach:  true,
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

			url := tt.path + "?token=" + tt.token + tt.extra
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

			resp, err := app.Test(req, -1)
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
