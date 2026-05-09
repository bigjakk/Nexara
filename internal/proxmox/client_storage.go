package proxmox

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

func (c *Client) GetStoragePools(ctx context.Context, node string) ([]StoragePool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []StoragePool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/storage", &pools); err != nil {
		return nil, fmt.Errorf("get storage pools on %s: %w", node, err)
	}
	return pools, nil
}
func (c *Client) GetStorageContent(ctx context.Context, node, storage string) ([]StorageContent, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	var items []StorageContent
	if err := c.do(ctx, path, &items); err != nil {
		return nil, fmt.Errorf("get storage content on %s/%s: %w", node, storage, err)
	}
	return items, nil
}
func (c *Client) UploadToStorage(ctx context.Context, node, storage, contentType, filename string, reader io.Reader, fileSize int64) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/upload"
	fields := map[string]string{
		"content": contentType,
	}
	var upid string
	if err := c.doMultipart(ctx, path, fields, "filename", filename, reader, fileSize, &upid); err != nil {
		return "", fmt.Errorf("upload to %s/%s: %w", node, storage, err)
	}
	return upid, nil
}
func (c *Client) DeleteStorageContent(ctx context.Context, node, storage, volume string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	// Volume IDs contain ":" (storage:path) — PathEscape would over-encode it.
	// Proxmox expects the volume as-is in the URL path.
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + volume
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete volume %s on %s/%s: %w", volume, node, storage, err)
	}
	return upid, nil
}
func (c *Client) GetStorageConfig(ctx context.Context, storage string) (*StorageConfig, error) {
	if storage == "" {
		return nil, fmt.Errorf("storage name is required")
	}
	var cfg StorageConfig
	if err := c.do(ctx, "/storage/"+url.PathEscape(storage), &cfg); err != nil {
		return nil, fmt.Errorf("get storage config %s: %w", storage, err)
	}
	return &cfg, nil
}
func (c *Client) ListStorageConfigs(ctx context.Context) ([]StorageConfig, error) {
	var cfgs []StorageConfig
	if err := c.do(ctx, "/storage", &cfgs); err != nil {
		return nil, fmt.Errorf("list storage configs: %w", err)
	}
	return cfgs, nil
}
func (c *Client) CreateStorage(ctx context.Context, params url.Values) error {
	if params.Get("storage") == "" {
		return fmt.Errorf("storage name is required")
	}
	if params.Get("type") == "" {
		return fmt.Errorf("storage type is required")
	}
	if err := c.doPost(ctx, "/storage", params, nil); err != nil {
		return fmt.Errorf("create storage %s: %w", params.Get("storage"), err)
	}
	return nil
}
func (c *Client) UpdateStorage(ctx context.Context, storage string, params url.Values) error {
	if storage == "" {
		return fmt.Errorf("storage name is required")
	}
	if err := c.doPut(ctx, "/storage/"+url.PathEscape(storage), params, nil); err != nil {
		return fmt.Errorf("update storage %s: %w", storage, err)
	}
	return nil
}
func (c *Client) DeleteStorage(ctx context.Context, storage string) error {
	if storage == "" {
		return fmt.Errorf("storage name is required")
	}
	if err := c.doDelete(ctx, "/storage/"+url.PathEscape(storage), nil); err != nil {
		return fmt.Errorf("delete storage %s: %w", storage, err)
	}
	return nil
}
func (c *Client) GetCephStatus(ctx context.Context, node string) (*CephStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var status CephStatus
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/status", &status); err != nil {
		return nil, fmt.Errorf("get ceph status on %s: %w", node, err)
	}
	return &status, nil
}
func (c *Client) GetCephOSDs(ctx context.Context, node string) (*CephOSDResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var resp CephOSDResponse
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/osd", &resp); err != nil {
		return nil, fmt.Errorf("get ceph osds on %s: %w", node, err)
	}
	return &resp, nil
}
func (c *Client) GetCephPools(ctx context.Context, node string) ([]CephPool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []CephPool
	nodePath := "/nodes/" + url.PathEscape(node)
	if err := c.do(ctx, nodePath+"/ceph/pool", &pools); err != nil {
		// Fall back to plural form.
		var pools2 []CephPool
		if err2 := c.do(ctx, nodePath+"/ceph/pools", &pools2); err2 != nil {
			return nil, fmt.Errorf("get ceph pools on %s: %w", node, err)
		}
		return pools2, nil
	}
	return pools, nil
}
func (c *Client) GetCephMonitors(ctx context.Context, node string) ([]CephMon, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	// Parse as raw JSON first since Proxmox versions differ in response shape.
	var raw []map[string]interface{}
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/mon", &raw); err != nil {
		return nil, fmt.Errorf("get ceph monitors on %s: %w", node, err)
	}

	mons := make([]CephMon, 0, len(raw))
	for _, entry := range raw {
		mon := CephMon{
			Name: stringVal(entry, "name"),
			Host: stringVal(entry, "host"),
			Addr: stringVal(entry, "addr"),
		}
		if r, ok := entry["rank"]; ok {
			if v, isFloat := r.(float64); isFloat {
				mon.Rank = FlexInt(int(v))
			}
		}
		mons = append(mons, mon)
	}
	return mons, nil
}
func (c *Client) GetCephFS(ctx context.Context, node string) ([]CephFS, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var fs []CephFS
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/fs", &fs); err != nil {
		return nil, fmt.Errorf("get ceph fs on %s: %w", node, err)
	}
	return fs, nil
}
func (c *Client) GetCephCrushRules(ctx context.Context, node string) ([]CephCrushRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var rules []CephCrushRule
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/rules", &rules); err != nil {
		return nil, fmt.Errorf("get ceph crush rules on %s: %w", node, err)
	}
	return rules, nil
}
func (c *Client) CreateCephPool(ctx context.Context, node string, params CephPoolCreateParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if params.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("size", strconv.Itoa(params.Size))
	form.Set("pg_num", strconv.Itoa(params.PGNum))
	if params.MinSize > 0 {
		form.Set("min_size", strconv.Itoa(params.MinSize))
	}
	if params.Application != "" {
		form.Set("application", params.Application)
	}
	if params.CrushRule != "" {
		form.Set("crush_rule_name", params.CrushRule)
	}
	if params.PGAutoScale != "" {
		form.Set("pg_autoscale_mode", params.PGAutoScale)
	}
	path := "/nodes/" + url.PathEscape(node) + "/ceph/pools"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create ceph pool %s on %s: %w", params.Name, node, err)
	}
	return nil
}
func (c *Client) DeleteCephPool(ctx context.Context, node, poolName string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/ceph/pools/" + url.PathEscape(poolName)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ceph pool %s on %s: %w", poolName, node, err)
	}
	return nil
}
