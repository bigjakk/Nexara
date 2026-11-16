package proxmox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every method that MINTS a snapshot (`form.Set("snapname", …)`) must call
// ValidateSnapshotName: the scheduler and guesttools were once written without it. Delete
// and rollback address a snapshot PVE already minted and must stay outside (validatePathSegment
// and client_guests_snapshot_address_test.go cover them). Not caught: a field name from a
// variable, a raw doPostRaw body, or a call whose error is dropped (the wire tests hold that).
const snapshotNameValidator = "ValidateSnapshotName"

// minSnapshotMintingMethods is the count below which this guard has stopped
// guarding anything.
//
// Without it the test passes trivially the day someone renames the form field
// or moves these methods to a file this walk does not read: zero matches means
// zero findings means green. A guard whose input cannot express "I found
// nothing to check" always takes the happy branch.
const minSnapshotMintingMethods = 2

// setsSnapnameFormField reports whether fn contains a `something.Set("snapname",
// …)` call — the one shape that puts a caller-supplied snapshot name into a
// POST body.
func setsSnapnameFormField(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Set" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if key, err := strconv.Unquote(lit.Value); err == nil && key == "snapname" {
			found = true
		}
		return true
	})
	return found
}

// callsSnapshotNameValidator reports whether fn calls ValidateSnapshotName,
// under either spelling — bare inside this package, or qualified, in case the
// guard ever reads a file that imports it.
func callsSnapshotNameValidator(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			if v.Name == snapshotNameValidator {
				found = true
			}
		case *ast.SelectorExpr:
			if v.Sel.Name == snapshotNameValidator {
				found = true
			}
		}
		return true
	})
	return found
}

func TestGuard_SnapshotNameValidatedAtEveryCreate(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	var minting, unguarded []string

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !setsSnapnameFormField(fn) {
				continue
			}
			minting = append(minting, fn.Name.Name)
			if !callsSnapshotNameValidator(fn) {
				unguarded = append(unguarded, path+": "+fn.Name.Name)
			}
		}
	}

	sort.Strings(minting)
	sort.Strings(unguarded)

	if len(minting) < minSnapshotMintingMethods {
		t.Fatalf("found %d method(s) setting the snapname form field %v, want at least %d — "+
			"either the field was renamed or these methods moved out of this package, and "+
			"this guard is now green because it has nothing to look at",
			len(minting), minting, minSnapshotMintingMethods)
	}

	for _, finding := range unguarded {
		t.Errorf("%s puts a caller-supplied snapshot name in the request body without calling %s. "+
			"The handlers are not the only caller — internal/scheduler and internal/guesttools "+
			"create snapshots without passing through one — so the check belongs here, at the "+
			"method, or the next caller silently skips it.", finding, snapshotNameValidator)
	}
}
