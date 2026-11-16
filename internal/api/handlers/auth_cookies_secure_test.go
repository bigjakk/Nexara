package handlers

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestRefreshCookieSecure_AutoFollowsTheScheme: under SECURE_COOKIES=auto the refresh
// cookie is Secure for TLS Nexara ends and for https from a trusted proxy, and not for
// plain http or a forwarded https no trusted proxy vouches for.
func TestRefreshCookieSecure_AutoFollowsTheScheme(t *testing.T) {
	t.Parallel()
	newApp := func(trusted bool) *fiber.App {
		cfg := fiber.Config{}
		if trusted {
			cfg.TrustProxy, cfg.TrustProxyConfig = true, fiber.TrustProxyConfig{Proxies: []string{"0.0.0.0", "127.0.0.1"}}
		}
		app := fiber.New(cfg)
		app.Get("/", func(c fiber.Ctx) error {
			if refreshCookieSecure(c) {
				return c.SendString("secure")
			}
			return c.SendString("plain")
		})
		return app
	}
	viaTest := func(app *fiber.App, forwarded string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if forwarded != "" {
			req.Header.Set("X-Forwarded-Proto", forwarded)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return readBody(t, resp.Body)
	}

	// TLS ended by Nexara: a real TLS listener, with the test server's certificate.
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: srv.TLS.Certificates, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	tlsApp := newApp(false)
	go func() { _ = tlsApp.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = tlsApp.Shutdown() })
	resp, err := srv.Client().Get("https://" + ln.Addr().(*net.TCPAddr).String() + "/")
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string][2]string{
		"TLS ended by Nexara":                  {readBody(t, resp.Body), "secure"},
		"https from a trusted proxy":           {viaTest(newApp(true), "https"), "secure"},
		"https claimed by an untrusted client": {viaTest(newApp(false), "https"), "plain"},
		"plain http":                           {viaTest(newApp(false), ""), "plain"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: the refresh cookie is %s, want %s", name, got[0], got[1])
		}
	}
}
