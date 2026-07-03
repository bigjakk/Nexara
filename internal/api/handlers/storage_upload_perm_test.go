package handlers

import "testing"

// TestUploadContentAllowed locks in the per-content upload permission matrix: ISO/CT-template
// uploads require manage:storage; OVA (import) uploads are allowed for either manage:storage
// or manage:vm_import; unknown content is always denied. This is the security-load-bearing
// branch of UploadFile — a manage:vm_import-only caller must never upload an ISO/vztmpl.
func TestUploadContentAllowed(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		canStorage bool
		canImport  bool
		want       bool
	}{
		{"iso with storage", "iso", true, false, true},
		{"iso with import only", "iso", false, true, false},
		{"iso with neither", "iso", false, false, false},
		{"vztmpl with storage", "vztmpl", true, false, true},
		{"vztmpl with import only", "vztmpl", false, true, false},
		{"import with storage", "import", true, false, true},
		{"import with import only", "import", false, true, true},
		{"import with both", "import", true, true, true},
		{"import with neither", "import", false, false, false},
		{"unknown content with all grants", "backup", true, true, false},
		{"empty content", "", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uploadContentAllowed(tt.content, tt.canStorage, tt.canImport); got != tt.want {
				t.Errorf("uploadContentAllowed(%q, canStorage=%v, canImport=%v) = %v, want %v",
					tt.content, tt.canStorage, tt.canImport, got, tt.want)
			}
		})
	}
}
