package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetVMs(ctx context.Context, node string) ([]VirtualMachine, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var vms []VirtualMachine
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/qemu?full=1", &vms); err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", node, err)
	}
	for i := range vms {
		vms[i].Node = node
	}
	return vms, nil
}
func (c *Client) GetContainers(ctx context.Context, node string) ([]Container, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var cts []Container
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/lxc", &cts); err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", node, err)
	}
	for i := range cts {
		cts[i].Node = node
	}
	return cts, nil
}
func (c *Client) GetClusterResources(ctx context.Context, resourceType string) ([]ClusterResource, error) {
	path := "/cluster/resources"
	if resourceType != "" {
		q := url.Values{}
		q.Set("type", resourceType)
		path += "?" + q.Encode()
	}
	var resources []ClusterResource
	if err := c.do(ctx, path, &resources); err != nil {
		return nil, fmt.Errorf("get cluster resources: %w", err)
	}
	return resources, nil
}

// GetVersion returns the Proxmox VE release info from GET /version.
// Used to feature-gate version-dependent UI (OCI image support requires 9.1+).
func (c *Client) GetVersion(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.do(ctx, "/version", &v); err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}
	return &v, nil
}

func (c *Client) GetClusterStatus(ctx context.Context) ([]ClusterStatusEntry, error) {
	var entries []ClusterStatusEntry
	if err := c.do(ctx, "/cluster/status", &entries); err != nil {
		return nil, fmt.Errorf("get cluster status: %w", err)
	}
	return entries, nil
}
func (c *Client) GetMachineTypes(ctx context.Context, node string) ([]MachineType, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var types []MachineType
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/machines", &types); err != nil {
		return nil, fmt.Errorf("get machine types on %s: %w", node, err)
	}
	return types, nil
}
func (c *Client) GetClusterOptions(ctx context.Context) (*ClusterOptions, error) {
	var opts ClusterOptions
	if err := c.do(ctx, "/cluster/options", &opts); err != nil {
		return nil, fmt.Errorf("get cluster options: %w", err)
	}
	return &opts, nil
}
func (c *Client) SetClusterOptions(ctx context.Context, params UpdateClusterOptionsParams) error {
	form := url.Values{}
	if params.Console != nil {
		form.Set("console", *params.Console)
	}
	if params.Keyboard != nil {
		form.Set("keyboard", *params.Keyboard)
	}
	if params.Language != nil {
		form.Set("language", *params.Language)
	}
	if params.EmailFrom != nil {
		form.Set("email_from", *params.EmailFrom)
	}
	if params.HTTPProxy != nil {
		form.Set("http_proxy", *params.HTTPProxy)
	}
	if params.MacPrefix != nil {
		form.Set("mac_prefix", *params.MacPrefix)
	}
	if params.Migration != nil {
		form.Set("migration", *params.Migration)
	}
	if params.MigrationType != nil {
		form.Set("migration_type", *params.MigrationType)
	}
	if params.BWLimit != nil {
		form.Set("bwlimit", *params.BWLimit)
	}
	if params.NextID != nil {
		form.Set("next-id", *params.NextID)
	}
	if params.HA != nil {
		form.Set("ha", *params.HA)
	}
	if params.Fencing != nil {
		form.Set("fencing", *params.Fencing)
	}
	if params.CRS != nil {
		form.Set("crs", *params.CRS)
	}
	if params.MaxWorkers != nil {
		form.Set("max_workers", strconv.Itoa(*params.MaxWorkers))
	}
	if params.Description != nil {
		form.Set("description", *params.Description)
	}
	if params.RegisteredTags != nil {
		form.Set("registered-tags", *params.RegisteredTags)
	}
	if params.UserTagAccess != nil {
		form.Set("user-tag-access", *params.UserTagAccess)
	}
	if params.TagStyle != nil {
		form.Set("tag-style", *params.TagStyle)
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	if err := c.doPut(ctx, "/cluster/options", form, nil); err != nil {
		return fmt.Errorf("set cluster options: %w", err)
	}
	return nil
}

// GetClusterConfig returns the cluster's corosync configuration.
//
// It reads /cluster/config/JOIN, not /cluster/config, and that is the whole
// reason this is more than a one-line wrapper. /cluster/config is a
// directory index: PVE declares it "type": "array" with a child link on
// {name} (pve-docs/api-viewer), so it answers with a list of its own
// subpaths — [{name: "nodes"}, {name: "totem"}, …] — and carries no
// configuration at all. Decoding that array into a struct failed outright,
// which is why this endpoint answered 500 on every call it ever received.
//
// /cluster/config/join is the object that actually holds the corosync
// config: nodelist and totem, the two fields ClusterConfig declares.
//
// A node that is not in a cluster is not an error here. PVE raises 424 for
// it, and the honest answer is an empty configuration — see
// IsNotInClusterError. ClusterConfig.Version is never populated: the join
// object has no such field, and the index that might have had one has no
// fields at all.
func (c *Client) GetClusterConfig(ctx context.Context) (*ClusterConfig, error) {
	join, err := c.GetClusterJoinInfo(ctx)
	if err != nil {
		if IsNotInClusterError(err) {
			return &ClusterConfig{}, nil
		}
		return nil, fmt.Errorf("get cluster config: %w", err)
	}
	return &ClusterConfig{Nodes: join.NodeList, Totem: join.Totem}, nil
}

// GetClusterJoinInfo returns what a node needs to join this cluster: the
// corosync nodelist, the totem config, each node's certificate fingerprint
// and the config digest.
func (c *Client) GetClusterJoinInfo(ctx context.Context) (*ClusterJoinInfo, error) {
	var info ClusterJoinInfo
	if err := c.do(ctx, "/cluster/config/join", &info); err != nil {
		return nil, fmt.Errorf("get cluster join info: %w", err)
	}
	return &info, nil
}

// GetCorosyncNodes returns the corosync nodelist with each node's id, ring
// addresses and vote weight.
//
// /cluster/config/nodes is the source, and it is NOT the empty index its
// schema suggests. PVE declares the items as {"node": string}, but the
// handler is
//
//	return PVE::RESTHandler::hash_to_array($nodelist, 'node');
//
// and hash_to_array stamps the key onto each entry and pushes the WHOLE
// entry (pve-common, src/PVE/RESTHandler.pm), so what comes back is every
// corosync nodelist section — name, nodeid, quorum_votes, ring0_addr —
// with an extra "node" key. Four of the five columns the SPA renders.
//
// The fifth, pve_addr, exists only on /cluster/config/join, which adds it
// per node from remote_node_ip. So it is merged in as an ENRICHMENT: the
// nodelist read above is authoritative and a failed join is not allowed to
// fail this call, because /join raises 424 on a standalone node while
// /nodes answers [] — and a standalone node must reach the SPA's empty
// state, not an error banner. A missing pve_addr renders as a blank cell,
// which is exactly what it rendered as before this merge existed.
func (c *Client) GetCorosyncNodes(ctx context.Context) ([]CorosyncNode, error) {
	var nodes []CorosyncNode
	if err := c.do(ctx, "/cluster/config/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get corosync nodes: %w", err)
	}

	join, err := c.GetClusterJoinInfo(ctx)
	if err != nil {
		return nodes, nil
	}
	addrs := make(map[string]CorosyncNode, len(join.NodeList))
	for _, n := range join.NodeList {
		addrs[corosyncNodeName(n)] = n
	}
	for i := range nodes {
		enriched, ok := addrs[corosyncNodeName(nodes[i])]
		if !ok {
			continue
		}
		if nodes[i].PVEAddr == "" {
			nodes[i].PVEAddr = enriched.PVEAddr
		}
		if nodes[i].PVEFP == "" {
			nodes[i].PVEFP = enriched.PVEFP
		}
	}
	return nodes, nil
}

// corosyncNodeName is the key the two /cluster/config views are joined on.
// It prefers "name", the corosync section's own field, and falls back to
// "node", the id property hash_to_array stamps on — the index carries
// both, the join object carries only the first.
func corosyncNodeName(n CorosyncNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.Node
}
