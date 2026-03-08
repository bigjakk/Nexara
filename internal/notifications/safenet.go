package notifications

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

func isPrivateOrReserved(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// validateExternalURL checks that a URL is HTTPS and does not resolve to a private IP.
func validateExternalURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must include a hostname")
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if isPrivateOrReserved(ip) {
			return fmt.Errorf("URL must not point to private/loopback addresses")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve hostname: %w", err)
	}
	for _, ipStr := range ips {
		resolved := net.ParseIP(ipStr)
		if resolved != nil && isPrivateOrReserved(resolved) {
			return fmt.Errorf("URL hostname resolves to a private/loopback address")
		}
	}
	return nil
}

// safeHTTPClient returns an HTTP client that blocks connections to private IPs
// and does not follow redirects.
func safeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: func(_ string, address string, _ syscall.RawConn) error {
					host, _, _ := net.SplitHostPort(address)
					ip := net.ParseIP(host)
					if ip != nil && isPrivateOrReserved(ip) {
						return fmt.Errorf("connections to private/loopback addresses blocked")
					}
					return nil
				},
			}).DialContext,
		},
	}
}

// sanitizeHeader removes CRLF characters from an SMTP header value.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
