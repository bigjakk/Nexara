package veeam

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// transportConstructorExempt names the functions allowed to build an
// http.Transport or http.Client. Everything else in this package must go
// through buildHTTPClient.
//
// buildHTTPClient is the sole owner of the SSRF dial guard, TLS fingerprint
// pinning (with session tickets disabled), and redirect refusal. A second
// constructor anywhere in this package silently forks that hardening: the next
// person to tighten one copy has no way to know the other exists, and the
// forked client is the one that carries an admin password to an
// operator-supplied URL.
//
// internal/proxmox pins the same invariant for the same reason; the two
// packages each need their own guard because each owns its own transport.
var transportConstructorExempt = map[string]string{
	"buildHTTPClient": "sole owner of the hardened transport",
}

func TestGuard_SingleTransportConstructor(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no .go files found — the guard would pass vacuously")
	}

	var checked int
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, exempt := transportConstructorExempt[fn.Name.Name]; exempt {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "http" {
					return true
				}
				if sel.Sel.Name != "Transport" && sel.Sel.Name != "Client" {
					return true
				}
				t.Errorf(
					"%s: %s constructs an http.%s directly. Every outbound Veeam client must come from buildHTTPClient, which owns the SSRF dial guard, TLS pinning and redirect refusal. Add it to transportConstructorExempt only with a reason.",
					fset.Position(lit.Pos()), fn.Name.Name, sel.Sel.Name)
				return true
			})
		}
	}

	if checked == 0 {
		t.Fatal("no non-test files parsed — the guard would pass vacuously")
	}
}
