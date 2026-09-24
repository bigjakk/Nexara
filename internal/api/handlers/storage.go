package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// StorageHandler handles storage pool read endpoints.
type StorageHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewStorageHandler creates a new storage handler.
func NewStorageHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *StorageHandler {
	return &StorageHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

type storageResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Storage   string    `json:"storage"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Active    bool      `json:"active"`
	Enabled   bool      `json:"enabled"`
	Shared    bool      `json:"shared"`
	Total     int64     `json:"total"`
	Used      int64     `json:"used"`
	Avail     int64     `json:"avail"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toStorageResponse(s db.StoragePool) storageResponse {
	return storageResponse{
		ID:         s.ID,
		ClusterID:  s.ClusterID,
		NodeID:     s.NodeID,
		Storage:    s.Storage,
		Type:       s.Type,
		Content:    s.Content,
		Active:     s.Active,
		Enabled:    s.Enabled,
		Shared:     s.Shared,
		Total:      s.Total,
		Used:       s.Used,
		Avail:      s.Avail,
		LastSeenAt: s.LastSeenAt,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

type storageContentResponse struct {
	Volid   string `json:"volid"`
	Format  string `json:"format"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
	Content string `json:"content"`
	VMID    int    `json:"vmid,omitempty"`
}

type uploadResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

type deleteContentResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/storage.
func (h *StorageHandler) ListByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pools, err := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list storage pools")
	}

	// `pool`, not `p`: the handler's own p is the *apischema.Params, and a
	// loop variable that shadows it is the name registry_paramkey_guard_test
	// scans for.
	resp := make([]storageResponse, len(pools))
	for i, pool := range pools {
		resp[i] = toStorageResponse(pool)
	}

	return RespondItems(c, resp)
}

// GetContent handles GET /api/v1/clusters/:cluster_id/storage/:storage_id/content.
func (h *StorageHandler) GetContent(c fiber.Ctx, p *apischema.Params) error {
	pool, pxClient, err := h.resolveStorage(c, p)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	items, err := pxClient.GetStorageContent(c.Context(), node.Name, pool.Storage)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]storageContentResponse, len(items))
	for i, item := range items {
		resp[i] = storageContentResponse{
			Volid:   item.Volid,
			Format:  item.Format,
			Size:    item.Size,
			CTime:   item.CTime,
			Content: item.Content,
			VMID:    item.VMID,
		}
	}

	return RespondItems(c, resp)
}

// uploadContentAllowed reports whether a caller holding the given cluster-scoped grants may
// upload the given content type. ISO/CT-template uploads are a storage-management action;
// OVA (import) uploads are permitted for either manage:storage or manage:vm_import. The
// decision is isolated here so its security-load-bearing branches are unit-tested and stay
// correct across refactors of the streaming multipart loop that calls it.
func uploadContentAllowed(content string, canStorage, canImport bool) bool {
	switch content {
	case "iso", "vztmpl":
		return canStorage
	case "import":
		return canStorage || canImport
	default:
		return false
	}
}

// ParseMultipartContentType reports whether a Content-Type names a multipart
// body — the kind UploadFile streams — and, when it does, the parameters it
// carries, the boundary among them: mime.ParseMediaType reads it without an
// error, and the media type, which it returns in lower case, begins
// "multipart/".
//
// UploadFile asks it of every upload, and the api package asks it too, to
// decide which bodies the server's bounds on a request body — the body read
// deadline, the 10 MiB body-size guard and the refusal of chunked bodies —
// leave to this handler (isStreamedUpload). One function for both, so the
// server never exempts a body this handler would not stream, nor bounds one
// it would.
func ParseMultipartContentType(contentType string) (params map[string]string, ok bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, false
	}
	return params, true
}

// UploadFile handles POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload.
//
// This handler uses streaming multipart parsing to avoid buffering the entire
// file in memory. With StreamRequestBody enabled on the server, fasthttp
// provides a body stream for every request that has a body, whatever
// BodyLimit is. We parse the multipart stream directly and pipe the file part
// to Proxmox.
//
// The frontend must send form fields in order: content, filesize, file.
func (h *StorageHandler) UploadFile(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// The route declares Permissions{Deferred} and installs no middleware, so
	// this IS the gate. Coarse first: ISO/CT-template uploads require
	// manage:storage; OVA (import) uploads are also reachable with
	// manage:vm_import. The exact content type only arrives inside the
	// multipart stream, so resolve both grants up front and enforce
	// per-content below.
	canStorage, err := hasClusterPerm(c, "manage", "storage", clusterID)
	if err != nil {
		return err
	}
	canImport, err := hasClusterPerm(c, "manage", "vm_import", clusterID)
	if err != nil {
		return err
	}
	if !canStorage && !canImport {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	pool, pxClient, err := h.resolveStorage(c, p)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	// Parse the multipart boundary from the Content-Type header.
	params, ok := ParseMultipartContentType(c.Get("Content-Type"))
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "Expected multipart form data")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Missing multipart boundary")
	}

	// Get the body stream. Under StreamRequestBody fasthttp provides one for
	// every request with a body — at most 8 KiB prefetched, the rest still on
	// the connection. It is nil only when there is none to read: a request
	// with no body framing, or one whose stream c.Body() already drained, and
	// then the in-memory buffer is all there is.
	// Fiber v3: the underlying fasthttp.RequestCtx is c.RequestCtx() (c.Context()
	// now returns the Go context.Context).
	bodyStream := c.RequestCtx().RequestBodyStream()
	if bodyStream == nil {
		bodyStream = bytes.NewReader(c.Body())
	}

	mr := multipart.NewReader(bodyStream, boundary)

	var uploadContent string
	var fileSize int64

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Failed to parse multipart form")
		}

		switch part.FormName() {
		case "content":
			val, _ := io.ReadAll(io.LimitReader(part, 64))
			uploadContent = strings.TrimSpace(string(val))

		case "filesize":
			val, _ := io.ReadAll(io.LimitReader(part, 32))
			fileSize, _ = strconv.ParseInt(strings.TrimSpace(string(val)), 10, 64)

		case "file":
			filename := filepath.Base(part.FileName())
			// Rejected here rather than after the upload lands, so we never
			// create a volume whose id the delete endpoint would refuse.
			if err := proxmox.ValidateStorageFilename(filename); err != nil {
				return mapProxmoxError(err)
			}
			switch uploadContent {
			case "iso", "vztmpl", "import":
				// valid content type
			default:
				return fiber.NewError(fiber.StatusBadRequest, "content must be 'iso', 'vztmpl', or 'import'")
			}
			// Per-content permission enforcement (see uploadContentAllowed). This runs before
			// any byte is streamed to Proxmox — a manage:vm_import-only caller cannot upload
			// an ISO/CT-template, only an OVA (import).
			if !uploadContentAllowed(uploadContent, canStorage, canImport) {
				return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
			}
			if fileSize <= 0 {
				return fiber.NewError(fiber.StatusBadRequest, "filesize field is required before file")
			}

			// Wrap the part reader in a large buffer to reduce syscalls.
			// multipart.Part does byte-level boundary scanning; buffering
			// amortises that overhead across 256KB chunks.
			bufferedPart := bufio.NewReaderSize(part, 256*1024)

			// Stream the file part directly to Proxmox without buffering.
			upid, uploadErr := pxClient.UploadToStorage(c.Context(), node.Name, pool.Storage, uploadContent, filename, bufferedPart, fileSize)
			if uploadErr != nil {
				return mapProxmoxError(uploadErr)
			}

			TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
				ClusterID:    pool.ClusterID,
				Node:         node.Name,
				ResourceType: "storage",
				ResourceID:   pool.ID.String(),
				Action:       "upload",
				UPID:         upid,
				Description:  "Upload " + filename,
				Extra:        map[string]any{"storage": pool.Storage, "filename": filename},
			})

			return c.JSON(uploadResponse{
				UPID:   upid,
				Status: "dispatched",
			})

		default:
			// Discard unknown form fields.
			_, _ = io.Copy(io.Discard, part)
		}
	}

	return fiber.NewError(fiber.StatusBadRequest, "No file provided in upload")
}

// DeleteContent handles DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/:volume.
func (h *StorageHandler) DeleteContent(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	// Still hand-placed, and it has to be: this is the one storage route the
	// registry cannot declare, because its volume id is a greedy wildcard
	// segment (see registerStorageEndpoints in internal/api/registry_storage.go).
	if err := requireClusterPerm(c, "delete", "storage", clusterID); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorageLegacy(c)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	rawVolume := c.Params("*")
	if rawVolume == "" {
		return fiber.NewError(fiber.StatusBadRequest, "volume is required")
	}
	volume, err := url.PathUnescape(rawVolume)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid volume ID")
	}

	log.Printf("DELETE storage content: node=%s storage=%s volume=%q", node.Name, pool.Storage, volume)
	upid, err := pxClient.DeleteStorageContent(c.Context(), node.Name, pool.Storage, volume)
	if err != nil {
		log.Printf("DELETE storage content error: %v", err)
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    pool.ClusterID,
		Node:         node.Name,
		ResourceType: "storage",
		ResourceID:   pool.ID.String(),
		Action:       "delete_content",
		UPID:         upid,
		Description:  "Delete " + volume,
		Extra:        map[string]any{"storage": pool.Storage, "volume": volume},
	})

	return c.JSON(deleteContentResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// storageConfigResponse wraps the Proxmox storage config for frontend consumption.
//
// Build it with newStorageConfigResponse — never with a bare composite literal.
// The constructor is the one place the write-only credentials are dropped, and
// TestGuard_StorageConfigResponseHasOneConstructor
// (proxmox_read_credentials_test.go) fails if any other function declaration
// in the package, tests aside, writes a composite literal whose type is
// spelled storageConfigResponse.
type storageConfigResponse struct {
	proxmox.StorageConfig
}

// newStorageConfigResponse is the body of GET .../storage/:storage_id/config,
// with password and keyring blanked — the same rule metric_servers.go applies
// to the InfluxDB token, and the same reason: the route is gated on
// view:storage, which every built-in Viewer holds.
//
// password (cifs, pbs, esxi) and keyring (rbd, cephfs) are blanked as defence
// in depth, not because Proxmox returns them. Each plugin lists them among its
// sensitive-properties (pve-storage 8.3.5 on; before that Config.pm
// hard-coded the same keys), API2/Storage/Config.pm extracts those before the
// section is written, and the plugin keeps them under /etc/pve/priv — so the
// storage.cfg section this read returns should never carry either. Blanking
// costs nothing on a GET the editor round-trips, because both halves already
// treat an absent value as "leave the stored one alone": storagePluginForm
// drops an empty value rather than sending it, EditStorageDialog submits a
// loaded field only when its value differs from the one it loaded, and it
// never loads the keyring at all — that field starts empty and is sent only
// when the operator types one. They are `omitempty`, so blanking drops the key
// rather than publishing an empty string that reads as "there is no password
// set".
//
// Deliberately NOT blanked:
//
//   - encryption-key (pbs) is the key's FINGERPRINT on this read, not the key.
//     PBSPlugin's add and update hooks store the key itself under
//     /etc/pve/priv and write `$decoded_key->{fingerprint} || 1` into
//     storage.cfg (pve-storage src/PVE/Storage/PBSPlugin.pm), and the edit
//     dialog shows it as the sign that client-side encryption is on. An
//     earlier version blanked it, which dropped that signal and protected
//     nothing.
//   - fingerprint is the PBS server's TLS certificate fingerprint, which the
//     edit dialog reads and shows, and master-pubkey is a PUBLIC key (stored as
//     1 in storage.cfg once set) — PBS encrypts a copy of the backup key to it
//     so the private half can recover it.
func newStorageConfigResponse(cfg proxmox.StorageConfig) storageConfigResponse {
	cfg.Password = ""
	cfg.Keyring = ""
	return storageConfigResponse{cfg}
}

// StorageTypes are the Proxmox storage plugin types POST
// /clusters/:cluster_id/storage accepts, in the order the storage dialog
// offers them.
//
// Exported so the route's declaration in internal/api/registry_storage.go
// can use it as the `type` enum: the list that validates the request and
// the list the dialog can fill settings in for are then the same list. It
// replaced a map whose only reader was the hand-rolled "Invalid storage
// type" check the schema now makes.
//
// This vocabulary is Nexara's own to close, unlike a ZFS raid level or an
// image format: STORAGE_TYPE_FIELDS in the frontend has to know a plugin's
// settings before the dialog can create one, so a type nothing here lists
// is a type this API could not usefully accept anyway.
var StorageTypes = []string{
	"dir", "btrfs", "nfs", "cifs", "glusterfs",
	"lvm", "lvmthin", "zfspool",
	"iscsi", "iscsidirect", "rbd", "cephfs", "pbs",
}

// StorageDownloadContents are the content kinds POST
// .../storage/:storage_id/download-url accepts. Exported for the same
// reason, and it is the same three the upload route's per-content
// permission decision switches on (see uploadContentAllowed).
var StorageDownloadContents = []string{"iso", "vztmpl", "import"}

// GetConfig handles GET /api/v1/clusters/:cluster_id/storage/:storage_id/config.
// Returns the Proxmox-level storage configuration (paths, servers, etc.).
func (h *StorageHandler) GetConfig(c fiber.Ctx, p *apischema.Params) error {
	pool, pxClient, err := h.resolveStorage(c, p)
	if err != nil {
		return err
	}

	cfg, err := pxClient.GetStorageConfig(c.Context(), pool.Storage)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(newStorageConfigResponse(*cfg))
}

// iscsiTargetResponse is one discovered target from an iSCSI portal scan.
type iscsiTargetResponse struct {
	Target string `json:"target"`
	Portal string `json:"portal"`
}

// ScanISCSI handles GET /api/v1/clusters/:cluster_id/scan/iscsi?portal=.
// It runs Proxmox's iSCSI discovery against the portal so the Add Storage dialog
// can offer the advertised target IQNs instead of making the operator type one,
// mirroring the PVE GUI's target dropdown.
//
// Discovery makes the node open an outbound connection to a caller-supplied
// address (an SSRF-shaped primitive), so it is gated on manage:storage — the same
// bar as creating the storage it precedes — not the lower view:storage.
//
// The scan runs from any online node. Which node answers doesn't change the
// result, but reachability is not guaranteed on a segmented network — a portal
// only some nodes can see reports no targets, and manual entry stays available
// for exactly that case.
func (h *StorageHandler) ScanISCSI(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// Trimmed here rather than in the schema: apischema does not normalize a
	// plain string, and ValidateISCSIPortal owns the rest of the rule — no
	// whitespace, no control characters, no "/?#".
	portal := strings.TrimSpace(p.String("portal"))
	if err := proxmox.ValidateISCSIPortal(portal); err != nil {
		return mapProxmoxError(err)
	}

	pxClient, nodeName, err := h.resolveOnlineNode(c, clusterID)
	if err != nil {
		return err
	}

	targets, err := pxClient.ScanISCSI(c.Context(), nodeName, portal)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]iscsiTargetResponse, len(targets))
	for i, t := range targets {
		resp[i] = iscsiTargetResponse{Target: t.Target, Portal: t.Portal}
	}
	return RespondItems(c, resp)
}

// storagePluginForm flattens a validated `params` object into the Proxmox
// form fields a storage create or update sends.
//
// "storage" and "type" are dropped because both bodies name them
// separately (and Proxmox marks the backend-identifying options of several
// plugins fixed, refusing a PUT that carries them). An EMPTY value is
// dropped rather than sent, which is what the storage dialog relies on to
// mean "leave this setting alone" — sending "" would clear it instead.
func storagePluginForm(params map[string]string) url.Values {
	form := url.Values{}
	for k, v := range params {
		if k != "storage" && k != "type" && v != "" {
			form.Set(k, v)
		}
	}
	return form
}

// storageWriteResponse is the body of a successful storage create or update.
type storageWriteResponse struct {
	Status  string `json:"status"`
	Storage string `json:"storage"`
	// GeneratedEncryptionKey is the PBS client encryption key Proxmox generated
	// for this request's encryption-key=autogen, and it is absent from every
	// other response — including one for a request that carried a key of its
	// own. It is the operator's one chance to take a copy. Nexara keeps none:
	// not in the database, the audit row, an event or a log line. Proxmox keeps
	// it only on the cluster, so without a copy the backups encrypted with it
	// cannot be restored if the cluster is lost.
	GeneratedEncryptionKey string `json:"generated_encryption_key,omitempty"`
}

// respondStorageWrite writes a create or update response. One that carries a
// generated key is marked no-store, so that no cache between Nexara and the
// operator keeps a copy of the one response the key is handed over in.
func respondStorageWrite(c fiber.Ctx, status int, resp storageWriteResponse) error {
	if resp.GeneratedEncryptionKey != "" {
		c.Set(fiber.HeaderCacheControl, "no-store")
	}
	return c.Status(status).JSON(resp)
}

// encryptionKeyChange says in words what a storage write did to a PBS
// encryption key — "generated", "supplied" or "removed" — or "" when it did
// not touch one. It is what the audit row records instead of the key:
// view:audit is granted to every Viewer by default, and a key the operator
// supplied is as much the whole secret as one Proxmox generated.
//
// Only a pbs storage has a key to touch, and for any other type the row must
// not claim a key changed. A Proxmox before pve-storage 8.3.5 takes
// encryption-key from a write to any storage type and drops it:
// API2/Storage/Config.pm extracted one hard-coded sensitive-property list for
// every type, and of the built-in plugins only PBSPlugin's hooks read the key.
// A current one refuses it for the other built-in types, which all declare
// their own sensitive-properties, but still takes and drops it for a plugin
// that declares none (the fallback in sensitive_properties, pve-storage
// src/PVE/Storage/Plugin.pm).
func encryptionKeyChange(storageType string, form url.Values, deleteOptions []string) string {
	switch {
	case storageType != "pbs":
		return ""
	case slices.Contains(deleteOptions, "encryption-key"):
		return "removed"
	case !form.Has("encryption-key"):
		return ""
	case form.Get("encryption-key") == proxmox.PBSEncryptionKeyAutogen:
		return "generated"
	default:
		return "supplied"
	}
}

// Create handles POST /api/v1/clusters/:cluster_id/storage.
func (h *StorageHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// The schema's enum is StorageTypes and its storage-id format refuses an
	// empty name, so the two hand-rolled checks the handler used to make are
	// both gone — and each now answers with the field it is about.
	storage, storageType := p.String("storage"), p.String("type")
	params, err := stringMap("params", p.Object("params"))
	if err != nil {
		return err
	}

	form := storagePluginForm(params)
	form.Set("storage", storage)
	form.Set("type", storageType)

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	result, err := pxClient.CreateStorage(c.Context(), form)
	if err != nil {
		return mapProxmoxError(err)
	}

	audit := map[string]string{"storage": storage, "type": storageType}
	if change := encryptionKeyChange(storageType, form, nil); change != "" {
		audit["encryption_key"] = change
	}
	details, _ := json.Marshal(audit)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", storage, "create", details)

	return respondStorageWrite(c, fiber.StatusCreated, storageWriteResponse{
		Status:                 "created",
		Storage:                storage,
		GeneratedEncryptionKey: result.GeneratedEncryptionKey.Reveal(),
	})
}

// Update handles PUT /api/v1/clusters/:cluster_id/storage/:storage_id.
func (h *StorageHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	pool, pxClient, err := h.resolveStorage(c, p)
	if err != nil {
		return err
	}

	params, err := stringMap("params", p.Object("params"))
	if err != nil {
		return err
	}
	form := storagePluginForm(params)
	// Read the way Proxmox reads its own lists, so the audit row's "removed"
	// is decided on the names Proxmox will act on. Which names may be cleared
	// is Proxmox's call; proxmox.UpdateStorage forwards any of them.
	deleteOptions := proxmox.SplitStorageDeleteList(p.String("delete"))

	result, err := pxClient.UpdateStorage(c.Context(), pool.Storage, form, deleteOptions)
	if err != nil {
		return mapProxmoxError(err)
	}

	var details json.RawMessage
	if change := encryptionKeyChange(pool.Type, form, deleteOptions); change != "" {
		details, _ = json.Marshal(map[string]string{"encryption_key": change})
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(pool.ClusterID), "storage", pool.ID.String(), "update", details)

	return respondStorageWrite(c, fiber.StatusOK, storageWriteResponse{
		Status:                 "updated",
		Storage:                pool.Storage,
		GeneratedEncryptionKey: result.GeneratedEncryptionKey.Reveal(),
	})
}

// Delete handles DELETE /api/v1/clusters/:cluster_id/storage/:storage_id.
func (h *StorageHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	pool, pxClient, err := h.resolveStorage(c, p)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteStorage(c.Context(), pool.Storage); err != nil {
		return mapProxmoxError(err)
	}

	// Remove from local DB immediately so it doesn't appear stale.
	// Storage is cluster-level, so delete all rows for this name across nodes.
	_ = h.queries.DeleteStoragePoolsByName(c.Context(), db.DeleteStoragePoolsByNameParams{
		ClusterID: pool.ClusterID,
		Storage:   pool.Storage,
	})

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(pool.ClusterID), "storage", pool.ID.String(), "delete", nil)

	return c.JSON(fiber.Map{
		"status":  "deleted",
		"storage": pool.Storage,
	})
}

// resolveStorage loads the storage pool named by a registry route's path
// parameters and creates a Proxmox client for its cluster.
//
// Both ids arrive validated — cluster_id and storage_id are declared with
// apischema's uuid format — so a parse failure here means the declaration
// and this call disagree, which is what parseParamUUID reports as a 500.
func (h *StorageHandler) resolveStorage(c fiber.Ctx, p *apischema.Params) (db.StoragePool, *proxmox.Client, error) {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return db.StoragePool{}, nil, err
	}
	storageID, err := parseParamUUID(p.String("storage_id"))
	if err != nil {
		return db.StoragePool{}, nil, err
	}
	return h.resolveStorageByID(c, clusterID, storageID)
}

// resolveStorageLegacy is resolveStorage for the ONE storage route still
// registered in router.go: DELETE .../storage/:storage_id/content/*, whose
// greedy wildcard the parameter schema cannot describe. See
// registerStorageEndpoints in internal/api/registry_storage.go for why it
// is not declared. It reads the same two ids straight off the context, the
// way every handler in this file did before Phase 6d.
func (h *StorageHandler) resolveStorageLegacy(c fiber.Ctx) (db.StoragePool, *proxmox.Client, error) {
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return db.StoragePool{}, nil, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	storageID, err := uuid.Parse(c.Params("storage_id"))
	if err != nil {
		return db.StoragePool{}, nil, fiber.NewError(fiber.StatusBadRequest, "Invalid storage ID")
	}
	return h.resolveStorageByID(c, clusterID, storageID)
}

// resolveStorageByID is the half both spellings share.
//
// The cluster check is the load-bearing line: the permission gate
// authorizes the cluster in the PATH, while GetStoragePool keys on the
// storage id alone, so without it a pool from another cluster would be
// reachable by anyone holding the grant anywhere. It answers 404 rather
// than 403 for the same reason the container and CVE routes do — whether a
// pool exists elsewhere is not the caller's to learn.
func (h *StorageHandler) resolveStorageByID(c fiber.Ctx, clusterID, storageID uuid.UUID) (db.StoragePool, *proxmox.Client, error) {
	var zero db.StoragePool

	pool, err := h.queries.GetStoragePool(c.Context(), storageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, fiber.NewError(fiber.StatusNotFound, "Storage pool not found")
		}
		return zero, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get storage pool")
	}

	if pool.ClusterID != clusterID {
		return zero, nil, fiber.NewError(fiber.StatusNotFound, "Storage pool not found in this cluster")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return zero, nil, err
	}

	return pool, pxClient, nil
}

// createProxmoxClient creates a Proxmox client for the given cluster.
// Uses 30-minute timeout for large ISO uploads.
func (h *StorageHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, 30*time.Minute)
}
