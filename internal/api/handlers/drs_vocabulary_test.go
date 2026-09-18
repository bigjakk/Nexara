package handlers

import (
	"slices"
	"testing"
)

// The two DRS vocabularies are declared once, in drs.go, and the endpoint
// declarations in internal/api/registry_drs.go use them as their enums.
// There is deliberately no second copy of either — the membership maps
// they replaced were the hand-rolled validators the schema now performs,
// and keeping them as well would have been exactly the "second copy that
// silently rots" this migration exists to remove.
//
// What is left to check is therefore not "do two lists agree" but "does
// every value the code branches on by name appear in the list the API
// accepts". A branch literal outside the vocabulary is a branch no request
// can ever reach.

// TestDRSModesCoverTheBranchesThatReadThem pins the two mode literals this
// package still compares against.
//
// UpdateConfig derives the stored `enabled` flag from `mode != "disabled"`,
// and TriggerEvaluate queues an execution only when the stored mode is
// "automatic". Either literal falling outside DRSModes would be a branch
// the schema makes unreachable — DRS would accept the mode, store it, and
// never act on it.
func TestDRSModesCoverTheBranchesThatReadThem(t *testing.T) {
	for _, branch := range []string{
		"disabled",  // UpdateConfig: enabled := mode != "disabled"
		"automatic", // TriggerEvaluate: queue the run for the scheduler leader
	} {
		if !slices.Contains(DRSModes, branch) {
			t.Errorf("this package branches on mode %q, which DRSModes does not offer — the schema's "+
				"enum makes that branch unreachable", branch)
		}
	}

	// The vocabulary itself is the API contract, and the frontend's DRSMode
	// union says the same three. Stated so that dropping one is a failure
	// here rather than a dropdown that silently loses an option.
	if want := []string{"disabled", "advisory", "automatic"}; !slices.Equal(DRSModes, want) {
		t.Errorf("DRSModes = %v, want %v", DRSModes, want)
	}
}

// TestDRSRuleTypesAllMapToAProxmoxRule pins the claim that justifies
// declaring rule_type as an enum at all: these three values are NEXARA's,
// and every one of them has a mapping onto a Proxmox HA rule. A fourth
// added to the vocabulary without a case in CreateHARule's switch would
// leave haRuleType empty and send Proxmox a rule with no type.
func TestDRSRuleTypesAllMapToAProxmoxRule(t *testing.T) {
	want := map[string]string{
		"pin":           "node-affinity",
		"affinity":      "resource-affinity",
		"anti-affinity": "resource-affinity",
	}
	for _, rt := range DRSRuleTypes {
		if _, ok := want[rt]; !ok {
			t.Errorf("DRSRuleTypes offers %q, which CreateHARule's switch has no case for — "+
				"the rule would reach Proxmox with an empty type", rt)
		}
	}
	if len(want) != len(DRSRuleTypes) {
		t.Errorf("this test names %d mappings but DRSRuleTypes has %d entries", len(want), len(DRSRuleTypes))
	}
}
