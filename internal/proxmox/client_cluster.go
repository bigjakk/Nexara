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
func (c *Client) GetClusterConfig(ctx context.Context) (*ClusterConfig, error) {
	var cfg ClusterConfig
	if err := c.do(ctx, "/cluster/config", &cfg); err != nil {
		return nil, fmt.Errorf("get cluster config: %w", err)
	}
	return &cfg, nil
}
func (c *Client) GetClusterJoinInfo(ctx context.Context) (*ClusterJoinInfo, error) {
	var info ClusterJoinInfo
	if err := c.do(ctx, "/cluster/config/join", &info); err != nil {
		return nil, fmt.Errorf("get cluster join info: %w", err)
	}
	return &info, nil
}
func (c *Client) GetCorosyncNodes(ctx context.Context) ([]CorosyncNode, error) {
	var nodes []CorosyncNode
	if err := c.do(ctx, "/cluster/config/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get corosync nodes: %w", err)
	}
	return nodes, nil
}
