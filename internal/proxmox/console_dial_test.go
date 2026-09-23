package proxmox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Timing for the bound tests.
const (
	// stallBound is what a test shortens the bound under test to.
	stallBound = 150 * time.Millisecond
	// stallSlack is how far past stallBound a call may run before the test
	// calls it unbounded. Generous, because the box these run on is shared,
	// and all it has to tell apart is "returned at the bound" from "never
	// returned".
	stallSlack = 3 * time.Second
	// unhurried is the bound a test leaves long, so that a call which
	// returned inside stallBound+stallSlack cannot have been ended by it.
	unhurried = 20 * time.Second
)

// canaryTicket stands in for a vncproxy ticket wherever these tests need one
// that must not come back out: the eight-character VNC password qemu-server
// puts in front ("${password}:${ticket}", drawn from '!'..'`'), then a PVEVNC
// ticket. The password ends in '"' and '\', which Go's quoting and JSON both
// rewrite, and it is full of characters url.QueryEscape rewrites, so each of
// the three spellings has runs the others do not.
const canaryTicket = `Q7ZK@2"\:PVEVNC:6FC0FFEE::Q2FuYXJ5+VGlja2V0/Zm9y+TmV4YXJh/VGVzdHM=`

// The canary API token every consoleClient authenticates with.
const (
	canaryTokenID     = "root@pam!nexara"
	canaryTokenSecret = "c0ffee00-cafe-4bad-8eed-5ec2e7c0ffee"
)

// canaryAuthHeader is the Authorization header a consoleClient sends, spelled
// out here rather than taken from buildAuthHeader; the refusal cases check the
// header a stand-in really received against it.
const canaryAuthHeader = "PVEAPIToken=" + canaryTokenID + "=" + canaryTokenSecret

// canarySecrets is everything a console error must not repeat.
var canarySecrets = []string{canaryTicket, canaryTokenSecret, canaryAuthHeader}

// leakRun is the shortest run of a secret these tests call a leak. It is
// written out rather than taken from secretWindow on purpose: a probe that
// borrowed the production window would widen with it, and a scrubber that
// had stopped matching short runs would pass a probe that had stopped
// looking for them.
const leakRun = 8

// leakSpellings is every spelling the probe looks for a secret in: raw,
// query-escaped, and Go-quoted. They are computed here, not taken from
// secretSet, so a spelling production forgot is one the probe still sees.
// The Go-quoted one is also how slog's JSON handler writes a string of
// printable ASCII, which is what the secrets are: both escape only '"' and
// '\' there.
func leakSpellings(secret string) []string {
	quoted := strconv.Quote(secret)
	return []string{secret, url.QueryEscape(secret), quoted[1 : len(quoted)-1]}
}

// secretLeaks lists every run of leakRun bytes of a canary secret, in any
// spelling, that text holds.
func secretLeaks(text string) []string {
	var leaks []string
	for _, secret := range canarySecrets {
		for _, spelling := range leakSpellings(secret) {
			for i := 0; i+leakRun <= len(spelling); i++ {
				if run := spelling[i : i+leakRun]; strings.Contains(text, run) {
					leaks = append(leaks, run)
				}
			}
		}
	}
	return leaks
}

// consoleClient is a Client for baseURL, built the way production builds one,
// authenticating with the canary token.
func consoleClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := NewClient(ClientConfig{
		BaseURL:     baseURL,
		TokenID:     canaryTokenID,
		TokenSecret: canaryTokenSecret,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// stallServer accepts TCP connections and never says anything on them.
type stallServer struct {
	ln       net.Listener
	accepted atomic.Int32

	mu    sync.Mutex
	conns []net.Conn
}

func newStallServer(t *testing.T) *stallServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &stallServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	return s
}

// newConsoleStandIn serves the vncwebsocket a console dial opens: it upgrades
// the request and hands the connection to onSocket, which plays the node's
// part from there. onSocket returning ends the connection.
func newConsoleStandIn(t *testing.T, onSocket func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{Subprotocols: []string{"binary"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/vncwebsocket") {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		onSocket(conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// outcome is what one call returned, and how long it took.
type outcome[T any] struct {
	val     T
	err     error
	elapsed time.Duration
}

// within runs call and fails the test if it has not returned within limit. A
// call that never returns is the failure these tests exist to catch, so it
// has to end the test rather than hang it.
func within[T any](t *testing.T, limit time.Duration, call func() (T, error)) outcome[T] {
	t.Helper()
	done := make(chan outcome[T], 1)
	start := time.Now()
	go func() {
		val, err := call()
		done <- outcome[T]{val: val, err: err, elapsed: time.Since(start)}
	}()
	select {
	case o := <-done:
		return o
	case <-time.After(limit):
		t.Fatalf("the call had not returned after %v", limit)
		return outcome[T]{}
	}
}

// dialWithin is within for a console dial, closing whatever connection it
// returns when the test ends.
func dialWithin(t *testing.T, limit time.Duration, dial func() (*websocket.Conn, error)) outcome[*websocket.Conn] {
	t.Helper()
	o := within(t, limit, dial)
	if o.val != nil {
		t.Cleanup(func() { _ = o.val.Close() })
	}
	return o
}

// readWithin reads one message from conn, failing the test if none has come
// within limit. It sets no deadline of its own on conn: one would replace
// whatever the code under test left behind, which is what these reads are
// there to find.
func readWithin(t *testing.T, conn *websocket.Conn, limit time.Duration) ([]byte, error) {
	t.Helper()
	o := within(t, limit, func() ([]byte, error) {
		_, msg, err := conn.ReadMessage()
		return msg, err
	})
	return o.val, o.err
}

// lastBytes is the last n bytes of s, or all of it when it is shorter: for a
// failure message, which must not itself panic on a string shorter than it
// expected.
func lastBytes(s string, n int) string {
	return s[max(len(s)-n, 0):]
}

// isTimeout reports whether err is a deadline's: both the context a dial is
// given and a connection deadline report themselves this way.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// consoleDials is both console dials, as the ws handlers call them.
var consoleDials = map[string]func(c *Client, ticket string) (*websocket.Conn, error){
	"DialVNCWebSocket": func(c *Client, ticket string) (*websocket.Conn, error) {
		return c.DialVNCWebSocket(context.Background(), "pve-01", ticket, 5900, "qemu/100")
	},
	"DialTerminal": func(c *Client, ticket string) (*websocket.Conn, error) {
		return c.DialTerminal(context.Background(), "pve-01", ticket, 5900, "", "root@pam")
	},
}

// consoleMints is every ticket request, as a caller makes it.
var consoleMints = map[string]func(c *Client) error{
	"NodeTermProxy": func(c *Client) error { _, err := c.NodeTermProxy(context.Background(), "pve-01"); return err },
	"VMTermProxy":   func(c *Client) error { _, err := c.VMTermProxy(context.Background(), "pve-01", 100); return err },
	"CTTermProxy":   func(c *Client) error { _, err := c.CTTermProxy(context.Background(), "pve-01", 100); return err },
	"VMVNCProxy":    func(c *Client) error { _, err := c.VMVNCProxy(context.Background(), "pve-01", 100); return err },
	"CTVNCProxy":    func(c *Client) error { _, err := c.CTVNCProxy(context.Background(), "pve-01", 100); return err },
	"NodeVNCProxy":  func(c *Client) error { _, err := c.NodeVNCProxy(context.Background(), "pve-01"); return err },
}

// TestConsoleMint_StallEndsAtTheBound sends every ticket request to a node
// that accepts the TCP connection and never answers. Each has to give up at
// the mint bound — the client's own Timeout is left at its 30 seconds, and
// the cache's is five minutes — and not before it, which would mean something
// else ended it.
func TestConsoleMint_StallEndsAtTheBound(t *testing.T) {
	for name, mint := range consoleMints {
		t.Run(name, func(t *testing.T) {
			stall := newStallServer(t)
			c := consoleClient(t, "http://"+stall.ln.Addr().String())
			c.console = consoleTimeouts{mint: stallBound, handshake: unhurried, auth: unhurried}

			o := within(t, stallBound+stallSlack, func() (struct{}, error) { return struct{}{}, mint(c) })
			if o.err == nil {
				t.Fatal("the request succeeded against a node that never answered")
			}
			if stall.accepted.Load() == 0 {
				t.Fatalf("the stand-in never accepted a connection, so the request did not stall where this test means it to: %v", o.err)
			}
			if !errors.Is(o.err, ErrConnectionFailed) || !strings.Contains(o.err.Error(), context.DeadlineExceeded.Error()) {
				t.Errorf("err = %v, want the request's context deadline", o.err)
			}
			if o.elapsed < stallBound {
				t.Errorf("the request gave up after %v, inside the %v bound: something other than the bound ended it (%v)",
					o.elapsed, stallBound, o.err)
			}
		})
	}
}

// TestConsoleDial_HandshakeStallEndsAtTheBound points both dials at a node
// that accepts the TCP connection and then says nothing. Over wss the dial
// waits in the TLS handshake, over ws for the upgrade's answer; the handshake
// bound covers both, and each dial has to give up at it — not before, which
// would mean something else ended it, and not long after.
func TestConsoleDial_HandshakeStallEndsAtTheBound(t *testing.T) {
	for name, dial := range consoleDials {
		for _, scheme := range []string{"https", "http"} {
			t.Run(name+"/"+scheme, func(t *testing.T) {
				stall := newStallServer(t)
				c := consoleClient(t, scheme+"://"+stall.ln.Addr().String())
				c.console = consoleTimeouts{mint: unhurried, handshake: stallBound, auth: unhurried}

				r := dialWithin(t, stallBound+stallSlack, func() (*websocket.Conn, error) {
					return dial(c, "PVEVNC:stall")
				})
				if r.err == nil {
					t.Fatal("the dial succeeded against a node that never answered")
				}
				if stall.accepted.Load() == 0 {
					t.Fatalf("the stand-in never accepted a connection, so the dial did not stall where this test means it to: %v", r.err)
				}
				if !isTimeout(r.err) {
					t.Errorf("err = %v, want the handshake bound's timeout", r.err)
				}
				if r.elapsed < stallBound {
					t.Errorf("the dial gave up after %v, inside the %v bound: something other than the bound ended it (%v)",
						r.elapsed, stallBound, r.err)
				}
			})
		}
	}
}

// TestDialTerminal_OKStallEndsAtTheAuthBound opens a terminal on a node that
// upgrades the connection, takes the ticket line, and never answers it —
// what a node whose API daemon never answers termproxy's authenticate() looks
// like from here. The handshake bound is left long, so only the auth bound
// can end the wait inside the test's limit. DialTerminal then has to close
// the connection it gave up on: the stand-in's next read has to end soon
// after, where a connection left open would hold it.
func TestDialTerminal_OKStallEndsAtTheAuthBound(t *testing.T) {
	gotLine := make(chan string, 1)
	ended := make(chan struct{})
	srv := newConsoleStandIn(t, func(conn *websocket.Conn) {
		_, line, err := conn.ReadMessage()
		if err != nil {
			return
		}
		gotLine <- string(line)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(ended)
				return
			}
		}
	})

	c := consoleClient(t, srv.URL)
	c.console = consoleTimeouts{mint: unhurried, handshake: unhurried, auth: stallBound}

	r := dialWithin(t, stallBound+stallSlack, func() (*websocket.Conn, error) {
		return c.DialTerminal(context.Background(), "pve-01", "PVEVNC:okstall", 5900, "", "root@pam")
	})
	select {
	case line := <-gotLine:
		if line != "root@pam:PVEVNC:okstall\n" {
			t.Fatalf("the stand-in received %q, want the ticket line", line)
		}
	default:
		t.Fatalf("the stand-in never received the ticket line, so the dial did not stall where this test means it to: %v", r.err)
	}
	if r.err == nil {
		t.Fatal("DialTerminal succeeded without an OK")
	}
	if !isTimeout(r.err) {
		t.Errorf("err = %v, want the auth bound's timeout", r.err)
	}
	if r.elapsed < stallBound {
		t.Errorf("DialTerminal gave up after %v, inside the %v auth bound: something other than the bound ended it (%v)",
			r.elapsed, stallBound, r.err)
	}
	select {
	case <-ended:
	case <-time.After(stallSlack):
		t.Errorf("the connection was still open %v after DialTerminal gave up on it", stallSlack)
	}
}

// TestDialTerminal_WriteStallEndsAtTheAuthBound opens a terminal on a node
// that upgrades the connection and then reads nothing. The ticket line is
// normally far too short for that to hold the write, so the test sends a user
// name no socket buffer holds; the auth bound has to end the write as it ends
// the read.
func TestDialTerminal_WriteStallEndsAtTheAuthBound(t *testing.T) {
	release := make(chan struct{})
	srv := newConsoleStandIn(t, func(*websocket.Conn) { <-release })
	t.Cleanup(func() { close(release) })

	c := consoleClient(t, srv.URL)
	c.console = consoleTimeouts{mint: unhurried, handshake: unhurried, auth: stallBound}
	user := strings.Repeat("u", 32<<20)

	r := dialWithin(t, stallBound+stallSlack, func() (*websocket.Conn, error) {
		return c.DialTerminal(context.Background(), "pve-01", "PVEVNC:writestall", 5900, "", user)
	})
	if r.err == nil {
		t.Fatal("DialTerminal succeeded against a node that read nothing")
	}
	if !strings.HasPrefix(r.err.Error(), "send vnc auth") {
		t.Fatalf("err = %v: the ticket line fitted in the socket buffers, so the write never stalled and this test proves nothing", r.err)
	}
	if !isTimeout(r.err) {
		t.Errorf("err = %v, want the auth bound's timeout", r.err)
	}
	if r.elapsed < stallBound {
		t.Errorf("the write gave up after %v, inside the %v auth bound: something other than the bound ended it (%v)",
			r.elapsed, stallBound, r.err)
	}
}

// TestDialTerminal_HandsOverAConnectionWithNothingLeftOn uses a terminal
// session past every limit the ticket exchange set: output arriving well
// after its deadline, a frame far larger than its read limit, and a write
// long after the deadline it put on writes. Any of them left behind would
// break a node shell in production — at the deadline, or at its first large
// frame.
func TestDialTerminal_HandsOverAConnectionWithNothingLeftOn(t *testing.T) {
	const later = "terminal output, long after the OK"
	large := strings.Repeat("b", 1<<20) // far past termproxyReadLimit
	okSent := make(chan time.Time, 1)
	fromClient := make(chan string, 1)
	srv := newConsoleStandIn(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("OK")); err != nil {
			return
		}
		okSent <- time.Now()
		time.Sleep(3 * stallBound)
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte(later)); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte(large)); err != nil {
			return
		}
		if _, msg, err := conn.ReadMessage(); err == nil {
			fromClient <- string(msg)
		}
		_, _, _ = conn.ReadMessage() // until the test closes its end
	})

	c := consoleClient(t, srv.URL)
	c.console = consoleTimeouts{mint: unhurried, handshake: unhurried, auth: stallBound}
	r := dialWithin(t, stallSlack, func() (*websocket.Conn, error) {
		return c.DialTerminal(context.Background(), "pve-01", "PVEVNC:handover", 5900, "", "root@pam")
	})
	if r.err != nil {
		t.Fatalf("DialTerminal: %v", r.err)
	}
	var sent time.Time
	select {
	case sent = <-okSent:
	case <-time.After(stallSlack):
		t.Fatal("the stand-in never sent its OK")
	}

	msg, err := readWithin(t, r.val, stallSlack)
	if err != nil {
		t.Fatalf("reading the session after the OK: %v — the exchange's read deadline was left on the connection", err)
	}
	if string(msg) != later {
		t.Fatalf("read %q, want %q", msg, later)
	}
	// Measured from when the stand-in sent the OK, which is after the
	// deadline was set: arriving later than stallBound after it puts this
	// read past the deadline, however long the dial took to return.
	if since := time.Since(sent); since < stallBound {
		t.Fatalf("the output arrived %v after the OK, inside the %v auth bound, so this read cannot show the deadline was cleared", since, stallBound)
	}

	msg, err = readWithin(t, r.val, stallSlack)
	if err != nil {
		t.Fatalf("reading a %d-byte frame: %v — the exchange's read limit was left on the connection", len(large), err)
	}
	if len(msg) != len(large) {
		t.Fatalf("read %d bytes, want %d", len(msg), len(large))
	}

	if err := r.val.WriteMessage(websocket.TextMessage, []byte("keystrokes")); err != nil {
		t.Fatalf("writing to the session after the OK: %v — the exchange's write deadline was left on the connection", err)
	}
	select {
	case got := <-fromClient:
		if got != "keystrokes" {
			t.Fatalf("the stand-in received %q, want %q", got, "keystrokes")
		}
	case <-time.After(stallSlack):
		t.Fatal("the stand-in never received the write")
	}
}

// TestDialTerminal_ReadsNoMoreThanAnOKNeeds pins the read limit at pveproxy's
// largest frame, 128 KiB, from both sides: an "OK" in a frame of exactly that
// size — it can share its frame with the first output — still opens the
// session, and a frame one byte larger is refused without being read into
// memory. The sizes are written out rather than taken from termproxyReadLimit,
// so a limit that moves either way fails one of the two.
func TestDialTerminal_ReadsNoMoreThanAnOKNeeds(t *testing.T) {
	const pveproxyMaxFrame = 128 << 10
	answer := func(reply string) *httptest.Server {
		return newConsoleStandIn(t, func(conn *websocket.Conn) {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte(reply)); err != nil {
				return
			}
			_, _, _ = conn.ReadMessage()
		})
	}

	t.Run("an OK in a frame of exactly pveproxy's largest is taken", func(t *testing.T) {
		okAndOutput := "OK" + strings.Repeat("o", pveproxyMaxFrame-2)
		conn, err := consoleDials["DialTerminal"](consoleClient(t, answer(okAndOutput).URL), "PVEVNC:largest-ok")
		if err != nil {
			t.Fatalf("DialTerminal refused an OK in a %d-byte frame, which pveproxy can send: %v", len(okAndOutput), err)
		}
		_ = conn.Close()
	})

	t.Run("a frame one byte larger than pveproxy's largest is refused", func(t *testing.T) {
		over := strings.Repeat("h", pveproxyMaxFrame+1)
		_, err := consoleDials["DialTerminal"](consoleClient(t, answer(over).URL), "PVEVNC:over")
		if !errors.Is(err, websocket.ErrReadLimit) {
			t.Fatalf("err = %v, want the read limit's refusal of a %d-byte frame", err, len(over))
		}
		if strings.Contains(err.Error(), strings.Repeat("h", leakRun)) {
			t.Errorf("err repeats the answer it refused: %.200s", err)
		}
	})
}

// TestConsoleDial_ProductionClientsUseTheRealBounds covers every way
// production gets a client: NewClient, with each Timeout it is configured
// with. They all use the real 30-second bounds — the cache's five-minute
// Timeout included, which the bounds deliberately do not follow — and a
// Client literal that sets no bounds is bounded all the same, rather than
// handing gorilla a zero it reads as "no timeout".
func TestConsoleDial_ProductionClientsUseTheRealBounds(t *testing.T) {
	built := func(timeout time.Duration) *Client {
		c, err := NewClient(ClientConfig{
			BaseURL:     "https://192.0.2.10:8006",
			TokenID:     canaryTokenID,
			TokenSecret: canaryTokenSecret,
			Timeout:     timeout,
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		return c
	}
	cases := map[string]*Client{
		"NewClient with no Timeout":                         built(0),
		"NewClient as the ws handlers build one":            built(30 * time.Second),
		"NewClient as the ClientCache builds one":           built(CachedClientTimeout),
		"a Client literal, with no bounds and no transport": {apiClient: &apiClient{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := c.consoleWaits()
			want := consoleTimeouts{mint: 30 * time.Second, handshake: 30 * time.Second, auth: 30 * time.Second}
			if got != want {
				t.Errorf("console bounds = %+v, want %+v", got, want)
			}
			ctx, cancel := c.mintContext(context.Background())
			defer cancel()
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
				t.Errorf("a mint's context deadline is %v away (set: %v), want 30s", time.Until(deadline), ok)
			}
			d := c.consoleDialer()
			if d.HandshakeTimeout != 30*time.Second {
				t.Errorf("the dialer's HandshakeTimeout = %v, want 30s", d.HandshakeTimeout)
			}
			if d.TLSClientConfig != c.tlsCfg {
				t.Error("the dialer does not use the client's own tls.Config, so it skips the certificate check every API request makes")
			}
		})
	}
}

// renderings is every way err prints — Error, %v, %+v and %#v — and every way
// everything it wraps prints, following both Unwrap shapes all the way down,
// then the lines production's logger and the text handler write for it.
func renderings(err error) map[string]string {
	out := map[string]string{}
	pending := []error{err}
	for depth := 0; len(pending) > 0; depth++ {
		e := pending[0]
		pending = pending[1:]
		if e == nil {
			continue
		}
		out[fmt.Sprintf("link %d (%T) Error()", depth, e)] = e.Error()
		out[fmt.Sprintf("link %d (%T) %%v", depth, e)] = fmt.Sprintf("%v", e)
		out[fmt.Sprintf("link %d (%T) %%+v", depth, e)] = fmt.Sprintf("%+v", e)
		out[fmt.Sprintf("link %d (%T) %%#v", depth, e)] = fmt.Sprintf("%#v", e)
		if next := errors.Unwrap(e); next != nil {
			pending = append(pending, next)
		}
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			pending = append(pending, joined.Unwrap()...)
		}
	}
	// cmd/nexara/main.go builds production's logger on slog.NewJSONHandler;
	// the text handler dispatches differently, so it is checked as well.
	var jsonLine, textLine bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonLine, nil)).Error("dial websocket failed", "error", err)
	slog.New(slog.NewTextHandler(&textLine, nil)).Error("dial websocket failed", "error", err)
	out["slog JSON line"] = jsonLine.String()
	out["slog text line"] = textLine.String()
	return out
}

// assertNoSecrets fails for every rendering of err that holds a run of a
// canary secret.
func assertNoSecrets(t *testing.T, err error) {
	t.Helper()
	for where, text := range renderings(err) {
		if leaks := secretLeaks(text); len(leaks) > 0 {
			t.Errorf("%s repeats a secret (%q): %s", where, leaks[0], text)
		}
	}
}

// hidingError prints less than it wraps: its own text is clean, and what it
// wraps is not. Nothing in the dial path is known to build one; it is the
// shape that shows whether a check reads past the outermost text.
type hidingError struct{ inner error }

func (e hidingError) Error() string { return "dial failed" }
func (e hidingError) Unwrap() error { return e.inner }

// hidingJoin is hidingError for the other Unwrap shape: several wrapped
// errors behind a text that names none of them.
type hidingJoin struct{ errs []error }

func (e hidingJoin) Error() string   { return "several things failed" }
func (e hidingJoin) Unwrap() []error { return e.errs }

// plusVError carries the ticket only in its %+v rendering.
type plusVError struct{}

func (plusVError) Error() string { return "dial failed" }
func (plusVError) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('+') {
		_, _ = fmt.Fprint(f, "dial failed: "+canaryTicket)
		return
	}
	_, _ = fmt.Fprint(f, "dial failed")
}

// plainVError carries the ticket only in its plain %v rendering, which is how
// %w renders it inside an error that wraps it.
type plainVError struct{}

func (plainVError) Error() string { return "dial failed" }
func (plainVError) Format(f fmt.State, verb rune) {
	if verb == 'v' && !f.Flag('+') && !f.Flag('#') {
		_, _ = fmt.Fprint(f, "dial failed: "+canaryTicket)
		return
	}
	_, _ = fmt.Fprint(f, "dial failed")
}

// TestSecretLeakProbe_SeesEverySpelling is the positive control for
// assertNoSecrets: every route the redaction cases rely on it to see down has
// to be one it reports. Without this, a probe that had stopped looking would
// pass every case below.
func TestSecretLeakProbe_SeesEverySpelling(t *testing.T) {
	quoted := strconv.Quote(canaryTicket)
	quoted = quoted[1 : len(quoted)-1]
	cases := map[string]error{
		"the raw ticket": errors.New("auth line root@pam:" + canaryTicket),
		// The escaped password alone: every run of it holds a '%', so only a
		// probe that looks for the escaped spelling itself can see it. The
		// whole escaped ticket would not show that — its hex and base64
		// stretches are runs of the raw spelling too.
		"the escaped password alone": errors.New("GET /?vncticket=" + url.QueryEscape(canaryTicket)[:16]),
		// The quoted password alone, which is how a '"' or '\' in it reaches
		// a %q-quoted error or a JSON log line: every run of it holds a
		// backslash the other spellings do not have.
		"the quoted password alone":     errors.New(`status "` + quoted[:10] + `"`),
		"the token's secret":            errors.New("token " + canaryTokenSecret),
		"the Authorization header":      errors.New("Authorization: " + canaryAuthHeader),
		"a run from the middle":         errors.New("reason: " + canaryTicket[20:20+leakRun]),
		"only in a wrapped link":        hidingError{inner: errors.New(canaryTicket)},
		"only in one of a list":         hidingJoin{errs: []error{errors.New("first"), errors.New(canaryTicket)}},
		"only in its %+v":               plusVError{},
		"only in its %v":                plainVError{},
		"only in a wrapped link's %+v":  hidingError{inner: plusVError{}},
		"only in a wrapped link's %v":   hidingError{inner: plainVError{}},
		"only in production's log line": errors.New(canaryTicket),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			for where, text := range renderings(err) {
				if name == "only in production's log line" && where != "slog JSON line" {
					continue
				}
				if len(secretLeaks(text)) > 0 {
					return
				}
			}
			t.Fatal("the probe saw no secret in an error that carries one")
		})
	}
}

// TestConsoleDial_ErrorsRepeatNoSecret drives every route by which a console
// dial's error could repeat a secret — the canary ticket, or the canary token
// in either form — and requires that no rendering of the error, its text,
// anything it wraps, or the log line it becomes, holds any run of one. Each
// case that relies on a peer to put a secret into the error first shows, with
// a twin that skips the redaction, that the peer really does.
func TestConsoleDial_ErrorsRepeatNoSecret(t *testing.T) {
	escaped := url.QueryEscape(canaryTicket)
	quoted := strconv.Quote(canaryTicket)
	quoted = quoted[1 : len(quoted)-1]

	// refusing answers every vncwebsocket with a 401 whose body is what
	// body makes of the request, and records what it sent.
	refusing := func(t *testing.T, body func(*http.Request) string) (*httptest.Server, *atomic.Value) {
		var sent atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := body(r)
			sent.Store(b)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, b)
		}))
		t.Cleanup(srv.Close)
		return srv, &sent
	}

	t.Run("a refused upgrade whose body repeats the query", func(t *testing.T) {
		for name, dial := range consoleDials {
			t.Run(name, func(t *testing.T) {
				srv, sent := refusing(t, func(r *http.Request) string { return "refused " + r.URL.RawQuery })
				_, err := dial(consoleClient(t, srv.URL), canaryTicket)
				if err == nil {
					t.Fatal("the dial succeeded against a refusal")
				}
				if body, _ := sent.Load().(string); !strings.Contains(body[:min(len(body), maxEchoedBytes)], escaped[:leakRun]) {
					t.Fatalf("precondition: the echoed part of the refusal holds no run of the ticket, so there was nothing to redact: %q", body)
				}
				assertNoSecrets(t, err)
				if !errors.Is(err, websocket.ErrBadHandshake) {
					t.Errorf("err = %v, want ErrBadHandshake still reachable: nothing it wraps held a secret, so nothing needed flattening", err)
				}
				if !strings.Contains(err.Error(), "status 401") {
					t.Errorf("err = %v, want the refusal's status kept", err)
				}
			})
		}
	})

	t.Run("a refused upgrade whose body repeats the Authorization header", func(t *testing.T) {
		var got atomic.Value
		srv, _ := refusing(t, func(r *http.Request) string {
			got.Store(r.Header.Get("Authorization"))
			return "you sent " + r.Header.Get("Authorization")
		})
		_, err := consoleDials["DialVNCWebSocket"](consoleClient(t, srv.URL), canaryTicket)
		if err == nil {
			t.Fatal("the dial succeeded against a refusal")
		}
		if header, _ := got.Load().(string); header != canaryAuthHeader {
			t.Fatalf("precondition: the dial sent Authorization %q, not the canary header the probe looks for", header)
		}
		assertNoSecrets(t, err)
	})

	// The echo cap cuts the body a few bytes into a secret: fewer than any
	// run the scrubber recognises, so only the trim of a cut tail removes
	// them. At the front of a vncproxy ticket they are VNC password
	// characters; at the front of the token, the start of its secret.
	for name, secretText := range map[string]string{
		"the raw ticket":     canaryTicket,
		"the escaped ticket": escaped,
		"the token's secret": canaryTokenSecret,
	} {
		t.Run("a refused upgrade whose body the echo cap cuts a few bytes into "+name, func(t *testing.T) {
			const kept = 5
			if maxEchoedBytes <= kept {
				t.Fatalf("precondition: the echo cap, %d bytes, leaves no room for padding before the %d bytes this case keeps", maxEchoedBytes, kept)
			}
			fragment := secretText[:kept]
			pad := strings.Repeat("x", maxEchoedBytes-kept)
			if strings.Contains(pad, fragment) {
				t.Fatalf("precondition: the padding itself holds %q", fragment)
			}
			srv, _ := refusing(t, func(*http.Request) string { return pad + secretText + " and more" })
			for dialName, dial := range consoleDials {
				_, err := dial(consoleClient(t, srv.URL), canaryTicket)
				if err == nil {
					t.Fatalf("%s: the dial succeeded against a refusal", dialName)
				}
				for where, text := range renderings(err) {
					if strings.Contains(text, fragment) {
						t.Errorf("%s: %s keeps the first %d bytes of %s, %q: %s", dialName, where, kept, name, fragment, text)
					}
				}
				if !strings.Contains(err.Error(), strings.Repeat("x", 32)) {
					t.Errorf("%s: err = %v, want the body echoed up to the cut", dialName, err)
				}
			}
		})
	}

	// peer serves one TCP connection at a time, answering each with what
	// reply makes of the request line it read.
	peer := func(t *testing.T, reply func(requestLine string) string) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					line, err := bufio.NewReader(c).ReadString('\n')
					if err != nil {
						return
					}
					_, _ = c.Write([]byte(reply(line)))
				}(c)
			}
		}()
		return ln.Addr().String()
	}
	// twin dials addr with plain gorilla and a ticket in the URL, and returns
	// the error gorilla itself gives: what the dial had to redact.
	twin := func(addr string) error {
		dialer := websocket.Dialer{HandshakeTimeout: stallSlack}
		_, _, err := dialer.Dial("ws://"+addr+"/api2/json/nodes/pve-01/vncwebsocket?vncticket="+escaped, nil)
		return err
	}

	t.Run("a peer that echoes the request line instead of answering", func(t *testing.T) {
		addr := peer(t, func(line string) string { return line })
		if raw := twin(addr); raw == nil || !strings.Contains(raw.Error(), escaped) {
			t.Fatalf("precondition: gorilla's own error does not carry the ticket here, so there was nothing to redact: %v", raw)
		}
		for name, dial := range consoleDials {
			t.Run(name, func(t *testing.T) {
				_, err := dial(consoleClient(t, "http://"+addr), canaryTicket)
				if err == nil {
					t.Fatal("the dial succeeded against a peer that never answered HTTP")
				}
				assertNoSecrets(t, err)
				if !strings.Contains(err.Error(), "malformed HTTP") {
					t.Errorf("err = %v, want gorilla's complaint kept, with only the ticket taken out", err)
				}
			})
		}
	})

	// net/http quotes a malformed status code with %q, which rewrites the
	// '"' and '\' in the password: only the quoted spelling finds it there.
	t.Run("a peer that answers with the raw ticket as its status code", func(t *testing.T) {
		addr := peer(t, func(string) string { return "HTTP/1.1 " + canaryTicket + " x\r\n\r\n" })
		if raw := twin(addr); raw == nil || !strings.Contains(raw.Error(), quoted) {
			t.Fatalf("precondition: gorilla's own error does not carry the quoted ticket here, so there was nothing to redact: %v", raw)
		}
		_, err := consoleDials["DialTerminal"](consoleClient(t, "http://"+addr), canaryTicket)
		if err == nil {
			t.Fatal("the dial succeeded against a malformed answer")
		}
		assertNoSecrets(t, err)
	})

	t.Run("a peer whose status line runs past the error cap", func(t *testing.T) {
		const tail = "END-OF-A-VERY-LONG-STATUS-LINE"
		long := strings.Repeat("s", 4<<10) + tail
		addr := peer(t, func(string) string { return "HTTP/1.1 " + long + "\r\n\r\n" })
		if raw := twin(addr); raw == nil || !strings.Contains(raw.Error(), tail) {
			t.Fatalf("precondition: gorilla's own error does not repeat the whole line, so there was nothing to cap: %v", raw)
		}
		_, err := consoleDials["DialVNCWebSocket"](consoleClient(t, "http://"+addr), canaryTicket)
		if err == nil {
			t.Fatal("the dial succeeded against a malformed answer")
		}
		if strings.Contains(err.Error(), tail) || len(err.Error()) > 2*maxErrorText {
			t.Errorf("err is %d bytes and repeats the line to its end: %.200s…", len(err.Error()), err)
		}
	})

	t.Run("a dial refused outright, where the only secret is in the URL", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close() // nothing listens there now

		for name, dial := range consoleDials {
			t.Run(name, func(t *testing.T) {
				_, err := dial(consoleClient(t, "http://"+addr), canaryTicket)
				if err == nil {
					t.Fatal("the dial succeeded with nothing listening")
				}
				// Where the ticket would be: the error names the URL.
				if !strings.Contains(err.Error(), "/vncwebsocket?port=5900&vncticket=") {
					t.Fatalf("precondition: err = %v, want it to show the URL it dialled", err)
				}
				assertNoSecrets(t, err)
				if !strings.Contains(err.Error(), "vncticket="+redactedSecret) {
					t.Errorf("err = %v, want the URL shown with the ticket replaced", err)
				}
			})
		}
	})

	t.Run("termproxy answers the ticket line with the ticket line", func(t *testing.T) {
		var echoed atomic.Value
		srv := newConsoleStandIn(t, func(conn *websocket.Conn) {
			_, line, err := conn.ReadMessage()
			if err != nil {
				return
			}
			echoed.Store(string(line))
			_ = conn.WriteMessage(websocket.BinaryMessage, line)
			_, _, _ = conn.ReadMessage()
		})
		_, err := consoleDials["DialTerminal"](consoleClient(t, srv.URL), canaryTicket)
		if err == nil {
			t.Fatal("DialTerminal took the ticket line for an OK")
		}
		if line, _ := echoed.Load().(string); !strings.Contains(line, canaryTicket) {
			t.Fatalf("precondition: the answer did not repeat the ticket, so there was nothing to redact: %q", line)
		}
		assertNoSecrets(t, err)
		if !strings.Contains(err.Error(), "vnc auth failed") {
			t.Errorf("err = %v, want the refusal reported", err)
		}
	})

	// A close reason with a few bytes of the ticket in it — fewer than any
	// run the scrubber recognises, and VNC password characters — so it is
	// only kept out by dropping the reason, which is what has to happen.
	t.Run("termproxy closes with the first bytes of the ticket as its reason", func(t *testing.T) {
		fragment := canaryTicket[:5]
		reason := "bye " + fragment
		srv := newConsoleStandIn(t, func(conn *websocket.Conn) {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason), time.Now().Add(time.Second))
			_, _, _ = conn.ReadMessage()
		})

		// The twin: a plain client reads the reason, fragment and all.
		raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/vncwebsocket", nil)
		if err != nil {
			t.Fatalf("precondition: dialing the stand-in: %v", err)
		}
		_ = raw.WriteMessage(websocket.TextMessage, []byte("root@pam:x\n"))
		var twinClose *websocket.CloseError
		if _, _, err := raw.ReadMessage(); !errors.As(err, &twinClose) || !strings.Contains(twinClose.Text, fragment) {
			t.Fatalf("precondition: the peer's close did not carry the fragment, so there was nothing to drop: %v", err)
		}
		_ = raw.Close()

		_, err = consoleDials["DialTerminal"](consoleClient(t, srv.URL), canaryTicket)
		if err == nil {
			t.Fatal("DialTerminal succeeded without an OK")
		}
		for where, text := range renderings(err) {
			if strings.Contains(text, fragment) {
				t.Errorf("%s keeps the reason's %q: %s", where, fragment, text)
			}
		}
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "" {
			t.Errorf("err = %v, want the close reported by its code alone", err)
		}
	})
}

// TestSecretSet_Scrub pins what the scrubber replaces and what it leaves.
func TestSecretSet_Scrub(t *testing.T) {
	secrets := newSecretSet(canarySecrets...)
	escaped := url.QueryEscape(canaryTicket)
	quoted := strconv.Quote(canaryTicket)
	quoted = quoted[1 : len(quoted)-1]
	cases := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"the raw ticket", "auth line:" + canaryTicket + "\n", "auth line:REDACTED\n", true},
		{"the query-escaped ticket in a URL", "ws://h/v?port=5900&vncticket=" + escaped + " x",
			"ws://h/v?port=5900&vncticket=REDACTED x", true},
		{"the Go-quoted ticket", `code "` + quoted + `"`, `code "REDACTED"`, true},
		{"the token's secret", "token " + canaryTokenSecret + " refused", "token REDACTED refused", true},
		{"the Authorization header", "Authorization: " + canaryAuthHeader, "Authorization: REDACTED", true},
		{"every spelling, twice each", canaryTicket + "|" + escaped + "|" + quoted + "|" + canaryTicket + "|" + escaped + "|" + quoted,
			"REDACTED|REDACTED|REDACTED|REDACTED|REDACTED|REDACTED", true},
		// Eight and seven are written out, not taken from secretWindow: these
		// two cases pin the window, and one derived from it would move with it.
		{"a run of eight bytes, from the middle", "a " + canaryTicket[30:38] + " b", "a REDACTED b", true},
		{"a run of seven bytes is left", "a " + canaryTicket[30:37] + " b", "a " + canaryTicket[30:37] + " b", false},
		{"PVE's own refusal, which shares PVEVNC with the ticket", "permission denied - invalid PVEVNC ticket",
			"permission denied - invalid PVEVNC ticket", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := secrets.scrub(tc.in)
			if got != tc.want || changed != tc.changed {
				t.Errorf("scrub(%q) = (%q, %v), want (%q, %v)", tc.in, got, changed, tc.want, tc.changed)
			}
		})
	}

	t.Run("an empty secret is no secret", func(t *testing.T) {
		if got, changed := newSecretSet("", "").scrub("anything " + canaryTicket); changed || got != "anything "+canaryTicket {
			t.Errorf("scrub = (%q, %v), want the text unchanged", got, changed)
		}
	})
	t.Run("a secret shorter than the window is matched whole", func(t *testing.T) {
		if got, changed := newSecretSet("abc").scrub("x abc y abc"); !changed || got != "x REDACTED y REDACTED" {
			t.Errorf("scrub = (%q, %v), want both replaced", got, changed)
		}
	})
}

// TestSecretSet_TrimCutTail pins the tail trim a cut text gets.
func TestSecretSet_TrimCutTail(t *testing.T) {
	cases := []struct {
		name, in string
		secrets  []string
		want     string
	}{
		{"the raw ticket's first bytes", "body " + canaryTicket[:5], canarySecrets, "body REDACTED"},
		{"the escaped ticket's first bytes", "body " + url.QueryEscape(canaryTicket)[:6], canarySecrets, "body REDACTED"},
		{"the token secret's first bytes", "token " + canaryTokenSecret[:6], canarySecrets, "token REDACTED"},
		// The raw spelling of "A:B" matches the tail's last byte, the escaped
		// spelling "A%3AB" its last four: the trim has to take all four, not
		// stop at the first spelling that matches anything.
		{"the longer overlap wins across spellings", "xA%3A", []string{"A:B"}, "xREDACTED"},
		{"no overlap", "body text", canarySecrets, "body text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newSecretSet(tc.secrets...).trimCutTail(tc.in); got != tc.want {
				t.Errorf("trimCutTail(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSecretSet_Echo pins how much of a peer's text a console error repeats.
func TestSecretSet_Echo(t *testing.T) {
	secrets := newSecretSet(canarySecrets...)
	t.Run("a short text is repeated whole, scrubbed", func(t *testing.T) {
		if got := secrets.echo([]byte("token " + canaryTokenSecret)); got != "token REDACTED" {
			t.Errorf("echo = %q", got)
		}
	})
	t.Run("a long text is cut to the cap before anything else", func(t *testing.T) {
		const tail = "PAST-THE-CAP"
		got := secrets.echo([]byte(strings.Repeat("y", 100) + tail))
		if got != strings.Repeat("y", 64) {
			t.Errorf("echo = %q, want the first 64 bytes and nothing after them", got)
		}
	})
}

// TestSecretSet_Redact pins when an error is passed through untouched and
// when it has to be flattened.
func TestSecretSet_Redact(t *testing.T) {
	secrets := newSecretSet(canarySecrets...)

	t.Run("nil stays nil", func(t *testing.T) {
		if err := secrets.redact(nil); err != nil {
			t.Errorf("redact(nil) = %v", err)
		}
	})

	t.Run("a clean error is passed through as it is", func(t *testing.T) {
		clean := fmt.Errorf("dial: %w", context.DeadlineExceeded)
		if got := secrets.redact(clean); got != clean {
			t.Errorf("redact returned %v, want the same error back so errors.Is keeps working", got)
		}
	})

	t.Run("a close keeps its code and loses its text", func(t *testing.T) {
		got := secrets.redact(&websocket.CloseError{Code: websocket.ClosePolicyViolation, Text: "bye " + canaryTicket[:5]})
		var closeErr *websocket.CloseError
		if !errors.As(got, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "" {
			t.Errorf("redact = %#v, want a CloseError with the code and no text", got)
		}
	})

	t.Run("an overlong error is cut to the cap", func(t *testing.T) {
		const tail = "PAST-THE-CAP"
		got := secrets.redact(errors.New(strings.Repeat("z", maxErrorText) + tail))
		if strings.Contains(got.Error(), tail) || len(got.Error()) != maxErrorText {
			t.Errorf("redact = %d bytes ending %q, want exactly the first %d", len(got.Error()), lastBytes(got.Error(), 20), maxErrorText)
		}
		if errors.Unwrap(got) != nil {
			t.Error("the capped error still unwraps to the original")
		}
	})

	t.Run("a cap that cuts into a secret trims what is left of it", func(t *testing.T) {
		got := secrets.redact(errors.New(strings.Repeat("z", max(maxErrorText-5, 0)) + canaryTicket))
		if strings.Contains(got.Error(), canaryTicket[:5]) {
			t.Errorf("redact keeps the ticket's first five bytes: …%s", lastBytes(got.Error(), 20))
		}
	})

	flattened := map[string]error{
		"the ticket in its own text":         fmt.Errorf("parse %q: %w", "ws://h/?vncticket="+url.QueryEscape(canaryTicket), errors.New("bad")),
		"the token in its own text":          errors.New("echo Authorization: " + canaryAuthHeader),
		"the ticket only in a wrapped link":  hidingError{inner: errors.New("echo " + canaryTicket)},
		"the ticket only in one of a list":   hidingJoin{errs: []error{errors.New("first"), errors.New("echo " + canaryTicket)}},
		"the ticket only in its %+v":         plusVError{},
		"the ticket only in its %v":          plainVError{},
		"the ticket in a wrapped link's %+v": hidingError{inner: plusVError{}},
		"the ticket in a wrapped link's %v":  hidingError{inner: plainVError{}},
	}
	for name, in := range flattened {
		t.Run(name, func(t *testing.T) {
			got := secrets.redact(in)
			if got == nil {
				t.Fatal("redact dropped the error")
			}
			if next := errors.Unwrap(got); next != nil {
				t.Errorf("the redacted error still unwraps to %T", next)
			}
			if _, ok := got.(interface{ Unwrap() []error }); ok {
				t.Error("the redacted error still unwraps to a list")
			}
			assertNoSecrets(t, got)
		})
	}
}

// TestRedactConsoleError is the exported form the relay in internal/ws logs
// through: it has to know the client's token without being told it.
func TestRedactConsoleError(t *testing.T) {
	c := consoleClient(t, "https://192.0.2.10:8006")
	got := c.RedactConsoleError(errors.New("peer said "+canaryAuthHeader+" and "+canaryTicket), canaryTicket)
	assertNoSecrets(t, got)
	if got == nil || !strings.HasPrefix(got.Error(), "peer said ") {
		t.Errorf("RedactConsoleError = %v, want the error kept with the secrets taken out", got)
	}
}
