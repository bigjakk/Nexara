package handlers

import "testing"

// endpointFingerprintStale decides whether the "TLS certificate changed" health
// issue fires. False positives matter more than false negatives here: an alarm
// the operator cannot clear is worse than no alarm, and re-pinning the
// fingerprint a wrong alarm reports would pin a certificate the endpoint never
// presents. So most of these cases are about staying quiet.
func TestEndpointFingerprintStale(t *testing.T) {
	const (
		pinned = "C9:F2:4E:42:5D:FA:D9:B0:AF:14:53:41:5F:67:EC:6C"
		other  = "0C:6A:3D:A6:D6:E5:38:EA:F7:C8:88:48:4A:E0:E4:27"
	)

	tests := []struct {
		name                    string
		apiURL, pinned          string
		nodeAddress, nodeFinger string
		want                    bool
	}{
		{
			name:   "certificate changed on the configured endpoint",
			apiURL: "https://192.0.2.10:8006/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: true,
		},
		{
			// Both columns hold Proxmox's "AA:BB:…" form in practice, so this
			// guards a hand-entered pin rather than a routine difference.
			name:   "colons and case are not a mismatch",
			apiURL: "https://192.0.2.10:8006/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: "c9f24e425dfad9b0af1453415f67ec6c",
			want: false,
		},
		{
			name:   "unchanged certificate",
			apiURL: "https://192.0.2.10:8006/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: pinned,
			want: false,
		},
		{
			// A different member's certificate says nothing about the endpoint.
			name:   "another member of the same cluster is ignored",
			apiURL: "https://192.0.2.10:8006/", pinned: pinned,
			nodeAddress: "192.0.2.11", nodeFinger: other,
			want: false,
		},
		{
			// A VIP or DNS name matches no member address, so stay silent
			// rather than compare against a certificate it never serves.
			name:   "endpoint is a name, not a member address",
			apiURL: "https://pve.example.com:8006/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: false,
		},
		{
			name:   "unpinned cluster is not stale",
			apiURL: "https://192.0.2.10:8006/", pinned: "",
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: false,
		},
		{
			// Nothing learned yet about this node — absence of evidence is not
			// evidence of a changed certificate.
			name:   "node fingerprint not yet collected",
			apiURL: "https://192.0.2.10:8006/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: "",
			want: false,
		},
		{
			name:   "unparseable api_url reports nothing",
			apiURL: "://not a url", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: false,
		},
		{
			// The regression this guards: a reverse proxy terminating TLS on
			// the node's own address. The host matches a member, so only the
			// port distinguishes it from pveproxy — and nodes.ssl_fingerprint
			// describes pveproxy's certificate, not the proxy's. Reporting
			// here would nag forever and could never be cleared.
			name:   "reverse proxy on the node's own address is not pveproxy",
			apiURL: "https://192.0.2.10/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: false,
		},
		{
			name:   "explicit 443 on a member address is still not pveproxy",
			apiURL: "https://192.0.2.10:443/", pinned: pinned,
			nodeAddress: "192.0.2.10", nodeFinger: other,
			want: false,
		},
		{
			// Relies on url.Hostname() stripping both the brackets and the
			// port; a switch to u.Host would silently stop matching.
			name:   "IPv6 endpoint matches its member address",
			apiURL: "https://[2001:db8::1]:8006/", pinned: pinned,
			nodeAddress: "2001:db8::1", nodeFinger: other,
			want: true,
		},
		{
			name:   "IPv6 endpoint with an unchanged certificate",
			apiURL: "https://[2001:db8::1]:8006/", pinned: pinned,
			nodeAddress: "2001:db8::1", nodeFinger: pinned,
			want: false,
		},
		{
			// Addresses are compared case-insensitively; a hostname stored
			// with different casing is the same endpoint.
			name:   "address comparison ignores case",
			apiURL: "https://PVE-01.example.com:8006/", pinned: pinned,
			nodeAddress: "pve-01.example.com", nodeFinger: other,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := endpointFingerprintStale(tt.apiURL, tt.pinned, tt.nodeAddress, tt.nodeFinger)
			if got != tt.want {
				t.Errorf("endpointFingerprintStale(%q, pinned, %q, finger) = %v, want %v",
					tt.apiURL, tt.nodeAddress, got, tt.want)
			}
		})
	}
}
