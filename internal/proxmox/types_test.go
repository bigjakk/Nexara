package proxmox

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCephHealthNormalizedChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input CephHealth
		want  []CephHealthCheckItem
	}{
		{
			name:  "no checks returns empty non-nil slice",
			input: CephHealth{Status: "HEALTH_OK"},
			want:  []CephHealthCheckItem{},
		},
		{
			name: "errors sort before warnings, detail captured",
			input: CephHealth{
				Status: "HEALTH_ERR",
				Checks: map[string]CephHealthCheck{
					"MON_DISK_LOW": {
						Severity: "HEALTH_WARN",
						Summary:  CephHealthCheckSummary{Message: "mon pve1 is low on available space"},
					},
					"OSD_DOWN": {
						Severity: "HEALTH_ERR",
						Summary:  CephHealthCheckSummary{Message: "1 osds down"},
						Detail:   []CephHealthCheckDetail{{Message: "osd.1 (pve2) is down"}},
					},
					"AUTH_INSECURE_GLOBAL_ID_RECLAIM": {
						Severity: "HEALTH_WARN",
						Summary:  CephHealthCheckSummary{Message: "client is using insecure global_id reclaim"},
					},
				},
			},
			want: []CephHealthCheckItem{
				{Type: "OSD_DOWN", Severity: "HEALTH_ERR", Message: "1 osds down", Detail: []string{"osd.1 (pve2) is down"}},
				{Type: "AUTH_INSECURE_GLOBAL_ID_RECLAIM", Severity: "HEALTH_WARN", Message: "client is using insecure global_id reclaim", Detail: []string{}},
				{Type: "MON_DISK_LOW", Severity: "HEALTH_WARN", Message: "mon pve1 is low on available space", Detail: []string{}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.input.NormalizedChecks()
			if got == nil {
				t.Fatal("NormalizedChecks() returned nil; want non-nil slice")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizedChecks() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

// TestCephHealthUnmarshalChecks verifies the checks map (incl. the detail[]
// specifics) is parsed from the Proxmox /ceph/status payload shape.
func TestCephHealthUnmarshalChecks(t *testing.T) {
	t.Parallel()

	const payload = `{
		"status": "HEALTH_WARN",
		"checks": {
			"DAEMON_OLD_VERSION": {
				"severity": "HEALTH_WARN",
				"summary": {"message": "There are daemons running an older version of ceph", "count": 1},
				"detail": [{"message": "mon.pve2 osd.1 mds.pve2 are running an older version of ceph: 20.2.0"}]
			}
		}
	}`

	var h CephHealth
	if err := json.Unmarshal([]byte(payload), &h); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	items := h.NormalizedChecks()
	if len(items) != 1 {
		t.Fatalf("got %d checks, want 1", len(items))
	}
	got := items[0]
	if got.Type != "DAEMON_OLD_VERSION" || got.Message != "There are daemons running an older version of ceph" {
		t.Errorf("unexpected check: %+v", got)
	}
	if len(got.Detail) != 1 ||
		got.Detail[0] != "mon.pve2 osd.1 mds.pve2 are running an older version of ceph: 20.2.0" {
		t.Errorf("detail not captured: %+v", got.Detail)
	}
}

func TestVolumeFilename(t *testing.T) {
	tests := []struct {
		name  string
		volid string
		want  string
	}{
		{"standard iso volid", "local:iso/virtio-win-0.1.302.iso", "virtio-win-0.1.302.iso"},
		{"nested path", "nfs-store:iso/sub/dir/virtio-win-0.1.96.iso", "virtio-win-0.1.96.iso"},
		{"no slash falls back to the colon", "local:virtio-win-0.1.302.iso", "virtio-win-0.1.302.iso"},
		{"bare name", "virtio-win-0.1.302.iso", "virtio-win-0.1.302.iso"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VolumeFilename(tt.volid); got != tt.want {
				t.Errorf("VolumeFilename(%q) = %q, want %q", tt.volid, got, tt.want)
			}
		})
	}
}

func TestVMConfigCDROMDrives(t *testing.T) {
	tests := []struct {
		name   string
		config VMConfig
		want   []CDROMDrive
	}{
		{"no cd-rom at all", VMConfig{"scsi0": "local-lvm:vm-100-disk-0,size=32G"}, []CDROMDrive{}},
		{
			"two drives come back in key order",
			VMConfig{
				"sata0": "local:iso/b.iso,media=cdrom",
				"ide2":  "local:iso/a.iso,media=cdrom",
				"scsi0": "local-lvm:vm-100-disk-0,size=32G",
			},
			[]CDROMDrive{{Key: "ide2", Volid: "local:iso/a.iso"}, {Key: "sata0", Volid: "local:iso/b.iso"}},
		},
		{"an empty drive reports no volid", VMConfig{"ide2": "none,media=cdrom"}, []CDROMDrive{{Key: "ide2", Volid: ""}}},
		{"a missing volid reports no volid", VMConfig{"ide2": ",media=cdrom"}, []CDROMDrive{{Key: "ide2", Volid: ""}}},
		{"trailing options are not part of the volid", VMConfig{"ide2": "local:iso/a.iso,media=cdrom,size=5G"}, []CDROMDrive{{Key: "ide2", Volid: "local:iso/a.iso"}}},
		{"non-string values are skipped", VMConfig{"ide2": 2, "boot": nil}, []CDROMDrive{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.CDROMDrives()
			if len(got) != len(tt.want) {
				t.Fatalf("CDROMDrives() = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("CDROMDrives()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// CDROMDrives documents a fixed order, which is a claim about every call
// agreeing rather than about one call coming out sorted. Go randomises map
// iteration, so an implementation that dropped the sort would still return the
// drives in key order on a good fraction of single calls.
func TestVMConfigCDROMDrivesIsRepeatable(t *testing.T) {
	config := VMConfig{
		"ide0":  "local:iso/a.iso,media=cdrom",
		"ide1":  "local:iso/b.iso,media=cdrom",
		"ide2":  "local:iso/c.iso,media=cdrom",
		"sata0": "local:iso/d.iso,media=cdrom",
		"sata1": "local:iso/e.iso,media=cdrom",
		"scsi0": "local-lvm:vm-100-disk-0,size=32G",
	}
	first := config.CDROMDrives()
	for i := range 100 {
		got := config.CDROMDrives()
		if len(got) != len(first) {
			t.Fatalf("CDROMDrives() call %d returned %d drives, first call returned %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("CDROMDrives() call %d = %+v, first call = %+v; the order is not stable", i, got, first)
			}
		}
	}
}
