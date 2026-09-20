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

// Static-analysis guard, in the shape of upid_signature_guard_test.go: no
// server, no client, just the package's own source.
//
// TestCreateSnapshotMethods_RefuseBadNamesBeforeTheWire proves the TWO methods
// that exist today consult ValidateSnapshotName. This is what covers the third
// one, written next year by someone who copied CreateVMSnapshot and deleted the
// part they did not recognise. That is not hypothetical here either: the rule
// used to live in internal/api/handlers, and both non-handler callers —
// internal/scheduler and internal/guesttools — were written without it, one of
// them producing a scheduled snapshot task that failed on every fire.
//
// SCOPE, and the boundary is the whole point:
//
// It keys on `form.Set("snapname", …)` — a method that PUTS a name into the
// request body, i.e. one that MINTS a snapshot. It deliberately does NOT key on
// the path, because DeleteVMSnapshot, RollbackVMSnapshot, DeleteCTSnapshot and
// RollbackCTSnapshot interpolate a snapshot name into the URL and must stay
// OUTSIDE this rule. Those address a snapshot Proxmox already minted, and a
// create-side validator there would strand any snapshot whose name predates
// this code or came from another tool — the invented-strictness shape, and the
// reason registry_vms.go declares the addressing side with MaxLength 128 and no
// two-character minimum. Their own (separate) path-guard gap — the snapshot
// name is a PATH segment there, so "." and ".." had to be refused even though
// the create rule must not be — is closed by validatePathSegment and covered by
// client_guests_snapshot_address_test.go, not by this guard.
//
// WHAT IT DOES NOT CATCH, stated rather than implied: it matches the literal
// `X.Set("snapname", …)` shape, so building the field name from a variable, or
// posting a raw body with doPostRaw, walks past it. And it proves the validator
// is CALLED in the same function, not that its error is returned — the wire
// tests above are what hold that half.
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
