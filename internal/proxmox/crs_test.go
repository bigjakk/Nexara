package proxmox

import "testing"

func TestParseCRSSettings(t *testing.T) {
	tests := []struct {
		name              string
		in                interface{}
		wantHA            string
		wantAutoRebalance bool
		wantThreshold     int
		wantMethod        string
		wantActive        bool
	}{
		{"nil", nil, "", false, 0, "", false},
		{"empty string", "", "", false, 0, "", false},
		{"static no rebalance", "ha=static", "static", false, 0, "", false},
		{
			"dynamic with rebalance",
			"ha=dynamic,ha-auto-rebalance=1,ha-auto-rebalance-threshold=30,ha-auto-rebalance-method=topsis",
			"dynamic", true, 30, "topsis", true,
		},
		{"rebalance on start only", "ha=basic,ha-rebalance-on-start=1", "basic", false, 0, "", false},
		{"spaces tolerated", " ha=dynamic , ha-auto-rebalance=1 ", "dynamic", true, 0, "", true},
		{
			"map shape numeric",
			map[string]interface{}{"ha": "dynamic", "ha-auto-rebalance": float64(1), "ha-auto-rebalance-threshold": float64(40)},
			"dynamic", true, 40, "", true,
		},
		{
			"map shape string",
			map[string]interface{}{"ha": "dynamic", "ha-auto-rebalance": "1"},
			"dynamic", true, 0, "", true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ParseCRSSettings(tt.in)
			if s.HA != tt.wantHA || s.AutoRebalance != tt.wantAutoRebalance ||
				s.Threshold != tt.wantThreshold || s.Method != tt.wantMethod ||
				s.AutoRebalanceActive() != tt.wantActive {
				t.Errorf("ParseCRSSettings(%v) = %+v (active=%v); want HA=%q auto=%v thr=%d method=%q active=%v",
					tt.in, s, s.AutoRebalanceActive(),
					tt.wantHA, tt.wantAutoRebalance, tt.wantThreshold, tt.wantMethod, tt.wantActive)
			}
		})
	}
}
