package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// --- MoveDisk ---

func TestMoveDisk_FormEncoding(t *testing.T) {
	tests := []struct {
		name   string
		params DiskMoveParams
		want   map[string]string
		absent []string
	}{
		{
			name:   "storage only",
			params: DiskMoveParams{Disk: "scsi0", Storage: "local-lvm"},
			want:   map[string]string{"disk": "scsi0", "storage": "local-lvm"},
			absent: []string{"format", "delete", "bwlimit"},
		},
		{
			name:   "format conversion",
			params: DiskMoveParams{Disk: "scsi0", Storage: "nfs-store", Format: "qcow2"},
			want:   map[string]string{"disk": "scsi0", "storage": "nfs-store", "format": "qcow2"},
			absent: []string{"delete", "bwlimit"},
		},
		{
			name:   "delete source and throttle",
			params: DiskMoveParams{Disk: "virtio1", Storage: "ceph", Delete: true, BWLimit: 51200},
			want:   map[string]string{"disk": "virtio1", "storage": "ceph", "delete": "1", "bwlimit": "51200"},
			absent: []string{"format"},
		},
		{
			// A zero bwlimit means "storage default", which PVE expresses by
			// omitting the key — sending 0 would mean "unlimited" instead.
			name:   "zero bwlimit omitted",
			params: DiskMoveParams{Disk: "scsi0", Storage: "local", BWLimit: 0},
			want:   map[string]string{"disk": "scsi0", "storage": "local"},
			absent: []string{"bwlimit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody string
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/qemu/100/move_disk": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					body, _ := io.ReadAll(r.Body)
					capturedBody = string(body)
					jsonResponse(w, "UPID:pve1:00001234:0001ABCD:65000000:qmmove:100:user@pam:")
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			upid, err := c.MoveDisk(context.Background(), "pve1", 100, tt.params)
			if err != nil {
				t.Fatalf("MoveDisk: %v", err)
			}
			if upid == "" {
				t.Error("expected a UPID")
			}
			assertForm(t, capturedBody, tt.want, tt.absent)
		})
	}
}

// --- MoveCTVolume ---

func TestMoveCTVolume_FormEncoding(t *testing.T) {
	tests := []struct {
		name   string
		params CTVolumeMoveParams
		want   map[string]string
		absent []string
	}{
		{
			name:   "storage only",
			params: CTVolumeMoveParams{Volume: "rootfs", Storage: "local-lvm"},
			want:   map[string]string{"volume": "rootfs", "storage": "local-lvm"},
			absent: []string{"delete", "bwlimit", "format"},
		},
		{
			name:   "delete source and throttle",
			params: CTVolumeMoveParams{Volume: "mp0", Storage: "ceph", Delete: true, BWLimit: 1024},
			want:   map[string]string{"volume": "mp0", "storage": "ceph", "delete": "1", "bwlimit": "1024"},
			absent: []string{"format"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody string
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/lxc/200/move_volume": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					body, _ := io.ReadAll(r.Body)
					capturedBody = string(body)
					jsonResponse(w, "UPID:pve1:00001234:0001ABCD:65000000:move_volume:200:user@pam:")
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			upid, err := c.MoveCTVolume(context.Background(), "pve1", 200, tt.params)
			if err != nil {
				t.Fatalf("MoveCTVolume: %v", err)
			}
			if upid == "" {
				t.Error("expected a UPID")
			}
			assertForm(t, capturedBody, tt.want, tt.absent)
		})
	}
}

// assertForm checks that a url-encoded request body carries exactly the
// expected values and none of the keys listed in absent.
func assertForm(t *testing.T, body string, want map[string]string, absent []string) {
	t.Helper()
	form, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("body not url-encoded: %v", err)
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("form[%q] = %q, want %q", k, got, v)
		}
	}
	for _, k := range absent {
		if form.Has(k) {
			t.Errorf("form[%q] should be absent, got %q", k, form.Get(k))
		}
	}
}
