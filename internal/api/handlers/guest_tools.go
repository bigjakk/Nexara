package handlers

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/guesttools"
	"github.com/bigjakk/nexara/internal/safeconv"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// GuestToolsHandler serves Windows guest tools version tracking and staged
// updates.
//
// Gated on a dedicated guest_tools resource rather than execute:vm. The two are
// close — anyone who can change a guest's boot media already controls it — but
// "run this installer inside the operating system" is worth granting, auditing
// and withholding on its own.
type GuestToolsHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
	engine   *guesttools.Engine // from the composition root; may be nil in tests
}

// NewGuestToolsHandler creates the handler. engine comes from internal/app.
func NewGuestToolsHandler(queries *db.Queries, eventPub *events.Publisher, engine *guesttools.Engine) *GuestToolsHandler {
	return &GuestToolsHandler{queries: queries, eventPub: eventPub, engine: engine}
}

// --- Request / Response types ---

type guestToolsConfigRequest struct {
	Mode          string `json:"mode"`
	TargetVersion string `json:"target_version"`
	MaxConcurrent int32  `json:"max_concurrent"`
	// A POINTER: this is the rollback for a driver swap that can leave a guest
	// unbootable, so an absent key must preserve the stored value rather than
	// reading as false. See UpsertGuestToolsConfig.
	SnapshotBefore *bool `json:"snapshot_before"`
}

type guestToolsConfigResponse struct {
	ClusterID      uuid.UUID `json:"cluster_id"`
	Mode           string    `json:"mode"`
	TargetVersion  string    `json:"target_version"`
	SnapshotBefore bool      `json:"snapshot_before"`
	MaxConcurrent  int32     `json:"max_concurrent"`
	// EffectiveVersion is what a guest with no override resolves to, so the UI
	// never has to re-derive the precedence rule.
	EffectiveVersion string `json:"effective_version"`
	// ISOStorage is the virtio-win storage this cluster is configured to use.
	// Empty means staging cannot work yet, which is worth surfacing directly.
	ISOStorage string `json:"iso_storage"`
}

type guestToolsGuestResponse struct {
	VMID             int32   `json:"vmid"`
	Name             string  `json:"name"`
	Node             string  `json:"node"`
	Status           string  `json:"status"`
	Template         bool    `json:"template"`
	InstalledVersion string  `json:"installed_version"`
	AgentVersion     string  `json:"agent_version"`
	AgentRunning     bool    `json:"agent_running"`
	DetectedAt       *string `json:"detected_at"`
	Stage            string  `json:"stage"`
	// RebootRequired: the installer returned 3010 — installed, but a driver that
	// was in use only swaps at the guest's next restart. Neither an error nor
	// fully done, so the UI can say exactly that.
	RebootRequired bool    `json:"reboot_required"`
	StagedVersion  string  `json:"staged_version"`
	StagedAt       *string `json:"staged_at"`
	LastError      string  `json:"last_error"`
	LastResultAt   *string `json:"last_result_at"`
	Excluded       bool    `json:"excluded"`
	PolicyVersion  string  `json:"policy_target_version"`
	Note           string  `json:"note"`
	// TargetVersion is the effective target for THIS guest, and UpToDate /
	// NeedsUpdate are computed server-side. Three fields rather than one so the
	// UI can distinguish "current", "behind" and "we have never looked".
	TargetVersion string `json:"target_version"`
	UpToDate      bool   `json:"up_to_date"`
	NeedsUpdate   bool   `json:"needs_update"`
}

// --- Handlers ---

// GetConfig returns a cluster's guest tools policy, defaulting to disabled.
func (h *GuestToolsHandler) GetConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "guest_tools", clusterID); err != nil {
		return err
	}

	cfg, err := guesttools.ConfigOrDefault(c.Context(), h.queries, clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read guest tools config")
	}

	return c.JSON(h.configResponse(c, clusterID, cfg))
}

// configResponse decorates a stored config with the two values the UI would
// otherwise have to derive: the version a guest with no override resolves to,
// and the virtio-win storage staging will pull the ISO from.
//
// Both are best-effort. A cluster with no virtio-win row yet is the normal
// pre-configuration state, and an unresolvable target is what an empty catalog
// looks like — neither is worth failing a config read over, and an empty string
// is what the UI renders as "not configured".
func (h *GuestToolsHandler) configResponse(c fiber.Ctx, clusterID uuid.UUID, cfg db.GuestToolsConfig) guestToolsConfigResponse {
	resp := guestToolsConfigResponse{
		ClusterID:      cfg.ClusterID,
		Mode:           cfg.Mode,
		TargetVersion:  cfg.TargetVersion,
		SnapshotBefore: cfg.SnapshotBefore,
		MaxConcurrent:  cfg.MaxConcurrent,
	}
	if vwCfg, err := h.queries.GetVirtioWinConfig(c.Context(), clusterID); err == nil {
		resp.ISOStorage = vwCfg.Storage
	}
	if h.engine != nil {
		if target, err := h.engine.ResolveTarget(c.Context(), cfg, nil); err == nil {
			resp.EffectiveVersion = target.Version
		}
	}
	return resp
}

// UpdateConfig writes a cluster's guest tools policy.
func (h *GuestToolsHandler) UpdateConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "guest_tools", clusterID); err != nil {
		return err
	}

	var req guestToolsConfigRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	switch req.Mode {
	case "disabled", "report", "staged":
	default:
		return fiber.NewError(fiber.StatusBadRequest, "mode must be disabled, report or staged")
	}
	if req.TargetVersion != "" && !virtiowin.ValidVersion(req.TargetVersion) {
		return fiber.NewError(fiber.StatusBadRequest, "target_version is not a valid virtio-win version")
	}
	if req.MaxConcurrent <= 0 {
		req.MaxConcurrent = guesttools.DefaultMaxConcurrent
	}
	if req.MaxConcurrent > 100 {
		return fiber.NewError(fiber.StatusBadRequest, "max_concurrent must be 100 or less")
	}

	cfg, err := h.queries.UpsertGuestToolsConfig(c.Context(), db.UpsertGuestToolsConfigParams{
		ClusterID:      clusterID,
		Mode:           req.Mode,
		TargetVersion:  req.TargetVersion,
		MaxConcurrent:  req.MaxConcurrent,
		SnapshotBefore: optionalBool(req.SnapshotBefore),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save guest tools config")
	}

	details, _ := json.Marshal(map[string]any{
		"mode":            cfg.Mode,
		"target_version":  cfg.TargetVersion,
		"snapshot_before": cfg.SnapshotBefore,
		"max_concurrent":  cfg.MaxConcurrent,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cluster", clusterID.String(), "guest_tools_config_update", details)

	return c.JSON(h.configResponse(c, clusterID, cfg))
}

// ListFleet returns every Windows guest in the cluster with its guest tools
// state, including guests that have never been probed and guests that are
// excluded — an exclusion nobody can see is one nobody can audit.
func (h *GuestToolsHandler) ListFleet(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "guest_tools", clusterID); err != nil {
		return err
	}

	rows, err := h.queries.ListGuestToolsFleet(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list guests")
	}

	cfg, err := guesttools.ConfigOrDefault(c.Context(), h.queries, clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read guest tools config")
	}

	out := make([]guestToolsGuestResponse, 0, len(rows))
	for _, r := range rows {
		item := guestToolsGuestResponse{
			VMID:             r.Vmid,
			Name:             r.Name,
			Node:             r.NodeName,
			Status:           r.Status,
			Template:         r.Template,
			InstalledVersion: r.InstalledVersion.String,
			AgentVersion:     r.AgentVersion.String,
			AgentRunning:     r.AgentRunning.Bool,
			Stage:            "idle",
			RebootRequired:   r.RebootRequired.Bool,
			StagedVersion:    r.StagedVersion.String,
			LastError:        r.LastError.String,
			Excluded:         r.Excluded.Bool,
			PolicyVersion:    r.PolicyTargetVersion.String,
			Note:             r.Note.String,
			DetectedAt:       tsPtr(r.DetectedAt),
			StagedAt:         tsPtr(r.StagedAt),
			LastResultAt:     tsPtr(r.LastResultAt),
		}
		// Valid is the only test needed: the column is NOT NULL with a CHECK
		// over six non-empty stages, so a row that has one cannot carry ''.
		// Invalid means the LEFT JOIN found no state row at all.
		if r.Stage.Valid {
			item.Stage = r.Stage.String
		}

		// Resolve each guest's own effective target: a per-guest pin outranks
		// the cluster's.
		var policy *db.GuestToolsPolicy
		if r.PolicyTargetVersion.Valid && r.PolicyTargetVersion.String != "" {
			policy = &db.GuestToolsPolicy{TargetVersion: r.PolicyTargetVersion.String}
		}
		if h.engine != nil {
			if target, err := h.engine.ResolveTarget(c.Context(), cfg, policy); err == nil {
				item.TargetVersion = target.Version
				item.UpToDate = guesttools.UpToDate(item.InstalledVersion, target.Version)
				item.NeedsUpdate = guesttools.NeedsUpdate(item.InstalledVersion, target.Version)
			}
		}
		out = append(out, item)
	}
	return RespondItems(c, out)
}

type guestToolsPolicyRequest struct {
	TargetVersion string `json:"target_version"`
	Note          string `json:"note"`
	// A POINTER: an exclusion is the operator saying "never touch this guest",
	// and a client that simply does not know about the field must not clear it.
	Excluded *bool `json:"excluded"`
}

// SetPolicy writes a per-guest override.
func (h *GuestToolsHandler) SetPolicy(c fiber.Ctx) error {
	clusterID, vmid, err := clusterAndVMIDFromParams(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "guest_tools", clusterID); err != nil {
		return err
	}

	var req guestToolsPolicyRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.TargetVersion != "" && !virtiowin.ValidVersion(req.TargetVersion) {
		return fiber.NewError(fiber.StatusBadRequest, "target_version is not a valid virtio-win version")
	}
	if len(req.Note) > 500 {
		return fiber.NewError(fiber.StatusBadRequest, "note must be 500 characters or fewer")
	}

	policy, err := h.queries.UpsertGuestToolsPolicy(c.Context(), db.UpsertGuestToolsPolicyParams{
		ClusterID:     clusterID,
		Vmid:          safeconv.Int32(vmid),
		Excluded:      optionalBool(req.Excluded),
		TargetVersion: req.TargetVersion,
		Note:          req.Note,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save guest policy")
	}

	details, _ := json.Marshal(map[string]any{
		"vmid":           vmid,
		"excluded":       policy.Excluded,
		"target_version": policy.TargetVersion,
		"note":           policy.Note,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm", strconv.Itoa(vmid), "guest_tools_policy_update", details)
	return c.JSON(fiber.Map{
		"cluster_id":     policy.ClusterID,
		"vmid":           policy.Vmid,
		"excluded":       policy.Excluded,
		"target_version": policy.TargetVersion,
		"note":           policy.Note,
	})
}

// Detect probes one guest on demand.
func (h *GuestToolsHandler) Detect(c fiber.Ctx) error {
	clusterID, vmid, err := clusterAndVMIDFromParams(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "guest_tools", clusterID); err != nil {
		return err
	}
	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "guest tools engine is not available")
	}

	detection, err := h.engine.DetectOne(c.Context(), clusterID, vmid)
	if err != nil {
		if errors.Is(err, guesttools.ErrNotEligible) {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{
		"installed_version": detection.InstalledVersion(),
		"agent_version":     detection.Agent,
		"agent_running":     detection.AgentRunning(),
		"installed":         detection.Installed(),
	})
}

type guestToolsUpdateRequest struct {
	// RunNow starts the installer immediately instead of waiting for the
	// guest's next boot.
	RunNow bool `json:"run_now"`
}

// StageUpdate stages a guest tools update, optionally running it immediately.
func (h *GuestToolsHandler) StageUpdate(c fiber.Ctx) error {
	clusterID, vmid, err := clusterAndVMIDFromParams(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "execute", "guest_tools", clusterID); err != nil {
		return err
	}
	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "guest tools engine is not available")
	}

	var req guestToolsUpdateRequest
	if err := c.Bind().JSON(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	result, err := h.engine.StageOne(c.Context(), clusterID, vmid, req.RunNow)
	if err != nil {
		switch {
		case errors.Is(err, guesttools.ErrNotEligible), errors.Is(err, guesttools.ErrNoCDROMSlot):
			return fiber.NewError(fiber.StatusConflict, err.Error())
		case errors.Is(err, guesttools.ErrNoTargetVersion), errors.Is(err, guesttools.ErrISOUnavailable):
			return fiber.NewError(fiber.StatusPreconditionFailed, err.Error())
		default:
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
	}

	// A pre-update snapshot is a Proxmox task, so it is recorded like every
	// other one rather than vanishing into this handler.
	if result.SnapshotUPID != "" {
		TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
			ClusterID:    clusterID,
			Node:         result.Node,
			ResourceType: "vm",
			ResourceID:   strconv.Itoa(vmid),
			ResourceName: result.SnapshotName,
			Action:       "snapshot_create",
			UPID:         result.SnapshotUPID,
			TaskType:     "qmsnapshot",
			Description:  "Snapshot before guest tools update to " + result.Target.Version,
			Extra:        map[string]any{"vmid": vmid, "snapshot": result.SnapshotName},
		})
	}

	details, _ := json.Marshal(map[string]any{
		"vmid":      vmid,
		"version":   result.Target.Version,
		"cdrom":     result.CDROMKey,
		"run_now":   result.RanNow,
		"snapshot":  result.SnapshotName,
		"iso":       result.Target.ISOFilename,
		"triggered": "manual",
	})
	action, stage := "guest_tools_stage", "staged"
	if result.RanNow {
		action, stage = "guest_tools_update", "running"
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm", strconv.Itoa(vmid), action, details)

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"vmid":     vmid,
		"version":  result.Target.Version,
		"cdrom":    result.CDROMKey,
		"run_now":  result.RanNow,
		"snapshot": result.SnapshotName,
		"stage":    stage,
	})
}

// CancelUpdate clears a staged update from a guest.
func (h *GuestToolsHandler) CancelUpdate(c fiber.Ctx) error {
	clusterID, vmid, err := clusterAndVMIDFromParams(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "execute", "guest_tools", clusterID); err != nil {
		return err
	}
	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "guest tools engine is not available")
	}

	if err := h.engine.CancelOne(c.Context(), clusterID, vmid); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	details, _ := json.Marshal(map[string]any{"vmid": vmid})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm", strconv.Itoa(vmid), "guest_tools_cancel", details)
	return c.JSON(fiber.Map{"status": "cancelled", "vmid": vmid})
}

// maxProxmoxVMID is the largest VMID Proxmox will assign.
const maxProxmoxVMID = 999999999

// clusterAndVMIDFromParams reads the cluster UUID and the Proxmox VMID.
//
// The VMID is the stable Proxmox identity, deliberately not a vms.id UUID: the
// collector mints a new UUID whenever it churns a guest row, and per-guest state
// keyed on it stops resolving at an arbitrary later time.
// Returned as an int because that is what every consumer here wants — the
// engine's methods, strconv.Itoa and the audit payloads. Only the sqlc params
// are int32, and those narrow at the call.
func clusterAndVMIDFromParams(c fiber.Ctx) (uuid.UUID, int, error) {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return uuid.Nil, 0, err
	}
	// Bounded at both ends. The upper bound is Proxmox's own maximum VMID, and
	// it is load-bearing rather than cosmetic: SetPolicy writes to a table with
	// no FK on vmid, so an out-of-range value would be clamped by safeconv at
	// the sqlc param while the audit row still recorded what the caller typed —
	// an audit entry naming a guest that was never written.
	vmid, convErr := strconv.Atoi(c.Params("vmid"))
	if convErr != nil || vmid <= 0 || vmid > maxProxmoxVMID {
		return uuid.Nil, 0, fiber.NewError(fiber.StatusBadRequest, "vmid must be a positive integer")
	}
	return clusterID, vmid, nil
}
