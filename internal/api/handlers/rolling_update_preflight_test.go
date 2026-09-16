package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/rolling"
)

// TestFoldPreflight is the coverage whose absence let this bug ship twice.
//
// The strict policy refuses a job when the pre-flight reports errors. Both
// checks used to be folded in with `if err == nil`, so a check that FAILED
// contributed no conflicts and left hasErrors false — the gate answering "all
// clear" precisely because it had learned nothing. The first attempt at a fix
// moved that swallow from the analyzer up into the caller without removing it,
// and every test stayed green because nothing here existed.
//
// The row that matters most is "neither check ran": before, that was
// indistinguishable from a clean cluster.
func TestFoldPreflight(t *testing.T) {
	haFail := errors.New("list HA rules: proxmox API error 503: cluster is not quorate")
	capFail := errors.New("get nodes: connection failed")

	conflict := func(source string) rolling.HAConflict {
		return rolling.HAConflict{Source: source, Severity: "error", Message: source + " conflict"}
	}

	tests := []struct {
		name          string
		report        *rolling.HAPreFlightReport
		haErr         error
		capConflicts  []rolling.HAConflict
		capHasErrors  bool
		capErr        error
		wantErrors    bool
		wantConflicts int
		wantMentions  []string
	}{
		{
			name:   "both checks clean",
			report: &rolling.HAPreFlightReport{},
		},
		{
			name:          "a real HA conflict is reported",
			report:        &rolling.HAPreFlightReport{Conflicts: []rolling.HAConflict{conflict("ha_rule")}, HasErrors: true},
			wantErrors:    true,
			wantConflicts: 1,
		},
		{
			// A warning-only report must NOT trip the strict gate, or strict
			// would refuse every job that has any soft affinity preference.
			name:          "warnings alone do not trip the gate",
			report:        &rolling.HAPreFlightReport{Conflicts: []rolling.HAConflict{conflict("drs_rule")}},
			wantErrors:    false,
			wantConflicts: 1,
		},
		{
			name:          "an HA check that could not run is an error",
			haErr:         haFail,
			wantErrors:    true,
			wantConflicts: 1,
			wantMentions:  []string{"HA constraint pre-flight could not run", "not quorate"},
		},
		{
			name:          "a capacity check that could not run is an error",
			report:        &rolling.HAPreFlightReport{},
			capErr:        capFail,
			wantErrors:    true,
			wantConflicts: 1,
			wantMentions:  []string{"Capacity pre-flight could not run"},
		},
		{
			// The case the whole change exists for. Both checks failed, so
			// nothing is known about this cluster — and that must not read the
			// same as "both checks passed".
			name:          "neither check ran",
			haErr:         haFail,
			capErr:        capFail,
			wantErrors:    true,
			wantConflicts: 2,
			wantMentions:  []string{"HA constraint", "Capacity"},
		},
		{
			// A nil report with no error should not invent an error either;
			// that is the analyzer declining to say anything, not a failure.
			name:       "a nil report without an error is not an error",
			wantErrors: false,
		},
		{
			// Pins the `||`: an HA error must survive a capacity check that
			// found nothing wrong. Assignment instead of or-ing would drop it.
			name:          "an HA error is not cleared by a clean capacity check",
			report:        &rolling.HAPreFlightReport{Conflicts: []rolling.HAConflict{conflict("ha_rule")}, HasErrors: true},
			capConflicts:  []rolling.HAConflict{conflict("capacity")},
			capHasErrors:  false,
			wantErrors:    true,
			wantConflicts: 2,
		},
		{
			name:          "a real capacity conflict is reported",
			report:        &rolling.HAPreFlightReport{},
			capConflicts:  []rolling.HAConflict{conflict("capacity")},
			capHasErrors:  true,
			wantErrors:    true,
			wantConflicts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflicts, hasErrors := foldPreflight(tt.report, tt.haErr, tt.capConflicts, tt.capHasErrors, tt.capErr)
			// Never nil, even with nothing to report. PreflightHA serialises
			// this onto report.Conflicts and the wizard reads
			// `preflightReport.conflicts.length` with no null guard, so a nil
			// slice here crashes the dialog on a clean cluster — the common
			// answer. An extraction that used a bare `var` did exactly that.
			if conflicts == nil {
				t.Error("conflicts is nil; it serialises as null and the wizard dereferences .length on it")
			}
			if hasErrors != tt.wantErrors {
				t.Errorf("hasErrors = %v, want %v — this is the boolean the strict gate reads", hasErrors, tt.wantErrors)
			}
			if len(conflicts) != tt.wantConflicts {
				t.Fatalf("conflicts = %d (%+v), want %d", len(conflicts), conflicts, tt.wantConflicts)
			}
			joined := ""
			for _, c := range conflicts {
				joined += c.Message + "\n"
			}
			for _, want := range tt.wantMentions {
				if !strings.Contains(joined, want) {
					t.Errorf("conflicts do not mention %q; the operator is told a check failed but not which or why:\n%s", want, joined)
				}
			}
		})
	}
}

// TestPreflightUnavailable_IsWellFormedForEveryConsumer pins the shape of the
// synthetic conflict. It is stored in ha_warnings and rendered by two frontend
// components, so a missing severity or type is not a cosmetic problem: severity
// is what makes the strict gate treat it as blocking.
func TestPreflightUnavailable_IsWellFormedForEveryConsumer(t *testing.T) {
	c := preflightUnavailable("HA constraint", errors.New("boom"))

	if c.Severity != "error" {
		t.Errorf("severity = %q, want %q — anything else and strict would let the job through", c.Severity, "error")
	}
	if c.Type != "preflight_unavailable" {
		t.Errorf("type = %q", c.Type)
	}
	if c.Source == "" || c.Message == "" {
		t.Errorf("source=%q message=%q — both are rendered", c.Source, c.Message)
	}
	// Node is deliberately empty: no single node caused this. The progress view
	// only renders the label when it is set, so leaving it blank must stay safe.
	if c.Node != "" {
		t.Errorf("node = %q, want empty — the failure is not attributable to one node", c.Node)
	}
}
