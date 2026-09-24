package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams, optFlag and optString — lives in
// registry_vms.go; pveObjectNameParam lives in registry_networks.go, and the
// reuse is deliberate rather than convenient (see acmeObjectNameParam).

// The two prefixes this domain hangs off. An ACME account, plugin, directory
// listing and ToS are CLUSTER-wide — Proxmox stores them in the cluster
// filesystem under /cluster/acme — while a certificate and the ACME settings
// that drive its renewal belong to ONE NODE, because that is where the
// certificate lives.
//
// The node prefix spells its parameter :node, not :node_name as registry_nodes.go
// does. That is what router.go registered and what the API has always published;
// Fiber matches positionally so the two never collide, and renaming it would be
// a gratuitous break for anyone reading the route table.
const (
	acmeScope     = clusterScope + "/acme"
	acmeNodeScope = clusterScope + "/nodes/:node"
)

// acmeCertificateResource is the RBAC resource all 18 routes share: view for
// the eight reads, manage for the ten writes. A certificate is the subject
// rather than the node, which is why ordering one needs manage:certificate and
// not manage:node.
const acmeCertificateResource = "certificate"

func acmeView() Permissions   { return clusterCheck("view", acmeCertificateResource) }
func acmeManage() Permissions { return clusterCheck("manage", acmeCertificateResource) }

// emptyOrACMEURL is the shape of the two URL-valued body parameters, with the
// empty string allowed.
//
// "" is a SENTINEL both handlers already read: proxmox.CreateACMEAccount sets
// the form key only `if params.Directory != ""`, so an empty value means "let
// Proxmox use its default directory" and refusing it would 400 a request that
// has always worked. Both branches of the alternation carry their own anchors
// because Go's regexp is a substring search — `^$|https?://` would match
// anything CONTAINING a URL — which is the same trap emptyOrNodeName documents
// in registry_vms.go.
//
// This is a SCHEME anchor and nothing more. It is deliberately NOT an SSRF
// control and must not be read as one: the ACME directory is fetched by the
// PROXMOX NODE, not by Nexara, so url_policy.go's validateURLFormat and
// enforceURLAddressPolicy — which exist for URLs this process dials — do not
// apply and are not invoked here. See the note on acmeDirectoryParam.
const emptyOrACMEURL = `^$|^https?://`

// acmeObjectNameParam is an ACME account name or plugin id as a PATH parameter.
//
// It reuses pveObjectNameParam (registry_networks.go) because the requirement is
// identical and the reason is the same: internal/proxmox/client_acme.go builds
// /cluster/acme/account/<name> and /cluster/acme/plugins/<id> by concatenation
// with url.PathEscape, and PathEscape escapes "/" but leaves "." and ".." alone,
// so an un-anchored name resolves upward onto the parent collection wherever a
// proxy in front of pveproxy normalises the path. (pveproxy itself takes the
// segment literally, as a name pve-configid refuses; see
// proxmox.validatePathSegment.)
//
// One thing IS different, and it is why this is worth a comment rather than a
// bare call: unlike every other traversal-anchored parameter in the registry,
// there is no second line of defence behind it. The access domain has
// validateUserID and friends, the PBS domain has validatePathSegment; ACME has
// nothing — client_acme.go escapes and sends. This pattern is the only anchor,
// so widening it is not a cosmetic change.
func acmeObjectNameParam(description string) apischema.Property {
	return pveObjectNameParam(description)
}

// acmeNodeParams is the pair every per-node route carries. node_name's format
// is apischema's own node-name, which is the tightest of the four spellings of
// that check in the tree; proxmox.validateNodeName runs again inside the client
// and stays there, because it guards every caller rather than this one.
func acmeNodeParams(extra apischema.Properties) apischema.Properties {
	node := apischema.StdOption("node-name")
	node.Description = "Proxmox node name — the node whose certificate or ACME settings this acts on."
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"node":       node,
	}, extra)
}

// acmeForceParam is the `force` flag the order and renew routes take.
//
// Both handlers bound it with `_ = c.Bind().Body(&req)` — the error DISCARDED —
// so a malformed body silently ordered with force=false. The declaration keeps
// the default and the meaning, and turns the malformed body into the 400 it
// always should have been.
func acmeForceParam(action string) apischema.Property {
	return optFlag("Replace the existing certificate even when " + action +
		" would normally be skipped. Omitted, Proxmox decides.")
}

// registerACMEEndpoints declares all 18 of ACMEHandler's routes.
//
// Every one is a plain cluster-scoped Check on `certificate` — 8 view, 10
// manage — and none is Deferred, Advisory or global: each handler resolved the
// cluster from its own path and made exactly one static requireClusterPerm
// call, so every one of them hoists into middleware.
//
// What stays in the handlers is what the declaration cannot see:
//
//   - ListPlugins blanks the dns-01 plugin's `data` field, which holds the DNS
//     provider's API credentials, before the response goes out on a view-gated
//     read.
//   - SetNodeACMEConfig maps PVE::Tools::assert_if_modified's die string to a
//     409 (mapNodeConfigError), because a digest mismatch is "someone else
//     edited this node" rather than a gateway failure.
//   - proxmox.SetNodeACMEConfig owns the delete allow-list and the
//     set-and-clear-in-one-request refusal. Both are choke-point checks that
//     protect every caller, not just this route — see deletableNodeACMEKeys in
//     internal/proxmox/client_nodes.go, which says so at length.
func registerACMEEndpoints(reg *Registry, h *handlers.ACMEHandler) {
	// ── ACME accounts ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   acmeScope + "/accounts",
		Description: "List the cluster's registered ACME accounts. An account is cluster-wide: Proxmox " +
			"stores it in the cluster filesystem and every node's certificate order can use it.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListAccounts,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   acmeScope + "/accounts",
		Description: "Register an ACME account with a directory. This runs as a Proxmox TASK — the " +
			"response carries its UPID — because registering involves a round trip to the CA and " +
			"accepting its terms of service.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  clusterParams(createACMEAccountParams()),
		Handler:     h.CreateAccount,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        acmeScope + "/accounts/:name",
		Description: "Get one ACME account: its directory, its registration location and the terms it accepted.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters: clusterParams(apischema.Properties{
			"name": acmeObjectNameParam("ACME account name, as GET .../acme/accounts reports it."),
		}),
		Handler: h.GetAccount,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   acmeScope + "/accounts/:name",
		Description: "Change an ACME account's contact address. Empty or omitted leaves the stored " +
			"contact alone — there is no way to clear it through this route, which is how it has always " +
			"behaved.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters: clusterParams(apischema.Properties{
			"name":    acmeObjectNameParam("ACME account name."),
			"contact": acmeContactParam(true),
		}),
		Handler: h.UpdateAccount,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   acmeScope + "/accounts/:name",
		Description: "Deregister an ACME account. Certificates already issued through it keep working " +
			"until they expire; nothing renews them afterwards.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters: clusterParams(apischema.Properties{
			"name": acmeObjectNameParam("ACME account name."),
		}),
		Handler: h.DeleteAccount,
	})

	// ── ACME plugins ──────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   acmeScope + "/plugins",
		Description: "List the cluster's ACME challenge plugins. The dns-01 `data` field — which holds " +
			"the DNS provider's API credentials — is blanked out of this response; only whether a plugin " +
			"is configured is reported.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListPlugins,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   acmeScope + "/plugins",
		Description: "Create an ACME challenge plugin. For dns-01 the `data` field carries the provider's " +
			"API credentials; it is stored by Proxmox and never returned by the listing.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  clusterParams(createACMEPluginParams()),
		Handler:     h.CreatePlugin,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   acmeScope + "/plugins/:plugin_id",
		Description: "Change an ACME plugin. Every parameter is optional and an omitted or EMPTY one " +
			"leaves the stored value alone, so this route cannot clear a credential — replace it, or " +
			"delete the plugin.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters: clusterParams(withParams(updateACMEPluginParams(), apischema.Properties{
			"plugin_id": acmeObjectNameParam("ACME plugin id, as GET .../acme/plugins reports it."),
		})),
		Handler: h.UpdatePlugin,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   acmeScope + "/plugins/:plugin_id",
		Description: "Delete an ACME plugin and the credentials stored with it. Node ACME settings that " +
			"name it keep the reference and stop validating; nothing rewrites them.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters: clusterParams(apischema.Properties{
			"plugin_id": acmeObjectNameParam("ACME plugin id."),
		}),
		Handler: h.DeletePlugin,
	})

	// ── Catalogue reads ───────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   acmeScope + "/challenge-schema",
		Description: "List the dns-01 challenge plugins this Proxmox knows about and the fields each one " +
			"takes, so a client can render a provider form without hardcoding one that dates.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListChallengeSchema,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        acmeScope + "/directories",
		Description: "List the ACME directories Proxmox ships as presets, with their URLs.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListDirectories,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        acmeScope + "/tos",
		Description: "Get the terms-of-service URL the default ACME directory currently publishes.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  clusterParams(nil),
		Handler:     h.GetTOS,
	})

	// ── Node ACME configuration ───────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   acmeNodeScope + "/acme-config",
		Description: "Read one node's ACME settings — which account it uses and the domains it requests — " +
			"together with the node config's digest. Send that digest back on the PUT to make the write a " +
			"compare-and-swap.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  acmeNodeParams(nil),
		Handler:     h.GetNodeACMEConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   acmeNodeScope + "/acme-config",
		Description: "Write one node's ACME settings. An omitted or EMPTY field leaves the stored value " +
			"alone — no value means \"remove\" — so clearing one means naming its key in `delete`. Naming " +
			"a key in both is refused rather than silently resolved as a delete, which is what Proxmox " +
			"would do. Sending `digest` turns the write into a compare-and-swap and answers 409 when the " +
			"node config changed since it was read.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  acmeNodeParams(nodeACMEConfigParams()),
		Handler:     h.SetNodeACMEConfig,
	})

	// ── Node certificates ─────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   acmeNodeScope + "/certificates",
		Description: "List the certificates installed on one node, with their issuer, subject, SANs and " +
			"validity window.",
		Group:       "Certificates",
		Permissions: acmeView(),
		Parameters:  acmeNodeParams(nil),
		Handler:     h.ListNodeCertificates,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   acmeNodeScope + "/certificates/order",
		Description: "Order a new ACME certificate for one node. Runs as a Proxmox task — the response " +
			"carries its UPID — and restarts pveproxy when it lands.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  acmeNodeParams(apischema.Properties{"force": acmeForceParam("a valid certificate is already installed")}),
		Handler:     h.OrderNodeCertificate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   acmeNodeScope + "/certificates/renew",
		Description: "Renew one node's ACME certificate. Runs as a Proxmox task. Proxmox skips a " +
			"certificate that is not near expiry unless force says otherwise.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  acmeNodeParams(apischema.Properties{"force": acmeForceParam("the certificate is not near expiry")}),
		Handler:     h.RenewNodeCertificate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   acmeNodeScope + "/certificates/revoke",
		Description: "Revoke one node's ACME certificate and remove it. Runs as a Proxmox task. The node " +
			"falls back to its self-signed certificate, so every client that pinned the ACME one will warn.",
		Group:       "Certificates",
		Permissions: acmeManage(),
		Parameters:  acmeNodeParams(nil),
		Handler:     h.RevokeNodeCertificate,
	})
}

// acmeContactParam is the account's contact address list.
//
// PVE declares it as a string-list — several addresses separated by commas — so
// it carries no "email" format: the format validates ONE address and would
// refuse the very shape this parameter exists to take. Proxmox rejects a
// malformed member itself, naming it.
//
// optional is false on CREATE, matching the handler's own `if req.Contact == ""`
// refusal, and true on UPDATE, where an empty value has always meant "leave the
// stored contact alone".
func acmeContactParam(optional bool) apischema.Property {
	p := apischema.Property{
		Type:      apischema.String,
		Optional:  optional,
		MaxLength: apischema.Ptr(1024),
		Typetext:  "<email[,email...]>",
		Description: "Contact address (or comma-separated addresses) the CA notifies about expiry. " +
			"Proxmox validates each one.",
	}
	if !optional {
		p.MinLength = apischema.Ptr(1)
	}
	return p
}

// acmeDirectoryParam is the ACME directory URL.
//
// It is the one value in this domain that reads as SSRF-shaped, and the note is
// here so the next reader does not have to re-derive the answer: Nexara never
// dials it. The value is handed to Proxmox as a form field and it is the PVE
// node that fetches the directory, which is why url_policy.go's
// enforceURLAddressPolicy — whose whole job is to classify an address THIS
// process is about to connect to, and which answers a private address with a
// 422 confirm-gate rather than a refusal — is not invoked here and must not be
// bolted on: it would refuse an internal ACME server (a lab step-ca on an
// RFC1918 address) that Proxmox itself reaches perfectly well.
//
// What the declaration does add is the scheme anchor, which matches PVE's own
// pve-acme-directory format. It keeps a file:// or gopher:// string out of the
// cluster's stored configuration without claiming to be an address policy.
func acmeDirectoryParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Optional:    true,
		Pattern:     emptyOrACMEURL,
		MaxLength:   apischema.Ptr(2048),
		Typetext:    "<https://…>",
		Description: description,
	}
}

// createACMEAccountParams is the body of POST .../acme/accounts.
//
// contact is the only required field, which is exactly the one check the handler
// made. The account NAME is optional because Proxmox names it "default" when it
// is absent, and it carries the same pattern its path-parameter form does — an
// account created under a name this API cannot address again would be a dead
// row from the moment it was written.
func createACMEAccountParams() apischema.Properties {
	// "" is a supplied value apischema does NOT fall back to the default for
	// (see present() in validate.go), and it reaches Proxmox as no name at all:
	// proxmox.CreateACMEAccount sets the form key only `if params.Name != ""`,
	// exactly as it does for directory and tos_url on this same body, so an
	// empty name creates the account Proxmox calls "default". The route was
	// unvalidated before it was declared, so refusing "" would 400 a request
	// that has always worked.
	name := pveObjectNameOrEmptyParam(
		"Name for the account. Omitted OR EMPTY, Proxmox calls it \"default\".")
	return apischema.Properties{
		"name":    name,
		"contact": acmeContactParam(false),
		"directory": acmeDirectoryParam(
			"ACME directory URL. Empty or omitted uses Proxmox's default, which is Let's Encrypt."),
		"tos_url": acmeDirectoryParam(
			"Terms-of-service URL being accepted, as GET .../acme/tos reports it. Empty or omitted " +
				"registers without accepting terms, which most CAs refuse."),
	}
}

// createACMEPluginParams is the body of POST .../acme/plugins.
//
// id and type are both required, matching the handler's single
// `if req.ID == "" || req.Type == ""` refusal.
//
// `type` carries no Enum. Proxmox owns that vocabulary — it is "dns" or
// "standalone" today — and GET .../acme/challenge-schema publishes the
// authoritative list of what a dns plugin may be, so a copy here would date on
// the next PVE release and reject a value the cluster in front of the operator
// actually accepts.
func createACMEPluginParams() apischema.Properties {
	// Declared as plugin_id with "id" as an ALIAS, and that is not cosmetic.
	// checkPathParams refuses a parameter named "id" that resolves to anything
	// but the path, because clusterIDFromParam reads TWO names — cluster_id,
	// falling back to id — so a body "id" is a name the permission gate also
	// reads. On THIS path the fallback cannot fire (:cluster_id is present, so
	// the gate never reaches it), but the refusal is deliberately about the NAME
	// rather than about whether today's path happens to make it safe, and
	// routing around a fail-closed guard is how the hole it protects gets
	// reopened by the next path edit. registry_replication.go's job_id carries
	// the same alias for the same reason.
	//
	// The alias keeps every existing caller working: the plugin dialog sends
	// {"id": "dns-example"}. plugin_id is the canonical name because it is
	// what the two per-plugin routes spell in their own paths.
	id := pveObjectNameParam("Identifier for the new plugin. Also accepted as \"id\", which is what " +
		"Nexara's own dialog sends.")
	id.Alias = "id"
	id.MinLength = apischema.Ptr(1)
	return withParams(acmePluginFieldParams(), apischema.Properties{
		"plugin_id": id,
		"type": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<dns|standalone>",
			Description: "Challenge type. Proxmox owns this vocabulary and rejects one it does not know.",
		},
	})
}

// updateACMEPluginParams is the body of PUT .../acme/plugins/:plugin_id.
//
// `digest` is here and not on the create for the reason it exists at all: it
// turns the write into a compare-and-swap against the plugin configuration the
// caller read. There is nothing to compare against on a create.
func updateACMEPluginParams() apischema.Properties {
	return withParams(acmePluginFieldParams(), apischema.Properties{
		"digest": optString(128, "<digest>",
			"Digest of the plugin configuration this edit was based on. Sending it makes the write a "+
				"compare-and-swap; omitting it overwrites unconditionally."),
	})
}

// acmePluginFieldParams are the mutable plugin fields create and update share.
//
// Every one is read as `if x != ""`, so an empty value is absent rather than a
// clear — which is why none of them carries a MinLength that would turn an empty
// string into a 400 the route has never answered.
func acmePluginFieldParams() apischema.Properties {
	return apischema.Properties{
		"api": optString(64, "<provider>",
			"DNS provider the dns-01 challenge talks to, as GET .../acme/challenge-schema names it."),
		// The credential. Proxmox base64-encodes it on the way out and the
		// listing blanks it on the way back, so this is the only direction it
		// ever travels. It is deliberately NOT recorded in the audit row: the
		// handler logs the plugin id alone.
		"data": optString(16384, "<key=value[\\n…]>",
			"Provider-specific credentials and options for the challenge, as newline-separated "+
				"key=value pairs. Stored by Proxmox and never read back."),
		"validation-delay": {
			Type:     apischema.Integer,
			Optional: true,
			Minimum:  apischema.Ptr(0.0),
			// PVE's own ceiling for this key: two days in seconds.
			Maximum: apischema.Ptr(172800.0),
			// NO Default: an omitted key means "do not send it", which leaves
			// Proxmox's own default in force. The handler's *int comes from
			// p.OptInt, which a default cannot fool (apischema.Property.Default),
			// so a declared one would only document a second copy of Proxmox's.
			Typetext: "<integer>",
			Description: "Seconds to wait after writing the DNS record before asking the CA to validate " +
				"it. Omitted, Proxmox's own default applies.",
		},
	}
}

// nodeACMEConfigParams is the body of PUT .../nodes/:node/acme-config.
//
// Every settable key is optional and carries no default, because an EMPTY value
// means "leave it alone" rather than "remove": PVE's node config declares all of
// them optional, so there is no value that spells removal and clearing one goes
// through `delete`.
//
// acmedomain0..5 is the complete set — $MAXDOMAINS is 5 in PVE::NodeConfig —
// and it is written out rather than generated so that each one renders into the
// API docs with a description of its own.
func nodeACMEConfigParams() apischema.Properties {
	domain := func(n string) apischema.Property {
		return optString(1024, "<domain=…,plugin=…>",
			"Additional domain "+n+" to request, as a Proxmox property string: "+
				"domain=host.example.com, optionally plugin=<id> and alias=<name>. Empty or omitted "+
				"leaves it alone; naming it in delete removes it.")
	}
	return apischema.Properties{
		"acme": optString(1024, "<account=…,domains=…>",
			"The node's ACME setting, as a Proxmox property string: account=<name> and an optional "+
				"domains=<a;b> list. Clearing it does NOT turn ACME off — Proxmox reads a missing "+
				"account as \"default\" — and it takes any standalone domains in the property string "+
				"with it."),
		"acmedomain0": domain("0"),
		"acmedomain1": domain("1"),
		"acmedomain2": domain("2"),
		"acmedomain3": domain("3"),
		"acmedomain4": domain("4"),
		"acmedomain5": domain("5"),
		"delete": {
			Type:     apischema.Array,
			Optional: true,
			// A bound on the list, not a restatement of what may be in it.
			// proxmox.SetNodeACMEConfig owns the membership rule
			// (deletableNodeACMEKeys) and refuses a key that is being SET in the
			// same request — a cross-field rule apischema cannot state, and one
			// that matters because PVE applies `delete` after the assignments
			// and would silently drop the value.
			MaxLength: apischema.Ptr(16),
			Items: &apischema.Property{
				Type:      apischema.String,
				MaxLength: apischema.Ptr(32),
				Typetext:  "<key>",
			},
			Typetext: "<key>[,<key>…]",
			Description: "Settings to clear. Only acme and acmedomain0..5 may be named; anything else " +
				"is refused, because PVE applies delete to the WHOLE node config and description, " +
				"location and wakeonlan live in the same file.",
		},
		"digest": optString(128, "<digest>",
			"Digest of the node config this edit was based on, as the GET returns it. Sending it makes "+
				"the write a compare-and-swap and answers 409 if the config changed; omitting it "+
				"overwrites unconditionally."),
	}
}
