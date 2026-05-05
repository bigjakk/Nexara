package scanner

import "testing"

func TestClassifySSVC(t *testing.T) {
	cases := []struct {
		name string
		cvss float32
		epss float32
		kev  bool
		want string
	}{
		// KEV always wins regardless of CVSS — actively exploited == patch now.
		{"KEV with low CVSS still acts", 2.5, 0, true, SSVCAct},
		{"KEV with high CVSS acts", 9.5, 0.0, true, SSVCAct},

		// Act criteria without KEV: EPSS ≥ 0.5 AND CVSS ≥ 7.
		{"high CVSS + EPSS triggers act", 8.0, 0.6, false, SSVCAct},
		{"high CVSS + low EPSS does not act", 8.0, 0.3, false, SSVCAttend}, // EPSS≥0.1 → attend
		{"high EPSS but low CVSS does not act", 4.0, 0.7, false, SSVCAttend},

		// Attend: EPSS ≥ 0.1 OR CVSS ≥ 9.
		{"epss bump alone attends", 3.0, 0.15, false, SSVCAttend},
		{"cvss 9 alone attends", 9.0, 0, false, SSVCAttend},
		{"cvss 9.5 + low epss attends", 9.5, 0.001, false, SSVCAttend},

		// Track*: CVSS ≥ 7 (no EPSS escalation).
		{"cvss 7 with no EPSS is track*", 7.0, 0, false, SSVCTrackStar},
		{"cvss 8 with no EPSS is track*", 8.0, 0, false, SSVCTrackStar},
		{"cvss 7 with epss 0.05 is track*", 7.0, 0.05, false, SSVCTrackStar},

		// Track: routine.
		{"low cvss, low epss is track", 3.0, 0.001, false, SSVCTrack},
		{"medium cvss, no epss is track", 5.0, 0, false, SSVCTrack},
		{"zero values are track", 0, 0, false, SSVCTrack},

		// Boundary checks.
		{"exactly EPSS 0.5 + CVSS 7 acts", 7.0, 0.5, false, SSVCAct},
		{"just under EPSS 0.5 falls to attend", 7.0, 0.499, false, SSVCAttend},
		{"exactly CVSS 9 attends", 9.0, 0, false, SSVCAttend},
		{"just under CVSS 9 with low epss is track*", 8.99, 0, false, SSVCTrackStar},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifySSVC(c.cvss, c.epss, c.kev)
			if got != c.want {
				t.Errorf("classifySSVC(cvss=%.2f, epss=%.3f, kev=%v) = %q, want %q",
					c.cvss, c.epss, c.kev, got, c.want)
			}
		})
	}
}

// Test the user's stated requirement: "if a critical CVE is being exposed,
// it should let the user know it's very vulnerable" — i.e., actively-
// exploited CVEs must reach Act regardless of how severe Debian thinks
// they are.
func TestKEVAlwaysReachesAct(t *testing.T) {
	for cvss := float32(0); cvss <= 10; cvss += 0.5 {
		for epss := float32(0); epss <= 1; epss += 0.1 {
			if got := classifySSVC(cvss, epss, true); got != SSVCAct {
				t.Errorf("KEV cve at cvss=%.1f epss=%.1f got %q, want act",
					cvss, epss, got)
			}
		}
	}
}

// And the converse: a clean (low CVSS, low EPSS, no KEV) vuln should
// always be track — quantity-of-low-severity vulns must never inflate.
func TestRoutineLowsStayTrack(t *testing.T) {
	for cvss := float32(0); cvss < 7; cvss += 0.5 {
		for epss := float32(0); epss < 0.1; epss += 0.01 {
			if got := classifySSVC(cvss, epss, false); got != SSVCTrack {
				t.Errorf("low vuln at cvss=%.1f epss=%.3f got %q, want track",
					cvss, epss, got)
			}
		}
	}
}
