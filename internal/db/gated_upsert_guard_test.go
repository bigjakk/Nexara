package db

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The node_* inventory upserts write a node's whole hardware table in one
// statement and skip rows that have not changed. Three column lists have to
// agree for that to be correct, and nothing but this test notices when they
// drift:
//
//	SET col = EXCLUDED.col   — the columns the upsert writes
//	updated_at = CASE WHEN (...) IS DISTINCT FROM (...)  — content-change clock
//	WHERE (...) IS DISTINCT FROM (...)                   — the write gate itself
//
// A column added to the SET list but missed in the WHERE gate is the dangerous
// one: the gate decides the row is unchanged, the statement writes nothing, and
// the new value is silently never persisted. Nothing fails, no error is logged,
// and the column just reads stale forever. It is the same shape as a guard whose
// input cannot express disagreement — reading the query does not reveal it.
//
// Static analysis only: no database, so it runs in the normal `go test` pass.
var gatedUpsertQueries = []string{
	"node_pci_devices",
	"node_network_interfaces",
	"node_disks",
}

func TestGuard_GatedInventoryUpsertColumnListsAgree(t *testing.T) {
	for _, table := range gatedUpsertQueries {
		t.Run(table, func(t *testing.T) {
			path := filepath.Join("..", "..", "queries", table+".sql")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			src := string(raw)

			// Isolate the upsert: the first statement in the file, from its
			// ON CONFLICT clause to the final WHERE (the gate).
			conflict := regexpIndex(t, src, `ON CONFLICT \(`)
			body := src[conflict:]
			gateAt := lastIndexOf(t, body, "\nWHERE (")
			setPart, gatePart := body[:gateAt], body[gateAt:]

			written := matchSet(regexp.MustCompile(`(?m)^\s{4}(\w+) = EXCLUDED\.`), setPart)
			if len(written) == 0 {
				t.Fatalf("parsed no written columns from %s; the guard would pass vacuously", path)
			}

			colRe := regexp.MustCompile(table + `\.(\w+)`)
			caseCols := matchSet(colRe, setPart)
			delete(caseCols, "updated_at")
			gateCols := matchSet(colRe, gatePart)
			delete(gateCols, "last_seen_at")

			if !equalSets(written, caseCols) {
				t.Errorf("updated_at CASE compares %v but the upsert writes %v; "+
					"updated_at would stop tracking a real content change",
					keys(caseCols), keys(written))
			}
			if !equalSets(written, gateCols) {
				t.Errorf("WHERE gate compares %v but the upsert writes %v; "+
					"a change to the missing column would be judged 'unchanged' and never persisted",
					keys(gateCols), keys(written))
			}
		})
	}
}

func regexpIndex(t *testing.T, s, pattern string) int {
	t.Helper()
	loc := regexp.MustCompile(pattern).FindStringIndex(s)
	if loc == nil {
		t.Fatalf("pattern %q not found; the upsert's shape changed and this guard no longer parses it", pattern)
	}
	return loc[0]
}

func lastIndexOf(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.LastIndex(s, sub)
	if i < 0 {
		t.Fatalf("%q not found; the upsert lost its WHERE gate, so every sweep writes every row again", sub)
	}
	return i
}

func matchSet(re *regexp.Regexp, s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out[m[1]] = true
	}
	return out
}

func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
