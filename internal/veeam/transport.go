package veeam

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

// buildHTTPClient constructs the hardened HTTP client every Veeam client
// shares: the SSRF dial guard, TLS fingerprint pinning with session tickets
// disabled, and redirect refusal.
//
// This is the SOLE owner of those settings in this package, mirroring
// proxmox.buildHTTPClient. TestGuard_SingleTransportConstructor pins that:
// a second http.Transport here would silently fork the hardening, and the
// forked client is the one that talks to an operator-supplied URL carrying an
// admin password.
//
// Verification modes, in precedence order:
//
//	fingerprint set          pin the leaf SHA-256; the CA chain is irrelevant
//	verifyTLS = false        no verification at all (operator opt-out)
//	otherwise                verify against the system CA pool
//
// All three are reachable in practice. VBR ships a self-signed certificate, so
// pinning is the common path — but the lab server sits behind a reverse proxy
// with a publicly-valid chain, so assuming self-signed would be wrong.
func buildHTTPClient(tlsFingerprint string, verifyTLS bool, timeout time.Duration) *http.Client {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	switch {
	case tlsFingerprint != "":
		expected := normalizeFingerprint(tlsFingerprint)

		tlsCfg.InsecureSkipVerify = true //nolint:gosec // VerifyPeerCertificate below pins the leaf fingerprint
		// Session tickets would let a resumed handshake skip
		// VerifyPeerCertificate entirely, so the pin has to disable them
		// (gosec G123).
		tlsCfg.SessionTicketsDisabled = true
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("veeam: server presented no certificates")
			}
			actual := formatFingerprint(rawCerts[0])
			if actual != expected {
				return fmt.Errorf("veeam: TLS fingerprint mismatch: got %s, want %s", actual, expected)
			}
			return nil
		}

	case !verifyTLS:
		// Operator opt-out. Deliberately the lowest-precedence branch: a
		// stored fingerprint always wins, so turning verification off on a
		// pinned server does not quietly unpin it.
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // G402: explicit operator opt-out, defaults to false and is surfaced in the UI and audit log
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
			// Block dialing cloud metadata, multicast, broadcast, Class E or
			// unspecified IPs even if DNS resolves to one after the handler's
			// policy check passed (rebinding defence).
			Control: netguard.DialControlSSRFGuard,
		}).DialContext,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// Surface any redirect as a caller error rather than following it.
		// The VBR REST API never legitimately 3xxes; a redirect means a
		// misconfigured reverse proxy or a hostile upstream, and either way
		// following it could carry the bearer token to a host it was never
		// minted for. Go strips Authorization across hosts but not across
		// paths on the same host.
		CheckRedirect: refuseRedirect,
		// No cookie jar: nothing about this API is cookie-authenticated, and
		// a jar would outlive the request.
		Jar: nil,
	}
}

// refuseRedirect turns any 3xx into a caller-visible error.
//
// The redirect TARGET is deliberately left out of the message: it comes
// straight from the remote's Location header, and this error is rendered to an
// API caller. Only the request we made is named.
func refuseRedirect(_ *http.Request, via []*http.Request) error {
	return fmt.Errorf("veeam: %s redirected, which the API never legitimately does", via[len(via)-1].URL)
}

// formatFingerprint returns the lowercase hex SHA-256 digest of a DER-encoded
// certificate.
func formatFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// normalizeFingerprint reduces a user-supplied fingerprint to bare lowercase
// hex, accepting the colon-separated uppercase form the UI displays.
func normalizeFingerprint(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
}
