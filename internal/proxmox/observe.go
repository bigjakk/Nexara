package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ObserveTarget is one read the change observer makes
// (internal/collector/observer.go): a listing, or one object's configuration,
// whose answer it compares field by field with the last one. It reads raw JSON
// rather than through the typed getters because a getter sees only the fields
// Nexara decodes, and an edit made in Proxmox to any other field is exactly what
// the observer exists to catch. An update handler reads its object so too just
// before it writes it, to record which fields its write changes (the Object*
// targets; handlers' auditSent.changed).
//
// A target is built only by the constructors below, which validate every
// caller-supplied path segment the way the client's own getters do
// (validatePathSegment), so no caller can name an arbitrary path.
type ObserveTarget struct {
	path string
	node string // the node a read under /nodes/{node} is proxied to; "" for the cluster's own
}

// String is the request path the target reads, query included.
func (t ObserveTarget) String() string { return t.path }

// Node is the node the read is answered by, through the endpoint that proxies
// it: "" for a read the endpoint answers itself.
func (t ObserveTarget) Node() string { return t.node }

// ObservePermissionsRoot reads the cluster token's own privileges on "/". It
// answers for any valid token (user => 'all'), so it doubles as the pass's
// reachability probe.
func ObservePermissionsRoot() ObserveTarget {
	return ObserveTarget{path: "/access/permissions?" + url.Values{"path": {"/"}}.Encode()}
}

// ObservePools lists the resource pools the token may audit (poolid, comment).
func ObservePools() ObserveTarget { return ObserveTarget{path: "/pools"} }

// ObservePool reads one pool with its members. The query form, not the
// deprecated /pools/{poolid}, because a nested pool's id carries a "/".
func ObservePool(poolID string) (ObserveTarget, error) {
	if poolID == "" || hasControlChar(poolID) {
		return ObserveTarget{}, fmt.Errorf("%w: pool id %q", ErrInvalidInput, poolID)
	}
	return ObserveTarget{path: "/pools?" + url.Values{"poolid": {poolID}}.Encode()}, nil
}

// ObserveStorage lists the storage definitions (storage.cfg).
func ObserveStorage() ObserveTarget { return ObserveTarget{path: "/storage"} }

// ObserveBackupJobs lists the scheduled backup jobs (jobs.cfg).
func ObserveBackupJobs() ObserveTarget { return ObserveTarget{path: "/cluster/backup"} }

// ObserveUSBMappings and ObservePCIMappings list the resource mappings, without
// check-node: the per-node checks it adds are run state, not configuration.
func ObserveUSBMappings() ObserveTarget { return ObserveTarget{path: "/cluster/mapping/usb"} }

// ObservePCIMappings: see ObserveUSBMappings.
func ObservePCIMappings() ObserveTarget { return ObserveTarget{path: "/cluster/mapping/pci"} }

// ObserveMetricServers lists the metric servers, five fields each.
func ObserveMetricServers() ObserveTarget { return ObserveTarget{path: "/cluster/metrics/server"} }

// ObserveMetricServer reads one metric server's whole configuration. Its token
// is kept in /etc/pve/priv and never returned.
func ObserveMetricServer(id string) (ObserveTarget, error) {
	if err := validatePathSegment("metric server id", id); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/metrics/server/" + url.PathEscape(id)}, nil
}

// ObserveClusterOptions reads datacenter.cfg. Without Sys.Audit on "/" Proxmox
// answers with two of its keys only, which the caller must not take for the
// whole file.
func ObserveClusterOptions() ObserveTarget { return ObserveTarget{path: "/cluster/options"} }

// ObserveHAResources, ObserveHARules and ObserveHAGroups list the HA
// configuration. Proxmox VE 8 has no rules (501); Proxmox VE 9 answers the
// groups read with an error once its groups have been migrated to rules.
func ObserveHAResources() ObserveTarget { return ObserveTarget{path: "/cluster/ha/resources"} }

// ObserveHARules: see ObserveHAResources.
func ObserveHARules() ObserveTarget { return ObserveTarget{path: "/cluster/ha/rules"} }

// ObserveHAGroups: see ObserveHAResources.
func ObserveHAGroups() ObserveTarget { return ObserveTarget{path: "/cluster/ha/groups"} }

// ObserveSDN lists one kind of the SDN configuration — zones, vnets,
// controllers, ipams or dns — in its plain view: the configuration as edited,
// which an apply copies to the running one (neither running nor pending is
// asked for).
func ObserveSDN(kind string) (ObserveTarget, error) {
	switch kind {
	case "zones", "vnets", "controllers", "ipams", "dns":
		return ObserveTarget{path: "/cluster/sdn/" + kind}, nil
	}
	return ObserveTarget{}, fmt.Errorf("%w: SDN kind %q", ErrInvalidInput, kind)
}

// ObjectBackupJob, ObjectNodeNetworkInterface, ObjectSDN, ObjectSDNSubnet
// and ObjectACMEPlugin read the one object an update is about to write (one
// metric server's is ObserveMetricServer).
func ObjectBackupJob(id string) (ObserveTarget, error) {
	if err := validatePathSegment("backup job ID", id); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/backup/" + url.PathEscape(id)}, nil
}

func ObjectNodeNetworkInterface(node, iface string) (ObserveTarget, error) {
	if err := validatePathSegment("interface name", iface); err != nil {
		return ObserveTarget{}, err
	}
	return nodeTarget(node, "/network/"+url.PathEscape(iface))
}

func ObjectSDN(kind, id string) (ObserveTarget, error) {
	t, err := ObserveSDN(kind)
	if err != nil {
		return ObserveTarget{}, err
	}
	if err := validatePathSegment("SDN "+kind+" id", id); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: t.path + "/" + url.PathEscape(id)}, nil
}

func ObjectSDNSubnet(vnet, subnet string) (ObserveTarget, error) {
	t, err := ObserveSDNSubnets(vnet)
	if err != nil {
		return ObserveTarget{}, err
	}
	if err := validatePathSegment("subnet", subnet); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: t.path + "/" + url.PathEscape(subnet)}, nil
}

func ObjectACMEPlugin(id string) (ObserveTarget, error) {
	if err := validatePathSegment("ACME plugin id", id); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/acme/plugins/" + url.PathEscape(id)}, nil
}

// ObserveSDNSubnets lists one vnet's subnets.
func ObserveSDNSubnets(vnet string) (ObserveTarget, error) {
	if err := validatePathSegment("vnet", vnet); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"}, nil
}

// ObserveFirewall reads one part of the cluster firewall: its options, rules,
// aliases, IP sets or security groups.
func ObserveFirewall(part string) (ObserveTarget, error) {
	switch part {
	case "options", "rules", "aliases", "ipset", "groups":
		return ObserveTarget{path: "/cluster/firewall/" + part}, nil
	}
	return ObserveTarget{}, fmt.Errorf("%w: firewall part %q", ErrInvalidInput, part)
}

// ObserveFirewallIPSet lists one IP set's entries.
func ObserveFirewallIPSet(name string) (ObserveTarget, error) {
	if err := validatePathSegment("IP set", name); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/firewall/ipset/" + url.PathEscape(name)}, nil
}

// ObserveFirewallGroup lists one security group's rules, in order.
func ObserveFirewallGroup(group string) (ObserveTarget, error) {
	if err := validatePathSegment("security group", group); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{path: "/cluster/firewall/groups/" + url.PathEscape(group)}, nil
}

// ObserveAccessUsers lists the users with their groups and API tokens
// (full=1), which spares a token listing per user. A token's secret is never
// returned.
func ObserveAccessUsers() ObserveTarget {
	return ObserveTarget{path: "/access/users?" + url.Values{"full": {"1"}}.Encode()}
}

// ObserveAccess lists the groups, the roles or the ACL.
func ObserveAccess(part string) (ObserveTarget, error) {
	switch part {
	case "groups", "roles", "acl":
		return ObserveTarget{path: "/access/" + part}, nil
	}
	return ObserveTarget{}, fmt.Errorf("%w: access part %q", ErrInvalidInput, part)
}

// ObserveACMEAccounts lists the ACME accounts by name: an account's details
// are root@pam's alone, which no API token is.
func ObserveACMEAccounts() ObserveTarget { return ObserveTarget{path: "/cluster/acme/account"} }

// ObserveACMEPlugins lists the ACME challenge plugins, their DNS API
// credentials among them; it needs Sys.Modify on "/".
func ObserveACMEPlugins() ObserveTarget { return ObserveTarget{path: "/cluster/acme/plugins"} }

// ObserveNodeNetwork lists a node's network interfaces as they will be after
// the pending changes are applied (interfaces.new, when there is one).
func ObserveNodeNetwork(node string) (ObserveTarget, error) {
	return nodeTarget(node, "/network")
}

// ObserveNodeConfig reads a node's own config file: its options, notes and ACME
// settings.
func ObserveNodeConfig(node string) (ObserveTarget, error) {
	return nodeTarget(node, "/config")
}

// ObserveNodeDNS reads a node's DNS settings.
func ObserveNodeDNS(node string) (ObserveTarget, error) {
	return nodeTarget(node, "/dns")
}

// ObserveNodeTime reads a node's time zone, beside its clock.
func ObserveNodeTime(node string) (ObserveTarget, error) {
	return nodeTarget(node, "/time")
}

// ObserveNodeFirewallRules lists a node's firewall rules, in order.
func ObserveNodeFirewallRules(node string) (ObserveTarget, error) {
	return nodeTarget(node, "/firewall/rules")
}

// ObserveGuestConfig reads a guest's configuration with its pending changes
// applied — GET .../config WITHOUT current=1. An edit that has to wait for a
// restart is pending until then; read with current=1 it would surface at the
// restart, inside a start task's window, and look explained.
func ObserveGuestConfig(node, guestType string, vmid int) (ObserveTarget, error) {
	if err := validateNodeName(node); err != nil {
		return ObserveTarget{}, err
	}
	if err := validateVMID(vmid); err != nil {
		return ObserveTarget{}, err
	}
	if guestType != "qemu" && guestType != "lxc" {
		return ObserveTarget{}, fmt.Errorf("%w: guest type %q", ErrInvalidInput, guestType)
	}
	return ObserveTarget{"/nodes/" + url.PathEscape(node) + "/" + guestType + "/" + strconv.Itoa(vmid) + "/config", node}, nil
}

func nodeTarget(node, rest string) (ObserveTarget, error) {
	if err := validateNodeName(node); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{"/nodes/" + url.PathEscape(node) + rest, node}, nil
}

// ObserveRead GETs the target and returns Proxmox's `data` exactly as it was
// sent. A target no constructor built is refused without a request.
func (c *Client) ObserveRead(ctx context.Context, t ObserveTarget) (json.RawMessage, error) {
	if t.path == "" {
		return nil, fmt.Errorf("%w: empty observe target", ErrInvalidInput)
	}
	var data json.RawMessage
	if err := c.do(ctx, t.path, &data); err != nil {
		return nil, fmt.Errorf("observe %s: %w", t.path, err)
	}
	return data, nil
}
