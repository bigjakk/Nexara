package scanner

import (
	"errors"
	"fmt"
	"net/http"
)

// errUnexpectedRedirect is returned by callers when an upstream feed responded
// with a 3xx status code. We never follow redirects from feed servers — they
// are the only host we expect to talk to, and a redirect indicates either a
// misconfiguration or an upstream takeover. Callers should fail closed.
var errUnexpectedRedirect = errors.New("scanner: unexpected redirect from external feed")

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
