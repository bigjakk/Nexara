package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// testConfigDigest is the SHA-1 Proxmox returns with a guest config, and
// takes back on the config PUT as a compare-and-swap token. Synthetic hex.
const testConfigDigest = "aabbccddeeff00112233445566778899aabbccdd"

// --- DiskAttachParams.Validate ---

// TestDiskAttachParamsValidate is the choke point's own test. Every case
// below is a value that would otherwise be concatenated into
// "storage:size[,format=fmt]" and produce a DIFFERENT, valid spec rather
// than an error — which is what makes this worth checking here rather
// than only in whichever handler happens to call AttachDisk.
func TestDiskAttachParamsValidate(t *testing.T) {
	valid := DiskAttachParams{Bus: "scsi", Index: 0, Storage: "store01", Size: "20", Digest: testConfigDigest}

	tests := []struct {
		name    string
		mutate  func(*DiskAttachParams)
		wantErr string
	}{
		{name: "the ordinary case", mutate: func(*DiskAttachParams) {}},
		{
			name:   "qcow2 format",
			mutate: func(p *DiskAttachParams) { p.Format = ImageFormatQcow2 },
		},
		{
			name:   "empty format means the storage decides",
			mutate: func(p *DiskAttachParams) { p.Format = "" },
		},
		{
			name:    "unknown bus",
			mutate:  func(p *DiskAttachParams) { p.Bus = "nvme" },
			wantErr: "bus must be one of",
		},
		{
			name:    "empty bus",
			mutate:  func(p *DiskAttachParams) { p.Bus = "" },
			wantErr: "bus must be one of",
		},
		{
			name:    "index past the bus's last slot",
			mutate:  func(p *DiskAttachParams) { p.Bus, p.Index = "ide", 4 },
			wantErr: "ide index must be between 0 and 3",
		},
		{
			name:    "negative index",
			mutate:  func(p *DiskAttachParams) { p.Index = -1 },
			wantErr: "scsi index must be between 0 and 30",
		},
		{
			name:    "missing storage",
			mutate:  func(p *DiskAttachParams) { p.Storage = "" },
			wantErr: "storage is required",
		},
		{
			// "store01:vm-9-disk-0" as a STORAGE would make the spec read
			// "store01:vm-9-disk-0:20" — a different volume entirely.
			name:    "storage containing a colon",
			mutate:  func(p *DiskAttachParams) { p.Storage = "store01:vm-9-disk-0" },
			wantErr: "restructure the volume spec",
		},
		{
			name:    "storage containing a comma",
			mutate:  func(p *DiskAttachParams) { p.Storage = "store01,backup=0" },
			wantErr: "restructure the volume spec",
		},
		{
			// The exact value this package's own doc comment used to
			// recommend, and the one PVE answers with a parse error.
			name:    `the documented "20G"`,
			mutate:  func(p *DiskAttachParams) { p.Size = "20G" },
			wantErr: "size must be a bare count of gibibytes",
		},
		{
			name:    "empty size",
			mutate:  func(p *DiskAttachParams) { p.Size = "" },
			wantErr: "size must be a bare count of gibibytes",
		},
		{
			name:    "zero size",
			mutate:  func(p *DiskAttachParams) { p.Size = "0" },
			wantErr: "size must be a bare count of gibibytes",
		},
		{
			name:    "fractional size",
			mutate:  func(p *DiskAttachParams) { p.Size = "1.5" },
			wantErr: "size must be a bare count of gibibytes",
		},
		{
			name:    "size smuggling a second option",
			mutate:  func(p *DiskAttachParams) { p.Size = "20,backup=0" },
			wantErr: "size must be a bare count of gibibytes",
		},
		{
			// The compare-and-swap token that stops two concurrent attaches
			// choosing the same slot and the second replacing the first.
			// Required rather than optional, so that a caller cannot forget
			// the read the write is supposed to be pinned to.
			name:    "missing digest",
			mutate:  func(p *DiskAttachParams) { p.Digest = "" },
			wantErr: "digest is required",
		},
		{
			name:    "unknown format",
			mutate:  func(p *DiskAttachParams) { p.Format = "qcow3" },
			wantErr: "format must be one of",
		},
		{
			name:    "format smuggling a second option",
			mutate:  func(p *DiskAttachParams) { p.Format = "raw,backup=0" },
			wantErr: "format must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := valid
			tt.mutate(&params)

			err := params.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() accepted %+v", params)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to mention %q", err.Error(), tt.wantErr)
			}
			// mapProxmoxError turns ErrInvalidInput into a 400 rather than
			// blaming Proxmox for a request we refused to send.
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Validate() error does not wrap ErrInvalidInput, so the API layer would report it as a 500")
			}
		})
	}
}

func TestMaxDiskIndex(t *testing.T) {
	want := map[string]int{"ide": 3, "sata": 5, "virtio": 15, "scsi": 30}
	for bus, wantMax := range want {
		got, ok := MaxDiskIndex(bus)
		if !ok {
			t.Errorf("MaxDiskIndex(%q) reports the bus is unknown", bus)
			continue
		}
		if got != wantMax {
			t.Errorf("MaxDiskIndex(%q) = %d, want %d", bus, got, wantMax)
		}
	}
	if _, ok := MaxDiskIndex("nvme"); ok {
		t.Error("MaxDiskIndex accepted a bus Proxmox has no disk controller for")
	}

	// DiskBuses is what the endpoint declares as its enum, so a bus listed
	// there with no ceiling here would pass validation and then produce a
	// config key Proxmox rejects.
	for _, bus := range DiskBuses {
		if _, ok := MaxDiskIndex(bus); !ok {
			t.Errorf("DiskBuses lists %q but MaxDiskIndex does not know it", bus)
		}
	}
	if len(DiskBuses) != len(want) {
		t.Errorf("DiskBuses has %d entries, MaxDiskIndex knows %d", len(DiskBuses), len(want))
	}
}

func TestDiskAttachParamsDiskKey(t *testing.T) {
	for _, tt := range []struct {
		params DiskAttachParams
		want   string
	}{
		{DiskAttachParams{Bus: "scsi", Index: 0}, "scsi0"},
		{DiskAttachParams{Bus: "virtio", Index: 15}, "virtio15"},
		{DiskAttachParams{Bus: "ide", Index: 2}, "ide2"},
	} {
		if got := tt.params.DiskKey(); got != tt.want {
			t.Errorf("DiskKey() = %q, want %q", got, tt.want)
		}
	}
}

// --- AttachDisk ---

// TestAttachDiskVolumeSpec pins the config key and the volume spec the
// client actually writes.
func TestAttachDiskVolumeSpec(t *testing.T) {
	tests := []struct {
		name   string
		params DiskAttachParams
		want   map[string]string
	}{
		{
			name:   "no format lets the storage decide",
			params: DiskAttachParams{Bus: "scsi", Index: 1, Storage: "store01", Size: "32", Digest: testConfigDigest},
			want:   map[string]string{"scsi1": "store01:32", "digest": testConfigDigest},
		},
		{
			name:   "format is appended as an option",
			params: DiskAttachParams{Bus: "virtio", Index: 0, Storage: "store02", Size: "500", Format: ImageFormatQcow2, Digest: testConfigDigest},
			want:   map[string]string{"virtio0": "store02:500,format=qcow2", "digest": testConfigDigest},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody string
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve1/qemu/100/config": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPut {
						t.Errorf("expected PUT, got %s", r.Method)
					}
					body, _ := io.ReadAll(r.Body)
					capturedBody = string(body)
					jsonResponse(w, nil)
				},
			})
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			if err := c.AttachDisk(context.Background(), "pve1", 100, tt.params); err != nil {
				t.Fatalf("AttachDisk: %v", err)
			}

			form, err := url.ParseQuery(capturedBody)
			if err != nil {
				t.Fatalf("parse body %q: %v", capturedBody, err)
			}
			for key, wantValue := range tt.want {
				if got := form.Get(key); got != wantValue {
					t.Errorf("%s = %q, want %q", key, got, wantValue)
				}
			}
		})
	}
}

// TestAttachDiskRefusesBeforeReachingProxmox proves the validation is a
// gate rather than a comment: a bad spec must not leave the process.
func TestAttachDiskRefusesBeforeReachingProxmox(t *testing.T) {
	var called bool
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/qemu/100/config": func(w http.ResponseWriter, _ *http.Request) {
			called = true
			jsonResponse(w, nil)
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.AttachDisk(context.Background(), "pve1", 100,
		DiskAttachParams{Bus: "scsi", Index: 0, Storage: "store01", Size: "20G", Digest: testConfigDigest})
	if err == nil {
		t.Fatal(`AttachDisk accepted a size of "20G"`)
	}
	if called {
		t.Error("the config write reached Proxmox although the parameters were refused")
	}
}
