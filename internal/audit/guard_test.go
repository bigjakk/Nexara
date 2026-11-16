package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// bareInsertExempt are the production files still allowed a bare audit insert,
// each with why. An entry that no longer matches a call fails the guard.
var bareInsertExempt = map[string]string{
	"internal/api/handlers/oidc.go": "lane L15 is removing this call; drop the entry when it lands",
}

// TestGuard_AuditRowsGoThroughWrite fails on a production call to InsertAuditLog
// or InsertAuditLogWithSource outside this package: such a row reaches neither
// the audit_entry event nor the syslog forwarder, which is how every background
// entry came to be missing from syslog. Call audit.Write instead.
func TestGuard_AuditRowsGoThroughWrite(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, dir := range []string{"cmd", "internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "internal/db/generated" || rel == "internal/audit" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "InsertAuditLog" && sel.Sel.Name != "InsertAuditLogWithSource" {
					return true
				}
				if _, exempt := bareInsertExempt[rel]; exempt {
					seen[rel] = true
					return true
				}
				t.Errorf("%s: a bare %s skips the audit_entry event and the syslog forward; "+
					"call audit.Write (or audit.WriteSourced for a sourced row) instead",
					fset.Position(call.Pos()), sel.Sel.Name)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for rel := range bareInsertExempt {
		if !seen[rel] {
			t.Errorf("%s is exempt from this guard but no longer makes a bare audit insert: drop its entry", rel)
		}
	}
}
