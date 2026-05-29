package proxmox

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in                 string
		wMaj, wMin, wPatch int
		wOK                bool
	}{
		{"9.1.2", 9, 1, 2, true},
		{"9.1", 9, 1, 0, true},
		{"9", 9, 0, 0, true},
		{"  9.2.0  ", 9, 2, 0, true},
		{"9.1.2-3", 9, 1, 2, true}, // trailing build suffix ignored
		{"", 0, 0, 0, false},
		{"abc", 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			maj, min, patch, ok := ParseVersion(tt.in)
			if maj != tt.wMaj || min != tt.wMin || patch != tt.wPatch || ok != tt.wOK {
				t.Errorf("ParseVersion(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
					tt.in, maj, min, patch, ok, tt.wMaj, tt.wMin, tt.wPatch, tt.wOK)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		name    string
		current string
		min     string
		want    bool
	}{
		{"equal major.minor", "9.1", "9.1", true},
		{"patch ge", "9.1.2", "9.1", true},
		{"patch lt", "9.1.2", "9.1.3", false},
		{"major gt", "9.0", "8.4", true},
		{"major lt", "8.4.1", "9.0", false},
		{"minor gt", "9.2", "9.1", true},
		{"minor lt", "9.1.8", "9.2", false},
		{"crs dynamic on 9.2.0", "9.2.0", CapCRSDynamic, true},
		{"crs dynamic on 9.1.8", "9.1.8", CapCRSDynamic, false},
		{"ha rules on 9.0", "9.0", CapHARules, true},
		{"empty current", "", "9.0", false},
		{"garbage current", "not-a-version", "9.0", false},
		{"full release vs minor", "9.2.1", "9.2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VersionAtLeast(tt.current, tt.min); got != tt.want {
				t.Errorf("VersionAtLeast(%q, %q) = %v, want %v", tt.current, tt.min, got, tt.want)
			}
		})
	}
}
