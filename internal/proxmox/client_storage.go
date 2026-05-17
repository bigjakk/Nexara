package proxmox

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
)

// ociReferencePattern is a pragmatic validator for Docker/OCI image references.
// We deliberately keep it looser than the upstream Proxmox regex — Proxmox will
// reject precise edge cases with a clear error; our job is to catch obvious garbage
// before a round-trip. Required: 1–512 chars, alphanumerics plus `._-/:@`.
var ociReferencePattern = regexp.MustCompile(`^[A-Za-z0-9._\-/:@]+$`)

// vztmplFilenamePattern restricts optional OCI output filenames to a safe basename.
// Server appends ".tar" itself, so we forbid extensions and path separators.
var vztmplFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9._\-]+$`)

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
// PullOCIImage triggers POST /nodes/{node}/storage/{storage}/oci-registry-pull.
// Available in Proxmox VE 9.1+. The storage must be file-based with vztmpl content
// enabled, and skopeo must be installed on the node.
//
// Returns the UPID of the async worker task that runs the skopeo pull.
func (c *Client) PullOCIImage(ctx context.Context, node, storage string, params OCIPullParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if storage == "" {
		return "", fmt.Errorf("storage name is required")
	}
	if params.Reference == "" {
		return "", fmt.Errorf("OCI image reference is required")
	}
	if len(params.Reference) > 512 {
		return "", fmt.Errorf("OCI reference exceeds 512 characters")
	}
	if !ociReferencePattern.MatchString(params.Reference) {
		return "", fmt.Errorf("OCI reference contains invalid characters")
	}
	if params.FileName != "" {
		if len(params.FileName) > 64 {
			return "", fmt.Errorf("filename exceeds 64 characters")
		}
		if !vztmplFilenamePattern.MatchString(params.FileName) {
			return "", fmt.Errorf("filename contains invalid characters")
		}
	}

	form := url.Values{}
	form.Set("reference", params.Reference)
	if params.FileName != "" {
		// Upstream parameter is `filename` (no separator). See:
		// https://lore.proxmox.com/pve-devel/20251117171528.262443-4-f.schauer@proxmox.com/
		form.Set("filename", params.FileName)
	}

	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/oci-registry-pull"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("pull OCI image %s to %s/%s: %w", params.Reference, node, storage, err)
	}
	return upid, nil
}

// DownloadURLToStorage triggers POST /nodes/{node}/storage/{storage}/download-url
// to fetch an arbitrary URL into the storage as `iso`, `vztmpl`, or `import` content.
// Returns the UPID of the async download worker.
func (c *Client) DownloadURLToStorage(ctx context.Context, node, storage string, params URLDownloadParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if storage == "" {
		return "", fmt.Errorf("storage name is required")
	}
	if params.URL == "" {
		return "", fmt.Errorf("URL is required")
	}
	if len(params.URL) > 2048 {
		return "", fmt.Errorf("URL exceeds 2048 characters")
	}
	switch params.Content {
	case "iso", "vztmpl", "import":
	default:
		return "", fmt.Errorf("content must be iso, vztmpl, or import (got %q)", params.Content)
	}
	if params.Filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	if len(params.Filename) > 255 {
		return "", fmt.Errorf("filename exceeds 255 characters")
	}
	// Disallow path separators and parent-dir references.
	if invalidFilename(params.Filename) {
		return "", fmt.Errorf("filename must not contain path separators or '..'")
	}

	form := url.Values{}
	form.Set("url", params.URL)
	form.Set("content", params.Content)
	form.Set("filename", params.Filename)
	if params.Checksum != "" {
		form.Set("checksum", params.Checksum)
		if params.ChecksumAlgorithm == "" {
			return "", fmt.Errorf("checksum-algorithm is required when checksum is set")
		}
		form.Set("checksum-algorithm", params.ChecksumAlgorithm)
	}
	if params.DecompressionAlgorithm != "" {
		form.Set("compression", params.DecompressionAlgorithm)
	}
	if params.VerifyCertificates != nil {
		if *params.VerifyCertificates {
			form.Set("verify-certificates", "1")
		} else {
			form.Set("verify-certificates", "0")
		}
	}

	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/download-url"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("download URL to %s/%s: %w", node, storage, err)
	}
	return upid, nil
}

// GetAppliances returns the Proxmox appliance catalog from GET /nodes/{node}/aplinfo.
// This lists official LXC templates (Debian/Ubuntu/Alpine/Turnkey/...).
func (c *Client) GetAppliances(ctx context.Context, node string) ([]ApplianceTemplate, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var entries []ApplianceTemplate
	path := "/nodes/" + url.PathEscape(node) + "/aplinfo"
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get appliance catalog on %s: %w", node, err)
	}
	return entries, nil
}

// DownloadAppliance triggers POST /nodes/{node}/aplinfo to download a specific
// appliance template (identified by the `template` field from GetAppliances) into
// the named storage. Returns the UPID of the async download worker.
func (c *Client) DownloadAppliance(ctx context.Context, node, storage, template string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if storage == "" {
		return "", fmt.Errorf("storage name is required")
	}
	if template == "" {
		return "", fmt.Errorf("template name is required")
	}
	if len(template) > 255 {
		return "", fmt.Errorf("template name exceeds 255 characters")
	}

	form := url.Values{}
	form.Set("storage", storage)
	form.Set("template", template)

	path := "/nodes/" + url.PathEscape(node) + "/aplinfo"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("download appliance %s to %s/%s: %w", template, node, storage, err)
	}
	return upid, nil
}

// invalidFilename returns true if the filename contains path separators or parent refs.
func invalidFilename(name string) bool {
	if name == "" || name == "." || name == ".." {
		return true
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' || name[i] == '\\' {
			return true
		}
	}
	// Disallow "../" sneaking through as a substring on platforms with weird encodings.
	for i := 0; i+1 < len(name); i++ {
		if name[i] == '.' && name[i+1] == '.' {
			return true
		}
	}
	return false
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
