package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/veeam"
)

// The Veeam routes are declared endpoints now
// (internal/api/registry_veeam.go), so what USED to be tested here splits in
// two, exactly as the PBS routes did.
//
// The parameter rules — the four required create fields, a malformed body, a
// path id that is not a UUID — are the SCHEMA's, and they are tested against
// the REAL declaration in internal/api/registry_veeam_test.go. So are the
// twelve routes whose permission is now a middleware Check: a bare handler
// mount cannot exercise a gate that no longer sits inside the handler.
//
// What stays here is what is still the handler's: the URL policy, the
// insecure-TLS acknowledgement, the response DTO's shape, and the two routes
// whose authorization is genuinely resolved at request time.

// veeamMirror is a local copy of the parts of the Veeam declarations these
// tests exercise.
//
// It is a mirror rather than the real thing because package api imports this
// package, not the other way round — the same reason pbsMirror keeps its own
// copy. The real declarations are pinned by
// internal/api/registry_veeam_test.go, so between the two the whole chain is
// covered.
func veeamMirror(t *testing.T, extra apischema.Properties) apischema.Properties {
	t.Helper()
	props := apischema.Properties{
		"name":                     {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(255)},
		"base_url":                 {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(2048)},
		"username":                 {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(255)},
		"password":                 {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(1024)},
		"tls_fingerprint":          {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
		"verify_tls":               {Type: apischema.Boolean, Optional: true, Default: true},
		"acknowledge_insecure_tls": {Type: apischema.Boolean, Optional: true, Default: false},
		"allow_private_address":    {Type: apischema.Boolean, Optional: true, Default: false},
	}
	for name, prop := range extra {
		props[name] = prop
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

// veeamPathMirror is veeamMirror for the routes that carry only path
// parameters.
func veeamPathMirror(t *testing.T, extra apischema.Properties) apischema.Properties {
	t.Helper()
	props := apischema.Properties{
		"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
	}
	for name, prop := range extra {
		props[name] = prop
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

// veeamHandlerWithParams adapts a registry-shaped handler to a fiber.Handler
// by doing what Endpoint.serve does: read the request into a map, validate it
// against the schema, and hand the result over. A validation failure comes
// back as the same 400 the registry answers with, so a test that drives an
// invalid body still sees the status a client would.
//
// It is pbsHandlerWithParams' twin rather than a shared helper, so that each
// domain's test file states the schema its own routes are driven against and
// a change to one cannot silently retune the other.
func veeamHandlerWithParams(t *testing.T, props apischema.Properties, pathKeys []string, h func(fiber.Ctx, *apischema.Params) error) fiber.Handler {
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

// newVeeamTestApp mounts the Veeam routes that still decide something in the
// handler, with a nil queries handle.
//
// Every test here exercises a path that returns before the first database
// call — validation, authorization, and the connection probe all run first —
// so the nil is safe and keeps these tests free of a live Postgres.
func newVeeamTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewVeeamHandler(nil, testEncryptionKey, nil)

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

	app.Post("/veeam-servers", veeamHandlerWithParams(t, veeamMirror(t, nil), nil, handler.Create))
	// The two routes whose permission is genuinely resolved at request time:
	// both run accessibleClusters("view", "veeam") before they touch a row, so
	// a caller with no grant anywhere is refused without a database.
	app.Get("/veeam-servers/:id/orphaned-objects",
		veeamHandlerWithParams(t, veeamPathMirror(t, nil), []string{"id"}, handler.ListOrphanedObjects))
	app.Put("/veeam-servers/:id/backup-objects/:object_id/guest",
		veeamHandlerWithParams(t, veeamPathMirror(t, apischema.Properties{
			"object_id":        {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
			"guest_cluster_id": {Type: apischema.String, Alias: "cluster_id", Optional: true, Format: "uuid"},
			"vmid":             {Type: apischema.Integer, Optional: true, Minimum: apischema.Ptr(1.0)},
		}), []string{"id", "object_id"}, handler.MapBackupObjectGuest))

	return app
}

func doVeeamRequest(t *testing.T, app *fiber.App, method, path, role, body string) (int, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// The URL carries an admin password on every request, so plaintext is refused
// outright rather than warned about.
//
// The rule stays in the handler because the schema has no URL format:
// validateURLFormat owns it and answers with a message of its own.
func TestVeeamCreate_RejectsNonHTTPSAndCredentialledURLs(t *testing.T) {
	app := newVeeamTestApp(t)

	tests := []struct {
		name string
		url  string
	}{
		{"http scheme", "http://vbr.example.com:9419"},
		{"no scheme", "vbr.example.com:9419"},
		{"credentials embedded", "https://admin:hunter2@vbr.example.com:9419"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"name":"vbr","base_url":"` + tc.url + `","username":"administrator","password":"s"}`
			status, respBody := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", body)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 — body: %s", status, respBody)
			}
		})
	}
}

// Per the project's warn-don't-block convention a private address is a
// confirm-required 422, not a refusal — self-hosted VBR on an RFC1918 address
// is the normal case, not the exception.
func TestVeeamCreate_PrivateAddressRequiresConfirmation(t *testing.T) {
	app := newVeeamTestApp(t)

	body := `{"name":"vbr","base_url":"https://10.20.30.40:9419","username":"administrator","password":"s"}`
	status, respBody := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", body)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — body: %s", status, respBody)
	}
	if !strings.Contains(respBody, "private_address_confirm_required") {
		t.Errorf("response %s does not carry the confirm-required error code", respBody)
	}
}

// An unreachable server is a 502, and it must be the probe that produces it —
// this test would panic on the nil *db.Queries if the handler stored the row
// before connecting.
func TestVeeamCreate_UnreachableServerIsBadGateway(t *testing.T) {
	app := newVeeamTestApp(t)

	// Port 1 on loopback: nothing listens, so the dial fails immediately.
	body := `{"name":"vbr","base_url":"https://127.0.0.1:1","username":"administrator",` +
		`"password":"s","allow_private_address":true}`
	status, respBody := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", body)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 — body: %s", status, respBody)
	}
}

// TestVeeamRoutes_RefuseBeforeAnyLookup covers the two routes whose
// authorization is still the handler's own.
//
// The other nine this used to cover — create, list, get, update, delete,
// test, the two platform routes and the infrastructure listing — are
// middleware Checks now, and
// TestVeeamGlobalRoutesAreGatedByTheirDeclaration in
// internal/api/registry_veeam_test.go drives each of them end to end against
// the real declaration. A bare handler mount here would prove nothing about
// a gate that no longer sits inside the handler, and leaving the cases in
// would quietly turn into "these routes need no permission".
//
// Both of the two run accessibleClusters("view", "veeam") FIRST, before the
// server row is loaded, which is what stops an unauthorized caller telling
// 404 from 403 and probing which server ids exist.
func TestVeeamRoutes_RefuseBeforeAnyLookup(t *testing.T) {
	app := newVeeamTestApp(t)
	id := uuid.New().String()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list orphaned objects", http.MethodGet, "/veeam-servers/" + id + "/orphaned-objects", ""},
		{"map object guest", http.MethodPut, "/veeam-servers/" + id + "/backup-objects/" + uuid.New().String() + "/guest", `{"cluster_id":null,"vmid":null}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The stub engine grants everything to "admin" and nothing to
			// anyone else, so a non-admin caller must be refused before any
			// work happens — including before the nil queries handle is
			// touched, which is what keeps this test from panicking.
			status, body := doVeeamRequest(t, app, tc.method, tc.path, "viewer", tc.body)
			if status != http.StatusForbidden {
				t.Errorf("status = %d, want 403 — body: %s", status, body)
			}
		})
	}
}

// The both-or-neither rule on MapBackupObjectGuest — a cluster with no vmid
// identifies nothing, and a vmid with no cluster is ambiguous across every
// cluster the server protects — cannot be driven from here: the handler
// authorizes and loads the object BEFORE it reaches that check, so a request
// that got far enough to exercise it would need a live database. What the
// migration had to preserve is the SHAPE the check reads, and that is asserted
// against the real declaration by
// TestMapBackupObjectGuestKeepsItsBothOrNeitherShape in
// internal/api/registry_veeam_test.go: both parameters optional, neither
// carrying a default, so "the caller said nothing" stays distinguishable from
// "the caller sent zero" the way the *pointer fields used to make it.

// A confirm-and-proceed gate, matching how private addresses are handled: a
// lab with an unreachable internal CA is real, doing it by accident is not.
func TestRequireInsecureTLSAck(t *testing.T) {
	tests := []struct {
		name         string
		insecure     bool
		acknowledged bool
		wantErr      bool
	}{
		{"secure, no ack", false, false, false},
		{"secure, ack", false, true, false},
		{"insecure, no ack", true, false, true},
		{"insecure, acknowledged", true, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireInsecureTLSAck(tc.insecure, tc.acknowledged)
			if (err != nil) != tc.wantErr {
				t.Fatalf("requireInsecureTLSAck(%v, %v) = %v, wantErr %v",
					tc.insecure, tc.acknowledged, err, tc.wantErr)
			}
			if err == nil {
				return
			}
			var fErr *fiber.Error
			if !errors.As(err, &fErr) || fErr.Code != http.StatusUnprocessableEntity {
				t.Errorf("error = %v, want a 422 *fiber.Error", err)
			}
		})
	}
}

// Storing a credential against a host whose certificate is never checked has
// to be said out loud.
func TestVeeamCreate_UnverifiedTLSNeedsAcknowledgement(t *testing.T) {
	app := newVeeamTestApp(t)

	base := `"name":"vbr","base_url":"https://127.0.0.1:1","username":"administrator",` +
		`"password":"s","allow_private_address":true,"verify_tls":false`

	status, body := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", "{"+base+"}")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — body: %s", status, body)
	}
	if !strings.Contains(body, "acknowledge_insecure_tls") {
		t.Errorf("response %s does not name the flag that unblocks it", body)
	}

	// With the acknowledgement it proceeds to the probe, which then fails on
	// the closed port — proving the gate was what stopped it, not the dial.
	status, body = doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin",
		"{"+base+`,"acknowledge_insecure_tls":true}`)
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 once acknowledged — body: %s", status, body)
	}
}

// The ack gate keys on the END STATE, not on either field alone.
//
// Over-firing here would break the legitimate case the edit dialog depends on:
// re-homing a pinned server onto a CA-signed address clears the pin, and that
// is a move to a verified connection, not away from one.
func TestVeeamUpdate_InsecureTLSAckPredicate(t *testing.T) {
	tests := []struct {
		name                       string
		existingPin, wantPin       string
		existingVerify, wantVerify bool
		wantAckRequired            bool
	}{
		{"pinned, unchanged", "abc", "abc", true, true, false},
		{"pinned, verify turned off (pin still governs)", "abc", "abc", true, false, false},
		{"pin cleared, CA verification stays on", "abc", "", true, true, false},
		{"CA-verified, verification turned off", "", "", true, false, true},
		{"pin cleared AND verification off", "abc", "", true, false, true},
		{"already unverified, unrelated edit", "", "", false, false, false},
		{"unverified server given a pin", "", "abc", false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wasUnverified := tc.existingPin == "" && !tc.existingVerify
			nowUnverified := tc.wantPin == "" && !tc.wantVerify
			err := requireInsecureTLSAck(nowUnverified && !wasUnverified, false)
			if (err != nil) != tc.wantAckRequired {
				t.Errorf("ack required = %v, want %v (err: %v)", err != nil, tc.wantAckRequired, err)
			}
		})
	}
}

// The response DTO must have no field that could ever hold the credential.
// Checking the shape rather than a single serialization means a future edit
// that adds one fails here instead of leaking on some untested route.
func TestVeeamResponse_HasNoSecretFields(t *testing.T) {
	forbidden := []string{"password", "secret", "token", "credential"}

	rt := reflect.TypeOf(veeamServerResponse{})
	for i := range rt.NumField() {
		field := rt.Field(i)
		name := strings.ToLower(field.Name)
		tag := strings.ToLower(field.Tag.Get("json"))
		for _, word := range forbidden {
			if strings.Contains(name, word) || strings.Contains(tag, word) {
				t.Errorf("veeamServerResponse.%s (json:%q) looks like a credential field — "+
					"the Veeam password must never be serialized", field.Name, field.Tag.Get("json"))
			}
		}
	}

	// And prove it on a populated value: the sqlc row carries the ciphertext,
	// the response must not.
	row := db.VeeamServer{
		ID:                uuid.New(),
		Name:              "vbr01",
		BaseUrl:           "https://vbr.example.com:9419",
		Username:          `ad\jdoe`,
		PasswordEncrypted: "SUPER-SECRET-CIPHERTEXT",
	}
	encoded, err := json.Marshal(toVeeamResponse(row, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "SUPER-SECRET-CIPHERTEXT") {
		t.Errorf("serialized response leaked the stored ciphertext: %s", encoded)
	}
}

// The account name is half of a domain administrator credential, and
// view:veeam is seeded to the built-in Viewer. A read-only caller must not get
// it — the audit path already withholds it for exactly this reason, and
// returning it here would defeat that control through the front door.
func TestVeeamResponse_WithholdsUsernameFromReadOnlyCallers(t *testing.T) {
	row := db.VeeamServer{
		ID:       uuid.New(),
		Name:     "vbr01",
		BaseUrl:  "https://vbr.example.com:9419",
		Username: `ad\jdoe`,
	}

	redacted, err := json.Marshal(toVeeamResponse(row, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(redacted), "jdoe") {
		t.Errorf("read-only response leaked the account name: %s", redacted)
	}

	full, err := json.Marshal(toVeeamResponse(row, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(full), "jdoe") {
		t.Errorf("manage:veeam response should keep the account name: %s", full)
	}
}

// view:audit is granted to every Viewer by default, so anything in a details
// blob is effectively world-readable within the install. The username is half
// of a domain administrator credential and stays out.
func TestVeeamAuditDetailsCarryNoSecrets(t *testing.T) {
	row := db.VeeamServer{
		ID:                uuid.New(),
		Name:              `vbr "primary"`,
		BaseUrl:           "https://vbr.example.com:9419",
		Username:          `ad\jdoe`,
		PasswordEncrypted: "SUPER-SECRET-CIPHERTEXT",
		ProductVersion:    "13.1.0.411",
		LicenseEdition:    "EnterprisePlus",
	}

	blob := veeamAuditDetails(row, map[string]any{"password_rotated": true, "verify_tls": false})

	var details map[string]any
	if err := json.Unmarshal(blob, &details); err != nil {
		t.Fatalf("details blob is not valid JSON (%s): %v", blob, err)
	}

	// A quote in the name must not have broken the encoding.
	if details["name"] != `vbr "primary"` {
		t.Errorf("name = %v, want the original including its quotes", details["name"])
	}
	if details["product_version"] != "13.1.0.411" {
		t.Errorf("product_version = %v", details["product_version"])
	}
	if details["password_rotated"] != true || details["verify_tls"] != false {
		t.Errorf("extra fields not merged: %v", details)
	}

	for _, banned := range []string{"SUPER-SECRET-CIPHERTEXT", `ad\jdoe`} {
		if strings.Contains(string(blob), banned) {
			t.Errorf("audit details leaked %q: %s", banned, blob)
		}
	}
	for key := range details {
		if key == "username" || key == "password" || key == "password_encrypted" {
			t.Errorf("audit details carry a %q key", key)
		}
	}
}

// Each branch exists because the operator's next action differs. Collapsing
// them all into 502 would tell someone with a wrong password to go check their
// network.
//
// The credential case is the load-bearing one, and it must NOT be 401: the
// SPA's api-client treats a 401 as an expired Nexara session, burns a token
// refresh and replays the request — which runs the probe again and spends a
// second failed logon against a domain admin account.
func TestRenderVeeamProbeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"bad credentials", veeam.ErrAuthFailed, http.StatusUnprocessableEntity},
		{"too old", &veeam.VersionError{Got: "13.0.1.204", Minimum: veeam.MinBuildVersion}, http.StatusUnprocessableEntity},
		{"no shared revision", veeam.ErrRevisionUnknown, http.StatusUnprocessableEntity},
		{"unreachable", veeam.ErrUnreachable, http.StatusBadGateway},
		{"bad config", veeam.ErrInvalidInput, http.StatusBadRequest},
		{"unclassified", errors.New("something else"), http.StatusBadGateway},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/render", func(c fiber.Ctx) error {
				return renderVeeamProbeError(c, tc.err)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/render", nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.want {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d — body: %s", resp.StatusCode, tc.want, body)
			}
			if tc.want == http.StatusUnauthorized {
				t.Fatal("no branch may map to 401 — see the doc comment above")
			}
		})
	}
}
