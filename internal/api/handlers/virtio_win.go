package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	// CheckSchedule is a five-field cron expression; empty means every six
	// hours. CheckTimezone is the IANA zone it is read in; empty means server
	// time, which in a container is all but always UTC.
	CheckSchedule string `json:"check_schedule"`
	CheckTimezone string `json:"check_timezone"`
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
	CheckSchedule string    `json:"check_schedule"`
	CheckTimezone string    `json:"check_timezone"`
	// NextCheckAt is null when a check is due now — a cluster just enabled, or
	// one that has never run. The UI renders that as "on the next tick" rather
	// than as a missing value.
	NextCheckAt *string `json:"next_check_at"`
	// EffectiveVersion is what the cluster will actually hold: the pin when set,
	// otherwise upstream's current stable. Surfaced so the UI never has to
	// re-derive the precedence rule and get it subtly different.
	EffectiveVersion string `json:"effective_version"`
	// SourceURL is the download root in force, so an operator reading a failed
	// check can see whether it went to upstream or to their mirror without
	// opening another screen. Instance-wide; see virtioWinMirrorSettingKey.
	SourceURL string `json:"source_url"`
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

// toVirtioWinConfigResponse renders one config. Shared by GET, PUT and the
// manual check so the three cannot drift on which derived fields they fill in —
// the effective version in particular, which the UI does not re-derive.
func (h *VirtioWinHandler) toVirtioWinConfigResponse(c fiber.Ctx, cfg db.VirtioWinConfig) virtioWinConfigResponse {
	resp := virtioWinConfigResponse{
		ClusterID:     cfg.ClusterID,
		Enabled:       cfg.Enabled,
		Storage:       cfg.Storage,
		Node:          cfg.Node,
		TargetVersion: cfg.TargetVersion,
		PruneEnabled:  cfg.PruneEnabled,
		LastError:     cfg.LastError,
		CheckSchedule: cfg.CheckSchedule,
		CheckTimezone: cfg.CheckTimezone,
		SourceURL:     virtiowin.BaseURL,
	}
	if cfg.LastCheckAt.Valid {
		t := cfg.LastCheckAt.Time.Format(time.RFC3339)
		resp.LastCheckAt = &t
	}
	if cfg.NextCheckAt.Valid {
		t := cfg.NextCheckAt.Time.Format(time.RFC3339)
		resp.NextCheckAt = &t
	}
	if h.engine != nil {
		if target, err := h.engine.ResolveTarget(c.Context(), cfg); err == nil {
			resp.EffectiveVersion = target.Version
		}
		resp.SourceURL = h.engine.ResolveBase(c.Context())
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

	return c.JSON(h.toVirtioWinConfigResponse(c, cfg))
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
	// Rejected here rather than stored, because the scheduler's fallback for an
	// unparseable expression is the six-hourly default: a typo would otherwise
	// be silently ignored instead of reported.
	if err := virtiowin.ValidateSchedule(req.CheckSchedule, req.CheckTimezone); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	cfg, err := h.queries.UpsertVirtioWinConfig(c.Context(), db.UpsertVirtioWinConfigParams{
		ClusterID:     clusterID,
		Enabled:       req.Enabled,
		Storage:       req.Storage,
		Node:          req.Node,
		TargetVersion: req.TargetVersion,
		PruneEnabled:  optionalBool(req.PruneEnabled),
		CheckSchedule: req.CheckSchedule,
		CheckTimezone: req.CheckTimezone,
		// Only applied when the schedule or its zone actually changed; the
		// statement decides, so a save that touches neither cannot push the
		// pending check out. See UpsertVirtioWinConfig.
		NextCheckAt: pgtype.Timestamptz{
			Time:  virtiowin.NextCheck(req.CheckSchedule, req.CheckTimezone, time.Now()),
			Valid: true,
		},
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
		"check_schedule": cfg.CheckSchedule,
		"check_timezone": cfg.CheckTimezone,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", cfg.Storage, "virtio_win_config_update", details)

	return c.JSON(h.toVirtioWinConfigResponse(c, cfg))
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

// CheckNow runs a cluster's scheduled check immediately: refresh the catalog
// from the configured source, then reconcile the cluster's storage against its
// target. It records the outcome the same way the scheduler does, so the next
// check moves to the schedule's next slot rather than firing again straight
// after.
//
// This is not "Download now" with a different name. Download now forces one
// specific version; this answers "is there anything new, and is my storage in
// line" — which is the only way an operator can tell whether the schedule and
// the source they just configured actually work, without waiting for 03:00.
func (h *VirtioWinHandler) CheckNow(c fiber.Ctx) error {
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

	cfg, err := h.queries.GetVirtioWinConfig(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusBadRequest, "configure a target ISO storage for this cluster first")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read virtio-win config")
	}
	if cfg.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "configure a target ISO storage for this cluster first")
	}
	// This is the manual trigger for the automatic cycle, so it has no meaning
	// with the cycle switched off — and running it anyway would dispatch an
	// ~840 MiB fetch on a cluster whose operator has just said not to, then
	// stamp check timestamps on a row the scheduler never looks at. "Download
	// now" is the button for fetching without opting in.
	if !cfg.Enabled {
		return fiber.NewError(fiber.StatusBadRequest,
			"automatic downloads are off for this cluster — turn them on to run a check, or use Download now")
	}

	// A refresh failure is not fatal: the cluster may still be behind on a
	// version already in the catalog, and syncing that is worth doing even when
	// the source is unreachable. It is recorded as the check's error either way.
	var checkErr error
	if _, err := h.engine.RefreshCatalog(c.Context()); err != nil {
		checkErr = err
		slog.Warn("virtio-win: manual check could not refresh the catalog", "cluster_id", clusterID, "error", err)
	}

	download, syncErr := h.engine.SyncCluster(c.Context(), cfg)
	if syncErr != nil {
		checkErr = syncErr
	}
	h.engine.MarkChecked(c.Context(), cfg, checkErr)

	if download != nil {
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
	}

	// Re-read so the response carries the timestamps MarkChecked just wrote.
	updated, err := h.queries.GetVirtioWinConfig(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "check ran, but its outcome could not be read back")
	}
	resp := h.toVirtioWinConfigResponse(c, updated)
	if download != nil {
		return c.JSON(fiber.Map{"config": resp, "download": toVirtioWinDownloadResponse(*download)})
	}
	return c.JSON(fiber.Map{"config": resp})
}

// --- Mirror (instance-wide download source) ---

// virtioWinMirrorSettingKey is virtiowin.MirrorSettingKey repeated as a local
// literal. TestGuard_GlobalSettingKeysClassified can only resolve keys written
// as a literal or a const in this package, and a qualified selector would trip
// it; TestVirtioWinMirrorSettingKeyMatches keeps the two in step.
const virtioWinMirrorSettingKey = "virtio_win.mirror"

type virtioWinMirrorRequest struct {
	// BaseURL replaces the upstream download root. Empty clears the override.
	BaseURL string `json:"base_url"`
	// AllowPrivateAddress confirms a base that resolves to a private or
	// loopback address — which an internal mirror always does. Same
	// warn-then-confirm shape as adding a cluster or a PBS server.
	AllowPrivateAddress bool `json:"allow_private_address,omitempty"`
	// AllowInsecure confirms a plain-http base. Separate from the address
	// confirmation because it is a different risk: a private address exposes
	// nothing, whereas http means the driver media a Windows guest installs
	// arrives unauthenticated over the wire.
	AllowInsecure bool `json:"allow_insecure,omitempty"`
}

type virtioWinMirrorResponse struct {
	// BaseURL is the configured override, empty when following upstream.
	BaseURL string `json:"base_url"`
	// EffectiveURL is the root actually in use: the override, or upstream.
	EffectiveURL string `json:"effective_url"`
	// UpstreamURL is what "no override" resolves to, so the UI can show the
	// default without hardcoding a copy of it.
	UpstreamURL string `json:"upstream_url"`
}

func (h *VirtioWinHandler) mirrorResponse(c fiber.Ctx, base string) virtioWinMirrorResponse {
	resp := virtioWinMirrorResponse{
		BaseURL:      base,
		EffectiveURL: virtiowin.BaseURL,
		UpstreamURL:  virtiowin.BaseURL,
	}
	if h.engine != nil {
		resp.EffectiveURL = h.engine.ResolveBase(c.Context())
	} else if base != "" {
		resp.EffectiveURL = base
	}
	return resp
}

// GetMirror returns the instance-wide download source.
//
// Read is gated on view:storage — the same permission that already shows every
// release's iso_url — rather than on manage:settings, so the operator reading a
// failed check on the cluster page can see where it was pointed.
func (h *VirtioWinHandler) GetMirror(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "storage"); err != nil {
		return err
	}
	base := ""
	row, err := h.queries.GetSetting(c.Context(), db.GetSettingParams{
		Key:     virtioWinMirrorSettingKey,
		Scope:   "global",
		ScopeID: pgtype.UUID{},
	})
	switch {
	case err == nil:
		var mirror virtiowin.Mirror
		if jsonErr := json.Unmarshal(row.Value, &mirror); jsonErr == nil {
			base = mirror.BaseURL
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read the virtio-win source")
	}
	return c.JSON(h.mirrorResponse(c, base))
}

// SetMirror writes the instance-wide download source.
//
// Instance-wide, so it is gated on manage:settings rather than manage:storage:
// one write here redirects every cluster's downloads at once, which is a
// broader blast radius than a storage operator has anywhere else. The value is
// not a credential — nothing authenticates to it — so this is about scope of
// effect, not secrecy.
func (h *VirtioWinHandler) SetMirror(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "settings"); err != nil {
		return err
	}

	var req virtioWinMirrorRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	base, err := virtiowin.NormalizeBase(req.BaseURL)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if base != "" {
		// Plain http is allowed but never by accident. The ISO it points at is
		// installed as kernel-mode drivers inside Windows guests, and upstream
		// publishes no checksum to fall back on, so an unauthenticated fetch is
		// worth one deliberate click. Per-release checksums remain available.
		if strings.HasPrefix(base, "http://") && !req.AllowInsecure {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"error": "insecure_source_confirm_required",
				"message": "This source uses plain HTTP, so the ISO is fetched without integrity or authenticity " +
					"protection — and upstream publishes no checksum for it. Re-submit with allow_insecure=true to confirm.",
			})
		}
		// An internal mirror resolves to a private address by definition, so
		// this is the expected path, not an edge case. Always-blocked classes
		// (cloud metadata and friends) are still refused outright, and
		// netguard's dial guard re-checks at connect time.
		if err := enforceURLAddressPolicy(c.Context(), base, req.AllowPrivateAddress); err != nil {
			return renderAddressPolicyError(c, err)
		}
	}

	value, err := json.Marshal(virtiowin.Mirror{BaseURL: base})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to encode the virtio-win source")
	}
	if _, err := h.queries.UpsertSetting(c.Context(), db.UpsertSettingParams{
		Key:     virtioWinMirrorSettingKey,
		Value:   value,
		Scope:   "global",
		ScopeID: pgtype.UUID{},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save the virtio-win source")
	}

	details, _ := json.Marshal(map[string]any{"base_url": base})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, settingResourceType, virtioWinMirrorSettingKey,
		"virtio_win_source_update", details)

	// The catalog was discovered against the previous source: its versions and
	// the URLs recorded for them no longer describe where the ISOs live. The
	// next scheduled check restates both, and dispatch rebuilds the URL from
	// the current base regardless — but say so rather than leaving a stale
	// version list looking authoritative.
	return c.JSON(h.mirrorResponse(c, base))
}
