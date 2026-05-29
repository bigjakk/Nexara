package drs

import (
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestDRSBlockedByNativeCRS(t *testing.T) {
	tests := []struct {
		name string
		mode string
		crs  interface{}
		want bool
	}{
		{"automatic + native auto-rebalance", "automatic", "ha=dynamic,ha-auto-rebalance=1", true},
		{"automatic + dynamic but no auto-rebalance", "automatic", "ha=dynamic", false},
		{"automatic + static", "automatic", "ha=static", false},
		{"advisory is allowed even with native rebalance", "advisory", "ha=dynamic,ha-auto-rebalance=1", false},
		{"disabled", "disabled", "ha=dynamic,ha-auto-rebalance=1", false},
		{"automatic + empty crs", "automatic", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &proxmox.ClusterOptions{CRS: tt.crs}
			if got := drsBlockedByNativeCRS(tt.mode, opts); got != tt.want {
				t.Errorf("drsBlockedByNativeCRS(%q, crs=%v) = %v, want %v", tt.mode, tt.crs, got, tt.want)
			}
		})
	}

	if drsBlockedByNativeCRS("automatic", nil) {
		t.Error("drsBlockedByNativeCRS with nil opts should be false")
	}
}
