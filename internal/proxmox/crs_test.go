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

func TestCRSPauseRestore(t *testing.T) {
	tests := []struct {
		name         string
		in           interface{}
		wantRestore  string
		wantPaused   string
	}{
		{
			// String shape: original is replayed verbatim for restore; only the
			// ha-auto-rebalance key flips to 0, every other key preserved in order.
			"string preserves other keys",
			"ha=dynamic,ha-auto-rebalance=1,ha-auto-rebalance-threshold=30,ha-auto-rebalance-method=topsis",
			"ha=dynamic,ha-auto-rebalance=1,ha-auto-rebalance-threshold=30,ha-auto-rebalance-method=topsis",
			"ha=dynamic,ha-auto-rebalance=0,ha-auto-rebalance-threshold=30,ha-auto-rebalance-method=topsis",
		},
		{
			// Whitespace around tokens is normalized away on the paused output.
			"spaces normalized",
			" ha=dynamic , ha-auto-rebalance=1 ",
			" ha=dynamic , ha-auto-rebalance=1 ",
			"ha=dynamic,ha-auto-rebalance=0",
		},
		{
			// Key absent → appended (so the balancer is definitively off).
			"key appended when absent",
			"ha=dynamic",
			"ha=dynamic",
			"ha=dynamic,ha-auto-rebalance=0",
		},
		{
			// Object shape has no Raw, so both paths serialize the parsed fields
			// in a fixed key order (map iteration order is irrelevant).
			"object shape serialized",
			map[string]interface{}{"ha": "dynamic", "ha-auto-rebalance": float64(1), "ha-auto-rebalance-threshold": float64(40)},
			"ha=dynamic,ha-auto-rebalance=1,ha-auto-rebalance-threshold=40",
			"ha=dynamic,ha-auto-rebalance=0,ha-auto-rebalance-threshold=40",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ParseCRSSettings(tt.in)
			if got := s.Restorable(); got != tt.wantRestore {
				t.Errorf("Restorable() = %q; want %q", got, tt.wantRestore)
			}
			if got := s.PausedAutoRebalance(); got != tt.wantPaused {
				t.Errorf("PausedAutoRebalance() = %q; want %q", got, tt.wantPaused)
			}
			// The paused string must parse back as inactive.
			if ParseCRSSettings(s.PausedAutoRebalance()).AutoRebalanceActive() {
				t.Errorf("paused string still reports auto-rebalance active: %q", s.PausedAutoRebalance())
			}
		})
	}
}
