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

	"github.com/bigjakk/nexara/internal/api/apischema"
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

type importMetadataResponse struct {
	Type       string                        `json:"type"`
	Source     string                        `json:"source"`
	Name       string                        `json:"name"`
	Cores      int                           `json:"cores"`
	Sockets    int                           `json:"sockets"`
	Memory     int                           `json:"memory"`
	OSType     string                        `json:"ostype"`
	CreateArgs map[string]string             `json:"create_args"`
	Disks      map[string]proxmox.ImportDisk `json:"disks"`
	Warnings   []proxmox.ImportWarning       `json:"warnings"`
}

// GetImportMetadata handles POST /api/v1/clusters/:cluster_id/import-metadata. It parses an
// importable OVA/OVF/ESXi guest and returns the pre-filled guest definition for the wizard.
// POST (not GET) is used so the slash/colon-laden source volid travels in the JSON body.
func (h *VMImportHandler) GetImportMetadata(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node, storage, volume := p.String("node"), p.String("storage"), p.String("volume")
	_, _, pxClient, err := h.resolveNode(c, clusterID, node)
	if err != nil {
		return err
	}
	meta, err := pxClient.GetImportMetadata(c.Context(), node, storage, volume)
	if err != nil {
		return mapProxmoxError(err)
	}
	args := meta.FlatCreateArgs()
	// Report the normalized vCPU topology (fold sockets into cores when the source gives no
	// explicit core count) so the wizard's Inspect/Customize/Review show exactly what the
	// import will produce — the same NormalizeVCPU BuildImportCreateParams applies.
	cores, sockets := proxmox.NormalizeVCPU(atoiOrZero(args["cores"]), atoiOrZero(args["sockets"]))
	return c.JSON(importMetadataResponse{
		Type:       meta.Type,
		Source:     meta.Source,
		Name:       args["name"],
		Cores:      cores,
		Sockets:    sockets,
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
func (h *VMImportHandler) QueryURLMetadata(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	rawURL := p.String("url")
	if !isHTTPURL(rawURL) {
		return fiber.NewError(fiber.StatusBadRequest, "url must be an http or https URL")
	}
	nodeName, err := h.pickImportNode(c, clusterID, p.String("node"))
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
func (h *VMImportHandler) ListImportSources(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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
	return RespondItems(c, entries)
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
func (h *VMImportHandler) ListImportContent(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	storage := p.String("storage")
	nodeName := p.String("node")
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
	Node              string // node that can see the source volume
	Storage           string // source storage name
	Volume            string // source volid
	SourceFormat      string // ova|ovf|vmdk|raw|esxi
	SourceAcquisition string // staged|url|esxi|upload
	TargetNode        string
	TargetStorage     string
	WorkingStorage    string
	Bridge            string
	VMID              int // 0 → auto-allocate via /cluster/nextid
	Name              string
	DiskFormat        string
	StartAfter        bool
	LiveImport        bool

	// Guest-config overrides (empty/zero = keep the source-derived value). These give the
	// import wizard the same knobs as Create VM, minus adding hardware.
	Cores       int
	Sockets     int
	Memory      int // MiB
	CPUType     string
	OSType      string
	BIOS        string
	Machine     string
	ScsiHW      string
	Pool        string
	Tags        string
	Description string
	OnBoot      *bool
	Agent       *bool
	Numa        *bool

	// Network options for the synthesised net0 (applied only when Bridge is set).
	NetModel   string
	VLANTag    int
	Firewall   *bool
	MACAddress string
	RateLimit  string
	MTU        int
	Multiqueue int
}

// startImportRequestFromParams reads the import body.
//
// The three *bool fields stay pointers because proxmox.ImportCreateOptions
// sends the corresponding Proxmox key only when one is non-nil: omitting
// onboot, agent or numa means "keep whatever the source metadata derived",
// which is different from sending it as false. OptBool reports which of the
// two the caller meant, with or without a declared default, which it never
// reports as supplied (apischema.Property.Default).
func startImportRequestFromParams(p *apischema.Params) startImportRequest {
	return startImportRequest{
		Node:              p.String("node"),
		Storage:           p.String("storage"),
		Volume:            p.String("volume"),
		SourceFormat:      p.String("source_format"),
		SourceAcquisition: p.String("source_acquisition"),
		TargetNode:        p.String("target_node"),
		TargetStorage:     p.String("target_storage"),
		WorkingStorage:    p.String("working_storage"),
		Bridge:            p.String("bridge"),
		VMID:              int(p.Int("vmid")),
		Name:              p.String("name"),
		DiskFormat:        p.String("disk_format"),
		StartAfter:        p.Bool("start_after"),
		LiveImport:        p.Bool("live_import"),

		Cores:       int(p.Int("cores")),
		Sockets:     int(p.Int("sockets")),
		Memory:      int(p.Int("memory")),
		CPUType:     p.String("cpu_type"),
		OSType:      p.String("os_type"),
		BIOS:        p.String("bios"),
		Machine:     p.String("machine"),
		ScsiHW:      p.String("scsihw"),
		Pool:        p.String("pool"),
		Tags:        p.String("tags"),
		Description: p.String("description"),
		OnBoot:      optBoolPtr(p.OptBool("onboot")),
		Agent:       optBoolPtr(p.OptBool("agent")),
		Numa:        optBoolPtr(p.OptBool("numa")),

		NetModel:   p.String("net_model"),
		VLANTag:    int(p.Int("vlan_tag")),
		Firewall:   optBoolPtr(p.OptBool("firewall")),
		MACAddress: p.String("mac_address"),
		RateLimit:  p.String("rate_limit"),
		MTU:        int(p.Int("mtu")),
		Multiqueue: int(p.Int("multiqueue")),
	}
}

// StartVMImport handles POST /api/v1/clusters/:cluster_id/vm-imports. It records an import
// job, dispatches the create-with-import-from call against the chosen target node, tracks
// the resulting Proxmox task, and returns the job. The disk conversion proceeds async and
// is reconciled to a terminal status by the scheduler.
func (h *VMImportHandler) StartVMImport(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := startImportRequestFromParams(p)
	// The enum on source_acquisition accepts the empty string, which has
	// always meant "staged"; this is the normalisation, not the check.
	acquisition := req.SourceAcquisition
	if acquisition == "" {
		acquisition = "staged"
	}
	// A VMID of 0 means auto-allocate; any explicit value must be in Proxmox's valid range
	// (100–999999999). The schema bounds the upper end and 0; the 1–99 gap is the part a
	// single numeric range cannot express, so it stays here.
	if req.VMID != 0 && req.VMID < 100 {
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
		Cores:          req.Cores,
		Sockets:        req.Sockets,
		MemoryMiB:      req.Memory,
		CPUType:        req.CPUType,
		OSType:         req.OSType,
		BIOS:           req.BIOS,
		Machine:        req.Machine,
		ScsiHW:         req.ScsiHW,
		Pool:           req.Pool,
		Tags:           req.Tags,
		Description:    req.Description,
		OnBoot:         req.OnBoot,
		Agent:          req.Agent,
		Numa:           req.Numa,
		NetModel:       req.NetModel,
		VLANTag:        req.VLANTag,
		Firewall:       req.Firewall,
		MACAddr:        req.MACAddress,
		RateLimit:      req.RateLimit,
		MTU:            req.MTU,
		Multiqueue:     req.Multiqueue,
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
func (h *VMImportHandler) ListVMImports(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobs, err := h.queries.ListVMImportJobsByCluster(c.Context(), db.ListVMImportJobsByClusterParams{
		ClusterID: clusterID,
		Limit:     safeconv.Int32(int(p.Int("limit"))),
		Offset:    safeconv.Int32(int(p.Int("offset"))),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list import jobs")
	}
	out := make([]vmImportJobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toVMImportJobResponse(j))
	}
	return RespondItems(c, out)
}

// GetVMImport handles GET /api/v1/clusters/:cluster_id/vm-imports/:id.
func (h *VMImportHandler) GetVMImport(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	job, err := h.getJobForCluster(c, p, clusterID)
	if err != nil {
		return err
	}
	return c.JSON(toVMImportJobResponse(job))
}

// --- cancel ----------------------------------------------------------------------------

// CancelVMImport handles POST /api/v1/clusters/:cluster_id/vm-imports/:id/cancel. It
// best-effort stops the running create task and, when delete_vm is set, destroys the
// partially-created VM, then marks the job cancelled.
func (h *VMImportHandler) CancelVMImport(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	job, err := h.getJobForCluster(c, p, clusterID)
	if err != nil {
		return err
	}
	if job.Status != "pending" && job.Status != "running" {
		return fiber.NewError(fiber.StatusBadRequest, "import is not in a cancellable state")
	}
	deleteVM := p.Bool("delete_vm")

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
	if deleteVM && job.TargetVmid > 0 {
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
	details, _ := json.Marshal(map[string]any{"job_id": job.ID.String(), "vmid": job.TargetVmid, "delete_vm": deleteVM})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_import", job.ID.String(), "cancel", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMImport, "vm_import", job.ID.String(), "cancelled")

	job.Status = "cancelled"
	return c.JSON(toVMImportJobResponse(job))
}

// --- ESXi source registration ----------------------------------------------------------

type esxiSourceRequest struct {
	Storage              string // storage id to create (e.g. "esxi-prod")
	Server               string
	Username             string
	Password             string
	SkipCertVerification bool
	Nodes                string // optional comma-separated node restriction
}

func esxiSourceRequestFromParams(p *apischema.Params) esxiSourceRequest {
	return esxiSourceRequest{
		Storage:              p.String("storage"),
		Server:               p.String("server"),
		Username:             p.String("username"),
		Password:             p.String("password"),
		SkipCertVerification: p.Bool("skip_cert_verification"),
		Nodes:                p.String("nodes"),
	}
}

// RegisterEsxiSource handles POST /api/v1/clusters/:cluster_id/vm-import-sources/esxi. It
// registers an ESXi/vCenter host as an "import"-content storage so its guests become
// importable. Credentials are stored by Proxmox under /etc/pve/priv; we never log them.
func (h *VMImportHandler) RegisterEsxiSource(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := esxiSourceRequestFromParams(p)

	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}

	form := esxiStorageForm(req)
	// The result only ever carries a PBS encryption key Proxmox generated for
	// encryption-key=autogen, which an ESXi source never sends.
	if _, err := pxClient.CreateStorage(c.Context(), form); err != nil {
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
func (h *VMImportHandler) DeleteImportSource(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	storageName := p.String("storage")
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

// EnableImportContent handles POST /api/v1/clusters/:cluster_id/vm-import-sources/enable-content.
// It adds the "import" content type to an existing file-based storage by MERGING it into the
// storage's current content list (never replacing it), so a fresh cluster can be made
// import-capable straight from the wizard. Requires manage:storage — changing what a storage
// is used for is a storage-management action, not something manage:vm_import alone permits.
func (h *VMImportHandler) EnableImportContent(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	storage := p.String("storage")
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetStorageConfig(c.Context(), storage)
	if err != nil {
		return mapProxmoxError(err)
	}
	if storageHasContent(cfg.Content, "import") {
		return c.JSON(fiber.Map{"status": "unchanged", "storage": storage, "content": cfg.Content})
	}
	merged := mergeContent(cfg.Content, "import")
	form := url.Values{}
	form.Set("content", merged)
	if _, err := pxClient.UpdateStorage(c.Context(), storage, form, nil); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"storage": storage, "content": merged})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", storage, "enable_import_content", details)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "storage", storage, "update")
	return c.JSON(fiber.Map{"status": "updated", "storage": storage, "content": merged})
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

func (h *VMImportHandler) getJobForCluster(c fiber.Ctx, p *apischema.Params, clusterID uuid.UUID) (db.VmImportJob, error) {
	var zero db.VmImportJob
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return zero, err
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
