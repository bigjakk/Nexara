package handlers

import (
	"go/ast"
	"go/token"
	"testing"
)

// The detach audit row records what a slot held, which it can do only if the lookup runs BEFORE the
// detach: afterwards the key is gone and every row reverts to "no such key in the VM config", the
// empty-row defect this was written to fix, with both unit tests still green. That reordering is a
// one-line edit with no other symptom, and there is no seam to test it through (DetachDisk builds a real
// *proxmox.Client out of DB rows via resolveVM), so this reads the source: it proves the calls appear in
// that order in the function's text, not that they execute in it (a resolution inside a conditional that
// never runs would pass). It catches the edit that would actually be made.
func TestGuard_DetachResolvesTheVolumeBeforeDetaching(t *testing.T) {
	fset := guardFset
	file, err := guardParsed("vms.go")
	if err != nil {
		t.Fatalf("parse vms.go: %v", err)
	}

	fn := findMethod(file, "VMHandler", "DetachDisk")
	if fn == nil {
		t.Fatal("VMHandler.DetachDisk not found in vms.go — if it moved, move this guard with it")
	}

	var resolvePos, detachPos token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == "detachedVolume" && !resolvePos.IsValid() {
				resolvePos = call.Pos()
			}
		case *ast.SelectorExpr:
			if f.Sel.Name == "DetachDisk" && !detachPos.IsValid() {
				detachPos = call.Pos()
			}
		}
		return true
	})

	if !resolvePos.IsValid() {
		t.Fatal("VMHandler.DetachDisk no longer calls detachedVolume — the audit row cannot name what was detached, " +
			"and for an unusedN key or a cloud-init drive that is a volume removed from storage with no record of which")
	}
	if !detachPos.IsValid() {
		t.Fatal("VMHandler.DetachDisk no longer calls a DetachDisk client method; this guard needs updating")
	}
	if resolvePos > detachPos {
		t.Errorf("detachedVolume is called at %s, AFTER the detach at %s — "+
			"the slot is gone by then, so every audit row reverts to %q. Resolve first.",
			fset.Position(resolvePos), fset.Position(detachPos), notInConfig)
	}
}

// findMethod returns the named method on the named receiver type.
func findMethod(file *ast.File, recv, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		for _, f := range fn.Recv.List {
			t := f.Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if id, ok := t.(*ast.Ident); ok && id.Name == recv {
				return fn
			}
		}
	}
	return nil
}
