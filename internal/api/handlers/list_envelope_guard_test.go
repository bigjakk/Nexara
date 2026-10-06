package handlers

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
)

// The one-shape rule for collection responses: every list goes out as handlers.ListResponse[T] (via
// RespondItems / RespondList), never a bare JSON array. The API had grown three shapes (bare arrays,
// {items,total}, {entries,total}) with one resource disagreeing across two routes, so every consumer
// needed a per-route type check and bare arrays had nowhere to carry a total. Static analysis, no
// database. Limits: it resolves slice-ness syntactically plus a cross-package index of which
// internal/db/generated and internal/proxmox functions return a slice first; it cannot see through an
// interface, a locally-defined named slice type or an unindexed helper; sliceReturningFuncs keys on the
// bare function name, so a same-named function in another package overwrites it; it inspects only the
// single-argument c.JSON form (TestGuard_NoRawJSONBodies covers c.Send). A regression net, not a proof;
// RespondItems takes []T, so handing it a struct does not build.

// sliceReturningFuncs indexes, by function name, whether the first result of
// every function in dirs is a slice. Handlers get their collections from
// queries.List*/Get* (sqlc-generated) and pxClient.List*/Get* (the Proxmox
// client), so those two packages are what a syntactic pass needs to see.
func sliceReturningFuncs(t *testing.T, dirs ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, dir := range dirs {
		_, files := parseGoFiles(t, dir)
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
					continue
				}
				arr, isArr := fn.Type.Results.List[0].Type.(*ast.ArrayType)
				// Len != nil is a fixed-size array, which marshals as a JSON
				// array too, but nothing here returns one.
				out[fn.Name.Name] = isArr && arr.Len == nil
			}
		}
	}
	return out
}

// isSliceExpr reports whether expr is a collection, judged from the enclosing
// function body plus the cross-package index.
func isSliceExpr(expr ast.Expr, body *ast.BlockStmt, sliceFuncs map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		arr, ok := e.Type.(*ast.ArrayType)
		return ok && arr.Len == nil
	case *ast.CallExpr:
		return sliceFuncs[callName(e)]
	case *ast.Ident:
		return identIsSlice(e.Name, body, sliceFuncs)
	}
	return false
}

// identIsSlice walks the function body for what name was bound to.
func identIsSlice(name string, body *ast.BlockStmt, sliceFuncs map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name {
					continue
				}
				// x, err := f() binds x to f's first result.
				if len(s.Rhs) == 1 {
					if isSliceExpr(s.Rhs[0], body, sliceFuncs) {
						found = true
					}
					continue
				}
				if i < len(s.Rhs) && isSliceExpr(s.Rhs[i], body, sliceFuncs) {
					found = true
				}
			}
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil {
					continue
				}
				arr, isArr := vs.Type.(*ast.ArrayType)
				if !isArr || arr.Len != nil {
					continue
				}
				for _, id := range vs.Names {
					if id.Name == name {
						found = true
					}
				}
			}
		case *ast.CallExpr:
			// append(x, …) only type-checks when x is a slice.
			if id, ok := s.Fun.(*ast.Ident); ok && id.Name == "append" && len(s.Args) > 0 {
				if arg, ok := s.Args[0].(*ast.Ident); ok && arg.Name == name {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// isFiberCtxChain reports whether expr is the request context `c`, or a
// method chain rooted at it (`c.Status(…)`).
func isFiberCtxChain(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "c"
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			return isFiberCtxChain(sel.X)
		}
	}
	return false
}

// TestGuard_NoRawJSONBodies exists because the envelope sweep missed a live endpoint: ACME's
// challenge-schema handler relayed Proxmox's response with `c.Send(raw)`, a bare array that never
// touched c.JSON, while the frontend had already switched to the unwrapping client (a permanently
// broken DNS-provider picker). The rule is simply: don't write a JSON body without c.JSON; decode into
// []json.RawMessage and hand it to RespondItems, as that endpoint now does.
func TestGuard_NoRawJSONBodies(t *testing.T) {
	fset, files := parseGoFiles(t, ".")

	var offenders []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Send" || !isFiberCtxChain(sel.X) {
				return true
			}
			offenders = append(offenders, fset.Position(call.Pos()).String())
			return true
		})
	}

	if len(offenders) > 0 {
		t.Errorf("c.Send writes a response body that no envelope guard can inspect.\n"+
			"If the payload is a collection, decode it (…[]json.RawMessage is fine for an\n"+
			"opaque passthrough) and return RespondItems(c, items) instead.\n\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestGuard_ListEndpointsUseEnvelope fails if any handler passes a collection
// straight to c.JSON instead of RespondItems / RespondList.
func TestGuard_ListEndpointsUseEnvelope(t *testing.T) {
	sliceFuncs := sliceReturningFuncs(t,
		filepath.FromSlash("../../db/generated"),
		filepath.FromSlash("../../proxmox"),
	)
	// Handlers also return collections built by same-package helpers
	// (toXResponses converters and the like).
	for name, isSlice := range sliceReturningFuncs(t, ".") {
		if _, seen := sliceFuncs[name]; !seen {
			sliceFuncs[name] = isSlice
		}
	}

	fset, files := parseGoFiles(t, ".")

	var offenders []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			body := fn.Body
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "JSON" {
					return true
				}
				// Matches both `c.JSON(x)` and `c.Status(…).JSON(x)`; the
				// latter's receiver is a CallExpr, not an Ident, and checking
				// only for the Ident form silently exempted it.
				if !isFiberCtxChain(sel.X) {
					return true
				}
				if !isSliceExpr(call.Args[0], body, sliceFuncs) {
					return true
				}
				offenders = append(offenders, fmt.Sprintf(
					"%s: %s returns a bare JSON array", fset.Position(call.Pos()), fn.Name.Name))
				return true
			})
		}
	}

	if len(offenders) > 0 {
		t.Errorf("collection responses must use the ListResponse envelope, not a bare array.\n"+
			"Replace `return c.JSON(items)` with `return RespondItems(c, items)` — or\n"+
			"`RespondList(c, items, total)` when a separate count query bounds the page.\n\n%s",
			strings.Join(offenders, "\n"))
	}
}
