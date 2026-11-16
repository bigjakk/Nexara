package db

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The node_* inventory upserts skip unchanged rows, so three column lists must agree: SET
// col = EXCLUDED.col, the updated_at CASE, and the WHERE gate. A column in SET but missed by the
// gate is silently never persisted: the gate calls the row unchanged and nothing is written or
// logged. Static; no database.
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
