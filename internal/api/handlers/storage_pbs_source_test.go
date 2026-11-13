package handlers

import (
	"net/url"
	"strings"
	"testing"
)

// Where a pbs storage made from a registered PBS server connects: the shape
// Proxmox stores, which is not always the shape Nexara keeps (a bare
// fingerprint, a bracketed IPv6 host, the default port written out).
func TestPBSConnectionSettings(t *testing.T) {
	t.Parallel()
	colons := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	tests := []struct {
		name        string
		apiURL      string
		fingerprint string
		want        url.Values // nil when refused
	}{
		{"the default port is left to Proxmox", "https://pbs.example.com:8007", colons,
			url.Values{"server": {"pbs.example.com"}, "fingerprint": {colons}}},
		{"no port reads as the default", "https://pbs.example.com", "",
			url.Values{"server": {"pbs.example.com"}}},
		{"another port is written out", "https://pbs.example.com:8443", "",
			url.Values{"server": {"pbs.example.com"}, "port": {"8443"}}},
		{"an IPv6 host goes in bare", "https://[2001:db8::1]:8007", "",
			url.Values{"server": {"2001:db8::1"}}},
		{"a bare lowercase digest becomes Proxmox's pairs", "https://pbs.example.com:8007", strings.Repeat("ab", 32),
			url.Values{"server": {"pbs.example.com"}, "fingerprint": {colons}}},
		{"a pin that is no SHA-256 digest is refused", "https://pbs.example.com:8007", "AB:CD", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pbsConnectionSettings(tt.apiURL, tt.fingerprint)
			if tt.want == nil {
				if err == nil {
					t.Fatalf("got %v, want a refusal", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got.Encode() != tt.want.Encode() {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
