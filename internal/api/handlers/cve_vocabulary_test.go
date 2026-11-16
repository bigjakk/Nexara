package handlers

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestCVESeveritiesAllHaveAPostureBucket: CVESeverities is what registry_cve.go validates
// ?severity= against, with deliberately no second membership map. So it is held against a
// real reader instead: a severity the filter accepts but the posture summary has no bucket
// for is one the API can slice by and never report on.
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
