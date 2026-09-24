package api

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// contentSecurityPolicy is the default CSP for the SPA shell + API responses.
// JSX auto-escaping is otherwise the only XSS control, so this is the second
// line of defense. Notes on the directives:
//   - script-src 'self': the Vite production build emits only external module
//     scripts (no inline, no eval). index.html must NOT carry inline scripts —
//     they get silently blocked (console-only error). The pre-paint theme
//     bootstrap lives in frontend/public/theme-init.js for exactly this reason.
//   - style-src 'unsafe-inline': Tailwind/shadcn (Radix), Recharts, and React
//     Flow inject inline styles — required, and low-risk for styles.
//   - connect-src ws: wss': the floating console (xterm) and noVNC open
//     same-origin WebSockets; ws: covers the dev (http) origin.
//   - worker-src/img-src blob': noVNC/xterm renderers and canvas-to-blob.
//
// Handlers that serve downloadable HTML (reports, settings export) set their
// own stricter CSP via c.Set after this middleware, which overrides it.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self' ws: wss:; " +
	"worker-src 'self' blob:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// corsAllowOrigins converts the comma-separated CORS_ALLOW_ORIGINS config value
// into the []string that Fiber v3's cors middleware expects (v2 accepted a raw
// comma-string). An empty or literal "*" value maps to ["*"], preserving the v2
// behavior where an unset AllowOrigins defaulted to "*" (allow all). The cors
// middleware sets no credentials here, so a wildcard origin is valid and does not
// trip v3's AllowCredentials+"*" guard.
func corsAllowOrigins(raw string) []string {
	if raw == "" || raw == "*" {
		return []string{"*"}
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

func (s *Server) setupMiddleware() {
	// Recover from panics.
	s.app.Use(recover.New())

	// Security headers (proxy-agnostic — always present regardless of reverse proxy choice).
	s.app.Use(func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("Content-Security-Policy", contentSecurityPolicy)
		// HSTS is opt-in (HSTS_MAX_AGE > 0) — pinning HTTPS over a self-signed or
		// plain-HTTP origin would make cert errors unbypassable.
		if s.config.HSTSMaxAge > 0 {
			c.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(s.config.HSTSMaxAge)+"; includeSubDomains")
		}
		return c.Next()
	})

	// Expose the shared Proxmox client cache to every request so
	// handlers.CreateProxmoxClient routes through it. Nil-safe: when
	// proxmoxCache is unset (no encryption key, test scaffolding) the
	// helper falls through to per-call construction.
	if s.proxmoxCache != nil {
		s.app.Use(func(c fiber.Ctx) error {
			handlers.SetProxmoxCacheLocal(c, s.proxmoxCache)
			return c.Next()
		})
	}

	// Add unique request ID.
	s.app.Use(requestid.New())

	// Structured request logging with request ID.
	// Fiber v3's requestid middleware registers a logger context tag
	// (logger.RegisterContextTag("requestid", …)), so the ID is referenced as
	// ${requestid} — the v2 ${locals:requestid} no longer resolves because
	// requestid stores the value in the request context, not c.Locals.
	s.app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${ip} | ${requestid} | ${method} ${path}\n",
	}))

	// Refuse (400) a request whose head frames its body ambiguously — an
	// obs-fold line, or Transfer-Encoding: identity — before anything reads
	// it; see refuseAmbiguousFraming. It reads only the head, and sits with
	// the two gates below for the reasons they do: ahead of every route, and
	// behind requestid and the security headers, so its refusal carries both.
	s.app.Use(refuseAmbiguousFraming)

	// Refuse a content-coded request before any handler reads its body, so
	// that nothing ever decodes it. See refuseContentCodedRequests for what it
	// refuses, and why on the header alone.
	//
	// When it runs, fasthttp has copied at most the first 8 KiB of the body
	// into the request (readBodyWithStreaming, under StreamRequestBody),
	// undecoded; its read buffer (ReadBufferSize, 16 KiB) may already hold
	// more, and the rest is still on the socket. fasthttp does not drain what
	// a handler leaves unread — on a kept-alive connection it parses the
	// remainder as the next request — so the connection is closed after the
	// refusal, by closeConnectionsLeftMidBody (body_framing.go), which does
	// that after every answer that leaves a body unread.
	// TestContentCodingRefusalClosesTheConnection pins it for this one.
	//
	// Its position is what makes it cover every route, and it is the only
	// copy of the check. New runs setupMiddleware before setupRoutes, main.go
	// mounts the /ws upgraders and the embedded-SPA handler after New, and
	// Fiber runs app-level handlers in the order they were registered — so the
	// registry routes, the legacy ones in router.go, the WebSocket upgrades and
	// the SPA handler all sit behind it. The chunked-body refusal, CORS, the
	// body-size guard, the limiters and compression happen to come after it
	// too; none of them reads a body, so its place among them carries no
	// weight.
	//
	// What runs ahead of it — recover, the security headers, the Proxmox-cache
	// local, requestid, the logger and the ambiguous-framing refusal — reads
	// headers, never a body. It sits
	// after them rather than first so that a refusal carries the security
	// headers and a request id and is logged like every other rejection;
	// TestContentCodingRefusalIsAccessLogged pins the last. The logger is the
	// one of them that COULD read a body, and after the refusal at that: it
	// renders its line once the chain returns, its ${body} tag calls c.Body(),
	// which decodes, and a ${form:…} tag calls c.FormValue, which on a
	// multipart body gunzips through fasthttp's multipart reader. The format
	// above uses neither. TestContentCodedRequestIsRefusedBeforeItsBodyIsRead
	// drives this whole stack with a body stream that records reads, and
	// fails if any of it starts to.
	s.app.Use(refuseContentCodedRequests)

	// Refuse a chunked request body with 411 before anything reads it, but for
	// a streamed multipart upload (isStreamedUpload); see
	// refuseChunkedRequestBodies. It reads only the head, as the content-coding
	// gate does, and sits beside it for the same reasons: ahead of every route,
	// and behind requestid and the security headers, so its refusal carries
	// both.
	s.app.Use(refuseChunkedRequestBodies)

	// CORS. Fiber v3 takes []string for the allow-lists (v2 took comma-strings).
	s.app.Use(cors.New(cors.Config{
		AllowOrigins: corsAllowOrigins(s.config.CORSAllowOrigins),
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	}))

	// Body-size guard: a declared Content-Length over 10 MiB is refused (413)
	// before any of the body is read — anywhere but as a streamed multipart
	// upload (isStreamedUpload).
	//
	// What bounds a request body, by kind:
	//
	//   - A body with a Content-Length: this guard. A registry endpoint that
	//     declares no body parameter also refuses a JSON body over 64 KiB
	//     unread (bodyValues).
	//   - A chunked body: refused (411) before any of it is read
	//     (refuseChunkedRequestBodies).
	//   - A content-coded body: refused (415) on every route
	//     (refuseContentCodedRequests), so nothing decodes a body past these
	//     bounds.
	//   - How long a body takes: bodyReadTimeout from when the handler starts
	//     (closeConnectionsLeftMidBody).
	//   - A streamed multipart upload: none of these, by design. ISO,
	//     CT-template and OVA files many GiB in size stream through the storage
	//     upload to Proxmox. The route is authenticated, and nothing reads the
	//     body before its handler has checked the caller's grants.
	//     isStreamedUpload is the exemption here, in the chunked refusal and in
	//     the body deadline; it matches the route only as it is declared, and a
	//     multipart body of no type the declaration's bodyValues reads as JSON
	//     — a +json type such as multipart/form-data+json is one, and bodyValues
	//     reads it before any grant is checked. Every other body sent there is
	//     bounded like a body sent anywhere else.
	//
	// BodyLimit bounds none of these reads: under StreamRequestBody fasthttp
	// streams a larger body instead of refusing it. Each refusal leaves the
	// body unread, and closeConnectionsLeftMidBody closes the connection after
	// it. The length compared is the one fasthttp parsed from the head
	// (RequestHeader.ContentLength), the same reading the chunked refusal and
	// the body's own reader take — -1 for a chunked body, which is never over
	// the limit here and is the chunked refusal's to answer.
	const apiBodyLimit = 10 * 1024 * 1024
	s.app.Use(func(c fiber.Ctx) error {
		if c.Request().Header.ContentLength() > apiBodyLimit && !isStreamedUploadRequest(c) {
			return fiber.ErrRequestEntityTooLarge
		}
		return c.Next()
	})

	// Strict rate limiter for login/register and TOTP code-validating paths
	// — 15 attempts per minute per IP. Applied before the general limiter so
	// auth brute-force is caught early. Includes Disable and
	// RegenerateRecoveryCodes because both validate a TOTP code; without this,
	// an attacker holding a stolen access token would have no per-IP cap and
	// only the per-user lockout (5 fails / 5 min cooldown) — see Phase 4.4.
	s.app.Use(limiter.New(limiter.Config{
		Max:        15,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":auth"
		},
		Next: func(c fiber.Ctx) bool {
			return !authLimitedPaths[limiterPath(c)]
		},
	}))

	// Refresh-specific rate limiter — 30/min/IP. Tighter than the general
	// limiter because /auth/refresh is below the general bypass; comfortably
	// above any legitimate pattern (proactive refresh fires once every ~14
	// minutes per session). Caps cookie-replay grinding without breaking
	// normal browser sessions.
	s.app.Use(limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":refresh"
		},
		Next: func(c fiber.Ctx) bool {
			return limiterPath(c) != refreshLimitedPath
		},
	}))

	// WS-token mint limiter — 60/min/IP. Each /ws connection mints one
	// token; legitimate reconnect/backoff is well under 1/sec. The
	// limiter sits inside the auth-bypassed group, so without this an
	// authenticated user could fire mints in a tight loop. Per-IP
	// (vs per-user) because limiter middleware runs before authRequired.
	s.app.Use(limiter.New(limiter.Config{
		Max:        60,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":ws-token"
		},
		Next: func(c fiber.Ctx) bool {
			return limiterPath(c) != wsTokenLimitedPath
		},
	}))

	// Snapshot-resync limiter — each call makes one Proxmox listing plus one
	// DB write per snapshot, and the endpoint is view-gated, so without a
	// dedicated cap a read-only account could pin both the DB and PVE well
	// within the general limiter's budget.
	s.app.Use(limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":snapshot-resync"
		},
		Next: func(c fiber.Ctx) bool {
			return !strings.HasSuffix(limiterPath(c), "/guest-snapshots/resync")
		},
	}))

	// General rate limiting (in-memory storage).
	// Skip auth endpoints so token refresh is never blocked — a 429 on
	// /auth/refresh causes the frontend to interpret it as an auth failure
	// and log the user out.
	s.app.Use(limiter.New(limiter.Config{
		Max:        s.config.RateLimitMax,
		Expiration: s.config.RateLimitExpiration,
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(limiterPath(c), authCookieScopePrefix) ||
				strings.HasPrefix(limiterPath(c), "/ws")
		},
	}))

	// Refuse a write whose path ends in "/" before it can reach a route. See
	// refuseTrailingSlashWrites for what it refuses and why.
	//
	// Its position is what makes it cover every route, the same way the
	// content-coding gate's does: setupMiddleware runs before setupRoutes,
	// and main.go mounts the /ws upgraders and the embedded-SPA handler
	// after New, so the registry routes, the legacy ones in router.go, the
	// upgraders and the SPA handler all sit behind it.
	// TestTrailingSlashGatePrecedesEveryRoute sweeps the route table for it.
	//
	// Among the app-level middleware, each neighbour was chosen:
	//
	//   - After the logger, requestid and the security headers, so a refusal
	//     carries a request id and the security headers and is access-logged
	//     like any other rejection — with the path as sent, trailing slash
	//     included, since the logger's ${path} reads the same c.Path().
	//   - After the content-coding gate and the two framing refusals beside
	//     it (refuseAmbiguousFraming, refuseChunkedRequestBodies). A write
	//     any of them refuses — a content-coded or chunked one with a
	//     trailing slash, say — gets that refusal first.
	//   - After CORS, which for a request that is not a preflight sets its
	//     response headers and calls Next. So a cross-origin caller's refused
	//     write still carries Access-Control-Allow-Origin, and its browser
	//     hands the caller this 400 rather than an opaque CORS failure. A
	//     preflight never gets here: CORS answers it with a 204 first.
	//   - After the body-size guard and every limiter. A refused write still
	//     spends the caller's own limiter budget, the rule this API already
	//     follows for permission checks: a flood of them is throttled rather
	//     than answered with free refusals. It is also what keeps a trailing-
	//     slash spelling of an auth-limited path on the auth cap —
	//     TestAuthLimiterCannotBeSpelledAround requires that of POST
	//     /api/v1/auth/login/, and fails if this gate moves ahead of the
	//     limiters. None of them reads a body; nothing here does before a
	//     route runs.
	//   - Ahead of compression and of every route: a refusal returns before
	//     either runs.
	//
	// It does not read the body, and it does not close the connection itself:
	// closeConnectionsLeftMidBody (body_framing.go), which sees every answer
	// the server gives, closes the connection after any answer that leaves a
	// body unread — this refusal's, as it does the 401, 404, 405, 413 and 429
	// answers — so what is left of the body is never read as the next
	// request. TestUnreadBodyClosesTheConnection holds it to that for this
	// refusal too.
	s.app.Use(refuseTrailingSlashWrites)

	// Response compression — see compressionSkipped for what is excluded and
	// why, and the block comment above it for the BREACH assessment.
	//
	// Registered last in setupMiddleware, so everything that produces a
	// compressible body sits INSIDE it: the route handlers setupRoutes mounts,
	// the /ws auth middleware and upgraders wsServer.RegisterRoutes adds, and
	// the embedded-SPA static handler RegisterFrontend appends — main.go calls
	// all three after New(). It is not the innermost app-level middleware in
	// the process (those three Use calls land after it); it is the last one
	// this function installs, which is what the ordering below is about.
	//
	// The ordering is load-bearing in four places, each of which breaks
	// something if reversed:
	//
	//   - Inside recover.New(). Fiber's compress middleware post-processes
	//     after c.Next() returns and has no deferred recover of its own, so a
	//     panic unwinds straight past it: recover catches it and the 500 goes
	//     out uncompressed, rather than compress running against a
	//     half-written response.
	//   - Inside logger.New(). The logger measures latency around its own
	//     c.Next(), so compression time is counted in the latency it reports —
	//     which is the number the client actually experienced.
	//   - Inside cors.New(). A preflight is short-circuited by cors with a 204
	//     and never reaches compress at all. (shouldSkip would skip a 204
	//     anyway; this just avoids the call.)
	//   - Inside every limiter. A 429 or a 413 from the body-size guard
	//     short-circuits above this point, so a request being throttled never
	//     pays for compression. Those envelopes are all well under fasthttp's
	//     200-byte floor, so nothing is lost by not compressing them.
	//
	// One consequence worth stating because it is invisible from here: an
	// error RETURNED to Fiber is never compressed. Fiber's compress middleware
	// does `if err := c.Next(); err != nil { return err }` and skips its
	// post-processing entirely, and buildFiberConfig's ErrorHandler runs above
	// the whole Use chain — so every 4xx/5xx raised via fiber.NewError is
	// rendered after compress has already bailed out.
	//
	// That is a statement about the ErrorHandler path ONLY, not about error
	// responses in general. Plenty of handlers answer with c.Status(…).JSON(…)
	// and a nil error instead, and compress does post-process those. Most sit
	// on mutating routes, where compressionSkipped's method rule covers them;
	// the /ws auth rejections are the notable GET-shaped exception and reach
	// compress unguarded. Nothing comes of it either way — every one of those
	// bodies is a few dozen bytes, under the size floor — but the reason is
	// the floor, not this paragraph.
	//
	// TestCompression_ErrorEnvelopesAreNeverCompressed pins the ErrorHandler
	// half, with a body large enough that the floor cannot be why it passes.
	if s.config.CompressionEnabled {
		s.app.Use(compress.New(compress.Config{Next: compressionSkipped}))
	}
}

// contentCodingRefusal is the message refuseContentCodedRequests answers
// with.
const contentCodingRefusal = "request body must be sent uncompressed: no Content-Encoding other than identity is accepted"

// refuseContentCodedRequests answers 415 to any request that names a content
// coding other than identity, and never touches the body to do it.
//
// Fiber's c.Body() transparently decodes gzip, deflate, br and zstd (and the
// x-gzip and brotli spellings), each step up to the 32 MiB BodyLimit. The
// bounds Nexara puts on a body both read its Content-Length — the 10 MiB
// body-size guard, on everything but a streamed multipart upload, and
// bodyValues' 64 KiB, on an endpoint that declares no body parameter — so they
// measure the body as it crosses the wire, the compressed bytes, and never
// what it decodes to. Measured with fasthttp's own encoders,
// 32 MiB of repeated bytes is 52 bytes of brotli or about 3.5 KB of zstd, and
// gzip gets there from about 64 KiB at its default level.
//
// Those bounds have a gap of their own, which refuseChunkedRequestBodies
// closes rather than this: a chunked body carries no Content-Length for
// either to read, so it is refused with 411 on every route but the storage
// upload, whose handler reads the stream itself.
//
// Several routes read a body before any session exists —
// POST /api/v1/auth/login, and the legacy register and logout routes, among
// them. Nor is c.Body() the only decoder: c.FormFile and c.MultipartForm, and
// c.Bind().Body() whenever the caller labels its body multipart, go through
// fasthttp's MultipartFormWithLimit, which gunzips by itself. Nexara has no
// use for a compressed request, so it refuses every one, on every route,
// here — rather than capping what each of those decoders may inflate.
//
// Refused on the header alone, whatever the method and whether or not a body
// follows. Narrowing it to "a request that carries a body" would take a
// second reading of the request — Content-Length, which is -1 for a chunked
// body, or the method, when a GET can carry a body too — and that reading
// would have to agree with fasthttp's framing on every request or become the
// bypass. It would buy a conforming client nothing: Content-Encoding describes
// content, and a request without content has none to describe. A HEAD, an
// OPTIONS preflight or a WebSocket upgrade carries no Content-Encoding and
// passes untouched. A client that sends one anyway — as a session-wide default
// header, or mistaking it for a charset — is refused even on a GET, which
// before this check it was not.
//
// RFC 9110 §8.4 lets an origin server answer 415 to a content coding it does
// not accept, and §12.5.3 (restated in §15.5.16) asks a server that fails a
// request over a content coding to say in Accept-Encoding which codings it
// would have taken: here only identity, the synonym for "no encoding". The
// same section forbids that header on a 415 sent for any other reason; today
// this is the only 415 the API sends.
func refuseContentCodedRequests(c fiber.Ctx) error {
	if hasContentCoding(c) {
		c.Set(fiber.HeaderAcceptEncoding, "identity")
		// The body is left unread on purpose, and fasthttp does not drain what
		// a handler leaves: on a kept-alive connection it parses whatever is
		// left of this body as the next request. closeConnectionsLeftMidBody
		// closes the connection after this answer, as it does after every
		// answer that leaves a body unread. There is deliberately no close of
		// its own here: two copies of that decision would each hide the other
		// from every test, and TestContentCodingRefusalClosesTheConnection's
		// precondition fails if one comes back.
		return fiber.NewError(fiber.StatusUnsupportedMediaType, contentCodingRefusal)
	}
	return c.Next()
}

// trailingSlashRefusal is the message refuseTrailingSlashWrites answers
// with.
const trailingSlashRefusal = `only GET, HEAD and OPTIONS requests may end their path in "/"; ` +
	`send this one without the trailing slash`

// refuseTrailingSlashWrites answers 400 to a request whose path ends in "/"
// unless it is a GET, a HEAD or an OPTIONS, before any route can see it.
//
// # Why
//
// A browser resolves a "." or ".." path segment before a request leaves it:
// the WHATWG URL parser every browser shares drops a "." segment, drops a
// ".." segment together with the segment before it, and counts "%2e" in
// either case as a dot while doing so. When the dot segment is the LAST
// one, what goes out ends in a slash: ".../pools/." is sent as ".../pools/"
// and ".../pools/.." as "/api/v1/clusters/<id>/", which is how the SPA's
// DELETE of a pool named ".." reached the server. Fiber ignores a trailing
// slash when it routes (StrictRouting is off in buildFiberConfig), so that
// request matched the CLUSTER delete route and removed the cluster. A final
// dot segment always leaves such a slash behind, so refusing it here closes
// that case for every route at once, whatever the id, the object or the
// caller. No caller needs one: the SPA sends every write without it. That
// includes the two write routes router.go registers on a group root, which
// the route table lists as POST /api/v1/alert-rules/ and POST
// /api/v1/firewall-templates/ but which Fiber matches without the slash.
//
// A dot segment in the MIDDLE of a path leaves no such mark. "." there
// just disappears and ".." takes the segment before it with it, so what is
// sent is an ordinary path — ".../pools/../members" goes out as
// "/api/v1/clusters/<id>/members" — which may be a real route. Only the
// SPA's refusal to put a "." or ".." into a path at all covers that case
// (apiPath, frontend/src/lib/api-path.ts); this gate does not.
//
// # What it reads
//
// c.Path(), which is the request path exactly as the router sees it before
// its own trailing-slash strip: configDependentPaths copies fasthttp's
// PathOriginal — the raw path, not percent-decoded, not dot-resolved, the
// query excluded — and with UnescapePath off (buildFiberConfig does not set
// it) Path returns those bytes unchanged. The router then trims every
// trailing "/" from the same bytes when the path is longer than one
// character, and this gate uses the same length test, so "/" alone is the
// root rather than a trailing slash and "//" at the end is refused like "/".
// "/x%2F" does not end in "/" to either of them.
//
// # Which methods
//
// Every method but GET, HEAD and OPTIONS — the three Nexara answers as reads.
// That list is Nexara's own contract, not HTTP's list of safe methods: RFC
// 9110 §9.2.1 also calls TRACE safe, RFC 10008 does QUERY, and Fiber v3
// routes both by default, but no Nexara route serves either, so exempting
// them would only let a trailing-slash QUERY through to whatever catch-all
// is added next. POST, PUT, PATCH and DELETE are the writes Nexara routes;
// refusing every method outside the three also covers CONNECT, TRACE, QUERY
// and a route added later under another method, without anyone having to
// remember this gate. The three pass because a mis-resolved one changes
// nothing: the worst a trailing-slash GET does is read the parent resource,
// with the caller's own permissions — the SPA handler serves deep links to
// GET under any path — and a HEAD is a GET without the body. An OPTIONS
// changes nothing either: a CORS preflight is answered by CORS before it
// gets here, and any other OPTIONS by the router. (A method Fiber does not
// know is answered 501 by the router before any middleware runs.)
//
// # Why 400
//
// The request is malformed, not aimed at something missing. A 404 would
// claim the resource does not exist, and a client that treats a 404 on
// DELETE as "already gone" would then record a deletion that never
// happened; a 405 would claim the method is not allowed there. Both are
// false — the same request without the slash reaches its route.
func refuseTrailingSlashWrites(c fiber.Ctx) error {
	if p := c.Path(); len(p) > 1 && p[len(p)-1] == '/' && !isReadMethod(c.Method()) {
		return fiber.NewError(fiber.StatusBadRequest, trailingSlashRefusal)
	}
	return c.Next()
}

// isReadMethod reports whether Nexara answers this method as a read, which
// is what exempts it from refuseTrailingSlashWrites: GET, HEAD and OPTIONS.
// See that function for why TRACE and QUERY are not on the list.
func isReadMethod(method string) bool {
	switch method {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return true
	}
	return false
}

// hasContentCoding reports whether the request names any content coding
// other than identity, on ANY of its Content-Encoding field lines.
//
// It walks the headers the way c.Body() does before it decodes — every field
// line whose name matches case-insensitively, combined into one list (RFC 9110
// §5.2) — because anything narrower is a bypass. fasthttp's
// Header.ContentEncoding() returns the first line alone, so a check built on
// it would pass "identity" followed by a second "gzip" line, which c.Body()
// inflates, and an empty line followed by "gzip", which it inflates too unless
// fasthttp happened to store that empty value as nil. Header.PeekAll
// would be no better once header normalising is off: it matches the name
// exactly, so "content-encoding: gzip" after "Content-Encoding: identity"
// would hide from it and not from c.Body(). Empty list elements are
// skipped, as §5.6.1.2 requires and as c.Body() does too, so an empty
// element can hide nothing. "identity" is §12.5.3's synonym for no
// encoding: §8.4 says a sender SHOULD NOT send it, but c.Body() reads it as
// the no-op it is, and refusing it would refuse a body that decodes to
// itself.
//
// It is wider than each decoder it stands in front of, never narrower.
// c.Body() decodes nothing unless some line is named exactly
// "Content-Encoding" (the fast path at the top of Fiber's DefaultReq.Body),
// and MultipartFormWithLimit gunzips only when the first such line is exactly
// "gzip"; this refuses a coding on any line, under any spelling of the name.
// With header normalising on, as Nexara runs, every spelling is parsed into
// "Content-Encoding" and the readings coincide.
func hasContentCoding(c fiber.Ctx) bool {
	for name, line := range c.Request().Header.All() {
		if !bytes.EqualFold(name, []byte(fiber.HeaderContentEncoding)) {
			continue
		}
		for coding := range bytes.SplitSeq(line, []byte{','}) {
			if coding = bytes.TrimSpace(coding); len(coding) > 0 && !bytes.EqualFold(coding, []byte("identity")) {
				return true
			}
		}
	}
	return false
}

// compressionSkipped reports whether a request's response must NOT be
// compressed. Returning true skips Fiber's compress middleware.
//
// ── BREACH assessment ───────────────────────────────────────────────────
//
// BREACH needs four things at once: a compressed response, a SECRET in that
// response body, ATTACKER-CHOSEN text reflected into the SAME body, and the
// ability to make the victim's browser issue that request thousands of times
// while measuring the compressed size. Nexara fails the fourth condition
// outright, for a reason that is structural rather than incidental:
//
//   - The credential that authorizes the entire /api/v1 surface is a Bearer
//     token (or an nxra_ API key) in the Authorization HEADER — see
//     extractBearerToken. A header is not covered by Content-Encoding, and
//     fasthttp speaks HTTP/1.1 only, so there is no HPACK header compression
//     either: the credential is never in the compressed stream. More
//     importantly, a header credential is not AMBIENT. A cross-origin page
//     cannot make a browser attach it, so an attacker cannot cause an
//     authenticated request at all — and anyone who CAN issue the request
//     already holds the token and can simply read the plaintext body. There
//     is no "cause it but can't read it" gap for a side channel to exploit.
//   - The one ambient credential is the refresh cookie, and it is HttpOnly +
//     SameSite=Strict + Path=/api/v1/auth/ (handlers/auth_cookies.go).
//     SameSite=Strict means it is not attached to ANY cross-site request,
//     including a top-level navigation from an attacker's page.
//   - No response body carries a STABLE secret paired with free-form
//     reflected input. Every token-bearing body mints a fresh random secret
//     per response (login, refresh, console/ws tokens, TOTP enrolment, API
//     key create, PVE token create). BREACH extracts a secret that repeats
//     across the responses it measures; a value that is re-randomised on
//     every request cannot be recovered a byte at a time.
//
// Conclusion: BREACH does not apply to Nexara as it stands. What follows is
// therefore not a fix for a live vulnerability — it is the cheap half of the
// trade. The endpoints excluded below are the only ones whose bodies can
// carry a credential, they are all small single-object responses, and they
// are all low-volume, so excluding them saves an attacker's future self a
// great deal and costs the operator no measurable bandwidth. It also means a
// later change to the auth model — cookie auth on /api/v1, a relaxed
// SameSite, a secret that stops being per-request — does not silently turn
// eleven handlers into oracles.
//
// ── What is NOT excluded, deliberately ──────────────────────────────────
//
// WebSocket upgrades (/ws, /ws/console, /ws/vnc) need no entry here. A
// successful upgrade leaves the response at 101, and compress's shouldSkip
// bails on any status < 200, so the library already excludes it and a /ws case
// would duplicate that. TestCompression_WebSocketUpgradeStillWorks pins the
// upgrade end-to-end so a Fiber release that changed shouldSkip would fail the
// build rather than break consoles in production.
//
// A REJECTED upgrade is a different matter, and not what it looks like: only
// the no-upgrade-header branch returns a *fiber.Error (fiber.ErrUpgradeRequired
// in ws/server.go); the token and scope/origin branches all answer with
// c.Status(…).JSON(…) and a nil error, so compress does post-process them.
// Nothing comes of it — those bodies are 20-60 bytes, well under the floor
// below — but the reason is the floor, not the error path.
//
// Likewise absent: a small-response floor (fasthttp declines to compress a
// BUFFERED body under 200 bytes — minCompressLen; a body STREAM has no floor,
// so a tiny file served by the SPA static handler can still be compressed,
// which is harmless), a content-type allow-list (fasthttp already restricts
// compression to text/*, application/*, image/svg, image/x-icon, font/* and
// multipart/*, so the branding PNG/JPEG/WEBP endpoints are skipped for free),
// an already-compressed check (both Fiber and fasthttp bail when
// Content-Encoding is already set), and Range requests (shouldSkip handles
// them, which is what keeps the SPA static handler's byte-range serving
// intact).
func compressionSkipped(c fiber.Ctx) bool {
	// Rule 1 — compress only responses to SAFE methods.
	//
	// Every one of the eleven handlers that puts a credential in a response
	// body answers a POST or a PUT: login/register/refresh, the console and
	// ws-token mints, all three TOTP enrolment steps, API key create, and PVE
	// token create/regenerate. Two of those additionally reflect caller-chosen
	// free text into the same body — the API key's `name` (100 chars) and the
	// PVE token's `comment` (1024 chars) — which is the literal shape BREACH
	// names, minus the delivery vehicle.
	//
	// Stated as a method rule rather than a list of paths on purpose: a path
	// list in this file and the routes in registry_*.go agree only by
	// spelling, so renaming a route would silently take the exclusion off
	// (the same trap limiterPath exists to document). The method is a property
	// of the request, and cannot drift.
	//
	// It costs nothing measurable. Every large payload this API produces is a
	// GET — the ~709 KB route catalogue, the {items,total} listings, metrics,
	// and the embedded SPA. No mutating endpoint returns a large body:
	// POST /reports/generate, the biggest candidate, answers with run METADATA
	// and leaves the rendered HTML to GET /reports/runs/:id/html.
	//
	// HEAD is swept up by the same comparison (compress's shouldSkip would
	// drop it too, but it never gets that far).
	if c.Method() != fiber.MethodGet {
		return true
	}

	// Rule 2 — never compress anything under the refresh cookie's own Path.
	//
	// Rule 1 guards the SECRET half of the BREACH precondition. This guards
	// the CAUSATION half: this prefix is the exact scope in which a browser
	// attaches a credential without the caller asking, and therefore the only
	// region of the API where an attacker could ever provoke a response they
	// cannot themselves read. The boundary is not a judgement call — it IS
	// handlers.RefreshCookiePath, the same constant, not a copy of it.
	//
	// Stated plainly: as the handlers stand today this rule is REDUNDANT with
	// rule 1. Every credential-bearing response is a POST or a PUT, and no
	// safe-method endpoint under this prefix returns a secret: /auth/me is a
	// profile, /auth/sessions omits token hashes, /auth/totp/status,
	// /auth/setup-status and /auth/sso-status are booleans, /auth/oidc/
	// authorize redirects to the IdP, and /auth/oidc/callback is a 302 whose
	// Location carries a 5-second exchange code — not a token in a body.
	//
	// The redundancy is the point, and is why it is kept rather than trimmed:
	// the two rules guard independent properties, so a future GET under this
	// prefix that does return a token — a /auth/session that re-issues, say —
	// would be compressed the day it lands with nothing to catch it. If the
	// cookie's Path is ever widened, this must widen with it;
	// TestCompression_AuthPathsAreAllExcluded pins that every rate-limited
	// auth path this file already tracks stays covered.
	//
	// Bandwidth cost is nil: these are sub-kilobyte responses, and the
	// busiest of them (/auth/refresh) fires once per session per ~14 minutes.
	//
	// limiterPath rather than c.Path(), for exactly the reason spelled out on
	// limiterPath: Fiber routes on a lowercased, slash-trimmed path, so a GET
	// of "/API/v1/auth/me/" reaches the handler while a raw comparison misses
	// it. The bare "/api/v1/auth" is named separately because the prefix's
	// trailing slash would not match it — it routes nowhere today, and the
	// point is that it would not need revisiting if it ever did.
	p := limiterPath(c)
	return p == authCookieScope || strings.HasPrefix(p, authCookieScopePrefix)
}

// authCookieScopePrefix names the part of the API the refresh cookie is scoped
// to, and therefore the only part a browser will authenticate automatically.
// It is handlers.RefreshCookiePath itself — the Path attribute the cookie is
// actually issued with — rather than a literal that matches it today.
//
// That indirection is the point. This value and the cookie's Path have to be
// the same string or the compression exclusion stops mirroring the credential
// it exists to protect, and until now a comment was the only thing holding
// them together. The trailing slash is the part that bites: RFC 6265 §5.1.4
// makes "/api/v1/auth" match a neighbour like "/api/v1/auth-debug", so
// dropping it would widen both the cookie AND this exclusion onto a path that
// is not auth at all. Deriving the value means it cannot drift here without
// also drifting on the wire, where TestRefreshCookie_PathIsTheExportedScope
// sees it; TestCompressionSkipped_Decisions pins that the exclusion does not
// reach that neighbour, and TestCompressionExclusion_TracksTheRefreshCookiePath
// pins that it is still derived rather than re-copied.
//
// The direction is forced: internal/api imports internal/api/handlers, so the
// constant has to be exported from handlers and read here. Exporting it the
// other way round would be an import cycle.
const authCookieScopePrefix = handlers.RefreshCookiePath

// authCookieScope is the same subtree without the trailing slash — needed
// because limiterPath has already stripped trailing slashes by the time it is
// compared, so the bare subtree root has to be matched by equality rather than
// by the prefix. Derived, not written out, for the reason above.
var authCookieScope = strings.TrimSuffix(authCookieScopePrefix, "/")

// clusterCreateLimiter caps POST /api/v1/clusters at 10/min/IP.
//
// That endpoint is the one place Nexara can be handed a Proxmox password, and
// in bootstrap mode every call spends a real /access/ticket attempt against the
// operator's hypervisor. Without a dedicated cap the general limiter's much
// larger budget would make Nexara a convenient password-spraying proxy onto a
// cluster it is trusted to reach.
//
// Attached to the ROUTE rather than via app.Use, for two reasons:
//
//   - A path-matching Next() cannot be written safely at app level. Fiber routes
//     on a lowercased, slash-trimmed path (CaseSensitive and StrictRouting are
//     both false), so "POST /API/v1/Clusters" reaches this handler while a
//     comparison against "/api/v1/clusters" does not match it — the limiter
//     would be skipped by a request that still spends a login attempt. (The
//     slash-trimmed spellings of a POST no longer reach the handler at all:
//     refuseTrailingSlashWrites answers them first. The case-folded ones do.)
//   - App-level middleware runs before the group's authRequired, so anonymous
//     traffic could drain the bucket and lock legitimate onboarding out.
//     Behind a proxy with TRUSTED_PROXIES unset every client shares one bucket,
//     which makes that a one-line denial of service.
//
// Per-IP rather than per-user because the limiter still runs before the handler
// resolves the actor; manage:cluster is enforced inside Create.
func (s *Server) clusterCreateLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":cluster-create"
		},
	})
}

// veeamConnectLimiter caps the Veeam endpoints that spend a real logon at
// 10/min/IP — create, update and test.
//
// Same reasoning as clusterCreateLimiter, and a worse target: every one of
// these calls performs an OAuth2 password grant against a VBR server that is
// usually Active-Directory-backed, with a `DOMAIN\user` credential. Under the
// general limiter's budget an authenticated manage:veeam holder could spray
// hundreds of domain logons a minute through Nexara's IP, and repeated /test
// calls against a stored account are a one-liner lockout DoS.
//
// Attached to the route rather than app-level, for the reasons spelled out on
// clusterCreateLimiter: a path-matching Next() cannot be written safely, and
// app-level middleware runs before authRequired.
func (s *Server) veeamConnectLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":veeam-connect"
		},
	})
}

// veeamControlLimiter caps the Veeam job-control endpoints at 30/min/IP.
//
// A SEPARATE instance from veeamConnectLimiter, not a shared one, and that is
// the deliberate opposite of the note on the routes that share theirs. Job
// control is a routine action an operator repeats — start, watch, stop, read
// the log — while add/edit/test are one-offs. Sharing one 10/min budget would
// let an afternoon of ordinary job control lock an operator out of registering
// a server, and would let a fumbled add-server flow block a stop.
//
// Higher than 10 for the same reason, and still bounded: every one of these
// calls mints its own OAuth2 password grant against a domain-backed VBR
// server, so an unbounded route here is the same domain-logon spraying
// primitive veeamConnectLimiter exists to cap.
func (s *Server) veeamControlLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":veeam-control"
		},
	})
}

// fingerprintFetchLimiter caps POST /api/v1/clusters/fetch-fingerprint at
// 30/min/IP.
//
// The endpoint dials an arbitrary caller-supplied host to read its
// certificate. It spends no credential, so it is not the spraying risk
// clusterCreateLimiter guards — but under the general limiter it was 600
// outbound TLS probes a minute, and the 200-vs-502 split is a clean
// "is something listening here" oracle. It is shared by the cluster, PBS and
// Veeam add-flows, so the cap is looser than the 10/min on the creates it
// precedes: a human fumbling the certificate step must not lock themselves
// out of the create that follows it.
func (s *Server) fingerprintFetchLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":fetch-fingerprint"
		},
	})
}

// limiterPath returns the request path the way Fiber ROUTED it, which is the
// only spelling a path-matching limiter can safely compare against.
//
// Fiber builds a lowercased, slash-trimmed detectionPath and matches routes on
// that, while c.Path() returns the raw path — CaseSensitive and StrictRouting
// are both false here. A limiter gating on the raw path therefore steps aside
// for spellings that still reach the handler:
//
//	GET /api/v1/auth/oidc/authorize/   the auth limiter's exact match misses
//	                                   it, and the general limiter's
//	                                   "/api/v1/auth/" prefix still matches, so
//	                                   it is skipped too — the OIDC flow ends
//	                                   up with NO rate limit at all
//	POST /API/v1/auth/login            auth limiter skipped; the general
//	                                   limiter applies its far larger budget
//	                                   instead
//
// A write spelled with a trailing slash — POST /api/v1/auth/login/, the
// spelling that once left login with no limit at all — no longer reaches its
// handler: refuseTrailingSlashWrites refuses it. The limiters run ahead of
// that gate and still count it, which TestAuthLimiterCannotBeSpelledAround
// pins.
//
// Route-attached limiters (see clusterCreateLimiter) do not need this, because
// matching is Fiber's job by then.
// The three auth-facing rate limiters select their traffic by PATH rather
// than by route, because they are mounted app-wide with Use rather than on
// each route. That works — every request passes through them, registry and
// legacy alike — but it means the limiter and the route agree only by
// spelling, and nothing about a route's declaration mentions the cap that
// protects it.
//
// Hoisting the paths out of the closures is what makes that agreement
// checkable: TestGuard_RateLimitedPathsAreRegisteredRoutes asserts every
// entry below still names a route the server mounts, so renaming or
// reshaping one of these paths fails the build instead of silently taking
// its brute-force cap off. The comparison uses the same normalisation
// limiterPath applies, since that is what the closures compare against.
var (
	// authLimitedPaths are the login and TOTP-code paths capped at 15
	// attempts per minute per IP.
	//
	// The last two are the OIDC flow, which is unauthenticated and does real
	// work on every call: /authorize performs an outbound discovery fetch to
	// the IdP and writes a 10-minute Redis state key. Everything under
	// /api/v1/auth/ is exempt from the general limiter, so without them an
	// anonymous loop can pin the server on outbound HTTP, hammer the
	// operator's IdP, and grow the Redis instance that also holds sessions.
	authLimitedPaths = map[string]bool{
		"/api/v1/auth/login":                          true,
		"/api/v1/auth/register":                       true,
		"/api/v1/auth/totp/verify-login":              true,
		"/api/v1/auth/totp":                           true,
		"/api/v1/auth/totp/recovery-codes/regenerate": true,
		"/api/v1/auth/oidc/authorize":                 true,
		"/api/v1/auth/oidc/callback":                  true,
	}
)

const (
	// refreshLimitedPath is capped separately, at 30/min/IP.
	refreshLimitedPath = "/api/v1/auth/refresh"

	// wsTokenLimitedPath is capped separately, at 60/min/IP.
	wsTokenLimitedPath = "/api/v1/auth/ws-token"
)

func limiterPath(c fiber.Ctx) string {
	p := strings.ToLower(c.Path())
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// authRequired returns middleware that rejects unauthenticated requests.
func (s *Server) authRequired() fiber.Handler {
	return func(c fiber.Ctx) error {
		if s.jwtService == nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Auth not configured")
		}

		token := extractBearerToken(c)
		if token == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Missing authorization token")
		}

		// API key tokens start with "nxra_".
		if strings.HasPrefix(token, "nxra_") {
			if err := s.authenticateAPIKey(c, token); err != nil {
				return err
			}
			return c.Next()
		}

		claims, err := s.jwtService.ValidateAccessToken(token)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired token")
		}

		// Scoped console tokens are single-purpose: they ONLY authorize a
		// specific WebSocket upgrade. Reject them at the regular API boundary
		// so a leaked console token cannot be used to call other endpoints.
		if claims.ConsoleScope != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Console-scoped token cannot be used for API requests")
		}
		// Same logic for WS-hub-scoped tokens — they only authorize the
		// /ws upgrade, never an API request.
		if claims.WSScope != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "WS-scoped token cannot be used for API requests")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)

		if s.rbacEngine != nil {
			c.Locals("rbac_engine", s.rbacEngine)
		}

		return c.Next()
	}
}

// authOptional returns middleware that extracts auth info if present, but doesn't require it.
func (s *Server) authOptional() fiber.Handler {
	return func(c fiber.Ctx) error {
		if s.jwtService == nil {
			return c.Next()
		}

		token := extractBearerToken(c)
		if token == "" {
			return c.Next()
		}

		// API key tokens start with "nxra_".
		if strings.HasPrefix(token, "nxra_") {
			// Best-effort: if API key auth fails, continue unauthenticated.
			if err := s.authenticateAPIKey(c, token); err != nil {
				return c.Next()
			}
			return c.Next()
		}

		claims, err := s.jwtService.ValidateAccessToken(token)
		if err != nil {
			return c.Next()
		}

		// Scoped console / WS-hub tokens must not be treated as general-purpose auth.
		if claims.ConsoleScope != nil || claims.WSScope != "" {
			return c.Next()
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)

		return c.Next()
	}
}

// extractBearerToken extracts the JWT from the Authorization header.
func extractBearerToken(c fiber.Ctx) string {
	header := c.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// authenticateAPIKey validates an nxra_ prefixed API key token and sets
// the user identity in Fiber locals. Returns an error on failure.
func (s *Server) authenticateAPIKey(c fiber.Ctx, token string) error {
	if s.queries == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Database not configured")
	}

	keyHash := auth.HashToken(token)
	row, err := s.queries.GetAPIKeyByHash(c.Context(), keyHash)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired API key")
	}
	if !row.UserIsActive {
		return fiber.NewError(fiber.StatusUnauthorized, "User account is inactive")
	}

	c.Locals("user_id", row.UserID)
	c.Locals("email", row.UserEmail)
	c.Locals("role", row.UserRole)
	c.Locals("auth_method", "api_key")
	c.Locals("api_key_id", row.ID)

	if s.rbacEngine != nil {
		c.Locals("rbac_engine", s.rbacEngine)
	}

	// Update last_used asynchronously to avoid adding latency.
	keyID := row.ID
	ip := c.IP()
	if len(ip) > 45 { // max IPv6 length
		ip = ip[:45]
	}
	go func() { //nolint:gosec // G118: intentionally detached — async last_used update must outlive the request (Fiber recycles the request context)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if updateErr := s.queries.UpdateAPIKeyLastUsed(ctx, db.UpdateAPIKeyLastUsedParams{
			ID:         keyID,
			LastUsedIp: pgtype.Text{String: ip, Valid: ip != ""},
		}); updateErr != nil {
			slog.Default().Warn("failed to update API key last_used", "error", updateErr)
		}
	}()

	return nil
}
