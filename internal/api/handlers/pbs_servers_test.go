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

// The PBS routes are declared endpoints now (internal/api/registry_pbs.go),
// so what USED to be tested here splits in two.
//
// The parameter rules — the four required create fields, a malformed body,
// a cluster_id that is not a UUID, a path id that is not a UUID — are the
// schema's, and they are tested against the REAL declaration in
// internal/api/registry_pbs_test.go rather than against a hand-mounted app
// here. The same goes for GET /clusters/:cluster_id/pbs-servers, whose
// permission is a middleware Check that a bare handler mount cannot
// exercise at all.
//
// What stays here is what is still the handler's: the URL policy, the
// empty-token_secret refusal, and the per-row permission split that makes
// four of these routes Deferred.

// pbsMirror is a local copy of the parts of the PBS declarations these
// tests exercise.
//
// It is a mirror rather than the real thing because package api imports
// this package, not the other way round — the same reason
// TestDiskAttachRequestFrom keeps its own copy. The real declarations are
// pinned by internal/api/registry_pbs_test.go, so between the two the
// whole chain is covered.
func pbsMirror(t *testing.T, extra apischema.Properties) apischema.Properties {
	t.Helper()
	props := apischema.Properties{
		"name":                  {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(255)},
		"api_url":               {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(2048)},
		"token_id":              {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(255)},
		"token_secret":          {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(1024)},
		"tls_fingerprint":       {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
		"attached_cluster_id":   {Type: apischema.String, Alias: "cluster_id", Optional: true, MaxLength: apischema.Ptr(36)},
		"allow_private_address": {Type: apischema.Boolean, Optional: true, Default: false},
	}
	for name, prop := range extra {
		props[name] = prop
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

// pbsHandlerWithParams adapts a registry-shaped handler to a fiber.Handler
// by doing what Endpoint.serve does: read the request into a map, validate
// it against the schema, and hand the result over. A validation failure
// comes back as the same 400 the registry answers with, so a test that
// drives an invalid body still sees the status a client would.
func pbsHandlerWithParams(t *testing.T, props apischema.Properties, pathKeys []string, h func(fiber.Ctx, *apischema.Params) error) fiber.Handler {
	t.Helper()
	return func(c fiber.Ctx) error {
		raw := map[string]any{}
		if body := c.Body(); len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &raw); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "request body is not valid JSON")
			}
		}
		for _, key := range pathKeys {
			if v := c.Params(key); v != "" {
				raw[key] = v
			}
		}
		params, err := props.Validate(raw)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return h(c, params)
	}
}

func newPBSTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewPBSHandler(nil, testEncryptionKey, nil)

	app := fiber.New(fiber.Config{
		ErrorHandler: testErrorHandler,
	})

	app.Use(func(c fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	idParam := apischema.Properties{"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}}
	app.Post("/pbs-servers", pbsHandlerWithParams(t, pbsMirror(t, nil), nil, handler.Create))
	app.Put("/pbs-servers/:id", pbsHandlerWithParams(t, pbsMirror(t, apischema.Properties{
		"id":           idParam["id"],
		"name":         {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(255)},
		"api_url":      {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(2048)},
		"token_id":     {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(255)},
		"token_secret": {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(1024)},
	}), []string{"id"}, handler.Update))

	return app
}

// validPBSCreateBody is a create body that passes the schema, so a test
// about the HANDLER reaches it.
//
// It matters that this is complete: a registry route validates before the
// handler runs, so an incomplete body now answers 400 rather than the 403
// the handler would have produced. That ordering is the same for every
// Deferred route in the registry, and sending a valid body is what keeps
// this test about authorization rather than about validation.
func validPBSCreateBody(t *testing.T, overrides map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"name":         "backup01",
		"api_url":      "https://pbs.example.com:8007",
		"token_id":     "user@pam!token",
		"token_secret": "secret",
	}
	for k, v := range overrides {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return raw
}

func TestPBSCreate_InvalidURL(t *testing.T) {
	app := newPBSTestApp(t)

	// http, not https — validateURLFormat's rule, which stays in the
	// handler because the schema has no URL format and the address policy
	// resolves DNS.
	req := httptest.NewRequest(http.MethodPost, "/pbs-servers",
		bytes.NewReader(validPBSCreateBody(t, map[string]any{"api_url": "http://pbs.example.com:8007"})))
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
}

// TestPBSCreate_NonAdminForbidden covers the Deferred half of the create
// route: with no cluster in the body it gates on the INSTANCE-WIDE
// manage:pbs, and the stub engine grants a non-admin nothing.
func TestPBSCreate_NonAdminForbidden(t *testing.T) {
	app := newPBSTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewReader(validPBSCreateBody(t, nil)))
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
}

// TestPBSCreate_NonAdminForbiddenWithCluster is the other branch of the
// same decision: a cluster in the body moves the gate to that cluster.
func TestPBSCreate_NonAdminForbiddenWithCluster(t *testing.T) {
	app := newPBSTestApp(t)

	body := validPBSCreateBody(t, map[string]any{"cluster_id": uuid.New().String()})
	req := httptest.NewRequest(http.MethodPost, "/pbs-servers", bytes.NewReader(body))
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
}

// See TestClusterUpdate_EmptyTokenSecretRejected — same rule, same reason.
//
// It stays a handler check rather than becoming a MinLength on the
// declaration so that the message can say what to do instead; a bare
// "must have at least 1 character" would not.
func TestPBSUpdate_EmptyTokenSecretRejected(t *testing.T) {
	app := newPBSTestApp(t)

	body, _ := json.Marshal(map[string]any{
		"api_url":      "https://attacker.example.net:8007",
		"token_secret": "",
	})
	req := httptest.NewRequest(http.MethodPut, "/pbs-servers/"+uuid.New().String(), bytes.NewReader(body))
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
