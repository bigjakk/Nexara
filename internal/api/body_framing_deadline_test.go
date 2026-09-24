package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/config"
)

// These tests hold closeConnectionsLeftMidBody's body deadline — how long a
// handler's reads of a body may take, on every request but the storage upload
// — to what it is for, on real TCP connections. They arm it test-sized, so
// they wait a second where the server waits bodyReadTimeout;
// TestServerConfigKeepsTheBodyFramingInvariants holds New to arming
// bodyReadTimeout itself.

// rewrapped takes New's closeConnectionsLeftMidBody back off the server and
// puts wrap in its place, with the server's ReadTimeout set to readTimeout
// when that is not zero. It is a mount for newProbedServer, to run last.
func rewrapped(readTimeout time.Duration, wrap func(*fiber.App)) func(*fiber.App) {
	return func(app *fiber.App) {
		if readTimeout > 0 {
			app.Server().ReadTimeout = readTimeout
		}
		app.Server().Handler = app.Handler()
		wrap(app)
	}
}

// withBodyTimeout is closeConnectionsLeftMidBody at the given body timeout.
func withBodyTimeout(bodyTimeout time.Duration) func(*fiber.App) {
	return func(app *fiber.App) { closeConnectionsLeftMidBody(app, bodyTimeout) }
}

// withoutTheCloseOnExpiry is the precondition twin of closeConnectionsLeftMidBody
// for the body deadline: it arms the same deadline and keeps the unread-body
// rule, and does not close the connection once the deadline has passed.
func withoutTheCloseOnExpiry(bodyTimeout time.Duration) func(*fiber.App) {
	return func(app *fiber.App) {
		srv := app.Server()
		next := srv.Handler
		srv.Handler = func(ctx *fasthttp.RequestCtx) {
			framing := ctx.Request.Header.ContentLength()
			_ = ctx.Conn().SetReadDeadline(time.Now().Add(bodyTimeout))
			next(ctx)
			if bodyMayBeLeftUnread(framing, ctx.Request.IsBodyStream()) {
				ctx.Response.SetConnectionClose()
			}
		}
	}
}

// writeUnlessClosed writes raw to conn, which the server may already have
// closed: a write that fails then is what a closed connection does, and what
// follows it says so.
func writeUnlessClosed(conn net.Conn, raw string) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, raw)
}

// serveAppOnLoopback serves app on a loopback listener for the rest of the
// test.
func serveAppOnLoopback(t *testing.T, app *fiber.App) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return ln.Addr().String()
}

// TestSlowBodyIsCutAtTheBodyDeadlineAndItsConnectionClosed sends two routes
// that read a body before any session is checked or any rate limiter counts
// the head of a request declaring a 10 MiB body — the most the body-size guard
// lets through — and the first 8 KiB of it, and then nothing. The route's read
// of the rest has to be cut at the body deadline, not before it and not long
// after, and the connection closed after the answer, so that what the client
// sends next — once the answer is in, as the body bytes it still owed — is
// never read as a request. What it sends is a request.
//
// The precondition twin is the same server with the body deadline armed and
// no close on its expiry, the unread-body rule alone: the cut read released
// the body's stream as a read to the end would have, so that rule keeps the
// connection, and the request sent next is served.
//
// ReadTimeout is shortened too, below the body timeout: left armed through
// the handler, it would cut the read sooner, which the lower bound catches.
// The upper bound is what shows the deadline did the cutting: nothing else
// ends that read before the test stops waiting.
func TestSlowBodyIsCutAtTheBodyDeadlineAndItsConnectionClosed(t *testing.T) {
	const readTimeout, bodyTimeout, slack = 300 * time.Millisecond, time.Second, 2 * time.Second
	for _, tt := range []struct {
		name, target string
		status       int
		message      string
	}{
		{"logout, which binds its body first", "/api/v1/auth/logout", fiber.StatusOK, "Logged out successfully"},
		{"the OIDC token exchange, which reads its body for its parameters", "/api/v1/auth/oidc/token-exchange",
			fiber.StatusBadRequest, "request body is not valid JSON"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := requestHead(fiber.MethodPost, tt.target, "Content-Type: application/json") +
				"Content-Length: 10485760\r\n\r\n" + strings.Repeat(" ", bodyPrefetch)
			send := func(t *testing.T, srv probedServer) wireAnswer {
				t.Helper()
				sent := time.Now()
				conn, br := dialAndSend(t, srv.addr, raw)
				ans := readAnswer(t, conn, br, fiber.MethodPost)
				elapsed := time.Since(sent)
				if ans.status != tt.status || ans.envelope.Message != tt.message {
					t.Fatalf("status %d (%s), want %d %q", ans.status, ans.body, tt.status, tt.message)
				}
				if elapsed < bodyTimeout {
					t.Errorf("answered %v after the request went out: before the %v body deadline could expire",
						elapsed, bodyTimeout)
				}
				if elapsed > bodyTimeout+slack {
					t.Errorf("answered %v after the request went out: the %v body deadline did not end the read",
						elapsed, bodyTimeout)
				}
				writeUnlessClosed(conn, hiddenRequest)
				ans.next, ans.closed = whatFollows(t, conn, br)
				return ans
			}

			twin := newProbedServer(t, true, rewrapped(readTimeout, withoutTheCloseOnExpiry(bodyTimeout)))
			ans := send(t, twin)
			if ans.next == nil || ans.next.StatusCode != fiber.StatusNoContent || twin.hits.Load() != 1 {
				t.Fatalf("precondition: without the close on expiry the request sent after the cut should have been "+
					"served (next answer %v, closed %v, probe reached %d time(s))", ans.next, ans.closed, twin.hits.Load())
			}

			guarded := newProbedServer(t, true, rewrapped(readTimeout, withBodyTimeout(bodyTimeout)))
			ans = send(t, guarded)
			if !ans.connClose {
				t.Error("the answer does not carry Connection: close")
			}
			if !ans.closed || ans.next != nil {
				t.Errorf("closed %v, next answer %v: want the connection closed after the answer", ans.closed, ans.next)
			}
			if n := guarded.hits.Load(); n != 0 {
				t.Errorf("the request sent after the cut was served %d time(s)", n)
			}
		})
	}
}

// TestBodyReadBeforeTheBodyDeadlineKeepsTheConnection is the other side of the
// deadline: a body that is slow to arrive, but in full well before the
// deadline, keeps the connection, and a request sent on it afterwards is
// served. The rest of the body goes out three fifths of the way to the
// deadline.
func TestBodyReadBeforeTheBodyDeadlineKeepsTheConnection(t *testing.T) {
	const bodyTimeout = 2 * time.Second
	const rest = bodyTimeout * 3 / 5
	srv := newProbedServer(t, true, rewrapped(0, withBodyTimeout(bodyTimeout)))
	body := `{"refresh_token":""}` + strings.Repeat(" ", 12<<10)
	head := requestHead(fiber.MethodPost, "/api/v1/auth/logout", "Content-Type: application/json") +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))

	sent := time.Now()
	conn, br := dialAndSend(t, srv.addr, head+body[:bodyPrefetch])
	time.Sleep(rest)
	writeRaw(t, conn, body[bodyPrefetch:])
	ans := readAnswer(t, conn, br, fiber.MethodPost)
	elapsed := time.Since(sent)
	if ans.status != fiber.StatusOK || ans.envelope.Message != "Logged out successfully" {
		t.Fatalf("status %d (%s), want logout's 200", ans.status, ans.body)
	}
	if elapsed < rest {
		t.Fatalf("precondition: answered %v after the request went out, before the rest of its body (%v)", elapsed, rest)
	}
	if elapsed >= bodyTimeout {
		t.Fatalf("precondition: answered %v after the request went out, past the %v body deadline; the case is "+
			"meant to answer before it", elapsed, bodyTimeout)
	}
	if ans.connClose {
		t.Error("the answer carries Connection: close")
	}
	writeRaw(t, conn, hiddenRequest)
	if next := readAnswer(t, conn, br, fiber.MethodGet); next.status != fiber.StatusNoContent || srv.hits.Load() != 1 {
		t.Errorf("the request sent after the answer got %d, and the probe was reached %d time(s); want it served "+
			"on the same connection", next.status, srv.hits.Load())
	}
}

// TestStreamedUploadBodyIsNotCutByTheBodyDeadline holds the body deadline's one
// exemption to the body it is for. A multipart body sent to a route at
// storageUploadPath, read as UploadFile reads — the stream itself, not
// c.Body() — stalls for three times the deadline and is read in full; a body
// of another type sent to the same route, the same route under another
// method, spellings of that path isStreamedUpload does not accept, and another
// path, have the read cut at the deadline.
//
// It runs on a bare app with the server's Fiber config and nothing but
// closeConnectionsLeftMidBody, so that the handler reads whatever reaches it:
// the assembled server answers the upload route's own 401 first. The encoded
// slash is a spelling the router sends to the upload route and
// isStreamedUpload accepts in the path as it arrived — decoded, it is a
// segment more.
func TestStreamedUploadBodyIsNotCutByTheBodyDeadline(t *testing.T) {
	const bodyTimeout, slack = 500 * time.Millisecond, 2 * time.Second
	const stall = 3 * bodyTimeout
	const size, first = 16 << 10, 12 << 10 // the stall comes after the read-ahead, with the handler reading
	app := fiber.New(buildFiberConfig(&config.Config{}))
	closeConnectionsLeftMidBody(app, bodyTimeout)
	read := func(c fiber.Ctx) error {
		n, err := io.Copy(io.Discard, c.RequestCtx().RequestBodyStream())
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("cut after " + strconv.FormatInt(n, 10))
		}
		return c.SendString(strconv.FormatInt(n, 10))
	}
	app.Post(storageUploadPath, read)
	app.Put(storageUploadPath, read)
	app.Post("/api/v1/stalled-body-probe", read)
	addr := serveAppOnLoopback(t, app)

	const multipart = "multipart/form-data; boundary=x"
	for _, tt := range []struct {
		name, method, target, contentType string
		cut                               bool
	}{
		{"a multipart body to the storage upload", fiber.MethodPost, uploadTarget, multipart, false},
		{"a multipart body to the storage upload, spelled in capitals", fiber.MethodPost, uploadTarget,
			"Multipart/Form-Data; boundary=x", false},
		{"a multipart body to the storage upload, with an encoded slash in a parameter", fiber.MethodPost,
			strings.Replace(uploadTarget, testClusterID, "a%2Fb", 1), multipart, false},
		{"a JSON body to the storage upload", fiber.MethodPost, uploadTarget, fiber.MIMEApplicationJSON, true},
		{"an octet-stream body to the storage upload", fiber.MethodPost, uploadTarget, "application/octet-stream", true},
		{"the upload spelled in capitals", fiber.MethodPost, strings.ToUpper(uploadTarget), multipart, true},
		{"the upload with a trailing slash", fiber.MethodPost, uploadTarget + "/", multipart, true},
		{"another method on the upload's path", fiber.MethodPut, uploadTarget, multipart, true},
		{"another route", fiber.MethodPost, "/api/v1/stalled-body-probe", multipart, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sent := time.Now()
			conn, br := dialAndSend(t, addr, requestHead(tt.method, tt.target, "Content-Type: "+tt.contentType)+
				fmt.Sprintf("Content-Length: %d\r\n\r\n", size)+strings.Repeat("a", first))
			if tt.cut {
				ans := readAnswer(t, conn, br, tt.method)
				elapsed := time.Since(sent)
				if ans.status != fiber.StatusBadRequest || !strings.HasPrefix(string(ans.body), "cut after") {
					t.Fatalf("status %d (%s), want the handler's answer to a read cut short", ans.status, ans.body)
				}
				if elapsed < bodyTimeout || elapsed > bodyTimeout+slack {
					t.Errorf("answered %v after the request went out, want the read cut at the %v body deadline",
						elapsed, bodyTimeout)
				}
				if !ans.connClose {
					t.Error("the answer to a read cut short does not carry Connection: close")
				}
				return
			}
			time.Sleep(stall)
			writeRaw(t, conn, strings.Repeat("a", size-first))
			ans := readAnswer(t, conn, br, tt.method)
			if ans.status != fiber.StatusOK || string(ans.body) != strconv.Itoa(size) {
				t.Errorf("status %d, body %q; want 200 and the whole %d bytes read after a %v stall", ans.status,
					ans.body, size, stall)
			}
		})
	}
}

// TestStreamedUploadPredicateSeesThePathTheRouterSees holds what lets
// isStreamedUpload be one predicate for the wrapper and for the middleware:
// closeConnectionsLeftMidBody asks it of the request's method,
// URI().PathOriginal() and RequestHeader.ContentType before Fiber has a Ctx,
// and isStreamedUploadRequest of c.Method(), c.Path() and
// c.Get("Content-Type") — and on every request here those are the same
// strings, so the two answers are the same. Each request is parsed from its
// head as the server parses it, two Content-Type lines included. c.Path() is
// PathOriginal only while UnescapePath is off; the twin shows that on, it is
// not, so the check here would notice the day it is turned on.
//
// The content-type rows hold the predicate to what it is for, written out as
// the answers it has to give: a body UploadFile streams — its own test,
// handlers.ParseMultipartContentType, a multipart media type — and that
// bodyValues does not read first, as it does a +json type whatever its prefix.
func TestStreamedUploadPredicateSeesThePathTheRouterSees(t *testing.T) {
	s := newAssembledServer(t)
	if s.app.Config().UnescapePath {
		t.Fatal("UnescapePath is on: c.Path() is then decoded, and no longer the path closeConnectionsLeftMidBody reads")
	}
	// ask parses raw, asks the predicate both ways, fails the test where they
	// differ, and returns the answer.
	ask := func(t *testing.T, raw string) bool {
		t.Helper()
		fctx := &fasthttp.RequestCtx{}
		if err := fctx.Request.Header.Read(bufio.NewReader(strings.NewReader(raw))); err != nil {
			t.Fatalf("parsing %q: %v", raw, err)
		}
		c := s.app.AcquireCtx(fctx)
		defer s.app.ReleaseCtx(c)
		original, contentType := string(fctx.URI().PathOriginal()), string(fctx.Request.Header.ContentType())
		if c.Path() != original || c.Method() != string(fctx.Method()) || c.Get(fiber.HeaderContentType) != contentType {
			t.Errorf("%q: c.Path() %q, c.Method() %q, c.Get(Content-Type) %q; PathOriginal %q, method %q, "+
				"ContentType %q", raw, c.Path(), c.Method(), c.Get(fiber.HeaderContentType), original, fctx.Method(),
				contentType)
		}
		inChain := isStreamedUploadRequest(c)
		if beforeIt := isStreamedUpload(string(fctx.Method()), original, contentType); inChain != beforeIt {
			t.Errorf("%q: exempt %v in the chain and %v before it", raw, inChain, beforeIt)
		}
		return inChain
	}

	const multipart = "Content-Type: multipart/form-data; boundary=x"
	prefix := strings.TrimSuffix(uploadTarget, "/upload")
	targets := []string{
		uploadTarget,
		strings.ToUpper(uploadTarget),
		uploadTarget + "/",
		uploadTarget + "?x=1",
		"http://example.com" + uploadTarget,
		strings.Replace(uploadTarget, testClusterID, "a%2Fb", 1),
		prefix + "/uplo%61d",
		prefix + "/../" + testClusterID + "/upload",
		strings.Replace(uploadTarget, "/v1/", "/v1//", 1),
	}
	accepted := 0
	for _, method := range []string{fiber.MethodPost, fiber.MethodPut, "post"} {
		for _, target := range targets {
			if ask(t, requestHead(method, target, multipart)+"\r\n") {
				accepted++
			}
		}
	}
	if accepted != 4 {
		t.Errorf("%d method and spelling pairs exempt, want 4: POST to the upload as declared, with a query, in "+
			"absolute form and with an encoded slash in a parameter", accepted)
	}

	for _, tt := range []struct {
		lines  []string
		exempt bool
	}{
		{[]string{multipart}, true},
		{[]string{"Content-Type: Multipart/Form-Data; Boundary=x"}, true},
		{[]string{"Content-Type: MULTIPART/MIXED; boundary=x"}, true},
		{[]string{"Content-Type: multipart/form-data"}, true},
		{[]string{"Content-Type: application/json"}, false},
		{[]string{"Content-Type: text/plain; charset=utf-8"}, false},
		{[]string{"Content-Type: application/octet-stream"}, false},
		{nil, false},
		{[]string{"Content-Type: multipart"}, false},
		{[]string{"Content-Type: multipart/"}, false},
		{[]string{"Content-Type: multipart/form-data; boundary"}, false},
		{[]string{"Content-Type: application/json; boundary=multipart/form-data"}, false},
		{[]string{"Content-Type: application/json", multipart}, true},
		{[]string{multipart, "Content-Type: application/json"}, false},
		// Multipart to UploadFile and JSON to bodyValues, which reads it before
		// any grant is checked: never exempt.
		{[]string{"Content-Type: multipart/form-data+json; boundary=x"}, false},
		{[]string{"Content-Type: multipart/x+json"}, false},
		{[]string{"Content-Type: Multipart/Related+JSON"}, false},
		{[]string{"Content-Type: MULTIPART/FORM-DATA+JSON; boundary=x"}, false},
		{[]string{"Content-Type: multipart/form-data+json ; boundary=x"}, false},
	} {
		if got := ask(t, requestHead(fiber.MethodPost, uploadTarget, tt.lines...)+"\r\n"); got != tt.exempt {
			t.Errorf("POST %s with %q: exempt %v, want %v", uploadTarget, tt.lines, got, tt.exempt)
		}
	}

	unescaping := fiber.New(fiber.Config{UnescapePath: true})
	fctx := &fasthttp.RequestCtx{}
	fctx.Request.Header.SetMethod(fiber.MethodPost)
	fctx.Request.SetRequestURI(prefix + "/uplo%61d")
	c := unescaping.AcquireCtx(fctx)
	defer unescaping.ReleaseCtx(c)
	if c.Path() == string(fctx.URI().PathOriginal()) {
		t.Errorf("precondition: with UnescapePath on, c.Path() %q is still PathOriginal; the check above could not "+
			"tell the setting apart", c.Path())
	}
}

// TestStreamedUploadIsNeverABodyReadAsJSON holds the upload exemption apart
// from the one reader that runs before UploadFile checks a grant: over every
// Content-Type in a corpus built from media types, letter cases, parameters
// and spacing, none is both exempt from the body bounds and a type
// isJSONContentType accepts — bodyValues would read such a body, unbounded,
// before any grant is checked. And none is exempt that UploadFile would not
// stream (handlers.ParseMultipartContentType): the exemption is no looser than
// the body it is for. Both counts are held above zero, so the corpus has to
// reach both sides.
func TestStreamedUploadIsNeverABodyReadAsJSON(t *testing.T) {
	mediaTypes := []string{
		"application/json", "application/merge-patch+json", "application/vnd.api+json", "text/json", "application/+json",
		"multipart/form-data", "multipart/mixed", "multipart/related", "multipart/x-custom", "multipart/json",
		"multipart/form-data+json", "multipart/x+json", "multipart/related+json", "multipart/+json",
		"multipart/form-data+jsonx", "multipart/form-data+xml", "multipart", "multipart/", "+json", "json",
		"text/plain", "application/octet-stream", "application/x-www-form-urlencoded", "",
	}
	cases := []func(string) string{
		func(m string) string { return m },
		strings.ToUpper,
		func(m string) string { // every other letter in capitals
			b := []byte(m)
			for i := range b {
				if i%2 == 0 && b[i] >= 'a' && b[i] <= 'z' {
					b[i] -= 'a' - 'A'
				}
			}
			return string(b)
		},
	}
	params := []string{"", "; boundary=x", ";boundary=x", " ; boundary=x", "; charset=utf-8", `; boundary="a;b"`,
		"; boundary", "; boundary=x; charset=utf-8", ";", "; x=+json"}
	exempt, json, tried := 0, 0, 0
	for _, media := range mediaTypes {
		for _, casing := range cases {
			for _, param := range params {
				for _, space := range []string{"", " "} {
					contentType := space + casing(media) + param
					tried++
					isExempt := isStreamedUpload(fiber.MethodPost, uploadTarget, contentType)
					isJSON := isJSONContentType(contentType)
					if isExempt {
						exempt++
					}
					if isJSON {
						json++
					}
					if isExempt && isJSON {
						t.Errorf("%q is exempt from the body bounds, and bodyValues reads it as JSON before any grant is "+
							"checked", contentType)
					}
					if _, multipart := handlers.ParseMultipartContentType(contentType); isExempt && !multipart {
						t.Errorf("%q is exempt from the body bounds, and UploadFile would not stream it", contentType)
					}
				}
			}
		}
	}
	if exempt == 0 || json == 0 {
		t.Fatalf("precondition: of %d Content-Types, %d exempt and %d JSON; the corpus has to reach both", tried, exempt,
			json)
	}
	t.Logf("%d Content-Types: %d exempt, %d read as JSON", tried, exempt, json)
}

// deadlineRecordingListener hands out connections that record every read
// deadline closeConnectionsLeftMidBody sets on them, and only those: the
// caller of SetReadDeadline decides.
type deadlineRecordingListener struct {
	net.Listener
	mu  sync.Mutex
	set []time.Time
}

func (l *deadlineRecordingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &deadlineRecordingConn{Conn: conn, l: l}, nil
}

// take returns the deadlines recorded so far and forgets them.
func (l *deadlineRecordingListener) take() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	set := l.set
	l.set = nil
	return set
}

type deadlineRecordingConn struct {
	net.Conn
	l *deadlineRecordingListener
}

func (c *deadlineRecordingConn) SetReadDeadline(deadline time.Time) error {
	if pc, _, _, ok := runtime.Caller(1); ok &&
		strings.Contains(runtime.FuncForPC(pc).Name(), "/internal/api.closeConnectionsLeftMidBody.") {
		c.l.mu.Lock()
		c.l.set = append(c.l.set, deadline)
		c.l.mu.Unlock()
	}
	return c.Conn.SetReadDeadline(deadline)
}
