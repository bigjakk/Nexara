package ws

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
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

// TestConsoleHandlers_CloseTheBrowserSocket pins that a console handler that
// returns — here on its earliest refusal — closes the browser's connection
// rather than leaving it for the client to close, and how it gets there.
//
// The websocket package closes a hijacked connection only after a panic, and
// fasthttp leaves it open (KeepHijackedConns), so a handler that wrote its
// Close frame and returned used to hold the socket until the client let go.
// closeBrowserSocket closes it now, after reading until the client answers the
// Close or closeDrainTimeout passes, and both arms are pinned: a client that
// answers is let go at once, and one that never does is let go anyway — but
// not before the deadline, because closing on unread input is the reset the
// drain exists to avoid.
//
// So the client here sends first, before it reads the refusal, and the EOF has
// to be a clean one. Input the drain left unread would turn the close into a
// reset, and a drain that read one message and gave up would leave most of it.
func TestConsoleHandlers_CloseTheBrowserSocket(t *testing.T) {
	logger := testLogger()
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	for _, kind := range []string{"console", "vnc"} {
		for _, tc := range []struct {
			name     string
			answers  bool
			earliest time.Duration // the soonest the server may close, from the Close frame
			latest   time.Duration // the latest
		}{
			{"a client that answers the Close is let go at once", true, 0, closeDrainTimeout - time.Second},
			{"a client that never answers is let go at the deadline", false,
				closeDrainTimeout - time.Second, closeDrainTimeout + 3*time.Second},
		} {
			t.Run(kind+": "+tc.name, func(t *testing.T) {
				t.Parallel()
				app := fiber.New()
				app.Get("/probe", gateHandlerFor(kind, db.New(&gateLookupDB{}), jwtSvc, logger))
				port := startGateApp(t, app)

				dialer := *gorillaws.DefaultDialer
				dialer.Subprotocols = []string{subprotocolNegotiationName}
				conn, resp, err := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/probe", port), http.Header{})
				if resp != nil {
					_ = resp.Body.Close()
				}
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				defer func() { _ = conn.Close() }()

				// Typing into a console the server is about to refuse: input
				// the handler never reads, in frames under the read limit.
				for range 10 {
					if err := conn.WriteMessage(gorillaws.BinaryMessage, make([]byte, 8<<10)); err != nil {
						t.Fatalf("send: %v", err)
					}
				}

				// gorilla's default close handler answers a Close with one of
				// its own before ReadMessage returns; replacing it is how a
				// client declines to.
				if !tc.answers {
					conn.SetCloseHandler(func(int, string) error { return nil })
				}
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatalf("reading the refusal: %v", err)
				}
				// writeError's own Close, carrying the refusal's reason — not
				// closeBrowserSocket's fallback, which would say "internal error".
				_, _, err = conn.ReadMessage()
				var ce *gorillaws.CloseError
				if !errors.As(err, &ce) || ce.Code != gorillaws.CloseInternalServerErr || ce.Text != "console scope missing" {
					t.Fatalf("after the refusal: %v, want its Close frame, carrying the refusal's reason", err)
				}
				closed := time.Now()

				if err := waitForEOF(conn.NetConn(), closeDrainTimeout+5*time.Second); err != nil {
					t.Fatal(err)
				}
				if took := time.Since(closed); took < tc.earliest || took > tc.latest {
					t.Errorf("the server closed the socket %v after its Close frame, want between %v and %v",
						took, tc.earliest, tc.latest)
				}
			})
		}
	}
}

// waitForEOF reads raw from a connection whose websocket traffic is over,
// until the server closes it. A timeout means the server never did; a byte
// means something arrived after the frames the caller read — though not bytes
// the websocket reader had already buffered, which are out of its sight; and
// any error but io.EOF means it closed with a reset rather than a FIN, which
// is what closing on unread input does.
func waitForEOF(raw net.Conn, limit time.Duration) error {
	_ = raw.SetReadDeadline(time.Now().Add(limit))
	_, err := raw.Read(make([]byte, 1))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return fmt.Errorf("the server never closed the connection (waited %v)", limit)
	}
	if err == nil {
		return errors.New("read a byte where the server's EOF should be")
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("the connection ended with %v, want a clean EOF (a FIN, not a reset)", err)
	}
	return nil
}

// TestConsoleHandlers_ClosePromptlyWithAReasonAfterAPanic pins the Close
// closeBrowserSocket opens with, for the one path that reaches it without
// having sent one: a panic. It unwinds through the deferred close before the
// websocket package's recover handler runs, so without that Close the client
// was told nothing — the recover handler's {"error":"internal error"} could no
// longer be written — and the drain waited out closeDrainTimeout for the
// answer to a Close that was never sent. A nil database makes each handler
// panic at its first lookup, and the websocket package's recover handler logs
// both panics, stack traces and all, to stderr: expected output, visible only
// under -v or when the package fails.
func TestConsoleHandlers_ClosePromptlyWithAReasonAfterAPanic(t *testing.T) {
	logger := testLogger()
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	for kind, consoleType := range map[string]string{"console": "vm_serial", "vnc": "vm_vnc"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			scope := auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: consoleType}
			app := fiber.New()
			app.Get("/probe", func(c fiber.Ctx) error {
				validated := scope
				c.Locals(consoleScopeLocal, &validated)
				return c.Next()
			}, gateHandlerFor(kind, nil, jwtSvc, logger))
			port := startGateApp(t, app)

			dialer := *gorillaws.DefaultDialer
			dialer.Subprotocols = []string{subprotocolNegotiationName}
			start := time.Now()
			conn, resp, err := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/probe", port), http.Header{})
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = conn.Close() }()

			_ = conn.SetReadDeadline(time.Now().Add(closeDrainTimeout + 5*time.Second))
			_, _, err = conn.ReadMessage()
			var ce *gorillaws.CloseError
			if !errors.As(err, &ce) || ce.Code != gorillaws.CloseInternalServerErr || ce.Text != "internal error" {
				t.Fatalf("first frame: %v, want a Close 1011 with the reason \"internal error\"", err)
			}
			if took := time.Since(start); took > closeDrainTimeout-time.Second {
				t.Errorf("the Close came %v after the dial, want it well inside closeDrainTimeout", took)
			}
		})
	}
}
