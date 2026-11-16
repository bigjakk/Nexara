package api

import (
	"bytes"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The fasthttp and Fiber internals this file names are those of the versions
// go.mod pins, read from their source: fasthttp v1.79.0 (http.go,
// streaming.go, server.go, header.go, headerscanner.go) and Fiber v3.7.0
// (router.go, router_skip.go, ctx.go, req.go, path.go, app.go, internal/bodystream). The tests
// in body_framing_test.go exercise that behaviour over real connections;
// SkipUnmatchedRoutes, which is off, is the one thing named here that none of
// them turns on.

// closeConnectionsLeftMidBody makes every answer to a request whose body was
// not read to its end close the connection it goes out on, so that what is
// left of that body is never read as the next request. It also bounds how long
// a handler's reads of a body may take: bodyTimeout from when the handler
// starts, on every request but a streamed multipart upload — a multipart body,
// of no type the server reads as JSON, sent to the storage upload
// (isStreamedUpload).
//
// Under StreamRequestBody (buildFiberConfig) fasthttp reads at most the first
// 8 KiB of a body with a Content-Length before any handler runs, and none of a
// chunked one (Request.ContinueReadBodyStream, readBodyWithStreaming). It hands
// the handler the rest as a stream over the connection's own reader. Up to
// v1.74 nothing drained that stream when the handler returned, and on a
// kept-alive connection whatever followed was parsed as the next request. Since
// v1.75 Server.serveConn drains it itself: it discards up to
// maxUnreadStreamBodySize (256 KiB) of what the handler left unread and keeps
// the connection, and closes it when more is left or a read of the stream
// failed (requestStream.failed). So when a request is answered without its
// body being read — a 401, 404, 405, 413, 415 or 429, the 411 below, a CORS
// preflight, the SPA shell — this close no longer stands alone between the
// rest of the body and the next request; it saves reading up to 256 KiB a
// client chose not to have read, and it is still the only close where the head
// frames the body ambiguously (below), because there fasthttp's own framing is
// the one a proxy may not share.
//
// It closes the connection after a request whose head frames its body
// ambiguously as well (ambiguousFramingRefusal). refuseAmbiguousFraming
// refuses one in Fiber's chain, but to fasthttp such a body may be empty, so
// the unread-body rule alone would keep the connection — and for an unknown
// method the chain never runs at all.
//
// It wraps the handler the fasthttp server calls instead of sitting in Fiber's
// middleware chain, because Fiber answers some requests without running that
// chain: a method outside Config.RequestMethods gets its 501 straight from
// App.defaultRequestHandler, and with Config.SkipUnmatchedRoutes on (it is off
// here) App.emitSkip answers an unrouted request's 404 or 405 the same way. A
// middleware registered first with Use would miss both; this sees every
// request the server hands the app, after the app is done with it.
//
// Three things happen before the handler runs, on purpose:
//
//   - The body's framing is read (RequestHeader.ContentLength). The question
//     afterwards is how fasthttp framed the body whose head it parsed; a
//     handler that rewrote the header would otherwise change the answer.
//   - So is whether the head is ambiguous, for the same reason.
//   - The connection's read deadline is replaced with the body deadline:
//     bodyTimeout from now, or none at all for a streamed multipart upload.
//     buildFiberConfig's ReadTimeout (headReadTimeout), which serveConn arms
//     when a request's first byte arrives, bounds the head and fasthttp's
//     read-ahead of the body; left armed, it would bound the handler's reads
//     of the rest of the body as well, at a minute from that first byte —
//     cutting any upload that takes longer. Cleared instead, it would leave
//     those reads unbounded on every route: a client could declare a body,
//     send its first 8 KiB and stop, and hold the handler reading it — on
//     logout or the OIDC token exchange, which no session or rate limiter
//     stands in front of — for as long as it liked. The body deadline bounds
//     them at bodyTimeout. A streamed multipart upload keeps them unbounded:
//     an ISO streams through UploadFile for as long as it takes, and nothing
//     reads a byte of it before UploadFile has checked the caller's grants.
//     Every other body sent to the upload gets the deadline as a body sent
//     anywhere else does — among them every body the endpoint's declaration
//     reads as JSON (bodyValues) before a grant is checked, a +json type such
//     as multipart/form-data+json included (isStreamedMultipart). serveConn
//     arms IdleTimeout again before it waits for the next request, and clears
//     every deadline itself before it hands a hijacked connection — a
//     WebSocket — to its handler (the c.SetDeadline(zeroTime) ahead of
//     hijackConnHandler).
//
// After the handler, the connection is closed when the head was ambiguous,
// when part of the body may still be on the connection (bodyMayBeLeftUnread),
// and whenever the body deadline has passed. A read the deadline cuts fails,
// and Request.bodyBytes — what c.Body() reads a Content-Length body within
// BodyLimit through — closes the stream on that failure exactly as it does at
// the end of the body, with the rest of the body still on the connection; once
// the stream is closed nothing tells the two apart. So an answer given after the deadline closes the connection whether
// or not a read was cut: a handler that read its whole body and then took
// longer than bodyTimeout to answer loses its connection too, the safe way to
// be wrong.
//
// A read the deadline cuts changes what the handler sees as well: on the
// failure Request.bodyBytes puts the read error's text where the body was, so
// c.Body() — and c.BodyRaw(), and c.Bind(), which read through it — hand the
// handler "read tcp <local address>-><peer address>: i/o timeout", the
// server's own address and port among it. Every handler today parses a body
// as JSON or multipart, which that text is not, and answers as it does a
// malformed body. None may store a raw body or send one back.
//
// Setting Connection: close is all it takes: once the handler returns,
// serveConn writes the answer, flushes it and, when the response header asks
// for a close, leaves its loop instead of reading another request. Closing a
// socket whose receive buffer still holds bytes makes the kernel reset the
// connection rather than close it cleanly, so a client still sending a large
// body may see the reset instead of the answer — the lesser harm.
//
// New installs it on the fasthttp server fiber.New creates, which Listen,
// Listener and App.Test all serve from. App.Handler returns Fiber's handler
// without it; TestGuard_NothingDetachesAnUnreadRequestBody fails non-test code
// in this module that refers to it, or that sets the server's Handler.
func closeConnectionsLeftMidBody(app *fiber.App, bodyTimeout time.Duration) {
	srv := app.Server()
	next := srv.Handler
	srv.Handler = func(ctx *fasthttp.RequestCtx) {
		framing := ctx.Request.Header.ContentLength()
		ambiguous := ambiguousFramingRefusal(&ctx.Request.Header) != ""
		var bodyDeadline time.Time
		if !isStreamedUploadAsRouted(app, ctx) {
			bodyDeadline = time.Now().Add(bodyTimeout)
		}
		if conn := ctx.Conn(); conn != nil {
			_ = conn.SetReadDeadline(bodyDeadline)
		}
		next(ctx)
		expired := !bodyDeadline.IsZero() && !time.Now().Before(bodyDeadline)
		if ambiguous || expired || bodyMayBeLeftUnread(framing, ctx.Request.IsBodyStream()) {
			ctx.Response.SetConnectionClose()
		}
	}
}

// bodyMayBeLeftUnread reports whether part of a request's body may still be on
// the connection once every handler has returned. framing is the request's
// Content-Length as fasthttp parsed it from the head — RequestHeader.ContentLength,
// which is -1 for a chunked body and -2 for a GET or HEAD that frames no body
// (ContinueReadBodyStream turns that into 0 for any other method) — and
// streamAttached is Request.IsBodyStream once the handler has returned.
//
// What it rests on is one inference: a stream no longer attached was read to
// its end. That holds only while two things stay true, and a test fails the
// day either stops:
//
//   - No read deadline cuts a body while the connection is kept. A deadline
//     that fires mid-body ends the read with an error, and Request.bodyBytes
//     releases the stream on an error exactly as it does at the end, with the
//     rest of the body still on the connection. Two deadlines are armed
//     while a handler runs. One is closeConnectionsLeftMidBody's body
//     deadline, and it closes the connection after any answer given once that
//     deadline has passed — which a read it cut always is.
//     TestSlowBodyIsCutAtTheBodyDeadlineAndItsConnectionClosed fails without
//     that close, and TestServerConfigKeepsTheBodyFramingInvariants fails if
//     the server stops being served through the wrapper or the wrapper stops
//     arming that deadline. The other is the storage upload's bound on each
//     read of its streamed body (handlers.stallBoundedBody), which nothing
//     here closes on. It rests on the upload reading the stream itself, never
//     through Request.bodyBytes: the stream then stays attached however a read
//     ends, so bodyMayBeLeftUnread closes the connection after every upload
//     with a body, cut or not (TestUploadThatStopsSendingIsAnswered408).
//   - Nothing releases a stream it has not read to the end. Every request-side
//     SetBody…, AppendBody…, ResetBody, CloseBodyStream, ReleaseBody and Reset
//     does exactly that, and so does anything that overwrites the request or
//     rewrites its framing. TestGuard_NothingDetachesAnUnreadRequestBody fails
//     non-test code that refers to one — within what it can see, which its
//     own comment states — and TestServerConfigKeepsTheBodyFramingInvariants
//     fails a server configured to read a body some other way.
//
// Given those:
//
//   - No body (0, or -2): nothing to leave. fasthttp attaches a stream to
//     every body with a Content-Length, even an empty one, so the stream alone
//     cannot be the test: every bodiless POST a browser sends declares
//     Content-Length: 0, and each would lose its connection.
//   - A Content-Length body: the stream stays attached until something reads
//     it to its end. c.Body() (Request.bodyBytes) copies it through and then
//     closes it, which is what detaches it; a stream read part of the way is
//     still attached. A handler that reads the stream itself — the storage
//     upload, or a multipart form — leaves it attached even after reading
//     every byte, and the connection is closed then too: the safe way to be
//     wrong. bodyBytes closes the stream on a failed read as well; a read of
//     a Content-Length body (requestStream.Read) fails either when the
//     connection itself does, after which there is nothing left to parse, or
//     at the body deadline, after which the connection is closed.
//   - A chunked body (-1): the connection is closed whether or not the stream
//     is attached. The stream reports a malformed chunk as a read error,
//     bodyBytes closes it on that error exactly as it does at the last chunk,
//     and the rest of the body is then still on the connection — once the
//     stream is closed, nothing tells the two apart. (Since Fiber v3.7 c.Body()
//     reads a chunked stream itself and leaves a failed one attached, and
//     since fasthttp v1.75 serveConn closes after a failed read too, so this
//     close is the second of two; it stays for a reader that does neither.)
//     refuseChunkedRequestBodies lets a chunked body reach the storage upload
//     alone, so this costs one connection per chunked upload.
func bodyMayBeLeftUnread(framing int, streamAttached bool) bool {
	switch {
	case framing == -1:
		return true
	case framing > 0:
		return streamAttached
	default:
		return false
	}
}

// The messages refuseAmbiguousFraming answers with.
const (
	obsFoldRefusal = "obsolete line folding is not accepted: no header line may start with a space or tab"

	identityCodingRefusal = "Transfer-Encoding: identity is not accepted: frame the body with a Content-Length alone"
)

// ambiguousFramingRefusal reports why a request's head frames its body in a
// way another HTTP parser — a reverse proxy in front of Nexara — may read
// differently, as the message refuseAmbiguousFraming answers with; or it
// returns an empty string. Two shapes do, both of which fasthttp accepts:
//
//   - obs-fold, a header line starting with a space or tab, which continues
//     the line before it. fasthttp joins it on (headerScanner.
//     readContinuedLineSlice) and refuses one only as the head's first line,
//     so "X-Pad: a\r\n Content-Length: 58" is one X-Pad field to it, and no
//     Content-Length: the body is empty. A proxy that reads the second line as
//     a field of its own forwards 58 bytes of body, which fasthttp then parses
//     as the next request. RFC 9112 §5.2 lets a server reject obs-fold with a
//     400.
//
//   - Transfer-Encoding: identity. RequestHeader.parseHeaders refuses every
//     transfer coding but chunked and identity, a second Transfer-Encoding
//     line, and any Transfer-Encoding at all on an HTTP/1.0 request (RFC 9112
//     §6.1), with a 400 of its own while it parses the head; identity on an
//     HTTP/1.1 request it accepts and ignores, framing the body by
//     Content-Length, or as empty without one. HTTP defines no identity
//     transfer coding any more (RFC 9112 §7), so a proxy may frame such a body
//     another way — as chunked, because the field is there. RFC 9112 §6.3
//     item 4 says what a server does with a request whose final transfer
//     coding is not chunked:
//     "the message body length cannot be determined reliably; the server MUST
//     respond with the 400 (Bad Request) status code and then close the
//     connection" — the 400 refuseAmbiguousFraming answers, and the close
//     closeConnectionsLeftMidBody adds.
//
//     parseHeaders matches the field's name and value ignoring letter case
//     (caseInsensitiveCompare), and the header scanner hands it the value
//     without the spaces and tabs around it (headerScanner.next, trim), from
//     a line ending in CRLF or in a bare LF (headerScanner.readLine) — so this
//     reads the line the same way: "transfer-encoding: IDENTITY",
//     "Transfer-Encoding:identity" and a tab-padded value are all identity to
//     both.
//
// It reads RequestHeader.RawHeaders, the header lines as they arrived:
// fasthttp keeps neither shape in the fields it parses — an identity
// Transfer-Encoding line is dropped, and a folded line becomes part of the
// field above it.
func ambiguousFramingRefusal(h *fasthttp.RequestHeader) string {
	raw := h.RawHeaders()
	if bytes.Contains(raw, []byte("\n ")) || bytes.Contains(raw, []byte("\n\t")) {
		return obsFoldRefusal
	}
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		name, value, found := bytes.Cut(line, []byte(":"))
		if found && bytes.EqualFold(name, []byte(fiber.HeaderTransferEncoding)) &&
			bytes.EqualFold(bytes.TrimSpace(value), []byte("identity")) {
			return identityCodingRefusal
		}
	}
	return ""
}

// refuseAmbiguousFraming answers 400 to a request whose head frames its body
// ambiguously (ambiguousFramingRefusal), before anything reads the body. It
// reads only the head, and closeConnectionsLeftMidBody closes the connection
// after the answer — it asks the same question of every request, including
// the ones Fiber answers before this runs.
func refuseAmbiguousFraming(c fiber.Ctx) error {
	if why := ambiguousFramingRefusal(&c.Request().Header); why != "" {
		return fiber.NewError(fiber.StatusBadRequest, why)
	}
	return c.Next()
}

// chunkedBodyRefusal is the message refuseChunkedRequestBodies answers with.
const chunkedBodyRefusal = "a chunked request body is not accepted: send the body with a Content-Length"

// refuseChunkedRequestBodies answers 411 Length Required to a request whose
// body is sent chunked — anywhere but as a streamed multipart upload
// (isStreamedUpload) — before any of the body is read.
//
// Every bound Nexara puts on a body reads its Content-Length — the 10 MiB
// body-size guard and bodyValues' 64 KiB — and a chunked body has none.
// Under StreamRequestBody fasthttp streams a body rather than refusing it, and
// c.Body() — or c.Bind(), which reads through it — copies what the client
// sends up to BodyLimit (32 MiB; Fiber v3.7 internal/bodystream answers 413
// past it), three times the guard, on routes that need no session among them
// (login, register, logout). RFC 9112 §6.3 lets a server
// reject a request that carries a body but no Content-Length with 411, the
// status RFC 9110 §15.5.12 defines for exactly that refusal; the client may
// repeat the request with a Content-Length.
//
// "Chunked" is read the way the body's own reader reads it: requestStream
// decodes chunks exactly when RequestHeader.ContentLength is -1, which
// RequestHeader.parseHeaders sets for a Transfer-Encoding of chunked, in any
// letter case, over any Content-Length. parseHeaders answers every other
// transfer coding but one, a second Transfer-Encoding line, and any
// Transfer-Encoding on an HTTP/1.0 request — chunked included, so this 411 is
// an HTTP/1.1 one — with a 400 of its own while it parses the head. The one it
// accepts, over HTTP/1.1, is identity, which it ignores; refuseAmbiguousFraming
// answers that one with a 400 of Nexara's own.
// None of a chunked body has been read when this runs — readBodyWithStreaming
// reads nothing of one — and the connection is closed after the refusal by
// closeConnectionsLeftMidBody, which closes it after every request with a
// chunked body.
//
// A streamed multipart upload is exempt: an ISO, CT template or OVA streams
// through StreamRequestBody to a handler that reads the stream itself and
// never buffers it, so a client that sends one chunked keeps working. That
// body stays unbounded by design — the route is authenticated, and nothing
// reads the body before the handler has checked the caller's grants (see
// isStreamedUpload). Any other body sent to that path — a JSON one, a +json
// type such as multipart/form-data+json included — is refused chunked here
// like one sent anywhere else.
func refuseChunkedRequestBodies(c fiber.Ctx) error {
	if c.Request().Header.ContentLength() == -1 && !isStreamedUploadRequest(c) {
		return fiber.NewError(fiber.StatusLengthRequired, chunkedBodyRefusal)
	}
	return c.Next()
}

// isStreamedUpload reports whether a request with this method, path — the path
// the router matches, c.Path() — and Content-Type is a streamed multipart body for
// the storage upload route (isStreamedMultipart), which three bounds on a
// request body exempt: the 10 MiB body-size guard, refuseChunkedRequestBodies,
// and closeConnectionsLeftMidBody's body deadline. It is the one predicate for
// all three, so the exemptions cannot drift apart.
//
// An exemption must be no looser than the body it is for: letting a request
// that some OTHER route answers, or some other reader reads, skip a body bound
// would hand that reader an unbounded body, while refusing an odd spelling of
// the upload only refuses a request no client of Nexara sends.
//
// It matches the method and the path exactly as the route is declared —
// storageUploadPath, the constant registerStorageEndpoints mounts the route
// on — and is stricter than Fiber's router, which matches a lowercased path
// with every trailing slash trimmed (DefaultCtx.configDependentPaths) and so
// sends "/API/v1/.../UPLOAD/" to the upload route as well. Here each literal
// segment must be spelled as declared, each parameter must be a non-empty
// segment, and there is no trailing slash.
//
// The path is the one the router matches, before that folding: c.Path(),
// which since Fiber v3.6 is the request path normalized as RFC 3986 §6.2.2
// says (DefaultCtx.configDependentPaths, normalizeRequestPath): escapes of
// unreserved characters decoded, every other escape kept, "." and ".."
// segments removed. So "…/uplo%61d" and "…/<id>/../<id>/upload" are the
// upload to the router, and to this. Both callers pass it:
// isStreamedUploadRequest from inside the chain, and closeConnectionsLeftMidBody,
// which runs before Fiber has a Ctx, through isStreamedUploadAsRouted.
// TestStreamedUploadPredicateSeesThePathTheRouterSees holds the two to each
// other, TestServerConfigKeepsTheBodyFramingInvariants holds the wrapper's
// deadline to it over the wire, and TestStreamedUploadExemptionIsNoLooserThanItsRoute
// holds the predicate against the assembled route table. The Content-Type is
// the same bytes both ways too: c.Get — what isStreamedUploadRequest and
// UploadFile call — peeks the header, which answers a Content-Type key with
// RequestHeader.ContentType. And none of the three may change after the
// wrapper asked:
// TestGuard_NothingDetachesAnUnreadRequestBody refuses code in this module
// that rewrites a request's path, method or Content-Type.
func isStreamedUpload(method, path, contentType string) bool {
	return method == fiber.MethodPost && isStreamedMultipart(contentType) &&
		matchesDeclaredPath(path, storageUploadPath)
}

// isStreamedMultipart reports whether a body with this Content-Type is one
// UploadFile streams, and that nothing reads before UploadFile has checked the
// caller's grants — the one kind of body the upload route may leave unbounded.
//
// It is UploadFile's own test, handlers.ParseMultipartContentType, which both
// share: a multipart media type. And it is not a type isJSONContentType
// accepts. The endpoint is Deferred, so its declaration's bodyValues runs
// before UploadFile does, and reads a body of any type isJSONContentType
// accepts — up to 64 KiB, with c.BodyRaw() — before a grant is checked. A +json
// structured-syntax type is one such, whatever comes before the suffix, so
// "multipart/form-data+json" is both multipart to UploadFile and JSON to
// bodyValues: exempted, any authenticated caller could declare one, send 8 KiB
// and hold the connection for as long as it liked. Asking bodyValues' own
// predicate, of the same Content-Type bytes, is what keeps the two apart
// whatever either comes to accept; TestStreamedUploadIsNeverABodyReadAsJSON
// holds them apart over a corpus of Content-Types.
//
// It asks for no more than that — any multipart subtype, not only form-data —
// because a stricter test here would bound a body UploadFile still streams.
func isStreamedMultipart(contentType string) bool {
	_, multipart := handlers.ParseMultipartContentType(contentType)
	return multipart && !isJSONContentType(contentType)
}

// isStreamedUploadRequest is isStreamedUpload for a request inside Fiber's
// middleware chain.
func isStreamedUploadRequest(c fiber.Ctx) bool {
	return isStreamedUpload(c.Method(), c.Path(), c.Get(fiber.HeaderContentType))
}

// isStreamedUploadAsRouted is isStreamedUploadRequest for a request Fiber has
// not taken up yet. It asks a Ctx of the app's own for the path, because the
// router matches the normalized path, not fasthttp's PathOriginal: asked of
// the raw path, a POST to "/api/v1/clusters/../storage/<id>/upload" — a 404 to
// the router — was exempt from the body deadline, and the upload spelled
// "…/uplo%61d" was not. AcquireCtx only resets the Ctx onto the request
// (DefaultCtx.Reset): it reads the request line and nothing of the body.
func isStreamedUploadAsRouted(app *fiber.App, fctx *fasthttp.RequestCtx) bool {
	c := app.AcquireCtx(fctx)
	defer app.ReleaseCtx(c)
	return isStreamedUploadRequest(c)
}

// matchesDeclaredPath reports whether path has exactly the segments of the
// route pattern: a literal segment spelled the same, a plain ":name" parameter
// matched by any one non-empty segment.
//
// Fiber's pattern syntax has more — a constrained parameter (":id<int>"), an
// optional one (":id?"), two parameters in one segment (":name.:ext",
// ":a-:b"), wildcards ("*", "+") — and every one of those matches a different
// set of paths than "any one non-empty segment": a constraint or a second
// parameter fewer, a wildcard more. Reading one as a plain parameter would
// exempt paths its route does not serve, so a pattern using any of it matches
// no path here: the route loses its exemption, which fails closed.
// TestMatchesDeclaredPathFailsClosedOnPatternSyntaxItDoesNotRead holds that
// against fiber.RoutePatternMatch.
func matchesDeclaredPath(path, pattern string) bool {
	got := strings.Split(path, "/")
	want := strings.Split(pattern, "/")
	if len(got) != len(want) {
		return false
	}
	for i, seg := range want {
		if name, isParam := strings.CutPrefix(seg, ":"); isParam {
			if !isPlainParamName(name) || got[i] == "" {
				return false
			}
			continue
		}
		if strings.ContainsAny(seg, ":*+?<>") || got[i] != seg {
			return false
		}
	}
	return true
}

// isPlainParamName reports whether name is a bare parameter name — ASCII
// letters, digits and underscores, and nothing else.
func isPlainParamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
