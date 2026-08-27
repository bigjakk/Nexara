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

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/veeam"
)

// newVeeamTestApp mounts the Veeam routes with a nil queries handle.
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

	app.Post("/veeam-servers", handler.Create)
	app.Get("/veeam-servers", handler.List)
	app.Get("/veeam-servers/:id", handler.Get)
	app.Put("/veeam-servers/:id", handler.Update)
	app.Delete("/veeam-servers/:id", handler.Delete)
	app.Post("/veeam-servers/:id/test", handler.Test)
	app.Get("/veeam-servers/:id/platforms", handler.ListPlatforms)
	app.Get("/veeam-servers/:id/infrastructure", handler.ListInfrastructure)
	app.Put("/veeam-servers/:id/platforms/:platform_id", handler.MapPlatform)

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

func TestVeeamCreate_MissingFields(t *testing.T) {
	app := newVeeamTestApp(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing name", `{"base_url":"https://vbr.example.com:9419","username":"administrator","password":"s"}`},
		{"missing base_url", `{"name":"vbr","username":"administrator","password":"s"}`},
		{"missing username", `{"name":"vbr","base_url":"https://vbr.example.com:9419","password":"s"}`},
		{"missing password", `{"name":"vbr","base_url":"https://vbr.example.com:9419","username":"administrator"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 — body: %s", status, body)
			}
		})
	}
}

func TestVeeamCreate_InvalidJSON(t *testing.T) {
	app := newVeeamTestApp(t)
	status, body := doVeeamRequest(t, app, http.MethodPost, "/veeam-servers", "admin", "not json")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — body: %s", status, body)
	}
}

// The URL carries an admin password on every request, so plaintext is refused
// outright rather than warned about.
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

func TestVeeamRoutes_RequirePermission(t *testing.T) {
	app := newVeeamTestApp(t)
	id := uuid.New().String()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/veeam-servers", `{"name":"vbr","base_url":"https://vbr.example.com:9419","username":"u","password":"p"}`},
		{"list", http.MethodGet, "/veeam-servers", ""},
		{"get", http.MethodGet, "/veeam-servers/" + id, ""},
		{"update", http.MethodPut, "/veeam-servers/" + id, `{"name":"renamed"}`},
		{"delete", http.MethodDelete, "/veeam-servers/" + id, ""},
		{"test", http.MethodPost, "/veeam-servers/" + id + "/test", ""},
		// The platform mapping decides which cluster a body of backup data is
		// attributed to, and every cluster-scoped Veeam permission resolves
		// through it — so both sides are gated on the GLOBAL grant, not on
		// one for the cluster being attached.
		{"list platforms", http.MethodGet, "/veeam-servers/" + id + "/platforms", ""},
		{"map platform", http.MethodPut, "/veeam-servers/" + id + "/platforms/" + uuid.New().String(), `{"cluster_id":null}`},
		{"list infrastructure", http.MethodGet, "/veeam-servers/" + id + "/infrastructure", ""},
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

func TestVeeamGet_InvalidID(t *testing.T) {
	app := newVeeamTestApp(t)
	status, body := doVeeamRequest(t, app, http.MethodGet, "/veeam-servers/not-a-uuid", "admin", "")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — body: %s", status, body)
	}
}

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
