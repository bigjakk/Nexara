package scanner

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

// errUnexpectedRedirect is returned by callers when an upstream feed responded
// with a 3xx status code. We never follow redirects from feed servers — they
// are the only host we expect to talk to, and a redirect indicates either a
// misconfiguration or an upstream takeover. Callers should fail closed.
var errUnexpectedRedirect = errors.New("scanner: unexpected redirect from external feed")

// newScannerHTTPClient builds the *http.Client used by all scanner clients
// (Debian tracker, CISA KEV, FIRST EPSS).
//
// Why one shared client per scanner Engine rather than one per call:
//   - Connection pooling: every cluster scan re-fetches feeds. With a fresh
//     client per call we discard keep-alive connections after each request,
//     adding a TLS handshake's worth of latency to every scan.
//   - Redirect policy: the per-call clients in the pre-3.8 code did not set
//     CheckRedirect. A malicious or misconfigured upstream could 302 us to an
//     attacker-controlled host. CheckRedirect=ErrUseLastResponse short-circuits
//     redirects so callers see the 3xx response and fail.
//   - SSRF defence-in-depth: the dial-control hook re-checks the resolved IP
//     at TCP-connect time and refuses if it's in the always-blocked classes
//     (cloud metadata, broadcast, multicast, Class E, unspecified). The hosts
//     we contact (security-tracker.debian.org, www.cisa.gov, api.first.org)
//     never resolve to those, but a hostile authoritative DNS server could
//     answer with one to bypass any validation step a future operator adds.
//
// The transport is built fresh (not http.DefaultTransport) so the dial guard
// only applies to outbound feed fetches, not to the rest of the binary.
func newScannerHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   netguard.DialControlSSRFGuard,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// checkUpstreamStatus inspects an HTTP response and returns a typed error for
// anything other than 200 OK. Specifically detects the redirect-not-followed
// case so callers can log it loudly — a 3xx after CheckRedirect=ErrUseLastResponse
// usually means an upstream redirect that wasn't there before.
func checkUpstreamStatus(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return fmt.Errorf("%w (status %d)", errUnexpectedRedirect, resp.StatusCode)
		}
		return fmt.Errorf("%w to %s (status %d)", errUnexpectedRedirect, loc, resp.StatusCode)
	}
	return fmt.Errorf("upstream returned status %d", resp.StatusCode)
}
