package veeam

import (
	"os"
	"path/filepath"
	"testing"
)

// The real bootstrap, byte for byte off the lab server. This is the parse that
// replaces the descending token probe, so it has to survive Swagger UI's
// actual formatting rather than a hand-written approximation of it.
func TestParseRevisions_LiveSwaggerIndex(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "swagger_index.js"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	got := parseRevisions(string(body))
	want := []string{
		"1.3-rev2", "1.3-rev1", "1.3-rev0",
		"1.2-rev1", "1.2-rev0",
		"1.1-rev2", "1.1-rev1", "1.1-rev0",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d revisions %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("revision[%d] = %q, want %q (order must be newest-first)", i, got[i], want[i])
		}
	}

	if rev := pickRevision(got); rev != DefaultRevision {
		t.Errorf("pickRevision = %q, want %q", rev, DefaultRevision)
	}
}

func TestParseRevisions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "empty body",
			body: "",
			want: nil,
		},
		{
			name: "no revisions present",
			body: `window.onload = function () { var x = 1; }`,
			want: nil,
		},
		{
			// The name field is uppercased ("V1.3-REV2") while the header
			// takes the bare lowercase form. Parsing anchors on the URL, so
			// the result needs no case repair beyond lowering.
			name: "uppercase URL still lowercases",
			body: `{"url":"/swagger/V1.3-REV2/swagger.json","name":"V1.3-REV2"}`,
			want: []string{"1.3-rev2"},
		},
		{
			name: "duplicates collapse",
			body: `"/swagger/v1.3-rev0/swagger.json" "/swagger/v1.3-rev0/swagger.json"`,
			want: []string{"1.3-rev0"},
		},
		{
			// Lexical ordering would rank rev10 below rev9. Numeric does not.
			name: "double-digit revision sorts above single-digit",
			body: `"/swagger/v1.3-rev9/swagger.json" "/swagger/v1.3-rev10/swagger.json"`,
			want: []string{"1.3-rev10", "1.3-rev9"},
		},
		{
			name: "minor version ordering",
			body: `"/swagger/v1.2-rev5/swagger.json" "/swagger/v1.10-rev0/swagger.json" "/swagger/v2.0-rev0/swagger.json"`,
			want: []string{"2.0-rev0", "1.10-rev0", "1.2-rev5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRevisions(tc.body)
			if len(got) != len(tc.want) {
				t.Fatalf("parseRevisions = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("revision[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPickRevision(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		want      string
	}{
		{"exact match", []string{"1.3-rev2", "1.3-rev1"}, "1.3-rev2"},
		{"older only", []string{"1.2-rev1", "1.1-rev0"}, "1.2-rev1"},
		{"unordered input", []string{"1.1-rev0", "1.3-rev1", "1.2-rev0"}, "1.3-rev1"},
		{"nothing supported", nil, ""},
		// A server newer than this client is not usable at its own revision:
		// taking it would opt into schema changes nothing was tested against.
		{"only newer than we know", []string{"1.4-rev0", "2.0-rev1"}, ""},
		{"newer alongside known", []string{"2.0-rev0", "1.3-rev1"}, "1.3-rev1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickRevision(tc.supported); got != tc.want {
				t.Errorf("pickRevision(%v) = %q, want %q", tc.supported, got, tc.want)
			}
		})
	}
}

func TestBuildVersionSupported(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"13.1.0.411", true},
		{"13.1", true},
		{"13.2.0.1", true},
		{"14.0.0.0", true},
		// 13.0.x reports Proxmox jobs as type "Unknown" with no lastRun.
		{"13.0.1.204", false},
		{"12.3.0.310", false},
		// Fail closed on anything we cannot read: this gates a feature that
		// silently misreports on older builds.
		{"", false},
		{"13", false},
		{"thirteen.one", false},
		{"13.x", false},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			if got := buildVersionSupported(tc.version); got != tc.want {
				t.Errorf("buildVersionSupported(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestProxmoxClusters(t *testing.T) {
	lic := &License{}
	lic.Summary.Workload = []LicenseWorkload{
		{PlatformType: "LinuxServer", HostName: "Linux"},
		{PlatformType: "Proxmox", HostName: "cluster02"},
		{PlatformType: "Proxmox", HostName: "cluster01"},
		{PlatformType: "Proxmox", HostName: "cluster01"},
		{PlatformType: "UnstructuredData", HostName: "192.0.2.10"},
	}

	got := lic.ProxmoxClusters()
	want := []ProxmoxCluster{{Name: "cluster01", VMCount: 2}, {Name: "cluster02", VMCount: 1}}
	if len(got) != len(want) {
		t.Fatalf("ProxmoxClusters = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cluster[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if clusters := (*License)(nil).ProxmoxClusters(); clusters != nil {
		t.Errorf("nil License returned %+v, want nil", clusters)
	}
}
