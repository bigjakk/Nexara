package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
)

// compressTestPaths are the extra routes the compression tests mount on the
// stub server. They exist because the three shapes the middleware
// discriminates between cannot all be reached on a DB-less stub server:
// there is exactly one real endpoint big enough to compress
// (GET /api/v1/api-docs), and it is a safe method outside the auth scope, so
// it can only ever demonstrate the ALLOW branch.
//
// Each returns the same payload, so a size comparison between them is a
// comparison of the middleware's decision and nothing else.
const (
	compressProbeAllowed = "/api/v1/compress-probe"      // safe method, outside the cookie scope → compressed
	compressProbeAuth    = "/api/v1/auth/compress-probe" // safe method, inside the cookie scope → not compressed
)

// compressProbePayload is a few KB of realistic, compressible JSON: the
// {items,total} envelope every listing in this API returns. Deliberately not
// a repeated single byte — that compresses ~1000:1 and would let a broken
// middleware look like a working one.
func compressProbePayload(t *testing.T) []byte {
	t.Helper()
	type item struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Node    string `json:"node"`
		Status  string `json:"status"`
		Tags    string `json:"tags"`
		Comment string `json:"comment"`
	}
	items := make([]item, 0, 40)
	for i := range 40 {
		items = append(items, item{
			ID:      fmt.Sprintf("00000000-0000-0000-0000-%012d", i),
			Name:    fmt.Sprintf("linux%02d", i),
			Node:    fmt.Sprintf("pve-%02d", i%3+1),
			Status:  []string{"running", "stopped", "paused"}[i%3],
			Tags:    "production;managed;compress-probe",
			Comment: fmt.Sprintf("probe row %d for the response compression tests", i),
		})
	}
	body, err := json.Marshal(map[string]any{"items": items, "total": len(items)})
	if err != nil {
		t.Fatalf("marshal probe payload: %v", err)
	}
	if len(body) < 2048 {
		t.Fatalf("probe payload is only %d bytes; it must be comfortably above "+
			"fasthttp's 200-byte minCompressLen for these tests to mean anything", len(body))
	}
	return body
}

// newCompressTestServer builds a server with the full route table, the real
// middleware chain, and a working JWT service, plus the probe routes above.
//
// It does NOT reuse newTestServer: that one passes a nil pool, which leaves
// almost every handler nil and collapses the route table to a handful of
// endpoints — GET /api/v1/api-docs then renders 794 bytes instead of ~709 KB
// and there is nothing left worth compressing.
func newCompressTestServer(t *testing.T, compressionEnabled bool) (*Server, string) {
	t.Helper()

	cfg := &config.Config{
		APIPort:          8080,
		LogLevel:         "info",
		CORSAllowOrigins: "*",
		// Far above anything a test issues: a 429 from the general limiter
		// short-circuits above the compress middleware, which would make a
		// throttled request look like a correctly-skipped one.
		RateLimitMax:        1_000_000,
		RateLimitExpiration: time.Minute,
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     7 * 24 * time.Hour,
		JWTSecret:           "test-secret-key-for-testing-only",
		CompressionEnabled:  compressionEnabled,
	}

	s := newRouteStubServer(t)
	s.config = cfg
	s.jwtService = auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	s.app = fiber.New(buildFiberConfig(cfg))
	s.setupMiddleware()
	s.setupRoutes()
	// Mirrors New(): the docs handler walks the route table at request time,
	// so it must be wired after setupRoutes or GetDocs 500s.
	s.apiDocsHandler.SetApp(s.app)

	payload := compressProbePayload(t)
	send := func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(payload)
	}
	// Appended after setupRoutes, the way RegisterFrontend is in main: the
	// app-level middleware registered by setupMiddleware still wraps them.
	s.app.Get(compressProbeAllowed, send)
	s.app.Get(compressProbeAuth, send)
	s.app.Post(compressProbeAllowed, send)

	token, _, err := s.jwtService.GenerateAccessToken(uuid.New(), "probe@example.com", "admin")
	if err != nil {
		t.Fatalf("mint access token: %v", err)
	}
	return s, token
}

// compressResult is one probe of the middleware: what came back, and how big.
type compressResult struct {
	status       int
	encoding     string
	vary         string
	cacheControl string
	body         []byte
}

// doRequest issues one authenticated request against the in-process app.
//
// Crucially it uses app.Test, which reads the response with http.ReadResponse
// — that does NOT transparently decode Content-Encoding the way an
// http.Transport would. body is therefore the bytes on the wire, which is the
// only thing these tests can meaningfully measure.
func doRequest(t *testing.T, s *Server, method, path, token, acceptEncoding string) compressResult {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}

	resp, err := s.App().Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return compressResult{
		status:       resp.StatusCode,
		encoding:     resp.Header.Get("Content-Encoding"),
		vary:         resp.Header.Get("Vary"),
		cacheControl: resp.Header.Get("Cache-Control"),
		body:         body,
	}
}

// mustGunzip decompresses a gzip body, failing the test if it is not in fact
// a valid gzip stream. This is the assertion that separates "the header says
// gzip" from "the response really is gzip" — a middleware that set the header
// without transforming the body would pass the first and fail here.
func mustGunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("response is labelled Content-Encoding: gzip but is not a gzip stream: %v", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip body did not decompress cleanly: %v", err)
	}
	return out
}

// TestCompression_APIDocsIsCompressedAndRoundTrips is the headline case: the
// ~709 KB route catalogue that motivated adding compression at all.
//
// It asserts the three things that together mean "actually compressed", not
// merely "middleware registered": the wire body is a real gzip stream, it
// decompresses to exactly the uncompressed response, and it is dramatically
// smaller.
func TestCompression_APIDocsIsCompressedAndRoundTrips(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	plain := doRequest(t, s, http.MethodGet, "/api/v1/api-docs", token, "")
	if plain.status != http.StatusOK {
		t.Fatalf("uncompressed status = %d, want 200", plain.status)
	}
	if plain.encoding != "" {
		t.Fatalf("a client that sent no Accept-Encoding got Content-Encoding: %q", plain.encoding)
	}

	gz := doRequest(t, s, http.MethodGet, "/api/v1/api-docs", token, "gzip")
	if gz.status != http.StatusOK {
		t.Fatalf("gzip status = %d, want 200", gz.status)
	}
	if gz.encoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q — the response was not compressed", gz.encoding, "gzip")
	}
	if !bytes.Equal(mustGunzip(t, gz.body), plain.body) {
		t.Fatal("gunzipped body differs from the uncompressed response — compression changed the payload")
	}
	if len(gz.body) >= len(plain.body) {
		t.Fatalf("compressed body is %d bytes vs %d uncompressed — no saving at all",
			len(gz.body), len(plain.body))
	}

	t.Logf("GET /api/v1/api-docs: %d bytes uncompressed → %d bytes gzip (%.1f%% of original)",
		len(plain.body), len(gz.body), 100*float64(len(gz.body))/float64(len(plain.body)))
}

// TestCompression_WithoutAcceptEncodingIsUnchanged pins the contract that
// matters most for compatibility: a client that cannot decode must never be
// handed encoded bytes. HTTP content negotiation is the escape hatch that
// makes turning this on safe for every existing API consumer.
func TestCompression_WithoutAcceptEncodingIsUnchanged(t *testing.T) {
	off, offToken := newCompressTestServer(t, false)
	on, onToken := newCompressTestServer(t, true)

	for _, path := range []string{"/api/v1/api-docs", compressProbeAllowed} {
		before := doRequest(t, off, http.MethodGet, path, offToken, "")
		after := doRequest(t, on, http.MethodGet, path, onToken, "")

		if after.encoding != "" {
			t.Errorf("%s: Content-Encoding = %q for a client that sent no Accept-Encoding", path, after.encoding)
		}
		if !bytes.Equal(before.body, after.body) {
			t.Errorf("%s: body changed (%d bytes without compression, %d with it) for a client "+
				"that never asked for an encoding", path, len(before.body), len(after.body))
		}
	}
}

// TestCompression_BrowserNegotiatesBrotli covers what a browser actually
// sends. fasthttp checks br before gzip, so every real SPA request takes the
// brotli path and the gzip test above never exercises it.
//
// The body is not decoded: andybalholm/brotli is an indirect dependency and
// importing it here would promote it in go.mod for one assertion. The header
// plus the size reduction is enough to prove the branch was taken.
func TestCompression_BrowserNegotiatesBrotli(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	plain := doRequest(t, s, http.MethodGet, "/api/v1/api-docs", token, "")
	br := doRequest(t, s, http.MethodGet, "/api/v1/api-docs", token, "gzip, deflate, br, zstd")

	if br.encoding != "br" {
		t.Fatalf("Content-Encoding = %q for a browser-style Accept-Encoding, want %q", br.encoding, "br")
	}
	if len(br.body) >= len(plain.body) {
		t.Fatalf("brotli body is %d bytes vs %d uncompressed — no saving at all", len(br.body), len(plain.body))
	}
	t.Logf("GET /api/v1/api-docs: %d bytes uncompressed → %d bytes brotli (%.1f%% of original)",
		len(plain.body), len(br.body), 100*float64(len(br.body))/float64(len(plain.body)))
}

// TestCompression_VaryAcceptEncodingIsSet guards a cache-poisoning shape
// rather than a bandwidth one.
//
// applyFrontendCacheHeaders marks /assets/* "public, max-age=31536000,
// immutable". Without Vary: Accept-Encoding a shared cache is entitled to
// hand that stored entry to the next client whatever it asked for — a
// gzipped bundle served to a client that cannot decode it, cached for a year.
func TestCompression_VaryAcceptEncodingIsSet(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	for _, ae := range []string{"", "gzip"} {
		got := doRequest(t, s, http.MethodGet, "/api/v1/api-docs", token, ae)
		// compress appends Vary on its skip paths too, so a 404 would still
		// satisfy the assertion below. Pin the status so this measures the
		// real response.
		if got.status != http.StatusOK {
			t.Fatalf("Accept-Encoding=%q: status = %d, want 200", ae, got.status)
		}
		if !strings.Contains(strings.ToLower(got.vary), "accept-encoding") {
			t.Errorf("Accept-Encoding=%q: Vary = %q, want it to name Accept-Encoding", ae, got.vary)
		}
	}
}

// TestCompression_SmallResponsesAreLeftAlone documents that no size floor is
// implemented here because fasthttp already has one (minCompressLen = 200).
//
// If a future upgrade dropped that floor this fails, which is the signal to
// add one rather than silently start gzipping 80-byte bodies into ~100-byte
// bodies.
func TestCompression_SmallResponsesAreLeftAlone(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	plain := doRequest(t, s, http.MethodGet, "/api/v1/version", token, "")
	// Asserted before the size gate, not after: a 404 envelope for this path
	// is ~60 bytes, which clears the "below the floor" gate on its own and
	// would let the test pass having measured a 404 instead of the endpoint.
	if plain.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the test would otherwise be measuring an error envelope", plain.status)
	}
	if len(plain.body) >= 200 {
		t.Skipf("the version response has grown to %d bytes and is no longer below "+
			"fasthttp's 200-byte floor; this test needs a different small endpoint", len(plain.body))
	}

	gz := doRequest(t, s, http.MethodGet, "/api/v1/version", token, "gzip")
	if gz.encoding != "" {
		t.Errorf("an %d-byte response was compressed (Content-Encoding: %q); compressing it "+
			"would make it larger", len(plain.body), gz.encoding)
	}
	if !bytes.Equal(gz.body, plain.body) {
		t.Error("the small response body changed when Accept-Encoding was offered")
	}
	t.Logf("GET /api/v1/version: %d bytes, unchanged with Accept-Encoding: gzip (below the 200-byte floor)",
		len(plain.body))
}

// TestCompression_AuthScopeIsNotCompressed proves rule 2 end to end: nothing
// under the refresh cookie's Path is compressed, however large it is.
//
// Both probes return byte-identical payloads and differ only in path, so a
// difference in outcome can only come from compressionSkipped.
func TestCompression_AuthScopeIsNotCompressed(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	want := compressProbePayload(t)

	allowed := doRequest(t, s, http.MethodGet, compressProbeAllowed, token, "gzip")
	if allowed.encoding != "gzip" {
		t.Fatalf("control probe %s was NOT compressed (Content-Encoding: %q) — the test cannot "+
			"distinguish the auth exclusion from compression being broken altogether",
			compressProbeAllowed, allowed.encoding)
	}
	if !bytes.Equal(mustGunzip(t, allowed.body), want) {
		t.Fatal("control probe did not round-trip")
	}

	authScoped := doRequest(t, s, http.MethodGet, compressProbeAuth, token, "gzip")
	if authScoped.encoding != "" {
		t.Errorf("%s was compressed (Content-Encoding: %q) — it is inside the refresh cookie's "+
			"Path, the one scope a browser authenticates automatically", compressProbeAuth, authScoped.encoding)
	}
	if !bytes.Equal(authScoped.body, want) {
		t.Errorf("%s body was transformed: %d bytes, want the %d-byte payload verbatim",
			compressProbeAuth, len(authScoped.body), len(want))
	}
}

// TestCompression_MutatingResponsesAreNotCompressed proves rule 1 end to end.
//
// Same path and same payload as the control in the previous test; only the
// method differs. Every response in this API that carries a credential
// answers a POST or a PUT.
func TestCompression_MutatingResponsesAreNotCompressed(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	want := compressProbePayload(t)

	get := doRequest(t, s, http.MethodGet, compressProbeAllowed, token, "gzip")
	if get.encoding != "gzip" {
		t.Fatalf("control GET %s was NOT compressed (Content-Encoding: %q) — the test cannot "+
			"distinguish the method rule from compression being broken altogether",
			compressProbeAllowed, get.encoding)
	}

	post := doRequest(t, s, http.MethodPost, compressProbeAllowed, token, "gzip")
	if post.status != http.StatusOK {
		t.Fatalf("POST %s status = %d, want 200", compressProbeAllowed, post.status)
	}
	if post.encoding != "" {
		t.Errorf("POST %s was compressed (Content-Encoding: %q) — mutating responses are the ones "+
			"that carry credentials", compressProbeAllowed, post.encoding)
	}
	if !bytes.Equal(post.body, want) {
		t.Errorf("POST %s body was transformed: %d bytes, want the %d-byte payload verbatim",
			compressProbeAllowed, len(post.body), len(want))
	}
}

// TestCompression_AuthPathsAreAllExcluded ties the exclusion to the auth
// paths this package already tracks, so the two cannot drift apart.
//
// authLimitedPaths / refreshLimitedPath / wsTokenLimitedPath are themselves
// pinned to real routes by TestGuard_RateLimitedPathsAreRegisteredRoutes, so
// this transitively pins the exclusion against the router: adding a new
// brute-force-capped auth route that somehow escaped the cookie scope would
// fail here.
func TestCompression_AuthPathsAreAllExcluded(t *testing.T) {
	paths := make([]string, 0, 2+len(authLimitedPaths))
	paths = append(paths, refreshLimitedPath, wsTokenLimitedPath)
	for p := range authLimitedPaths {
		paths = append(paths, p)
	}
	if len(paths) < 3 {
		t.Fatalf("only %d auth paths to check; this test would pass by iterating nothing", len(paths))
	}

	app := fiber.New()
	for _, p := range paths {
		var got bool
		probe := p
		app.Get(probe, func(c fiber.Ctx) error {
			got = compressionSkipped(c)
			return c.SendString("ok")
		})
		req := httptest.NewRequest(http.MethodGet, probe, nil)
		if _, err := app.Test(req); err != nil {
			t.Fatalf("%s: %v", probe, err)
		}
		if !got {
			t.Errorf("compressionSkipped(%s) = false; every path that carries a credential in its "+
				"response body must be excluded from compression", probe)
		}
	}
}

// TestCompressionSkipped_Decisions is the unit-level table for the predicate,
// covering the spellings Fiber will still route but a naive string compare
// would miss — the trap limiterPath exists to document.
func TestCompressionSkipped_Decisions(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"safe method outside auth scope is compressed", http.MethodGet, "/api/v1/api-docs", false},
		{"root SPA shell is compressed", http.MethodGet, "/", false},
		{"hashed asset is compressed", http.MethodGet, "/assets/index-abc123.js", false},
		{"POST is skipped", http.MethodPost, "/api/v1/api-keys", true},
		{"PUT is skipped", http.MethodPut, "/api/v1/clusters/x/access/users/u/tokens/t", true},
		{"PATCH is skipped", http.MethodPatch, "/api/v1/settings/theme", true},
		{"DELETE is skipped", http.MethodDelete, "/api/v1/api-keys/x", true},
		{"auth scope is skipped", http.MethodGet, "/api/v1/auth/sessions", true},
		{"oidc callback is skipped", http.MethodGet, "/api/v1/auth/oidc/callback", true},
		{"auth scope with trailing slash is skipped", http.MethodGet, "/api/v1/auth/sessions/", true},
		{"auth scope upper-cased is skipped", http.MethodGet, "/API/V1/AUTH/sessions", true},
		{"bare auth scope is skipped", http.MethodGet, "/api/v1/auth", true},
		// The refresh cookie's Path carries a trailing slash precisely so it
		// cannot match a neighbour like /api/v1/auth-debug (RFC 6265 §5.1.4);
		// the exclusion must not be broader than the cookie it mirrors.
		{"auth-prefixed neighbour is NOT skipped", http.MethodGet, "/api/v1/auth-debug/status", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			var got, ran bool
			app.Add([]string{tc.method}, "/*", func(c fiber.Ctx) error {
				got, ran = compressionSkipped(c), true
				return c.SendString("ok")
			})
			if _, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil)); err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			// Without this, every want:false row passes whether or not the
			// handler was reached: `got` starts false and nothing else writes
			// it, so a route that stopped matching (app.Test reports transport
			// errors, not a 404) would look like a correct negative. The
			// want:false rows include the only assertion that the exclusion is
			// not over-broad, so that failure mode would be silent and total.
			if !ran {
				t.Fatalf("%s %s never reached the handler, so compressionSkipped was never called",
					tc.method, tc.path)
			}
			if got != tc.want {
				t.Errorf("compressionSkipped(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestCompression_DisabledByConfig pins COMPRESSION_ENABLED=false as a real
// off switch rather than a field nothing reads.
func TestCompression_DisabledByConfig(t *testing.T) {
	on, onToken := newCompressTestServer(t, true)
	off, offToken := newCompressTestServer(t, false)

	if got := doRequest(t, on, http.MethodGet, "/api/v1/api-docs", onToken, "gzip"); got.encoding != "gzip" {
		t.Fatalf("with compression enabled, Content-Encoding = %q, want %q — the off switch cannot "+
			"be tested against a server that never compresses", got.encoding, "gzip")
	}

	got := doRequest(t, off, http.MethodGet, "/api/v1/api-docs", offToken, "gzip")
	if got.encoding != "" {
		t.Errorf("with COMPRESSION_ENABLED=false, Content-Encoding = %q, want none", got.encoding)
	}
	if got.status != http.StatusOK {
		t.Errorf("status = %d, want 200", got.status)
	}
}

// TestCompression_ErrorEnvelopesAreNeverCompressed pins the claim made on the
// registration in setupMiddleware: no response rendered by the app-level
// ErrorHandler is ever compressed, wherever compress sits in the chain.
//
// The mechanism is that Fiber's compress middleware does
// `if err := c.Next(); err != nil { return err }` and skips its
// post-processing, while buildFiberConfig's ErrorHandler runs above the whole
// Use chain — so the body does not exist yet when compress has already bailed.
//
// The error message is deliberately long. A real 404 envelope is ~60 bytes,
// which fasthttp would decline to compress anyway; asserting on one would pass
// for the floor's reason rather than the error path's, and prove nothing.
func TestCompression_ErrorEnvelopesAreNeverCompressed(t *testing.T) {
	s, token := newCompressTestServer(t, true)

	long := strings.Repeat("this error message is long enough to clear the compression floor. ", 40)
	s.app.Get("/api/v1/compress-probe-error", func(_ fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, long)
	})

	got := doRequest(t, s, http.MethodGet, "/api/v1/compress-probe-error", token, "gzip")
	if got.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got.status)
	}
	if len(got.body) < 200 {
		t.Fatalf("error envelope is only %d bytes — below fasthttp's floor, so this test would "+
			"pass for the wrong reason", len(got.body))
	}
	if got.encoding != "" {
		t.Errorf("a %d-byte error envelope was compressed (Content-Encoding: %q); the ErrorHandler "+
			"path is supposed to be unreachable from the compress middleware", len(got.body), got.encoding)
	}
	if !strings.Contains(string(got.body), "bad_request") {
		t.Errorf("body is not the standard JSON error envelope: %s", got.body)
	}
	t.Logf("GET /api/v1/compress-probe-error: %d-byte error envelope, uncompressed", len(got.body))
}

// TestCompression_EmbeddedFrontendIsCompressed covers the other half of the
// win, and a genuinely different code path from every test above.
//
// The SPA is not served by a route handler — RegisterFrontend mounts Fiber's
// static middleware as a root catch-all AFTER setupRoutes, with its own
// ModifyResponse hook. This asserts the compress middleware still wraps it,
// and that the two headers that have to survive together actually do:
// Cache-Control: immutable (set by applyFrontendCacheHeaders) and Vary:
// Accept-Encoding. Without the second, a shared cache may hand a stored
// gzipped bundle to a client that cannot decode it — for a year.
func TestCompression_EmbeddedFrontendIsCompressed(t *testing.T) {
	s, _ := newCompressTestServer(t, true)

	// Big enough to clear fasthttp's 200-byte floor, and shaped like real
	// bundled JS rather than a repeated character.
	var bundle strings.Builder
	for i := range 60 {
		fmt.Fprintf(&bundle, "export function handler%d(a,b){return a*%d+b;}\n", i, i)
	}
	shell := "<!doctype html><title>nexara</title>" + strings.Repeat("<div class=\"app-root\"></div>", 20)

	s.RegisterFrontend(fstest.MapFS{
		"index.html":        {Data: []byte(shell)},
		"assets/app-abc.js": {Data: []byte(bundle.String())},
	})

	for _, tc := range []struct {
		name, path, wantCacheControl string
	}{
		{"hashed asset", "/assets/app-abc.js", "immutable"},
		{"SPA shell", "/", "no-cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain := doRequest(t, s, http.MethodGet, tc.path, "", "")
			if plain.status != http.StatusOK {
				t.Fatalf("status = %d, want 200", plain.status)
			}
			if len(plain.body) < 200 {
				t.Fatalf("fixture is only %d bytes — below fasthttp's floor, so this test "+
					"would pass without compressing anything", len(plain.body))
			}

			gz := doRequest(t, s, http.MethodGet, tc.path, "", "gzip")
			if gz.encoding != "gzip" {
				t.Fatalf("Content-Encoding = %q, want %q — the embedded frontend is not being compressed",
					gz.encoding, "gzip")
			}
			if !bytes.Equal(mustGunzip(t, gz.body), plain.body) {
				t.Fatal("gunzipped asset differs from the uncompressed response")
			}
			if !strings.Contains(strings.ToLower(gz.vary), "accept-encoding") {
				t.Errorf("Vary = %q, want it to name Accept-Encoding; a shared cache could otherwise "+
					"serve these bytes to a client that cannot decode them", gz.vary)
			}
			if !strings.Contains(gz.cacheControl, tc.wantCacheControl) {
				t.Errorf("Cache-Control = %q, want it to contain %q — compression must not displace "+
					"the caching headers applyFrontendCacheHeaders sets", gz.cacheControl, tc.wantCacheControl)
			}
			t.Logf("GET %s: %d bytes → %d bytes gzip (Cache-Control: %s)",
				tc.path, len(plain.body), len(gz.body), gz.cacheControl)
		})
	}
}

// TestCompression_WebSocketUpgradeStillWorks is the reason there is no /ws
// case in compressionSkipped.
//
// A successful upgrade leaves the response at 101 and Fiber's shouldSkip
// bails on any status below 200, so the library already excludes upgrades and
// an exclusion here would be noise. That is a claim about a dependency's
// internals, so it is pinned behaviourally instead: a real TCP listener, a
// real gorilla dial, with the compress middleware in the chain. If a Fiber
// upgrade ever changed shouldSkip, this fails here rather than in production
// consoles.
func TestCompression_WebSocketUpgradeStillWorks(t *testing.T) {
	s, _ := newCompressTestServer(t, true)

	s.app.Get("/ws/compress-probe", websocket.New(func(c *websocket.Conn) {
		_ = c.WriteMessage(gorillaws.TextMessage, []byte("welcome"))
		// Block until the peer goes away so the handler does not return (and
		// close the socket) before the client has read.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() { _ = s.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = s.app.Shutdown() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probe, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			probe.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	dialer := *gorillaws.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second
	// The dialer sends Accept-Encoding by default on the handshake, which is
	// what makes this a test of compression rather than of WebSockets.
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", port), Path: "/ws/compress-probe"}

	conn, resp, err := dialer.Dial(u.String(), http.Header{"Accept-Encoding": []string{"gzip, deflate, br"}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("WebSocket upgrade failed with compression enabled: %v (status %d)", err, status)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("the 101 response carries Content-Encoding: %q — a hijacked connection must not be encoded", enc)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read over the upgraded connection: %v", err)
	}
	if string(msg) != "welcome" {
		t.Errorf("first frame = %q, want %q — the WebSocket stream was altered", msg, "welcome")
	}
}
