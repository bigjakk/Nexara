package scanner

// SSVC (Stakeholder-Specific Vulnerability Categorization) action labels.
//
// SSVC is CISA's decision tree for vulnerability prioritization, designed
// to replace CVSS-as-priority. Instead of compressing risk to a scalar,
// it emits one of four explicit *actions* per vulnerability:
//
//	Act        — patch immediately, drop other work
//	Attend     — schedule a maintenance window this week
//	Track*     — keep an eye on; patch in next routine cycle
//	Track      — routine; batch with monthly updates
//
// Reference: https://www.cisa.gov/sites/default/files/publications/cisa-ssvc-guide%20508c.pdf
//
// We use a lightweight version of the decision rule keyed on the same
// signals Phase 2 already collects (CVSS proxy, EPSS, KEV). Full SSVC
// also incorporates Mission Prevalence and Public Well-being Impact —
// both require asset-context we don't have for self-hosted Proxmox
// clusters, so we hold them at the conservative defaults implied by
// CISA's calculator when those inputs are unknown.
const (
	SSVCAct       = "act"
	SSVCAttend    = "attend"
	SSVCTrackStar = "track_star"
	SSVCTrack     = "track"
)

const (
	ssvcActEPSSThreshold     float32 = 0.5
	ssvcActCVSSThreshold     float32 = 7.0
	ssvcAttendEPSSThreshold  float32 = 0.1
	ssvcAttendCVSSThreshold  float32 = 9.0
	ssvcTrackStarCVSSThresh  float32 = 7.0
)

// classifySSVC returns the SSVC action label for a vulnerability.
//
// Decision rule (top-down, first match wins):
//
//	Act        — in KEV, OR (EPSS ≥ 0.5 AND CVSS ≥ 7)
//	Attend     — EPSS ≥ 0.1, OR CVSS ≥ 9
//	Track*     — CVSS ≥ 7
//	Track      — otherwise
//
// KEV → Act regardless of CVSS, matching CISA's own treatment of the
// KEV catalog as the authoritative "patch now" list.
func classifySSVC(cvss, epss float32, kev bool) string {
	if kev {
		return SSVCAct
	}
	if epss >= ssvcActEPSSThreshold && cvss >= ssvcActCVSSThreshold {
		return SSVCAct
	}
	if epss >= ssvcAttendEPSSThreshold || cvss >= ssvcAttendCVSSThreshold {
		return SSVCAttend
	}
	if cvss >= ssvcTrackStarCVSSThresh {
		return SSVCTrackStar
	}
	return SSVCTrack
}
