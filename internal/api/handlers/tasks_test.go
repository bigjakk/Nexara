package handlers

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseVmidsParam(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []int32
		wantErr bool
	}{
		{name: "single", raw: "105", want: []int32{105}},
		{name: "list", raw: "100,101,205", want: []int32{100, 101, 205}},
		{name: "spaces tolerated", raw: " 100 , 101 ", want: []int32{100, 101}},
		{name: "empty element", raw: "100,,101", wantErr: true},
		{name: "non-numeric", raw: "100,abc", wantErr: true},
		{name: "negative", raw: "-5", wantErr: true},
		{name: "overflow", raw: "99999999999", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVmidsParam(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVmidsParam(%q) = %v, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVmidsParam(%q) unexpected error: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseVmidsParam(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseVmidsParam(%q)[%d] = %d, want %d", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseVmidsParamCap(t *testing.T) {
	parts := make([]string, maxVmidsFilter+1)
	for i := range parts {
		parts[i] = strconv.Itoa(100 + i)
	}
	if _, err := parseVmidsParam(strings.Join(parts, ",")); err == nil {
		t.Fatalf("expected error for %d vmids (cap %d)", len(parts), maxVmidsFilter)
	}

	if _, err := parseVmidsParam(strings.Join(parts[:maxVmidsFilter], ",")); err != nil {
		t.Fatalf("unexpected error at the cap: %v", err)
	}
}
