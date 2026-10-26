package api

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
)

const guestHistoryRouteCount = 1

// categoryThenRe pulls the values the category CASE can produce.
var categoryThenRe = regexp.MustCompile(`(?:THEN|ELSE)\s+'([a-z]+)'`)

// TestGuestHistoryCategoryVocabulary holds the type filter's enum to the
// category CASE in ListGuestHistory. A category only the SQL produces is one
// the filter refuses with a 400; one only the enum lists is a filter that
// always comes back empty.
func TestGuestHistoryCategoryVocabulary(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../queries/guest_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)CASE\n\s+WHEN e\.kind = 'status'.*?END::text AS category`).Find(src)
	if m == nil {
		t.Fatal("the category CASE was not found in queries/guest_history.sql — if it moved, this guard must follow it")
	}
	var inSQL []string
	for _, sub := range categoryThenRe.FindAllSubmatch(m, -1) {
		if c := string(sub[1]); !slices.Contains(inSQL, c) {
			inSQL = append(inSQL, c)
		}
	}
	slices.Sort(inSQL)

	declared := declaredEndpoint(t, fiber.MethodGet, clusterScope+"/guests/:vmid/history").Parameters["category"].Enum
	got := slices.DeleteFunc(slices.Clone(declared), func(s string) bool { return s == "" })
	slices.Sort(got)
	if !slices.Equal(got, inSQL) {
		t.Errorf("the declared ?category= enum is %v but the SQL produces %v", got, inSQL)
	}
}
