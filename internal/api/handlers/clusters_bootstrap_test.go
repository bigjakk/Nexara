package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestClusterCreate_BootstrapAndTokenAreMutuallyExclusive(t *testing.T) {
	app := newClusterTestApp(t)

	bodies := []string{
		`{"name":"c","api_url":"https://pve.example.com:8006","token_secret":"sec","bootstrap":{"username":"root@pam","password":"p"}}`,
		`{"name":"c","api_url":"https://pve.example.com:8006","token_id":"root@pam!t","bootstrap":{"username":"root@pam","password":"p"}}`,
	}
	for _, body := range bodies {
		req := httptest.NewRequest(http.MethodPost, "/clusters", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Role", "admin")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Errorf("status = %d, want 400 for %s", resp.StatusCode, body)
		}
	}
}

func TestClusterCreate_BootstrapRequiresUsernameAndPassword(t *testing.T) {
	app := newClusterTestApp(t)

	bodies := []string{
		`{"name":"c","api_url":"https://pve.example.com:8006","bootstrap":{}}`,
		`{"name":"c","api_url":"https://pve.example.com:8006","bootstrap":{"username":"root@pam"}}`,
		`{"name":"c","api_url":"https://pve.example.com:8006","bootstrap":{"password":"p"}}`,
	}
	for _, body := range bodies {
		req := httptest.NewRequest(http.MethodPost, "/clusters", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Role", "admin")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Errorf("status = %d, want 400 for %s", resp.StatusCode, body)
		}
	}
}

func TestBootstrapRequestDefaults(t *testing.T) {
	req := &bootstrapRequest{Username: "root@pam", Password: "p"}
	if err := req.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if req.UserID != defaultBootstrapUserID {
		t.Errorf("UserID = %q, want %q", req.UserID, defaultBootstrapUserID)
	}
	if req.TokenName != defaultBootstrapTokenName {
		t.Errorf("TokenName = %q, want %q", req.TokenName, defaultBootstrapTokenName)
	}

	// An explicit choice must survive.
	custom := &bootstrapRequest{Username: "root@pam", Password: "p", UserID: "svc@pve", TokenName: "nx"}
	if err := custom.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if custom.UserID != "svc@pve" || custom.TokenName != "nx" {
		t.Errorf("defaults overwrote an explicit choice: %+v", custom)
	}
}

func TestWantsCredentialRevocation(t *testing.T) {
	app := fiber.New()
	var got []bool
	app.Get("/x", func(c fiber.Ctx) error {
		got = append(got, wantsCredentialRevocation(c))
		return c.SendStatus(fiber.StatusOK)
	})

	queries := []string{"", "?revoke_pve_credentials=1", "?revoke_pve_credentials=true",
		"?revoke_pve_credentials=yes", "?revoke_pve_credentials=0", "?revoke_pve_credentials=false"}
	for _, q := range queries {
		if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/x"+q, nil)); err != nil {
			t.Fatalf("Test(%q): %v", q, err)
		}
	}

	want := []bool{false, true, true, true, false, false}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("query %q: wantsCredentialRevocation = %v, want %v", queries[i], got[i], w)
		}
	}
}

func TestClusterCredentialMintedAtColumn(t *testing.T) {
	manual := &clusterCredential{Source: credentialSourceManual}
	if manual.mintedAtColumn().Valid {
		t.Error("a manually supplied credential must store SQL NULL, not a zero time")
	}

	now := time.Now().UTC()
	minted := &clusterCredential{Source: credentialSourceBootstrap, MintedAt: now}
	col := minted.mintedAtColumn()
	if !col.Valid || !col.Time.Equal(now) {
		t.Errorf("mintedAtColumn = %+v, want the mint time", col)
	}
}

// TestRevokeSkipsCredentialsNexaraDidNotCreate is the core safety property of
// the delete-time cleanup: a pasted-in operator token is never touched, and no
// request is made at all.
func TestRevokeSkipsCredentialsNexaraDidNotCreate(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name    string
		cluster db.Cluster
	}{
		{"manual", db.Cluster{ApiUrl: srv.URL, CredentialSource: credentialSourceManual, BootstrapUserID: "nexara@pve"}},
		{"bootstrap without a recorded user", db.Cluster{ApiUrl: srv.URL, CredentialSource: credentialSourceBootstrap}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps := revokeBootstrapCredentials(context.Background(), tc.cluster, "secret", false)
			if len(steps) != 1 || steps[0].Status != "skipped" {
				t.Errorf("steps = %+v, want a single skipped step", steps)
			}
		})
	}
	if hits != 0 {
		t.Errorf("made %d request(s) to Proxmox while skipping revocation, want 0", hits)
	}
}

// pveRevokeStub answers the endpoints revocation touches. tokens is what the
// token index returns for the bootstrap user.
func pveRevokeStub(t *testing.T, tokens []string, calls *[]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/token") {
			body := make([]map[string]any, 0, len(tokens))
			for _, name := range tokens {
				body = append(body, map[string]any{"tokenid": name})
			}
			raw, _ := json.Marshal(body)
			_, _ = w.Write([]byte(`{"data":` + string(raw) + `}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestRevokeDeletesTheUserWhenItOwnsNothingElse pins the ordering rule: the
// user delete takes its tokens and ACL entries with it, so once it is safe it
// is the ONLY mutation. Revoking the ACL first would strip the very privileges
// the delete needs.
func TestRevokeDeletesTheUserWhenItOwnsNothingElse(t *testing.T) {
	var calls []string
	url := pveRevokeStub(t, []string{"nexara"}, &calls)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               url,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
		BootstrapCreatedAcl:  true,
	}, "secret", false)

	want := []string{
		"GET /api2/json/access/users/nexara@pve/token",
		"DELETE /api2/json/access/users/nexara@pve",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("calls[%d] = %q, want %q", i, calls[i], w)
		}
	}
	if len(steps) != 1 || steps[0].Step != "user" || steps[0].Status != "revoked" {
		t.Errorf("steps = %+v, want a single revoked user step", steps)
	}
}

// TestRevokeNeverCascadesOverForeignTokens is the safety property behind the
// delete dialog's promise that Nexara removes only what it created.
//
// DELETE /access/users cascades in PVE: every token that user owns goes with
// it. BootstrapCreatedUser says Nexara created the USER — not that it is the
// only thing attached. The same hypervisor onboarded twice, or a token the
// operator added through the Access Control tab, both live under that user, and
// destroying them would take a working cluster offline with no warning.
func TestRevokeNeverCascadesOverForeignTokens(t *testing.T) {
	var calls []string
	url := pveRevokeStub(t, []string{"nexara", "nexara-second-install"}, &calls)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               url,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
		BootstrapCreatedAcl:  true,
	}, "secret", false)

	// Neither the account NOR its grant may be touched. Guarding only the user
	// delete would be no protection at all: the foreign token is privsep=0, so
	// it inherits the OWNER's privileges — revoking the shared Administrator
	// grant kills it exactly as dead as deleting the account would.
	for _, c := range calls {
		if c == "DELETE /api2/json/access/users/nexara@pve" {
			t.Errorf("cascaded a user delete over a foreign token; calls = %v", calls)
		}
		if c == "PUT /api2/json/access/acl" {
			t.Errorf("revoked the shared Administrator grant, de-privileging the foreign token; calls = %v", calls)
		}
	}
	// This cluster's own token is still removed.
	if !slices.Contains(calls, "DELETE /api2/json/access/users/nexara@pve/token/nexara") {
		t.Errorf("calls = %v, want this cluster's own token removed", calls)
	}
	if steps[0].Step != "user" || steps[0].Status != "skipped" {
		t.Errorf("steps[0] = %+v, want the user delete reported as skipped", steps[0])
	}
}

// TestRevokeFailsClosedWhenItCannotEnumerate: if the token listing errors we
// cannot know what else is attached, so the cascade must not run. The narrow
// path is always safe; the cascade is not.
func TestRevokeFailsClosedWhenItCannotEnumerate(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/token") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":{"detail":"permission denied"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               srv.URL,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
	}, "secret", false)

	for _, c := range calls {
		if c == "DELETE /api2/json/access/users/nexara@pve" {
			t.Fatalf("cascaded a user delete without knowing what else was attached; calls = %v", calls)
		}
	}
	if steps[0].Status != "skipped" {
		t.Errorf("steps[0] = %+v, want the user delete skipped", steps[0])
	}
}

// TestRevokeOnAdoptedUserRevokesACLBeforeTheToken pins the other half of the
// ordering rule. With the user adopted rather than created, only the grant and
// the token may be removed — and the ACL goes first, because the residue of a
// failure there (a live Administrator grant) is worse than the residue of a
// failed token delete (a token whose owner has no privileges).
func TestRevokeOnAdoptedUserRevokesACLBeforeTheToken(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               srv.URL,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: false,
		BootstrapCreatedAcl:  true,
	}, "secret", false)

	want := []string{
		// The dependent check runs first — nothing else lives on the account,
		// so the grant and token are genuinely ours to remove.
		"GET /api2/json/access/users/nexara@pve/token",
		"PUT /api2/json/access/acl",
		"DELETE /api2/json/access/users/nexara@pve/token/nexara",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("calls[%d] = %q, want %q", i, calls[i], w)
		}
	}
	for _, s := range steps {
		if s.Status != "revoked" {
			t.Errorf("step %+v did not succeed", s)
		}
	}
	// The user was adopted, so it must survive.
	for _, c := range calls {
		if c == "DELETE /api2/json/access/users/nexara@pve" {
			t.Error("deleted a user Nexara did not create")
		}
	}
}

// TestRevokeRecordsFailuresInsteadOfBlockingDeletion: a cluster being removed
// is often already unreachable, and a failed cleanup must not strand the
// operator with an undeletable cluster.
func TestRevokeRecordsFailuresInsteadOfBlockingDeletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":{"detail":"permission denied"}}`))
	}))
	t.Cleanup(srv.Close)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               srv.URL,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
	}, "secret", false)

	// Every step must be accounted for, and nothing may claim success.
	if len(steps) == 0 {
		t.Fatal("an unreachable cluster produced no revocation report at all")
	}
	var named bool
	for _, step := range steps {
		if step.Status == "revoked" {
			t.Errorf("step %+v claims success against a cluster that refused every call", step)
		}
		if strings.Contains(step.Detail, "nexara@pve") {
			named = true
		}
	}
	if !named {
		t.Errorf("steps = %+v: none name the object left behind", steps)
	}
}

// TestRevocationDetailsAreBoundedAndPrintable: the Proxmox error body is
// echoed into these details, nothing upstream bounds it, and view:audit is held
// by every built-in Viewer.
func TestRevocationDetailsAreBoundedAndPrintable(t *testing.T) {
	noisy := "boom\n\x00" + strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		raw, _ := json.Marshal(map[string]any{"errors": map[string]string{"detail": noisy}})
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               srv.URL,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
	}, "secret", false)

	for _, step := range steps {
		if len(step.Detail) > maxAuditDetail+8 {
			t.Errorf("step detail is %d bytes, want it bounded near %d", len(step.Detail), maxAuditDetail)
		}
		for _, r := range step.Detail {
			if r < 0x20 || r == 0x7f {
				t.Errorf("step detail carries control character %q into an audit row", r)
				break
			}
		}
	}
}

// TestBootstrapRequestNeverSerialisesTheCredential guards the redaction that
// makes a whole class of leak impossible, rather than merely unwritten: the
// struct is in scope wherever onboarding errors are assembled.
func TestBootstrapRequestNeverSerialisesTheCredential(t *testing.T) {
	req := bootstrapRequest{
		Username: "root@pam", Password: "hunter2", OTP: "123456",
		UserID: "nexara@pve", TokenName: "nexara",
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rendered := []string{string(raw), fmt.Sprintf("%v", req), fmt.Sprintf("%+v", req), fmt.Sprint(req)}
	for _, out := range rendered {
		for _, secret := range []string{"hunter2", "123456"} {
			if strings.Contains(out, secret) {
				t.Errorf("bootstrapRequest rendered its credential: %s", out)
			}
		}
	}

	// Redaction must not break decoding of the request body.
	var decoded bootstrapRequest
	if err := json.Unmarshal([]byte(`{"username":"root@pam","password":"pw","otp":"999"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Password != "pw" || decoded.OTP != "999" {
		t.Errorf("decoded = %+v, want the credential to still arrive from the wire", decoded)
	}
}

func TestBootstrapRejectsPamUserID(t *testing.T) {
	req := &bootstrapRequest{Username: "root@pam", Password: "p", UserID: "nexara@pam"}
	err := req.validate()
	if err == nil {
		t.Fatal("validate accepted a @pam user id for the account being created")
	}
	var fe *fiber.Error
	if !errors.As(err, &fe) || fe.Code != fiber.StatusBadRequest {
		t.Errorf("err = %v, want a 400", err)
	}

	// The LOGIN account is unrestricted — root@pam is the normal answer.
	ok := &bootstrapRequest{Username: "root@pam", Password: "p"}
	if err := ok.validate(); err != nil {
		t.Errorf("validate rejected the default: %v", err)
	}
}

func TestRenderBootstrapErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMarker string
	}{
		{"tfa", &proxmox.TFARequiredError{Username: "root@pam"}, fiber.StatusUnprocessableEntity, "tfa_required"},
		{"token exists", &proxmox.TokenExistsError{UserID: "nexara@pve", TokenName: "nexara"}, fiber.StatusConflict, "token_exists"},
		{"bad password", proxmox.ErrBootstrapAuthFailed, fiber.StatusUnprocessableEntity, "bootstrap_auth_failed"},
		{"not permitted", proxmox.ErrForbidden, fiber.StatusUnprocessableEntity, "bootstrap_forbidden"},
		{"unreachable", proxmox.ErrConnectionFailed, fiber.StatusBadGateway, ""},
		{"bad input", proxmox.ErrInvalidInput, fiber.StatusBadRequest, ""},
		{"anything else", errors.New("boom"), fiber.StatusBadGateway, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/x", func(c fiber.Ctx) error {
				return renderBootstrapError(c, tc.err, &bootstrapRequest{
					Username: "root@pam", Password: "hunter2", OTP: "123456",
					UserID: "nexara@pve", TokenName: "nexara",
				})
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
			if err != nil {
				t.Fatalf("Test: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			body, _ := io.ReadAll(resp.Body)
			if tc.wantMarker != "" {
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("unmarshal %s: %v", body, err)
				}
				if payload["error"] != tc.wantMarker {
					t.Errorf("error = %v, want %q so the dialog can act on it", payload["error"], tc.wantMarker)
				}
			}
			// Whatever the branch, the operator's own credentials must not be
			// echoed back in the message.
			for _, secret := range []string{"hunter2", "123456"} {
				if strings.Contains(string(body), secret) {
					t.Errorf("error response echoed %q: %s", secret, body)
				}
			}
		})
	}
}

// TestGuard_BootstrapPasswordReachesOnlyLogin is a static guard over the
// onboarding handler files: the operator's password and one-time code may be
// read in exactly two places — the emptiness check in validate, and the
// Login call that spends them.
//
// The realistic mistake this catches is someone adding the password to a log
// line, an audit detail or an error message while debugging a failed
// onboarding. Unlike the token secret, this credential belongs to a privileged
// human account and is very likely reused elsewhere.
//
// Limitation, stated plainly: it checks reads of the struct field. Copying the
// password into a local first and leaking that would pass. It is aimed at the
// realistic slip, not at deliberate laundering.
func TestGuard_BootstrapPasswordReachesOnlyLogin(t *testing.T) {
	allowed := map[string]string{
		"validate":            "checks the field is non-empty; never reads its value elsewhere",
		"runClusterBootstrap": "spends the credential on the one /access/ticket call",
		// Tests presence only ("was a code supplied?") to pick the right
		// message; TestRenderBootstrapErrorStatuses asserts no response body
		// ever contains the value.
		"renderBootstrapError": "branches on whether an OTP was supplied; never emits it",
	}

	fset := token.NewFileSet()
	var checked int

	for _, path := range []string{"clusters.go", "clusters_bootstrap.go"} {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name != "Password" && sel.Sel.Name != "OTP" {
					return true
				}
				checked++
				if _, ok := allowed[fn.Name.Name]; !ok {
					t.Errorf("%s:%d: %s reads .%s — the onboarding password and one-time code must not travel beyond the ticket request (no logs, no audit rows, no error strings)",
						path, fset.Position(sel.Pos()).Line, fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}

	if checked == 0 {
		t.Fatal("found no .Password/.OTP reads — the guard would pass vacuously")
	}
}

// TestRenderBootstrapErrorNeverAnswers401 pins the reason those branches use
// 422. The frontend api-client treats any 401 as an expired session: it
// refreshes the token and replays the request, so a 401 here would spend two
// Proxmox login attempts for every one the operator made — enough to trip a
// pveproxy fail2ban jail or a TFA lockout on a single typo.
func TestRenderBootstrapErrorNeverAnswers401(t *testing.T) {
	errs := []error{
		&proxmox.TFARequiredError{Username: "root@pam"},
		&proxmox.TokenExistsError{UserID: "nexara@pve", TokenName: "nexara"},
		proxmox.ErrBootstrapAuthFailed,
		proxmox.ErrForbidden,
		proxmox.ErrInvalidInput,
		proxmox.ErrConnectionFailed,
		errors.New("boom"),
	}

	for _, e := range errs {
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Get("/x", func(c fiber.Ctx) error {
			return renderBootstrapError(c, e, &bootstrapRequest{Username: "root@pam", Password: "p"})
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		if resp.StatusCode == fiber.StatusUnauthorized {
			t.Errorf("%v answered 401 — the api-client would refresh and replay it, doubling the Proxmox login attempt", e)
		}
	}
}

// TestRevokeLeavesTheGrantAloneWhenAnotherClusterSharesTheUser closes the door
// the cascade guard alone leaves open.
//
// Two cluster rows can end up on one PVE user: the first bootstraps and creates
// nexara@pve, the second adopts it under a different token name. Skipping the
// user delete saves the second cluster's token — but the token is privsep=0, so
// it inherits the USER's privileges. Revoking the shared Administrator grant
// therefore takes that cluster offline just as surely, while leaving it listed
// in Nexara looking healthy.
func TestRevokeLeavesTheGrantAloneWhenAnotherClusterSharesTheUser(t *testing.T) {
	var calls []string
	url := pveRevokeStub(t, []string{"nexara", "nexara2"}, &calls)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               url,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
		BootstrapCreatedAcl:  true,
	}, "secret", true)

	for _, c := range calls {
		if c == "PUT /api2/json/access/acl" {
			t.Error("revoked the Administrator grant out from under a cluster that still uses it")
		}
		if c == "DELETE /api2/json/access/users/nexara@pve" {
			t.Error("deleted a user another cluster still authenticates as")
		}
	}
	if !slices.Contains(calls, "DELETE /api2/json/access/users/nexara@pve/token/nexara") {
		t.Errorf("calls = %v, want this cluster's own token removed", calls)
	}

	var sawSkip bool
	for _, step := range steps {
		if step.Step == "user" && step.Status == "skipped" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Errorf("steps = %+v, want the shared account reported as deliberately left intact", steps)
	}
}

// TestRevokeFailsClosedWithoutInventingASibling: when the token listing fails we
// cannot rule out a dependent, so nothing on the account may be touched — but
// the recorded reason must say that, not claim another cluster is using it. This
// text is the only durable record an operator reconciles against.
func TestRevokeFailsClosedWithoutInventingASibling(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/token") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":{"detail":"permission denied"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)

	steps := revokeBootstrapCredentials(context.Background(), db.Cluster{
		ApiUrl:               srv.URL,
		TokenID:              "nexara@pve!nexara",
		CredentialSource:     credentialSourceBootstrap,
		BootstrapUserID:      "nexara@pve",
		BootstrapTokenName:   "nexara",
		BootstrapCreatedUser: true,
		BootstrapCreatedAcl:  true,
	}, "secret", false)

	for _, c := range calls {
		if c == "DELETE /api2/json/access/users/nexara@pve" || c == "PUT /api2/json/access/acl" {
			t.Errorf("acted on the account without being able to rule out a dependent; calls = %v", calls)
		}
	}
	detail := steps[0].Detail
	if !strings.Contains(detail, "could not list") {
		t.Errorf("steps[0].Detail = %q, want it to say the check failed", detail)
	}
	if strings.Contains(detail, "another cluster in Nexara") {
		t.Errorf("steps[0].Detail = %q — a failed check must not be reported as a known sibling", detail)
	}
}

// TestRevokeDialogIntentReachesTheServer: the checkbox is the only place the
// operator expresses intent to mutate a live hypervisor, and one query parameter
// carries it.
func TestRevokeQueryParamIsTheOnlyOptIn(t *testing.T) {
	app := fiber.New()
	var seen []bool
	app.Delete("/c/:id", func(c fiber.Ctx) error {
		seen = append(seen, wantsCredentialRevocation(c))
		return c.SendStatus(fiber.StatusNoContent)
	})
	for _, q := range []string{"", "?revoke_pve_credentials=1"} {
		if _, err := app.Test(httptest.NewRequest(http.MethodDelete, "/c/x"+q, nil)); err != nil {
			t.Fatalf("Test: %v", err)
		}
	}
	if len(seen) != 2 || seen[0] || !seen[1] {
		t.Errorf("opt-in = %v, want [false true]", seen)
	}
}
