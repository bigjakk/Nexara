package changelog

import (
	"os"
	"regexp"
	"sort"
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
	tsUnionRe  = regexp.MustCompile(`export type ChangelogChangeType\s*=([^;]+);`)
	tsMemberRe = regexp.MustCompile(`"([a-z]+)"`)

	// A member only counts when the union actually reaches it — i.e. it follows
	// the `=` or a `|`. Scanning for any quoted word instead would let a comment
	// inside the declaration ("// \"docs\" is server-only now") stand in for the
	// member it replaced, and the guard would pass while the two genuinely differ.
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

	block := tsUnionRe.FindSubmatch(src)
	if block == nil {
		t.Fatalf("no `export type ChangelogChangeType = …;` in %s — the guard "+
			"cannot see the union it is meant to pin", tsUnionPath)
	}

	members := tsMemberRe.FindAllSubmatch(tsCommentRe.ReplaceAll(block[1], nil), -1)
	frontend := make([]string, 0, len(members))
	for _, m := range members {
		frontend = append(frontend, string(m[1]))
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
