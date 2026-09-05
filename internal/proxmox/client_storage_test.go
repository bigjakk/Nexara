package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newCaptureServer returns a server that records the verbatim request target of
// every request it receives. It deliberately does NOT use http.ServeMux: the mux
// cleans dot segments and would normalise away the exact behaviour these tests
// exist to pin down.
func newCaptureServer(t *testing.T, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestValidateVolumeID(t *testing.T) {
	tests := []struct {
		name    string
		volume  string
		wantErr bool
	}{
		// Real Proxmox volume ids, across every storage type the UI can delete from.
		{"dir iso", "local:iso/debian-12.7.0-amd64-netinst.iso", false},
		{"dir backup", "local:backup/vzdump-qemu-100-2024_01_01-00_00_00.vma.zst", false},
		{"dir vztmpl", "local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst", false},
		{"dir snippet", "local:snippets/user-data.yml", false},
		{"dir per-vmid disk", "local:100/vm-100-disk-0.qcow2", false},
		{"lvm-thin disk", "local-lvm:vm-100-disk-0", false},
		{"zfs disk", "rpool-data:vm-101-disk-1", false},
		{"rbd disk", "ceph-vm:vm-102-disk-0", false},
		{"ct subvol", "local-zfs:subvol-103-disk-0", false},
		{"template base disk", "local-lvm:base-9000-disk-0", false},
		{"cloudinit drive", "local-lvm:vm-100-cloudinit", false},
		{"import ova", "local:import/appliance.ova", false},
		{"storage with dots and dashes", "nfs.backup-01:iso/x.iso", false},
		{"dots inside a filename", "local:iso/ubuntu-24.04.1-live-server.iso", false},
		{"space in filename", "local:iso/Windows 11 x64.iso", false},
		// Only the FIRST colon separates storage from name, so a PBS-style
		// timestamped snapshot path stays valid. Tightening the name half to
		// exclude ":" would break every PBS-backed storage.
		{"colons inside the name", "pbs-store:backup/vm/100/2024-01-01T00:00:00Z", false},

		// Traversal — the reported vulnerability.
		{"bare traversal", "../../../../access/users/root@pam", true},
		{"traversal after storage", "local:../../../../access/users/root@pam", true},
		{"traversal mid-path", "local:iso/../../../access/users/root@pam", true},
		{"single dot segment", "local:iso/./debian.iso", true},
		{"trailing dotdot", "local:iso/..", true},

		// Percent-encoding — would be decoded on the far side, smuggling the above past us.
		{"encoded traversal", "local:iso/%2e%2e%2f%2e%2e%2faccess", true},
		{"encoded slash", "local:iso%2F..%2Fx", true},
		{"lone percent", "local:iso/100%.iso", true},

		// Query / fragment injection into the outbound call.
		{"query injection", "local:iso/a.iso?force=1", true},
		{"fragment injection", "local:iso/a.iso#frag", true},

		// Structural nonsense.
		{"empty", "", true},
		{"no colon", "just-a-name", true},
		{"empty storage", ":iso/x.iso", true},
		{"empty name", "local:", true},
		{"leading slash in name", "local:/etc/passwd", true},
		{"doubled slash", "local:iso//x.iso", true},
		{"backslash", `local:iso\..\..\x`, true},
		{"newline", "local:iso/x.iso\nX-Injected: 1", true},
		{"null byte", "local:iso/x\x00.iso", true},
		// U+0085 NEL is a single rune above 0x7f, so a rune-wise control
		// check would wave it through.
		{"C1 control", "local:iso/x\u0085.iso", true},
		{"storage starts with dot", ".hidden:iso/x.iso", true},
		{"storage with slash", "loc/al:iso/x.iso", true},
		{"over length", "local:iso/" + strings.Repeat("a", 600), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVolumeID(tt.volume)
			if tt.wantErr && err == nil {
				t.Errorf("validateVolumeID(%q) = nil, want an error", tt.volume)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateVolumeID(%q) = %v, want nil", tt.volume, err)
			}
		})
	}
}

func TestDeleteStorageContent_SendsVolumeIDLiterally(t *testing.T) {
	// Regression guard on the wire format. Proxmox matches the literal path, so
	// the ":" and the "/" inside the volume id must reach it unencoded —
	// percent-encoding either one breaks every file-based volume.
	srv, seen := newCaptureServer(t, `{"data":"UPID:pve1:0000:00:00:00:imgdel::root@pam:"}`)
	c := newTestClient(t, srv.URL)

	if _, err := c.DeleteStorageContent(context.Background(),
		"pve1", "local", "local:iso/debian-12.iso"); err != nil {
		t.Fatalf("DeleteStorageContent: %v", err)
	}

	want := "/api2/json/nodes/pve1/storage/local/content/local:iso/debian-12.iso"
	if len(*seen) != 1 || (*seen)[0] != want {
		t.Errorf("request target = %v, want [%s]", *seen, want)
	}
}

func TestDeleteStorageContent_RejectsInjectionWithoutIssuingRequest(t *testing.T) {
	// The rejection has to happen before the request is built: the outbound call
	// carries the cluster's API token, so "Proxmox will reject it" is not a
	// defence — a traversal that reaches pveproxy is already a request made with
	// admin credentials against an attacker-chosen path.
	attacks := []string{
		"../../../../access/users/root@pam",
		"local:../../../../access/users/root@pam",
		"local:iso/%2e%2e%2f%2e%2e%2faccess",
		"local:iso/a.iso?force=1",
		"local:iso/a.iso#x",
	}

	for _, volume := range attacks {
		t.Run(volume, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			upid, err := c.DeleteStorageContent(context.Background(), "pve1", "local", volume)
			if err == nil {
				t.Fatalf("DeleteStorageContent(%q) succeeded, want rejection", volume)
			}
			if upid != "" {
				t.Errorf("returned upid %q, want empty", upid)
			}
			if len(*seen) != 0 {
				t.Errorf("issued %d request(s) %v, want none", len(*seen), *seen)
			}
		})
	}
}

func TestValidateISCSIPortal(t *testing.T) {
	tests := []struct {
		name    string
		portal  string
		wantErr bool
	}{
		{"bare host", "192.168.5.2", false},
		{"host with port", "192.168.5.2:3260", false},
		{"hostname", "nas.example.com", false},
		{"hostname with port", "nas.example.com:3260", false},
		{"ipv6 bracketed", "[fd00::1]:3260", false},

		{"empty", "", true},
		{"space", "192.168.5.2 3260", true},
		{"tab", "192.168.5.2\t", true},
		{"path separator", "192.168.5.2/target", true},
		{"query string", "192.168.5.2?x=1", true},
		{"fragment", "192.168.5.2#x", true},
		{"newline", "192.168.5.2\niscsi", true},
		{"over length", strings.Repeat("a", 256), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateISCSIPortal(tt.portal)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateISCSIPortal(%q) error = %v, wantErr %v", tt.portal, err, tt.wantErr)
			}
		})
	}
}

func TestScanISCSI_RequestShapeAndDecoding(t *testing.T) {
	srv, seen := newCaptureServer(t,
		`{"data":[{"target":"iqn.2005-10.org.freenas.ctl:test","portal":"192.168.5.2:3260"}]}`)
	c := newTestClient(t, srv.URL)

	targets, err := c.ScanISCSI(context.Background(), "pve1", "192.168.5.2")
	if err != nil {
		t.Fatalf("ScanISCSI: %v", err)
	}

	want := "/api2/json/nodes/pve1/scan/iscsi?portal=192.168.5.2"
	if len(*seen) != 1 || (*seen)[0] != want {
		t.Errorf("request target = %v, want [%s]", *seen, want)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets))
	}
	if targets[0].Target != "iqn.2005-10.org.freenas.ctl:test" {
		t.Errorf("target = %q", targets[0].Target)
	}
	if targets[0].Portal != "192.168.5.2:3260" {
		t.Errorf("portal = %q", targets[0].Portal)
	}
}

func TestScanISCSI_RejectsBadInputWithoutIssuingRequest(t *testing.T) {
	// Discovery makes the node dial a caller-supplied address, so a malformed
	// portal must be refused here rather than forwarded with the cluster's token.
	cases := []struct{ node, portal string }{
		{"pve1", ""},
		{"pve1", "192.168.5.2/../../access"},
		{"pve1", "192.168.5.2?x=1"},
		{"", "192.168.5.2"},
		{"../other", "192.168.5.2"},
	}

	for _, tc := range cases {
		t.Run(tc.node+"|"+tc.portal, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":[]}`)
			c := newTestClient(t, srv.URL)

			if _, err := c.ScanISCSI(context.Background(), tc.node, tc.portal); err == nil {
				t.Fatalf("ScanISCSI(%q, %q) succeeded, want rejection", tc.node, tc.portal)
			}
			if len(*seen) != 0 {
				t.Errorf("issued %d request(s) %v, want none", len(*seen), *seen)
			}
		})
	}
}
