package api

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// docs/api-reference.md says which routes are interactive-only, and which of them
// still spend a request of the per-address Auth budget when a key is refused on
// them. Both are statements about code — interactiveOnlyRoutes and authLimitedPaths —
// that nothing tied to the prose: a route added to the set, or a path added to the
// bucket, left the reference telling a client author something that was no longer
// so, with every other test green. These two read the prose and hold it to the code,
// the way the figure tests hold the numbers.

// docsRoutes returns the `METHOD /path` tokens in a stretch of the reference, as
// "METHOD /path" with the /api/v1 prefix the reference leaves out put back.
func docsRoutes(t *testing.T, stretch string) []string {
	t.Helper()
	token := regexp.MustCompile("`(GET|POST|PUT|PATCH|DELETE) (/[^`]+)`")
	found := token.FindAllStringSubmatch(stretch, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, m[1]+" /api/v1"+m[2])
	}
	slices.Sort(out)
	return out
}

// referenceSentence finds, in the reference with its line breaks flattened, the
// text between lead and the first full stop after it.
func referenceSentence(t *testing.T, lead string) string {
	t.Helper()
	raw, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	flat := strings.Join(strings.Fields(string(raw)), " ")
	i := strings.Index(flat, lead)
	if i < 0 {
		t.Fatalf("%s no longer says %q: this guard anchors on that sentence, so reword it together with the guard, not around it", apiReferencePath(), lead)
	}
	rest := flat[i+len(lead):]
	j := strings.Index(rest, ". ")
	if j < 0 {
		t.Fatalf("%s: the sentence after %q does not end", apiReferencePath(), lead)
	}
	return rest[:j]
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	slices.Sort(got)
	slices.Sort(want)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s: the reference does not name %s", what, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("%s: the reference names %s, which is not in the set the code holds", what, g)
		}
	}
	if len(got) == 0 {
		t.Errorf("%s: the reference names none, so this guard checked nothing", what)
	}
}

// TestDocs_TheInteractiveOnlyListIsTheRouteSet: the list of interactive-only routes
// in the reference is exactly interactiveOnlyRoutes. A route flagged and not listed
// sends a client author to a 403 the reference never mentioned; a route listed and
// not flagged promises a refusal nothing makes.
func TestDocs_TheInteractiveOnlyListIsTheRouteSet(t *testing.T) {
	want := make([]string, 0, len(interactiveOnlyRoutes))
	for _, r := range interactiveOnlyRoutes {
		want = append(want, r.method+" "+r.path)
	}
	sameSet(t, "the Interactive-Only Routes list",
		docsRoutes(t, referenceSentence(t, "The routes are ")), want)
}

// TestDocs_TheRoutesThatSpendTheAuthBudgetAreTheOnesThatDo: the reference says which
// of the interactive-only routes a refused key still costs a request of the
// per-address Auth budget on — those the budget covers, because the budget is spent
// before authentication. That list is the intersection of the two sets in code, and
// follows both: put a path into authLimitedPaths, or flag another path already in
// it, and the sentence is stale until it is changed (and with it the comment on
// InteractiveOnly in registry.go, which says the same).
func TestDocs_TheRoutesThatSpendTheAuthBudgetAreTheOnesThatDo(t *testing.T) {
	want := make([]string, 0, len(interactiveOnlyRoutes))
	for _, r := range interactiveOnlyRoutes {
		if authLimitedPaths[limiterPathOf(r.path)] {
			want = append(want, r.method+" "+r.path)
		}
	}
	if len(want) == 0 {
		t.Fatal("no interactive-only route is in the per-address Auth budget: the clause this guard holds to the code is moot — remove it from the reference, registry.go and this test together")
	}
	sameSet(t, "the routes that spend the per-address Auth budget",
		docsRoutes(t, referenceSentence(t, "on the flagged routes that budget covers:")), want)
}
