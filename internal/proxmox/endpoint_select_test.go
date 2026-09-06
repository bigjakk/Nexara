package proxmox

import "testing"

// SelectClusterEndpoint sits under every Proxmox client the app builds, so the
// property that matters most is restraint: it must return the configured
// api_url unchanged unless it has positive evidence that endpoint cannot serve
// requests. A wrong substitution would silently send traffic to a different
// host than the operator configured.
func TestSelectClusterEndpoint(t *testing.T) {
	const (
		pinned = "AA:BB:CC"
		other  = "DD:EE:FF"
		apiURL = "https://10.0.0.1:8006/"
	)

	members := func(primaryStatus, primaryFP string) []NodeEndpoint {
		return []NodeEndpoint{
			{Name: "pve1", Address: "10.0.0.1", SSLFingerprint: primaryFP, Status: primaryStatus},
			{Name: "pve2", Address: "10.0.0.2", SSLFingerprint: "FP2", Status: "online"},
			{Name: "pve3", Address: "10.0.0.3", SSLFingerprint: "FP3", Status: "online"},
		}
	}

	t.Run("healthy primary is used unchanged", func(t *testing.T) {
		got := SelectClusterEndpoint(apiURL, pinned, members("online", pinned))
		if got.BaseURL != apiURL || got.TLSFingerprint != pinned || got.ViaNode != "" {
			t.Fatalf("substituted a healthy endpoint: %+v", got)
		}
	})

	t.Run("a rotated certificate on the primary routes to a member", func(t *testing.T) {
		got := SelectClusterEndpoint(apiURL, pinned, members("online", other))
		if got.ViaNode != "pve2" {
			t.Fatalf("ViaNode = %q, want pve2 (%+v)", got.ViaNode, got)
		}
		// Each member serves its own certificate; reusing the cluster pin
		// would fail the handshake just as surely as the primary did.
		if got.TLSFingerprint != "FP2" {
			t.Errorf("TLSFingerprint = %q, want the member's own FP2", got.TLSFingerprint)
		}
		if got.BaseURL != "https://10.0.0.2:8006" {
			t.Errorf("BaseURL = %q, want the member on the primary's port", got.BaseURL)
		}
	})

	t.Run("an offline primary routes to a member", func(t *testing.T) {
		got := SelectClusterEndpoint(apiURL, pinned, members("offline", pinned))
		if got.ViaNode != "pve2" {
			t.Fatalf("ViaNode = %q, want pve2", got.ViaNode)
		}
	})

	t.Run("offline members are skipped", func(t *testing.T) {
		eps := members("offline", pinned)
		eps[1].Status = "offline"
		got := SelectClusterEndpoint(apiURL, pinned, eps)
		if got.ViaNode != "pve3" {
			t.Fatalf("ViaNode = %q, want pve3", got.ViaNode)
		}
	})

	t.Run("a member with no recorded fingerprint is skipped, not connected unpinned", func(t *testing.T) {
		// Connecting without a pin would silently downgrade to system-CA
		// verification, which for a self-signed Proxmox cert means no
		// verification the operator asked for at all.
		eps := members("offline", pinned)
		eps[1].SSLFingerprint = ""
		got := SelectClusterEndpoint(apiURL, pinned, eps)
		if got.ViaNode != "pve3" {
			t.Fatalf("ViaNode = %q, want pve3", got.ViaNode)
		}
		if got.TLSFingerprint == "" {
			t.Error("selected an endpoint with no pin")
		}
	})

	t.Run("no usable alternate falls back to the configured endpoint", func(t *testing.T) {
		// Returning the primary keeps the caller's error identical to what it
		// would have been without failover, rather than inventing a new one.
		eps := []NodeEndpoint{
			{Name: "pve1", Address: "10.0.0.1", SSLFingerprint: pinned, Status: "offline"},
		}
		got := SelectClusterEndpoint(apiURL, pinned, eps)
		if got.BaseURL != apiURL || got.ViaNode != "" {
			t.Fatalf("expected the configured endpoint, got %+v", got)
		}
	})

	t.Run("a reverse proxy on the node's own address is left alone", func(t *testing.T) {
		// The regression this guards. nodes.ssl_fingerprint describes
		// pveproxy's certificate; a proxy fronting :8006 on the same address
		// serves a different one, so the comparison finds a permanent
		// mismatch. Without the port guard this exiles a perfectly healthy
		// cluster to a member on a port the operator deliberately fronted,
		// forever, with no way to clear it.
		got := SelectClusterEndpoint("https://10.0.0.1/", pinned, members("online", other))
		if got.ViaNode != "" {
			t.Fatalf("redirected a proxy-fronted endpoint: %+v", got)
		}
	})

	t.Run("an explicit non-pveproxy port is left alone", func(t *testing.T) {
		got := SelectClusterEndpoint("https://10.0.0.1:443/", pinned, members("online", other))
		if got.ViaNode != "" {
			t.Fatalf("redirected a non-8006 endpoint: %+v", got)
		}
	})

	t.Run("an alternate of unknown status is not chosen", func(t *testing.T) {
		// Never-synced rows read as usable when deciding to LEAVE the primary
		// alone, but choosing to send traffic somewhere needs a positive
		// signal that it is up.
		eps := members("offline", pinned)
		eps[1].Status = ""
		got := SelectClusterEndpoint(apiURL, pinned, eps)
		if got.ViaNode != "pve3" {
			t.Fatalf("ViaNode = %q, want pve3 — unknown status is not online", got.ViaNode)
		}
	})

	t.Run("an api_url that is not a member is left alone", func(t *testing.T) {
		// A VIP, load balancer or DNS name. We know nothing about the
		// certificate or health of whatever answers there, so substituting
		// would be guessing.
		got := SelectClusterEndpoint("https://pve.example.com:8006/", pinned, members("online", other))
		if got.BaseURL != "https://pve.example.com:8006/" || got.ViaNode != "" {
			t.Fatalf("substituted a non-member endpoint: %+v", got)
		}
	})

	t.Run("no endpoints known yet leaves the configured endpoint alone", func(t *testing.T) {
		got := SelectClusterEndpoint(apiURL, pinned, nil)
		if got.BaseURL != apiURL || got.ViaNode != "" {
			t.Fatalf("substituted with no evidence: %+v", got)
		}
	})

	t.Run("an unpinned cluster is never treated as having a changed certificate", func(t *testing.T) {
		// Empty pin means the operator opted into system-CA verification;
		// that is not a mismatch and must not trigger failover.
		got := SelectClusterEndpoint(apiURL, "", members("online", other))
		if got.ViaNode != "" {
			t.Fatalf("failed over an unpinned cluster: %+v", got)
		}
	})

	t.Run("a member whose certificate we have not learned yet is not a mismatch", func(t *testing.T) {
		got := SelectClusterEndpoint(apiURL, pinned, members("online", ""))
		if got.ViaNode != "" {
			t.Fatalf("failed over on absent evidence: %+v", got)
		}
	})

	t.Run("addresses compare case-insensitively", func(t *testing.T) {
		eps := []NodeEndpoint{
			{Name: "pve1", Address: "PVE-01.example.com", SSLFingerprint: other, Status: "online"},
			{Name: "pve2", Address: "10.0.0.2", SSLFingerprint: "FP2", Status: "online"},
		}
		got := SelectClusterEndpoint("https://pve-01.example.com:8006/", pinned, eps)
		if got.ViaNode != "pve2" {
			t.Fatalf("ViaNode = %q, want pve2 — host match should ignore case", got.ViaNode)
		}
	})

	t.Run("an unparseable api_url is left alone", func(t *testing.T) {
		got := SelectClusterEndpoint("://not a url", pinned, members("offline", pinned))
		if got.ViaNode != "" {
			t.Fatalf("substituted for an unparseable url: %+v", got)
		}
	})
}

func TestEndpointCertificateChanged(t *testing.T) {
	t.Run("colon and case differences are the same certificate", func(t *testing.T) {
		if EndpointCertificateChanged("AA:BB:CC", "aabbcc") {
			t.Error("reported a mismatch for two spellings of one fingerprint")
		}
	})

	t.Run("a different certificate is a mismatch", func(t *testing.T) {
		if !EndpointCertificateChanged("AA:BB:CC", "DD:EE:FF") {
			t.Error("missed a genuinely different certificate")
		}
	})

	t.Run("an unknown value on either side is never a mismatch", func(t *testing.T) {
		if EndpointCertificateChanged("", "AA:BB") || EndpointCertificateChanged("AA:BB", "") {
			t.Error("absence of evidence was treated as evidence of change")
		}
	})
}
