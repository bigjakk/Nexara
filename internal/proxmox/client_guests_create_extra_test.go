package proxmox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"testing"
)

// The owned-key sets are what keeps an extra key from overriding a typed create
// parameter, so they must name every key CreateVM and CreateCT set themselves:
// a form.Set added to either without its key here would reopen the override.
func TestCreateOwnedKeysCoverTheTypedFormKeys(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "client_guests.go", nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for fn, owned := range map[string]map[string]bool{"CreateVM": vmCreateOwnedKeys, "CreateCT": ctCreateOwnedKeys} {
		set := map[string]bool{}
		for _, decl := range file.Decls {
			if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == fn {
				ast.Inspect(d.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) != 2 {
						return true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Set" {
						return true
					}
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						key, _ := strconv.Unquote(lit.Value)
						set[key] = true
					}
					return true
				})
			}
		}
		// The loop over Extra sets a variable key, which the walk does not see.
		if len(set) < 10 {
			t.Fatalf("%s: found %d form.Set keys; the walk no longer sees the function", fn, len(set))
		}
		if got, want := slices.Sorted(maps.Keys(owned)), slices.Sorted(maps.Keys(set)); !slices.Equal(got, want) {
			t.Errorf("%s owned keys = %v, want the keys it sets: %v", fn, got, want)
		}
	}
}
