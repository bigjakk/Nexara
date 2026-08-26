package handlers

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func newAccessTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewAccessHandler(nil, testEncryptionKey, nil)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	g := app.Group("/clusters/:cluster_id/access")
	g.Get("/users", handler.ListUsers)
	g.Post("/users", handler.CreateUser)
	g.Get("/users/:userid", handler.GetUser)
	g.Delete("/users/:userid", handler.DeleteUser)
	g.Post("/users/:userid/tokens/:tokenid", handler.CreateToken)
	g.Get("/groups", handler.ListGroups)
	g.Post("/groups", handler.CreateGroup)
	g.Post("/roles", handler.CreateRole)
	g.Put("/acl", handler.UpdateACL)
	g.Get("/permissions", handler.GetPermissions)

	return app
}

func doAccessReq(t *testing.T, app *fiber.App, method, path, role, body string) *http.Response {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5_000_000_000})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// TestAccessRoutesRequirePermission covers the gate every route shares. The
// stub engine grants everything to "admin" and nothing to anyone else, so a
// non-admin role must be refused before the handler reaches Proxmox or the DB.
func TestAccessRoutesRequirePermission(t *testing.T) {
	app := newAccessTestApp(t)
	cid := uuid.New().String()
	base := "/clusters/" + cid + "/access"

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, base + "/users", ""},
		{http.MethodPost, base + "/users", `{"userid":"a@pve"}`},
		{http.MethodGet, base + "/users/a@pve", ""},
		{http.MethodDelete, base + "/users/a@pve", ""},
		{http.MethodPost, base + "/users/a@pve/tokens/tok", `{}`},
		{http.MethodGet, base + "/groups", ""},
		{http.MethodPost, base + "/groups", `{"groupid":"g"}`},
		{http.MethodPost, base + "/roles", `{"roleid":"r"}`},
		{http.MethodPut, base + "/acl", `{"path":"/","roles":"PVEAdmin","users":"a@pve"}`},
		{http.MethodGet, base + "/permissions", ""},
	}

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			resp := doAccessReq(t, app, r.method, r.path, "viewer", r.body)
			if resp.StatusCode != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 for a non-admin role", resp.StatusCode)
			}
		})
	}
}

// TestAccessValidationRejectsBeforeProxmox pins the validation that runs before
// any Proxmox client is built. With nil queries, reaching the client would
// panic or 5xx — a 400 proves the handler stopped at validation.
func TestAccessValidationRejectsBeforeProxmox(t *testing.T) {
	app := newAccessTestApp(t)
	cid := uuid.New().String()
	base := "/clusters/" + cid + "/access"

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"user without userid", http.MethodPost, base + "/users", `{"comment":"x"}`},
		{"group without groupid", http.MethodPost, base + "/groups", `{"comment":"x"}`},
		{"role without roleid", http.MethodPost, base + "/roles", `{"privs":"VM.Audit"}`},
		{"malformed user body", http.MethodPost, base + "/users", `{"userid":`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doAccessReq(t, app, tc.method, tc.path, "admin", tc.body)
			if resp.StatusCode != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestAccessRejectsInvalidClusterID(t *testing.T) {
	app := newAccessTestApp(t)
	resp := doAccessReq(t, app, http.MethodGet, "/clusters/not-a-uuid/access/users", "admin", "")
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed cluster id", resp.StatusCode)
	}
}

func TestSplitFullTokenID(t *testing.T) {
	tests := []struct {
		in        string
		user, tok string
		ok        bool
	}{
		{"nexara@pve!api", "nexara@pve", "api", true},
		{"root@pam!nexara", "root@pam", "nexara", true},
		{"root@pam", "", "", false},
		{"!api", "", "", false},
		{"root@pam!", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range tests {
		u, k, ok := splitFullTokenID(tc.in)
		if ok != tc.ok || u != tc.user || k != tc.tok {
			t.Errorf("splitFullTokenID(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, u, k, ok, tc.user, tc.tok, tc.ok)
		}
	}
}

// TestSelfCredentialSubject covers the guard that stops Nexara destroying its
// own cluster credential. The empty-tokenid cases matter most: deleting a user
// takes its tokens with it, so that collides on the user alone.
func TestSelfCredentialSubject(t *testing.T) {
	const own = "nexara@pve!api"

	tests := []struct {
		name            string
		ownTokenID      string
		userid, tokenid string
		wantConflict    bool
		wantSubject     string
	}{
		{"exact token match", own, "nexara@pve", "api", true, "token nexara@pve!api"},
		{"case-insensitive", own, "NEXARA@PVE", "API", true, "token nexara@pve!api"},
		{"user delete takes our token", own, "nexara@pve", "", true, "user nexara@pve"},
		{"different token, same user", own, "nexara@pve", "other", false, ""},
		{"different user", own, "alice@pve", "api", false, ""},
		{"different user, whole-user delete", own, "alice@pve", "", false, ""},
		{"cluster token id has no bang", "nexara@pve", "nexara@pve", "api", false, ""},
		{"cluster token id empty", "", "nexara@pve", "api", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, conflict := selfCredentialSubject(tc.ownTokenID, tc.userid, tc.tokenid)
			if conflict != tc.wantConflict {
				t.Fatalf("conflict = %v, want %v", conflict, tc.wantConflict)
			}
			if conflict && subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
		})
	}
}

func TestForceRequested(t *testing.T) {
	app := fiber.New()
	var got []bool
	app.Get("/x", func(c fiber.Ctx) error {
		got = append(got, forceRequested(c))
		return c.SendStatus(fiber.StatusOK)
	})

	for _, q := range []string{"", "?force=true", "?force=TRUE", "?force=1", "?force=0", "?force=no"} {
		if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/x"+q, nil)); err != nil {
			t.Fatalf("Test(%q): %v", q, err)
		}
	}
	want := []bool{false, true, true, true, false, false}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("case %d: forceRequested = %v, want %v", i, got[i], w)
		}
	}
}

// secretishKey matches audit-detail keys that would leak a credential.
var secretishKey = []string{"password", "secret", "token_secret", "value", "ticket", "otp", "csrf", "privatekey", "bind_password"}

// TestGuard_AccessAuditDetailsCarryNoSecrets is a static guard over access.go:
// no map literal handed to json.Marshal may carry a credential-shaped key, and
// none may read a credential-bearing field.
//
// This matters more here than almost anywhere else in the codebase. Proxmox
// returns a token secret exactly once, this file is where that value lives, and
// audit rows are readable by anyone holding view:audit — which every built-in
// Viewer does. A secret reaching an audit detail would be readable by
// accounts that cannot even see the token list.
//
// Static rather than behavioural because the detail maps are built inline at
// twelve call sites; a runtime test would have to reach Proxmox at each one.
//
// Limitation, stated plainly: this checks the marshal site only. Assigning a
// secret to a local first and marshalling that would pass. It is aimed at the
// realistic mistake — someone adding `"secret": created.Value` while wiring up
// a new endpoint — not at deliberate laundering. Keeping the rule absolute
// (never read .Value/.Password here at all) is what makes it worth having; the
// one legitimate derived value, has_password, is computed before the literal
// and commented as such.
func TestGuard_AccessAuditDetailsCarryNoSecrets(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}

	var checked int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Marshal" || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		checked++

		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok {
				continue
			}
			name := strings.ToLower(strings.Trim(key.Value, `"`))
			for _, bad := range secretishKey {
				if name == bad {
					t.Errorf("%s:%d: audit details carry key %q — token secrets and passwords must never reach an audit row (view:audit is held by every Viewer)",
						"access.go", fset.Position(key.Pos()).Line, name)
				}
			}

			// Also reject reading a credential-bearing field, whatever the
			// key is called: details{"x": created.Value} is the same leak.
			ast.Inspect(kv.Value, func(v ast.Node) bool {
				sel, ok := v.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				field := strings.ToLower(sel.Sel.Name)
				if field == "value" || field == "password" || field == "tokensecret" || field == "secret" {
					t.Errorf("%s:%d: audit details read .%s — that is a credential, keep it out of the audit row",
						"access.go", fset.Position(sel.Pos()).Line, sel.Sel.Name)
				}
				return true
			})
		}
		return true
	})

	if checked == 0 {
		t.Fatal("found no json.Marshal(map literal) in access.go — the guard would pass vacuously")
	}
}

// TestAccessParamDecodesPercentEncoding is a regression test for a
// feature-breaking bug.
//
// Fiber v3 does not percent-decode path params: c.Params returns the raw
// segment. A PVE user id is "name@realm", so any correct client sends
// encodeURIComponent("nexara@pve") = "nexara%40pve". Reading c.Params directly
// handed the validator a string containing "%" and no "@", which it rejected —
// so every user, token, group, role and realm lookup 400'd for clients doing
// exactly the right thing.
//
// The decode must stay paired with validation happening afterwards: "%2e%2e"
// decodes to ".." and is rejected by the proxmox client's validators, and the
// outbound path is re-escaped. This test pins the decode; the traversal half is
// pinned by TestAccessMethodsRejectInjectionWithoutIssuingRequest.
func TestAccessParamDecodesPercentEncoding(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	var got string
	app.Get("/u/:userid", func(c fiber.Ctx) error {
		v, err := accessParam(c, "userid")
		if err != nil {
			return err
		}
		got = v
		return c.SendStatus(fiber.StatusOK)
	})

	tests := []struct {
		raw  string
		want string
	}{
		{"nexara%40pve", "nexara@pve"},
		{"nexara@pve", "nexara@pve"},
		{"root%40pam", "root@pam"},
		{"first.last-1%40pve", "first.last-1@pve"},
		// Decodes to a traversal; the proxmox-client validators reject it
		// downstream, which is why decoding first is safe.
		{"%2e%2e", ".."},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got = ""
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/u/"+tc.raw, nil))
			if err != nil {
				t.Fatalf("Test: %v", err)
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got != tc.want {
				t.Errorf("accessParam(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	// The malformed-escape branch (url.PathUnescape returning an error) is not
	// exercised here: httptest.NewRequest panics on a URL like "/u/nexara%zz", and
	// for the same reason Go's http.Server rejects it before routing. The branch
	// stays as defence in depth, not because a request can reach it today.
}

// TestAccessUpdateAffectsAccess covers which user edits are treated as capable
// of severing Nexara's own cluster access.
//
// The distinction matters in both directions: guarding too little lets a PUT
// disable nexara@pve with no confirmation (PVE validates the owning user when
// verifying a token, so every later call 401s), while guarding too much makes
// routine comment edits demand a force flag and trains operators to pass it
// without reading.
func TestAccessUpdateAffectsAccess(t *testing.T) {
	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name string
		req  proxmox.UpdateAccessUserParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessUserParams{}, false},
		{"comment only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Comment: str("hi")},
		}, false},
		{"email only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Email: str("a@b.c")},
		}, false},
		{"explicitly enabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(true)},
		}, false},
		{"expire cleared to never", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(0)},
		}, false},

		{"disabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(false)},
		}, true},
		{"setting an expiry", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(1767225600)},
		}, true},
		{"changing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("admins")},
		}, true},
		{"clearing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("")},
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}
