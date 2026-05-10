package netguard

import (
	"net"
	"strings"
	"testing"
)

func TestIsCloudMetadataIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"169.254.169.254", true},
		{"::ffff:169.254.169.254", true},
		{"169.254.169.253", false},
		{"169.254.1.1", false},
		{"127.0.0.1", false},
		{"8.8.8.8", false},
		{"fe80::a9fe:a9fe", false}, // covered by link-local-unicast, not metadata
		{"::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP")
			}
			if got := IsCloudMetadataIP(ip); got != tc.want {
				t.Errorf("IsCloudMetadataIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestIsAlwaysBlocked(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// Always blocked
		{"169.254.169.254", true},   // cloud metadata
		{"0.0.0.0", true},           // unspecified IPv4
		{"::", true},                // unspecified IPv6
		{"224.0.0.1", true},         // multicast IPv4
		{"239.255.255.250", true},   // multicast IPv4 (SSDP)
		{"ff02::1", true},           // multicast IPv6
		{"255.255.255.255", true},   // limited broadcast
		{"240.0.0.1", true},         // class E
		{"255.255.255.0", true},     // class E (just under limited broadcast)
		{"::ffff:169.254.169.254", true},

		// NOT always blocked (private but legitimate Proxmox/SSH targets)
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"172.16.0.1", false},
		{"169.254.1.1", false}, // link-local-unicast (not metadata)
		{"fe80::1", false},     // link-local-unicast IPv6
		{"fc00::1", false},     // ULA
		{"fec0::1", false},     // site-local IPv6 (deprecated)
		{"100.64.0.1", false},  // CGNAT
		{"8.8.8.8", false},     // public
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP")
			}
			if got := IsAlwaysBlocked(ip); got != tc.want {
				t.Errorf("IsAlwaysBlocked(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestDialControlSSRFGuard(t *testing.T) {
	cases := []struct {
		address string
		wantErr bool
	}{
		{"169.254.169.254:80", true},
		{"169.254.169.254:443", true},
		{"[::ffff:169.254.169.254]:80", true},
		{"0.0.0.0:8006", true},
		{"224.0.0.1:9999", true},
		{"255.255.255.255:80", true},
		{"240.0.0.1:80", true},
		{"127.0.0.1:8006", false}, // private allowed at dial layer
		{"10.0.0.1:22", false},
		{"8.8.8.8:443", false},
		{"[2606:4700:4700::1111]:443", false},
		{"hostname-not-ip:443", false}, // SplitHostPort succeeds; ParseIP returns nil; pass-through
		{"malformed", false},           // SplitHostPort fails; pass-through
	}
	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			err := DialControlSSRFGuard("tcp", tc.address, nil)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %s, got nil", tc.address)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %s: %v", tc.address, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "blocked") {
				t.Errorf("error message %q does not mention 'blocked'", err.Error())
			}
		})
	}
}

func TestIsAlwaysBlocked_Nil(t *testing.T) {
	if IsAlwaysBlocked(nil) {
		t.Error("nil IP should not be reported as blocked")
	}
}
