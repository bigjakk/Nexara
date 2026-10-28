package pvefield

// secretParams are the Proxmox VE API parameters whose value is a secret, which
// no record Nexara keeps may repeat. Transcribed from the PVE API schema
// (pve-docs apidoc.js: every parameter described as a password, token, key or
// credential), which takes in Proxmox's own lists: PVE::Storage::Plugin's
// sensitive_properties (encryption-key, keyring, master-pubkey, password), the
// metric servers' token, a realm's bind password and qemu-server's cipassword,
// which Proxmox's own task log shows as <hidden>. key and data are a keystroke
// (sendkey) and /etc/hosts (hosts) on the routes where they are no secret;
// redacting those too costs nothing.
//
// migrations/000127_redact_audit_secrets scrubbed the rows written before this
// list existed. A name added later needs its own scrub of the rows already kept.
var secretParams = map[string]bool{
	"password":              true, // users, realms, CIFS/PBS/ESXi storage, SMTP endpoints, a container's root, the agent's set-user-password
	"cipassword":            true, // cloud-init
	"confirmation-password": true, // the caller's own, on PUT /access/password
	"token":                 true, // InfluxDB metric servers, Gotify endpoints, SDN IPAMs
	"secret":                true, // webhook endpoints
	"key":                   true, // SDN DNS, a custom certificate's private key, the subscription
	"keyring":               true, // RBD and CephFS storage of an external cluster
	"encryption-key":        true, // PBS storage
	"master-pubkey":         true, // PBS storage
	"eab-hmac-key":          true, // ACME accounts
	"data":                  true, // ACME DNS plugins: the provider's credentials, base64
}

// Secret reports whether the Proxmox parameter name carries a secret.
func Secret(name string) bool { return secretParams[name] }
