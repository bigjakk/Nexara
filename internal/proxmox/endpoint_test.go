package proxmox

import "testing"

func TestFailoverBaseURL(t *testing.T) {
	cases := []struct {
		name, primary, addr, want string
	}{
		{"default port from trailing-slash url", "https://10.0.0.1:8006/", "10.0.0.2", "https://10.0.0.2:8006"},
		{"reuses non-default port", "https://host:9001", "1.2.3.4", "https://1.2.3.4:9001"},
		{"defaults when primary unparseable", "://bad", "1.2.3.4", "https://1.2.3.4:8006"},
		{"brackets ipv6 address", "https://[::1]:8006/", "fe80::1", "https://[fe80::1]:8006"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FailoverBaseURL(tc.primary, tc.addr); got != tc.want {
				t.Fatalf("FailoverBaseURL(%q, %q) = %q, want %q", tc.primary, tc.addr, got, tc.want)
			}
		})
	}
}

func TestAPIURLHost(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"ipv4 with port and trailing slash", "https://10.0.0.1:8006/", "10.0.0.1"},
		{"hostname with port", "https://pve1.example.com:8006", "pve1.example.com"},
		{"ipv6 strips brackets", "https://[fe80::1]:8006/", "fe80::1"},
		{"unparseable returns empty", "://bad", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := APIURLHost(tc.in); got != tc.want {
				t.Fatalf("APIURLHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
