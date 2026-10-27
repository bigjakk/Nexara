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
// the observer exists to catch.
//
// A target is built only by the constructors below, which validate every
// caller-supplied path segment the way the client's own getters do
// (validatePathSegment), so no caller can name an arbitrary path.
type ObserveTarget struct {
	path string
}

// String is the request path the target reads, query included.
func (t ObserveTarget) String() string { return t.path }

// ObservePermissionsRoot reads the cluster token's own privileges on "/". It
// answers for any valid token (user => 'all'), so it doubles as the pass's
// reachability probe.
func ObservePermissionsRoot() ObserveTarget {
	return ObserveTarget{"/access/permissions?" + url.Values{"path": {"/"}}.Encode()}
}

// ObservePools lists the resource pools the token may audit (poolid, comment).
func ObservePools() ObserveTarget { return ObserveTarget{"/pools"} }

// ObservePool reads one pool with its members. The query form, not the
// deprecated /pools/{poolid}, because a nested pool's id carries a "/".
func ObservePool(poolID string) (ObserveTarget, error) {
	if poolID == "" || hasControlChar(poolID) {
		return ObserveTarget{}, fmt.Errorf("%w: pool id %q", ErrInvalidInput, poolID)
	}
	return ObserveTarget{"/pools?" + url.Values{"poolid": {poolID}}.Encode()}, nil
}

// ObserveStorage lists the storage definitions (storage.cfg).
func ObserveStorage() ObserveTarget { return ObserveTarget{"/storage"} }

// ObserveBackupJobs lists the scheduled backup jobs (jobs.cfg).
func ObserveBackupJobs() ObserveTarget { return ObserveTarget{"/cluster/backup"} }

// ObserveUSBMappings and ObservePCIMappings list the resource mappings, without
// check-node: the per-node checks it adds are run state, not configuration.
func ObserveUSBMappings() ObserveTarget { return ObserveTarget{"/cluster/mapping/usb"} }

// ObservePCIMappings: see ObserveUSBMappings.
func ObservePCIMappings() ObserveTarget { return ObserveTarget{"/cluster/mapping/pci"} }

// ObserveMetricServers lists the metric servers, five fields each.
func ObserveMetricServers() ObserveTarget { return ObserveTarget{"/cluster/metrics/server"} }

// ObserveMetricServer reads one metric server's whole configuration. Its token
// is kept in /etc/pve/priv and never returned.
func ObserveMetricServer(id string) (ObserveTarget, error) {
	if err := validatePathSegment("metric server id", id); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{"/cluster/metrics/server/" + url.PathEscape(id)}, nil
}

// ObserveClusterOptions reads datacenter.cfg. Without Sys.Audit on "/" Proxmox
// answers with two of its keys only, which the caller must not take for the
// whole file.
func ObserveClusterOptions() ObserveTarget { return ObserveTarget{"/cluster/options"} }

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
	return ObserveTarget{"/nodes/" + url.PathEscape(node) + "/" + guestType + "/" + strconv.Itoa(vmid) + "/config"}, nil
}

func nodeTarget(node, rest string) (ObserveTarget, error) {
	if err := validateNodeName(node); err != nil {
		return ObserveTarget{}, err
	}
	return ObserveTarget{"/nodes/" + url.PathEscape(node) + rest}, nil
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
