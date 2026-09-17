package handlers

import (
	"go/ast"
	"strings"
	"testing"
)

// A confirm gate that writes its own response does not gate anything.
//
// fiber's c.Status(...).JSON(...) returns nil on success, so a function shaped
//
//	func requireThing(c fiber.Ctx, ...) error {
//	    ...
//	    return c.Status(422).JSON(...)   // returns nil!
//	}
//
// leaves its caller's `if err != nil` false. The handler runs on and performs
// the action the gate just "refused", and the 422 status makes the response
// look like a refusal while carrying the result of the thing it did. That
// shipped in the first draft of requireLDAPTransportAck and was reproduced
// immediately afterwards in requireSSHTrustResetAck, which is two for two.
//
// The rule this pins: functions named require* decide, they do not respond.
// Rendering belongs in render*, whose whole job is to turn the returned error
// into a response — see renderConfirmRequired and renderAddressPolicyError.
func TestGuard_RequireGatesDoNotWriteResponses(t *testing.T) {
	fset, files := parseGoFiles(t, ".")

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isRequireGateName(fn.Name.Name) {
				continue
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				// Any c.Status(...) / c.JSON(...) / c.SendStatus(...) inside a
				// require* function is the shape being banned.
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if !isResponseWriter(sel.Sel.Name) {
					return true
				}
				pos := fset.Position(call.Pos())
				t.Errorf("%s: %q writes a response with %s(...). A require* gate must RETURN "+
					"its refusal so the caller's `if err != nil` can stop the handler — "+
					"c.Status(...).JSON(...) returns nil, so writing it here silently lets the "+
					"action proceed. Return a *confirmRequiredError and render it at the call "+
					"site with renderConfirmRequired.", pos, fn.Name.Name, sel.Sel.Name)
				return true
			})
		}
	}
}

// isRequireGateName reports whether a function name is a require* gate.
//
// The match is case-INSENSITIVE on the prefix, because this package now
// has both spellings: the unexported requireClusterPerm and friends, and
// the exported RequirePermission / RequireClusterPermission /
// RequireAnyPermission that the declarative registry attaches as route
// middleware. A case-sensitive "require" prefix saw only the first set,
// so the three functions that decide authorization for every registry
// route sat outside the guard that exists to catch a gate writing its own
// response and returning nil.
//
// They are, if anything, the ones that matter most: a middleware that
// answers 403 by writing the response and returning nil would call
// c.Next() — or fall through to it — and run the handler anyway, with the
// caller already looking at a refusal.
func isRequireGateName(name string) bool {
	return len(name) >= len("require") && strings.EqualFold(name[:len("require")], "require")
}

// TestIsRequireGateName pins the case-insensitivity, and that the guard
// reaches the exported middleware constructors specifically — which is
// the gap this check was widened to close.
func TestIsRequireGateName(t *testing.T) {
	for _, name := range []string{
		"requireClusterPerm", "requirePerm", "requireLDAPTransportAck",
		"RequirePermission", "RequireClusterPermission", "RequireAnyPermission",
	} {
		if !isRequireGateName(name) {
			t.Errorf("%s is a require* gate but the guard does not see it", name)
		}
	}
	for _, name := range []string{"resolveVM", "req", "Requir", "", "renderConfirmRequired"} {
		if isRequireGateName(name) {
			t.Errorf("%s is not a require* gate but the guard treats it as one", name)
		}
	}
}

// isResponseWriter reports whether a method name commits an HTTP response.
//
// Every spelling matters, not just the one that caused the bug. c.Status(422)
// .JSON(...) is caught by Status, but a bare `return c.JSON(...)` inside a gate
// has the identical failure — nil return, response committed — and would
// otherwise sail through.
func isResponseWriter(name string) bool {
	switch name {
	case "Status", "SendStatus", "SendString", "SendStream", "SendFile", "Send",
		"Redirect", "JSON", "JSONP", "XML", "CBOR", "MsgPack", "Format",
		"Write", "WriteString", "Writef", "End":
		return true
	default:
		return false
	}
}
