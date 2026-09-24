package api

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/config"
)

// bareErrorServer serves an app with the server's Fiber config and New's
// SecureErrorLogMessage, and whatever mount adds to it. With nothing added it
// is the precondition twin of the server New builds for
// redactServerErrorBodies: its fasthttp errors go through Fiber's own handler,
// App.serverErrorHandler, and errorHandler, without the redaction.
func bareErrorServer(t *testing.T, mount ...func(*fiber.App)) string {
	t.Helper()
	app := fiber.New(buildFiberConfig(&config.Config{}))
	app.Server().SecureErrorLogMessage = true
	for _, m := range mount {
		m(app)
	}
	return serveAppOnLoopback(t, app)
}

// TestServerErrorsAnswerWithoutTheRequest sends, for each way a request head
// makes fasthttp quote its own bytes in the error it answers with, a head
// carrying canaries exactly where the quote falls — the refresh cookie and a
// bearer token among them — and holds the server New builds to answering with
// none of them: the error envelope with the status's slug and reason phrase,
// and nothing else.
//
// Each case first shows the canaries in the answer of the twin without the
// redaction, where fasthttp's error text reaches the client as the envelope's
// message. That is what makes the case a quote of the request rather than a
// head the server answers some other way. TestFiberSentErrorStatusesCarryTheirSlug
// holds the statuses Fiber classifies from the error's own text — 408, 431 and
// 501 — which the redaction must leave as they are.
func TestServerErrorsAnswerWithoutTheRequest(t *testing.T) {
	const host = "Host: example.com\r\n"
	const get = "GET /api/v1/version HTTP/1.1\r\n"
	for _, tt := range []struct {
		name     string
		raw      string
		canaries []string
	}{
		{"a request line with no HTTP version, which quotes the whole head",
			"GET /api/v1/version\r\n" + host + "Cookie: nexara_refresh=canary-cookie-0001\r\n" +
				"Authorization: Bearer canary-bearer-0001\r\n\r\n",
			[]string{"canary-cookie-0001", "canary-bearer-0001"}},
		{"a control byte in the Cookie value",
			get + host + "Cookie: nexara_refresh=canary-cookie-0002\x01\r\n\r\n", []string{"canary-cookie-0002"}},
		{"an invalid byte in a header name, which quotes the line",
			get + host + "Coo\x01kie: nexara_refresh=canary-cookie-0003\r\n\r\n", []string{"canary-cookie-0003"}},
		{"a header line with no colon",
			get + host + "Cookie nexara_refresh=canary-cookie-0004\r\n\r\n", []string{"canary-cookie-0004"}},
		{"a control byte in the request URI's query",
			"GET /api/v1/version?token=canary-query-0005\x01 HTTP/1.1\r\n" + host + "\r\n", []string{"canary-query-0005"}},
		{"a Host it cannot parse", get + "Host: canary-host-0006[x\r\n\r\n", []string{"canary-host-0006"}},
		{"a Host whose port it cannot parse", get + "Host: example.com:canary-port-0007\r\n\r\n",
			[]string{"canary-port-0007"}},
		{"an HTTP version it does not know", "GET /api/v1/version HTTP/1.1canary-version-0008\r\n" + host + "\r\n",
			[]string{"canary-version-0008"}},
		{"a header name ending in a space", get + host + "X-Canary-Name-0009 : v\r\n\r\n",
			[]string{"X-Canary-Name-0009"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ans := exchange(t, bareErrorServer(t), tt.raw)
			if ans.status != fiber.StatusBadRequest {
				t.Fatalf("precondition: the twin answered %d (%s), want fasthttp's 400", ans.status, ans.body)
			}
			for _, canary := range tt.canaries {
				if !strings.Contains(string(ans.body), canary) {
					t.Fatalf("precondition: without the redaction the answer does not quote %q (%s); the case does "+
						"not reach a quoting error", canary, ans.body)
				}
			}

			ans = exchange(t, serveOnLoopback(t, newAssembledServer(t)), tt.raw)
			if ans.status != fiber.StatusBadRequest {
				t.Fatalf("status %d (%s), want fasthttp's 400", ans.status, ans.body)
			}
			for _, canary := range tt.canaries {
				if strings.Contains(string(ans.body), canary) {
					t.Errorf("the answer quotes %q from the request: %s", canary, ans.body)
				}
			}
			if want := `{"error":"bad_request","message":"Bad Request"}`; string(ans.body) != want {
				t.Errorf("body %s, want %s", ans.body, want)
			}
			if ans.contentType != fiber.MIMEApplicationJSONCharsetUTF8 {
				t.Errorf("Content-Type %q, want JSON", ans.contentType)
			}
			if !ans.connClose || !ans.closed {
				t.Errorf("Connection: close %v, closed %v: fasthttp closes the connection after its error", ans.connClose,
					ans.closed)
			}
		})
	}
}

// TestServerErrorBodyDropsTheContentEncodingItReplaces holds the redaction to
// removing a Content-Encoding the middleware left on the answer: Fiber's
// handler runs the Use chain before errorHandler renders the error, and
// errorHandler sets a body without touching that header, so the twin's JSON
// goes out labelled with a coding it is not in. The middleware here sets the
// header itself, as the compress middleware would for a body it compressed.
func TestServerErrorBodyDropsTheContentEncodingItReplaces(t *testing.T) {
	encodes := func(app *fiber.App) {
		app.Use(func(c fiber.Ctx) error {
			c.Set(fiber.HeaderContentEncoding, "gzip")
			return c.Next()
		})
	}
	const raw = "GET /api/v1/version HTTP/1.1\r\nHost: example.com\r\nX-Pad nothing\r\n\r\n"
	contentEncoding := func(t *testing.T, addr string) string {
		t.Helper()
		ans := exchange(t, addr, raw)
		if ans.status != fiber.StatusBadRequest || ans.envelope.Error != "bad_request" {
			t.Fatalf("status %d (%s), want fasthttp's 400 in the error envelope", ans.status, ans.body)
		}
		return ans.contentEncoding
	}
	if got := contentEncoding(t, bareErrorServer(t, encodes)); got != "gzip" {
		t.Fatalf("precondition: without the redaction the answer's Content-Encoding is %q, want the middleware's gzip",
			got)
	}
	if got := contentEncoding(t, bareErrorServer(t, encodes, redactServerErrorBodies)); got != "" {
		t.Errorf("the answer carries Content-Encoding %q over a body in none", got)
	}
}

// TestServerErrorBodyIsTheEnvelopeWhateverFiberRendered holds the redaction
// to not relying on errorHandler: with Fiber's default error handler, which
// answers in plain text with the error's own text, the answer is still the
// JSON envelope and nothing of the request. The twin, the same app without the
// redaction, quotes the canary in plain text.
func TestServerErrorBodyIsTheEnvelopeWhateverFiberRendered(t *testing.T) {
	const canary = "canary-cookie-0010"
	const raw = "GET /api/v1/version\r\nHost: example.com\r\nCookie: nexara_refresh=" + canary + "\r\n\r\n"
	serve := func(t *testing.T, redacted bool) string {
		t.Helper()
		app := fiber.New()
		app.Server().SecureErrorLogMessage = true
		if redacted {
			redactServerErrorBodies(app)
		}
		return serveAppOnLoopback(t, app)
	}
	ans := exchange(t, serve(t, false), raw)
	if ans.status != fiber.StatusBadRequest || !strings.HasPrefix(ans.contentType, "text/plain") ||
		!strings.Contains(string(ans.body), canary) {
		t.Fatalf("precondition: status %d, Content-Type %q, body %q; want Fiber's default handler's plain-text 400 "+
			"quoting the cookie", ans.status, ans.contentType, ans.body)
	}
	ans = exchange(t, serve(t, true), raw)
	if want := `{"error":"bad_request","message":"Bad Request"}`; ans.status != fiber.StatusBadRequest ||
		string(ans.body) != want || ans.contentType != fiber.MIMEApplicationJSONCharsetUTF8 {
		t.Errorf("status %d, Content-Type %q, body %s; want 400, JSON, %s", ans.status, ans.contentType, ans.body, want)
	}
}
