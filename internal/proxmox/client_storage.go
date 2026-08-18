package proxmox

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	// Checked before a byte is streamed: a name that survives here but not
	// validateVolumeID would land a volume the delete endpoint cannot address.
	if err := ValidateStorageFilename(filename); err != nil {
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
	if err := validateVolumeID(volume); err != nil {
		return "", err
	}
	// Volume IDs contain ":" (storage:path) — PathEscape would over-encode it.
	// Proxmox expects the volume as-is in the URL path. validateVolumeID above
	// is what makes interpolating it here safe.
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + volume
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete volume %s on %s/%s: %w", volume, node, storage, err)
	}
	return upid, nil
}

// volumeStorageIDPattern matches the "storeid" half of a volume id.
var volumeStorageIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// forbiddenVolumeIDChars are the characters that would let a volume id escape
// its position in the request path:
//
//	%   smuggles an encoded "../" or "?" through for pveproxy to decode
//	?   starts a query string, appending parameters to the outbound call
//	#   starts a fragment, silently truncating the path
//	\   is not valid in a PVE volume id and confuses path parsers
//
// Excluding "%" is the load-bearing one, and NOT because net/url would escape a
// stray "%" for us — it would not. net/url is all-or-nothing: URL.EscapedPath
// returns RawPath verbatim whenever validEncoded accepts it, and validEncoded
// explicitly allows "%". So "local:iso/%2e%2e%2f%2e%2e%2faccess" reaches
// Proxmox byte-for-byte and decodes to "../../access" on the far side —
// verified against a capture server. Do not drop "%" from this set on the
// assumption that the encoder cleans up after us.
//
// Everything else is safe to pass through. Bytes >= 0x80 are always
// percent-encoded (net/url's escape is byte-wise), and the ASCII characters
// that do survive literally — ! $ & ' ( ) * + , ; = : @ [ ] — carry no
// structural meaning inside a path segment. So after this set and the segment
// rules below, a volume id cannot leave the slot it occupies.
const forbiddenVolumeIDChars = "%?#\\"

// validateVolumeID checks a Proxmox volume id — "local:iso/debian-12.iso",
// "local-lvm:vm-100-disk-0", "local:backup/vzdump-qemu-100-....vma.zst" —
// before it is interpolated into a request path.
//
// The id is deliberately NOT percent-encoded on the way out, because Proxmox
// matches the literal path: escaping would turn the "/" of "iso/debian-12.iso"
// into %2F and break every file-based volume. That makes this function the only
// thing standing between an operator-supplied string and a URL that is fetched
// with the cluster's API token, so it rejects traversal outright rather than
// relying on the far side to normalise.
//
// Accepted trade-off: a volume whose name contains one of forbiddenVolumeIDChars
// cannot be deleted through this client. Proxmox will not mint such a name
// itself, but Nexara's own upload and download-url handlers do not filter those
// characters out of a caller-supplied filename, so it is possible to create one.
// The fix for that belongs on the creating side — restricting a filename is safe,
// whereas relaxing anything here reopens the path-injection hole.
func validateVolumeID(volume string) error {
	if volume == "" {
		return fmt.Errorf("%w: volume id is required", ErrInvalidInput)
	}
	if len(volume) > 512 {
		return fmt.Errorf("%w: volume id too long (%d bytes)", ErrInvalidInput, len(volume))
	}
	if i := strings.IndexAny(volume, forbiddenVolumeIDChars); i >= 0 {
		return fmt.Errorf("%w: volume id %q contains %q", ErrInvalidInput, volume, volume[i])
	}
	if hasControlChar(volume) {
		return fmt.Errorf("%w: volume id %q contains a control character", ErrInvalidInput, volume)
	}

	storage, name, ok := strings.Cut(volume, ":")
	if !ok {
		return fmt.Errorf("%w: volume id %q is not in \"storage:name\" form", ErrInvalidInput, volume)
	}
	if !volumeStorageIDPattern.MatchString(storage) {
		return fmt.Errorf("%w: volume id %q has a bad storage name %q", ErrInvalidInput, volume, storage)
	}
	if name == "" {
		return fmt.Errorf("%w: volume id %q is missing a volume name", ErrInvalidInput, volume)
	}
	// Reject "."/".." segments (traversal) and empty ones (a leading or doubled
	// "/"). Dots *inside* a segment are left alone — "vzdump-qemu-100.vma.zst"
	// and "ubuntu-24.04.1.iso" are ordinary names.
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: volume id %q has a bad path segment %q", ErrInvalidInput, volume, segment)
		}
	}
	return nil
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
			return "", fmt.Errorf("%w: filename exceeds 64 characters", ErrInvalidInput)
		}
		if !vztmplFilenamePattern.MatchString(params.FileName) {
			return "", fmt.Errorf("%w: filename contains invalid characters", ErrInvalidInput)
		}
		// The pattern permits dots, so bare "." and ".." slip through it. Those
		// are the values here that could become an undeletable volume id, since
		// the stored name depends on whether Proxmox appends its ".tar" suffix.
		if params.FileName == "." || strings.Contains(params.FileName, "..") {
			return "", fmt.Errorf("%w: filename %q is not a valid name", ErrInvalidInput, params.FileName)
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
	if err := ValidateStorageFilename(params.Filename); err != nil {
		return "", err
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

// QueryURLMetadata performs GET /nodes/{node}/query-url-metadata?url=<url> — the endpoint
// the PVE GUI uses to detect a download's filename and size before fetching it. verifyCerts
// mirrors the download-url option (nil = Proxmox default, which verifies). The node makes an
// outbound HEAD request to the URL, so callers must be authorized for it.
func (c *Client) QueryURLMetadata(ctx context.Context, node, rawURL string, verifyCerts *bool) (*URLMetadata, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if rawURL == "" {
		return nil, fmt.Errorf("URL is required")
	}
	if len(rawURL) > 2048 {
		return nil, fmt.Errorf("URL exceeds 2048 characters")
	}
	q := url.Values{}
	q.Set("url", rawURL)
	if verifyCerts != nil {
		if *verifyCerts {
			q.Set("verify-certificates", "1")
		} else {
			q.Set("verify-certificates", "0")
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/query-url-metadata?" + q.Encode()
	var meta URLMetadata
	if err := c.do(ctx, path, &meta); err != nil {
		return nil, fmt.Errorf("query url metadata on %s: %w", node, err)
	}
	return &meta, nil
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

// hasControlChar reports whether s contains a C0 control, DEL, or a C1 control
// (U+0080–U+009F). It decodes runes deliberately: C1 controls encode as
// 0xC2 0x80–0x9F in UTF-8, so a byte-wise scan for ASCII controls misses them.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

// ValidateStorageFilename checks a caller-supplied filename that Proxmox will
// turn into the tail of a volume id — "local:iso/<filename>".
//
// This is the creating-side mirror of validateVolumeID, and the two share
// forbiddenVolumeIDChars on purpose. A filename accepted here must always
// produce a volume id that validateVolumeID accepts, or we mint volumes the
// delete path can never address: exactly the gap that opened when the upload
// handler's ad-hoc check and the delete-side rules drifted apart. Widen one
// side only by widening the other — TestStorageFilenamesProduceDeletableVolids
// pins the two together.
//
// Spaces and non-ASCII are fine: they survive a volume id unharmed, since
// net/url percent-encodes them on the wire and Proxmox decodes them back.
func ValidateStorageFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("%w: filename is required", ErrInvalidInput)
	}
	if len(filename) > 255 {
		return fmt.Errorf("%w: filename too long (%d bytes)", ErrInvalidInput, len(filename))
	}
	// "." and ".." get their own message: filepath.Base("") yields ".", so a
	// multipart part with no filename lands here, and telling that caller their
	// name "must not contain .." sends them hunting for dots that aren't there.
	if filename == "." || filename == ".." {
		return fmt.Errorf("%w: %q is not a valid filename", ErrInvalidInput, filename)
	}
	if strings.Contains(filename, "..") {
		return fmt.Errorf("%w: filename %q must not contain %q", ErrInvalidInput, filename, "..")
	}
	if strings.ContainsAny(filename, `/\`) {
		return fmt.Errorf("%w: filename %q must not contain a path separator", ErrInvalidInput, filename)
	}
	if i := strings.IndexAny(filename, forbiddenVolumeIDChars); i >= 0 {
		return fmt.Errorf("%w: filename %q contains %q, which cannot appear in a Proxmox volume id",
			ErrInvalidInput, filename, filename[i])
	}
	if hasControlChar(filename) {
		return fmt.Errorf("%w: filename %q contains a control character", ErrInvalidInput, filename)
	}
	return nil
}

// ScanISCSI performs GET /nodes/{node}/scan/iscsi?portal=<portal>, the discovery
// call the PVE GUI makes to fill the target dropdown when adding iSCSI storage.
// The node runs an iscsiadm sendtargets discovery against the portal, so callers
// must be authorized to make it reach out to an arbitrary address.
func (c *Client) ScanISCSI(ctx context.Context, node, portal string) ([]ISCSITarget, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := ValidateISCSIPortal(portal); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("portal", portal)
	path := "/nodes/" + url.PathEscape(node) + "/scan/iscsi?" + q.Encode()
	var targets []ISCSITarget
	if err := c.do(ctx, path, &targets); err != nil {
		return nil, fmt.Errorf("scan iscsi portal %s on %s: %w", portal, node, err)
	}
	return targets, nil
}

// ValidateISCSIPortal checks a caller-supplied iSCSI portal address before it is
// handed to a node for discovery. Proxmox accepts "host" or "host:port"; anything
// with whitespace or control characters is a malformed value, not a reachable
// portal, and is rejected here rather than passed on.
func ValidateISCSIPortal(portal string) error {
	if portal == "" {
		return fmt.Errorf("%w: portal is required", ErrInvalidInput)
	}
	if len(portal) > 255 {
		return fmt.Errorf("%w: portal exceeds 255 characters", ErrInvalidInput)
	}
	if hasControlChar(portal) {
		return fmt.Errorf("%w: portal %q contains a control character", ErrInvalidInput, portal)
	}
	if strings.ContainsAny(portal, " \t/?#") {
		return fmt.Errorf("%w: portal %q must be a host or host:port address", ErrInvalidInput, portal)
	}
	return nil
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

// cephOSDInOutActions are the mon-level OSD membership commands. Unlike the
// daemon actions below these run synchronously and return no task UPID.
var cephOSDInOutActions = map[string]bool{"in": true, "out": true}

// cephDaemonActions are the systemd-level Ceph daemon commands exposed at
// POST /nodes/{node}/ceph/{action}.
var cephDaemonActions = map[string]bool{"start": true, "stop": true, "restart": true}

// SetCephOSDIn marks an OSD "in" — eligible to hold data — via
// POST /nodes/{node}/ceph/osd/{osdid}/in. Ceph begins backfilling PGs onto it.
func (c *Client) SetCephOSDIn(ctx context.Context, node string, osdID int) error {
	return c.setCephOSDInOut(ctx, node, osdID, "in")
}

// SetCephOSDOut marks an OSD "out" via POST /nodes/{node}/ceph/osd/{osdid}/out.
// The daemon keeps running but Ceph remaps its PGs onto the remaining OSDs.
func (c *Client) SetCephOSDOut(ctx context.Context, node string, osdID int) error {
	return c.setCephOSDInOut(ctx, node, osdID, "out")
}

// setCephOSDInOut issues an OSD in/out mon command. Any node in the cluster can
// serve it — the request does not have to reach the OSD's own host.
func (c *Client) setCephOSDInOut(ctx context.Context, node string, osdID int, action string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if osdID < 0 {
		return fmt.Errorf("invalid OSD ID %d", osdID)
	}
	if !cephOSDInOutActions[action] {
		return fmt.Errorf("invalid OSD membership action %q", action)
	}
	path := "/nodes/" + url.PathEscape(node) + "/ceph/osd/" + strconv.Itoa(osdID) + "/" + action
	if err := c.doPost(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("mark osd.%d %s on %s: %w", osdID, action, node, err)
	}
	return nil
}

// CephServiceAction starts, stops or restarts a Ceph daemon via
// POST /nodes/{node}/ceph/{action} with service=<type>.<id> (e.g. "osd.3"), and
// returns the UPID of the resulting Proxmox task.
//
// Unlike the in/out mon commands this is a systemd operation, so node MUST be
// the host actually running the daemon.
func (c *Client) CephServiceAction(ctx context.Context, node, service, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if !cephDaemonActions[action] {
		return "", fmt.Errorf("invalid ceph daemon action %q", action)
	}
	if !cephServicePattern.MatchString(service) {
		return "", fmt.Errorf("invalid ceph service name %q", service)
	}
	form := url.Values{}
	form.Set("service", service)

	var upid string
	path := "/nodes/" + url.PathEscape(node) + "/ceph/" + action
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("%s ceph service %s on %s: %w", action, service, node, err)
	}
	return upid, nil
}

// cephServicePattern mirrors the Proxmox API's own `service` parameter format,
// e.g. "osd.3", "mon.pve1". An unqualified type ("osd") would target every
// daemon of that type on the node, so the id suffix is required here.
var cephServicePattern = regexp.MustCompile(`^(mon|mds|osd|mgr)\.[A-Za-z0-9\-]{1,200}$`)
