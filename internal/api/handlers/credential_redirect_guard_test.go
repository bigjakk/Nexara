package handlers

import (
	"go/ast"
	"sort"
	"strings"
	"testing"
)

// Static-analysis guard, in the same spirit as tracktask_guard_test.go: no
// database, no running server, so it fails CI the moment a handler grows the
// stored-credential/mutable-address pairing without the refusal.
//
// The tell is narrow and deliberate. An Update handler that lets a caller move
// an address while keeping the secret has to carry the old ciphertext forward
// from the row it just loaded — `existing.<Something>Encrypted`. Every handler
// in this package that does so must also consult credentialRedirected, use the
// result, and do it before the write.
//
// BE CLEAR ABOUT WHAT THIS DOES NOT CATCH. It is a check against copy-pasting
// an existing Update handler, not a proof:
//
//   - It keys on the `existing` variable name. A handler that names its loaded
//     row `srv`, `cfg` or `creds` is invisible to it.
//   - It only sees an address and a credential in the SAME row. The pairing can
//     span tables: cluster_ssh_credentials stores a key against a cluster_id,
//     while the address it is delivered to lives in nodes.address, which the
//     collector fills from whatever the cluster's api_url reports. Nothing here
//     models that, and credentialRedirected would not fix it.
//   - It cannot tell that the arguments are the right ones.
//
// See credential_redirect.go for the rule itself.

// storedCredentialCarriers returns the name of every function in the package
// that carries a stored ciphertext forward from a loaded row, mapped to
// whether it gates on credentialRedirected in a way that is actually load
// bearing (result used in a condition, ahead of the persisting write).
func storedCredentialCarriers(t *testing.T) map[string]bool {
	t.Helper()

	_, files := parseGoFiles(t, ".")
	carriers := map[string]bool{}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			var carries bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "existing" &&
					isEncryptedField(sel.Sel.Name) {
					carries = true
				}
				return true
			})
			if !carries {
				continue
			}

			carriers[qualifiedFuncName(fn)] = guardedByRedirectCheck(fn)
		}
	}
	return carriers
}

// guardedByRedirectCheck reports whether fn consults credentialRedirected in a
// way that can actually refuse the request: the result reaches an `if`
// condition, and the branch is reached before the row is persisted. A bare call
// whose result is dropped, or one that runs after the write, is not a guard —
// both would sail past a check that only looked for the call.
//
// Two spellings count. Calling it inline in the condition is the common one;
// VeeamHandler.Update assigns it to a local first, because the value is
// computed where the plaintext password is still in scope and consumed further
// down, so the assigned identifier is followed too.
func guardedByRedirectCheck(fn *ast.FuncDecl) bool {
	// Locals holding the result of credentialRedirected(...).
	redirectVars := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || callName(call) != "credentialRedirected" {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
			redirectVars[ident.Name] = true
		}
		return true
	})

	var condPos, writePos int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ifStmt, ok := n.(*ast.IfStmt); ok && ifStmt.Cond != nil {
			ast.Inspect(ifStmt.Cond, func(c ast.Node) bool {
				switch node := c.(type) {
				case *ast.CallExpr:
					if callName(node) == "credentialRedirected" {
						if condPos == 0 || int(ifStmt.Pos()) < condPos {
							condPos = int(ifStmt.Pos())
						}
					}
				case *ast.Ident:
					if redirectVars[node.Name] {
						if condPos == 0 || int(ifStmt.Pos()) < condPos {
							condPos = int(ifStmt.Pos())
						}
					}
				}
				return true
			})
		}
		// The persisting write: h.queries.Update<Something>(...).
		if call, ok := n.(*ast.CallExpr); ok {
			if name := callName(call); strings.HasPrefix(name, "Update") && name != "Update" {
				if writePos == 0 || int(call.Pos()) < writePos {
					writePos = int(call.Pos())
				}
			}
		}
		return true
	})

	if condPos == 0 {
		return false
	}
	return writePos == 0 || condPos < writePos
}

func TestGuard_StoredCredentialCarriersRefuseRedirects(t *testing.T) {
	// The ONLY sanctioned way to skip the check. Keep it tiny and documented.
	exempt := map[string]string{
		// The address and the secret live in ONE encrypted blob
		// (notification_channels.config_encrypted) that req.Config replaces
		// wholesale — there is no field-level merge. A caller cannot move the
		// endpoint while keeping the token, because changing either means
		// supplying both, so the pairing this guard looks for does not exist.
		"AlertHandler.UpdateChannel": "config_encrypted is an atomic blob; address and secret are replaced together",
	}

	carriers := storedCredentialCarriers(t)

	for name, guarded := range carriers {
		if guarded {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		t.Errorf("%q carries a stored credential forward from an existing row but does not "+
			"gate on credentialRedirected before persisting. A caller who may edit the "+
			"address but has never seen the secret could re-point the row at a host they "+
			"control and have Nexara deliver the credential there. Add the check (see "+
			"credential_redirect.go), or add a documented entry to the exempt map.", name)
	}

	// A stale exemption is worse than none: it silently blesses a handler that
	// may have been rewritten into the vulnerable shape since it was added.
	for name := range exempt {
		if _, ok := carriers[name]; !ok {
			t.Errorf("exempt entry %q no longer carries a stored credential — remove it, "+
				"so the exemption list stays a statement about live code", name)
		}
	}
}

// The guard is worthless if its own detector stops matching — a renamed field
// convention or a reworked AST walk would make it pass by finding nothing at
// all. Pin the handlers it is known to cover, by NAME rather than by a count
// of matching expressions (several carriers reference the ciphertext more than
// once, so a raw occurrence count would stay above any threshold even after
// most of the handlers slipped out of view).
func TestGuard_CredentialRedirectGuardStillSeesKnownCarriers(t *testing.T) {
	carriers := storedCredentialCarriers(t)

	// Every handler the fix covers, by name. AlertHandler.UpdateChannel is
	// exempt from the refusal but must still be SEEN by the detector — that is
	// what keeps its exemption meaningful.
	want := []string{
		"ClusterHandler.Update",
		"PBSHandler.Update",
		"VeeamHandler.Update",
		"LDAPHandler.Update",
		"OIDCHandler.Update",
		"AlertHandler.UpdateChannel",
	}
	for _, name := range want {
		if _, ok := carriers[name]; !ok {
			got := make([]string, 0, len(carriers))
			for k := range carriers {
				got = append(got, k)
			}
			sort.Strings(got)
			t.Errorf("detector no longer sees %q as a stored-credential carrier (saw %v) — "+
				"the schema's Encrypted naming or the `existing` variable convention likely "+
				"changed, which would let TestGuard_StoredCredentialCarriersRefuseRedirects "+
				"pass by matching nothing", name, got)
		}
	}
}

// qualifiedFuncName renders a method as `ReceiverType.Method` so the five
// handlers that each call their method Update stay distinct.
func qualifiedFuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// isEncryptedField reports whether a struct field name holds a stored
// ciphertext. The schema uses both conventions — TokenSecretEncrypted,
// PasswordEncrypted, BindPasswordEncrypted, ClientSecretEncrypted, and also
// EncryptedPassword / EncryptedPrivateKey on cluster_ssh_credentials — so
// match on either side rather than the suffix alone.
func isEncryptedField(name string) bool {
	return strings.Contains(name, "Encrypted")
}
