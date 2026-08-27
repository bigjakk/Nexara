package handlers

import "github.com/gofiber/fiber/v3"

// A stored secret paired with a mutable address is an exfiltration primitive.
// A caller who may edit the address but has never seen the credential can
// point the row at a host they control and let Nexara deliver it. Nothing
// about the target looks wrong on the way in: it is a public host, so the
// SSRF policy in url_policy.go passes it, and validateURLFormat only ever
// inspected scheme, host and userinfo.
//
// The rule every such pair enforces is the same one: a credential is only ever
// sent to the address it was saved for. Move the address and the caller has to
// supply the credential again, which means they can only ever send one they
// already hold.
//
// Delivery is what makes this a hard refusal rather than a warning. Some of
// these paths connect during the update itself — ClusterHandler.Update ends in
// testClusterConnectivity, which dials with the decrypted stored secret. The
// rest hand the row to a background reader: the collector rebuilds clients
// from the stored row (internal/proxmox/cache.go), LDAP binds on the next
// sync, OIDC exchanges the secret at the next callback. Nothing has to go
// wrong for the credential to leave, and on the delayed paths it leaves long
// after the request that armed it — which is harder to notice than an
// immediate probe would be.

// credentialRedirected reports whether an update moves a stored credential to
// an address it was not saved for without supplying a replacement for it.
//
// The address comparison is on the FULL URL, not scheme+host. Every client
// here appends its API path to the stored address verbatim —
// internal/proxmox/api_client.go builds `baseURL + "/api2/json" + path`, the
// Veeam client appends /api/oauth2/token, OIDC discovery appends
// /.well-known/openid-configuration — so a path or a query string re-points
// the credential at a different listener behind the same host.
// "https://pve.example.com/attacker-path" and "https://pve.example.com?x="
// both keep the origin while sending the token somewhere else entirely. An
// origin-only comparison would have made the refusal message a lie.
//
// Comparing raw strings also means a cosmetic edit — a trailing slash the
// client would have trimmed anyway — asks for the credential again. That is
// the harmless direction to be wrong in, and normalizing is how an equality
// check like this grows a bypass.
//
// storedSecret covers the case where there is nothing to redirect: an LDAP
// config doing an anonymous bind, or an OIDC public client, holds no
// credential at risk and has no reason to be blocked from changing address.
func credentialRedirected(newAddress, existingAddress, storedSecret, suppliedSecret string) bool {
	return newAddress != existingAddress && suppliedSecret == "" && storedSecret != ""
}

// errCredentialRedirect is the refusal. It names the field the caller has to
// fill in, because the alternative reads as an unexplained validation failure
// on a form where that field is legitimately optional every other time.
func errCredentialRedirect(addressNoun, credentialNoun string) error {
	return fiber.NewError(fiber.StatusBadRequest,
		"Changing the "+addressNoun+" requires re-entering the "+credentialNoun+". "+
			"The stored credential is only ever sent to the address it was saved for.")
}
