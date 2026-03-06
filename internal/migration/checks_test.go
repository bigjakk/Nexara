package migration

import (
	"testing"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]bool
	}{
		{
			name:  "empty",
			input: "",
			want:  map[string]bool{},
		},
		{
			name:  "single flag",
			input: "vmx",
			want:  map[string]bool{"vmx": true},
		},
		{
			name:  "multiple flags",
			input: "fpu vme de pse tsc msr pae mce cx8 apic vmx",
			want: map[string]bool{
				"fpu": true, "vme": true, "de": true, "pse": true,
				"tsc": true, "msr": true, "pae": true, "mce": true,
				"cx8": true, "apic": true, "vmx": true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFlags(tc.input)
			if len(got) != len(tc.want) {
				t.Errorf("parseFlags(%q) returned %d flags, want %d", tc.input, len(got), len(tc.want))
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("parseFlags(%q)[%q] = %v, want %v", tc.input, k, got[k], v)
				}
			}
		})
	}
}

func TestFormatMapping(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]string
		want  int // number of pairs
	}{
		{
			name:  "empty",
			input: map[string]string{},
			want:  0,
		},
		{
			name:  "single mapping",
			input: map[string]string{"local": "ceph"},
			want:  1,
		},
		{
			name:  "multiple mappings",
			input: map[string]string{"local": "ceph", "local-lvm": "ceph-pool"},
			want:  2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMapping(tc.input)
			if tc.want == 0 {
				if got != "" {
					t.Errorf("formatMapping(%v) = %q, want empty", tc.input, got)
				}
				return
			}
			// Count colons to verify pairs.
			pairs := 0
			for _, c := range got {
				if c == ':' {
					pairs++
				}
			}
			if pairs != tc.want {
				t.Errorf("formatMapping(%v) = %q, has %d pairs, want %d", tc.input, got, pairs, tc.want)
			}
		})
	}
}

func TestPreFlightReportAddCheck(t *testing.T) {
	report := &PreFlightReport{
		Checks: make([]CheckResult, 0),
		Passed: true,
	}

	// Add passing check.
	report.addCheck(CheckResult{
		Name:     "test1",
		Severity: SeverityPass,
		Message:  "OK",
	})

	if !report.Passed {
		t.Error("report should still be passed after adding a pass check")
	}
	if len(report.Checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(report.Checks))
	}

	// Add warning check — should NOT fail the report.
	report.addCheck(CheckResult{
		Name:     "test2",
		Severity: SeverityWarn,
		Message:  "Warning",
	})

	if !report.Passed {
		t.Error("report should still be passed after adding a warn check")
	}

	// Add failing check — should fail the report.
	report.addCheck(CheckResult{
		Name:     "test3",
		Severity: SeverityFail,
		Message:  "Failed",
	})

	if report.Passed {
		t.Error("report should be failed after adding a fail check")
	}
	if len(report.Checks) != 3 {
		t.Errorf("expected 3 checks, got %d", len(report.Checks))
	}
}
