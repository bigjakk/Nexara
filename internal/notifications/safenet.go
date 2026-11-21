package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/redact"
)

// TargetAddressError refuses a notification target by the address its name
// resolves to. Overridable is true for a private address (RFC 1918, IPv6
// unique-local, carrier-grade NAT), which a channel the operator confirmed may
// use; false for a loopback, link-local, unspecified or cloud metadata one,
// which no channel may.
//
// Unreadable marks an address that does not parse as one IP — an IPv6 zone
// ("::1%1") is the case that matters — refused like a loopback one.
//
// Metadata marks a cloud metadata service's address that sits in a range a
// confirmation relaxes (cloudMetadata), so the refusal names it as one.
type TargetAddressError struct {
	IP          string
	Overridable bool
	Unreadable  bool
	Metadata    bool
}

// Error names the class of address and not the address itself, which a
// dead-letter row, readable by every Viewer, would otherwise keep.
func (e *TargetAddressError) Error() string {
	if e.Overridable {
		return "a private address, which this channel is not confirmed for"
	}
	if e.Unreadable {
		return "an address with a zone, or one that cannot be read, which no channel may use"
	}
	if e.Metadata {
		return "a cloud metadata address, which no channel may use"
	}
	return "a loopback, link-local or unspecified address, which no channel may use"
}

// Allowance is what a target may resolve to beyond the public internet.
// Private is the operator's confirmation (allow_private_address): any private
// address, carrier-grade NAT included. CGNAT is carrier-grade NAT space alone,
// which a channel or OIDC configuration made before v1.18.0 keeps without a
// confirmation (cgnat_grandfathered, migration 000157) until its target
// changes: v1.17.2 took that range for public and refused every other private one.
type Allowance struct {
	Private bool
	CGNAT   bool
}

// checkTargetIP is the rule every notification send applies to an address:
// loopback, link-local (the cloud metadata addresses included) and unspecified
// are refused always; a private address unless allow.Private, the channel's
// confirmation (ChannelContext), or, in carrier-grade NAT space, allow.CGNAT.
func checkTargetIP(ip net.IP, allow Allowance) error {
	switch {
	case cloudMetadata(ip):
		return &TargetAddressError{IP: ip.String(), Metadata: true}
	case ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified():
		return &TargetAddressError{IP: ip.String()}
	case allow.Private || (allow.CGNAT && cgnat.Contains(ip)):
		return nil
	case ip.IsPrivate() || cgnat.Contains(ip):
		return &TargetAddressError{IP: ip.String(), Overridable: true}
	}
	return nil
}

// cgnat is RFC 6598 carrier-grade NAT space, 100.64.0.0/10: not public, so it
// needs a channel's confirmation as a private address does (or Allowance.CGNAT).
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// cloudMetadata reports the cloud metadata services' addresses that sit in a
// range a confirmation relaxes: AWS's and GCP's IPv6 ones (unique-local) and
// Alibaba Cloud's 100.100.100.200 (carrier-grade NAT).
func cloudMetadata(ip net.IP) bool {
	return ip.Equal(net.ParseIP("fd00:ec2::254")) || ip.Equal(net.ParseIP("fd20:ce::254")) ||
		ip.Equal(net.ParseIP("100.100.100.200"))
}

type allowanceKey struct{}

// ChannelContext is ctx for a send through ch: it carries ch's Allowance (its
// confirmation of a private target, and carrier-grade NAT for a channel made
// before v1.18.0), which every address check of the send reads. A send whose
// context did not come through here refuses private addresses.
func ChannelContext(ctx context.Context, ch db.NotificationChannel) context.Context {
	return context.WithValue(ctx, allowanceKey{}, Allowance{Private: ch.AllowPrivateAddress, CGNAT: ch.CgnatGrandfathered})
}

func allowanceFrom(ctx context.Context) Allowance {
	allow, _ := ctx.Value(allowanceKey{}).(Allowance)
	return allow
}

// validateExternalURL checks that a URL is http(s) and does not resolve to an
// address checkTargetIP refuses, with the Allowance ctx carries.
func validateExternalURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", targetErr(err))
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must include a hostname")
	}
	ips, err := resolveTarget(ctx, host)
	var refused *TargetAddressError
	if errors.As(err, &refused) {
		return fmt.Errorf("URL resolves to %w", err)
	}
	if err != nil {
		return err
	}
	allow := allowanceFrom(ctx)
	for _, ip := range ips {
		if err := checkTargetIP(ip, allow); err != nil {
			return fmt.Errorf("URL resolves to %w", err)
		}
	}
	return nil
}

// unreadable refuses an address that does not parse as one IP. It fails
// closed: an address skipped because it could not be read is one the resolver
// and the dialer may still accept — an IPv6 zone ("::1%1") is.
func unreadable(s string) error {
	return &TargetAddressError{IP: s, Unreadable: true}
}

// targetIP reads one address as the IP it reaches: an IPv6 zone dropped and
// an IPv4-mapped address unmapped, so ::1%1 is ::1 and ::ffff:127.0.0.1 is
// 127.0.0.1. Anything that is not an address is refused (unreadable).
func targetIP(s string) (net.IP, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil, unreadable(s)
	}
	return net.IP(addr.WithZone("").Unmap().AsSlice()), nil
}

// resolveTarget is the addresses host names, or host itself as an IP literal.
// A host with a zone is refused, and so is a name any of whose addresses
// cannot be read.
func resolveTarget(ctx context.Context, host string) ([]net.IP, error) {
	if strings.Contains(host, "%") {
		return nil, unreadable(host)
	}
	if ip, err := targetIP(host); err == nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve hostname: %w", err)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ip, err := targetIP(a)
		if err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

// SafeHTTPClient returns an HTTP client that blocks connections to private IPs
// and does not follow redirects. Exported for reuse in other packages (e.g. OIDC).
func SafeHTTPClient(timeout time.Duration) *http.Client {
	return safeHTTPClient(timeout, Allowance{})
}

// SafeHTTPClientAllowing is SafeHTTPClient with the addresses allow admits
// (checkTargetIP) for the host it is for: an OIDC issuer the operator confirmed
// on the LAN, or one in carrier-grade NAT space from before v1.18.0. Loopback,
// link-local, unspecified and zoned addresses stay refused at every dial.
func SafeHTTPClientAllowing(timeout time.Duration, allow Allowance) *http.Client {
	return safeHTTPClient(timeout, allow)
}

// targetHTTPClient is SafeHTTPClient for a send through a channel, with the
// Allowance ctx carries (ChannelContext).
func targetHTTPClient(ctx context.Context, timeout time.Duration) *http.Client {
	return safeHTTPClient(timeout, allowanceFrom(ctx))
}

func safeHTTPClient(timeout time.Duration, allow Allowance) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: dialControl(allow),
			}).DialContext,
		},
	}
}

// dialControl refuses, at connect time, an address checkTargetIP refuses: the
// address actually dialled, whatever the name resolved to when it was checked.
func dialControl(allow Allowance) func(string, string, syscall.RawConn) error {
	return func(_ string, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("connection blocked: %w", unreadable(address))
		}
		ip, err := targetIP(host)
		if err != nil {
			return fmt.Errorf("connection blocked: %w", err)
		}
		if err := checkTargetIP(ip, allow); err != nil {
			return fmt.Errorf("connection blocked: the address is %w", err)
		}
		return nil
	}
}

// targetErr is err with the URL a *url.Error in it carries kept as
// redact.TargetURL, its scheme and host alone. A channel's URL is its secret —
// a chat webhook's path, a bot's token — and a failed send's error goes where
// the channel's configuration never does: the dead-letter row's last_error,
// which every Viewer can read, the channel test's reply and the logs.
func targetErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = redact.TargetURL(ue.URL)
	}
	return err
}

// sanitizeHeader removes CRLF characters from an SMTP header value.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// CheckChannelTarget checks, before a channel is stored, the address its
// settings send to: a webhook's URL, a chat webhook's, an SMTP host reached
// with STARTTLS. private reports that it resolves to a private address. err
// is a *TargetAddressError when it resolves to one no channel may use, or to
// a private one and allowPrivate, the operator's confirmation, is false.
//
// A name that does not resolve, and a channel type whose target is a fixed
// public service (Telegram, PagerDuty), pass: every send checks its target
// again. So does cleartext SMTP, which the send holds to loopback.
func CheckChannelTarget(ctx context.Context, channelType string, config json.RawMessage, allowPrivate bool) (private bool, err error) {
	host := channelTargetHost(channelType, config)
	if host == "" {
		return false, nil
	}
	private, err = CheckTargetHost(ctx, host, Allowance{Private: allowPrivate})
	var refused *TargetAddressError
	if err != nil && !errors.As(err, &refused) {
		return false, nil // unresolvable passes; the send checks again
	}
	return private, err
}

// CheckTargetHost is CheckChannelTarget's check for a host named anywhere
// else, an OIDC issuer's, with the addresses allow admits: private reports that
// host resolves to a private address (carrier-grade NAT included); err is a
// *TargetAddressError for an address refused (as CheckChannelTarget's), or the
// resolver's error for a name that does not resolve, which the caller decides
// about.
func CheckTargetHost(ctx context.Context, host string, allow Allowance) (private bool, err error) {
	ips, err := resolveTarget(ctx, host)
	if err != nil {
		return false, err
	}
	for _, ip := range ips {
		var refused *TargetAddressError
		if !errors.As(checkTargetIP(ip, Allowance{}), &refused) {
			continue
		}
		if errors.As(checkTargetIP(ip, allow), &refused) {
			return refused.Overridable, refused
		}
		private = true
	}
	return private, nil
}

// channelTargetHost is the host a channel's settings send to, or "" for none
// the channel's owner chooses.
func channelTargetHost(channelType string, config json.RawMessage) string {
	var cfg struct {
		URL        string `json:"url"`
		WebhookURL string `json:"webhook_url"`
		Host       string `json:"host"`
		TLS        *bool  `json:"tls"`
	}
	if json.Unmarshal(config, &cfg) != nil {
		return ""
	}
	raw := ""
	switch channelType {
	case "webhook":
		raw = cfg.URL
	case "slack", "discord", "teams":
		raw = cfg.WebhookURL
	case "email":
		if cfg.TLS != nil && !*cfg.TLS {
			return ""
		}
		return strings.TrimSpace(cfg.Host)
	default:
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
