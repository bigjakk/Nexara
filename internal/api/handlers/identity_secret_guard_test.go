package handlers

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

// The identity handlers carry the most distinct credentials in the tree (passwords, LDAP bind
// passwords, OIDC client secrets, TOTP secrets, recovery codes, refresh tokens, console tokens, API
// keys), every one passes through a function listed here, and every file writes audit rows.
// audit_log.details is readable by anyone with view:audit, which every built-in Viewer holds, so a
// credential reaching a row is readable by accounts that cannot see its object. This is the access.go
// pair of guards widened to the domains Phase 6i migrated, and worth having because of it: Params has a
// Raw() accessor returning EVERY declared parameter, and `json.Marshal(p.Raw())` is one reasonable-looking
// line that would publish a password. Limitation: it checks the marshal site only (assigning a secret to
// a local first would pass); it is aimed at adding `"password": ...` while wiring a new endpoint.

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
	fset := guardFset
	file, err := guardParsed(name)
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

// TestGuard_IdentityHandlersNeverReadTheRawParams is the half the registry migration created the need
// for: Params.Raw() returns every declared parameter at once (the login password, the LDAP bind password,
// the OIDC client secret, the TOTP code, the API key's name and lifetime), and handing it to json.Marshal
// would write a credential into a row every Viewer can read. No call site does today. The rule is
// absolute (never call Raw() in these files) rather than "never marshal it", because the laundering
// version (assign, then marshal the local) is the one a static check cannot see.
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
