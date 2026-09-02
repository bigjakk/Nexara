package virtiowin

import (
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

// The keep-set is built from ISO versions (suffix-less), because that is the
// form a filename on disk carries. A pin recorded as "0.1.285-1" must protect
// "virtio-win-0.1.285.iso".
func TestKeepSetMatchesISOFilenames(t *testing.T) {
	pinned := "0.1.285-1"
	_, isoVersion := SplitVersion(pinned)

	onDisk := VersionFromISOFilename("virtio-win-0.1.285.iso")
	if onDisk != isoVersion {
		t.Fatalf("pin %q reduces to %q but the ISO on disk reads as %q — prune would delete a pinned ISO",
			pinned, isoVersion, onDisk)
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
			err:     errString("403 Permission check failed (/nodes/pve1, Sys.AccessNetwork)"),
			wantSub: "Sys.AccessNetwork on the node",
		},
		{
			name:    "storage privilege gets its own hint",
			err:     errString("403 Permission check failed (/storage/local, Datastore.AllocateTemplate)"),
			wantSub: "Datastore.AllocateTemplate on this storage",
		},
		{
			name:    "unrelated errors pass through unchanged",
			err:     errString("connection refused"),
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

type errString string

func (e errString) Error() string { return string(e) }
