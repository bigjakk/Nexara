package scanner

// Phase 2 per-CVE risk scoring.
//
// We combine three signals into a single 0–10 score:
//   - CVSS base (severity in the absence of context)
//   - EPSS    (FIRST's empirical probability of exploitation in 30 days)
//   - KEV     (CISA's binary "actively exploited in the wild" flag)
//
// The formula is:
//
//	R_v = max(
//	  CVSS × likelihood,
//	  kevFloor          if v ∈ KEV,
//	  highEPSSFloor     if EPSS ≥ epssEscalateThreshold,
//	)
//
//	where likelihood = max(EPSS, likelihoodKEV if KEV else 0, likelihoodFloor)
//
// Rationale:
//   - The CVSS×likelihood term graduates by both severity and probability —
//     a CVSS-9 with no public exploit and EPSS=0.001 lands near 0.5 (low).
//   - kevFloor pins KEV-listed CVEs into the *critical* bucket regardless
//     of CVSS. CISA KEV means "actively exploited in the wild right now",
//     which matches SSVC's "Act" decision: drop everything and patch.
//   - highEPSSFloor pins top-decile EPSS into the *high* bucket — likely
//     imminent exploitation, but not yet observed.
//
// Pattern adapted from Qualys QDS (CVSS + threat factors with caps) and
// Rapid7 Real Risk (impact × likelihood). Numbers are deliberately
// conservative for an open-source tool with no commercial threat intel.

const (
	kevFloor              float32 = 7.5  // KEV → at least "critical" (= riskCriticalThreshold)
	highEPSSFloor         float32 = 5.0  // EPSS ≥ threshold → at least "high"
	likelihoodKEV         float32 = 0.9  // KEV likelihood substitute when EPSS is missing or low
	likelihoodFloor       float32 = 0.05 // EPSS-less vulns still carry a small base likelihood
	epssEscalateThreshold float32 = 0.9

	// Risk-score → severity bucket thresholds.
	riskCriticalThreshold float32 = 7.5
	riskHighThreshold     float32 = 5.0
	riskMediumThreshold   float32 = 2.5
	riskLowThreshold      float32 = 0.1
)

// computeRiskScore combines CVSS base, EPSS, and KEV into a 0–10 risk score.
// Inputs are clamped to sensible bounds; missing EPSS data should be passed
// as 0 (likelihood degrades to the baseline floor).
func computeRiskScore(cvssBase, epss float32, kev bool) float32 {
	if cvssBase < 0 {
		cvssBase = 0
	}
	if cvssBase > 10 {
		cvssBase = 10
	}
	if epss < 0 {
		epss = 0
	}
	if epss > 1 {
		epss = 1
	}

	likelihood := likelihoodFloor
	if epss > likelihood {
		likelihood = epss
	}
	if kev && likelihoodKEV > likelihood {
		likelihood = likelihoodKEV
	}

	score := cvssBase * likelihood

	if kev && score < kevFloor {
		score = kevFloor
	}
	if epss >= epssEscalateThreshold && score < highEPSSFloor {
		score = highEPSSFloor
	}

	if score > 10 {
		score = 10
	}
	return score
}

// riskToSeverity buckets a risk score into the same severity tiers used by
// the existing aggregate counts and the posture-score formula.
func riskToSeverity(risk float32) string {
	switch {
	case risk >= riskCriticalThreshold:
		return "critical"
	case risk >= riskHighThreshold:
		return "high"
	case risk >= riskMediumThreshold:
		return "medium"
	case risk >= riskLowThreshold:
		return "low"
	default:
		return "unknown"
	}
}

// severityToCVSSProxy maps Debian's urgency labels to a CVSS-like base
// score. Used as the CVSS input when actual CVSS data is unavailable
// (Debian Security Tracker doesn't include CVSS scores). Values are the
// midpoints of the standard CVSS v3.1 severity bands.
func severityToCVSSProxy(severity string) float32 {
	switch severity {
	case "critical":
		return 9.5
	case "high":
		return 7.5
	case "medium":
		return 5.0
	case "low":
		return 2.5
	default:
		return 1.0 // unknown/empty — small baseline
	}
}
