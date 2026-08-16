package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot and generatedDir are relative to this package directory. The guard
// walks the whole repository, not just this package: a scoped params struct is
// no safer for being built in a service or a job.
const (
	repoRoot     = "../../.."
	generatedDir = "internal/db/generated"
)

// scopeField is the parameter every RBAC-scoped list query takes. nil reaches
// SQL as NULL and lifts the filter, so a params struct built without it — or
// with an explicit nil — reads across every cluster.
const scopeField = "AccessibleClusterIds"

// scopeStampers are the helpers whose own tests pin that they set scopeField on
// every params struct handed to them by pointer. A function that passes its
// struct to one of these has discharged its obligation without naming the field.
var scopeStampers = map[string]bool{
	"applyAuditListScope": true,
	"applyTaskListScope":  true,
	"parseAuditFilters":   true,
}

// TestGuard_ScopedParamsCarryClusterScope fails when code builds a query params
// struct that has an AccessibleClusterIds field without ever setting it to
// something real.
//
// This is the mistake that produced the original cross-cluster leak and then
// tried to recur twice while it was being fixed: the SQL grows a scope
// parameter, and a call site that does not pass it silently reverts to reading
// every cluster. Neither a unit test nor a per-row guard catches it — handlers
// hold a concrete *db.Queries, so no test can observe the params they actually
// send, and a per-row guard cannot repair a Total or an over-broad LIMIT.
// Static structure is what is left.
//
// A struct is satisfied by carrying the field with a non-nil value in its
// literal, by a later assignment to <var>.AccessibleClusterIds, or by being
// handed to a scopeStamper as &<var>.
func TestGuard_ScopedParamsCarryClusterScope(t *testing.T) {
	t.Parallel()

	scoped := scopedParamsTypes(t)
	if len(scoped) == 0 {
		t.Fatal("found no generated params types with an " + scopeField +
			" field — the guard would pass vacuously; check generatedDir")
	}

	files := goSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("found no Go source files to scan — the guard would pass vacuously")
	}

	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			checkFuncScopeStamping(t, fset, fn, scoped)
		}
	}
}

// scopedStruct is one construction of a scoped params type inside a function.
// name is the variable it was bound to, or "" when the struct is built inline
// as a call argument — inline structs have no later statement that could stamp
// them, so they must carry the field themselves.
type scopedStruct struct {
	typeName string
	name     string
	pos      token.Pos
}

func checkFuncScopeStamping(t *testing.T, fset *token.FileSet, fn *ast.FuncDecl, scoped map[string]bool) {
	t.Helper()

	for _, s := range scopedStructsIn(fn, scoped) {
		if s.name == "" {
			t.Errorf("%s: %s builds a %s inline without setting %s.\n"+
				"\tThat field is the RBAC cluster scope: unset or nil, it reaches SQL as\n"+
				"\tNULL and matches EVERY cluster. Pass clusterScopeFilter(access) — or\n"+
				"\taccess.ScopedIDs() — into it.",
				fset.Position(s.pos), fn.Name.Name, s.typeName, scopeField)
			continue
		}
		if stampsVar(fn, s.name) {
			continue
		}
		t.Errorf("%s: %s builds %s (a %s) but never sets %s on it.\n"+
			"\tThat field is the RBAC cluster scope: unset or nil, it reaches SQL as\n"+
			"\tNULL and matches EVERY cluster. Assign %s.%s, or hand &%s to one of: %s.",
			fset.Position(s.pos), fn.Name.Name, s.name, s.typeName, scopeField,
			s.name, scopeField, s.name, stamperNames())
	}
}

// scopedStructsIn finds every scoped params struct a function is responsible
// for: composite literals, `var` declarations, named results, and pointer
// parameters. Literals that already carry a real scope value are not returned —
// they are satisfied where they stand.
func scopedStructsIn(fn *ast.FuncDecl, scoped map[string]bool) []scopedStruct {
	var found []scopedStruct
	// Literals bound to a variable, so the general sweep can tell them from
	// inline ones.
	bound := make(map[*ast.CompositeLit]string)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range node.Rhs {
				lit, ok := rhs.(*ast.CompositeLit)
				if !ok || i >= len(node.Lhs) {
					continue
				}
				if id, ok := node.Lhs[i].(*ast.Ident); ok {
					bound[lit] = id.Name
				}
			}
		case *ast.ValueSpec:
			for _, v := range node.Values {
				lit, ok := v.(*ast.CompositeLit)
				if !ok {
					continue
				}
				if len(node.Names) > 0 {
					bound[lit] = node.Names[0].Name
				}
			}
		}
		return true
	})

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			name, ok := typeIdent(node.Type)
			if !ok || !scoped[name] {
				return true
			}
			if litStampsScope(node) {
				return true
			}
			found = append(found, scopedStruct{typeName: name, name: bound[node], pos: node.Pos()})
		case *ast.ValueSpec:
			// `var p db.XParams` with no value — stamped later or not at all.
			if len(node.Values) > 0 {
				return true
			}
			name, ok := typeIdent(node.Type)
			if !ok || !scoped[name] {
				return true
			}
			for _, id := range node.Names {
				found = append(found, scopedStruct{typeName: name, name: id.Name, pos: id.Pos()})
			}
		}
		return true
	})

	// Named results and parameters: `func f() (listP db.XParams, …)` declares
	// the struct without any node inside the body. parseAuditFilters is exactly
	// this shape, and skipping it would leave the audit path unguarded.
	for _, field := range signatureFields(fn) {
		name, ok := typeIdent(derefType(field.Type))
		if !ok || !scoped[name] {
			continue
		}
		for _, id := range field.Names {
			found = append(found, scopedStruct{typeName: name, name: id.Name, pos: id.Pos()})
		}
	}
	return found
}

func signatureFields(fn *ast.FuncDecl) []*ast.Field {
	var fields []*ast.Field
	if fn.Type.Results != nil {
		fields = append(fields, fn.Type.Results.List...)
	}
	if fn.Type.Params != nil {
		fields = append(fields, fn.Type.Params.List...)
	}
	return fields
}

func derefType(expr ast.Expr) ast.Expr {
	if star, ok := expr.(*ast.StarExpr); ok {
		return star.X
	}
	return expr
}

// litStampsScope reports whether a composite literal sets scopeField to
// something other than nil. An explicit nil is rejected: it is indistinguishable
// from omitting the field once it reaches SQL.
func litStampsScope(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != scopeField {
			continue
		}
		if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "nil" {
			return false
		}
		return true
	}
	return false
}

// stampsVar reports whether the function assigns the scope field on the named
// variable, or hands that variable to a scopeStamper by address. Both are tied
// to this variable specifically, so stamping one of two structs no longer
// satisfies the other — which is the Items-vs-Total shape of the original leak.
func stampsVar(fn *ast.FuncDecl, name string) bool {
	stamped := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if stamped {
			return false
		}
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != scopeField {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != name {
					continue
				}
				// `p.AccessibleClusterIds = nil` is not a stamp.
				if i < len(node.Rhs) {
					if rhs, ok := node.Rhs[i].(*ast.Ident); ok && rhs.Name == "nil" {
						continue
					}
				}
				stamped = true
			}
		case *ast.CallExpr:
			fnName, ok := typeIdent(node.Fun)
			if !ok || !scopeStampers[fnName] {
				return true
			}
			for _, arg := range node.Args {
				unary, ok := arg.(*ast.UnaryExpr)
				if !ok || unary.Op != token.AND {
					continue
				}
				if id, ok := unary.X.(*ast.Ident); ok && id.Name == name {
					stamped = true
				}
			}
		}
		return !stamped
	})
	return stamped
}

// scopedParamsTypes returns the names of generated structs carrying scopeField,
// read from the generated source so the set cannot drift from it.
func scopedParamsTypes(t *testing.T) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(repoRoot, generatedDir, "*.go"))
	if err != nil {
		t.Fatalf("glob generated: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no generated files under %s", filepath.Join(repoRoot, generatedDir))
	}

	scoped := make(map[string]bool)
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				for _, fieldName := range f.Names {
					if fieldName.Name == scopeField {
						scoped[spec.Name.Name] = true
					}
				}
			}
			return true
		})
	}
	return scoped
}

// goSourceFiles walks the repository for non-test Go sources, skipping the
// generated package (which defines the structs rather than filling them),
// anything vendored, and any nested checkout.
func goSourceFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	skipDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
	}
	generated := filepath.Clean(filepath.Join(repoRoot, generatedDir))
	root := filepath.Clean(repoRoot)

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || filepath.Clean(path) == generated {
				return filepath.SkipDir
			}
			// A nested checkout holds its own copy of these very sources,
			// typically at an older commit — walking into one reports findings
			// against paths that are not this repository, and that this
			// repository may already have fixed. Agent worktrees land under
			// .claude/worktrees/, which is gitignored but still on disk, so
			// this fires on a normal dev box while CI's clean checkout passes.
			if filepath.Clean(path) != root && isNestedCheckout(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}
	return files
}

// isNestedCheckout reports whether dir is the root of its own git checkout.
// A worktree and a submodule carry a .git FILE rather than a directory, so
// neither is caught by the ".git" entry in skipDirs — that one only matches a
// directory the walk is about to descend into.
func isNestedCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func stamperNames() string {
	names := make([]string, 0, len(scopeStampers))
	for name := range scopeStampers {
		names = append(names, name)
	}
	sortStrings(names)
	return strings.Join(names, ", ")
}

// sortStrings keeps failure messages stable without pulling in a dependency
// for a three-element slice.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// typeIdent pulls "XParams" out of `db.XParams` or a bare `XParams`, and the
// callee name out of a call expression.
func typeIdent(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name, true
	case *ast.Ident:
		return t.Name, true
	}
	return "", false
}
