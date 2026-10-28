package pvefield

// secretParams names Proxmox VE API parameters whose value is a secret, so that
// no record Nexara keeps repeats one: those Proxmox itself hides or treats as
// sensitive (PVE::Storage::Plugin's sensitive_properties — encryption-key,
// keyring, master-pubkey, password; qemu-server's cipassword, which its own task
// log shows as <hidden>; a container's env, which it shows only to
// VM.Config.Options), and the passwords, tokens, keys and client secrets the
// PVE API schema (pve-docs apidoc.js) takes under these names, each marked below
// with where. It is not every secret the API takes: it matches a name, on any
// route, so a secret under another name is not covered (a guest agent's
// file-write content, a webhook endpoint's header values), and key and data are
// also a keystroke (sendkey) and /etc/hosts (hosts), where redacting them costs
// nothing.
//
// migrations/000127_redact_audit_secrets scrubbed these names from the rows
// written before; a name added later needs its own scrub of the rows already
// kept (TestSecretParamsAreTheScrubs).
var secretParams = map[string]bool{
	"password":              true, // users, LDAP and AD realms' bind, CIFS/PBS/ESXi storage, SMTP endpoints, a container's root, the agent's set-user-password
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
	"client-key":            true, // OpenID Connect realms: the client secret
	"tfa":                   true, // a realm's second factor: a Yubico server's API key, inside its property string
	"totp":                  true, // a TOTP entry's otpauth:// URI, which holds its shared secret
	"env":                   true, // a container's environment
}

// Secret reports whether the Proxmox parameter name carries a secret.
func Secret(name string) bool { return secretParams[name] }
