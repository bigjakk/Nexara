package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestAssertThrowawayDB pins the guard that keeps the destructive migration
// tests off real databases (see the 2026-08-04 live-DB incident). It runs
// with no database, so the guard itself is verified on every push — not
// only on the day someone points the suite somewhere bad.
func TestAssertThrowawayDB(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"local chaintest", "postgres://nexara:pw@db:5432/nexara_chaintest?sslmode=disable", false},
		{"ci test db", "postgres://nexara:nexara@localhost:5432/nexara_test?sslmode=disable", false},
		{"freshtest precedent", "postgres://u:p@db:5432/nexara_freshtest", false},
		{"uppercase", "postgres://u:p@db:5432/NEXARA_TEST", false},
		{"postgresql scheme", "postgresql://u:p@db:5432/nexara_test", false},
		{"dbname param names throwaway", "postgres://u:p@db:5432?dbname=nexara_chaintest", false},

		{"live database — the incident", "postgres://nexara:pw@db:5432/nexara?sslmode=disable", true},
		{"live-shaped name", "postgres://u:p@db:5432/nexara_dev", true},
		{"dbname param overrides safe path", "postgres://u:p@db:5432/nexara_test?dbname=nexara", true},
		{"database param overrides safe path", "postgres://u:p@db:5432/nexara_test?database=nexara", true},
		{"no database at all", "postgres://u:p@db:5432/", true},
		{"keyword-value DSN", "host=db port=5432 dbname=nexara_test user=x", true},
		{"test not a segment suffix", "postgres://u:p@db:5432/testimony_db", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertThrowawayDB(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("assertThrowawayDB(%q) error = %v, wantErr %t", tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestGuard_NoDirectTestDBURLReads fails when any test file in this package
// reads the NEXARA_TEST_DB_URL environment variable outside
// migration_helpers_test.go. The variable is only safe behind testDBURL's
// throwaway-name check — a direct os.Getenv read is one wrong database name
// away from destroying a live database. Same go/ast enforcement style as
// internal/api/handlers/tracktask_guard_test.go.
func TestGuard_NoDirectTestDBURLReads(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if filepath.Base(file) == "migration_helpers_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if val, err := strconv.Unquote(lit.Value); err == nil && val == "NEXARA_TEST_DB_URL" {
					t.Errorf("%s: passes NEXARA_TEST_DB_URL to a call directly; "+
						"obtain the URL via testDBURL(t) or setupMigration(t) so the "+
						"throwaway-database guard applies", fset.Position(lit.Pos()))
				}
			}
			return true
		})
	}
}
