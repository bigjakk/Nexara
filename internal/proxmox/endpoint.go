package proxmox

import (
	"net"
	"net/url"
)

// APIURLHost returns the host (without port) of a Proxmox api_url such as
// "https://192.168.1.10:8006/". It returns "" when the URL cannot be parsed,
// which callers use to harmlessly disable primary-endpoint de-duplication
// during failover.
func APIURLHost(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// FailoverBaseURL builds the API base URL for an alternate cluster member,
// reusing the scheme and port of the primary api_url so clusters on a
// non-default port keep working. It defaults to https and port 8006 when the
// primary URL cannot be parsed or omits a port.
func FailoverBaseURL(primaryAPIURL, address string) string {
	scheme, port := "https", "8006"
	if u, err := url.Parse(primaryAPIURL); err == nil {
		if u.Scheme != "" {
			scheme = u.Scheme
		}
		if p := u.Port(); p != "" {
			port = p
		}
	}
	return scheme + "://" + net.JoinHostPort(address, port)
}
