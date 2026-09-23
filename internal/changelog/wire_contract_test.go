package changelog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// tsUnionPath is the frontend's copy of the ChangeType vocabulary. The dialog
// declares its chip table as Record<ChangelogChangeType, …>, so `tsc` already
// forces that table to cover the union exactly; this pins the union itself
// against Go.
//
// That leaves one gap tsc cannot see, which is why this guard exists: dropping
// a member from BOTH the union and the chip table type-checks cleanly, while
// the Go side keeps emitting it and the dialog renders a chipless row in an
// otherwise typed list.
const tsUnionPath = "../../frontend/src/lib/changelog.ts"

var (
	tsUnionRe = regexp.MustCompile(`export type ChangelogChangeType\s*=([^;]+);`)

	// A member is one whole `|`-separated alternative that is exactly one
	// double-quoted string. Comments are stripped from the file before the
	// union is even found, so a comment inside the declaration ("// \"docs\"
	// is server-only now") cannot stand in for the member it replaced, and a
	// `;` inside one cannot end the union early and hide the members after
	// it. An alternative of any other shape — an Exclude<…, "docs">, a
	// reference to another type — fails the parse rather than contributing
	// whichever quoted words it happens to contain. What the string may hold
	// is the set comparison's business, not this pattern's.
	tsMemberRe = regexp.MustCompile(`^"([^"\\]+)"$`)
	// tsCommentRe does not understand string literals: a "/*" inside one reads
	// as the start of a comment. That fails loudly — the union goes missing —
	// rather than silently.
	tsCommentRe = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
)

// TestChangeTypesMatchTheFrontendUnion fails the build when a ChangeType exists
// on one side of the wire only.
//
// Without it the drift is silent and one-directional: adding a Go constant and
// a headingTypes entry leaves the frontend with no key for it, so
// CHANGE_TYPE_CHIPS[type] is undefined, the row renders with no chip, and the
// entry still counts as "typed" — every sibling row then sits against a gutter
// that this one cannot fill. Nothing in either language's own test suite
// notices.
func TestChangeTypesMatchTheFrontendUnion(t *testing.T) {
	src, err := os.ReadFile(tsUnionPath)
	if err != nil {
		t.Fatalf("read %s: %v — if the frontend type moved, update tsUnionPath "+
			"rather than deleting this guard", tsUnionPath, err)
	}

	block := tsUnionRe.FindSubmatch(tsCommentRe.ReplaceAll(src, nil))
	if block == nil {
		t.Fatalf("no `export type ChangelogChangeType = …;` in %s — the guard "+
			"cannot see the union it is meant to pin", tsUnionPath)
	}

	var frontend []string
	for _, alt := range strings.Split(string(block[1]), "|") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue // a leading `|`, which the formatter may write
		}
		m := tsMemberRe.FindStringSubmatch(alt)
		if m == nil {
			t.Fatalf("the union has an alternative %q that is not a plain string literal; "+
				"this guard compares literal members only, so extend it before relying on it", alt)
		}
		frontend = append(frontend, m[1])
	}
	if len(frontend) == 0 {
		t.Fatalf("parsed no members out of %q", block[1])
	}

	backend := make([]string, 0, len(allChangeTypes))
	for _, ct := range allChangeTypes {
		backend = append(backend, string(ct))
	}

	sort.Strings(frontend)
	sort.Strings(backend)

	if len(frontend) != len(backend) {
		t.Fatalf("ChangeType vocabularies differ\n  Go:       %v\n  frontend: %v", backend, frontend)
	}
	for i := range backend {
		if backend[i] != frontend[i] {
			t.Fatalf("ChangeType vocabularies differ\n  Go:       %v\n  frontend: %v", backend, frontend)
		}
	}
}

// TestAllChangeTypesCoversEveryConstantAndEveryHeading ties allChangeTypes to
// what it summarises. The two guards that iterate allChangeTypes —
// TestHighlight_TypeWireFormat in parser_test.go and
// TestChangeTypesMatchTheFrontendUnion above — cannot see a type that never
// made it into the slice: a new value, emitted by the parser, passes both while
// the dialog renders its rows chipless. The slice is hand-kept, so it is held
// to every ChangeType value the package's source can produce — see
// changeTypeValuesInSource for why that is a type-check and not a match on
// declarations — and to every value headingTypes can emit.
func TestAllChangeTypesCoversEveryConstantAndEveryHeading(t *testing.T) {
	listed := map[ChangeType]bool{}
	for _, ct := range allChangeTypes {
		listed[ct] = true
	}

	produced := changeTypeValuesInSource(t)
	// What no floor can catch is a PARTIAL miss, which is why the collection is
	// a type-check; this only turns a collector that finds nothing at all into
	// one clear message instead of one per listed type below.
	if len(produced) == 0 {
		t.Fatal("found no ChangeType value in the package source; this guard is reading nothing")
	}
	for _, ct := range produced {
		if !listed[ct] {
			t.Errorf("the package source produces ChangeType %q, which is missing from allChangeTypes, so "+
				"neither the wire-format table nor the frontend-union guard can see it", ct)
		}
	}
	for heading, ct := range headingTypes {
		if !listed[ct] {
			t.Errorf("headingTypes[%q] emits %q, which is not in allChangeTypes", heading, ct)
		}
	}
	// A self-check on the collector rather than on the vocabulary: the slice
	// literal is itself part of the source it reads, so a listed value is
	// always found — unless it is "" (the zero value, never a type), or the
	// collector has stopped seeing constants at all. A value that is listed
	// but that nothing emits is dead weight, not a chipless row, and nothing
	// here tries to find it.
	for _, ct := range allChangeTypes {
		if !slices.Contains(produced, ct) {
			t.Errorf("allChangeTypes lists %q, which the collector did not find in the package source — "+
				"the empty zero value, or a collector that has stopped seeing the constants", ct)
		}
	}
}

// changeTypeValuesInSource type-checks the package's non-test files and
// returns every distinct constant string used as a ChangeType.
//
// Type-checked, because a match on declaration shapes misses the ways a new
// value actually arrives: `ChangeRemoved = ChangeType("removed")`, an untyped
// `ChangeRemoved = "removed"` in the existing const block, or an inline
// `h.Type = ChangeType("removed")` in the parser. go/types records every USE of
// such a value as a constant of type ChangeType wherever it is spelled, so
// every constant the source uses as a ChangeType is found. (An untyped
// constant nothing uses as a ChangeType is not — and cannot reach the wire
// either.) A literal the source only COMPARES against — with == or !=, or as
// a case label, parenthesised or converted — is skipped: it says what the
// package expects to see, not what it emits, and counting it would force a
// chip nothing ever shows. A named ChangeType constant still counts even when
// it is only compared, because its declaration is a use. The empty string is
// skipped too: it is the zero value, "no chip", not a type.
//
// A conversion of a NON-constant to ChangeType — ChangeType(sec.heading), a
// map[string]string lookup — is refused outright: the values it can produce
// cannot be enumerated from the source, and it is exactly how a type the
// frontend has no chip for would slip past everything above. Some ways in
// stay out of reach, and the package uses none of them today: reflection (a
// json.Unmarshal into a ChangeType field, fmt.Sscan, reflect's SetString), a
// package-local generic helper instantiated at ChangeType (`as[ChangeType](s)`
// returning `T(s)`, whose conversion is of a type parameter), a pointer
// conversion (`*(*ChangeType)(&s)`), and concatenating a ChangeType variable
// with another ChangeType variable or a listed constant. (With an unlisted
// literal it is caught: go/types types the literal as a ChangeType constant.)
//
// The files and their imports come from `go list -export`, not a glob and
// importer.Default: that way the package's build tags decide which files are
// in it, and an import from this module resolves, where importer.Default
// knows nothing of modules. The package itself is the one record go list
// marks DepOnly false.
func changeTypeValuesInSource(t *testing.T) []ChangeType {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-export",
		"-json=ImportPath,Dir,Export,GoFiles,DepOnly", ".").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list the package and its dependencies: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list the package and its dependencies: %v", err)
	}
	type listed struct {
		ImportPath, Dir, Export string
		GoFiles                 []string
		DepOnly                 bool
	}
	exports := map[string]string{}
	var self listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		exports[p.ImportPath] = p.Export
		if !p.DepOnly {
			self = p
		}
	}
	if self.ImportPath == "" {
		t.Fatal("go list named no package that is not a dependency; this guard cannot tell which to read")
	}

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(self.GoFiles))
	for _, name := range self.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(self.Dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file := exports[path]
		if file == "" {
			return nil, fmt.Errorf("go list reported no export data for %q", path)
		}
		return os.Open(file)
	})
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	pkg, err := (&types.Config{Importer: imp}).Check(self.ImportPath, fset, files, info)
	if err != nil {
		t.Fatalf("type-check the package: %v", err)
	}
	named := pkg.Scope().Lookup("ChangeType")
	if named == nil {
		t.Fatal("the package declares no ChangeType; this guard cannot tell what to collect")
	}

	// markCompared marks an operand and whatever it wraps through parentheses
	// and constant conversions, because go/types records the literal inside
	// `ChangeType("legacy")` or `("legacy")` as a ChangeType constant too.
	compared := map[ast.Expr]bool{}
	markCompared := func(e ast.Expr) {
		for e != nil {
			compared[e] = true
			switch x := e.(type) {
			case *ast.ParenExpr:
				e = x.X
			case *ast.CallExpr:
				if fn, ok := info.Types[x.Fun]; ok && fn.IsType() && len(x.Args) == 1 && info.Types[x].Value != nil {
					e = x.Args[0]
				} else {
					e = nil
				}
			default:
				e = nil
			}
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if x.Op == token.EQL || x.Op == token.NEQ {
					markCompared(x.X)
					markCompared(x.Y)
				}
			case *ast.CaseClause:
				for _, e := range x.List {
					markCompared(e)
				}
			case *ast.CallExpr:
				fn, ok := info.Types[x.Fun]
				if ok && fn.IsType() && types.Identical(fn.Type, named.Type()) && len(x.Args) == 1 &&
					info.Types[x].Value == nil {
					t.Errorf("%s converts a non-constant to ChangeType; the values it can produce cannot be "+
						"enumerated, so nothing can check that the frontend has a chip for each",
						fset.Position(x.Pos()))
				}
			}
			return true
		})
	}

	seen := map[ChangeType]bool{}
	var values []ChangeType
	for expr, tv := range info.Types {
		if compared[expr] || tv.Value == nil || tv.Value.Kind() != constant.String || !types.Identical(tv.Type, named.Type()) {
			continue
		}
		ct := ChangeType(constant.StringVal(tv.Value))
		if ct == "" || seen[ct] {
			continue
		}
		seen[ct] = true
		values = append(values, ct)
	}
	slices.Sort(values)
	return values
}
