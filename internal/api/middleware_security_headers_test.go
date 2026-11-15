package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/config"
)

// newHeadersProbe is the production middleware stack in front of an API route and
// a page route, each answering 200.
func newHeadersProbe(t *testing.T) *fiber.App {
	t.Helper()
	cfg := &config.Config{RateLimitMax: 1_000_000, RateLimitExpiration: time.Minute}
	s := &Server{config: cfg, app: fiber.New(buildFiberConfig(cfg))}
	s.setupMiddleware()
	ok := func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	s.app.Get("/api/v1/probe", ok)
	s.app.Get("/dashboard", ok)
	return s.app
}

// TestSecurityHeaders_CrossOriginOpenerPolicy: a cross-origin window that opened
// ours, or that ours opened, gets no handle on it.
func TestSecurityHeaders_CrossOriginOpenerPolicy(t *testing.T) {
	t.Parallel()
	app := newHeadersProbe(t)
	for _, target := range []string{"/api/v1/probe", "/dashboard"} {
		status, header, _ := sendFull(t, app, authedRequest(http.MethodGet, target))
		if got := header.Get("Cross-Origin-Opener-Policy"); status != http.StatusOK || got != "same-origin" {
			t.Errorf("GET %s: status %d, Cross-Origin-Opener-Policy = %q, want 200 and same-origin", target, status, got)
		}
	}
}
