package ws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	fiberws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	gorillaws "github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These cases run a console session end to end against a stand-in Proxmox:
// enough of its API to mint a ticket (the vncproxy POST) and to serve the
// vncwebsocket the handler then dials. The gate tests stop at the cluster
// lookup; these go past it, into the relay.

// fakeProxmox is that stand-in. It hands each vncwebsocket it accepts to
// onSocket, which decides how "Proxmox" behaves once a session is open.
type fakeProxmox struct {
	srv      *httptest.Server
	onSocket func(*gorillaws.Conn)
}

func newFakeProxmox(t *testing.T, onSocket func(*gorillaws.Conn)) *fakeProxmox {
	t.Helper()
	f := &fakeProxmox{onSocket: onSocket}
	upgrader := gorillaws.Upgrader{Subprotocols: []string{"binary"}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/vncproxy"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"port":"5900","ticket":"PVEVNC:fake",`+
				`"upid":"UPID:pve-01:00000001:00000001:00000001:vncproxy:100:root@pam:","user":"root@pam"}}`)
		case strings.HasSuffix(r.URL.Path, "/vncwebsocket"):
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			f.onSocket(c)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// sessionKey encrypts the fake cluster's token secret the way the real key
// does; any 32-byte hex key will do.
var sessionKey = strings.Repeat("ab", 32)

// sessionDB answers GetCluster for the one cluster it holds — whose API URL
// is the fake Proxmox — and anything else with no rows.
type sessionDB struct {
	gateLookupDB
	cluster db.Cluster
}

func (d *sessionDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	_ = d.gateLookupDB.QueryRow(ctx, sql, args...)
	if len(args) == 1 {
		if id, ok := args[0].(uuid.UUID); ok && id == d.cluster.ID {
			return clusterRow{d.cluster}
		}
	}
	return gateNoRow{}
}

// clusterRow replays a db.Cluster through pgx.Row in the struct's field
// order, which is GetCluster's scan order: sqlc builds the struct from the
// table, and GetCluster selects every column in table order. It refuses a
// mismatch in count or type; two fields of one type swapped would still scan,
// silently.
type clusterRow struct{ c db.Cluster }

func (r clusterRow) Scan(dest ...any) error {
	v := reflect.ValueOf(r.c)
	if len(dest) != v.NumField() {
		return fmt.Errorf("clusterRow: GetCluster scans %d columns and db.Cluster has %d fields", len(dest), v.NumField())
	}
	for i, d := range dest {
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Pointer || dv.Elem().Type() != v.Field(i).Type() {
			return fmt.Errorf("clusterRow: column %d scans into %T, and db.Cluster field %d is %s",
				i, d, i, v.Field(i).Type())
		}
		dv.Elem().Set(v.Field(i))
	}
	return nil
}

// openSession serves the handler of the given kind behind a stand-in gate
// that hands it scope, dials it the way the SPA does, and returns the
// browser's end of the connection.
func openSession(t *testing.T, kind string, scope auth.ConsoleScope, pve *fakeProxmox) *gorillaws.Conn {
	t.Helper()
	secret, err := crypto.Encrypt("fake-token-secret", sessionKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	queries := db.New(&sessionDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(scope.ClusterID),
		Name:                 "cluster01",
		ApiUrl:               pve.srv.URL,
		TokenID:              "root@pam!nexara",
		TokenSecretEncrypted: secret,
	}})
	logger := testLogger()
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	cfg := wsConfigWithSubprotocol(nil)
	handler := fiberws.New(NewConsoleHandler(queries, sessionKey, jwtSvc, logger).HandleConsole, cfg)
	if kind == "vnc" {
		handler = fiberws.New(NewVNCHandler(queries, sessionKey, jwtSvc, logger).HandleVNC, cfg)
	}
	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		validated := scope
		c.Locals(consoleScopeLocal, &validated)
		return c.Next()
	}, handler)
	port := startGateApp(t, app)

	dialer := *gorillaws.DefaultDialer
	dialer.Subprotocols = []string{subprotocolNegotiationName}
	dialer.HandshakeTimeout = 3 * time.Second
	conn, resp, err := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/probe", port), http.Header{})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readConnected reads the first message of a session and requires it to be
// the handler's "connected".
func readConnected(t *testing.T, browser *gorillaws.Conn) {
	t.Helper()
	_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := browser.ReadMessage()
	if err != nil {
		t.Fatalf("reading the first message: %v", err)
	}
	if !strings.Contains(string(msg), `"type":"connected"`) {
		t.Fatalf("first message = %s, want the session to be connected", msg)
	}
}

// TestConsoleRelay_EndsWhenAPeerNeverAnswersTheClose drives sessions that one
// side ends and the other never acknowledges, and requires the server to let
// go of the browser's socket anyway.
//
// Each leg of the relay sends a Close to the connection it writes to when it
// stops, and it is the OTHER leg that reads that connection — so the other
// leg is the one waiting on the answer. Neither wait was bounded: a peer that
// never answered kept a leg in ReadMessage for as long as it stayed
// connected, the handler never got past wg.Wait, and nothing after it — the
// deferred close, pxConn.Close — ran. closeRelayLeg bounds the wait at
// closeDrainTimeout, from either end.
func TestConsoleRelay_EndsWhenAPeerNeverAnswersTheClose(t *testing.T) {
	scopes := map[string]auth.ConsoleScope{
		"vnc":     {ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"},
		"console": {ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"},
	}
	for kind, scope := range scopes {
		t.Run(kind+": Proxmox ends it and the browser never answers", func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) {
				_ = c.WriteMessage(gorillaws.CloseMessage,
					gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, ""))
				<-release // and read nothing, so answer nothing
			})
			// Lets the stand-in's socket goroutine return. httptest stops
			// tracking a hijacked connection, so nothing else would, and the
			// goroutine would outlive the test.
			t.Cleanup(func() { close(release) })

			browser := openSession(t, kind, scope, pve)
			browser.SetCloseHandler(func(int, string) error { return nil })
			readConnected(t, browser)
			if _, _, err := browser.ReadMessage(); !gorillaws.IsCloseError(err, gorillaws.CloseNormalClosure) {
				t.Fatalf("after Proxmox closed: %v, want the relay to pass the Close on", err)
			}
			if err := waitForEOF(browser.NetConn(), closeDrainTimeout+5*time.Second); err != nil {
				t.Fatal(err)
			}
		})

		t.Run(kind+": the browser ends it and Proxmox never answers", func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			// The stand-in records the Close the relay sends it and does not
			// answer — replacing gorilla's default close handler is how a
			// peer declines to.
			closed := make(chan int, 1)
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) {
				c.SetCloseHandler(func(code int, _ string) error {
					closed <- code
					return nil
				})
				_, _, _ = c.ReadMessage()
				<-release
			})
			t.Cleanup(func() { close(release) })

			browser := openSession(t, kind, scope, pve)
			readConnected(t, browser)
			if err := browser.WriteMessage(gorillaws.CloseMessage,
				gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, "")); err != nil {
				t.Fatalf("sending the Close: %v", err)
			}
			// The server's answer to that Close comes first; the EOF is what
			// has to follow it.
			if _, _, err := browser.ReadMessage(); !gorillaws.IsCloseError(err, gorillaws.CloseNormalClosure) {
				t.Fatalf("after the browser closed: %v, want the server's answering Close", err)
			}
			if err := waitForEOF(browser.NetConn(), closeDrainTimeout+5*time.Second); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-closed:
				if code != gorillaws.CloseNormalClosure {
					t.Errorf("Proxmox was sent a Close with code %d, want %d", code, gorillaws.CloseNormalClosure)
				}
			default:
				t.Error("Proxmox was never sent a Close; the relay leg ended without asking it to")
			}
		})
	}
}
