package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/bigjakk/nexara/internal/netguard"
)

// addressPolicyError is returned when the resolved host violates the SSRF
// policy. HardReject means there is no legitimate use case (cloud metadata,
// multicast, broadcast, Class E, unspecified) — the caller cannot override
// it. Otherwise the caller may re-submit with the allow_private flag set.
type addressPolicyError struct {
	HardReject bool
	IP         string
	Reason     string
}

func (e *addressPolicyError) Error() string { return e.Reason }

// validateURLFormat performs the cheap syntax checks that don't need DNS:
// the URL must parse, use https://, expose a host, and not embed credentials.
// The returned error is a fiber.Error suitable for the handler to return
// directly.
func validateURLFormat(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid URL format")
	}
	if u.Scheme == "" || u.Host == "" {
		return fiber.NewError(fiber.StatusBadRequest, "URL must include scheme and host")
	}
	if u.Scheme != "https" {
		return fiber.NewError(fiber.StatusBadRequest, "URL must use HTTPS scheme")
	}
	if u.User != nil {
		return fiber.NewError(fiber.StatusBadRequest, "URL must not contain credentials")
	}
	return nil
}

// enforceURLAddressPolicy applies the SSRF policy to the host portion of raw.
// It assumes validateURLFormat has already passed.
//
//   - Cloud metadata, multicast, broadcast, Class E, unspecified: always rejected.
//   - Private/loopback/link-local/IPv6 ULA/IPv6 site-local/CGNAT: rejected
//     unless allowPrivate=true.
//   - Public addresses: allowed.
//
// DNS resolution failures are NOT treated as policy violations. The dial-time
// guard in package netguard catches any post-validation rebinding to an
// always-blocked class, so a soft-pass on resolver error here is safe.
func enforceURLAddressPolicy(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return enforceHostAddressPolicy(ctx, u.Hostname(), allowPrivate)
}

// enforceHostAddressPolicy is the host-only entry point. Use this when the
// caller already has a hostname or IP literal (e.g. from a node address) and
// doesn't need URL parsing.
func enforceHostAddressPolicy(ctx context.Context, host string, allowPrivate bool) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	// Accept "host:port" or "[ipv6]:port" forms. SplitHostPort fails for
	// bare hosts; fall back to manual bracket-stripping in that case.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}

	ips, resolveErr := resolveHostIPs(ctx, host)
	if resolveErr != nil {
		return nil
	}
	for _, ip := range ips {
		if err := classifyIP(ip, allowPrivate); err != nil {
			return err
		}
	}
	return nil
}

// classifyIP returns nil if the IP is allowed under the current policy, or
// an *addressPolicyError describing why it isn't.
func classifyIP(ip net.IP, allowPrivate bool) error {
	if ip == nil {
		return nil
	}
	if netguard.IsAlwaysBlocked(ip) {
		reason := fmt.Sprintf("Address resolves to a blocked IP class (%s). Cloud metadata, multicast, broadcast, Class E, and unspecified addresses are never allowed.", ip.String())
		if netguard.IsCloudMetadataIP(ip) {
			reason = "Address resolves to the cloud instance metadata service IP (169.254.169.254). Connections to this address are never allowed."
		}
		return &addressPolicyError{
			HardReject: true,
			IP:         ip.String(),
			Reason:     reason,
		}
	}
	if isPrivateOrReservedIP(ip) && !allowPrivate {
		return &addressPolicyError{
			HardReject: false,
			IP:         ip.String(),
			Reason:     fmt.Sprintf("Address resolves to a private/loopback IP (%s). Self-hosted lab setups can re-submit with allow_private=true to confirm.", ip.String()),
		}
	}
	return nil
}

// resolveHostIPs returns the IP literal(s) that host points to. If host is
// already an IP literal, it short-circuits without touching the resolver.
func resolveHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	if literal := net.ParseIP(host); literal != nil {
		return []net.IP{literal}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// cgnatNet is RFC 6598 Carrier-Grade NAT space (100.64.0.0/10) — not
// private under Go's IsPrivate(), but routes to ISP-internal infrastructure
// and shouldn't be reachable from a public-facing service without the user
// explicitly opting in.
var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// ipv6SiteLocal is RFC 3879 deprecated site-local space (fec0::/10). Some
// legacy IPv6 networks still use it; treat it the same as IPv6 ULA.
var ipv6SiteLocal = &net.IPNet{
	IP:   net.IP{0xfe, 0xc0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	Mask: net.CIDRMask(10, 128),
}

// isPrivateOrReservedIP returns true for any address that should not be
// reachable from a typical public-facing service: loopback, RFC1918,
// IPv6 ULA (fc00::/7), link-local unicast (169.254/16, fe80::/10),
// link-local multicast, RFC 6598 CGNAT (100.64/10), or RFC 3879 IPv6
// site-local (fec0::/10).
func isPrivateOrReservedIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil && cgnatNet.Contains(ip4) {
		return true
	}
	if ipv6SiteLocal.Contains(ip) {
		return true
	}
	return false
}

// renderAddressPolicyError converts an *addressPolicyError into a JSON
// response. Hard rejections are 400; confirm-required cases are 422 with a
// stable error code so the frontend can show a "this is a private address —
// proceed?" dialog.
func renderAddressPolicyError(c *fiber.Ctx, err error) error {
	var pErr *addressPolicyError
	if !errors.As(err, &pErr) {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if pErr.HardReject {
		return fiber.NewError(fiber.StatusBadRequest, pErr.Reason)
	}
	return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
		"error":   "private_address_confirm_required",
		"message": pErr.Reason,
		"details": fiber.Map{
			"ip":           pErr.IP,
			"address_kind": "private",
		},
	})
}
