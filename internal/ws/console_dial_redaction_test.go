package ws

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
)

// dialCanaryTicket is the ticket the stand-ins below mint: shaped like a
// vncproxy ticket — the VNC password qemu-server puts in front, then a PVEVNC
// ticket. The password ends in '"' and '\', which JSON and Go's quoting both
// rewrite, and it is full of characters url.QueryEscape rewrites, so each of
// its spellings has runs the others do not.
const dialCanaryTicket = `Q7ZK@2"\:PVEVNC:6FC0FFEE::Q2FuYXJ5+VGlja2V0/Zm9y+TmV4YXJh/VGVzdHM=`

// The token openSessionAt's cluster authenticates with, and the header it
// becomes. The secret is what a console must not repeat; the rest of the
// header is the token's ID, which the handlers log in their own right — the
// ticket's user and UPID name it — so the probe looks for the secret, which
// is also every secret byte of the header.
const (
	sessionTokenSecret = "fake-token-secret"
	sessionAuthHeader  = "PVEAPIToken=root@pam!nexara=" + sessionTokenSecret
)

// dialCanaryRuns lists every run of eight bytes of a session secret, raw,
// query-escaped or quoted, that text holds. The quoted spelling is also how
// the JSON log handler writes printable ASCII: both escape only '"' and '\'.
func dialCanaryRuns(text string) []string {
	var runs []string
	for _, secret := range []string{dialCanaryTicket, sessionTokenSecret} {
		quoted := strconv.Quote(secret)
		for _, spelling := range []string{secret, url.QueryEscape(secret), quoted[1 : len(quoted)-1]} {
			for i := 0; i+8 <= len(spelling); i++ {
				if run := spelling[i : i+8]; strings.Contains(text, run) {
					runs = append(runs, run)
				}
			}
		}
	}
	return runs
}

// lockedBuffer is a log sink the handler writes from its own goroutine while
// the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// debugLogger is built the way cmd/nexara/main.go builds production's logger,
// on slog.NewJSONHandler, at debug so that every line a handler writes is
// there to check.
func debugLogger(sink *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// canaryProxmox is a stand-in Proxmox that hands every vncproxy and termproxy
// POST to mint and every vncwebsocket request to onSocket.
func canaryProxmox(t *testing.T, mint, onSocket http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			(strings.HasSuffix(r.URL.Path, "/vncproxy") || strings.HasSuffix(r.URL.Path, "/termproxy")):
			mint(w, r)
		case strings.HasSuffix(r.URL.Path, "/vncwebsocket"):
			onSocket(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// answering is a mint that answers every ticket request with the JSON of
// data, as Proxmox wraps it.
func answering(t *testing.T, data map[string]any) http.HandlerFunc {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatalf("marshal the mint response: %v", err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// canaryMint is a vncproxy or termproxy answer carrying dialCanaryTicket.
func canaryMint() map[string]any {
	return map[string]any{
		"port":   "5900",
		"ticket": dialCanaryTicket,
		"upid":   "UPID:pve-01:00000001:00000001:00000001:vncproxy:100:root@pam:",
		"user":   "root@pam",
	}
}

// readToClose reads everything the browser is sent, through to the close and
// its reason.
func readToClose(browser *gorillaws.Conn) []string {
	var sent []string
	_ = browser.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		_, msg, err := browser.ReadMessage()
		if err != nil {
			return append(sent, err.Error())
		}
		sent = append(sent, string(msg))
	}
}

// consoleKinds is every kind of console, through the dial each one makes, and
// the lines its handler logs for a failed dial and a failed ticket request.
var consoleKinds = map[string]struct {
	kind       string
	scope      auth.ConsoleScope
	logged     string
	mintLogged string
}{
	"node shell, through DialTerminal": {"console",
		auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", Type: "node_shell"},
		"dial websocket failed", "proxy request failed"},
	"serial console, through DialVNCWebSocket": {"console",
		auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"},
		"dial websocket failed", "proxy request failed"},
	"VNC, through DialVNCWebSocket": {"vnc",
		auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"},
		"dial VNC websocket failed", "vncproxy request failed"},
}

// TestConsoleHandlers_DialFailureLogsNoSecret opens every kind of console
// against a stand-in Proxmox that mints a canary ticket and then refuses the
// vncwebsocket with a body repeating a secret: the ticket from the query, or
// the token from the Authorization header. Each handler logs the dial's
// failure; neither that line, nor anything else the handler logs or sends the
// browser, may hold any run of a secret.
func TestConsoleHandlers_DialFailureLogsNoSecret(t *testing.T) {
	// Positive controls for the probe: it sees a secret in a JSON log line,
	// and it sees each spelling on its own — the escaped password, and the
	// quoted one, neither of which shares a run with the raw spelling.
	var probe lockedBuffer
	slog.New(slog.NewJSONHandler(&probe, nil)).Error("x", "error", dialCanaryTicket)
	quoted := strconv.Quote(dialCanaryTicket)
	for what, text := range map[string]string{
		"a JSON log line":        probe.String(),
		"the escaped password":   url.QueryEscape(dialCanaryTicket)[:16],
		"the quoted password":    quoted[1:11],
		"the token's secret":     "t=" + sessionTokenSecret,
		"the Authorization line": "Authorization: " + sessionAuthHeader,
	} {
		if len(dialCanaryRuns(text)) == 0 {
			t.Fatalf("the probe cannot see a secret in %s (%q), so it could not see that leak", what, text)
		}
	}

	bodies := map[string]func(*http.Request) string{
		"the query":                func(r *http.Request) string { return "refused " + r.URL.RawQuery },
		"the Authorization header": func(r *http.Request) string { return "you sent " + r.Header.Get("Authorization") },
	}
	for name, tc := range consoleKinds {
		for echoing, body := range bodies {
			t.Run(name+", refused with a body repeating "+echoing, func(t *testing.T) {
				t.Parallel()
				var refusal atomic.Value
				pve := canaryProxmox(t, answering(t, canaryMint()), func(w http.ResponseWriter, r *http.Request) {
					b := body(r)
					refusal.Store(b)
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(b))
				})

				var logs lockedBuffer
				sent := readToClose(openSessionAt(t, tc.kind, tc.scope, pve.URL, "", debugLogger(&logs)))

				b, _ := refusal.Load().(string)
				if len(dialCanaryRuns(b[:min(len(b), 64)])) == 0 {
					t.Fatalf("precondition: the part of the refusal a dial error repeats holds no secret, so there was nothing to redact: %q", b)
				}
				logged := logs.String()
				if !strings.Contains(logged, tc.logged) {
					t.Fatalf("precondition: the handler logged no %q line, so there was no line to check:\n%s", tc.logged, logged)
				}
				if runs := dialCanaryRuns(logged); len(runs) > 0 {
					t.Errorf("the handler's log holds a secret (%q):\n%s", runs[0], logged)
				}
				for _, m := range sent {
					if runs := dialCanaryRuns(m); len(runs) > 0 {
						t.Errorf("the browser was sent a secret (%q): %s", runs[0], m)
					}
				}
			})
		}
	}
}

// TestConsoleHandlers_MintFailureLogsNoSecret refuses the ticket request
// itself, with an error body that repeats the request's Authorization header
// and runs on for 64 KiB. The client puts the whole body into the error it
// returns, and every kind of console logs that error; the line must hold no
// run of the token, and must stop well short of the body's end.
func TestConsoleHandlers_MintFailureLogsNoSecret(t *testing.T) {
	const bodyEnd = "END-OF-THE-ERROR-BODY"
	for name, tc := range consoleKinds {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var refusal atomic.Value
			pve := canaryProxmox(t, func(w http.ResponseWriter, r *http.Request) {
				b := "refused: you sent " + r.Header.Get("Authorization") + " " + strings.Repeat("p", 64<<10) + " " + bodyEnd
				refusal.Store(b)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(b))
			}, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("the handler dialed %s after its ticket request was refused", r.URL.Path)
				http.NotFound(w, r)
			})

			var logs lockedBuffer
			sent := readToClose(openSessionAt(t, tc.kind, tc.scope, pve.URL, "", debugLogger(&logs)))

			if b, _ := refusal.Load().(string); !strings.Contains(b, sessionTokenSecret) {
				t.Fatalf("precondition: the refusal did not repeat the token, so there was nothing to redact: %.120q", b)
			}
			logged := logs.String()
			if !strings.Contains(logged, tc.mintLogged) {
				t.Fatalf("precondition: the handler logged no %q line, so there was no line to check:\n%.2000s", tc.mintLogged, logged)
			}
			if runs := dialCanaryRuns(logged); len(runs) > 0 {
				t.Errorf("the handler's log holds a secret (%q):\n%.2000s", runs[0], logged)
			}
			if strings.Contains(logged, bodyEnd) {
				t.Errorf("the handler logged the refusal's body to its end: %d bytes of log", len(logged))
			}
			for _, m := range sent {
				if runs := dialCanaryRuns(m); len(runs) > 0 {
					t.Errorf("the browser was sent a secret (%q): %s", runs[0], m)
				}
			}
		})
	}
}

// TestConsoleHandlers_RelayLogsNoPeerCloseText opens a serial console, whose
// ticket carries the VNC password in front, and has the stand-in end the
// session with a close whose reason holds the ticket's first five bytes and
// the token. The relay logs what ended its read at debug; that line must say
// how the session closed and nothing the peer wrote. Five bytes is below any
// run a scrubber recognises, so only dropping the reason keeps them out.
func TestConsoleHandlers_RelayLogsNoPeerCloseText(t *testing.T) {
	fragment := dialCanaryTicket[:5]
	reason := "bye " + fragment + " " + sessionTokenSecret
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	upgrader := gorillaws.Upgrader{Subprotocols: []string{"binary"}}
	pve := canaryProxmox(t, answering(t, canaryMint()), func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.WriteControl(gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(gorillaws.CloseGoingAway, reason), time.Now().Add(time.Second))
		<-release
	})

	var logs lockedBuffer
	scope := auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"}
	browser := openSessionAt(t, "console", scope, pve.URL, "", debugLogger(&logs))
	readConnected(t, browser)
	_ = readToClose(browser)

	logged := logs.String()
	if !strings.Contains(logged, "proxmox read error") {
		t.Fatalf("precondition: the relay logged no read error, so there was no line to check:\n%s", logged)
	}
	if !strings.Contains(logged, "close 1001") {
		t.Errorf("the relay's line does not say how the session closed:\n%s", logged)
	}
	quoted := strconv.Quote(fragment)
	if strings.Contains(logged, fragment) || strings.Contains(logged, quoted[1:len(quoted)-1]) {
		t.Errorf("the relay's log keeps the ticket's first bytes from the peer's close:\n%s", logged)
	}
	if runs := dialCanaryRuns(logged); len(runs) > 0 {
		t.Errorf("the relay's log holds a secret (%q):\n%s", runs[0], logged)
	}
}

// TestVNCHandler_SendsThePasswordNotTheTicket checks what a VNC console hands
// the browser for noVNC's RFB authentication: the password PVE returned beside
// the ticket, when it returned one, and the ticket itself only from a PVE
// that returns no password, where the ticket is the password.
func TestVNCHandler_SendsThePasswordNotTheTicket(t *testing.T) {
	const password = "PW!8CHR2"
	cases := map[string]struct {
		mint func() map[string]any
		want string
	}{
		"a PVE that returns the password": {
			mint: func() map[string]any {
				m := canaryMint()
				m["ticket"] = password + ":PVEVNC:6FC0FFEE::Q2FuYXJ5+VGlja2V0/Zm9y+TmV4YXJh/VGVzdHM="
				m["password"] = password
				return m
			},
			want: password,
		},
		"a PVE that returns none": {
			mint: canaryMint,
			want: dialCanaryTicket,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			upgrader := gorillaws.Upgrader{Subprotocols: []string{"binary"}}
			pve := canaryProxmox(t, answering(t, tc.mint()), func(w http.ResponseWriter, r *http.Request) {
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
				<-release
			})

			scope := auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"}
			browser := openSessionAt(t, "vnc", scope, pve.URL, "", testLogger())
			_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, msg, err := browser.ReadMessage()
			if err != nil {
				t.Fatalf("reading the first message: %v", err)
			}
			var connected struct {
				Type     string `json:"type"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal(msg, &connected); err != nil || connected.Type != "connected" {
				t.Fatalf("first message = %s, want the session connected", msg)
			}
			if connected.Password != tc.want {
				t.Errorf("the browser was sent %q as the VNC password, want %q", connected.Password, tc.want)
			}
			if tc.want == password && strings.Contains(string(msg), "PVEVNC") {
				t.Errorf("the browser was sent the ticket as well as the password: %s", msg)
			}
		})
	}
}
