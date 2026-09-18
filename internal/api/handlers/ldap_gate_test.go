package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// newLDAPTestApp mounts the LDAP routes with a nil queries handle.
//
// This asserts the 422 CONTRACT — the status and the error code the admin page
// keys on. It does not prove the handler stopped: a fall-through panics on the
// nil handle inside app.Test, and that does not reliably surface as a failure
// here. TestRequireLDAPTransportAck covers the stopping half deterministically,
// by asserting the gate returns a non-nil error.
func newLDAPTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewLDAPHandler(nil, testEncryptionKey, nil, nil)

	app := fiber.New(fiber.Config{
		ErrorHandler: testErrorHandler,
	})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Post("/ldap/configs", withRequestParams(t, ldapCreateMirror(t), nil, handler.Create))

	return app
}

// ldapCreateMirror is a local copy of the parts of POST /api/v1/ldap/configs'
// declaration (internal/api/registry_ldap.go) that Create reads.
//
// A mirror for the reason migrationListMirror gives — package api imports this
// package, not the other way round — and with the same caveat: what it has to
// get right is that every key the handler reads IS declared, and that the three
// transport booleans carry Default false. That last part is load-bearing here
// rather than incidental: an omitted start_tls meaning FALSE is the accident
// the gate under test exists to catch, so a mirror that made them tristate
// would quietly stop testing it.
func ldapCreateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return apischema.Properties{
		"name":                     {Type: apischema.String, Optional: true},
		"enabled":                  {Type: apischema.Boolean, Optional: true, Default: false},
		"server_url":               {Type: apischema.String, MinLength: apischema.Ptr(1)},
		"start_tls":                {Type: apischema.Boolean, Optional: true, Default: false},
		"skip_tls_verify":          {Type: apischema.Boolean, Optional: true, Default: false},
		"bind_dn":                  {Type: apischema.String, Optional: true},
		"bind_password":            {Type: apischema.String, Optional: true},
		"search_base_dn":           {Type: apischema.String, MinLength: apischema.Ptr(1)},
		"user_filter":              {Type: apischema.String, Optional: true},
		"username_attribute":       {Type: apischema.String, Optional: true},
		"email_attribute":          {Type: apischema.String, Optional: true},
		"display_name_attribute":   {Type: apischema.String, Optional: true},
		"group_search_base_dn":     {Type: apischema.String, Optional: true},
		"group_filter":             {Type: apischema.String, Optional: true},
		"group_attribute":          {Type: apischema.String, Optional: true},
		"group_role_mapping":       {Type: apischema.Object, Optional: true},
		"default_role_id":          {Type: apischema.String, Optional: true},
		"sync_interval_minutes":    {Type: apischema.Integer, Optional: true, Default: 0},
		"acknowledge_insecure_tls": {Type: apischema.Boolean, Optional: true, Default: false},
	}
}

func doLDAPRequest(t *testing.T, app *fiber.App, method, path, role, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func ldapCreateBody(serverURL string, startTLS, skipVerify bool) string {
	return `{"name":"Directory","enabled":true,"server_url":"` + serverURL + `",` +
		`"start_tls":` + boolJSON(startTLS) + `,"skip_tls_verify":` + boolJSON(skipVerify) + `,` +
		`"bind_dn":"cn=svc,dc=corp","bind_password":"hunter2","search_base_dn":"dc=corp",` +
		`"sync_interval_minutes":60}`
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// The handler must refuse an insecure transport with the structured 422 the
// admin page turns into a confirm prompt — and must do it WITHOUT reaching the
// database. See newLDAPTestApp for how the second half is enforced.
func TestLDAPCreate_InsecureTransportRequiresConfirmation(t *testing.T) {
	tests := []struct {
		name       string
		serverURL  string
		startTLS   bool
		skipVerify bool
		wantKind   string
	}{
		{
			name:      "cleartext ldap with no StartTLS",
			serverURL: "ldap://dc.example.com",
			wantKind:  "cleartext",
		},
		{
			name:       "ldaps but certificate never verified",
			serverURL:  "ldaps://dc.example.com",
			skipVerify: true,
			wantKind:   "unverified",
		},
		{
			name:       "StartTLS but certificate never verified",
			serverURL:  "ldap://dc.example.com",
			startTLS:   true,
			skipVerify: true,
			wantKind:   "unverified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newLDAPTestApp(t)
			status, body := doLDAPRequest(t, app, http.MethodPost, "/ldap/configs", "admin",
				ldapCreateBody(tt.serverURL, tt.startTLS, tt.skipVerify))

			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 — body: %s", status, body)
			}
			if !strings.Contains(body, "insecure_ldap_transport_confirm_required") {
				t.Errorf("response %s does not carry the confirm-required error code", body)
			}
			if !strings.Contains(body, tt.wantKind) {
				t.Errorf("response %s does not name the transport kind %q", body, tt.wantKind)
			}
		})
	}
}

// requireLDAPTransportAck returns an ERROR VALUE rather than writing the
// response itself, and the distinction is the whole fix: a version that wrote
// a 422 and returned nil left its callers' `if err != nil` false, so the
// handler ran on and saved the config the gate had just "refused".
//
// Nothing about the response body proves that — the status is 422 either way.
// Only the return value does, so it is asserted directly.
func TestRequireLDAPTransportAck(t *testing.T) {
	tests := []struct {
		name              string
		wasProtected      bool
		nowProtected      bool
		wasSkippingVerify bool
		nowSkippingVerify bool
		acknowledged      bool
		wantKind          string // empty means the request must be allowed through
	}{
		{
			name:         "no downgrade, no error",
			wasProtected: true, nowProtected: true,
		},
		{
			name:         "encryption lost",
			wasProtected: true, nowProtected: false,
			wantKind: "cleartext",
		},
		{
			name:         "verification lost",
			wasProtected: true, nowProtected: true,
			nowSkippingVerify: true,
			wantKind:          "unverified",
		},
		{
			// Losing encryption outranks losing verification: a cleartext
			// connection has no certificate to talk about, and the operator
			// needs the stronger of the two warnings.
			name:         "both lost reports cleartext",
			wasProtected: true, nowProtected: false,
			nowSkippingVerify: true,
			wantKind:          "cleartext",
		},
		{
			name:         "acknowledged downgrade is allowed through",
			wasProtected: true, nowProtected: false,
			acknowledged: true,
		},
		{
			name:         "acknowledged verification loss is allowed through",
			wasProtected: true, nowProtected: true,
			nowSkippingVerify: true,
			acknowledged:      true,
		},
		{
			name:         "already insecure, unchanged, not re-prompted",
			wasProtected: false, nowProtected: false,
			wasSkippingVerify: true, nowSkippingVerify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireLDAPTransportAck(
				tt.wasProtected, tt.nowProtected,
				tt.wasSkippingVerify, tt.nowSkippingVerify,
				tt.acknowledged)

			if tt.wantKind == "" {
				if err != nil {
					t.Fatalf("got %v, want nil (request should pass the gate)", err)
				}
				return
			}
			if err == nil {
				t.Fatal("got nil, want a confirm-required error — the handler would have saved the config")
			}

			var confirmErr *confirmRequiredError
			if !errors.As(err, &confirmErr) {
				t.Fatalf("error %v is not a *confirmRequiredError, so renderConfirmRequired "+
					"would pass it through as a plain 500 instead of the confirm prompt", err)
			}
			if confirmErr.Code != confirmInsecureLDAPTransport {
				t.Errorf("Code = %q, want %q", confirmErr.Code, confirmInsecureLDAPTransport)
			}
			if got := confirmErr.Fields["transport_kind"]; got != tt.wantKind {
				t.Errorf("transport_kind = %v, want %q", got, tt.wantKind)
			}
		})
	}
}
