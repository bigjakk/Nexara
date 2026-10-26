package ws

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
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
// enough of its API to mint a ticket (the vncproxy or termproxy POST) and to
// serve the vncwebsocket the handler then dials. The gate tests stop at the
// cluster lookup; these go past it, into the relay.

// fakeProxmox is that stand-in. It records every request it is sent, as
// "METHOD path", and hands each vncwebsocket it accepts to onSocket, which
// decides how "Proxmox" behaves once a session is open.
type fakeProxmox struct {
	srv      *httptest.Server
	onSocket func(*gorillaws.Conn)

	mu   sync.Mutex
	seen []string
}

// requests is every request the stand-in has been sent so far, in order.
func (f *fakeProxmox) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func newFakeProxmox(t *testing.T, onSocket func(*gorillaws.Conn)) *fakeProxmox {
	t.Helper()
	f := &fakeProxmox{onSocket: onSocket}
	upgrader := gorillaws.Upgrader{Subprotocols: []string{"binary"}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost &&
			(strings.HasSuffix(r.URL.Path, "/vncproxy") || strings.HasSuffix(r.URL.Path, "/termproxy")):
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
	row := d.gateLookupDB.QueryRow(ctx, sql, args...)
	if isLivenessCheck(sql) {
		return row
	}
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
// browser's end of the connection. query is appended to the dial URL
// verbatim — TestConsoleHandlers_OpenWhatTheScopeNames uses it to name
// something else in every field.
func openSession(t *testing.T, kind string, scope auth.ConsoleScope, pve *fakeProxmox, query string) *gorillaws.Conn {
	t.Helper()
	return openSessionAt(t, kind, scope, pve.srv.URL, query, testLogger())
}

// openSessionAt is openSession against any stand-in Proxmox at apiURL, with
// the handler logging to logger.
func openSessionAt(t *testing.T, kind string, scope auth.ConsoleScope, apiURL, query string, logger *slog.Logger) *gorillaws.Conn {
	t.Helper()
	return openSessionWith(t, kind, scope, newSessionDB(t, scope, apiURL), credentialCheckEvery, query, logger)
}

// newSessionDB holds the scope's cluster, with its API at apiURL.
func newSessionDB(t *testing.T, scope auth.ConsoleScope, apiURL string) *sessionDB {
	t.Helper()
	secret, err := crypto.Encrypt("fake-token-secret", sessionKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return &sessionDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(scope.ClusterID),
		Name:                 "cluster01",
		ApiUrl:               apiURL,
		TokenID:              "root@pam!nexara",
		TokenSecretEncrypted: secret,
	}}
}

// openSessionWith is openSessionAt over the given database, re-checking the
// console's credential every checkEvery.
func openSessionWith(t *testing.T, kind string, scope auth.ConsoleScope, sdb *sessionDB, checkEvery time.Duration, query string, logger *slog.Logger) *gorillaws.Conn {
	t.Helper()
	queries := db.New(sdb)
	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	cfg := wsConfigWithSubprotocol(nil)
	consoleHandler := NewConsoleHandler(queries, sessionKey, jwtSvc, logger)
	consoleHandler.checkEvery = checkEvery
	handler := fiberws.New(consoleHandler.HandleConsole, cfg)
	if kind == "vnc" {
		vncHandler := NewVNCHandler(queries, sessionKey, jwtSvc, logger)
		vncHandler.checkEvery = checkEvery
		handler = fiberws.New(vncHandler.HandleVNC, cfg)
	}
	app := fiber.New()
	app.Get("/probe", standInGate(&scope), handler)
	port := startGateApp(t, app)

	dialer := *gorillaws.DefaultDialer
	dialer.Subprotocols = []string{subprotocolNegotiationName}
	dialer.HandshakeTimeout = 3 * time.Second
	conn, resp, err := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/probe%s", port, query), http.Header{})
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

			browser := openSession(t, kind, scope, pve, "")
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

			browser := openSession(t, kind, scope, pve, "")
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

// TestConsoleRelay_HeldToItsCredential: a console whose session or key has ended
// is refused at open, before Proxmox is asked for a ticket, and is closed when its
// credential ends while it is open — Proxmox first, then the browser, with the
// Close code the SPA stops on.
func TestConsoleRelay_HeldToItsCredential(t *testing.T) {
	scopes := map[string]auth.ConsoleScope{
		"vnc":     {ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"},
		"console": {ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"},
	}
	for kind, scope := range scopes {
		t.Run(kind+": refused at open", func(t *testing.T) {
			t.Parallel()
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) { _, _, _ = c.ReadMessage() })
			sdb := newSessionDB(t, scope, pve.srv.URL)
			sdb.setEnded()

			browser := openSessionWith(t, kind, scope, sdb, credentialCheckEvery, "", testLogger())
			_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, msg, err := browser.ReadMessage(); err != nil || !strings.Contains(string(msg), `"code":"session_ended"`) {
				t.Fatalf("first message = %s (%v), want the session_ended error", msg, err)
			}
			if _, _, err := browser.ReadMessage(); !gorillaws.IsCloseError(err, closeSessionEnded) {
				t.Fatalf("then: %v, want a Close %d", err, closeSessionEnded)
			}
			if got := pve.requests(); len(got) != 0 {
				t.Errorf("Proxmox was sent %v; nothing may be asked of it for an ended session", got)
			}
		})

		// Proxmox can take seconds to set a console up, and the next check is
		// credentialCheckEvery away: it is asked again before it is announced.
		t.Run(kind+": refused when it ends while Proxmox sets it up", func(t *testing.T) {
			t.Parallel()
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) { _, _, _ = c.ReadMessage() })
			sdb := newSessionDB(t, scope, pve.srv.URL)
			sdb.endAfterChecks(1)

			browser := openSessionWith(t, kind, scope, sdb, credentialCheckEvery, "", testLogger())
			_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, msg, err := browser.ReadMessage(); err != nil || !strings.Contains(string(msg), `"code":"session_ended"`) {
				t.Fatalf("first message = %s (%v), want the session_ended error, never connected", msg, err)
			}
			if got := pve.requests(); len(got) == 0 || !strings.HasSuffix(got[len(got)-1], "/vncwebsocket") {
				t.Errorf("Proxmox was sent %v; the second check must follow the console's setup", got)
			}
		})

		t.Run(kind+": closed when it ends while open", func(t *testing.T) {
			t.Parallel()
			closed := make(chan int, 1)
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) {
				c.SetCloseHandler(func(code int, _ string) error {
					closed <- code
					return c.WriteControl(gorillaws.CloseMessage, gorillaws.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
				})
				for {
					if _, _, err := c.ReadMessage(); err != nil {
						return
					}
				}
			})
			sdb := newSessionDB(t, scope, pve.srv.URL)
			browser := openSessionWith(t, kind, scope, sdb, 10*time.Millisecond, "", testLogger())
			readConnected(t, browser)

			sdb.setEnded()

			_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, _, err := browser.ReadMessage(); !gorillaws.IsCloseError(err, closeSessionEnded) {
				t.Fatalf("after the session ended: %v, want a Close %d", err, closeSessionEnded)
			}
			select {
			case <-closed:
			default:
				t.Error("Proxmox was never sent a Close; its leg must be cut first")
			}
		})
	}
}
