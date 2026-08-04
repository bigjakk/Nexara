package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bannedConstructors are the domain-service constructors that must be called
// exactly once per process, from the composition root in this package.
//
// Before internal/app existed, each of these was called at two to four sites
// with different dependencies, and the differences were silent bugs: a rolling
// orchestrator built with a nil notification registry dropped job-failure
// notifications on the HTTP path; a DRS engine and executor built per request
// dispatched migrations outside the scheduler's leader lock and raced the tick;
// only one of three event publishers carried the syslog forwarder, so
// background audit rows never reached a SIEM; two CVE scanners meant two copies
// of the ~80MB feed caches.
//
// Keyed on the resolved IMPORT PATH, not the local identifier: matching on the
// qualifier alone is defeated by any alias, and this codebase aliases
// (internal/api/server.go imports this package as nexapp).
var bannedConstructors = map[string]map[string]bool{
	"github.com/bigjakk/nexara/internal/rolling":       {"NewOrchestrator": true},
	"github.com/bigjakk/nexara/internal/drs":           {"NewEngine": true, "NewExecutor": true},
	"github.com/bigjakk/nexara/internal/notifications": {"NewEngine": true, "BuildRegistry": true},
	"github.com/bigjakk/nexara/internal/scanner":       {"NewEngine": true},
	"github.com/bigjakk/nexara/internal/reports":       {"NewGenerator": true},
	"github.com/bigjakk/nexara/internal/events":        {"NewPublisher": true},
	"github.com/bigjakk/nexara/internal/proxmox":       {"NewClientCache": true},
}

// guardedDirs are the trees that must not construct domain services. Engine
// packages construct their own types in their own tests, which is fine; these
// are the consumers.
var guardedDirs = []string{"../api", "../scheduler", "../ws", "../collector", "../../cmd"}

// importsByLocalName maps each import's local identifier to its path, so a
// selector expression can be resolved to the package it actually refers to.
// A dot-import would make call sites unqualified and invisible to the selector
// walk below — it is reported rather than silently skipped.
func importsByLocalName(t *testing.T, file *ast.File, fset *token.FileSet) map[string]string {
	t.Helper()

	out := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			switch spec.Name.Name {
			case ".":
				if _, banned := bannedConstructors[path]; banned {
					t.Errorf("%s: dot-imports %s, which hides constructor calls from this guard",
						fset.Position(spec.Pos()), path)
				}
				continue
			case "_":
				continue
			default:
				name = spec.Name.Name
			}
		}
		out[name] = path
	}
	return out
}

// TestGuard_NoDomainServiceConstructionOutsideApp fails when a consumer
// package constructs a domain service instead of taking it from the
// composition root. Same enforcement style as
// internal/api/handlers/tracktask_guard_test.go: the invariant is checked by
// the compiler-adjacent test rather than trusted to review.
//
// If a new service legitimately belongs in the composition root, add it to
// app.App and take it from there — do not add an exemption here.
func TestGuard_NoDomainServiceConstructionOutsideApp(t *testing.T) {
	fset := token.NewFileSet()

	for _, dir := range guardedDirs {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Test files may construct engines directly to build fixtures.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}

			parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			imports := importsByLocalName(t, parsed, fset)

			ast.Inspect(parsed, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}

				importPath, known := imports[pkgIdent.Name]
				if !known {
					return true
				}
				if !bannedConstructors[importPath][sel.Sel.Name] {
					return true
				}

				t.Errorf("%s: constructs %s.%s directly; take the shared instance "+
					"from the composition root (internal/app) instead — per-site "+
					"construction is how engines end up wired differently by trigger path",
					fset.Position(call.Pos()), importPath, sel.Sel.Name)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
