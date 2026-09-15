package reports

import (
	"encoding/json"
	"testing"
)

func TestParseParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(Params) bool
	}{
		{"empty is the defaults", "", false, func(p Params) bool {
			return p.StaleAfterHours == 24 && p.TopN == 10 && p.SnapshotWarnDays == 7 && p.SectionEnabled("runs")
		}},
		{"null is the defaults", "null", false, func(p Params) bool { return p.StaleAfterHours == 24 }},
		{"empty object is the defaults", "{}", false, func(p Params) bool { return p.TopN == 10 }},
		{"override stale hours", `{"stale_after_hours": 48}`, false, func(p Params) bool { return p.StaleAfterHours == 48 && p.TopN == 10 }},
		{"section toggle off", `{"sections": {"runs": false}}`, false, func(p Params) bool { return !p.SectionEnabled("runs") && p.SectionEnabled("capacity") }},
		{"unknown keys ignored", `{"future": 1}`, false, func(p Params) bool { return p.StaleAfterHours == 24 }},
		{"not an object", `[1,2]`, true, nil},
		{"stale hours too small", `{"stale_after_hours": -1}`, true, nil},
		{"stale hours too large", `{"stale_after_hours": 99999}`, true, nil},
		{"top_n too large", `{"top_n": 101}`, true, nil},
		{"snapshot days zero-ish negative", `{"snapshot_warn_days": -3}`, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := ParseParams(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseParams(%q): %v", tc.raw, err)
			}
			if !tc.check(p) {
				t.Errorf("ParseParams(%q) = %+v", tc.raw, p)
			}
		})
	}
}
