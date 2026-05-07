// Package netguard provides defense-in-depth SSRF guards for outbound
// network code (Proxmox API client, SSH dial, fingerprint fetch).
//
// The host-policy gate in internal/api/handlers/url_policy.go runs at
// validation time, but the actual TCP connection happens later — and DNS
// is re-resolved at that point. A hostile authoritative DNS server can
// answer with a public IP during validation and a private/metadata IP at
// connection time (the "DNS rebinding" attack). DialControlSSRFGuard is a
// net.Dialer.Control callback that re-checks the post-resolution IP before
// any packet is sent, closing the rebinding window.
//
// The dial-time guard only enforces the *always-blocked* IP classes
// (cloud metadata, broadcast, Class E, multicast, unspecified). Private and
// loopback addresses are intentionally allowed because they are legitimate
// Proxmox / SSH targets in homelab deployments.
package netguard

import (
	"fmt"
	"net"
	"syscall"
)

// cloudMetadataIPv4 is the universally-blocked IMDS address used by
// AWS, GCP, Azure, Oracle Cloud, and most Hetzner/DO setups.
var cloudMetadataIPv4 = net.IPv4(169, 254, 169, 254)

// IsCloudMetadataIP reports whether ip is the IPv4 metadata address
// 169.254.169.254. The IPv6 fe80::a9fe:a9fe variant is covered by the
// link-local-unicast check (which is in the warn-and-confirm class).
func IsCloudMetadataIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.Equal(cloudMetadataIPv4)
	}
	return false
}

// IsAlwaysBlocked reports whether ip belongs to a class that is never
// allowed as an outbound target: cloud metadata, IPv4 limited broadcast,
// IPv4 Class E (240.0.0.0/4), multicast, or unspecified. These have no
// legitimate Proxmox/SSH use case and are blocked even when the user
// explicitly opts into private addresses.
func IsAlwaysBlocked(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if IsCloudMetadataIP(ip) {
		return true
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Limited broadcast 255.255.255.255.
		if ip4[0] == 255 && ip4[1] == 255 && ip4[2] == 255 && ip4[3] == 255 {
			return true
		}
		// Class E reserved: 240.0.0.0/4.
		if ip4[0]&0xf0 == 0xf0 {
			return true
		}
	}
	return false
}

// DialControlSSRFGuard is a net.Dialer.Control callback that fails the dial
// before any TCP packet is sent if the post-resolution IP belongs to an
// always-blocked class. Use it on every outbound dialer that operates on a
// user-influenced host name to defeat DNS rebinding.
func DialControlSSRFGuard(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Address came in without a port — let the dialer surface the error.
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if IsAlwaysBlocked(ip) {
		return fmt.Errorf("blocked SSRF target: %s", ip)
	}
	return nil
}
