package handlers

import (
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestDeriveURLFilename(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"simple", "https://example.com/appliance.ova", "appliance.ova"},
		{"with query", "https://example.com/path/vm.ova?token=abc", "vm.ova"},
		{"percent-encoded", "https://example.com/my%20vm.ova", "my vm.ova"},
		{"trailing slash", "https://example.com/dir/", ""},
		{"no path", "https://example.com", ""},
		{"basename of traversal path is safe", "https://example.com/../etc/passwd", "passwd"},
		{"dotdot basename rejected", "https://example.com/foo/..", ""},
		{"unparseable", "://nope", ""},
		{"root only", "https://example.com/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveURLFilename(tt.url); got != tt.want {
				t.Errorf("deriveURLFilename(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestMergeContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"appends when absent", "images,iso", "images,iso,import"},
		{"idempotent when present", "images,import,iso", "images,import,iso"},
		{"empty becomes want", "", "import"},
		{"present with spaces", "images, import", "images, import"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeContent(tt.content, "import"); got != tt.want {
				t.Errorf("mergeContent(%q, import) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsSharedImportStorage(t *testing.T) {
	tests := []struct {
		name string
		cfg  proxmox.StorageConfig
		want bool
	}{
		{"explicit shared dir", proxmox.StorageConfig{Type: "dir", Shared: 1}, true},
		{"non-shared dir", proxmox.StorageConfig{Type: "dir", Shared: 0}, false},
		{"nfs implicit shared", proxmox.StorageConfig{Type: "nfs", Shared: 0}, true},
		{"cifs implicit shared", proxmox.StorageConfig{Type: "cifs"}, true},
		{"esxi implicit shared", proxmox.StorageConfig{Type: "esxi"}, true},
		{"lvm not shared", proxmox.StorageConfig{Type: "lvmthin"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSharedImportStorage(tt.cfg); got != tt.want {
				t.Errorf("isSharedImportStorage(%+v) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestParseNodeRestriction(t *testing.T) {
	if got := parseNodeRestriction(""); got != nil {
		t.Errorf("empty should be nil (unrestricted), got %v", got)
	}
	if got := parseNodeRestriction("  "); got != nil {
		t.Errorf("whitespace should be nil (unrestricted), got %v", got)
	}
	got := parseNodeRestriction("pve1, pve2 ,pve3")
	if len(got) != 3 || !got["pve1"] || !got["pve2"] || !got["pve3"] {
		t.Errorf("parseNodeRestriction returned %v, want {pve1,pve2,pve3}", got)
	}
}

func TestIsHTTPURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/x.ova", true},
		{"http://example.com/x.ova", true},
		{"ftp://example.com/x.ova", false},
		{"file:///etc/passwd", false},
		{"not-a-url", false},
		{"https://", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := isHTTPURL(tt.url); got != tt.want {
				t.Errorf("isHTTPURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
