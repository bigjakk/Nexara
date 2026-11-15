package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/redact"
)

// TargetAddressError refuses a notification target by the address its name
// resolves to. Overridable is true for a private address (RFC 1918, IPv6
// unique-local), which a channel the operator confirmed may use; false for a
// loopback, link-local or unspecified one, which no channel may.
type TargetAddressError struct {
	IP          string
	Overridable bool
}

// Error names the class of address and not the address itself, which a
// dead-letter row, readable by every Viewer, would otherwise keep.
func (e *TargetAddressError) Error() string {
	if e.Overridable {
		return "a private address, which this channel is not confirmed for"
	}
	return "a loopback, link-local or unspecified address, which no channel may use"
}

// checkTargetIP is the rule every notification send applies to an address:
// loopback, link-local (the cloud metadata address included) and unspecified
// are refused always; a private address only unless allowPrivate, the
// channel's confirmation (ChannelContext).
func checkTargetIP(ip net.IP, allowPrivate bool) error {
	switch {
	case ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified():
		return &TargetAddressError{IP: ip.String()}
	case ip.IsPrivate() && !allowPrivate:
		return &TargetAddressError{IP: ip.String(), Overridable: true}
	}
	return nil
}

type allowPrivateKey struct{}

// ChannelContext is ctx for a send through ch: it carries whether the
// operator confirmed ch's private target (allow_private_address), which every
// address check of the send reads. A send whose context did not come through
// here refuses private addresses.
func ChannelContext(ctx context.Context, ch db.NotificationChannel) context.Context {
	return context.WithValue(ctx, allowPrivateKey{}, ch.AllowPrivateAddress)
}

func privateAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(allowPrivateKey{}).(bool)
	return allowed
}

// validateExternalURL checks that a URL is http(s) and does not resolve to an
// address checkTargetIP refuses, with the private allowance ctx carries.
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
	if err != nil {
		return err
	}
	allow := privateAllowed(ctx)
	for _, ip := range ips {
		if err := checkTargetIP(ip, allow); err != nil {
			return fmt.Errorf("URL resolves to %w", err)
		}
	}
	return nil
}

// resolveTarget is the addresses host names, or host itself as an IP literal.
func resolveTarget(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve hostname: %w", err)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

// SafeHTTPClient returns an HTTP client that blocks connections to private IPs
// and does not follow redirects. Exported for reuse in other packages (e.g. OIDC).
func SafeHTTPClient(timeout time.Duration) *http.Client {
	return safeHTTPClient(timeout, false)
}

// targetHTTPClient is SafeHTTPClient for a send through a channel, with the
// private allowance ctx carries (ChannelContext).
func targetHTTPClient(ctx context.Context, timeout time.Duration) *http.Client {
	return safeHTTPClient(timeout, privateAllowed(ctx))
}

func safeHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: dialControl(allowPrivate),
			}).DialContext,
		},
	}
}

// dialControl refuses, at connect time, an address checkTargetIP refuses: the
// address actually dialled, whatever the name resolved to when it was checked.
func dialControl(allowPrivate bool) func(string, string, syscall.RawConn) error {
	return func(_ string, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		if ip := net.ParseIP(host); ip != nil {
			if err := checkTargetIP(ip, allowPrivate); err != nil {
				return fmt.Errorf("connection blocked: the address is %w", err)
			}
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
	ips, err := resolveTarget(ctx, host)
	if err != nil {
		return false, nil //nolint:nilerr // unresolvable passes; the send checks again
	}
	for _, ip := range ips {
		var refused *TargetAddressError
		if !errors.As(checkTargetIP(ip, false), &refused) {
			continue
		}
		if !refused.Overridable || !allowPrivate {
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
