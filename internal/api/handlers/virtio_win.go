package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/safeconv"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// VirtioWinHandler serves the virtio-win ISO catalog and per-cluster
// auto-download policy.
//
// Everything here is gated on storage permissions rather than a new resource:
// the effect of this feature is "a Proxmox node fetches a URL into a storage",
// which is exactly what manage:storage already authorises on the existing
// download-url endpoint. A separate permission would add a five-minute 403
// window after the migration (the RBAC cache has no bulk flush) to grant an
// operator something they can already do by hand.
type VirtioWinHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
	engine   *virtiowin.Engine // from the composition root; may be nil in tests
}

// NewVirtioWinHandler creates the handler. engine comes from internal/app.
func NewVirtioWinHandler(queries *db.Queries, eventPub *events.Publisher, engine *virtiowin.Engine) *VirtioWinHandler {
	return &VirtioWinHandler{queries: queries, eventPub: eventPub, engine: engine}
}

// --- Request / Response types ---

type virtioWinConfigRequest struct {
	Enabled       bool   `json:"enabled"`
	Storage       string `json:"storage"`
	Node          string `json:"node"`
	TargetVersion string `json:"target_version"`
	// A POINTER because prune DELETES ISOs. Absent must mean "leave the stored
	// value alone", not false: a plain bool would let a client that predates
	// the field disarm an operator's pruning on its next save, and coercing
	// absent to true would arm destructive behaviour nobody asked for. See
	// UpsertVirtioWinConfig, which does the preserving.
	PruneEnabled *bool `json:"prune_enabled"`
}

type virtioWinConfigResponse struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	Enabled       bool      `json:"enabled"`
	Storage       string    `json:"storage"`
	Node          string    `json:"node"`
	TargetVersion string    `json:"target_version"`
	PruneEnabled  bool      `json:"prune_enabled"`
	LastCheckAt   *string   `json:"last_check_at"`
	LastError     string    `json:"last_error"`
	// EffectiveVersion is what the cluster will actually hold: the pin when set,
	// otherwise upstream's current stable. Surfaced so the UI never has to
	// re-derive the precedence rule and get it subtly different.
	EffectiveVersion string `json:"effective_version"`
}

type virtioWinReleaseResponse struct {
	Version           string  `json:"version"`
	ISOVersion        string  `json:"iso_version"`
	ISOFilename       string  `json:"iso_filename"`
	ISOURL            string  `json:"iso_url"`
	ISOSize           int64   `json:"iso_size"`
	IsStable          bool    `json:"is_stable"`
	Checksum          string  `json:"checksum"`
	ChecksumAlgorithm string  `json:"checksum_algorithm"`
	PublishedAt       *string `json:"published_at"`
	DiscoveredAt      string  `json:"discovered_at"`
}

type virtioWinDownloadResponse struct {
	ID          uuid.UUID `json:"id"`
	ClusterID   uuid.UUID `json:"cluster_id"`
	Node        string    `json:"node"`
	Storage     string    `json:"storage"`
	Version     string    `json:"version"`
	Filename    string    `json:"filename"`
	Status      string    `json:"status"`
	UPID        string    `json:"upid"`
	Error       string    `json:"error"`
	TriggeredBy string    `json:"triggered_by"`
	StartedAt   string    `json:"started_at"`
	FinishedAt  *string   `json:"finished_at"`
}

func toVirtioWinReleaseResponse(r db.VirtioWinRelease) virtioWinReleaseResponse {
	resp := virtioWinReleaseResponse{
		Version:           r.Version,
		ISOVersion:        r.IsoVersion,
		ISOFilename:       r.IsoFilename,
		ISOURL:            r.IsoUrl,
		ISOSize:           r.IsoSize,
		IsStable:          r.IsStable,
		Checksum:          r.Checksum,
		ChecksumAlgorithm: r.ChecksumAlgorithm,
		DiscoveredAt:      r.DiscoveredAt.Format(time.RFC3339),
	}
	if r.PublishedAt.Valid {
		s := r.PublishedAt.Time.Format(time.RFC3339)
		resp.PublishedAt = &s
	}
	return resp
}

func toVirtioWinDownloadResponse(d db.VirtioWinDownload) virtioWinDownloadResponse {
	resp := virtioWinDownloadResponse{
		ID:          d.ID,
		ClusterID:   d.ClusterID,
		Node:        d.Node,
		Storage:     d.Storage,
		Version:     d.Version,
		Filename:    d.Filename,
		Status:      d.Status,
		UPID:        d.Upid,
		Error:       d.Error,
		TriggeredBy: d.TriggeredBy,
		StartedAt:   d.StartedAt.Format(time.RFC3339),
	}
	if d.FinishedAt.Valid {
		s := d.FinishedAt.Time.Format(time.RFC3339)
		resp.FinishedAt = &s
	}
	return resp
}

// --- Handlers ---

// ListReleases returns the known upstream virtio-win catalog.
//
// The catalog is global rather than per-cluster, so this is gated on the
// instance-wide view:storage rather than a cluster permission.
func (h *VirtioWinHandler) ListReleases(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "storage"); err != nil {
		return err
	}
	releases, err := h.queries.ListVirtioWinReleases(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list virtio-win releases")
	}
	out := make([]virtioWinReleaseResponse, 0, len(releases))
	for _, r := range releases {
		out = append(out, toVirtioWinReleaseResponse(r))
	}
	return RespondItems(c, out)
}

// GetConfig returns a cluster's auto-download policy, defaulting to disabled.
func (h *VirtioWinHandler) GetConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "storage", clusterID); err != nil {
		return err
	}

	cfg, err := h.queries.GetVirtioWinConfig(c.Context(), clusterID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			// Do NOT fall through to the default here. Rendering a real read
			// failure as "never configured" shows the operator an empty form,
			// and their next save would assert those blanks over a live config.
			return fiber.NewError(fiber.StatusInternalServerError, "failed to read virtio-win config")
		}
		// No row means the feature was never configured — the default state.
		cfg = db.VirtioWinConfig{ClusterID: clusterID}
	}

	resp := virtioWinConfigResponse{
		ClusterID:     cfg.ClusterID,
		Enabled:       cfg.Enabled,
		Storage:       cfg.Storage,
		Node:          cfg.Node,
		TargetVersion: cfg.TargetVersion,
		PruneEnabled:  cfg.PruneEnabled,
		LastError:     cfg.LastError,
	}
	if cfg.LastCheckAt.Valid {
		s := cfg.LastCheckAt.Time.Format(time.RFC3339)
		resp.LastCheckAt = &s
	}
	if h.engine != nil {
		if target, err := h.engine.ResolveTarget(c.Context(), cfg); err == nil {
			resp.EffectiveVersion = target.Version
		}
	}
	return c.JSON(resp)
}

// UpdateConfig writes a cluster's auto-download policy.
func (h *VirtioWinHandler) UpdateConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
		return err
	}

	var req virtioWinConfigRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Enabled && req.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage is required when auto-download is enabled")
	}
	if req.TargetVersion != "" && !virtiowin.ValidVersion(req.TargetVersion) {
		return fiber.NewError(fiber.StatusBadRequest, "target_version is not a valid virtio-win version")
	}

	cfg, err := h.queries.UpsertVirtioWinConfig(c.Context(), db.UpsertVirtioWinConfigParams{
		ClusterID:     clusterID,
		Enabled:       req.Enabled,
		Storage:       req.Storage,
		Node:          req.Node,
		TargetVersion: req.TargetVersion,
		PruneEnabled:  optionalBool(req.PruneEnabled),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save virtio-win config")
	}

	// Enabling is the moment the operator expects to see something. The
	// scheduler deliberately does not fetch upstream until a cluster opts in,
	// so without this the version list stays empty and nothing happens for up
	// to six hours. Best-effort: a save must not fail because upstream is down.
	if cfg.Enabled && h.engine != nil {
		if err := h.engine.EnsureCatalog(c.Context()); err != nil {
			slog.Warn("virtio-win: could not populate the release catalog on enable", "error", err)
		}
	}

	details, _ := json.Marshal(map[string]any{
		"enabled":        cfg.Enabled,
		"storage":        cfg.Storage,
		"node":           cfg.Node,
		"target_version": cfg.TargetVersion,
		"prune_enabled":  cfg.PruneEnabled,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", cfg.Storage, "virtio_win_config_update", details)

	resp := virtioWinConfigResponse{
		ClusterID:     cfg.ClusterID,
		Enabled:       cfg.Enabled,
		Storage:       cfg.Storage,
		Node:          cfg.Node,
		TargetVersion: cfg.TargetVersion,
		PruneEnabled:  cfg.PruneEnabled,
		LastError:     cfg.LastError,
	}
	if h.engine != nil {
		if target, err := h.engine.ResolveTarget(c.Context(), cfg); err == nil {
			resp.EffectiveVersion = target.Version
		}
	}
	return c.JSON(resp)
}

type virtioWinDownloadRequest struct {
	Version string `json:"version"`
}

// Download dispatches an immediate download of one version to the cluster's
// configured storage.
func (h *VirtioWinHandler) Download(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
		return err
	}
	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "virtio-win engine is not available")
	}

	var req virtioWinDownloadRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	cfg, err := h.queries.GetVirtioWinConfig(c.Context(), clusterID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// A read failure is not the operator forgetting to configure a storage;
		// telling them to go configure one sends them to fix the wrong thing.
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read virtio-win config")
	}
	if cfg.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "configure a target ISO storage for this cluster first")
	}

	version := req.Version
	if version == "" {
		target, err := h.engine.ResolveTarget(c.Context(), cfg)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "no version specified and no target could be resolved")
		}
		version = target.Version
	}
	if !virtiowin.ValidVersion(version) {
		return fiber.NewError(fiber.StatusBadRequest, "version is not a valid virtio-win version")
	}

	download, err := h.engine.DownloadNow(c.Context(), cfg, version)
	if err != nil {
		switch {
		case errors.Is(err, virtiowin.ErrNoTarget):
			return fiber.NewError(fiber.StatusNotFound, err.Error())
		case errors.Is(err, virtiowin.ErrDownloadInFlight):
			// Two operators clicking at once, or a click racing a scheduler
			// tick. The requested state is already being reached, so this is
			// not an error to show the caller.
			return c.JSON(fiber.Map{"status": "already_running", "version": version})
		default:
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
	}
	if download == nil {
		// Already present on the storage. Not an error — the caller's intent
		// ("hold this version") is satisfied.
		return c.JSON(fiber.Map{"status": "already_present", "version": version})
	}

	// download-url returns a UPID, so this is a tracked Proxmox task like any
	// other, not a bare audit row.
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         download.Node,
		ResourceType: "storage",
		ResourceID:   download.Storage,
		ResourceName: download.Storage,
		Action:       "virtio_win_download",
		UPID:         download.Upid,
		TaskType:     "download",
		Description:  "Download virtio-win " + download.Version + " to " + download.Storage,
		Extra: map[string]any{
			"version":  download.Version,
			"filename": download.Filename,
			"storage":  download.Storage,
		},
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(),
		events.KindVirtioWinChange, "storage", download.Storage, "virtio_win_download")

	return c.Status(fiber.StatusAccepted).JSON(toVirtioWinDownloadResponse(*download))
}

// ListDownloads returns a cluster's download history, most recent first.
func (h *VirtioWinHandler) ListDownloads(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "storage", clusterID); err != nil {
		return err
	}

	limit := int32(50)
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = safeconv.Int32(n)
		}
	}

	rows, err := h.queries.ListVirtioWinDownloadsByCluster(c.Context(), db.ListVirtioWinDownloadsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list virtio-win downloads")
	}
	total, err := h.queries.CountVirtioWinDownloadsByCluster(c.Context(), clusterID)
	if err != nil {
		total = int64(len(rows))
	}

	out := make([]virtioWinDownloadResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toVirtioWinDownloadResponse(r))
	}
	return RespondList(c, out, total)
}
