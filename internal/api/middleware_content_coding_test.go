package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/ws"
)

// These tests hold refuseContentCodedRequests (middleware.go) to what it is
// for: no handler reads the body of a request that names a content coding, so
// nothing decodes it, on any route. They drive the server as cmd/nexara
// assembles it, because the claim is about the whole stack — a gate that
// refused correctly but sat behind a route, or behind a middleware that read
// the body first, would pass any test of the gate alone.

// recordingReader is a request body stream that records whether anything
// read from it, so a test can tell a refusal made before the body was
// touched from one made after it was drained.
type recordingReader struct {
	r    io.Reader
	read bool
}

func (r *recordingReader) Read(p []byte) (int, error) {
	r.read = true
	return r.r.Read(p)
}

// contentCodingPayload is the plaintext most encoded fixtures carry: a login
// body whose e-mail is 64 KiB of one byte, so it compresses to a few dozen
// bytes and decodes back to all of it — the amplification being refused.
var contentCodingPayload = []byte(`{"email":"` + strings.Repeat("a", 64<<10) + `"}`)

// newAssembledServer builds the server the way cmd/nexara/main.go does:
// New over a composition root — newSweepServer's, whose database and Redis
// are unreachable and are never reached here — then the WebSocket routes,
// then the embedded SPA. What sits behind an app-level middleware is decided
// by registration order, and the last two steps happen outside New, so a
// server built by New alone would leave them out of the question. One
// difference from production: newSweepServer's config leaves compression
// off, and compress only post-processes responses.
func newAssembledServer(t *testing.T) *Server {
	t.Helper()
	s := newSweepServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ws.NewServer(ws.NewHub(logger, 1), s.jwtService, logger, time.Minute, time.Minute, ws.ServerConfig{
		ConsoleHandler: ws.NewConsoleHandler(s.queries, sweepEncryptionKey, s.jwtService, logger),
		VNCHandler:     ws.NewVNCHandler(s.queries, sweepEncryptionKey, s.jwtService, logger),
	}).RegisterRoutes(s.App())
	s.RegisterFrontend(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Nexara</title>")}})
	return s
}

// wireRequest is one request as a client puts it on the wire.
type wireRequest struct {
	method, target string
	contentType    string   // "" sends application/json
	lines          []string // further header lines, written as they go on the wire
	exactNames     bool     // header normalising off: every name keeps the case it was sent in
	chunked        bool     // framed with Transfer-Encoding: chunked, which fasthttp reports as Content-Length -1
	body           []byte
}

// build hands the request over the way the server receives it. The head goes
// through fasthttp's own header parser, so a lower-case name, a repeated line
// or an empty value arrives exactly as it would off the wire, and the body is
// a stream that records reads, as it is under StreamRequestBody.
func (w wireRequest) build(t *testing.T) (*fasthttp.RequestCtx, *recordingReader) {
	t.Helper()
	contentType := w.contentType
	if contentType == "" {
		contentType = fiber.MIMEApplicationJSON
	}
	var head strings.Builder
	fmt.Fprintf(&head, "%s %s HTTP/1.1\r\nHost: example.com\r\nContent-Type: %s\r\n", w.method, w.target, contentType)
	for _, line := range w.lines {
		head.WriteString(line + "\r\n")
	}
	size := len(w.body)
	if w.chunked {
		head.WriteString("Transfer-Encoding: chunked\r\n")
		size = -1
	} else {
		fmt.Fprintf(&head, "Content-Length: %d\r\n", size)
	}
	head.WriteString("\r\n")

	// Init gives the context the connection and server a real one has. A bare
	// RequestCtx has neither: fasthttp's file server, behind the SPA handler,
	// logs a missing path through ctx.Logger(), which then dereferences the
	// nil server, and recover turns the panic into a 500 no real request sees.
	fctx := &fasthttp.RequestCtx{}
	fctx.Init(&fasthttp.Request{}, nil, nil)
	if w.exactNames {
		fctx.Request.Header.DisableNormalizing()
	}
	if err := fctx.Request.Header.Read(bufio.NewReader(strings.NewReader(head.String()))); err != nil {
		t.Fatalf("parsing the request head %q: %v", head.String(), err)
	}
	stream := &recordingReader{r: bytes.NewReader(w.body)}
	fctx.Request.SetBodyStream(stream, size)
	return fctx, stream
}

// servedResponse is what one request through the whole app answered.
type servedResponse struct {
	status         int
	acceptEncoding string
	requestID      string
	nosniff        string
	envelope       ErrorResponse
	body           []byte
}

// serveThroughApp runs fctx through every handler the app has, exactly as
// fasthttp's server would hand it over.
func serveThroughApp(s *Server, fctx *fasthttp.RequestCtx) servedResponse {
	s.app.Handler()(fctx)
	res := servedResponse{
		status:         fctx.Response.StatusCode(),
		acceptEncoding: string(fctx.Response.Header.Peek(fiber.HeaderAcceptEncoding)),
		requestID:      string(fctx.Response.Header.Peek(fiber.HeaderXRequestID)),
		nosniff:        string(fctx.Response.Header.Peek(fiber.HeaderXContentTypeOptions)),
		body:           append([]byte(nil), fctx.Response.Body()...),
	}
	_ = json.Unmarshal(res.body, &res.envelope)
	return res
}

// refusedByTheGate reports what, if anything, is wrong with res as the
// gate's refusal: the status, the envelope's slug and message, and the
// Accept-Encoding RFC 9110 §12.5.3 asks for — and the request id and security
// headers, which the refusal carries only because the gate sits after
// requestid and the security-header middleware, as its comment says it does.
func refusedByTheGate(res servedResponse) []string {
	var wrong []string
	if res.status != fiber.StatusUnsupportedMediaType {
		wrong = append(wrong, fmt.Sprintf("status = %d, want 415 (body %.200s)", res.status, res.body))
	}
	if res.acceptEncoding != "identity" {
		wrong = append(wrong, fmt.Sprintf("Accept-Encoding = %q, want \"identity\"", res.acceptEncoding))
	}
	if res.envelope.Error != "unsupported_media_type" || res.envelope.Message != contentCodingRefusal {
		wrong = append(wrong, fmt.Sprintf("envelope = %+v, want error \"unsupported_media_type\" and the gate's message", res.envelope))
	}
	if res.requestID == "" || res.nosniff != "nosniff" {
		wrong = append(wrong, fmt.Sprintf("X-Request-Id = %q, X-Content-Type-Options = %q: the refusal did not pass "+
			"through requestid and the security headers", res.requestID, res.nosniff))
	}
	return wrong
}

// TestContentCodedRequestIsRefusedBeforeItsBodyIsRead drives the assembled
// server — every app-level middleware (compression aside, see
// newAssembledServer), then the real login route — with a body stream that
// records reads, for each way a request can put a content coding where a
// decoder finds it.
//
// "Refused" and "refused unread" are different claims, and only the second
// denies the buffer: a gate that drained the stream first, or a middleware
// that read it — before the gate, or after it, as the logger renders its line
// once the chain returns — would answer the same 415 with the damage done.
//
// Every refused case but one is first shown to be dangerous. The same request,
// handed straight to c.Body() with nothing in front of it, has to come back as
// the full plaintext from at most a hundredth of its size on the wire — so
// each is a real amplification today, and one that stopped being one fails at
// the precondition instead of passing as a refusal nobody needed. The
// exception is a coding nothing here decodes, which pins that the gate refuses
// every coding but identity rather than a list of known ones. The accepted
// cases are the other half: without them a stack that never read any stream
// would make "unread" vacuous, and a check that refused too much — identity,
// or an empty list element, which RFC 9110 §5.6.1.2 says a recipient MUST
// accept — would pass.
//
// POST /api/v1/auth/login is the route because it is the one the attack
// was measured on: it needs no session, and it declares a JSON body, so
// extraction reads it with c.Body().
func TestContentCodedRequestIsRefusedBeforeItsBodyIsRead(t *testing.T) {
	s := newAssembledServer(t)
	const target = "/api/v1/auth/login"

	plain := contentCodingPayload
	gz := fasthttp.AppendGzipBytes(nil, plain)
	br := fasthttp.AppendBrotliBytes(nil, plain)
	zst := fasthttp.AppendZstdBytes(nil, plain)
	deflate := fasthttp.AppendDeflateBytes(nil, plain)

	// A multipart body reaches a second decoder: c.FormValue, c.FormFile and
	// a form Bind() go through fasthttp's MultipartFormWithLimit, which
	// gunzips on its own — and so does the logger's ${form:…} tag.
	const boundary = "nexara-boundary"
	form := []byte("--" + boundary + "\r\nContent-Disposition: form-data; name=\"email\"\r\n\r\n" +
		strings.Repeat("a", 64<<10) + "\r\n--" + boundary + "--\r\n")
	formType := "multipart/form-data; boundary=" + boundary

	for _, tt := range []struct {
		name        string
		contentType string
		lines       []string
		exactNames  bool
		chunked     bool
		body        []byte
		plain       []byte // what c.Body() must decode body to; nil where nothing does
		refuse      bool
	}{
		{name: "gzip", lines: []string{"Content-Encoding: gzip"}, body: gz, plain: plain, refuse: true},
		{name: "br", lines: []string{"Content-Encoding: br"}, body: br, plain: plain, refuse: true},
		{name: "zstd", lines: []string{"Content-Encoding: zstd"}, body: zst, plain: plain, refuse: true},
		{name: "deflate", lines: []string{"Content-Encoding: deflate"}, body: deflate, plain: plain, refuse: true},
		// Fiber decodes these two spellings as well (tryDecodeBodyInOrder).
		{name: "x-gzip", lines: []string{"Content-Encoding: x-gzip"}, body: gz, plain: plain, refuse: true},
		{name: "brotli", lines: []string{"Content-Encoding: brotli"}, body: br, plain: plain, refuse: true},
		{name: "a coding in capitals", lines: []string{"Content-Encoding: GZIP"}, body: gz, plain: plain, refuse: true},
		{name: "identity then gzip, in one list", lines: []string{"Content-Encoding: identity, gzip"}, body: gz, plain: plain, refuse: true},
		// Repeated field lines are one list (RFC 9110 §5.2), and c.Body()
		// decodes the whole list, while fasthttp's ContentEncoding() returns
		// only the first line.
		{name: "identity then gzip, on two field lines",
			lines: []string{"Content-Encoding: identity", "Content-Encoding: gzip"}, body: gz, plain: plain, refuse: true},
		{name: "an empty field line then gzip",
			lines: []string{"Content-Encoding:", "Content-Encoding: gzip"}, body: gz, plain: plain, refuse: true},
		// Parsed with normalising on, as the server runs: the name reaches
		// the app as "Content-Encoding".
		{name: "a lower-case field name", lines: []string{"content-encoding: gzip"}, body: gz, plain: plain, refuse: true},
		// With normalising off, the two lines keep their own spellings.
		// c.Body() matches the name case-insensitively and joins them;
		// Header.PeekAll matches it exactly and would see only the first.
		{name: "identity then gzip under two spellings of the name, normalising off",
			lines: []string{"Content-Encoding: identity", "content-encoding: gzip"}, exactNames: true, body: gz, plain: plain, refuse: true},
		// A chunked body has no Content-Length (fasthttp reports -1), so a
		// gate keyed on "does it declare a body" could miss it.
		{name: "a chunked body", lines: []string{"Content-Encoding: gzip"}, chunked: true, body: gz, plain: plain, refuse: true},
		{name: "a gzipped multipart form", contentType: formType,
			lines: []string{"Content-Encoding: gzip"}, body: fasthttp.AppendGzipBytes(nil, form), plain: form, refuse: true},
		// compress is a registered coding (RFC 9110 §8.4.1.1) that Fiber
		// answers with 501 rather than decoding. Refusing it is not about
		// danger today: it is what keeps a coding some later Fiber learns to
		// decode refused already, where a list of known codings would not.
		{name: "a coding nothing here decodes", lines: []string{"Content-Encoding: compress"}, body: gz, refuse: true},

		{name: "no Content-Encoding at all", body: plain},
		{name: "identity", lines: []string{"Content-Encoding: identity"}, body: plain},
		{name: "identity in capitals", lines: []string{"Content-Encoding: IDENTITY"}, body: plain},
		{name: "a lone empty field line", lines: []string{"Content-Encoding:"}, body: plain},
		{name: "identity with empty elements and spaces", lines: []string{"Content-Encoding: identity, , identity"}, body: plain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := wireRequest{
				method: fiber.MethodPost, target: target, contentType: tt.contentType,
				lines: tt.lines, exactNames: tt.exactNames, chunked: tt.chunked, body: tt.body,
			}
			if tt.plain != nil {
				twin, _ := request.build(t)
				tctx := s.app.AcquireCtx(twin)
				decoded := bytes.Equal(tctx.Body(), tt.plain)
				s.app.ReleaseCtx(tctx)
				if !decoded {
					t.Fatal("precondition: c.Body() did not decode this request to its plaintext — " +
						"a case c.Body() does not decode cannot show the gate refusing anything real")
				}
				if len(tt.body)*100 > len(tt.plain) {
					t.Fatalf("precondition: %d bytes on the wire for a %d-byte plaintext is not an amplification",
						len(tt.body), len(tt.plain))
				}
			}
			if tt.contentType == formType {
				twin, _ := request.build(t)
				tctx := s.app.AcquireCtx(twin)
				field := len(tctx.FormValue("email"))
				s.app.ReleaseCtx(tctx)
				if field != 64<<10 {
					t.Fatalf("precondition: the multipart reader gave a %d-byte field, want the %d it gunzips to",
						field, 64<<10)
				}
			}

			fctx, stream := request.build(t)
			res := serveThroughApp(s, fctx)

			if !tt.refuse {
				// The route's own answer to the plain payload: extraction read
				// the body and the schema judged it. Measured, not guessed.
				if res.status != fiber.StatusBadRequest || res.envelope.Message != "email: must have at most 320 characters" {
					t.Fatalf("status %d %+v, want the login route's own 400 on the e-mail's length", res.status, res.envelope)
				}
				if res.acceptEncoding != "" {
					t.Errorf("Accept-Encoding = %q on a request the gate let through; only its refusal sets it",
						res.acceptEncoding)
				}
				if !stream.read {
					t.Fatal("the accepted body was never read, so no case here can show a refusal came before the read")
				}
				return
			}
			for _, w := range refusedByTheGate(res) {
				t.Error(w)
			}
			if stream.read {
				t.Error("the body stream was read; a content-coded body must be refused without anything reading it")
			}
		})
	}
}

// TestContentCodedRequestIsRefusedOnEveryKindOfRoute sends an encoded body
// over the wire — app.Test serialises the request and fasthttp's server
// parses it — to one route of each kind that reads a body: a legacy route in
// router.go that binds its own (register, which needs no session), a
// registry route whose extraction reads a declared JSON body (login), and the
// upload route, which reads its multipart stream itself and falls back to
// c.Body() when the body was not streamed.
//
// Each route is first shown to be there and to answer for itself: its path is
// in the route table, and the same request without the coding gets that
// route's own answer. Register's handler refuses the missing password; login's
// declared schema refuses the e-mail's length in Endpoint.serve, before its
// handler runs; the upload route's authRequired answers 401 — a Deferred
// declaration is still authenticated, so mountRegistry attaches authRequired
// to the route itself, and without a session neither its handler nor its body
// is reached. So the
// 415 that follows is the gate standing in front of a route that would
// otherwise have answered the request, not a request that went nowhere.
func TestContentCodedRequestIsRefusedOnEveryKindOfRoute(t *testing.T) {
	s := newAssembledServer(t)

	mounted := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		mounted[r.Method+" "+r.Path] = true
	}

	const boundary = "nexara-boundary"
	form := []byte("--" + boundary + "\r\nContent-Disposition: form-data; name=\"content\"\r\n\r\niso\r\n--" + boundary + "--\r\n")

	for _, tt := range []struct {
		name        string
		route       string // as mounted
		target      string
		contentType string
		body        []byte
		// What the route answers when the same body arrives without a
		// coding.
		plainStatus  int
		plainMessage string
	}{
		{
			name: "legacy route in router.go", route: "POST /api/v1/auth/register",
			target: "/api/v1/auth/register", contentType: fiber.MIMEApplicationJSON, body: contentCodingPayload,
			plainStatus: fiber.StatusBadRequest, plainMessage: "Email and password are required",
		},
		{
			name: "registry route with a declared JSON body", route: "POST /api/v1/auth/login",
			target: "/api/v1/auth/login", contentType: fiber.MIMEApplicationJSON, body: contentCodingPayload,
			plainStatus: fiber.StatusBadRequest, plainMessage: "email: must have at most 320 characters",
		},
		{
			// The route's own authRequired, not the /api/v1/clusters group's:
			// that group's is mounted after the registry and never reached.
			name: "upload route", route: "POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload",
			target:      "/api/v1/clusters/" + testClusterID + "/storage/" + testClusterID + "/upload",
			contentType: "multipart/form-data; boundary=" + boundary, body: form,
			plainStatus: fiber.StatusUnauthorized, plainMessage: "Missing authorization token",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !mounted[tt.route] {
				t.Fatalf("precondition: %s is not in the route table", tt.route)
			}

			request := func(body []byte, coding string) *http.Response {
				req := httptest.NewRequest(fiber.MethodPost, tt.target, bytes.NewReader(body))
				req.Header.Set(fiber.HeaderContentType, tt.contentType)
				if coding != "" {
					req.Header.Set(fiber.HeaderContentEncoding, coding)
				}
				res, err := s.App().Test(req)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				t.Cleanup(func() { _ = res.Body.Close() })
				return res
			}
			envelope := func(res *http.Response) ErrorResponse {
				var env ErrorResponse
				raw, _ := io.ReadAll(res.Body)
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatalf("response body %.200s is not an error envelope: %v", raw, err)
				}
				return env
			}

			plain := request(tt.body, "")
			if env := envelope(plain); plain.StatusCode != tt.plainStatus || env.Message != tt.plainMessage {
				t.Fatalf("precondition: without a coding the route answered %d %q, want its own %d %q",
					plain.StatusCode, env.Message, tt.plainStatus, tt.plainMessage)
			}

			coded := request(fasthttp.AppendGzipBytes(nil, tt.body), "gzip")
			env := envelope(coded)
			if coded.StatusCode != fiber.StatusUnsupportedMediaType {
				t.Fatalf("status = %d (%+v), want 415", coded.StatusCode, env)
			}
			if env.Error != "unsupported_media_type" || env.Message != contentCodingRefusal {
				t.Errorf("envelope = %+v, want error \"unsupported_media_type\" and the gate's message", env)
			}
			if ae := coded.Header.Get(fiber.HeaderAcceptEncoding); ae != "identity" {
				t.Errorf("Accept-Encoding = %q, want \"identity\"", ae)
			}
		})
	}
}

// fiberRouteParamRe matches a parameter or wildcard segment in a Fiber route
// path.
var fiberRouteParamRe = regexp.MustCompile(`:[A-Za-z0-9_]+\??|\*|\+`)

// TestContentCodingGatePrecedesEveryRoute is the structural half of the
// coverage claim: every route the assembled server mounts — the registry's,
// the legacy ones in router.go, the WebSocket upgrades — every prefix an
// app-level Use mounts a handler on, and the SPA handler answer an encoded
// request with the gate's 415, its body unread. A route or path-scoped
// handler registered ahead of refuseContentCodedRequests would answer first
// and fail here, whichever it is, including one no other test names.
//
// It cannot tell a route from a path that routes nowhere, since the gate
// answers both. That is why it also requires the route table to hold the
// kinds of route it claims to cover, and the SPA handler — which sits on the
// root prefix, where the gate would answer anyway — to answer a deep link
// itself when no coding is named.
func TestContentCodingGatePrecedesEveryRoute(t *testing.T) {
	s := newAssembledServer(t)
	gz := fasthttp.AppendGzipBytes(nil, contentCodingPayload)

	type probe struct{ method, path string }
	routes := s.app.GetRoutes(true)
	probes := make([]probe, 0, len(routes)+16)
	covered := map[string]bool{}
	for _, r := range routes {
		covered[r.Method+" "+r.Path] = true
		probes = append(probes, probe{r.Method, fiberRouteParamRe.ReplaceAllString(r.Path, "x")})
	}
	for _, want := range []string{
		"POST /api/v1/auth/register",
		"PUT /api/v1/settings/:key",
		"POST /api/v1/auth/login",
		"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload",
		"GET /ws",
		"GET /ws/console",
		"GET /ws/vnc",
	} {
		if !covered[want] {
			t.Errorf("precondition: %s is not in the route table, so the sweep does not cover it", want)
		}
	}
	if len(probes) < 500 {
		t.Fatalf("precondition: the route table holds %d routes; the assembled server mounts far more", len(probes))
	}

	// GetRoutes(true) leaves out what Use mounted, which GetRoutes(false)
	// lists under every method. A Use handler matches any method, so each
	// prefix is probed once, with a POST.
	usePrefixes := map[string]bool{}
	for _, r := range s.app.GetRoutes(false) {
		if !covered[r.Method+" "+r.Path] {
			usePrefixes[r.Path] = true
		}
	}
	if !usePrefixes["/api/v1/settings"] {
		t.Fatal("precondition: the legacy /api/v1/settings group's Use prefix is missing, so Use prefixes are not being found")
	}
	for prefix := range usePrefixes {
		probes = append(probes, probe{fiber.MethodPost, fiberRouteParamRe.ReplaceAllString(prefix, "x")})
	}

	const deepLink = "/clusters/x/vms"
	plain, _ := wireRequest{method: fiber.MethodGet, target: deepLink}.build(t)
	if res := serveThroughApp(s, plain); res.status != fiber.StatusOK || !bytes.Contains(res.body, []byte("<!doctype html>")) {
		t.Fatalf("precondition: GET %s without a coding answered %d, want the SPA shell", deepLink, res.status)
	}
	probes = append(probes, probe{fiber.MethodGet, deepLink}, probe{fiber.MethodGet, "/"})

	var failures []string
	for _, p := range probes {
		fctx, stream := wireRequest{
			method: p.method, target: p.path, lines: []string{"Content-Encoding: gzip"}, body: gz,
		}.build(t)
		wrong := refusedByTheGate(serveThroughApp(s, fctx))
		if stream.read {
			wrong = append(wrong, "the body stream was read")
		}
		if len(wrong) > 0 {
			failures = append(failures, fmt.Sprintf("%s %s: %s", p.method, p.path, strings.Join(wrong, "; ")))
		}
	}
	slices.Sort(failures)
	for _, f := range failures {
		t.Error(f)
	}
}

// serveOnLoopback starts s on a real TCP listener and returns its address.
// A connection made before the server starts accepting waits in the
// listen backlog, so nothing here has to poll for readiness.
func serveOnLoopback(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = s.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = s.app.Shutdown() })
	return ln.Addr().String()
}

// TestContentCodingRefusalClosesTheConnection holds the refusal to closing
// the connection it answers on, over a real TCP connection.
//
// The gate leaves the body unread, and fasthttp does not drain what a
// handler leaves: it copies the first 8 KiB of a body with a Content-Length
// into the request (readBodyWithStreaming) and, on a kept-alive connection,
// parses what follows as the next request. So a request placed at that
// offset inside a refused body would be served as if the client had sent it.
// The fixture puts one there — GET /api/v1/version, padded so the body runs
// to about 16 KiB.
//
// The precondition sends the same body, without a coding, to a probe route
// that answers without reading it: the hidden request then IS answered,
// which is what shows the fixture reaches fasthttp's parser. It leans on an
// early answer leaving the connection open, as every one but this refusal
// does today; if that changes for all of them, the precondition is the part
// to revisit, not the assertion.
func TestContentCodingRefusalClosesTheConnection(t *testing.T) {
	s := newAssembledServer(t)
	s.app.Post("/api/v1/unread-body-probe", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
	addr := serveOnLoopback(t, s)

	hidden := "GET /api/v1/version HTTP/1.1\r\nHost: example.com\r\nX-Pad: " + strings.Repeat("p", 8<<10) + "\r\n\r\n"
	body := strings.Repeat("x", 8<<10) + hidden

	// exchange sends one request with body and reads two responses off the
	// connection: the request's own, then whatever follows it.
	exchange := func(target string, lines ...string) (*http.Response, *http.Response, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		head := "POST " + target + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n"
		for _, line := range lines {
			head += line + "\r\n"
		}
		head += fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
		if _, err := conn.Write([]byte(head + body)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		r := bufio.NewReader(conn)
		first, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatalf("reading the response to POST %s: %v", target, err)
		}
		_, _ = io.Copy(io.Discard, first.Body)
		_ = first.Body.Close()
		second, err := http.ReadResponse(r, nil)
		if second != nil {
			_, _ = io.Copy(io.Discard, second.Body)
			_ = second.Body.Close()
		}
		return first, second, err
	}

	probe, answered, err := exchange("/api/v1/unread-body-probe")
	if probe.StatusCode != fiber.StatusNoContent || err != nil || answered.StatusCode != fiber.StatusOK {
		t.Fatalf("precondition: the probe answered %d, then %v / %v — the request hidden in its unread body "+
			"should have been answered 200, or this fixture cannot show a refusal preventing it",
			probe.StatusCode, answered, err)
	}

	refused, second, err := exchange("/api/v1/auth/login", "Content-Encoding: gzip")
	if refused.StatusCode != fiber.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want the gate's 415", refused.StatusCode)
	}
	if !refused.Close {
		t.Error("the 415 does not carry Connection: close")
	}
	var netErr net.Error
	switch {
	case err == nil:
		t.Errorf("the request hidden in the refused body was answered, with %d", second.StatusCode)
	case errors.As(err, &netErr) && netErr.Timeout():
		t.Error("the connection stayed open after the 415 — nothing hidden came back yet, but nothing stops it")
	}
}

// accessLogChildEnv marks the child run of
// TestContentCodingRefusalIsAccessLogged.
const accessLogChildEnv = "NEXARA_TEST_ACCESS_LOG_CHILD"

// TestContentCodingRefusalIsAccessLogged pins the gate inside the access
// logger, which middleware.go gives as a reason for where it sits: a refused
// request is logged, with its request id, like every other rejection. Moved
// ahead of the logger, the gate would still refuse, and only the log would
// show the difference.
//
// The logger writes to the standard output its package captured when it
// initialised (logger.ConfigDefault.Stream), so pointing os.Stdout elsewhere
// does not reach it. Swapping ConfigDefault.Stream would, but that is package
// state every server in the process reads, and it would change the output as
// well: the logger strips its colour codes only when it writes to a stdout
// that is not a terminal. So the test runs its own binary again as a child,
// sends a plain request and a refused one through the assembled server there,
// and reads the child's output — the production logger, unmodified. The plain request is the positive control: a
// missing line for the refusal means something only if the plain request's
// line is there.
func TestContentCodingRefusalIsAccessLogged(t *testing.T) {
	const plainID, refusedID = "access-log-plain-0001", "access-log-refused-0001"
	if os.Getenv(accessLogChildEnv) == "1" {
		s := newAssembledServer(t)
		plain, _ := wireRequest{method: fiber.MethodGet, target: "/api/v1/version",
			lines: []string{"X-Request-ID: " + plainID}}.build(t)
		serveThroughApp(s, plain)
		refused, _ := wireRequest{method: fiber.MethodPost, target: "/api/v1/auth/login",
			lines: []string{"X-Request-ID: " + refusedID, "Content-Encoding: gzip"},
			body:  fasthttp.AppendGzipBytes(nil, contentCodingPayload)}.build(t)
		serveThroughApp(s, refused)
		return
	}

	child := exec.Command(os.Args[0], "-test.run=^TestContentCodingRefusalIsAccessLogged$", "-test.count=1")
	child.Env = append(os.Environ(), accessLogChildEnv+"=1")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("the child run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.Bytes(), stderr.Bytes())
	}
	lineFor := func(id string) string {
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(line, "| "+id+" |") {
				return line
			}
		}
		return ""
	}

	if line := lineFor(plainID); !strings.Contains(line, "| 200 |") {
		t.Fatalf("precondition: no access-log line with status 200 for the plain request (found %q), so the "+
			"child's output is not showing the access log at all\n%s", line, stdout.Bytes())
	}
	line := lineFor(refusedID)
	if line == "" {
		t.Fatalf("the refused request left no line in the access log: the gate answers from outside the logger\n%s",
			stdout.Bytes())
	}
	if !strings.Contains(line, "| 415 |") || !strings.HasSuffix(strings.TrimSpace(line), "| POST /api/v1/auth/login") {
		t.Errorf("access-log line %q, want the 415 for POST /api/v1/auth/login", line)
	}
}
