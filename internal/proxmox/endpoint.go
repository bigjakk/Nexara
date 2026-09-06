package proxmox

import (
	"net"
	"net/url"
	"strings"
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

// PVEProxyPort is the port pveproxy serves the Proxmox API on. It is not
// configurable in any supported way, which is what makes it usable as evidence
// that an api_url points straight at a node rather than at something in front
// of one.
const PVEProxyPort = "8006"

// APIURLIsDirectToPVEProxy reports whether an api_url addresses pveproxy
// itself, i.e. its port is 8006 (explicitly, since the URL's own default would
// be 443).
//
// Callers use this before comparing an api_url against a node's certificate:
// nodes.ssl_fingerprint only ever describes what pveproxy serves, so a URL on
// any other port is reaching something else — commonly a reverse proxy
// terminating TLS on the node's own address — whose certificate it says
// nothing about.
func APIURLIsDirectToPVEProxy(apiURL string) bool {
	u, err := url.Parse(apiURL)
	if err != nil {
		return false
	}
	return u.Port() == PVEProxyPort
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

// NodeEndpoint is one cluster member's reachable address and the certificate
// it serves, as the collector last observed them. Declared here rather than
// taking the generated db row so this package stays independent of the schema
// and the selection logic can be tested without a database.
type NodeEndpoint struct {
	Name           string
	Address        string
	SSLFingerprint string
	// Status is the collector's view: "online", "offline", …
	Status string
}

// ClusterEndpoint is the address and pin a client should actually use.
type ClusterEndpoint struct {
	BaseURL        string
	TLSFingerprint string
	// ViaNode is the member that was substituted for the configured api_url,
	// or "" when the configured endpoint is being used as-is.
	ViaNode string
}

// EndpointCertificateChanged reports whether a member is serving a certificate
// other than the one pinned for it.
//
// Both sides are normalised so this asks exactly the question the TLS verifier
// asks; an empty value on either side means "unknown", which is never a
// mismatch — absence of evidence is not evidence of a changed certificate.
func EndpointCertificateChanged(pinned, observed string) bool {
	if pinned == "" || observed == "" {
		return false
	}
	return NormalizeFingerprint(pinned) != NormalizeFingerprint(observed)
}

// SelectClusterEndpoint chooses which member a client for this cluster should
// talk to: normally the configured api_url, but an alternate when the
// configured one demonstrably cannot serve requests.
//
// The API layer historically had no failover at all, so a primary that was
// rebooting — or presenting a rotated certificate — took down every live
// Proxmox call for the cluster while the collector sailed on through another
// member. This closes that asymmetry using facts the collector already
// records, without probing the network on the request path.
//
// It substitutes only on positive evidence and falls back to the configured
// endpoint whenever it has none, so a healthy cluster is unaffected. Three
// guards keep it from redirecting a working deployment:
//
//   - The api_url must address pveproxy directly (port 8006). nodes.
//     ssl_fingerprint describes only what pveproxy serves, so for anything
//     else — most commonly a reverse proxy terminating TLS on the node's own
//     address, which DOES match by address — it is evidence about a different
//     listener. Comparing the two would find a permanent mismatch and exile
//     the operator to a port they deliberately fronted.
//   - The api_url host must be a member we know about; a VIP or DNS name
//     matches nothing and is left alone.
//   - Each alternate is pinned to its own certificate (members present
//     distinct ones), must be online, and is skipped when no fingerprint has
//     been recorded rather than connecting unpinned, which would silently
//     downgrade to system-CA verification.
//
// Note the substitution is not a defence against interception, and does not
// paper over one: a real MITM fails the collector's pin too, so the collector
// fails over and the alternate reports the primary's true certificate — which
// still matches the pin, and no substitution happens. What this triggers on is
// the cluster itself reporting a different certificate for that node, i.e. a
// rotation.
func SelectClusterEndpoint(apiURL, pinnedFingerprint string, endpoints []NodeEndpoint) ClusterEndpoint {
	configured := ClusterEndpoint{BaseURL: apiURL, TLSFingerprint: pinnedFingerprint}

	// nodes.ssl_fingerprint only describes pveproxy's certificate, so it is
	// evidence about this endpoint only when the endpoint IS pveproxy. Same
	// guard endpointFingerprintStale applies before raising the banner.
	if !APIURLIsDirectToPVEProxy(apiURL) {
		return configured
	}

	primaryHost := APIURLHost(apiURL)
	if primaryHost == "" {
		return configured
	}

	var primary *NodeEndpoint
	for i := range endpoints {
		if strings.EqualFold(endpoints[i].Address, primaryHost) {
			primary = &endpoints[i]
			break
		}
	}
	// The configured endpoint is not a member we know about — a VIP, a load
	// balancer, a DNS name. We know nothing about its health, so leave it be.
	if primary == nil {
		return configured
	}

	primaryUsable := primary.Status != "offline" &&
		!EndpointCertificateChanged(pinnedFingerprint, primary.SSLFingerprint)
	if primaryUsable {
		return configured
	}

	for i := range endpoints {
		alt := endpoints[i]
		// Stricter than the primary check above, deliberately. Treating an
		// unknown status as usable is the safe default when deciding whether
		// to LEAVE an endpoint alone; it is the unsafe one when choosing where
		// to send traffic instead.
		if strings.EqualFold(alt.Address, primaryHost) ||
			alt.Address == "" || alt.SSLFingerprint == "" || alt.Status != "online" {
			continue
		}
		return ClusterEndpoint{
			BaseURL:        FailoverBaseURL(apiURL, alt.Address),
			TLSFingerprint: alt.SSLFingerprint,
			ViaNode:        alt.Name,
		}
	}

	// Nothing better to offer. Returning the configured endpoint keeps the
	// error the caller gets identical to what it would have been.
	return configured
}
