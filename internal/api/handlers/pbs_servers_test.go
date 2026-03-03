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

func newPBSTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewPBSHandler(nil, testEncryptionKey)

	app := fiber.New(fiber.Config{
		ErrorHandler: testErrorHandler,
	})

	app.Use(func(c *fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})

	app.Post("/pbs-servers", handler.Create)
	app.Get("/pbs-servers", handler.List)
	app.Get("/pbs-servers/:id", handler.Get)
	app.Put("/pbs-servers/:id", handler.Update)
	app.Delete("/pbs-servers/:id", handler.Delete)
	app.Get("/clusters/:cluster_id/pbs-servers", handler.ListByCluster)

	return app
}

func TestPBSCreate_MissingFields(t *testing.T) {
	app := newPBSTestApp(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing name", `{"api_url":"https://pbs.example.com:8007","token_id":"user@pam!token","token_secret":"secret"}`},
		{"missing api_url", `{"name":"test","token_id":"user@pam!token","token_secret":"secret"}`},
		{"missing token_id", `{"name":"test","api_url":"https://pbs.example.com:8007","token_secret":"secret"}`},
		{"missing token_secret", `{"name":"test","api_url":"https://pbs.example.com:8007","token_id":"user@pam!token"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", "admin")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, body)
			}
		})
	}
}

func TestPBSCreate_InvalidJSON(t *testing.T) {
	app := newPBSTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPBSCreate_InvalidURL(t *testing.T) {
	app := newPBSTestApp(t)

	body, _ := json.Marshal(map[string]string{
		"name":         "test",
		"api_url":      "http://pbs.example.com:8007",
		"token_id":     "user@pam!token",
		"token_secret": "secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, respBody)
	}
}

func TestPBSCreate_InvalidClusterID(t *testing.T) {
	app := newPBSTestApp(t)

	badID := "not-a-uuid"
	body, _ := json.Marshal(map[string]string{
		"name":         "test",
		"api_url":      "https://pbs.example.com:8007",
		"token_id":     "user@pam!token",
		"token_secret": "secret",
		"cluster_id":   badID,
	})
	req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, respBody)
	}
}

func TestPBSGet_InvalidUUID(t *testing.T) {
	app := newPBSTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/pbs-servers/not-a-uuid", nil)
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPBS_NoAuth(t *testing.T) {
	app := newPBSTestApp(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/pbs-servers"},
		{http.MethodGet, "/pbs-servers"},
		{http.MethodGet, "/pbs-servers/" + uuid.New().String()},
		{http.MethodPut, "/pbs-servers/" + uuid.New().String()},
		{http.MethodDelete, "/pbs-servers/" + uuid.New().String()},
		{http.MethodGet, "/clusters/" + uuid.New().String() + "/pbs-servers"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var body io.Reader
			if ep.method == http.MethodPost || ep.method == http.MethodPut {
				body = bytes.NewBufferString(`{}`)
			}
			req := httptest.NewRequest(ep.method, ep.path, body)
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusForbidden, respBody)
			}
		})
	}
}

func TestPBS_NonAdmin(t *testing.T) {
	app := newPBSTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/pbs-servers", nil)
	req.Header.Set("X-Test-Role", "user")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPBSListByCluster_InvalidUUID(t *testing.T) {
	app := newPBSTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/clusters/bad-uuid/pbs-servers", nil)
	req.Header.Set("X-Test-Role", "admin")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
