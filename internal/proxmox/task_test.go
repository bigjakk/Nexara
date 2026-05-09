package proxmox

import "testing"

func TestTaskSucceeded(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"OK", "OK", true},
		{"ok lowercase", "ok", true},
		{"OK with whitespace", "  OK  ", true},
		{"OK with warnings", "OK (with warnings)", true},
		{"OK prefix lowercase", "ok (warnings)", true},
		{"WARNINGS", "WARNINGS", true},
		{"warnings lowercase", "warnings", true},
		{"FAILED", "FAILED", false},
		{"interrupted", "interrupted by signal", false},
		{"timed out", "timed out", false},
		{"random text", "something went wrong", false},
		{"OK suffix is not a prefix", "WAS-OK", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskSucceeded(tc.in); got != tc.want {
				t.Errorf("TaskSucceeded(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
