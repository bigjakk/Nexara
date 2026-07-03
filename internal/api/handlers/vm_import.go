package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// VMImportHandler handles importing guests into Proxmox from foreign sources
// (OVA/OVF appliances, raw disk images, and ESXi/vCenter guests) via Proxmox's
// create-with-import-from flow.
type VMImportHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewVMImportHandler creates a new VMImportHandler.
func NewVMImportHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *VMImportHandler {
	return &VMImportHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// importClientTimeout is generous: ESXi/OVA metadata parsing runs server-side over a
// FUSE mount and can be slow, and the create call may block briefly while Proxmox stages
// the import. The long disk conversion itself is async (tracked by UPID), not awaited here.
const importClientTimeout = 30 * time.Minute

// resolveNode loads a node by name within the cluster and returns it together with the
// cluster and an authenticated Proxmox client. Unlike the storage-scoped helpers, the
// node is the caller's chosen target — the import-from create call must dispatch here.
func (h *VMImportHandler) resolveNode(c fiber.Ctx, clusterID uuid.UUID, nodeName string) (db.Node, db.Cluster, *proxmox.Client, error) {
	var zeroNode db.Node
	var zeroCluster db.Cluster

	if nodeName == "" {
		return zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusBadRequest, "node is required")
	}
	node, err := h.queries.GetNodeByClusterAndName(c.Context(), db.GetNodeByClusterAndNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")
		}
		return zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get node")
	}
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, importClientTimeout)
	if err != nil {
		return zeroNode, zeroCluster, nil, err
	}
	return node, cluster, pxClient, nil
}

// --- import-metadata -------------------------------------------------------------------

type importMetadataRequest struct {
	Node    string `json:"node"`
	Storage string `json:"storage"`
	Volume  string `json:"volume"`
}

type importMetadataResponse struct {
	Type       string                     `json:"type"`
	Source     string                     `json:"source"`
	Name       string                     `json:"name"`
	Cores      int                        `json:"cores"`
	Memory     int                        `json:"memory"`
	OSType     string                     `json:"ostype"`
	CreateArgs map[string]string          `json:"create_args"`
	Disks      map[string]proxmox.ImportDisk `json:"disks"`
	Warnings   []proxmox.ImportWarning    `json:"warnings"`
}

// GetImportMetadata handles POST /api/v1/clusters/:cluster_id/import-metadata. It parses an
// importable OVA/OVF/ESXi guest and returns the pre-filled guest definition for the wizard.
// POST (not GET) is used so the slash/colon-laden source volid travels in the JSON body.
func (h *VMImportHandler) GetImportMetadata(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "vm_import", clusterID); err != nil {
		return err
	}
	var req importMetadataRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Storage == "" || req.Volume == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage and volume are required")
	}
	_, _, pxClient, err := h.resolveNode(c, clusterID, req.Node)
	if err != nil {
		return err
	}
	meta, err := pxClient.GetImportMetadata(c.Context(), req.Node, req.Storage, req.Volume)
	if err != nil {
		return mapProxmoxError(err)
	}
	args := meta.FlatCreateArgs()
	return c.JSON(importMetadataResponse{
		Type:       meta.Type,
		Source:     meta.Source,
		Name:       args["name"],
		Cores:      atoiOrZero(args["cores"]),
		Memory:     atoiOrZero(args["memory"]),
		OSType:     args["ostype"],
		CreateArgs: args,
		Disks:      meta.ParsedDisks(),
		Warnings:   meta.Warnings,
	})
}

// --- URL metadata probe ----------------------------------------------------------------

// QueryURLMetadata handles GET /api/v1/clusters/:cluster_id/query-url-metadata?node=&url=.
// It asks Proxmox to detect a remote download's filename and size (what the PVE GUI's
// "Query URL" button does) so the import wizard can pre-fill the filename before staging an
// OVA. This makes the node issue an outbound request to an arbitrary URL (an SSRF-shaped
// primitive), so it is gated on manage:storage — the same bar as the download it precedes
// (DownloadURL) — rather than the lower manage:vm_import, which must not gain a node-side
// URL-fetch capability it otherwise lacks. The browser-upload leg (no node-side fetch) is
// what remains available to manage:vm_import holders.
func (h *VMImportHandler) QueryURLMetadata(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
		return err
	}
	rawURL := c.Query("url")
	if rawURL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "url is required")
	}
	if !isHTTPURL(rawURL) {
		return fiber.NewError(fiber.StatusBadRequest, "url must be an http or https URL")
	}
	nodeName, err := h.pickImportNode(c, clusterID, c.Query("node"))
	if err != nil {
		return err
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, importClientTimeout)
	if err != nil {
		return err
	}
	meta, err := pxClient.QueryURLMetadata(c.Context(), nodeName, rawURL, nil)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(meta)
}

// pickImportNode validates the given node belongs to the cluster, or — when empty — falls
// back to the first online node. Used by node-agnostic probes (URL metadata) where any
// reachable node will do.
func (h *VMImportHandler) pickImportNode(c fiber.Ctx, clusterID uuid.UUID, nodeName string) (string, error) {
	if nodeName != "" {
		node, err := h.queries.GetNodeByClusterAndName(c.Context(), db.GetNodeByClusterAndNameParams{
			ClusterID: clusterID,
			Name:      nodeName,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")
			}
			return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to get node")
		}
		return node.Name, nil
	}
	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}
	for _, n := range nodes {
		if n.Status == "online" {
			return n.Name, nil
		}
	}
	return "", fiber.NewError(fiber.StatusServiceUnavailable, "No online node available")
}

// isHTTPURL reports whether raw parses as an absolute http(s) URL with a host.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// --- import sources + content listing --------------------------------------------------

// intrinsicallySharedStorageTypes are PVE storage backends reachable from every cluster
// node by their nature (network-backed), so PVE may omit the explicit `shared` flag in the
// storage config. ESXi import storage is a network connection to the ESXi host and is
// likewise visible cluster-wide.
var intrinsicallySharedStorageTypes = map[string]bool{
	"nfs": true, "cifs": true, "glusterfs": true, "cephfs": true, "esxi": true,
}

func isSharedImportStorage(cfg proxmox.StorageConfig) bool {
	return cfg.Shared == 1 || intrinsicallySharedStorageTypes[cfg.Type]
}

type importSourceEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Shared  bool   `json:"shared"`
	Node    string `json:"node"`              // an online node from which this source can be browsed
	PoolID  string `json:"pool_id,omitempty"` // storage_pools UUID for (storage,node), when in inventory
}

// ListImportSources handles GET /api/v1/clusters/:cluster_id/vm-import-sources. It returns
// the import-capable storages (content=import) and ESXi sources across the cluster, built
// from the *live* Proxmox storage config rather than Nexara's periodically-synced inventory
// — so a shared storage appears once (not once per node) and a just-registered ESXi source
// shows up immediately. Each entry carries an online node from which to browse it.
func (h *VMImportHandler) ListImportSources(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "vm_import", clusterID); err != nil {
		return err
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, importClientTimeout)
	if err != nil {
		return err
	}
	cfgs, err := pxClient.ListStorageConfigs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}
	onlineNodes := make([]db.Node, 0, len(nodes))
	nodeByID := make(map[uuid.UUID]string, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n.Name
		if n.Status == "online" {
			onlineNodes = append(onlineNodes, n)
		}
	}

	// (storage,node) -> pool UUID, so the wizard can drive the pool-id-keyed upload/download
	// endpoints. Absent for a source not yet in inventory (e.g. a just-registered ESXi host).
	pools, _ := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	poolID := make(map[string]string, len(pools))
	for _, p := range pools {
		poolID[p.Storage+"\x00"+nodeByID[p.NodeID]] = p.ID.String()
	}

	entries := make([]importSourceEntry, 0)
	for _, cfg := range cfgs {
		if cfg.Disable == 1 {
			continue
		}
		if cfg.Type != "esxi" && !storageHasContent(cfg.Content, "import") {
			continue
		}
		allowed := parseNodeRestriction(cfg.Nodes)
		candidates := make([]db.Node, 0, len(onlineNodes))
		for _, n := range onlineNodes {
			if allowed == nil || allowed[n.Name] {
				candidates = append(candidates, n)
			}
		}
		if len(candidates) == 0 {
			continue // no online node can reach it; nothing to browse
		}
		shared := isSharedImportStorage(cfg)
		emit := func(n db.Node) {
			entries = append(entries, importSourceEntry{
				Storage: cfg.Storage,
				Type:    cfg.Type,
				Content: cfg.Content,
				Shared:  shared,
				Node:    n.Name,
				PoolID:  poolID[cfg.Storage+"\x00"+n.Name],
			})
		}
		if shared {
			emit(candidates[0]) // one entry; any online node can browse it
		} else {
			for _, n := range candidates {
				emit(n) // per-node: each node's copy holds different files
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Storage != entries[j].Storage {
			return entries[i].Storage < entries[j].Storage
		}
		return entries[i].Node < entries[j].Node
	})
	return c.JSON(entries)
}

// parseNodeRestriction parses a PVE storage `nodes` field (comma-separated node names).
// Returns nil when unrestricted (all nodes), or a set of the allowed node names.
func parseNodeRestriction(nodes string) map[string]bool {
	nodes = strings.TrimSpace(nodes)
	if nodes == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, n := range strings.Split(nodes, ",") {
		if n = strings.TrimSpace(n); n != "" {
			set[n] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

type importSourceContentResponse struct {
	Node    string                   `json:"node"`
	Storage string                   `json:"storage"`
	Items   []proxmox.StorageContent `json:"items"`
}

// ListImportContent handles GET /api/v1/clusters/:cluster_id/vm-import-sources/content
// ?storage=<name>&node=<name>. It returns the importable volumes/guests (content=import) on
// the given storage as seen from the given node. The node is one that ListImportSources
// already resolved to be online, which is what lets a shared source be browsed even when its
// inventory-owning node is down.
func (h *VMImportHandler) ListImportContent(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "vm_import", clusterID); err != nil {
		return err
	}
	storage := c.Query("storage")
	nodeName := c.Query("node")
	if storage == "" || nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage and node are required")
	}
	node, err := h.queries.GetNodeByClusterAndName(c.Context(), db.GetNodeByClusterAndNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, importClientTimeout)
	if err != nil {
		return err
	}
	items, err := pxClient.GetStorageContentByType(c.Context(), node.Name, storage, "import")
	if err != nil {
		return mapProxmoxError(err)
	}
	if items == nil {
		items = []proxmox.StorageContent{}
	}
	return c.JSON(importSourceContentResponse{
		Node:    node.Name,
		Storage: storage,
		Items:   items,
	})
}

// --- start import ----------------------------------------------------------------------

type startImportRequest struct {
	Node              string `json:"node"`               // node that can see the source volume
	Storage           string `json:"storage"`            // source storage name
	Volume            string `json:"volume"`             // source volid
	SourceFormat      string `json:"source_format"`      // ova|ovf|vmdk|raw|esxi
	SourceAcquisition string `json:"source_acquisition"` // staged|url|esxi|upload
	TargetNode        string `json:"target_node"`
	TargetStorage     string `json:"target_storage"`
	WorkingStorage    string `json:"working_storage"`
	Bridge            string `json:"bridge"`
	VMID              int    `json:"vmid"` // 0 → auto-allocate via /cluster/nextid
	Name              string `json:"name"`
	DiskFormat        string `json:"disk_format"`
	StartAfter        bool   `json:"start_after"`
	LiveImport        bool   `json:"live_import"`
}

// StartVMImport handles POST /api/v1/clusters/:cluster_id/vm-imports. It records an import
// job, dispatches the create-with-import-from call against the chosen target node, tracks
// the resulting Proxmox task, and returns the job. The disk conversion proceeds async and
// is reconciled to a terminal status by the scheduler.
func (h *VMImportHandler) StartVMImport(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm_import", clusterID); err != nil {
		return err
	}
	var req startImportRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Storage == "" || req.Volume == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage and volume are required")
	}
	if req.TargetNode == "" || req.TargetStorage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "target_node and target_storage are required")
	}
	acquisition := req.SourceAcquisition
	switch acquisition {
	case "", "staged":
		acquisition = "staged"
	case "url", "esxi", "upload":
	default:
		return fiber.NewError(fiber.StatusBadRequest, "invalid source_acquisition")
	}
	// A VMID of 0 means auto-allocate; any explicit value must be in Proxmox's valid range
	// (100–999999999). Reject early with a clear message rather than letting PVE fail the
	// create task after the wizard has been completed.
	if req.VMID != 0 && (req.VMID < 100 || req.VMID > 999999999) {
		return fiber.NewError(fiber.StatusBadRequest, "vmid must be between 100 and 999999999")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	node, cluster, pxClient, err := h.resolveNode(c, clusterID, req.TargetNode)
	if err != nil {
		return err
	}

	// Node-targeting safety: a non-shared source storage's volume is only visible on the
	// node that hosts it, so the import-from target node must match the source node.
	if err := h.validateTargetNode(c, clusterID, req.Storage, req.Node, req.TargetNode); err != nil {
		return err
	}

	meta, err := pxClient.GetImportMetadata(c.Context(), req.Node, req.Storage, req.Volume)
	if err != nil {
		return mapProxmoxError(err)
	}

	vmid := req.VMID
	if vmid <= 0 {
		vmid, err = pxClient.GetNextVMID(c.Context())
		if err != nil {
			return mapProxmoxError(err)
		}
	}

	params := proxmox.BuildImportCreateParams(meta, proxmox.ImportCreateOptions{
		VMID:           vmid,
		Name:           req.Name,
		TargetStorage:  req.TargetStorage,
		WorkingStorage: req.WorkingStorage,
		Bridge:         req.Bridge,
		DiskFormat:     req.DiskFormat,
		StartAfter:     req.StartAfter,
		LiveImport:     req.LiveImport,
	})

	warnings, _ := json.Marshal(meta.Warnings)
	if len(warnings) == 0 {
		warnings = json.RawMessage(`[]`)
	}
	options, _ := json.Marshal(map[string]any{
		"working_storage": req.WorkingStorage,
		"bridge":          req.Bridge,
		"disk_format":     req.DiskFormat,
		"start_after":     req.StartAfter,
		"live_import":     req.LiveImport,
	})

	job, err := h.queries.InsertVMImportJob(c.Context(), db.InsertVMImportJobParams{
		ClusterID:         clusterID,
		SourceAcquisition: acquisition,
		SourceFormat:      req.SourceFormat,
		SourceRef:         req.Volume,
		TargetNode:        req.TargetNode,
		TargetStorage:     req.TargetStorage,
		TargetVmid:        safeconv.Int32(vmid),
		Name:              params.Name,
		WarningsJson:      warnings,
		OptionsJson:       options,
		CreatedBy:         userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record import job")
	}

	upid, err := pxClient.CreateVM(c.Context(), node.Name, params)
	if err != nil {
		_ = h.queries.FailVMImportJob(c.Context(), db.FailVMImportJobParams{
			ID:            job.ID,
			FailureReason: err.Error(),
		})
		h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMImport, "vm_import", job.ID.String(), "failed")
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   strconv.Itoa(vmid),
		ResourceName: params.Name,
		Action:       "vm_import",
		UPID:         upid,
		TaskType:     "qmcreate",
		Description:  importDescription(params.Name, vmid, meta.Source),
		Extra:        map[string]any{"vmid": vmid, "source": meta.Source, "job_id": job.ID.String()},
	})

	_ = h.queries.SetVMImportJobUPID(c.Context(), db.SetVMImportJobUPIDParams{ID: job.ID, Upid: upid})
	_ = h.queries.StartVMImportJob(c.Context(), job.ID)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMImport, "vm_import", job.ID.String(), "started")

	job.Upid = upid
	job.Status = "running"
	return c.Status(fiber.StatusAccepted).JSON(toVMImportJobResponse(job))
}

// validateTargetNode enforces that a non-shared source storage is imported on the node
// that actually hosts the source volume. If the storage isn't in Nexara's inventory yet
// (e.g. a freshly-registered ESXi source), the check is skipped and Proxmox will reject
// an impossible placement with a clear error.
func (h *VMImportHandler) validateTargetNode(c fiber.Ctx, clusterID uuid.UUID, storage, sourceNode, targetNode string) error {
	pools, err := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return nil // best-effort; don't block the import on an inventory read
	}
	shared := false
	known := false
	for _, p := range pools {
		if p.Storage == storage {
			known = true
			if p.Shared {
				shared = true
			}
		}
	}
	if known && !shared && sourceNode != "" && targetNode != sourceNode {
		return fiber.NewError(fiber.StatusBadRequest,
			"source storage '"+storage+"' is not shared; the import must target node '"+sourceNode+"' where the source resides")
	}
	return nil
}

// --- list / get ------------------------------------------------------------------------

// ListVMImports handles GET /api/v1/clusters/:cluster_id/vm-imports.
func (h *VMImportHandler) ListVMImports(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "vm_import", clusterID); err != nil {
		return err
	}
	limit := 100
	if v, convErr := strconv.Atoi(c.Query("limit")); convErr == nil && v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, convErr := strconv.Atoi(c.Query("offset")); convErr == nil && v > 0 {
		offset = v
	}
	jobs, err := h.queries.ListVMImportJobsByCluster(c.Context(), db.ListVMImportJobsByClusterParams{
		ClusterID: clusterID,
		Limit:     safeconv.Int32(limit),
		Offset:    safeconv.Int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list import jobs")
	}
	out := make([]vmImportJobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toVMImportJobResponse(j))
	}
	return c.JSON(out)
}

// GetVMImport handles GET /api/v1/clusters/:cluster_id/vm-imports/:id.
func (h *VMImportHandler) GetVMImport(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "vm_import", clusterID); err != nil {
		return err
	}
	job, err := h.getJobForCluster(c, clusterID)
	if err != nil {
		return err
	}
	return c.JSON(toVMImportJobResponse(job))
}

// --- cancel ----------------------------------------------------------------------------

type cancelImportRequest struct {
	DeleteVM bool `json:"delete_vm"`
}

// CancelVMImport handles POST /api/v1/clusters/:cluster_id/vm-imports/:id/cancel. It
// best-effort stops the running create task and, when delete_vm is set, destroys the
// partially-created VM, then marks the job cancelled.
func (h *VMImportHandler) CancelVMImport(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm_import", clusterID); err != nil {
		return err
	}
	job, err := h.getJobForCluster(c, clusterID)
	if err != nil {
		return err
	}
	if job.Status != "pending" && job.Status != "running" {
		return fiber.NewError(fiber.StatusBadRequest, "import is not in a cancellable state")
	}
	var req cancelImportRequest
	_ = c.Bind().Body(&req)

	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, importClientTimeout)
	if err != nil {
		return err
	}

	// Best-effort: signal the running create task to stop.
	if job.Upid != "" {
		_ = pxClient.StopNodeTask(c.Context(), job.TargetNode, job.Upid)
	}

	// Optional cleanup of the partially-created guest.
	if req.DeleteVM && job.TargetVmid > 0 {
		if upid, destroyErr := pxClient.DestroyVM(c.Context(), job.TargetNode, int(job.TargetVmid)); destroyErr == nil {
			TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
				ClusterID:    cluster.ID,
				Node:         job.TargetNode,
				ResourceType: "vm",
				ResourceID:   strconv.Itoa(int(job.TargetVmid)),
				Action:       "destroy",
				UPID:         upid,
				TaskType:     "qmdestroy",
				Description:  "Clean up cancelled import VM " + strconv.Itoa(int(job.TargetVmid)),
				Extra:        map[string]any{"vmid": job.TargetVmid, "job_id": job.ID.String()},
			})
		}
	}

	if err := h.queries.CancelVMImportJob(c.Context(), job.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to cancel import job")
	}
	details, _ := json.Marshal(map[string]any{"job_id": job.ID.String(), "vmid": job.TargetVmid, "delete_vm": req.DeleteVM})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_import", job.ID.String(), "cancel", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMImport, "vm_import", job.ID.String(), "cancelled")

	job.Status = "cancelled"
	return c.JSON(toVMImportJobResponse(job))
}

// --- ESXi source registration ----------------------------------------------------------

type esxiSourceRequest struct {
	Storage              string `json:"storage"` // storage id to create (e.g. "esxi-prod")
	Server               string `json:"server"`
	Username             string `json:"username"`
	Password             string `json:"password"`
	SkipCertVerification bool   `json:"skip_cert_verification"`
	Nodes                string `json:"nodes"` // optional comma-separated node restriction
}

// RegisterEsxiSource handles POST /api/v1/clusters/:cluster_id/vm-import-sources/esxi. It
// registers an ESXi/vCenter host as an "import"-content storage so its guests become
// importable. Credentials are stored by Proxmox under /etc/pve/priv; we never log them.
func (h *VMImportHandler) RegisterEsxiSource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm_import", clusterID); err != nil {
		return err
	}
	var req esxiSourceRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Storage == "" || req.Server == "" || req.Username == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage, server, username and password are required")
	}

	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}

	form := esxiStorageForm(req)
	if err := pxClient.CreateStorage(c.Context(), form); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]any{"storage": req.Storage, "server": req.Server, "type": "esxi"})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", req.Storage, "create_import_source", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "storage", req.Storage, "create")

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "created", "storage": req.Storage})
}

// DeleteImportSource handles DELETE /api/v1/clusters/:cluster_id/vm-import-sources/:storage.
// The param is the Proxmox storage name. The import-source-only guard is enforced against
// the *live* storage config so a just-registered ESXi source (not yet in inventory) can be
// removed, and so a role holding only manage:vm_import cannot delete arbitrary production
// storage — that still requires manage:storage via the storage management endpoint.
func (h *VMImportHandler) DeleteImportSource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm_import", clusterID); err != nil {
		return err
	}
	storageName := c.Params("storage")
	if storageName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage is required")
	}
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetStorageConfig(c.Context(), storageName)
	if err != nil {
		return mapProxmoxError(err)
	}
	if cfg.Type != "esxi" && !storageHasContent(cfg.Content, "import") {
		return fiber.NewError(fiber.StatusBadRequest, "storage is not an import source; use the storage management endpoint")
	}
	if err := pxClient.DeleteStorage(c.Context(), storageName); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"storage": storageName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", storageName, "delete_import_source", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "storage", storageName, "delete")
	return c.JSON(fiber.Map{"status": "deleted", "storage": storageName})
}

// --- enable import content -------------------------------------------------------------

type enableImportContentRequest struct {
	Storage string `json:"storage"`
}

// EnableImportContent handles POST /api/v1/clusters/:cluster_id/vm-import-sources/enable-content.
// It adds the "import" content type to an existing file-based storage by MERGING it into the
// storage's current content list (never replacing it), so a fresh cluster can be made
// import-capable straight from the wizard. Requires manage:storage — changing what a storage
// is used for is a storage-management action, not something manage:vm_import alone permits.
func (h *VMImportHandler) EnableImportContent(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
		return err
	}
	var req enableImportContentRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage is required")
	}
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetStorageConfig(c.Context(), req.Storage)
	if err != nil {
		return mapProxmoxError(err)
	}
	if storageHasContent(cfg.Content, "import") {
		return c.JSON(fiber.Map{"status": "unchanged", "storage": req.Storage, "content": cfg.Content})
	}
	merged := mergeContent(cfg.Content, "import")
	form := url.Values{}
	form.Set("content", merged)
	if err := pxClient.UpdateStorage(c.Context(), req.Storage, form); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"storage": req.Storage, "content": merged})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", req.Storage, "enable_import_content", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "storage", req.Storage, "update")
	return c.JSON(fiber.Map{"status": "updated", "storage": req.Storage, "content": merged})
}

// mergeContent appends want to a comma-separated PVE content list if absent, preserving the
// existing entries and their order.
func mergeContent(content, want string) string {
	for _, part := range strings.Split(content, ",") {
		if strings.TrimSpace(part) == want {
			return content
		}
	}
	if strings.TrimSpace(content) == "" {
		return want
	}
	return content + "," + want
}

// --- helpers ---------------------------------------------------------------------------

func (h *VMImportHandler) getJobForCluster(c fiber.Ctx, clusterID uuid.UUID) (db.VmImportJob, error) {
	var zero db.VmImportJob
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return zero, fiber.NewError(fiber.StatusBadRequest, "Invalid import job ID")
	}
	job, err := h.queries.GetVMImportJob(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, fiber.NewError(fiber.StatusNotFound, "Import job not found")
		}
		return zero, fiber.NewError(fiber.StatusInternalServerError, "Failed to get import job")
	}
	if job.ClusterID != clusterID {
		return zero, fiber.NewError(fiber.StatusNotFound, "Import job not found in this cluster")
	}
	return job, nil
}

func esxiStorageForm(req esxiSourceRequest) url.Values {
	form := url.Values{}
	form.Set("storage", req.Storage)
	form.Set("type", "esxi")
	form.Set("server", req.Server)
	form.Set("username", req.Username)
	form.Set("password", req.Password)
	form.Set("content", "import")
	if req.SkipCertVerification {
		form.Set("skip-cert-verification", "1")
	}
	if req.Nodes != "" {
		form.Set("nodes", req.Nodes)
	}
	return form
}

type vmImportJobResponse struct {
	ID                string          `json:"id"`
	ClusterID         string          `json:"cluster_id"`
	SourceAcquisition string          `json:"source_acquisition"`
	SourceFormat      string          `json:"source_format"`
	SourceRef         string          `json:"source_ref"`
	TargetNode        string          `json:"target_node"`
	TargetStorage     string          `json:"target_storage"`
	TargetVMID        int32           `json:"target_vmid"`
	Name              string          `json:"name"`
	Status            string          `json:"status"`
	UPID              string          `json:"upid,omitempty"`
	FailureReason     string          `json:"failure_reason,omitempty"`
	Warnings          json.RawMessage `json:"warnings"`
	Options           json.RawMessage `json:"options"`
	CreatedBy         string          `json:"created_by"`
	StartedAt         string          `json:"started_at,omitempty"`
	CompletedAt       string          `json:"completed_at,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

func toVMImportJobResponse(j db.VmImportJob) vmImportJobResponse {
	warnings := j.WarningsJson
	if len(warnings) == 0 || string(warnings) == "null" {
		warnings = json.RawMessage(`[]`)
	}
	options := j.OptionsJson
	if len(options) == 0 || string(options) == "null" {
		options = json.RawMessage(`{}`)
	}
	return vmImportJobResponse{
		ID:                j.ID.String(),
		ClusterID:         j.ClusterID.String(),
		SourceAcquisition: j.SourceAcquisition,
		SourceFormat:      j.SourceFormat,
		SourceRef:         j.SourceRef,
		TargetNode:        j.TargetNode,
		TargetStorage:     j.TargetStorage,
		TargetVMID:        j.TargetVmid,
		Name:              j.Name,
		Status:            j.Status,
		UPID:              j.Upid,
		FailureReason:     j.FailureReason,
		Warnings:          warnings,
		Options:           options,
		CreatedBy:         j.CreatedBy.String(),
		StartedAt:         formatTimestamptz(j.StartedAt),
		CompletedAt:       formatTimestamptz(j.CompletedAt),
		CreatedAt:         j.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:         j.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func importDescription(name string, vmid int, source string) string {
	label := name
	if label == "" {
		label = "VM " + strconv.Itoa(vmid)
	}
	if source != "" {
		return "Import " + label + " from " + source
	}
	return "Import " + label
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
