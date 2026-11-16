package handlers

import (
	"slices"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The DRS vocabularies are declared once, in drs.go, and registry_drs.go uses them as its
// enums; there is deliberately no second copy. What is checked is that every value the
// code branches on by name is in the list the API accepts: one outside it is a branch no
// request can reach.

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

// The config card reads the weights the engine plans with, so a weight stored
// before UpdateConfig refused it comes back as the defaults rather than as a
// value the card's normalisation turns into NaN.
func TestDRSConfigResponseShowsTheWeightsDRSUses(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		`{"cpu":0.4,"memory":0.6}`:     `{"cpu":0.4,"memory":0.6}`,
		`{"cpu":1e308,"memory":1e308}`: `{"cpu":0.3,"memory":0.7}`,
	} {
		if got := toDRSConfigResponse(db.DrsConfig{Weights: []byte(raw)}).Weights; string(got) != want {
			t.Errorf("stored %s: response weights = %s, want %s", raw, got, want)
		}
	}
}
