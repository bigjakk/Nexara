package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// permCheck records one HasPermission call so tests can pin not just the
// action but the resource and scope the handler asked about.
type permCheck struct {
	action    string
	resource  string
	scopeType string
	scopeID   uuid.UUID
}

// recordingEngine satisfies permissionEngine, granting exactly the actions in
// its set and recording every cluster-scoped check. Unlike stubEngine
// (all-or-nothing by role) it can express "holds view but not console" — the
// shape of the vulnerability this file guards against. HasGlobalPermission
// always denies so a regression from requireClusterPerm to requirePerm
// (losing per-cluster isolation) also fails the allow-side test.
type recordingEngine struct {
	actions map[string]bool
	checks  []permCheck
}

func (e *recordingEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	e.checks = append(e.checks, permCheck{action: action, resource: resource, scopeType: scopeType, scopeID: scopeID})
	return e.actions[action], nil
}

func (e *recordingEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, _, _ string) (bool, error) {
	return false, nil
}

func (e *recordingEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{}, nil
}

// newConsoleTokenTestApp wires ConsoleToken behind the given engine. The
// handler has nil queries/pool — the deny path never reaches them, and the
// allow path's nil-deref panic is contained by the recoverer (see the
// not-403 assertion below).
func newConsoleTokenTestApp(t *testing.T, engine permissionEngine) *fiber.App {
	t.Helper()

	handler := &AuthHandler{
		jwtService: auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour),
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": code})
		},
	})
	app.Use(recoverer.New())
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", engine)
		return c.Next()
	})
	app.Post("/auth/console-token", handler.ConsoleToken)
	return app
}

func mintConsoleTokenReq(clusterID uuid.UUID, consoleType string, vmid int) *http.Request {
	body := fmt.Sprintf(`{"cluster_id":%q,"node":"pve1","type":%q`, clusterID, consoleType)
	if vmid > 0 {
		body += fmt.Sprintf(`,"vmid":%d`, vmid)
	}
	body += `}`
	req := httptest.NewRequest(http.MethodPost, "/auth/console-token", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

var consoleTokenTypes = []struct {
	consoleType  string
	vmid         int
	wantResource string
}{
	{"node_shell", 0, "node"},
	{"vm_serial", 100, "vm"},
	{"vm_vnc", 100, "vm"},
	{"ct_attach", 100, "container"},
	{"ct_vnc", 100, "container"},
}

// TestConsoleToken_ViewOnlyDenied is the regression test for the Viewer
// root-shell vulnerability: an account holding only view:* permissions (the
// built-in Viewer role's grant set) must not be able to mint any console
// token. Before migration 000078 the gate was requireClusterPerm("view", …),
// which handed every Viewer a node_shell token — a root shell on the
// hypervisor.
func TestConsoleToken_ViewOnlyDenied(t *testing.T) {
	for _, tt := range consoleTokenTypes {
		t.Run(tt.consoleType, func(t *testing.T) {
			engine := &recordingEngine{actions: map[string]bool{"view": true}}
			app := newConsoleTokenTestApp(t, engine)

			resp, err := app.Test(mintConsoleTokenReq(uuid.New(), tt.consoleType, tt.vmid))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want %d (view:* must not mint %s tokens)",
					resp.StatusCode, http.StatusForbidden, tt.consoleType)
			}
		})
	}
}

// TestConsoleToken_ConsolePermPassesGate asserts the complementary
// direction: an account holding the console action clears the RBAC gate for
// every console type, and the gate asked exactly the right question —
// action "console", the type's resource, scoped to the requested cluster.
// With nil queries the handler cannot complete the mint (the recoverer
// converts the nil-deref into a 500), so the status assertion is "not 403"
// — proof the permission check accepted console:* — rather than a full 200
// path, which needs a real user row.
func TestConsoleToken_ConsolePermPassesGate(t *testing.T) {
	for _, tt := range consoleTokenTypes {
		t.Run(tt.consoleType, func(t *testing.T) {
			engine := &recordingEngine{actions: map[string]bool{"console": true}}
			app := newConsoleTokenTestApp(t, engine)
			clusterID := uuid.New()

			resp, err := app.Test(mintConsoleTokenReq(clusterID, tt.consoleType, tt.vmid))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("status = 403; console:* should clear the RBAC gate for %s", tt.consoleType)
			}

			if len(engine.checks) != 1 {
				t.Fatalf("permission checks = %d, want exactly 1", len(engine.checks))
			}
			got := engine.checks[0]
			want := permCheck{action: "console", resource: tt.wantResource, scopeType: "cluster", scopeID: clusterID}
			if got != want {
				t.Errorf("permission check = %+v, want %+v", got, want)
			}
		})
	}
}
