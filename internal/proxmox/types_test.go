package proxmox

import (
	"encoding/json"
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
			name: "errors sort before warnings, then alphabetical by type",
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
					},
					"AUTH_INSECURE_GLOBAL_ID_RECLAIM": {
						Severity: "HEALTH_WARN",
						Summary:  CephHealthCheckSummary{Message: "client is using insecure global_id reclaim"},
					},
				},
			},
			want: []CephHealthCheckItem{
				{Type: "OSD_DOWN", Severity: "HEALTH_ERR", Message: "1 osds down"},
				{Type: "AUTH_INSECURE_GLOBAL_ID_RECLAIM", Severity: "HEALTH_WARN", Message: "client is using insecure global_id reclaim"},
				{Type: "MON_DISK_LOW", Severity: "HEALTH_WARN", Message: "mon pve1 is low on available space"},
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
			if len(got) != len(tt.want) {
				t.Fatalf("got %d checks, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("check[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCephHealthUnmarshalChecks verifies the checks map is parsed from the
// Proxmox /ceph/status payload shape (object keyed by check id).
func TestCephHealthUnmarshalChecks(t *testing.T) {
	t.Parallel()

	const payload = `{
		"status": "HEALTH_WARN",
		"checks": {
			"MON_DISK_LOW": {
				"severity": "HEALTH_WARN",
				"summary": {"message": "mon pve1 is low on available space", "count": 1}
			}
		}
	}`

	var h CephHealth
	if err := json.Unmarshal([]byte(payload), &h); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if h.Status != "HEALTH_WARN" {
		t.Errorf("status = %q, want HEALTH_WARN", h.Status)
	}
	items := h.NormalizedChecks()
	if len(items) != 1 {
		t.Fatalf("got %d checks, want 1", len(items))
	}
	if items[0].Type != "MON_DISK_LOW" || items[0].Message != "mon pve1 is low on available space" {
		t.Errorf("unexpected check: %+v", items[0])
	}
}
