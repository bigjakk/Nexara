package handlers

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestCVESeveritiesAllHaveAPostureBucket holds the exported severity
// vocabulary against the thing in this package that actually counts by
// severity.
//
// CVESeverities, with the empty no-filter value added, is what the
// endpoint declaration in internal/api/registry_cve.go validates
// ?severity= against. There is deliberately no second membership map
// beside it — that map WAS the hand-rolled validator the schema now
// performs — so the check that is left compares the vocabulary against a
// real reader instead: the posture summary carries one count per severity,
// and a severity the filter accepts but the summary has no bucket for is a
// value the API can slice by and never report on.
func TestCVESeveritiesAllHaveAPostureBucket(t *testing.T) {
	posture := reflect.TypeOf(securityPostureResponse{})

	buckets := map[string]bool{}
	for i := range posture.NumField() {
		name := posture.Field(i).Name
		if rest, ok := strings.CutSuffix(name, "Count"); ok {
			buckets[strings.ToLower(rest)] = true
		}
	}
	if len(buckets) == 0 {
		t.Fatal("securityPostureResponse has no *Count fields; this test would pass vacuously")
	}

	for _, severity := range CVESeverities {
		if !buckets[severity] {
			t.Errorf("CVESeverities offers %q, but securityPostureResponse has no %sCount field — "+
				"the API can filter by a severity it never reports a total for",
				severity, strings.ToUpper(severity[:1])+severity[1:])
		}
	}

	// The vocabulary itself is the API contract; stated so that dropping
	// one is a failure here rather than a filter that silently stops
	// working. The order is most severe first, which is the order the docs
	// render.
	if want := []string{"critical", "high", "medium", "low", "unknown"}; !slices.Equal(CVESeverities, want) {
		t.Errorf("CVESeverities = %v, want %v", CVESeverities, want)
	}
}
