package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// testEncryptionKey is a valid 32-byte hex key for tests.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newClusterTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewClusterHandler(nil, testEncryptionKey, nil)

	app := fiber.New(fiber.Config{
		ErrorHandler: testErrorHandler,
	})

	// Inject role + user_id from X-Test-Role, then wire the stub
	// permissionEngine so the production gate path runs end-to-end.
	app.Use(func(c *fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Post("/clusters", handler.Create)
	app.Get("/clusters", handler.List)
	app.Get("/clusters/:id", handler.Get)
	app.Put("/clusters/:id", handler.Update)
	app.Delete("/clusters/:id", handler.Delete)

	return app
}

func testErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}
	return c.Status(code).JSON(fiber.Map{
		"error":   code,
		"message": message,
	})
}

func TestClusterCreate_MissingFields(t *testing.T) {
	app := newClusterTestApp(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing name", `{"api_url":"https://pve.example.com:8006","token_id":"user@pam!token","token_secret":"sec"}`},
		{"missing api_url", `{"name":"test","token_id":"user@pam!token","token_secret":"sec"}`},
		{"missing token_id", `{"name":"test","api_url":"https://pve.example.com:8006","token_secret":"sec"}`},
		{"missing token_secret", `{"name":"test","api_url":"https://pve.example.com:8006","token_id":"user@pam!token"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", "admin")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, body)
			}
		})
	}
}

func TestClusterCreate_InvalidJSON(t *testing.T) {
	app := newClusterTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestClusterCreate_InvalidURL(t *testing.T) {
	app := newClusterTestApp(t)

	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "pve.example.com:8006"},
		{"no host", "https://"},
		{"http scheme", "http://pve.example.com:8006"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"name":         "test",
				"api_url":      tt.url,
				"token_id":     "user@pam!token",
				"token_secret": "secret",
			})
			req := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", "admin")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusBadRequest {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, respBody)
			}
		})
	}
}

func TestClusterGet_InvalidUUID(t *testing.T) {
	app := newClusterTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/clusters/not-a-uuid", nil)
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestCluster_NonAdminDenied(t *testing.T) {
	app := newClusterTestApp(t)

	// GET /clusters now returns an empty filtered list rather than 403 — the
	// per-row scope check in handlers/clusters.go::List drops every cluster
	// when the caller has no scope. The other write/read-by-id paths still
	// 403 because requireClusterPerm rejects non-admin callers via the
	// stub permissionEngine (engine wired, role=user has no permissions).
	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/clusters"},
		{http.MethodGet, "/clusters/" + uuid.New().String()},
		{http.MethodPut, "/clusters/" + uuid.New().String()},
		{http.MethodDelete, "/clusters/" + uuid.New().String()},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var body io.Reader
			if ep.method == http.MethodPost || ep.method == http.MethodPut {
				body = bytes.NewBufferString(`{}`)
			}
			req := httptest.NewRequest(ep.method, ep.path, body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", "user")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusForbidden {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusForbidden, respBody)
			}
		})
	}
}

func TestClusterUpdate_InvalidUUID(t *testing.T) {
	app := newClusterTestApp(t)

	req := httptest.NewRequest(http.MethodPut, "/clusters/bad-uuid", bytes.NewBufferString(`{"name":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestClusterDelete_InvalidUUID(t *testing.T) {
	app := newClusterTestApp(t)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/bad-uuid", nil)
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
