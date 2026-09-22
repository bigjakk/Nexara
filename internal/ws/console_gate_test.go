package ws

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	fiberws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	gorillaws "github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These cases drive the console and VNC handlers over a REAL WebSocket dial,
// because the two bypasses they pin both lived past the point app.Test can
// reach: the handler only runs after the upgrade.
//
// HandleConsole and HandleVNC make no permission check of their own. The
// console gate is the whole of their authorization, so what matters is that
// (a) no request reaches them without passing it, and (b) what they act on is
// what it checked. The stand-in database records the cluster each handler
// goes to look up and then answers "no rows", which stops the handler before
// it needs a Proxmox client — the lookup is the observable proof of which
// cluster a console would have been opened on.

// gateLookupDB records the uuid handed to every QueryRow (GetCluster's id).
type gateLookupDB struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (d *gateLookupDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *gateLookupDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (d *gateLookupDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range args {
		if id, ok := a.(uuid.UUID); ok {
			d.ids = append(d.ids, id)
		}
	}
	return gateNoRow{}
}

func (d *gateLookupDB) lookedUp() []uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]uuid.UUID(nil), d.ids...)
}

type gateNoRow struct{}

func (gateNoRow) Scan(...any) error { return pgx.ErrNoRows }

// startGateApp serves app on a free loopback port and returns the port.
func startGateApp(t *testing.T, app *fiber.App) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return ln.Addr().(*net.TCPAddr).Port
}

// dialGate opens a WebSocket with the token in the subprotocol, the way the
// SPA does, and returns the handshake status and the first message the
// server sent (empty when the upgrade was refused).
func dialGate(t *testing.T, port int, pathAndQuery, token string) (int, string) {
	t.Helper()
	dialer := *gorillaws.DefaultDialer
	dialer.Subprotocols = []string{subprotocolNegotiationName, subprotocolTokenPrefix + token}
	dialer.HandshakeTimeout = 3 * time.Second

	conn, resp, err := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d%s", port, pathAndQuery), http.Header{})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		_ = resp.Body.Close()
	}
	if err != nil {
		return status, ""
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, _ := conn.ReadMessage()
	return status, string(msg)
}

const (
	gateClusterA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	gateClusterB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// TestConsoleGate_RealRoutesRefuseTheBypasses mounts the production routes —
// Server.RegisterRoutes, the real handlers — and replays the two requests that
// used to open a console the caller held no grant for.
func TestConsoleGate_RealRoutesRefuseTheBypasses(t *testing.T) {
	logger := testLogger()
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)
	lookups := &gateLookupDB{}
	queries := db.New(lookups)

	hub := NewHub(logger, 0)
	hub.Run()
	t.Cleanup(hub.Stop)
	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second, ServerConfig{
		ConsoleHandler: NewConsoleHandler(queries, "unused", jwtSvc, logger),
		VNCHandler:     NewVNCHandler(queries, "unused", jwtSvc, logger),
	})
	app := fiber.New()
	server.RegisterRoutes(app)
	port := startGateApp(t, app)

	userID := uuid.New()
	// The hub token is what ANY signed-in account can mint.
	hubToken, _, err := jwtSvc.GenerateWSHubToken(userID, "viewer@example.com", "viewer", time.Minute)
	if err != nil {
		t.Fatalf("hub token: %v", err)
	}
	serialToken, _, err := jwtSvc.GenerateConsoleToken(userID, "operator@example.com", "operator",
		auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"}, time.Minute)
	if err != nil {
		t.Fatalf("console token: %v", err)
	}
	vncToken, _, err := jwtSvc.GenerateConsoleToken(userID, "operator@example.com", "operator",
		auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"}, time.Minute)
	if err != nil {
		t.Fatalf("vnc token: %v", err)
	}

	// Each refusal is asserted by its exact status. "Not 101" alone would
	// also be satisfied by a route that stopped matching at all — a 404, or
	// the SPA shell — which is not the gate doing its job.
	tests := []struct {
		name       string
		path       string
		token      string
		wantStatus int
	}{
		{"hub token, trailing slash on /ws/console",
			"/ws/console/?cluster_id=" + gateClusterB + "&node=pve-02&type=node_shell", hubToken, http.StatusForbidden},
		{"hub token, trailing slash on /ws/vnc",
			"/ws/vnc/?cluster_id=" + gateClusterB + "&node=pve-02&vmid=999", hubToken, http.StatusForbidden},
		{"hub token, case and slash variant",
			"/WS/CONSOLE/?cluster_id=" + gateClusterB + "&node=pve-02&type=node_shell", hubToken, http.StatusForbidden},
		{"vm_serial token for A with a second cluster, node and type appended",
			"/ws/console?cluster_id=" + gateClusterA + "&node=pve-01&vmid=100&type=vm_serial" +
				"&cluster_id=" + gateClusterB + "&node=pve-02&type=node_shell", serialToken, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(lookups.lookedUp())
			status, msg := dialGate(t, port, tt.path, tt.token)
			if status != tt.wantStatus {
				t.Errorf("status = %d (first message %q), want %d — this request must be refused at the gate",
					status, msg, tt.wantStatus)
			}
			if got := lookups.lookedUp()[before:]; len(got) != 0 {
				t.Errorf("a console handler ran and looked up cluster %v", got)
			}
		})
	}

	// The controls: the matching requests still upgrade and reach their
	// handlers, which look up the token's cluster. Without them every case
	// above could pass because the harness refuses everything.
	for _, control := range []struct{ name, path, token string }{
		{"console", "/ws/console?cluster_id=" + gateClusterA + "&node=pve-01&vmid=100&type=vm_serial", serialToken},
		{"vnc", "/ws/vnc?cluster_id=" + gateClusterA + "&node=pve-01&vmid=100", vncToken},
	} {
		t.Run("control: the token's own "+control.name+" still opens", func(t *testing.T) {
			before := len(lookups.lookedUp())
			status, _ := dialGate(t, port, control.path, control.token)
			if status != http.StatusSwitchingProtocols {
				t.Fatalf("status = %d, want 101", status)
			}
			time.Sleep(50 * time.Millisecond)
			if got := lookups.lookedUp()[before:]; len(got) != 1 || got[0].String() != gateClusterA {
				t.Errorf("the handler looked up %v, want exactly [%s]", got, gateClusterA)
			}
		})
	}
}

// TestConsoleHandlers_ActOnTheValidatedScope pins the other half of the
// duplicate-key fix, independently of the gate's refusal: even if a request
// carrying a different cluster in its query did get through, the handlers
// act on the scope the gate validated, never on the query.
//
// And a handler reached with no validated scope at all — a route mounted
// without the gate — refuses rather than falling back to the query.
func TestConsoleHandlers_ActOnTheValidatedScope(t *testing.T) {
	logger := testLogger()
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	scopes := map[string]*auth.ConsoleScope{
		"console": {ClusterID: gateClusterA, Node: "pve-01", Type: "node_shell"},
		"vnc":     {ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"},
	}

	for _, kind := range []string{"console", "vnc"} {
		t.Run(kind+": the scope, not the query", func(t *testing.T) {
			lookups := &gateLookupDB{}
			app := fiber.New()
			app.Get("/probe", func(c fiber.Ctx) error {
				c.Locals(consoleScopeLocal, scopes[kind])
				return c.Next()
			}, gateHandlerFor(kind, db.New(lookups), jwtSvc, logger))
			port := startGateApp(t, app)

			status, _ := dialGate(t, port, "/probe?cluster_id="+gateClusterB+"&node=pve-02&vmid=999&type=lxc", "x")
			if status != http.StatusSwitchingProtocols {
				t.Fatalf("status = %d, want 101", status)
			}
			time.Sleep(50 * time.Millisecond)
			if got := lookups.lookedUp(); len(got) != 1 || got[0].String() != gateClusterA {
				t.Errorf("the handler looked up %v; it must act on the validated scope's cluster %s, "+
					"not the query's %s", got, gateClusterA, gateClusterB)
			}
		})

		// A type the gate would never pass is still refused by the handler
		// rather than filled in — as node_shell, the most powerful console,
		// or as a VM for any VNC type that is not ct_vnc.
		t.Run(kind+": an unknown type opens nothing", func(t *testing.T) {
			lookups := &gateLookupDB{}
			bogus := *scopes[kind]
			bogus.Type = ""
			if kind == "vnc" {
				bogus.Type = "node_shell"
			}
			app := fiber.New()
			app.Get("/probe", func(c fiber.Ctx) error {
				c.Locals(consoleScopeLocal, &bogus)
				return c.Next()
			}, gateHandlerFor(kind, db.New(lookups), jwtSvc, logger))
			port := startGateApp(t, app)

			_, msg := dialGate(t, port, "/probe", "x")
			if got := lookups.lookedUp(); len(got) != 0 {
				t.Errorf("the handler acted on scope type %q and looked up %v", bogus.Type, got)
			}
			if msg == "" {
				t.Error("the handler sent no error message; it must refuse, and say so")
			}
		})

		t.Run(kind+": no scope, no console", func(t *testing.T) {
			lookups := &gateLookupDB{}
			app := fiber.New()
			app.Get("/probe", gateHandlerFor(kind, db.New(lookups), jwtSvc, logger))
			port := startGateApp(t, app)

			_, msg := dialGate(t, port, "/probe?cluster_id="+gateClusterB+"&node=pve-02&vmid=999", "x")
			if got := lookups.lookedUp(); len(got) != 0 {
				t.Errorf("the handler ran without a validated scope and looked up %v", got)
			}
			if msg == "" {
				t.Error("the handler sent no error message; it must refuse, and say so")
			}
		})
	}
}

// gateHandlerFor wraps the real handler of the given kind in the same
// websocket upgrader production uses.
func gateHandlerFor(kind string, queries *db.Queries, jwtSvc *auth.JWTService, logger *slog.Logger) fiber.Handler {
	cfg := wsConfigWithSubprotocol(nil)
	if kind == "vnc" {
		return fiberws.New(NewVNCHandler(queries, "unused", jwtSvc, logger).HandleVNC, cfg)
	}
	return fiberws.New(NewConsoleHandler(queries, "unused", jwtSvc, logger).HandleConsole, cfg)
}

// TestGuard_ConsoleHandlersReadOnlyTheScope is the static half of the
// duplicate-key fix. The test above can only observe the cluster a handler
// looks up — its stand-in database stops it before Proxmox — so a handler
// that went back to reading node, vmid or type from the query would pass it.
// This fails on any Query, Params, Headers or Cookies call in either handler:
// everything a console acts on has to come from the scope the gate validated.
func TestGuard_ConsoleHandlersReadOnlyTheScope(t *testing.T) {
	banned := map[string]bool{"Query": true, "Params": true, "Headers": true, "Cookies": true}
	handlers := map[string]string{"console.go": "HandleConsole", "vnc.go": "HandleVNC"}

	fset := token.NewFileSet()
	found := 0
	for file, name := range handlers {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != name || fd.Body == nil {
				continue
			}
			found++
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && banned[sel.Sel.Name] {
					t.Errorf("%s: %s calls .%s — a console handler takes every value from the validated scope "+
						"(consoleScopeLocal), never from the request", fset.Position(call.Pos()), name, sel.Sel.Name)
				}
				return true
			})
		}
	}
	if found != len(handlers) {
		t.Fatalf("found %d of the %d console handlers; one was renamed or moved, so this guard checks nothing for it",
			found, len(handlers))
	}
}
