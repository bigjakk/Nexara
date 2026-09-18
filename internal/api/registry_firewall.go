package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary lives in registry_vms.go; the four-file
// split of NetworkHandler's 66 routes is explained in registry_networks.go.

// firewallScope is the cluster-wide firewall collection, and
// guestFirewallScope is the per-guest one. :cluster_id is the FIRST path
// parameter of both, which namesACluster (permissions.go) requires of a
// cluster-scoped Check.
const (
	firewallScope      = clusterScope + "/firewall"
	guestFirewallScope = clusterScope + "/vms/:vm_id/firewall"
)

// firewallVMIDParam is the guest whose rule set a route acts on, and it is
// NOT the identifier the rest of /clusters/:cluster_id/vms/:vm_id takes.
//
// Every other /vms/:vm_id route in this API spells :vm_id as Nexara's own
// row uuid (apischema's "vm-id" standard option, which says so in as many
// words). These four spell it as the PROXMOX VMID: the handlers have always
// read it with strconv.Atoi and looked the guest up with
// GetVMByClusterAndVmid. That is a genuine inconsistency in the route table
// rather than something this declaration introduces — the declaration's job
// is to state which of the two a caller must send, since until now the only
// way to find out was to send the wrong one and read the message.
//
// The lookup it feeds is cluster-scoped (GetVMByClusterAndVmid takes the
// cluster from the path), so a VMID that exists on another cluster is a 404
// here rather than a cross-cluster read.
//
// The range matches the one the VM clone body already uses for a new VMID.
// It is well inside int32, which is what safeconv.Int32 narrows it to.
var firewallVMIDParam = apischema.Property{
	Type:     apischema.Integer,
	Minimum:  apischema.Ptr(1.0),
	Maximum:  apischema.Ptr(999999999.0),
	Typetext: "<integer>",
	Description: "Proxmox VMID of the guest, e.g. 101 — NOT the Nexara uuid that the other " +
		"/vms/{vm_id} routes take.",
}

// firewallRulePosParam is a rule's position in the ruleset it belongs to, as
// a PATH parameter. Shared by the node, cluster, guest and security-group
// rule routes, which all count from 0 at the top of their own list.
//
// Declared as an integer rather than a string so the schema rejects "abc"
// and "-1" before the handler reaches strconv — which is what the
// hand-rolled parse used to do for the first, and never did for the second.
// The ceiling is the int32 range the value is narrowed into rather than a
// policy: Proxmox's own rule lists are far shorter.
var firewallRulePosParam = apischema.Property{
	Type:        apischema.Integer,
	Minimum:     apischema.Ptr(0.0),
	Maximum:     apischema.Ptr(2147483647.0),
	Typetext:    "<integer>",
	Description: "Rule position, counted from 0 at the top of the ruleset.",
}

// firewallRuleBody is the Proxmox rule shape that FOUR route pairs take —
// the node, cluster, guest and security-group rule sets. Declared once so
// they cannot drift apart.
//
// A fifth surface stores the same shape and is NOT covered by it: the rules
// inside a Nexara firewall template. Those two routes are the ones this
// phase left legacy (see registerFirewallTemplateEndpoints), so they carry
// no schema at all and this helper's guarantee does not reach them.
//
// optional makes type and action optional, which is the update spelling:
// the create handlers refused an empty either, and the update handlers
// refused neither, because firewallRuleToForm drops an empty field rather
// than sending it and Proxmox then keeps the rule's current value. Making
// them required on update as well would look tidier and would reject a
// request that has always worked.
//
// The SECURITY-GROUP create is on the optional side too, and that is not an
// oversight: CreateSecurityGroupRule never checked either field, unlike its
// cluster and node counterparts. The required set is pinned to what each
// handler actually enforced, not to what would be consistent.
//
// Nothing here carries an Enum. The rule vocabulary — macros especially —
// is Proxmox's, it grows with every release, and a copy in this schema would
// reject a macro the cluster in front of the operator accepts. The values
// are relayed as form fields, never as path segments, so what the schema
// owes them is a length bound and a closed parameter SET: a typo'd key now
// comes back as "unknown parameter" instead of silently creating a rule that
// ignores half the request.
func firewallRuleBody(optional bool) apischema.Properties {
	return apischema.Properties{
		"type": {
			Type:        apischema.String,
			Optional:    optional,
			MaxLength:   apischema.Ptr(32),
			Typetext:    "<in|out|group>",
			Description: "Direction the rule matches, or group to include a security group.",
		},
		"action": {
			Type:      apischema.String,
			Optional:  optional,
			MaxLength: apischema.Ptr(64),
			Typetext:  "<ACCEPT|DROP|REJECT|group name>",
			Description: "What to do with a matching packet, or the security group to include when " +
				"type is group.",
		},
		"source":  optString(512, "<address>", "Source address, CIDR, alias or IP set. Empty matches any."),
		"dest":    optString(512, "<address>", "Destination address, CIDR, alias or IP set. Empty matches any."),
		"sport":   optString(128, "<port|range|list>", "Source port, range or comma-separated list. Empty matches any."),
		"dport":   optString(128, "<port|range|list>", "Destination port, range or comma-separated list. Empty matches any."),
		"proto":   optString(64, "<protocol>", "IP protocol name or number. Empty matches any."),
		"macro":   optString(128, "<macro>", "Proxmox firewall macro to expand into this rule, e.g. SSH."),
		"comment": optString(512, "<string>", "Free-text note stored with the rule."),
		"log":     optString(32, "<nolog|emerg|alert|crit|err|warning|notice|info|debug>", "Log level for matching packets."),
		"iface":   optString(64, "<interface>", "Network interface the rule applies to. Empty applies it to all."),
		"enable": {
			Type:     apischema.Integer,
			Optional: true,
			// An INTEGER rather than a boolean, because that is what the wire
			// shape has always been and what the rule list returns: Proxmox
			// treats the field as a counter, where 0 disables and anything
			// higher enables. Declaring it as a boolean would reject the value
			// the listing hands back, which is what an edit form sends
			// straight back in.
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			Maximum:  apischema.Ptr(2147483647.0),
			Typetext: "<integer>",
			Description: "0 disables the rule; any higher value enables it. Unlike every other field " +
				"here it is ALWAYS sent to Proxmox (firewallRuleToForm), so omitting it on an update " +
				"disables the rule rather than leaving it as it was.",
		},
	}
}

// firewallOptionsParams is the PUT body for the cluster firewall options.
//
// Every field is optional, which is what the handler did (it checked none)
// AND what the options card relies on: it sends `{"policy_in": "DROP"}` on
// its own, and `{"enable": 0}` on its own. A required field here would break
// both.
func firewallOptionsParams() apischema.Properties {
	return apischema.Properties{
		"enable": {
			Type:     apischema.Integer,
			Optional: true,
			// NO Default, and that is load-bearing: proxmox.FirewallOptions
			// carries *int and firewallOptionsToForm sends the key only when
			// it is non-nil, so "the caller never mentioned enable" and "the
			// caller sent 0" are different requests. A Default would collapse
			// them and start writing enable=0 into the cluster firewall on
			// every policy-only save — i.e. silently disabling the firewall.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1.0),
			Typetext:    "<integer>",
			Description: "1 turns the cluster firewall on, 0 off. Omitted leaves it as it is.",
		},
		"policy_in":     optString(32, "<ACCEPT|REJECT|DROP>", "Default policy for inbound traffic. Empty or omitted leaves it as it is."),
		"policy_out":    optString(32, "<ACCEPT|REJECT|DROP>", "Default policy for outbound traffic. Empty or omitted leaves it as it is."),
		"log_level_in":  optString(32, "<nolog|emerg|alert|crit|err|warning|notice|info|debug>", "Log level for inbound traffic the default policy handles."),
		"log_level_out": optString(32, "<nolog|emerg|alert|crit|err|warning|notice|info|debug>", "Log level for outbound traffic the default policy handles."),
	}
}

// firewallIPSetEntryCIDRParam is an IP set entry's address, as a PATH
// parameter.
//
// It carries a PATTERN rather than the cidr format, and the reason is what
// actually arrives: Fiber runs with UnescapePath at its default of false, so
// c.Params hands back the RAW segment — the client percent-encodes the
// slash, and the handler sees "192.0.2.0%2F24" rather than "192.0.2.0/24".
// The cidr format would reject that outright and turn a call that reaches
// Proxmox today into a 400.
//
// (That double encoding is a real defect on this route — the client escapes
// the value a second time on the way out, so Proxmox is asked for an entry
// whose id contains a literal "%2F" — but it is a pre-existing one, and
// fixing it means changing what the route accepts. Flagged for separate
// scoping rather than smuggled into a migration.)
//
// The leading character class excludes "." so that "." and ".." cannot get
// through; proxmox.DeleteFirewallIPSetEntry still runs validatePathSegment,
// and its own comment says why — cidr=".." addresses the IP SET, deleting
// every entry while the audit row still reads as a single-entry change.
var firewallIPSetEntryCIDRParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     `^[0-9A-Za-z:%][0-9A-Za-z.:%_-]*$`,
	MaxLength:   apischema.Ptr(64),
	Typetext:    "<ip|cidr>",
	Description: "Entry to remove, exactly as the entry listing reports it. Percent-encode the slash of a CIDR.",
}

// registerFirewallEndpoints declares the 28 firewall routes served by
// NetworkHandler.
//
// Every one of them is the uniform shape: resolve the cluster from the path,
// then one static requireClusterPerm. So all 28 declare a plain
// cluster-scoped Check — nothing here is Deferred, Advisory, Public,
// SelfService or global.
//
// Two things about the permissions are worth naming because neither is the
// obvious one, and both are preserved rather than chosen:
//
//   - The resource is :network throughout, and as of the repoint in
//     registry_nodes.go so are the five node firewall routes, which asked
//     for a :firewall resource the permission catalogue never seeded and
//     so answered 403 to everyone for six months. There is no longer a
//     split: every firewall route in the API gates on :network, which is
//     what the catalogue entry describes ("View networks, firewall, SDN").
//     The tally below still records each route's permission so a future
//     divergence is a decision rather than a drift.
//   - delete:network is used by exactly two of the twenty-eight — removing a
//     cluster rule and removing a guest rule. Deleting an ALIAS, an IP SET,
//     an IP set ENTRY, a SECURITY GROUP or a security-group RULE is
//     manage:network. That is not a pattern, it is the shape the handlers
//     had, and the tally freezes it.
func registerFirewallEndpoints(reg *Registry, h *handlers.NetworkHandler) {
	// ── Cluster firewall rules ────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/rules",
		Description: "List the cluster-wide firewall rules in evaluation order.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListClusterFirewallRules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallScope + "/rules",
		Description: "Add a cluster-wide firewall rule, at the top of the list.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(firewallRuleBody(false)),
		Handler:     h.CreateClusterFirewallRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   firewallScope + "/rules/:pos",
		Description: "Change one cluster-wide firewall rule. A field left out is not sent, so Proxmox " +
			"keeps its current value — except enable, which is always written and therefore resets to 0 " +
			"when the body omits it.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(withParams(firewallRuleBody(true), apischema.Properties{"pos": firewallRulePosParam})),
		Handler:     h.UpdateClusterFirewallRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        firewallScope + "/rules/:pos",
		Description: "Delete one cluster-wide firewall rule by position. Every rule below it moves up.",
		Group:       "Firewall",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"pos": firewallRulePosParam}),
		Handler:     h.DeleteClusterFirewallRule,
	})

	// ── Cluster firewall options ──────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/options",
		Description: "Read the cluster firewall's master switch, default policies and log levels.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetFirewallOptions,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   firewallScope + "/options",
		Description: "Change the cluster firewall's options. Every field is optional and a field left " +
			"out is not sent, so a body carrying one key changes only that setting.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(firewallOptionsParams()),
		Handler:     h.SetFirewallOptions,
	})

	// ── Per-guest firewall rules ──────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   guestFirewallScope + "/rules",
		Description: "List one guest's firewall rules in evaluation order. vm_id is the Proxmox VMID " +
			"here, not the Nexara uuid the other /vms routes take.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(apischema.Properties{"vm_id": firewallVMIDParam}),
		Handler:     h.ListVMFirewallRules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        guestFirewallScope + "/rules",
		Description: "Add a firewall rule to one guest, at the top of its list.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(firewallRuleBody(false),
			apischema.Properties{"vm_id": firewallVMIDParam})),
		Handler: h.CreateVMFirewallRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   guestFirewallScope + "/rules/:pos",
		Description: "Change one of a guest's firewall rules. A field left out keeps its current value; " +
			"enable is always written and resets to 0 when the body omits it.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(firewallRuleBody(true), apischema.Properties{
			"vm_id": firewallVMIDParam,
			"pos":   firewallRulePosParam,
		})),
		Handler: h.UpdateVMFirewallRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        guestFirewallScope + "/rules/:pos",
		Description: "Delete one of a guest's firewall rules by position. Every rule below it moves up.",
		Group:       "Firewall",
		Permissions: clusterCheck("delete", "network"),
		Parameters: clusterParams(apischema.Properties{
			"vm_id": firewallVMIDParam,
			"pos":   firewallRulePosParam,
		}),
		Handler: h.DeleteVMFirewallRule,
	})

	// ── Aliases ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/aliases",
		Description: "List the cluster's firewall aliases — named addresses a rule can refer to.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListFirewallAliases,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallScope + "/aliases",
		Description: "Create a firewall alias. A name already in use comes back as a conflict, not a gateway error.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"name": pveObjectNameParam("Name for the alias, as a rule will refer to it."),
			"cidr": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(64),
				Typetext:  "<ip|cidr>",
				// No cidr FORMAT: Proxmox accepts a bare address here as well
				// as a prefixed one, and netip.ParsePrefix refuses the first.
				Description: "Address or network the alias stands for.",
			},
			"comment": optString(512, "<string>", "Free-text note stored with the alias."),
			// `rename` is deliberately NOT declared on create. It is a field
			// of proxmox.FirewallAliasParams, so c.Bind().Body accepted it
			// before this migration — and CreateFirewallAlias never read it,
			// so sending it did nothing at all. Declaring it would document a
			// parameter with no effect; leaving it out turns the same request
			// into a 400 that says so.
		}),
		Handler: h.CreateFirewallAlias,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   firewallScope + "/aliases/:name",
		Description: "Change a firewall alias, or rename it. cidr is optional here because the handler " +
			"never required it — Proxmox answers for an empty one.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"name": pveObjectNameParam("Alias to change."),
			// Optional, pinned to what UpdateFirewallAlias enforced: nothing.
			// It is still always SENT (firewall client), so an omitted cidr
			// reaches Proxmox as an empty one and Proxmox rejects it — the
			// same answer a caller got before. Making it required here would
			// be tidier and would move a rejection the handler never made.
			"cidr":    optString(64, "<ip|cidr>", "New address or network. Proxmox requires one, so omitting it is rejected there."),
			"comment": optString(512, "<string>", "Free-text note stored with the alias."),
			"rename":  pveObjectNameParam("New name for the alias.").AsOptional(),
			// `name` is NOT declared as a body field even though
			// proxmox.FirewallAliasParams carries one: the handler reads the
			// alias from the PATH and ignores the body's copy, so a body name
			// that disagreed with the path used to be silently dropped.
		}),
		Handler: h.UpdateFirewallAlias,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   firewallScope + "/aliases/:name",
		Description: "Delete a firewall alias. manage:network rather than delete:network — that is what " +
			"the handler enforced.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(apischema.Properties{"name": pveObjectNameParam("Alias to delete.")}),
		Handler:     h.DeleteFirewallAlias,
	})

	// ── IP sets ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/ipset",
		Description: "List the cluster's firewall IP sets.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListFirewallIPSets,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallScope + "/ipset",
		Description: "Create an empty firewall IP set. A name already in use comes back as a conflict.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"name":    pveObjectNameParam("Name for the IP set, as a rule will refer to it."),
			"comment": optString(512, "<string>", "Free-text note stored with the IP set."),
		}),
		Handler: h.CreateFirewallIPSet,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        firewallScope + "/ipset/:name",
		Description: "Delete a firewall IP set and every entry in it. manage:network, not delete:network.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(apischema.Properties{"name": pveObjectNameParam("IP set to delete.")}),
		Handler:     h.DeleteFirewallIPSet,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/ipset/:name/entries",
		Description: "List the addresses in one firewall IP set.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(apischema.Properties{"name": pveObjectNameParam("IP set to read.")}),
		Handler:     h.ListFirewallIPSetEntries,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallScope + "/ipset/:name/entries",
		Description: "Add an address to a firewall IP set.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"name": pveObjectNameParam("IP set to add to."),
			"cidr": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(64),
				Typetext:  "<ip|cidr>",
				// No cidr FORMAT, for the reason the alias body gives: a bare
				// address is legal here and netip.ParsePrefix refuses one.
				Description: "Address or network to add.",
			},
			"nomatch": {
				Type:     apischema.Integer,
				Optional: true,
				// NO Default, for the reason the firewall options enable
				// gives: proxmox.FirewallIPSetEntryParams carries *int and
				// the key is sent only when the caller chose, so "omitted"
				// and "0" are different requests.
				Minimum:     apischema.Ptr(0.0),
				Maximum:     apischema.Ptr(1.0),
				Typetext:    "<integer>",
				Description: "1 makes the entry an EXCLUSION from the set. Omitted leaves Proxmox's default.",
			},
			"comment": optString(512, "<string>", "Free-text note stored with the entry."),
		}),
		Handler: h.AddFirewallIPSetEntry,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        firewallScope + "/ipset/:name/entries/:cidr",
		Description: "Remove one address from a firewall IP set. manage:network, not delete:network.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"name": pveObjectNameParam("IP set to remove from."),
			"cidr": firewallIPSetEntryCIDRParam,
		}),
		Handler: h.DeleteFirewallIPSetEntry,
	})

	// ── Security groups ───────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/groups",
		Description: "List the cluster's firewall security groups — named rule bundles a rule can include.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSecurityGroups,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        firewallScope + "/groups",
		Description: "Create an empty security group. A name already in use comes back as a conflict.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"group":   pveObjectNameParam("Name for the security group, as a rule will refer to it."),
			"comment": optString(512, "<string>", "Free-text note stored with the group."),
		}),
		Handler: h.CreateSecurityGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        firewallScope + "/groups/:group",
		Description: "Delete a security group and every rule in it. manage:network, not delete:network.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(apischema.Properties{"group": pveObjectNameParam("Security group to delete.")}),
		Handler:     h.DeleteSecurityGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallScope + "/groups/:group/rules",
		Description: "List the rules inside one security group, in evaluation order.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(apischema.Properties{"group": pveObjectNameParam("Security group to read.")}),
		Handler:     h.ListSecurityGroupRules,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   firewallScope + "/groups/:group/rules",
		Description: "Add a rule to a security group. type and action are OPTIONAL here, unlike the " +
			"cluster and node rule creates: this handler never required them and Proxmox answers for " +
			"what it gets.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(firewallRuleBody(true), apischema.Properties{
			"group": pveObjectNameParam("Security group to add to."),
		})),
		Handler: h.CreateSecurityGroupRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   firewallScope + "/groups/:group/rules/:pos",
		Description: "Change one rule inside a security group. A field left out keeps its current value; " +
			"enable is always written and resets to 0 when the body omits it.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(firewallRuleBody(true), apischema.Properties{
			"group": pveObjectNameParam("Security group the rule lives in."),
			"pos":   firewallRulePosParam,
		})),
		Handler: h.UpdateSecurityGroupRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        firewallScope + "/groups/:group/rules/:pos",
		Description: "Delete one rule from a security group. manage:network, not delete:network.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"group": pveObjectNameParam("Security group the rule lives in."),
			"pos":   firewallRulePosParam,
		}),
		Handler: h.DeleteSecurityGroupRule,
	})

	// ── Log ───────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   firewallScope + "/log",
		Description: "Read one node's firewall log. The firewall is cluster-wide but the log is not, so " +
			"node says which host's to read and is required.",
		Group:       "Firewall",
		Permissions: clusterCheck("view", "network"),
		Parameters: clusterParams(apischema.Properties{
			// Required, which is what the handler enforced — it answered 400
			// for an absent ?node=. The node-name format additionally rejects
			// the empty string, which c.Query could not tell from absent.
			"node": apischema.StdOption("node-name"),
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  500,
				// The floor is 0 rather than 1 because 0 was always
				// REACHABLE: c.Query's "500" default only applied to an
				// absent key, so ?limit=0 has been a working request. It does
				// not mean "no limit" — proxmox.GetNodeFirewallLog writes the
				// key only when it is positive, so 0 omits it and Proxmox
				// applies its own default. What the floor closes is a
				// NEGATIVE limit, which strconv.Atoi passed straight through.
				Minimum: apischema.Ptr(0.0),
				// No Maximum, and that DIFFERS from the node firewall log
				// route on purpose: that handler clamped at 5000, so its
				// declaration states the clamp as a bound. This one never
				// clamped, and inventing a ceiling here would 400 a caller
				// who has been reading bigger pages happily.
				Typetext:    "<integer>",
				Description: "Maximum entries to return. 0 or omitted leaves Proxmox's own default.",
			},
			"start": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  0,
				Minimum:  apischema.Ptr(0.0),
				// No Maximum, for the reason the node syslog offset gives:
				// paging depth was never bounded and a ceiling would break a
				// working caller.
				Typetext:    "<integer>",
				Description: "Line offset into the log.",
			},
		}),
		Handler: h.GetFirewallLog,
	})
}
