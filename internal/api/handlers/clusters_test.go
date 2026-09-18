package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
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
	app.Use(func(c fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	// The permission middleware is mounted HERE because it is no longer in the
	// handler bodies: internal/api/registry_clusters.go declares it, and
	// mountRegistry splices it in. These calls mirror that declaration so the
	// 403 assertions below still exercise a real gate rather than a hand-rolled
	// one — and registry_clusters_test.go's
	// TestClusterRoutesAreGatedByTheirDeclaration drives the REAL declaration
	// end to end, so a drift between the two is caught there rather than here.
	app.Post("/clusters", RequirePermission("manage", "cluster"),
		withRequestParams(t, clusterCreateMirror(t), nil, handler.Create))
	app.Get("/clusters", withRequestParams(t, apischema.Properties{}, nil, handler.List))
	app.Get("/clusters/:id", RequireClusterPermission("view", "cluster"),
		withRequestParams(t, clusterIDMirror(t), []string{"id"}, handler.Get))
	app.Put("/clusters/:id", RequireClusterPermission("manage", "cluster"),
		withRequestParams(t, clusterUpdateMirror(t), []string{"id"}, handler.Update))
	app.Delete("/clusters/:id", RequireClusterPermission("delete", "cluster"),
		withRequestParams(t, clusterDeleteMirror(t), []string{"id"}, handler.Delete))

	return app
}

// The three mirrors below restate the schemas registry_clusters.go declares, for
// the reason withRequestParams' own doc comment gives: package api imports this
// package, not the other way round. They carry only what these tests reach —
// the required fields, the pointer-shaped optional ones, and the uuid format on
// :id — and each is compiled, so a mirror that drifts into invalidity fails
// loudly rather than quietly accepting anything.
func clusterIDMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
	})
}

func clusterCreateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"name":                  {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(255)},
		"api_url":               {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(2048)},
		"token_id":              {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(255)},
		"token_secret":          {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(1024)},
		"tls_fingerprint":       {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
		"sync_interval_seconds": {Type: apischema.Integer, Optional: true, Default: 30, Minimum: apischema.Ptr(10.0), Maximum: apischema.Ptr(86400.0)},
		"allow_private_address": {Type: apischema.Boolean, Optional: true, Default: false},
		"bootstrap":             {Type: apischema.Object, Optional: true},
	})
}

func clusterUpdateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"id":                          {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"name":                        {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(255)},
		"api_url":                     {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(2048)},
		"token_id":                    {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(255)},
		"token_secret":                {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(1024)},
		"tls_fingerprint":             {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
		"sync_interval_seconds":       {Type: apischema.Integer, Optional: true, Minimum: apischema.Ptr(10.0), Maximum: apischema.Ptr(86400.0)},
		"is_active":                   {Type: apischema.Boolean, Optional: true},
		"allow_private_address":       {Type: apischema.Boolean, Optional: true, Default: false},
		"acknowledge_ssh_trust_reset": {Type: apischema.Boolean, Optional: true, Default: false},
	})
}

func clusterDeleteMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"id":                     {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"revoke_pve_credentials": {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(16)},
	})
}

// compiledMirror compiles a mirror schema and fails the test if it is itself
// malformed, so a drifting mirror is never mistaken for a passing handler.
func compiledMirror(t *testing.T, props apischema.Properties) apischema.Properties {
	t.Helper()
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

func testErrorHandler(c fiber.Ctx, err error) error {
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

// An explicit empty token_secret is rejected rather than encrypted. Storing
// the ciphertext of "" would break the cluster's connectivity in one request,
// and it reads to the credential-redirect check as "no secret supplied" — so
// a caller who DID send the field would be told to re-enter it.
//
// The check runs on the request alone, ahead of the row fetch, which is why it
// is reachable with the nil-queries test handler.
func TestClusterUpdate_EmptyTokenSecretRejected(t *testing.T) {
	app := newClusterTestApp(t)

	body, _ := json.Marshal(map[string]any{
		"api_url":      "https://attacker.example.net:8006",
		"token_secret": "",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+uuid.New().String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Role", "admin")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(raw, []byte("token_secret must not be empty")) {
		t.Errorf("body %s does not explain the empty token_secret", raw)
	}
}
