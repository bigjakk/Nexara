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
