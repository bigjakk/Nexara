package scanner

import (
	"math"
	"testing"
)

func TestComputeRiskScore(t *testing.T) {
	cases := []struct {
		name     string
		cvss     float32
		epss     float32
		kev      bool
		minScore float32
		maxScore float32
		wantSev  string
	}{
		// "Theoretically critical" but no public exploit and no KEV →
		// stays low. This is the failure mode Phase 2 fixes.
		{"critical with no exploit", 9.5, 0.001, false, 0.4, 0.6, "low"},

		// Same critical, but EPSS bumps to 50% → graduates to medium/high.
		{"critical with moderate EPSS", 9.5, 0.5, false, 4.5, 5.0, "medium"},

		// Critical + KEV → KEV likelihood pin → 9.5×0.9 ≈ 8.55 → critical.
		{"critical in KEV", 9.5, 0.5, true, 8.4, 8.7, "critical"},

		// Debian-low (CVSS=2.5) but in KEV → KEV floor pins to 7.5 → critical.
		// This matches SSVC "Act": KEV means actively exploited right now.
		{"low in KEV escalates to critical", 2.5, 0.0, true, 7.4, 7.6, "critical"},

		// EPSS ≥ threshold without KEV pins to the high floor.
		{"high EPSS escalates", 5.0, 0.95, false, 4.9, 5.1, "high"},

		// No data at all → baseline likelihood floor of 0.05.
		{"no data critical", 9.5, 0, false, 0.4, 0.6, "low"},
		{"no data low", 2.5, 0, false, 0.1, 0.2, "low"},

		// Edge: very low CVSS + KEV — KEV floor still wins → critical.
		{"trivial CVE in KEV", 0.1, 0, true, 7.4, 7.6, "critical"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeRiskScore(c.cvss, c.epss, c.kev)
			if got < c.minScore || got > c.maxScore {
				t.Errorf("score=%.2f, want in [%.2f,%.2f]", got, c.minScore, c.maxScore)
			}
			gotSev := riskToSeverity(got)
			if gotSev != c.wantSev {
				t.Errorf("severity=%q, want %q (score=%.2f)", gotSev, c.wantSev, got)
			}
		})
	}
}

func TestComputeRiskScoreClamps(t *testing.T) {
	cases := []struct {
		name      string
		cvss      float32
		epss      float32
		kev       bool
		max, min  float32
	}{
		{"negative cvss", -5, 0.5, false, 1.0, 0},
		{"oversized cvss", 99, 0.5, false, 10, 4},
		{"oversized epss", 5, 99, false, 10, 4},
		{"negative epss", 5, -0.5, false, 1.0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeRiskScore(c.cvss, c.epss, c.kev)
			if got > c.max || got < c.min {
				t.Errorf("score=%.2f, out of bounds [%.2f,%.2f]", got, c.min, c.max)
			}
			if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
				t.Errorf("score is NaN/Inf: %v", got)
			}
		})
	}
}

func TestRiskToSeverityBoundaries(t *testing.T) {
	cases := []struct {
		score float32
		want  string
	}{
		{10.0, "critical"},
		{7.5, "critical"},
		{7.49, "high"},
		{5.0, "high"},
		{4.99, "medium"},
		{2.5, "medium"},
		{2.49, "low"},
		{0.1, "low"},
		{0.09, "unknown"},
		{0, "unknown"},
	}
	for _, c := range cases {
		got := riskToSeverity(c.score)
		if got != c.want {
			t.Errorf("riskToSeverity(%.2f) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestSeverityToCVSSProxy(t *testing.T) {
	cases := []struct {
		sev  string
		want float32
	}{
		{"critical", 9.5},
		{"high", 7.5},
		{"medium", 5.0},
		{"low", 2.5},
		{"unknown", 1.0},
		{"", 1.0},
		{"garbage", 1.0},
	}
	for _, c := range cases {
		if got := severityToCVSSProxy(c.sev); got != c.want {
			t.Errorf("severityToCVSSProxy(%q) = %.1f, want %.1f", c.sev, got, c.want)
		}
	}
}
