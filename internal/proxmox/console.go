package proxmox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// NodeTermProxy requests a terminal proxy ticket for a node shell.
func (c *Client) NodeTermProxy(ctx context.Context, node string) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/termproxy"
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("node termproxy on %s: %w", node, err)
	}
	return &resp, nil
}

// VMTermProxy requests a terminal proxy ticket for a QEMU VM serial console.
func (c *Client) VMTermProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/termproxy"
	params := url.Values{}
	params.Set("serial", "serial0")
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("VM %d termproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// CTTermProxy requests a terminal proxy ticket for an LXC container console.
func (c *Client) CTTermProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/termproxy"
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("CT %d termproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// VMVNCProxy requests a VNC proxy ticket for a QEMU VM graphical console.
// Uses websocket=1 so we can connect via the vncwebsocket endpoint.
func (c *Client) VMVNCProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/vncproxy"
	params := url.Values{}
	params.Set("websocket", "1")
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("VM %d vncproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// CTVNCProxy requests a VNC proxy ticket for an LXC container console.
// Uses websocket=1 so we can connect via the vncwebsocket endpoint.
// This avoids the termproxy ticket+handshake flow which doesn't work with API tokens.
func (c *Client) CTVNCProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/vncproxy"
	params := url.Values{}
	params.Set("websocket", "1")
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("CT %d vncproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// NodeVNCProxy requests a VNC proxy ticket for a node shell.
// Uses websocket=1 so we can connect via the vncwebsocket endpoint.
func (c *Client) NodeVNCProxy(ctx context.Context, node string) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/vncproxy"
	params := url.Values{}
	params.Set("websocket", "1")
	ctx, cancel := c.mintContext(ctx)
	defer cancel()
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("node vncproxy on %s: %w", node, err)
	}
	return &resp, nil
}

// Console timing.
//
// Opening a console waits on a node three times: the POST that mints the
// ticket (the methods above), the websocket dial, and — for a node shell — the
// ticket exchange with termproxy after it. Both callers, HandleConsole and
// HandleVNC in internal/ws, run all three on context.Background(), so nothing
// cancels any of them: a browser that gives up closes a socket nobody reads
// until they are over. These bounds are therefore what ends a wait on a node
// that has stopped answering. Without them a node that accepted the TCP
// connection and then said nothing held the handler's goroutine, and the
// browser's socket, for the client's Timeout in the POST — five minutes on a
// cached client — and for as long as the connection stayed up in the other
// two.
//
// Each is consoleRequestTimeout, 30 seconds: the per-request Timeout
// newAPIClient gives a client configured with none, and what the ws handlers
// configure on the clients they build themselves. Each wait is one request's
// worth of work — the POST is one API request, the upgrade a single GET
// answered with a 101, and termproxy's "OK" follows one local API call (see
// DialTerminal) — so each gets one request's budget.
//
// Deliberately NOT the Timeout of the client in hand. In production the ws
// handlers take their client from the ClientCache, and a cached client's
// Timeout is CachedClientTimeout: five minutes, sized for the slowest API
// calls any engine makes. The browser has no connect timeout of its own, so
// that would be five minutes of "connecting" per attempt against a node that
// is never going to answer.
const consoleRequestTimeout = 30 * time.Second

// consoleTimeouts bounds the three waits opening a console makes on a node.
type consoleTimeouts struct {
	// mint bounds each ticket request — the POST to a termproxy or vncproxy
	// endpoint — as a deadline on that request's context.
	mint time.Duration
	// handshake bounds everything up to the 101 as one budget, measured from
	// the start of the dial. It becomes the Dialer's HandshakeTimeout, and
	// gorilla/websocket (v1.5.3, client.go, Dialer.DialContext) turns that
	// into a context deadline, which bounds the TCP connect, and the same
	// instant as the connection's deadline, which bounds the TLS handshake,
	// the upgrade request and the wait for its answer. It clears that deadline
	// once the upgrade succeeds.
	handshake time.Duration
	// auth bounds DialTerminal's ticket exchange as one budget: sending the
	// ticket line, and waiting for termproxy's "OK".
	auth time.Duration
}

// consoleWaits is the bounds this client's consoles use: Client.console where
// a test set it, consoleRequestTimeout everywhere else.
//
// A zero resolves to the default rather than passing through, and every
// production client has zeros here: NewClient never sets the field, and
// nothing outside tests builds a Client. Passed through, a zero would be a
// deadline that has already passed, or — as a HandshakeTimeout — gorilla's
// "no timeout".
func (c *Client) consoleWaits() consoleTimeouts {
	t := c.console
	if t.mint <= 0 {
		t.mint = consoleRequestTimeout
	}
	if t.handshake <= 0 {
		t.handshake = consoleRequestTimeout
	}
	if t.auth <= 0 {
		t.auth = consoleRequestTimeout
	}
	return t
}

// mintContext bounds one ticket request by the mint budget.
func (c *Client) mintContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.consoleWaits().mint)
}

// consoleDialer is the websocket dialer both console dials use.
//
// It shares the tls.Config buildHTTPClient built for this client, so a
// console connection's certificate is checked exactly as an API request's is —
// against the pinned fingerprint, where the cluster has one — and it connects
// through guardedDialer, as that client's transport does, so the SSRF dial
// guard sees every address a console dial resolves to. gorilla still does the
// TLS handshake itself: NetDialTLSContext is left unset.
//
// The net.Dialer gets no timeout of its own. gorilla hands NetDialContext the
// context HandshakeTimeout set, so that deadline already bounds the connect,
// and a second bound on the same wait would only hide whether the first one
// works.
func (c *Client) consoleDialer() *websocket.Dialer {
	return &websocket.Dialer{
		NetDialContext:   guardedDialer(0).DialContext,
		TLSClientConfig:  c.tlsCfg,
		Subprotocols:     []string{"binary"},
		HandshakeTimeout: c.consoleWaits().handshake,
	}
}

// DialVNCWebSocket opens a WebSocket connection to the Proxmox vncwebsocket
// endpoint. The ticket is passed as a URL query parameter and Proxmox handles
// authentication from the Authorization header + vncticket — no handshake needed.
// The vncPath controls the resource path:
//   - "" → /nodes/{node}/vncwebsocket
//   - "qemu/{vmid}" → /nodes/{node}/qemu/{vmid}/vncwebsocket
//   - "lxc/{vmid}" → /nodes/{node}/lxc/{vmid}/vncwebsocket
//
// The dial is bounded, and no error it returns repeats the connection's
// secrets; see dialConsole.
func (c *Client) DialVNCWebSocket(ctx context.Context, node string, vncTicket string, port int, vncPath string) (*websocket.Conn, error) {
	return c.dialConsole(ctx, node, vncTicket, port, vncPath, c.consoleSecrets(vncTicket))
}

// termproxyReadLimit caps the one message DialTerminal reads before the relay
// takes over: termproxy's "OK". pveproxy relays termproxy's output in frames
// of at most 128 KiB — $max_payload_size in pve-http-server,
// src/PVE/APIServer/AnyEvent.pm, websocket_proxy — so no frame PVE sends is
// larger, an "OK" that shares its frame with the first output included.
// Without a cap gorilla reads a message of any size into memory.
const termproxyReadLimit = 128 << 10

// DialTerminal opens a WebSocket connection to the Proxmox vncwebsocket endpoint.
// The vncPath must match the resource that issued the termproxy ticket:
//   - "" or "node" → /nodes/{node}/vncwebsocket
//   - "lxc/{vmid}" → /nodes/{node}/lxc/{vmid}/vncwebsocket
//   - "qemu/{vmid}" → /nodes/{node}/qemu/{vmid}/vncwebsocket
//
// The dial, and the ticket exchange with termproxy after it, are bounded, and
// no error it returns repeats the connection's secrets; see dialConsole.
func (c *Client) DialTerminal(ctx context.Context, node string, vncTicket string, port int, vncPath string, user string) (*websocket.Conn, error) {
	secrets := c.consoleSecrets(vncTicket)
	conn, err := c.dialConsole(ctx, node, vncTicket, port, vncPath, secrets)
	if err != nil {
		return nil, err
	}

	// Authenticate the terminal session.
	// Proxmox termproxy expects "user@realm:ticket\n" as a single message,
	// then responds with "OK". The user must match the identity that created
	// the ticket (from the termproxy response's User field).
	//
	// The exchange is bounded as a whole, the write as well as the read. The
	// read is the one that waits: termproxy writes "OK" only once its own
	// authenticate() has returned, and that is a POST to the node's own API
	// daemon made with no timeout — termproxy's two 10-second limits cover
	// only the wait for this connection and for the ticket line (pve-xtermjs,
	// termproxy/src/main.rs, do_main and authenticate). A daemon that never
	// answered termproxy held this read for as long as the connection stayed
	// up. The write is one short frame, which a new connection's buffers take
	// at once; it is bounded all the same, because gorilla puts no deadline
	// on a write it was not given one for, and nothing else would end one to
	// a peer that has stopped reading.
	deadline := time.Now().Add(c.consoleWaits().auth)
	if err := conn.SetWriteDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bound vnc auth: %w", err)
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bound vnc auth response: %w", err)
	}
	conn.SetReadLimit(termproxyReadLimit)

	authMsg := user + ":" + vncTicket + "\n"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(authMsg)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send vnc auth: %w", secrets.redact(err))
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read vnc auth response: %w", secrets.redact(err))
	}
	if !strings.HasPrefix(string(msg), "OK") {
		conn.Close()
		return nil, fmt.Errorf("vnc auth failed: got %q", secrets.echo(msg))
	}

	// Hand the connection over as the dial left it. It becomes the relay's
	// Proxmox leg, read and written for as long as the session lasts, and a
	// deadline left behind would cut every node shell off that long after it
	// opened. The read limit goes back to zero — gorilla's "no limit", which
	// is what the connection had — because it was sized for the one answer
	// the exchange expects, not for the session.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear vnc auth deadline: %w", err)
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear vnc auth write deadline: %w", err)
	}
	conn.SetReadLimit(0)

	return conn, nil
}

// maxEchoedBytes caps how much of anything a peer sent a console error
// repeats: a refused upgrade's response body, or termproxy's answer to the
// ticket line. Enough to show what the peer said; not enough for a peer to
// fill a log line with whatever it likes.
const maxEchoedBytes = 64

// maxErrorText caps the text of an error a console dial passes on from
// gorilla or the network. Their own messages are far shorter; what makes one
// long is text a peer sent, which net/http quotes whole — a status line of any
// length becomes "malformed HTTP status code" and all of it.
const maxErrorText = 512

// dialConsole opens the vncwebsocket both console flows attach to, bounded by
// consoleWaits().handshake, and returns no error that repeats anything in
// secrets.
//
// The ticket and the token both ride on the dial itself: the ticket has to
// travel in the URL — the endpoint takes it as a GET parameter, which
// pveproxy reads from the query — and the token in the Authorization header.
// Three routes lead from those into internal/ws's "dial ... failed" log lines:
//
//   - this file's own messages, which printed the whole URL, and so logged
//     the ticket on every failed node-shell dial, whatever the reason;
//   - gorilla's errors, which can quote the URL, or whatever a peer sent back
//     in place of a response: net/http's ReadResponse puts a malformed status
//     line into its error, so a peer that echoes the request line hands it
//     the ticket;
//   - the response body a refused upgrade echoes into the error, which a peer
//     that repeats its request's headers fills with the token. PVE's own
//     refusal does not repeat the ticket — the check raises "permission
//     denied - invalid PVEVNC ticket" (pve-common, src/PVE/Ticket.pm,
//     verify_rsa_ticket) and the JSON formatter adds only data, errors and
//     message (pve-http-server, src/PVE/APIServer/Formatter/Standard.pm,
//     prepare_response_data) — but whatever answers at the cluster's address
//     need not be PVE.
//
// So the URL is shown with the ticket replaced, the body is echoed through
// secrets.echo, and gorilla's error goes through secrets.redact. DialTerminal
// does the same for what it adds on top.
func (c *Client) dialConsole(ctx context.Context, node, vncTicket string, port int, vncPath string, secrets *secretSet) (*websocket.Conn, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}

	// Build the WebSocket URL from the base URL.
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	// Switch scheme to wss/ws.
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		parsed.Scheme = "wss"
	}

	basePath := "/api2/json/nodes/" + url.PathEscape(node)
	if vncPath != "" {
		basePath += "/" + vncPath
	}
	parsed.Path = basePath + "/vncwebsocket"
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("vncticket", vncTicket)
	parsed.RawQuery = q.Encode()
	target := parsed.String()

	// The same URL with the ticket replaced: what errors show instead.
	q.Set("vncticket", redactedSecret)
	parsed.RawQuery = q.Encode()
	shown := parsed.String()

	header := http.Header{}
	header.Set("Authorization", c.authHeader)

	conn, resp, err := c.consoleDialer().DialContext(ctx, target, header)
	if err != nil {
		err = secrets.redact(err)
		if resp != nil {
			// One byte past the cap, so echo can tell a body it cut from one
			// that fitted.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxEchoedBytes+1))
			resp.Body.Close()
			return nil, fmt.Errorf("dial Proxmox vncwebsocket (status %d, url %s, body %s): %w",
				resp.StatusCode, shown, secrets.echo(body), err)
		}
		return nil, fmt.Errorf("dial Proxmox vncwebsocket (url %s): %w", shown, err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	return conn, nil
}

// consoleSecrets is everything a console connection holding ticket must never
// repeat: the ticket, the API token's secret, and the Authorization header
// value that carries the token.
func (c *Client) consoleSecrets(ticket string) *secretSet {
	return newSecretSet(ticket, c.tokenSecret, c.authHeader)
}

// RedactConsoleError returns err as it may be logged for a console session
// holding ticket: a close frame's text dropped, every secret of the
// connection scrubbed out, and the text capped; see secretSet.redact. ticket
// may be empty, for an error from before there is one.
//
// The ws handlers log two kinds of error through this. One is what the relay
// reads from Proxmox once a session is open. The other is a refused ticket
// request, whose error carries the node's error body: checkStatus
// (api_client.go) repeats up to maxResponseSize, 50 MB, of it, and a peer that
// reflects its request puts the API token there. That echo is still
// checkStatus's for every other caller of this client; only the console's log
// lines are scrubbed and capped here.
func (c *Client) RedactConsoleError(err error, ticket string) error {
	return c.consoleSecrets(ticket).redact(err)
}

// redactedSecret is what a secret reads as wherever a console error would
// otherwise have repeated it.
const redactedSecret = "REDACTED"

// secretWindow is the shortest run of a secret secretSet recognises.
//
// Matching only whole secrets is not enough, because a text can hold part of
// one: a cap can cut through an echoed secret. Eight bytes is long enough that
// ordinary text does not match a run of a secret by accident — the six bytes
// of "PVEVNC" that PVE's own messages share with every ticket fall short of
// it — so nothing but the secrets is replaced.
//
// A shorter run is left alone, and nothing this file passes on can hold one
// that a cut of its own made. Where it cuts a peer's text — an echo, an
// overlong error — it trims a cut secret's opening bytes too
// (secretSet.trimCutTail), and a close frame's text, which a peer has to fit
// into 123 bytes, is dropped rather than repeated (secretSet.redact). That
// leaves a secret a peer cut short itself before sending it. At the front of
// a vncproxy ticket those bytes would be characters of the eight-character
// VNC password that qemu-server and pve-container put there
// (src/PVE/API2/Qemu.pm and src/PVE/API2/LXC.pm, vncproxy:
// "${password}:${ticket}").
const secretWindow = 8

// secretSet is everything one console connection must never repeat, in every
// spelling a secret can take on its way into an error:
//
//   - raw, as in termproxy's ticket line, or a peer echoing a header back;
//   - query-escaped, as in the dial URL;
//   - Go-quoted, as when net/http %q-quotes a status line a peer sent, or a
//     JSON body escapes one. The VNC password in front of a vncproxy ticket
//     is drawn from '!' to '`' (pve-common, src/PVE/Ticket.pm,
//     generate_vnc_password), which takes in '"' and '\', and quoting
//     rewrites both.
type secretSet struct {
	spellings []string
	grams     map[string]struct{}
}

func newSecretSet(secrets ...string) *secretSet {
	s := &secretSet{grams: map[string]struct{}{}}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		quoted := strconv.Quote(secret)
		for _, sp := range []string{secret, url.QueryEscape(secret), quoted[1 : len(quoted)-1]} {
			s.spellings = append(s.spellings, sp)
			for i := 0; i+secretWindow <= len(sp); i++ {
				s.grams[sp[i:i+secretWindow]] = struct{}{}
			}
		}
	}
	return s
}

// scrub replaces every run of at least secretWindow bytes of a secret in
// text with redactedSecret, and reports whether it replaced anything. A
// spelling shorter than the window is matched whole.
func (s *secretSet) scrub(text string) (string, bool) {
	replaced := false
	for _, sp := range s.spellings {
		if len(sp) < secretWindow && strings.Contains(text, sp) {
			text = strings.ReplaceAll(text, sp, redactedSecret)
			replaced = true
		}
	}

	var covered []bool
	for i := 0; i+secretWindow <= len(text); i++ {
		if _, hit := s.grams[text[i:i+secretWindow]]; !hit {
			continue
		}
		if covered == nil {
			covered = make([]bool, len(text))
		}
		for j := i; j < i+secretWindow; j++ {
			covered[j] = true
		}
	}
	if covered == nil {
		return text, replaced
	}

	var b strings.Builder
	for i := 0; i < len(text); {
		if !covered[i] {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteString(redactedSecret)
		for i < len(text) && covered[i] {
			i++
		}
	}
	return b.String(), true
}

// trimCutTail replaces the longest tail of text that is the start of a
// secret, in any spelling, with redactedSecret. A tail that only happens to
// match a secret's first byte or two goes too: that is the price of catching
// a cut that kept that little, and the reason this is only for a text this
// file has just cut.
func (s *secretSet) trimCutTail(text string) string {
	longest := 0
	for _, sp := range s.spellings {
		for n := min(len(sp), len(text)); n > longest; n-- {
			if strings.HasSuffix(text, sp[:n]) {
				longest = n
				break
			}
		}
	}
	if longest == 0 {
		return text
	}
	return text[:len(text)-longest] + redactedSecret
}

// echo is what a console error repeats of something a peer sent: at most its
// first maxEchoedBytes bytes, with the secrets scrubbed out. It cuts first, so
// the scrub sees exactly what is shown, and when it has cut, the tail that is
// the start of a secret goes as well: a cut that keeps fewer than
// secretWindow bytes of a secret leaves too little to recognise.
func (s *secretSet) echo(peer []byte) string {
	text := string(peer)
	if len(text) > maxEchoedBytes {
		text = s.trimCutTail(text[:maxEchoedBytes])
	}
	text, _ = s.scrub(text)
	return text
}

// redact returns err as a console error may pass it on.
//
// A close frame's error keeps its code and loses its text. The text is
// whatever the peer chose to write, and pveproxy never sends a close frame at
// all (pve-http-server, src/PVE/APIServer/AnyEvent.pm, websocket_proxy), so a
// reason can only come from something that is not PVE.
//
// Anything else comes back unchanged when neither it nor anything it wraps
// mentions a secret and its text is not overlong, so errors.Is and errors.As
// keep working on the ordinary failures — a timeout, a refused connection,
// ErrBadHandshake — none of which carries a secret. Otherwise it is flattened
// into an error holding only its text, cut to maxErrorText first and then
// scrubbed, because wrapping the original with %w would keep it reachable
// through errors.Unwrap however well the outer text was scrubbed.
func (s *secretSet) redact(err error) error {
	if err == nil {
		return nil
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return &websocket.CloseError{Code: closeErr.Code}
	}
	text := err.Error()
	if len(text) <= maxErrorText && !s.mentionedBy(err) {
		return err
	}
	if len(text) > maxErrorText {
		text = s.trimCutTail(text[:maxErrorText])
	}
	text, _ = s.scrub(text)
	return redactedError(text)
}

// mentionedBy reports whether err, or anything reachable from it through
// Unwrap, mentions a secret in any of the ways it can print: its text, %v —
// which is how %w renders it inside the error that wraps it — or %+v. Every
// link is checked, not just the outermost text, because a wrapper is free to
// print less than it wraps.
func (s *secretSet) mentionedBy(err error) bool {
	pending := []error{err}
	for len(pending) > 0 {
		e := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if e == nil {
			continue
		}
		for _, rendered := range []string{e.Error(), fmt.Sprintf("%v", e), fmt.Sprintf("%+v", e)} {
			if _, found := s.scrub(rendered); found {
				return true
			}
		}
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			pending = append(pending, u.Unwrap())
		case interface{ Unwrap() []error }:
			pending = append(pending, u.Unwrap()...)
		}
	}
	return false
}

// redactedError is an error redact had to flatten. It is the scrubbed text
// and nothing else, so no rendering of it can reach what was scrubbed out.
type redactedError string

func (e redactedError) Error() string { return string(e) }
