package virtiowin

import (
	"errors"
	"strings"
	"testing"
)

// Prune deletes files. Everything it must NOT touch is encoded here: anything
// that is not a virtio-win ISO by naming convention returns "", and prune skips
// on "". An operator's unrelated ISO sharing the storage has to stay invisible.
func TestVersionFromISOFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"virtio-win ISO", "virtio-win-0.1.302.iso", "0.1.302"},
		{"older virtio-win ISO", "virtio-win-0.1.96.iso", "0.1.96"},

		{"unrelated ISO is not ours", "ubuntu-24.04-live-server-amd64.iso", ""},
		{"windows media is not ours", "Win11_23H2_English_x64.iso", ""},
		{"lookalike prefix without version", "virtio-win.iso", ""},
		{"lookalike prefix with a word", "virtio-win-latest.iso", ""},
		{"right name, wrong extension", "virtio-win-0.1.302.img", ""},
		{"guest tools installer, not the ISO", "virtio-win-guest-tools.exe", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VersionFromISOFilename(tt.filename); got != tt.want {
				t.Errorf("VersionFromISOFilename(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestDescribeDownloadError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string
	}{
		{
			name:    "network privilege gets the actionable hint",
			err:     errors.New("403 Permission check failed (/nodes/pve1, Sys.AccessNetwork)"),
			wantSub: "Sys.AccessNetwork on the node",
		},
		{
			name:    "storage privilege gets its own hint",
			err:     errors.New("403 Permission check failed (/storage/local, Datastore.AllocateTemplate)"),
			wantSub: "Datastore.AllocateTemplate on this storage",
		},
		{
			name:    "unrelated errors pass through unchanged",
			err:     errors.New("connection refused"),
			wantSub: "connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeDownloadError(tt.err)
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("describeDownloadError(%v) = %q, want it to mention %q", tt.err, got, tt.wantSub)
			}
		})
	}
}
