package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nexapp "github.com/bigjakk/nexara/internal/app"
	"github.com/bigjakk/nexara/internal/config"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		APIPort:             8080,
		LogLevel:            "info",
		CORSAllowOrigins:    "*",
		RateLimitMax:        100,
		RateLimitExpiration: time.Minute,
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     7 * 24 * time.Hour,
		// Matches the envconfig default. A Config built as a struct literal
		// bypasses envconfig entirely, so every defaulted field has to be
		// restated or the stub server runs in a shape production never has —
		// here, with the compress middleware absent from the chain.
		CompressionEnabled: true,
	}
	// Nil pool and nil Redis: app.New leaves every DB- and Redis-backed
	// singleton nil, which is the same degraded shape the server has always
	// been constructed with in tests.
	return New(nexapp.New(context.Background(), cfg, nil, nil, slog.Default()))
}

func TestHealthEndpoint_ReturnsOK(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestVersionEndpoint_ReturnsJSON(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	var result versionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result.Version == "" {
		t.Error("version should not be empty")
	}
	if result.GoVersion == "" {
		t.Error("go_version should not be empty")
	}
}

func TestRequestID_InResponse(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	rid := resp.Header.Get("X-Request-Id")
	if rid == "" {
		t.Error("X-Request-Id header should be present")
	}
}

func TestNotFound_ReturnsErrorEnvelope(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body, _ := io.ReadAll(resp.Body)
	var result ErrorResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.Error != "not_found" {
		t.Errorf("error = %q, want %q", result.Error, "not_found")
	}
}

func TestCORS_Preflight(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/version", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao == "" {
		t.Error("Access-Control-Allow-Origin header should be present")
	}
}
