package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	gorillaws "github.com/gorilla/websocket"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/config"
)

// These tests hold the request-framing layer — closeConnectionsLeftMidBody,
// refuseChunkedRequestBodies, the 10 MiB body-size guard, the upload exemption
// the last two share, and the keep-alive idle timeout — to what it is for, on
// real TCP connections to the server as cmd/nexara assembles it.

// hiddenRequestProbe is the route the request hidden in a fixture's body asks
// for. It counts what reaches it, so a test can tell whether the server ever
// served that request.
const hiddenRequestProbe = "/api/v1/hidden-request-probe"

// hiddenRequest is a whole request, placed in a fixture's body where fasthttp
// would parse it as the next request if the body around it were left unread.
const hiddenRequest = "GET " + hiddenRequestProbe + " HTTP/1.1\r\nHost: example.com\r\n\r\n"

// bodyPrefetch is how much of a body with a Content-Length fasthttp reads
// before any handler runs (readBodyWithStreaming). A fixture pads its body to
// exactly that much ahead of the hidden request, so the hidden request is the
// first thing left on the connection. Each case's precondition twin is what
// shows it still is: were the prefetch to change, the hidden request would no
// longer be served there either, and the case would fail as a precondition
// rather than pass as the refusal of something harmless.
const bodyPrefetch = 8 << 10

// probedServer is the assembled server, with hiddenRequestProbe mounted,
// serving on a loopback listener.
type probedServer struct {
	addr string
	hits *atomic.Int32
}

// newProbedServer builds and serves the assembled server, with any further
// routes mount adds. guarded false takes closeConnectionsLeftMidBody back out
// and leaves every middleware and route as New builds them: the precondition
// twin.
func newProbedServer(t *testing.T, guarded bool, mount ...func(*fiber.App)) probedServer {
	t.Helper()
	s := newAssembledServer(t)
	hits := new(atomic.Int32)
	s.app.Get(hiddenRequestProbe, func(c fiber.Ctx) error {
		hits.Add(1)
		return c.SendStatus(fiber.StatusNoContent)
	})
	for _, m := range mount {
		m(s.app)
	}
	if !guarded {
		// App.Handler is Fiber's own request handler — what the fasthttp
		// server called before New wrapped it.
		s.app.Server().Handler = s.app.Handler()
	}
	return probedServer{addr: serveOnLoopback(t, s), hits: hits}
}

// requestHead is a request line, a Host and the given header lines, without
// the blank line that ends the head.
func requestHead(method, target string, lines ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: example.com\r\n", method, target)
	for _, line := range lines {
		b.WriteString(line + "\r\n")
	}
	return b.String()
}

// paddedRequest is a request whose body, framed by a Content-Length, is
// bodyPrefetch bytes of padding followed by hiddenRequest. A non-zero declared
// replaces the Content-Length the head declares; the bytes sent stay the same.
func paddedRequest(method, target string, declared int, lines ...string) string {
	body := strings.Repeat("x", bodyPrefetch) + hiddenRequest
	if declared == 0 {
		declared = len(body)
	}
	return requestHead(method, target, lines...) + fmt.Sprintf("Content-Length: %d\r\n\r\n", declared) + body
}

// chunkedRequest is a request whose body is sent chunked and begins with
// hiddenRequest. fasthttp reads none of a chunked body before a handler runs,
// so the hidden request is the first thing left on the connection; it is not
// valid chunked framing, which is the point — nothing is meant to read it.
func chunkedRequest(method, target string, lines ...string) string {
	return requestHead(method, target, lines...) + "Transfer-Encoding: chunked\r\n\r\n" + hiddenRequest
}

// obsFoldRequest is a request whose head folds a Content-Length into the field
// above it — "X-Pad: x", then " Content-Length: N" on a line of its own that
// starts with a space — followed by N bytes that are hiddenRequest. fasthttp
// joins the folded line into X-Pad, so to it the body is empty and
// hiddenRequest is the next request; a proxy that read the folded line as a
// field of its own would forward hiddenRequest as this request's body.
func obsFoldRequest(method, target string) string {
	return requestHead(method, target, "Content-Type: application/json", "X-Pad: x",
		fmt.Sprintf(" Content-Length: %d", len(hiddenRequest))) + "\r\n" + hiddenRequest
}

// http10 makes raw's request line — the first — an HTTP/1.0 one.
func http10(raw string) string {
	return strings.Replace(raw, " HTTP/1.1\r\n", " HTTP/1.0\r\n", 1)
}

// methodOf is the method on raw's request line.
func methodOf(raw string) string {
	method, _, _ := strings.Cut(raw, " ")
	return method
}

// wireAnswer is what a connection said back to one request.
type wireAnswer struct {
	status          int
	connClose       bool // the answer carries Connection: close
	nosniff         string
	requestID       string
	contentType     string
	contentEncoding string
	body            []byte
	envelope        ErrorResponse

	// next is an answer the connection gave after this one without being sent
	// another request, and closed says it ended after this one instead.
	// Neither is set when it stayed open and said nothing more.
	next   *http.Response
	closed bool
}

// writeRaw writes raw to conn.
func writeRaw(t *testing.T, conn net.Conn, raw string) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// dialAndSend opens a connection to addr and sends raw on it.
func dialAndSend(t *testing.T, addr, raw string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	writeRaw(t, conn, raw)
	return conn, bufio.NewReader(conn)
}

// readAnswer reads the answer to one request off the connection — past a
// 100 Continue ahead of it — as the answer to method, which decides whether it
// carries a body: an answer to HEAD does not.
func readAnswer(t *testing.T, conn net.Conn, br *bufio.Reader, method string) wireAnswer {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	req := &http.Request{Method: method}
	res, err := http.ReadResponse(br, req)
	for err == nil && res.StatusCode == http.StatusContinue {
		res, err = http.ReadResponse(br, req)
	}
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatalf("reading the answer's body: %v", err)
	}
	ans := wireAnswer{
		status:          res.StatusCode,
		connClose:       res.Close,
		nosniff:         res.Header.Get(fiber.HeaderXContentTypeOptions),
		requestID:       res.Header.Get(fiber.HeaderXRequestID),
		contentType:     res.Header.Get(fiber.HeaderContentType),
		contentEncoding: res.Header.Get(fiber.HeaderContentEncoding),
		body:            body,
	}
	_ = json.Unmarshal(body, &ans.envelope)
	return ans
}

// exchange sends raw on a new connection and reads what comes back: the
// answer, then whatever the connection does next.
func exchange(t *testing.T, addr, raw string) wireAnswer {
	t.Helper()
	conn, br := dialAndSend(t, addr, raw)
	ans := readAnswer(t, conn, br, methodOf(raw))
	ans.next, ans.closed = whatFollows(t, conn, br)
	return ans
}

// whatFollows reads what a connection that has just carried an answer does
// next, for up to 3 seconds: another answer, which it returns, or its end,
// which closed reports. Neither is set when it stays open and says nothing.
func whatFollows(t *testing.T, conn net.Conn, br *bufio.Reader) (next *http.Response, closed bool) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	next, err := http.ReadResponse(br, nil)
	var netErr net.Error
	switch {
	case err == nil:
		_, _ = io.Copy(io.Discard, next.Body)
		_ = next.Body.Close()
		return next, false
	case errors.As(err, &netErr) && netErr.Timeout():
		// Still open, and silent.
		return nil, false
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return nil, true
	default:
		t.Fatalf("reading what followed the answer: %v", err)
		return nil, false
	}
}

// unreadBodyCase is one way the server answers a request without reading its
// body.
type unreadBodyCase struct {
	name string
	// raw is the request as it goes out: the head, and as much of the body as
	// is sent — never all of it where the head declares more.
	raw    string
	status int
	// slug and message, when set, are the error envelope's. The slug is
	// spelled out as a client sees it, rather than taken from statusText, the
	// code under test; the message is the refusal's own constant where it has
	// one, which pins WHICH part of the server answered rather than its
	// wording.
	slug, message string
	// mount adds the routes the case needs to each server.
	mount func(*fiber.App)
	// prime runs against each server before raw is sent.
	prime func(t *testing.T, addr string)
	// beforeChain marks an answer Fiber gives without running its middleware
	// chain, which therefore lacks the security headers.
	beforeChain bool
}

// run sends the case's request to the server without
// closeConnectionsLeftMidBody, where the request hidden in the unread body has
// to be served — the precondition, which shows the fixture is one fasthttp
// really parses — and then to the server as New builds it, where the answer
// has to close the connection with the hidden request unserved.
func (tc unreadBodyCase) run(t *testing.T) {
	check := func(t *testing.T, ans wireAnswer) {
		t.Helper()
		if ans.status != tc.status {
			t.Fatalf("status %d (%s), want %d", ans.status, ans.body, tc.status)
		}
		if tc.message != "" && (ans.envelope.Message != tc.message || ans.envelope.Error != tc.slug) {
			t.Fatalf("envelope %+v, want %q with the slug %q", ans.envelope, tc.message, tc.slug)
		}
		wantNosniff := "nosniff"
		if tc.beforeChain {
			wantNosniff = ""
		}
		if ans.nosniff != wantNosniff {
			t.Fatalf("X-Content-Type-Options %q, want %q: the case does not reach the part of the server it names",
				ans.nosniff, wantNosniff)
		}
	}

	var mount []func(*fiber.App)
	if tc.mount != nil {
		mount = append(mount, tc.mount)
	}
	twin := newProbedServer(t, false, mount...)
	if tc.prime != nil {
		tc.prime(t, twin.addr)
	}
	ans := exchange(t, twin.addr, tc.raw)
	check(t, ans)
	if ans.next == nil || ans.next.StatusCode != fiber.StatusNoContent || twin.hits.Load() != 1 {
		t.Fatalf("precondition: without closeConnectionsLeftMidBody the request hidden in the unread body should "+
			"have been served (next answer %v, closed %v, probe reached %d time(s)); a fixture fasthttp does not "+
			"parse cannot show the close preventing anything", ans.next, ans.closed, twin.hits.Load())
	}

	guarded := newProbedServer(t, true, mount...)
	if tc.prime != nil {
		tc.prime(t, guarded.addr)
	}
	ans = exchange(t, guarded.addr, tc.raw)
	check(t, ans)
	if !ans.connClose {
		t.Error("the answer does not carry Connection: close")
	}
	if ans.next != nil {
		t.Errorf("the connection answered again, with %d", ans.next.StatusCode)
	}
	if !ans.closed {
		t.Error("the connection stayed open after the answer")
	}
	if n := guarded.hits.Load(); n != 0 {
		t.Errorf("the request hidden in the body was served %d time(s)", n)
	}
}

// exhaustLoginLimiter spends this client's login rate-limit budget with
// bodiless logins on one kept-alive connection, so that the next login is
// answered 429.
func exhaustLoginLimiter(t *testing.T, addr string) {
	t.Helper()
	const login = "POST /api/v1/auth/login HTTP/1.1\r\nHost: example.com\r\n" +
		"Content-Type: application/json\r\nContent-Length: 0\r\n\r\n"
	conn, br := dialAndSend(t, addr, login)
	for range 100 {
		if readAnswer(t, conn, br, fiber.MethodPost).status == fiber.StatusTooManyRequests {
			return
		}
		writeRaw(t, conn, login)
	}
	t.Fatal("precondition: the login rate limiter never answered 429")
}

// uploadTarget is a request path for the storage upload route, spelled as
// the SPA spells it.
var uploadTarget = strings.NewReplacer(":cluster_id", testClusterID, ":storage_id", testClusterID).
	Replace(storageUploadPath)

// TestUnreadBodyClosesTheConnection sends, for each way the server answers a
// request without reading its body, one kept-alive connection a request whose
// body hides a second request past the part fasthttp reads ahead — and holds
// the answer to closing the connection with the hidden request unserved. Every
// case first shows, against the same server without
// closeConnectionsLeftMidBody, that the hidden request is served there.
//
// The 501s are the cases a middleware in Fiber's chain could not cover: Fiber
// answers a method it does not know before the chain runs, which the missing
// security headers show. The trailing-slash rows are refuseTrailingSlashWrites'
// 400, which reads nothing of a body and leaves the close to
// closeConnectionsLeftMidBody like every other early refusal. The obs-fold and Transfer-Encoding: identity rows
// frame the body so that fasthttp reads none of it — the hidden request is
// what a lenient proxy would have sent as the body — and the answer closes
// the connection although no body is left unread; for the obs-fold under an
// unknown method, only the close stands between it and the next request.
func TestUnreadBodyClosesTheConnection(t *testing.T) {
	const jsonType = "Content-Type: application/json"
	for _, tc := range []unreadBodyCase{
		{name: "401 from an authenticated route", raw: paddedRequest(fiber.MethodPost, "/api/v1/clusters", 0, jsonType),
			status: fiber.StatusUnauthorized, slug: "unauthorized", message: "Missing authorization token"},
		{name: "404 for a path no route matches", raw: paddedRequest(fiber.MethodPost, "/api/v1/no-such-route", 0, jsonType),
			status: fiber.StatusNotFound},
		{name: "405 for a method the route does not take", raw: paddedRequest(fiber.MethodPut, "/api/v1/version", 0, jsonType),
			status: fiber.StatusMethodNotAllowed},
		{name: "413 from the body-size guard", raw: paddedRequest(fiber.MethodPost, "/api/v1/auth/login", 10<<20+1, jsonType),
			status: fiber.StatusRequestEntityTooLarge},
		{name: "411 from the chunked-body refusal", raw: chunkedRequest(fiber.MethodPost, "/api/v1/auth/login", jsonType),
			status: fiber.StatusLengthRequired, slug: "length_required", message: chunkedBodyRefusal},
		{name: "the upload route's own 401, for a chunked body the refusal lets through",
			raw:    chunkedRequest(fiber.MethodPost, uploadTarget, "Content-Type: multipart/form-data; boundary=x"),
			status: fiber.StatusUnauthorized, slug: "unauthorized", message: "Missing authorization token"},
		{name: "429 from a rate limiter", raw: paddedRequest(fiber.MethodPost, "/api/v1/auth/login", 0, jsonType),
			status: fiber.StatusTooManyRequests, prime: exhaustLoginLimiter},
		{name: "a CORS preflight", raw: paddedRequest(fiber.MethodOptions, "/api/v1/auth/login", 0,
			"Origin: https://example.com", "Access-Control-Request-Method: POST"),
			status: fiber.StatusNoContent},
		{name: "the SPA shell, for a GET with a body", raw: paddedRequest(fiber.MethodGet, "/clusters/x/vms", 0),
			status: fiber.StatusOK},
		{name: "501 for a method Fiber answers before its middleware runs", raw: paddedRequest("FOO", "/api/v1/version", 0),
			status: fiber.StatusNotImplemented, beforeChain: true},
		{name: "401 for a request that expects 100-continue",
			raw:    paddedRequest(fiber.MethodPost, "/api/v1/clusters", 0, jsonType, "Expect: 100-continue"),
			status: fiber.StatusUnauthorized, slug: "unauthorized", message: "Missing authorization token"},
		{name: "a HEAD with a body", raw: paddedRequest(fiber.MethodHead, "/api/v1/version", 0),
			status: fiber.StatusOK},
		{name: "401 on an HTTP/1.0 connection kept alive",
			raw:    http10(paddedRequest(fiber.MethodPost, "/api/v1/clusters", 0, jsonType, "Connection: keep-alive")),
			status: fiber.StatusUnauthorized, slug: "unauthorized", message: "Missing authorization token"},
		{name: "400 for a Content-Length folded into another field (obs-fold)",
			raw:    obsFoldRequest(fiber.MethodPost, "/api/v1/auth/login"),
			status: fiber.StatusBadRequest, slug: "bad_request", message: obsFoldRefusal},
		{name: "400 for a Content-Length folded in with a tab",
			raw:    strings.Replace(obsFoldRequest(fiber.MethodPost, "/api/v1/auth/login"), "\r\n Content-Length", "\r\n\tContent-Length", 1),
			status: fiber.StatusBadRequest, slug: "bad_request", message: obsFoldRefusal},
		{name: "501 for an obs-fold head under a method Fiber answers before its middleware runs",
			raw:    obsFoldRequest("FOO", "/api/v1/version"),
			status: fiber.StatusNotImplemented, beforeChain: true},
		{name: "400 for Transfer-Encoding: identity",
			raw: requestHead(fiber.MethodPost, "/api/v1/auth/login", jsonType, "Transfer-Encoding: identity") +
				"\r\n" + hiddenRequest,
			status: fiber.StatusBadRequest, slug: "bad_request", message: identityCodingRefusal},
		{name: "400 for Transfer-Encoding: identity beside a Content-Length",
			raw: requestHead(fiber.MethodPost, "/api/v1/auth/login", jsonType, "Transfer-Encoding: identity",
				"Content-Length: 2") + "\r\n{}" + hiddenRequest,
			status: fiber.StatusBadRequest, slug: "bad_request", message: identityCodingRefusal},
		{name: "400 from the trailing-slash refusal, for a POST to a collection",
			raw:    paddedRequest(fiber.MethodPost, "/api/v1/clusters/"+testClusterID+"/pools/", 0, jsonType),
			status: fiber.StatusBadRequest, slug: "bad_request", message: trailingSlashRefusal},
		{name: "400 from the trailing-slash refusal, for a DELETE a browser resolved away from a pool named ..",
			raw:    paddedRequest(fiber.MethodDelete, "/api/v1/clusters/"+testClusterID+"/", 0, jsonType),
			status: fiber.StatusBadRequest, slug: "bad_request", message: trailingSlashRefusal},
	} {
		t.Run(tc.name, tc.run)
	}
	// Every spelling of the identity line fasthttp reads as identity: it
	// compares the name and the value ignoring letter case, trims the spaces
	// and tabs around the value, and ends a line at a bare LF as well as at
	// CRLF. The refusal has to read each of them the same way.
	for _, spelling := range []struct{ name, line string }{
		{"the name in lower case", "transfer-encoding: identity"},
		{"the name in capitals", "TRANSFER-ENCODING: identity"},
		{"the value in capitals", "Transfer-Encoding: IDENTITY"},
		{"the value in mixed case", "Transfer-Encoding: Identity"},
		{"no space after the colon", "Transfer-Encoding:identity"},
		{"the value padded with tabs", "Transfer-Encoding:\tidentity\t"},
		{"the value padded with spaces", "Transfer-Encoding:   identity   "},
	} {
		t.Run("400 for Transfer-Encoding: identity with "+spelling.name, unreadBodyCase{
			raw:    requestHead(fiber.MethodPost, "/api/v1/auth/login", jsonType, spelling.line) + "\r\n" + hiddenRequest,
			status: fiber.StatusBadRequest, slug: "bad_request", message: identityCodingRefusal,
		}.run)
	}
	t.Run("400 for Transfer-Encoding: identity on a line ended by a bare LF", unreadBodyCase{
		raw: strings.Replace(requestHead(fiber.MethodPost, "/api/v1/auth/login", "Transfer-Encoding: identity", jsonType),
			"identity\r\n", "identity\n", 1) + "\r\n" + hiddenRequest,
		status: fiber.StatusBadRequest, slug: "bad_request", message: identityCodingRefusal,
	}.run)
}

// TestUnreadBodyIsJudgedByTheFramingItsHeadDeclared is why
// closeConnectionsLeftMidBody reads the framing before the handler runs. The
// route here rewrites its request's Content-Length to 0 and reads nothing: read
// afterwards, the framing would say there was no body to leave, and the rest
// of it would stay on the connection.
func TestUnreadBodyIsJudgedByTheFramingItsHeadDeclared(t *testing.T) {
	const probe = "/api/v1/rewrites-its-framing"
	unreadBodyCase{
		raw:    paddedRequest(fiber.MethodPost, probe, 0),
		status: fiber.StatusNoContent,
		mount: func(app *fiber.App) {
			app.Post(probe, func(c fiber.Ctx) error {
				c.Request().Header.SetContentLength(0)
				return c.SendStatus(fiber.StatusNoContent)
			})
		},
	}.run(t)
}

// TestWebSocketUpgradeWithABodyIsAnsweredThenClosed pins what an upgrade that
// carries a body gets: the 101, and then a closed connection, with the
// WebSocket handler never run. The upgrader answers 101 and hijacks; the body
// is left unread, so closeConnectionsLeftMidBody asks for a close, and
// Server.serveConn, which writes the answer before it would start the hijack
// handler, leaves its loop on that request instead. Without
// closeConnectionsLeftMidBody the handler does run, with the unread body
// waiting on the connection as its first frames.
func TestWebSocketUpgradeWithABodyIsAnsweredThenClosed(t *testing.T) {
	const probe = "/api/v1/ws-body-probe"
	raw := paddedRequest(fiber.MethodGet, probe, 0, "Upgrade: websocket", "Connection: Upgrade",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version: 13")
	serve := func(t *testing.T, guarded bool) (string, *atomic.Int32) {
		t.Helper()
		runs := new(atomic.Int32)
		srv := newProbedServer(t, guarded, func(app *fiber.App) {
			app.Get(probe, websocket.New(func(c *websocket.Conn) {
				runs.Add(1)
				_, _, _ = c.ReadMessage()
			}))
		})
		return srv.addr, runs
	}

	addr, runs := serve(t, false)
	conn, br := dialAndSend(t, addr, raw)
	if ans := readAnswer(t, conn, br, fiber.MethodGet); ans.status != fiber.StatusSwitchingProtocols {
		t.Fatalf("precondition: the upgrade answered %d, want 101", ans.status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runs.Load() != 1 {
		t.Fatalf("precondition: without closeConnectionsLeftMidBody the WebSocket handler ran %d time(s), want 1",
			runs.Load())
	}

	addr, runs = serve(t, true)
	ans := exchange(t, addr, raw)
	if ans.status != fiber.StatusSwitchingProtocols || !ans.connClose {
		t.Fatalf("status %d, Connection: close %v; want the 101, asking for a close", ans.status, ans.connClose)
	}
	if !ans.closed || ans.next != nil {
		t.Errorf("closed %v, next answer %v: want the connection closed after the 101", ans.closed, ans.next)
	}
	if n := runs.Load(); n != 0 {
		t.Errorf("the WebSocket handler ran %d time(s) on a connection that was closed", n)
	}
}

// TestBodyReadToItsEndKeepsTheConnection is the other half: an answer that
// leaves nothing of the body unread keeps the connection, so a second request
// sent on it afterwards is served. A body read to its end past the prefetch,
// an empty body the route never reads, and a request that frames no body at
// all — the last two answered with errors, so the close is shown to follow the
// body, not the status.
func TestBodyReadToItsEndKeepsTheConnection(t *testing.T) {
	longEmail := `{"email":"` + strings.Repeat("a", 64<<10) + `"}`
	for _, tt := range []struct {
		name    string
		raw     string
		status  int
		message string
	}{
		{"a body the route reads to its end, far past the prefetch",
			requestHead(fiber.MethodPost, "/api/v1/auth/login", "Content-Type: application/json") +
				fmt.Sprintf("Content-Length: %d\r\n\r\n", len(longEmail)) + longEmail,
			fiber.StatusBadRequest, "email: must have at most 320 characters"},
		{"an empty body the route never reads",
			requestHead(fiber.MethodPost, "/api/v1/clusters", "Content-Type: application/json") + "Content-Length: 0\r\n\r\n",
			fiber.StatusUnauthorized, "Missing authorization token"},
		{"a request that frames no body",
			requestHead(fiber.MethodGet, "/api/v1/no-such-route") + "\r\n",
			fiber.StatusNotFound, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newProbedServer(t, true)
			conn, br := dialAndSend(t, srv.addr, tt.raw)
			ans := readAnswer(t, conn, br, methodOf(tt.raw))
			if ans.status != tt.status || (tt.message != "" && ans.envelope.Message != tt.message) {
				t.Fatalf("status %d %+v, want %d %q", ans.status, ans.envelope, tt.status, tt.message)
			}
			if ans.connClose {
				t.Error("the answer carries Connection: close")
			}
			writeRaw(t, conn, hiddenRequest)
			if next := readAnswer(t, conn, br, fiber.MethodGet); next.status != fiber.StatusNoContent || srv.hits.Load() != 1 {
				t.Errorf("the request sent after the answer got %d, and the probe was reached %d time(s); want it "+
					"served on the same connection", next.status, srv.hits.Load())
			}
		})
	}
}

// TestChunkedBodyIsRefusedOffTheStreamedUploadRoute pins the 411 and the one
// body exempt from it — a streamed multipart upload (isStreamedUpload) — against
// spellings the router sends to that route but the exemption does not accept:
// TestStreamedUploadExemptionIsNoLooserThanItsRoute shows, from the route
// table, that the capitals and the trailing slash do route to it. Refusing
// them chunked is the exemption being stricter than the router, the direction
// it is meant to err in; so is refusing a chunked JSON body sent to the upload
// as declared, which its declaration would read before any grant is checked.
// The rows with a Content-Length show the refusal is the chunked framing's
// alone: the same spellings with a Content-Length get past it — to
// authentication, or, for the trailing slash, to refuseTrailingSlashWrites,
// which answers a write whose path ends in "/" after the limiters.
func TestChunkedBodyIsRefusedOffTheStreamedUploadRoute(t *testing.T) {
	srv := newProbedServer(t, true)
	const multipart = "Content-Type: multipart/form-data; boundary=x"
	capitals := strings.ToUpper(uploadTarget)
	const refused, unauthorized = "length_required", "unauthorized"
	for _, tt := range []struct {
		name           string
		method, target string
		chunked        bool
		status         int
		slug, message  string
		contentType    string // "" sends multipart
	}{
		{"a chunked body, to a route that reads its body", fiber.MethodPost, "/api/v1/auth/login", true,
			fiber.StatusLengthRequired, refused, chunkedBodyRefusal, ""},
		{"a chunked body, to the storage upload", fiber.MethodPost, uploadTarget, true,
			fiber.StatusUnauthorized, unauthorized, "Missing authorization token", ""},
		{"a chunked JSON body, to the storage upload", fiber.MethodPost, uploadTarget, true,
			fiber.StatusLengthRequired, refused, chunkedBodyRefusal, "Content-Type: application/json"},
		{"a chunked body, to the upload spelled in capitals", fiber.MethodPost, capitals, true,
			fiber.StatusLengthRequired, refused, chunkedBodyRefusal, ""},
		{"a Content-Length body, to the upload spelled in capitals", fiber.MethodPost, capitals, false,
			fiber.StatusUnauthorized, unauthorized, "Missing authorization token", ""},
		{"a chunked body, to the upload with a trailing slash", fiber.MethodPost, uploadTarget + "/", true,
			fiber.StatusLengthRequired, refused, chunkedBodyRefusal, ""},
		{"a Content-Length body, to the upload with a trailing slash", fiber.MethodPost, uploadTarget + "/", false,
			fiber.StatusBadRequest, "bad_request", trailingSlashRefusal, ""},
		{"a chunked body, to the upload's path with another method", fiber.MethodPut, uploadTarget, true,
			fiber.StatusLengthRequired, refused, chunkedBodyRefusal, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			contentType := tt.contentType
			if contentType == "" {
				contentType = multipart
			}
			raw := requestHead(tt.method, tt.target, contentType)
			if tt.chunked {
				raw += "Transfer-Encoding: chunked\r\n\r\n4\r\n--x-\r\n0\r\n\r\n"
			} else {
				raw += "Content-Length: 4\r\n\r\n--x-"
			}
			ans := exchange(t, srv.addr, raw)
			if ans.status != tt.status || ans.envelope.Message != tt.message || ans.envelope.Error != tt.slug {
				t.Fatalf("status %d %+v, want %d %q with the slug %q", ans.status, ans.envelope, tt.status, tt.message,
					tt.slug)
			}
			if tt.status == fiber.StatusLengthRequired && (ans.requestID == "" || ans.nosniff != "nosniff") {
				t.Errorf("X-Request-Id %q, X-Content-Type-Options %q: the refusal did not pass through requestid "+
					"and the security headers", ans.requestID, ans.nosniff)
			}
		})
	}
}

// TestBodySizeGuardExemptsOnlyTheUploadRouteAsDeclared pins the 10 MiB guard's
// limit and its one exemption: a streamed multipart upload (isStreamedUpload).
// Each request sends only the part of its body fasthttp reads before any
// handler runs — it answers nothing until it has that much — and each answer
// comes without the rest being read.
//
// The JSON row is a body the upload's own declaration would read (bodyValues)
// before its handler checks any grant; it is refused like one sent anywhere
// else. The last row is a path the guard's earlier exemption — any path
// holding "/storage/" and ending in "/upload" — let through, although no route
// serves it.
func TestBodySizeGuardExemptsOnlyTheUploadRouteAsDeclared(t *testing.T) {
	srv := newProbedServer(t, true)
	const limit = 10 << 20
	const jsonType, multipart = "Content-Type: application/json", "Content-Type: multipart/form-data; boundary=x"
	for _, tt := range []struct {
		name        string
		target      string
		contentType string
		declared    int
		status      int
	}{
		{"at the limit, to an authenticated route", "/api/v1/clusters", jsonType, limit, fiber.StatusUnauthorized},
		{"over the limit, to the same route", "/api/v1/clusters", jsonType, limit + 1, fiber.StatusRequestEntityTooLarge},
		{"over the limit, a multipart body to the storage upload", uploadTarget, multipart, limit + 1,
			fiber.StatusUnauthorized},
		{"over the limit, a JSON body to the storage upload", uploadTarget, jsonType, limit + 1,
			fiber.StatusRequestEntityTooLarge},
		{"over the limit, to the upload spelled in capitals", strings.ToUpper(uploadTarget), multipart, limit + 1,
			fiber.StatusRequestEntityTooLarge},
		{"over the limit, to the upload with a trailing slash", uploadTarget + "/", multipart, limit + 1,
			fiber.StatusRequestEntityTooLarge},
		{"over the limit, to a path under a storage pool that ends in /upload", strings.TrimSuffix(uploadTarget, "upload") +
			"x/upload", multipart, limit + 1, fiber.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := requestHead(fiber.MethodPost, tt.target, tt.contentType) +
				fmt.Sprintf("Content-Length: %d\r\n\r\n", tt.declared) + strings.Repeat("x", bodyPrefetch)
			if ans := exchange(t, srv.addr, raw); ans.status != tt.status {
				t.Errorf("status %d (%s), want %d", ans.status, ans.body, tt.status)
			}
		})
	}
}

// TestStreamedUploadExemptionIsNoLooserThanItsRoute holds
// isStreamedUploadRequest to the router, over the assembled route table.
//
// Every path it accepts has to be one the router sends to the upload route: of
// the POST routes, in the order the router tries them, the first whose pattern
// matches it (fiber.RoutePatternMatch, under the app's own config) has to be
// storageUploadPath. The paths tried put, in each parameter position, a uuid,
// a number (for a route whose parameter carries a constraint such as <int>),
// a few shapes Fiber's pattern syntax treats specially, and every literal any
// POST route has in that position — a route with a literal where the upload
// has a parameter is found by the probe that puts that literal there — in
// every combination. Use-mounted middleware is not a route and is not in the
// table; a middleware that answered such a path itself would be a gate, not
// a different route.
//
// And every path it refuses has to stay refused when only one literal segment
// differs: each literal position is tried with every literal the POST routes
// have there, and must not be exempt — an exemption that compared fewer
// segments than it should would take one of them. Where no route varies a
// position (api, v1), a row below does.
//
// The spellings it refuses are listed with where the router sends them, so
// that the ones it refuses although the router would serve them are
// deliberate: the capitals and the trailing slash.
func TestStreamedUploadExemptionIsNoLooserThanItsRoute(t *testing.T) {
	s := newAssembledServer(t)
	cfg := s.app.Config()

	var post []fiber.Route
	declared := 0
	for _, r := range s.app.GetRoutes(true) {
		if r.Path == storageUploadPath {
			declared++
			if r.Method != fiber.MethodPost {
				t.Errorf("%s %s: the exemption accepts POST alone", r.Method, r.Path)
			}
		}
		if r.Method == fiber.MethodPost {
			post = append(post, r)
		}
	}
	if declared != 1 {
		t.Fatalf("precondition: %d routes are mounted on %s, want the upload route alone", declared, storageUploadPath)
	}
	routedTo := func(path string) string {
		for _, r := range post {
			if fiber.RoutePatternMatch(path, r.Path, cfg) {
				return r.Path
			}
		}
		return ""
	}
	// accepts asks the exemption of a multipart body — the one kind it is for;
	// TestStreamedUploadPredicateSeesThePathTheRouterSees holds it to that kind.
	accepts := func(method, path string) bool {
		fctx := &fasthttp.RequestCtx{}
		fctx.Request.Header.SetMethod(method)
		fctx.Request.SetRequestURI(path)
		fctx.Request.Header.SetContentType("multipart/form-data; boundary=x")
		c := s.app.AcquireCtx(fctx)
		defer s.app.ReleaseCtx(c)
		return isStreamedUploadRequest(c)
	}

	pattern := strings.Split(storageUploadPath, "/")
	var params []int
	for i, seg := range pattern {
		if strings.HasPrefix(seg, ":") {
			params = append(params, i)
		}
	}
	if len(params) != 2 {
		t.Fatalf("precondition: %s has %d parameters; this test places probes in two", storageUploadPath, len(params))
	}
	vocab := make([][]string, len(params))
	for k, i := range params {
		words := map[string]bool{testClusterID: true, "1": true, "a%2Fb": true, "x.y": true, "x-y": true}
		for _, r := range post {
			if segs := strings.Split(r.Path, "/"); i < len(segs) && segs[i] != "" && !strings.HasPrefix(segs[i], ":") {
				words[segs[i]] = true
			}
		}
		vocab[k] = slices.Sorted(maps.Keys(words))
	}
	probes := 0
	for _, x := range vocab[0] {
		for _, y := range vocab[1] {
			segs := slices.Clone(pattern)
			segs[params[0]], segs[params[1]] = x, y
			path := strings.Join(segs, "/")
			probes++
			if !accepts(fiber.MethodPost, path) {
				t.Errorf("POST %s is refused, though it is spelled as the upload route is declared", path)
			}
			if got := routedTo(path); got != storageUploadPath {
				t.Errorf("POST %s is exempt, but the router sends it to %q", path, got)
			}
		}
	}
	if probes < 25 {
		t.Fatalf("precondition: only %d paths tried; the route table's literals should give far more", probes)
	}
	t.Logf("%d paths tried against %d POST routes", probes, len(post))

	variants := 0
	for i, want := range pattern {
		if i == 0 || strings.HasPrefix(want, ":") {
			continue
		}
		words := map[string]bool{}
		for _, r := range post {
			if segs := strings.Split(r.Path, "/"); i < len(segs) && segs[i] != want && segs[i] != "" &&
				!strings.HasPrefix(segs[i], ":") {
				words[segs[i]] = true
			}
		}
		for _, word := range slices.Sorted(maps.Keys(words)) {
			segs := strings.Split(uploadTarget, "/")
			segs[i] = word
			path := strings.Join(segs, "/")
			variants++
			if accepts(fiber.MethodPost, path) {
				t.Errorf("POST %s is exempt, though its segment %d is %q where the route declares %q (the router "+
					"sends it to %q)", path, i, word, want, routedTo(path))
			}
		}
	}
	if variants < 10 {
		t.Fatalf("precondition: only %d one-literal variants tried; the route table's literals should give far more",
			variants)
	}
	t.Logf("%d one-literal variants tried", variants)

	prefix := strings.TrimSuffix(uploadTarget, "/upload")
	for _, tt := range []struct {
		name, method, path string
		routedTo           string // where the router sends it as a POST
	}{
		{"another method", fiber.MethodPut, uploadTarget, storageUploadPath},
		{"GET", fiber.MethodGet, uploadTarget, storageUploadPath},
		{"capitals", fiber.MethodPost, strings.ToUpper(uploadTarget), storageUploadPath},
		{"a capital in one literal segment", fiber.MethodPost, prefix + "/Upload", storageUploadPath},
		{"a trailing slash", fiber.MethodPost, uploadTarget + "/", storageUploadPath},
		{"two trailing slashes", fiber.MethodPost, uploadTarget + "//", storageUploadPath},
		{"an empty parameter", fiber.MethodPost, "/api/v1/clusters//storage/" + testClusterID + "/upload", ""},
		{"a segment more", fiber.MethodPost, prefix + "/x/upload", ""},
		{"a segment on the end", fiber.MethodPost, uploadTarget + "/x", ""},
		{"a longer last segment", fiber.MethodPost, uploadTarget + "s", ""},
		{"no cluster", fiber.MethodPost, "/api/v1/storage/" + testClusterID + "/upload", ""},
		{"a prefix before the route", fiber.MethodPost, "/x" + uploadTarget, ""},
		{"another first segment", fiber.MethodPost, strings.Replace(uploadTarget, "/api/", "/apx/", 1), ""},
		{"another API version", fiber.MethodPost, strings.Replace(uploadTarget, "/v1/", "/v2/", 1), ""},
		{"another collection", fiber.MethodPost, strings.Replace(uploadTarget, "/clusters/", "/nodes/", 1), ""},
		{"another sub-collection", fiber.MethodPost, strings.Replace(uploadTarget, "/storage/", "/vms/", 1), ""},
		{"a sibling action", fiber.MethodPost, prefix + "/oci-pull", storageScope + "/:storage_id/oci-pull"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if accepts(tt.method, tt.path) {
				t.Errorf("%s %s is exempt; it is not spelled as the upload route is declared", tt.method, tt.path)
			}
			if got := routedTo(tt.path); got != tt.routedTo {
				t.Errorf("the router sends POST %s to %q, want %q", tt.path, got, tt.routedTo)
			}
		})
	}
}

// TestChunkedBodyClosesTheConnectionAfterItsStreamIsReleased is why a chunked
// body closes the connection whether or not its stream is still attached.
//
// A chunk-size line that is not hex makes the stream's read fail — readHexInt
// consumes the byte it rejects — and c.Body() then releases the stream as it
// does at the last chunk, with the rest of the body still on the connection.
// The precondition twin, an app without closeConnectionsLeftMidBody, shows
// both halves: the stream is no longer attached once the handler has read, so
// a check of the stream alone would have kept the connection, and the request
// behind the bad chunk is then served.
//
// It runs on a bare app with the server's Fiber config, not the assembled
// server: there, refuseChunkedRequestBodies lets a chunked body reach the
// storage upload alone, whose handler reads the stream itself and never calls
// c.Body() on it.
func TestChunkedBodyClosesTheConnectionAfterItsStreamIsReleased(t *testing.T) {
	serve := func(t *testing.T, guarded bool) (string, *atomic.Int32, *atomic.Bool) {
		t.Helper()
		app := fiber.New(buildFiberConfig(&config.Config{}))
		if guarded {
			closeConnectionsLeftMidBody(app, bodyReadTimeout)
		}
		hits := new(atomic.Int32)
		released := new(atomic.Bool)
		app.Post("/reads-its-body", func(c fiber.Ctx) error {
			n := len(c.Body())
			released.Store(!c.Request().IsBodyStream())
			return c.SendString(strconv.Itoa(n))
		})
		app.Get(hiddenRequestProbe, func(c fiber.Ctx) error {
			hits.Add(1)
			return c.SendStatus(fiber.StatusNoContent)
		})
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
		t.Cleanup(func() { _ = app.Shutdown() })
		return ln.Addr().String(), hits, released
	}
	raw := requestHead(fiber.MethodPost, "/reads-its-body") + "Transfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n" + "Z" + hiddenRequest

	addr, hits, released := serve(t, false)
	ans := exchange(t, addr, raw)
	if ans.status != fiber.StatusOK || !released.Load() {
		t.Fatalf("precondition: status %d, stream released %v; want the handler to have read the body and "+
			"fasthttp to have let go of its stream", ans.status, released.Load())
	}
	if ans.next == nil || ans.next.StatusCode != fiber.StatusNoContent || hits.Load() != 1 {
		t.Fatalf("precondition: without closeConnectionsLeftMidBody the request behind the bad chunk should have "+
			"been served (next answer %v, closed %v, probe reached %d time(s))", ans.next, ans.closed, hits.Load())
	}

	addr, hits, _ = serve(t, true)
	ans = exchange(t, addr, raw)
	if ans.status != fiber.StatusOK {
		t.Fatalf("status %d (%s), want the handler's 200", ans.status, ans.body)
	}
	if !ans.connClose || !ans.closed || ans.next != nil {
		t.Errorf("Connection: close %v, closed %v, next answer %v: want the connection closed after the answer",
			ans.connClose, ans.closed, ans.next)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the request behind the bad chunk was served %d time(s)", n)
	}
}

// TestServerConfigKeepsTheBodyFramingInvariants pins the fasthttp server New
// builds to the configuration closeConnectionsLeftMidBody and
// bodyMayBeLeftUnread are written for. Each row says what breaks without it.
// The timeouts are held to their own reasons as well: the idle timeout has to
// outlast the longest upstream idle timeout of the proxies the README
// configures (Caddy's two minutes), the read timeout has to be shorter than
// the idle one — it bounds what comes after the idle wait, never the wait —
// and the body timeout is the operator's five minutes, which the wrapper New
// installs has to arm before a handler runs: from then, on every request but a
// multipart one for the storage upload — a JSON one for the upload included —
// and not at all on that one.
// TestServerTimeoutsBoundTheHeadTheBodyAndTheWaitBetweenRequests shows what
// the timeouts do, on this same server, with shorter values.
func TestServerConfigKeepsTheBodyFramingInvariants(t *testing.T) {
	s := newAssembledServer(t)
	srv := s.app.Server()
	handler := runtime.FuncForPC(reflect.ValueOf(srv.Handler).Pointer()).Name()
	errorHandler := runtime.FuncForPC(reflect.ValueOf(srv.ErrorHandler).Pointer()).Name()
	for _, tt := range []struct {
		name string
		ok   bool
		why  string
	}{
		{"it serves through closeConnectionsLeftMidBody", strings.Contains(handler, "/internal/api.closeConnectionsLeftMidBody.func"),
			"that is what closes the connection after a body left unread, and what replaces ReadTimeout with the " +
				"body deadline before a handler reads a body — without it, ReadTimeout cuts a streamed upload a minute " +
				"after its first byte, and nothing closes a connection whose body read a deadline cut"},
		{"ReadTimeout is headReadTimeout", srv.ReadTimeout == headReadTimeout,
			"it is the one bound on a request's head and on fasthttp's read-ahead of its body"},
		{"IdleTimeout is keepAliveIdleTimeout", srv.IdleTimeout == keepAliveIdleTimeout,
			"it is the bound on the wait between requests; zero, fasthttp would use ReadTimeout there instead"},
		{"ContinueHandler is nil", srv.ContinueHandler == nil,
			"a request it turns down is answered 417 without the handler running — closeConnectionsLeftMidBody " +
				"included — on a connection kept alive, with the body the client may already be sending unread"},
		{"HeaderReceived is nil", srv.HeaderReceived == nil,
			"it can arm a read deadline of its own and change the body-size limit per request, after the head, " +
				"which is not what headReadTimeout's bound on the read-ahead or the read-ahead fixtures are written for"},
		{"DisablePreParseMultipartForm is set", srv.DisablePreParseMultipartForm,
			"otherwise fasthttp reads every multipart body — an ISO upload included — into memory and temporary " +
				"files before any handler runs"},
		{"StreamRequestBody is set", srv.StreamRequestBody,
			"otherwise fasthttp reads every body whole before the handler runs, refusing one over BodyLimit — an " +
				"ISO upload among them"},
		{"its errors are answered through redactServerErrorBodies",
			strings.Contains(errorHandler, "/internal/api.redactServerErrorBodies.func"),
			"that is what keeps the request out of the answer to a request fasthttp cannot read — without it, a " +
				"request line with no HTTP version is answered with the whole head, its cookie and bearer token included"},
		{"SecureErrorLogMessage is set", srv.SecureErrorLogMessage,
			"otherwise the error fasthttp hands its ErrorHandler carries a snippet of the buffered request, its cookie " +
				"included, into the text Fiber's handler classifies — kept from the client by the redaction alone"},
	} {
		if !tt.ok {
			t.Errorf("%s: not so (handler %s, error handler %s) — %s", tt.name, handler, errorHandler, tt.why)
		}
	}
	if keepAliveIdleTimeout <= 2*time.Minute {
		t.Errorf("keepAliveIdleTimeout = %v; it has to outlast Caddy's two-minute upstream idle timeout", keepAliveIdleTimeout)
	}
	if headReadTimeout <= 0 || headReadTimeout >= keepAliveIdleTimeout {
		t.Errorf("headReadTimeout = %v; it has to be set, and shorter than keepAliveIdleTimeout (%v)",
			headReadTimeout, keepAliveIdleTimeout)
	}
	if bodyReadTimeout != 5*time.Minute {
		t.Errorf("bodyReadTimeout = %v; the operator set five minutes, and docs/api-reference.md says so", bodyReadTimeout)
	}

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln := &deadlineRecordingListener{Listener: inner}
	go func() { _ = s.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = s.app.Shutdown() })
	for _, tt := range []struct {
		name, raw string
		status    int
		exempt    bool
	}{
		{"a request for another route", requestHead(fiber.MethodGet, "/api/v1/version") + "\r\n", fiber.StatusOK, false},
		{"a multipart request for the storage upload", requestHead(fiber.MethodPost, uploadTarget,
			"Content-Type: multipart/form-data; boundary=x") + "Content-Length: 0\r\n\r\n", fiber.StatusUnauthorized, true},
		{"a JSON request for the storage upload", requestHead(fiber.MethodPost, uploadTarget,
			"Content-Type: application/json") + "Content-Length: 0\r\n\r\n", fiber.StatusUnauthorized, false},
	} {
		before := time.Now()
		conn, br := dialAndSend(t, ln.Addr().String(), tt.raw)
		if ans := readAnswer(t, conn, br, methodOf(tt.raw)); ans.status != tt.status {
			t.Fatalf("%s: status %d (%s), want %d", tt.name, ans.status, ans.body, tt.status)
		}
		after := time.Now()
		set := ln.take()
		switch {
		case len(set) != 1:
			t.Errorf("%s: closeConnectionsLeftMidBody set %d read deadline(s), want one", tt.name, len(set))
		case tt.exempt && !set[0].IsZero():
			t.Errorf("%s: the body deadline is %v, want none: the upload streams for as long as it takes", tt.name,
				set[0].Sub(before))
		case !tt.exempt && (set[0].Before(before.Add(bodyReadTimeout)) || set[0].After(after.Add(bodyReadTimeout))):
			t.Errorf("%s: the body deadline is %v after the request went out, want bodyReadTimeout (%v) from when "+
				"the handler started", tt.name, set[0].Sub(before), bodyReadTimeout)
		}
	}
}

// TestServerTimeoutsBoundTheHeadTheBodyAndTheWaitBetweenRequests runs the
// assembled server with its three read timeouts shortened — the one thing
// changed, so the test waits seconds rather than minutes — and holds them to
// what headReadTimeout's, bodyReadTimeout's and keepAliveIdleTimeout's
// comments say they bound: the head of a request and fasthttp's read-ahead of
// its body; a handler's reads of the rest of the body, from when the handler
// starts; and the wait between requests. Not a WebSocket. The cases before the
// last are also what make it mean anything: they show every timeout is live
// on this server.
//
// Every lower bound is timed from before the client sends or dials, and the
// server arms a deadline only after that, so a close sooner than the timeout
// is a close from something else.
func TestServerTimeoutsBoundTheHeadTheBodyAndTheWaitBetweenRequests(t *testing.T) {
	const readTimeout, idleTimeout, bodyTimeout = 400 * time.Millisecond, 700 * time.Millisecond, 1200 * time.Millisecond
	const slack = 2 * time.Second
	s := newAssembledServer(t)
	s.app.Post("/api/v1/stalled-body-probe", func(c fiber.Ctx) error {
		// Read as UploadFile reads: the stream itself, not c.Body().
		n, err := io.Copy(io.Discard, c.RequestCtx().RequestBodyStream())
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "the body read failed: "+err.Error())
		}
		return c.SendString(strconv.FormatInt(n, 10))
	})
	s.app.Get("/api/v1/ws-echo-probe", websocket.New(func(c *websocket.Conn) {
		for {
			kind, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}))
	s.app.Server().IdleTimeout = idleTimeout
	rewrapped(readTimeout, withBodyTimeout(bodyTimeout))(s.app)
	addr := serveOnLoopback(t, s)

	// keptAlive opens a connection and has one request served on it, so that
	// what a case sends next is the connection's second request: the kind
	// whose wait IdleTimeout bounds, and whose head ReadTimeout bounds from
	// its first byte.
	keptAlive := func(t *testing.T) (net.Conn, *bufio.Reader) {
		t.Helper()
		conn, br := dialAndSend(t, addr, requestHead(fiber.MethodGet, "/api/v1/version")+"\r\n")
		if ans := readAnswer(t, conn, br, fiber.MethodGet); ans.status != fiber.StatusOK || ans.connClose {
			t.Fatalf("precondition: GET /api/v1/version answered %d, Connection: close %v; want a kept-alive 200",
				ans.status, ans.connClose)
		}
		return conn, br
	}
	// cutByTheReadTimeout holds what the connection says next to fasthttp's
	// answer to a read deadline: a 408, no sooner than readTimeout after
	// since, and then the end of the connection.
	cutByTheReadTimeout := func(t *testing.T, conn net.Conn, br *bufio.Reader, since time.Time) {
		t.Helper()
		ans := readAnswer(t, conn, br, fiber.MethodGet)
		elapsed := time.Since(since)
		if ans.status != fiber.StatusRequestTimeout || ans.envelope.Error != "request_timeout" {
			t.Fatalf("status %d %+v after %v, want the 408 with its slug", ans.status, ans.envelope, elapsed)
		}
		if elapsed < readTimeout {
			t.Errorf("answered %v after the request went out, with a %v read timeout: before it could expire",
				elapsed, readTimeout)
		}
		if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
			t.Errorf("after the 408 the connection gave %v, want its end", err)
		}
	}

	t.Run("an idle kept-alive connection is closed after the idle timeout, silently, not before", func(t *testing.T) {
		sent := time.Now()
		conn, br := keptAlive(t)
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, err := br.ReadByte()
		elapsed := time.Since(sent)
		var netErr net.Error
		switch {
		case err == nil:
			t.Fatal("the server sent something on an idle connection; its close is meant to be silent")
		case errors.As(err, &netErr) && netErr.Timeout():
			t.Fatalf("the connection was still open %v after the request went out", elapsed)
		}
		if elapsed < idleTimeout {
			t.Errorf("closed %v after the request went out, with a %v idle timeout: before it could expire",
				elapsed, idleTimeout)
		}
	})

	t.Run("a first request that never comes is answered 408 after the read timeout, not before", func(t *testing.T) {
		dialed := time.Now()
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		cutByTheReadTimeout(t, conn, bufio.NewReader(conn), dialed)
	})

	t.Run("a head trickled past the read timeout is answered 408, not before", func(t *testing.T) {
		conn, br := keptAlive(t)
		sent := time.Now()
		writeRaw(t, conn, "GET /api/v1/version HTTP/1.1\r\nHost: exa")
		cutByTheReadTimeout(t, conn, br, sent)
	})

	t.Run("a head trickled within the read timeout is served", func(t *testing.T) {
		conn, br := keptAlive(t)
		writeRaw(t, conn, "GET /api/v1/version HTTP/1.1\r\nHo")
		time.Sleep(readTimeout / 4)
		writeRaw(t, conn, "st: example.com\r\n\r\n")
		if ans := readAnswer(t, conn, br, fiber.MethodGet); ans.status != fiber.StatusOK {
			t.Errorf("status %d, want the 200 of a head that arrived inside the read timeout", ans.status)
		}
	})

	t.Run("a body whose first 8 KiB trickle past the read timeout is answered 408, not before", func(t *testing.T) {
		conn, br := keptAlive(t)
		sent := time.Now()
		writeRaw(t, conn, requestHead(fiber.MethodPost, "/api/v1/stalled-body-probe", "Content-Type: application/octet-stream")+
			"Content-Length: 16384\r\n\r\n"+strings.Repeat("a", 4<<10))
		cutByTheReadTimeout(t, conn, br, sent)
	})

	const size, first = 16 << 10, 12 << 10 // a stall comes after the read-ahead, with the handler reading
	stalledBody := requestHead(fiber.MethodPost, "/api/v1/stalled-body-probe", "Content-Type: application/octet-stream") +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", size) + strings.Repeat("a", first)

	t.Run("a body stalled past the read timeout, within the body timeout, is read in full", func(t *testing.T) {
		const stall = (readTimeout + bodyTimeout) / 2
		conn, br := keptAlive(t)
		writeRaw(t, conn, stalledBody)
		time.Sleep(stall)
		writeRaw(t, conn, strings.Repeat("a", size-first))
		if ans := readAnswer(t, conn, br, fiber.MethodPost); ans.status != fiber.StatusOK || string(ans.body) != strconv.Itoa(size) {
			t.Errorf("status %d, body %q; want 200 and the whole %d bytes read after a %v stall", ans.status, ans.body,
				size, stall)
		}
	})

	t.Run("a body stalled past the body timeout is cut at it, not before, and the connection closed", func(t *testing.T) {
		conn, br := keptAlive(t)
		sent := time.Now()
		writeRaw(t, conn, stalledBody)
		ans := readAnswer(t, conn, br, fiber.MethodPost)
		elapsed := time.Since(sent)
		if ans.status != fiber.StatusBadRequest || !strings.HasPrefix(ans.envelope.Message, "the body read failed") {
			t.Fatalf("status %d (%s), want the probe's answer to a read that failed", ans.status, ans.body)
		}
		if elapsed < bodyTimeout || elapsed > bodyTimeout+slack {
			t.Errorf("answered %v after the request went out, want the read cut at the %v body timeout", elapsed,
				bodyTimeout)
		}
		if !ans.connClose {
			t.Error("the answer to a read the body timeout cut does not carry Connection: close")
		}
	})

	t.Run("a WebSocket idle longer than every timeout stays open", func(t *testing.T) {
		const stall = 3 * bodyTimeout // the longest of the three
		conn, br := keptAlive(t)
		if br.Buffered() != 0 {
			t.Fatalf("precondition: %d bytes already read past the answer would be lost to the upgrade", br.Buffered())
		}
		dialer := gorillaws.Dialer{
			HandshakeTimeout: 5 * time.Second,
			NetDialContext:   func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		}
		ws, res, err := dialer.Dial("ws://example.com/api/v1/ws-echo-probe", nil)
		if err != nil {
			t.Fatalf("upgrade on the kept-alive connection: %v", err)
		}
		defer func() { _ = ws.Close() }()
		if res.StatusCode != fiber.StatusSwitchingProtocols {
			t.Fatalf("upgrade status %d, want 101", res.StatusCode)
		}
		time.Sleep(stall)
		if err := ws.WriteMessage(gorillaws.TextMessage, []byte("still here")); err != nil {
			t.Fatalf("write after idling: %v", err)
		}
		if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		if _, msg, err := ws.ReadMessage(); err != nil || string(msg) != "still here" {
			t.Errorf("echo after idling: %q, %v; want the message back", msg, err)
		}
	})
}

// TestMatchesDeclaredPathFailsClosedOnPatternSyntaxItDoesNotRead holds
// matchesDeclaredPath to its own comment: a pattern with any syntax beyond
// literals and plain parameters matches no path — neither one the router
// serves with that pattern nor one it refuses — so the route it declares loses
// its exemption rather than exempting paths it does not serve. The positive
// control is a plain pattern, which matches exactly what the router matches.
func TestMatchesDeclaredPathFailsClosedOnPatternSyntaxItDoesNotRead(t *testing.T) {
	cfg := newAssembledServer(t).app.Config()
	for _, tt := range []struct {
		pattern string
		served  []string // paths the router matches with pattern — the pattern's own spelling among them, where it is one
		refused string   // a path it does not
	}{
		{"/a/:id<int>/upload", []string{"/a/1/upload"}, "/a/foo/upload"},
		{"/a/:name.:ext/upload", []string{"/a/x.y/upload"}, "/a/foo/upload"},
		{"/a/:x-:y/upload", []string{"/a/p-q/upload"}, "/a/foo/upload"},
		{"/a/:id?/upload", []string{"/a/1/upload"}, "/a/1/2/upload"},
		{"/a/*", []string{"/a/x/y", "/a/*"}, "/b/x"},
		{"/a/+", []string{"/a/x", "/a/+"}, "/b/x"},
		{"/a/b*", []string{"/a/bcd", "/a/b*"}, "/a/xcd"},
	} {
		t.Run(tt.pattern, func(t *testing.T) {
			for _, path := range tt.served {
				if !fiber.RoutePatternMatch(path, tt.pattern, cfg) {
					t.Fatalf("precondition: the router does not match %s with %s", path, tt.pattern)
				}
			}
			if fiber.RoutePatternMatch(tt.refused, tt.pattern, cfg) {
				t.Fatalf("precondition: the router matches %s with %s", tt.refused, tt.pattern)
			}
			for _, path := range append(slices.Clone(tt.served), tt.refused) {
				if matchesDeclaredPath(path, tt.pattern) {
					t.Errorf("%s matches %s; a pattern with syntax it does not read must match nothing", path, tt.pattern)
				}
			}
		})
	}

	const plain = "/a/:id/upload"
	for path, want := range map[string]bool{"/a/1/upload": true, "/a/foo/upload": true, "/a//upload": false, "/a/1/2/upload": false} {
		if got := matchesDeclaredPath(path, plain); got != want || got != fiber.RoutePatternMatch(path, plain, cfg) {
			t.Errorf("%s against %s: matchesDeclaredPath %v, router %v; want both %v",
				path, plain, got, fiber.RoutePatternMatch(path, plain, cfg), want)
		}
	}
}
