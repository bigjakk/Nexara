package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetCPUModels(ctx context.Context, node string) ([]CPUModel, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var models []CPUModel
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/cpu", &models); err != nil {
		return nil, fmt.Errorf("get CPU models on %s: %w", node, err)
	}
	return models, nil
}
func (c *Client) GetResourcePools(ctx context.Context) ([]ResourcePool, error) {
	var pools []ResourcePool
	if err := c.do(ctx, "/pools", &pools); err != nil {
		return nil, fmt.Errorf("get resource pools: %w", err)
	}
	return pools, nil
}
func (c *Client) CreateResourcePool(ctx context.Context, params CreatePoolParams) error {
	form := url.Values{}
	form.Set("poolid", params.PoolID)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/pools", form, nil); err != nil {
		return fmt.Errorf("create resource pool %s: %w", params.PoolID, err)
	}
	return nil
}
func (c *Client) GetResourcePool(ctx context.Context, poolID string) (*ResourcePoolDetail, error) {
	path := "/pools/" + url.PathEscape(poolID)
	var pool ResourcePoolDetail
	if err := c.do(ctx, path, &pool); err != nil {
		return nil, fmt.Errorf("get resource pool %s: %w", poolID, err)
	}
	return &pool, nil
}
func (c *Client) UpdateResourcePool(ctx context.Context, poolID string, params UpdatePoolParams) error {
	form := url.Values{}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.VMs != "" {
		form.Set("vms", params.VMs)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update resource pool %s: %w", poolID, err)
	}
	return nil
}
func (c *Client) DeleteResourcePool(ctx context.Context, poolID string) error {
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete resource pool %s: %w", poolID, err)
	}
	return nil
}
func (c *Client) GetMetricServers(ctx context.Context) ([]MetricServerConfig, error) {
	var servers []MetricServerConfig
	if err := c.do(ctx, "/cluster/metrics/server", &servers); err != nil {
		return nil, fmt.Errorf("get metric servers: %w", err)
	}
	return servers, nil
}
func (c *Client) CreateMetricServer(ctx context.Context, params CreateMetricServerParams) error {
	form := url.Values{}
	form.Set("type", params.Type)
	form.Set("server", params.Server)
	form.Set("port", strconv.Itoa(params.Port))
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	path := "/cluster/metrics/server/" + url.PathEscape(params.ID)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create metric server %s: %w", params.ID, err)
	}
	return nil
}
func (c *Client) GetMetricServer(ctx context.Context, id string) (*MetricServerConfig, error) {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	var server MetricServerConfig
	if err := c.do(ctx, path, &server); err != nil {
		return nil, fmt.Errorf("get metric server %s: %w", id, err)
	}
	return &server, nil
}
func (c *Client) UpdateMetricServer(ctx context.Context, id string, params UpdateMetricServerParams) error {
	form := url.Values{}
	if params.Server != "" {
		form.Set("server", params.Server)
	}
	if params.Port != nil {
		form.Set("port", strconv.Itoa(*params.Port))
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update metric server %s: %w", id, err)
	}
	return nil
}
func (c *Client) DeleteMetricServer(ctx context.Context, id string) error {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete metric server %s: %w", id, err)
	}
	return nil
}
