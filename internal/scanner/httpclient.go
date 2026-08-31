package scanner

import (
	"errors"
	"fmt"
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
// (Debian tracker, CISA KEV, FIRST EPSS). The redirect and SSRF policy lives in
// netguard.NewHTTPClient, which several subsystems now share; see its doc for
// why redirects are not followed and why the dial guard is needed.
//
// One client per scanner Engine, not one per call: every cluster scan re-fetches
// the feeds, and a fresh client would discard the keep-alive connection and pay
// a TLS handshake each time.
func newScannerHTTPClient(timeout time.Duration) *http.Client {
	return netguard.NewHTTPClient(timeout)
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
