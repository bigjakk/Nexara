package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The 25 access routes are declared endpoints now
// (internal/api/registry_access.go), so what used to be tested here splits in
// two, the same way the Veeam and PBS routes did.
//
// The PERMISSION each route enforces is a middleware Check attached from its
// declaration, and the parameter rules — a required userid, a malformed body, a
// cluster id that is not a UUID — are the schema's. Both are tested against the
// REAL declarations in internal/api/registry_access_test.go; a bare handler
// mount here could not exercise a gate that no longer sits inside the handler,
// and a test that mounted one anyway would pass while proving nothing.
//
// What stays here is what is still this file's own: the percent-decode of a
// path identifier, the self-credential guard's decision, which user edits count
// as capable of severing access, and the static audit-detail guard.

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

// TestGuard_AccessAuditDetailsNeverReadTheRawParams is the other half of the
// guard above, and it exists because the migration to declared parameters
// created a NEW way to leak the same secret.
//
// apischema.Params.Raw() returns every validated parameter, including
// `password` on the user-create route, and handing it to json.Marshal would
// write that password into a row every Viewer can read — which is exactly what
// Raw's own doc comment warns against. No call site does this today; the guard
// is here so none appears.
func TestGuard_AccessAuditDetailsNeverReadTheRawParams(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Raw" {
			return true
		}
		t.Errorf("access.go:%d: calls Params.Raw() — it carries every declared parameter, "+
			"including the create route's password, and this file's audit rows are readable by every Viewer",
			fset.Position(call.Pos()).Line)
		return true
	})
}

// TestAccessParamDecodesPercentEncoding is a regression test for a
// feature-breaking bug.
//
// Fiber v3 does not percent-decode path params, and the registry hands the
// handler what Fiber matched. A PVE user id is "name@realm", so any correct
// client sends encodeURIComponent("nexara@pve") = "nexara%40pve". Using that
// raw value handed the validator a string containing "%" and no "@", which it
// rejected — so every user, token, group, role and realm lookup 400'd for
// clients doing exactly the right thing.
//
// The decode must stay paired with validation happening afterwards: "%2e%2e"
// decodes to ".." and is rejected by the proxmox client's validators, and the
// outbound path is re-escaped. This test pins the decode; the traversal half is
// pinned by TestAccessMethodsRejectInjectionWithoutIssuingRequest.
func TestAccessParamDecodesPercentEncoding(t *testing.T) {
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
			got, err := accessParam(tc.raw, "userid")
			if err != nil {
				t.Fatalf("accessParam(%q) returned %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("accessParam(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	// The malformed-escape branch. The declared patterns admit only a
	// well-formed "%XX", so no request can reach this today — it stays as
	// defence in depth, and naming the parameter is what makes the 400
	// actionable when something else starts calling this.
	if _, err := accessParam("nexara%zz", "userid"); err == nil {
		t.Error("accessParam accepted a malformed percent-escape")
	} else if !strings.Contains(err.Error(), "userid") {
		t.Errorf("the rejection does not name the parameter: %v", err)
	}
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
