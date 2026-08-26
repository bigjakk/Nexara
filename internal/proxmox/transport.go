package proxmox

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

// buildHTTPClient constructs the hardened HTTP client every Proxmox client
// shares: the SSRF dial guard, TLS fingerprint pinning with session tickets
// disabled, and redirect refusal.
//
// This is the SOLE owner of those settings. Constructing a second
// http.Transport or http.Client elsewhere in this package would silently fork
// the hardening — the next person to tighten one copy would not know the other
// existed. TestGuard_SingleTransportConstructor pins that invariant.
//
// The returned *tls.Config is the same one installed on the transport; callers
// keep it because the console websocket dialer needs to reuse it.
func buildHTTPClient(tlsFingerprint string, timeout time.Duration) (*http.Client, *tls.Config) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if tlsFingerprint != "" {
		expected := strings.ToLower(strings.ReplaceAll(tlsFingerprint, ":", ""))

		tlsCfg.InsecureSkipVerify = true //nolint:gosec // Custom VerifyPeerCertificate provides fingerprint verification
		// Disable TLS session tickets to ensure VerifyPeerCertificate is called on every connection.
		// Without this, resumed sessions could bypass fingerprint verification (gosec G123).
		tlsCfg.SessionTicketsDisabled = true
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("proxmox: server presented no certificates")
			}
			actual := formatFingerprint(rawCerts[0])
			if actual != expected {
				return fmt.Errorf("proxmox: TLS fingerprint mismatch: got %s, want %s", actual, expected)
			}
			return nil
		}
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
			// Block dialing to cloud metadata, multicast, broadcast, Class E,
			// or unspecified IPs even if DNS resolves to one (rebinding defence).
			Control: netguard.DialControlSSRFGuard,
		}).DialContext,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// Surface any redirect as a caller-visible error rather than
		// silently following. Proxmox API endpoints don't legitimately
		// 3xx — a redirect almost certainly means a misconfigured
		// reverse-proxy or a hostile upstream; either way the safe
		// posture is to stop. Go's stdlib already strips Authorization
		// on cross-host redirects, but turning the redirect into a
		// caller error also defeats the case where a redirect lands on
		// a same-host different-path endpoint that would still observe
		// the bearer.
		//
		// This matters more for ticketAuth than it ever did for tokenAuth:
		// a PVE ticket rides in a Cookie header, and cookies are the one
		// credential class where "just don't follow it" is the only
		// defence we control.
		CheckRedirect: refuseRedirect,
		// Jar stays nil deliberately. A cookie jar would outlive the
		// request and could re-attach a PVE ticket to a host it was
		// never minted for.
		Jar: nil,
	}, tlsCfg
}

// requestAuth injects credentials into one outbound Proxmox request.
//
// Implementations must be safe for concurrent use: a cached *Client is shared
// across every goroutine serving a cluster. The two long-lived implementations
// (tokenAuth, noAuth) are immutable values, which satisfies that trivially.
type requestAuth interface {
	apply(req *http.Request)
}

// tokenAuth is the API-token mode used by every cached client: a static
// Authorization header, identical to what this package has always sent.
type tokenAuth struct {
	header string
}

func (t tokenAuth) apply(req *http.Request) {
	req.Header.Set("Authorization", t.header)
}

// ticketAuth is the PVEAuthCookie mode, used only by BootstrapClient during
// cluster onboarding. POST /access/ticket sets `allowtoken => 0` in the PVE
// source, so minting a token has no API-token alternative — a ticket is the
// only way in.
type ticketAuth struct {
	ticket string
	csrf   string
}

func (t ticketAuth) apply(req *http.Request) {
	// AddCookie rather than a raw Header.Set, and no cookie Jar on the
	// client: the ticket must not outlive this request.
	//
	// gosec G124 wants Secure/HttpOnly/SameSite here, but those are
	// Set-Cookie directives a server sends to a browser. This is an outbound
	// request cookie, where they carry no meaning and net/http would not emit
	// them. Confidentiality comes from the pinned TLS transport; refuseRedirect
	// is what stops the ticket reaching a host it was never minted for.
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: t.ticket}) //nolint:gosec // G124: outbound request cookie, not a Set-Cookie response

	// PVE requires CSRFPreventionToken on every state-changing request made
	// with cookie auth. Deciding it here, from req.Method, rather than in
	// each do* helper means a future helper cannot forget it.
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		req.Header.Set("CSRFPreventionToken", t.csrf)
	}
}

// noAuth carries the single unauthenticated request this package makes:
// POST /access/ticket itself, which is what produces a ticketAuth.
type noAuth struct{}

func (noAuth) apply(*http.Request) {}
