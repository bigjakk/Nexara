package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The identity handlers carry more distinct credentials than anywhere else in
// the tree: account passwords, LDAP bind passwords, OIDC client secrets, TOTP
// secrets, single-use recovery codes, refresh tokens, scoped console tokens and
// API keys. Every one of them passes through a function in this list, and every
// one of these files writes audit rows.
//
// audit_log.details is readable by anyone holding view:audit, which every
// built-in Viewer holds by default (see the project's own note on that). So a
// credential reaching an audit row is readable by accounts that cannot see the
// object it belongs to — an API key's owner list, say, or the LDAP config.
//
// This is the access.go pair of guards widened to the domains Phase 6i
// migrated, and it is worth having precisely BECAUSE of that migration: moving
// a handler to declared parameters replaces a hand-written request struct with
// an apischema.Params, which has a Raw() accessor that returns EVERY declared
// parameter at once. `json.Marshal(p.Raw())` is one short line, reads as
// reasonable, and would publish a password.
//
// Limitation, stated plainly: this checks the marshal site only. Assigning a
// secret to a local first and marshalling that would pass. It is aimed at the
// realistic mistake — adding `"password": …` while wiring up a new endpoint —
// not at deliberate laundering.

// identitySecretFiles are the handler files this pair of guards covers.
var identitySecretFiles = []string{
	"auth.go",
	"auth_sessions.go",
	"totp.go",
	"oidc.go",
	"ldap.go",
	"rbac.go",
	"users.go",
	"api_keys.go",
}

// identitySecretishKey matches audit-detail keys that would leak a credential
// in these files. It extends access_test.go's secretishKey with the shapes this
// tranche introduced: the LDAP bind password, the OIDC client secret, TOTP
// secrets and recovery codes, and the refresh/API-key material.
var identitySecretishKey = []string{
	"password", "old_password", "new_password", "bind_password",
	"secret", "client_secret", "token_secret", "totp_secret",
	"recovery_code", "recovery_codes", "refresh_token", "access_token",
	"key", "api_key", "value", "ticket", "otp", "csrf", "privatekey",
}

// identitySecretishField matches a struct field or method whose VALUE is a
// credential, whatever the key it is stored under is called.
var identitySecretishField = []string{
	"password", "passwordhash", "bindpassword", "bindpasswordencrypted",
	"clientsecret", "clientsecretencrypted", "totpsecret", "recoverycode",
	"refreshtoken", "accesstoken", "keyhash", "fullkey", "plainsecret", "secret", "value",
}

func parseIdentityFile(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return fset, file
}

// TestGuard_IdentityAuditDetailsCarryNoSecrets is the static guard: no map
// literal handed to json.Marshal in an identity handler may carry a
// credential-shaped key, and none may read a credential-bearing field.
//
// The `checked == 0` assertion at the end is what keeps this from being
// vacuous. A guard whose input cannot express failure — here, one that found no
// marshal sites at all because a refactor moved them — passes silently, which
// is the shape this codebase has been bitten by before.
func TestGuard_IdentityAuditDetailsCarryNoSecrets(t *testing.T) {
	var checked int

	for _, name := range identitySecretFiles {
		fset, file := parseIdentityFile(t, name)

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
				keyName := strings.ToLower(strings.Trim(key.Value, `"`))
				for _, bad := range identitySecretishKey {
					if keyName == bad {
						t.Errorf("%s:%d: audit details carry key %q — credentials must never reach an "+
							"audit row (view:audit is held by every Viewer)",
							name, fset.Position(key.Pos()).Line, keyName)
					}
				}

				ast.Inspect(kv.Value, func(v ast.Node) bool {
					sel, ok := v.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					field := strings.ToLower(sel.Sel.Name)
					for _, bad := range identitySecretishField {
						if field == bad {
							t.Errorf("%s:%d: audit details read .%s — that is a credential, keep it out "+
								"of the audit row", name, fset.Position(sel.Pos()).Line, sel.Sel.Name)
						}
					}
					return true
				})
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("found no json.Marshal(map literal) across the identity handlers — the guard would pass vacuously")
	}
}

// TestGuard_IdentityHandlersNeverReadTheRawParams is the half the registry
// migration created the need for.
//
// apischema.Params.Raw() returns every declared parameter at once — the login
// password, the LDAP bind password, the OIDC client secret, the TOTP code, the
// API key's requested name and lifetime. Handing it to json.Marshal is one
// short line that reads as reasonable and would write a credential into a row
// every Viewer can read, which is exactly what Raw's own doc comment warns
// against. No call site does this today; the guard is here so none appears.
//
// The rule is absolute — never call Raw() in these files at all — rather than
// "never marshal it", because the laundering version (assign, then marshal the
// local) is the one a static check cannot see.
func TestGuard_IdentityHandlersNeverReadTheRawParams(t *testing.T) {
	for _, name := range identitySecretFiles {
		fset, file := parseIdentityFile(t, name)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || callName(call) != "Raw" {
				return true
			}
			t.Errorf("%s:%d: calls Params.Raw() — it carries every declared parameter, including this "+
				"domain's passwords, bind credentials, client secrets and TOTP codes, and these files' "+
				"audit rows are readable by every Viewer", name, fset.Position(call.Pos()).Line)
			return true
		})
	}
}
