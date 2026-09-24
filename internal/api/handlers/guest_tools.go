package handlers

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
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
func (h *GuestToolsHandler) GetConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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
func (h *GuestToolsHandler) UpdateConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// mode's vocabulary is the schema's Enum now; the version vocabulary
	// stays here, because virtiowin.ValidVersion owns it and "" is a
	// meaningful value the schema cannot express as a format.
	targetVersion := p.String("target_version")
	if targetVersion != "" && !virtiowin.ValidVersion(targetVersion) {
		return fiber.NewError(fiber.StatusBadRequest, "target_version is not a valid virtio-win version")
	}
	// 0 still means "use the default": the policy card sends it for a
	// cleared field, and the schema bounds the parameter at 0..100 rather
	// than 1..100 so that spelling keeps working. The upper bound is the
	// schema's, so the hand-written "must be 100 or less" is gone.
	maxConcurrent := safeconv.Int32(int(p.Int("max_concurrent")))
	if maxConcurrent <= 0 {
		maxConcurrent = guesttools.DefaultMaxConcurrent
	}

	cfg, err := h.queries.UpsertGuestToolsConfig(c.Context(), db.UpsertGuestToolsConfigParams{
		ClusterID:     clusterID,
		Mode:          p.String("mode"),
		TargetVersion: targetVersion,
		MaxConcurrent: maxConcurrent,
		// A *bool, so that omitting the key means "keep the stored value"
		// rather than "set it to false" — the distinction p.OptBool carries,
		// and one a declared default could not blur (apischema.Property.Default).
		SnapshotBefore: optionalBool(optBoolPtr(p.OptBool("snapshot_before"))),
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
func (h *GuestToolsHandler) ListFleet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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

// SetPolicy writes a per-guest override.
func (h *GuestToolsHandler) SetPolicy(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmid, err := guestToolsIDs(p)
	if err != nil {
		return err
	}

	targetVersion := p.String("target_version")
	if targetVersion != "" && !virtiowin.ValidVersion(targetVersion) {
		return fiber.NewError(fiber.StatusBadRequest, "target_version is not a valid virtio-win version")
	}

	policy, err := h.queries.UpsertGuestToolsPolicy(c.Context(), db.UpsertGuestToolsPolicyParams{
		ClusterID: clusterID,
		Vmid:      safeconv.Int32(vmid),
		// A *bool: an exclusion is the operator saying "never touch this
		// guest", and a client that does not know about the field must not
		// clear it. An omitted key reads back from p.OptBool as not supplied,
		// whatever the schema declares as a default (apischema.Property.Default).
		Excluded:      optionalBool(optBoolPtr(p.OptBool("excluded"))),
		TargetVersion: targetVersion,
		// The note's length cap is the schema's now.
		Note: p.String("note"),
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
func (h *GuestToolsHandler) Detect(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmid, err := guestToolsIDs(p)
	if err != nil {
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

// StageUpdate stages a guest tools update, optionally running it immediately.
func (h *GuestToolsHandler) StageUpdate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmid, err := guestToolsIDs(p)
	if err != nil {
		return err
	}
	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "guest tools engine is not available")
	}

	result, err := h.engine.StageOne(c.Context(), clusterID, vmid, p.Bool("run_now"))
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
func (h *GuestToolsHandler) CancelUpdate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmid, err := guestToolsIDs(p)
	if err != nil {
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

// guestToolsIDs reads the two identifiers every per-guest guest tools
// route carries in its path.
//
// The VMID is the stable Proxmox identity, deliberately not a vms.id UUID: the
// collector mints a new UUID whenever it churns a guest row, and per-guest state
// keyed on it stops resolving at an arbitrary later time.
// Returned as an int because that is what every consumer here wants — the
// engine's methods, strconv.Itoa and the audit payloads. Only the sqlc params
// are int32, and those narrow at the call.
//
// The bounds that used to live here are the schema's (see guestVMIDParams
// in internal/api/registry_guest_tools.go), including the upper one, which
// is load-bearing rather than cosmetic: SetPolicy writes to a table with
// no FK on vmid, so an out-of-range value would be clamped by safeconv at
// the sqlc param while the audit row still recorded what the caller typed —
// an audit entry naming a guest that was never written.
//
// It is a guest-tools-specific helper rather than a shared one for the
// reason containerIDs' doc comment gives: registry_paramkey_guard_test.go
// walks a handler's callees for literal accessor keys and checks them
// against THAT endpoint's schema, so the keys have to be literals here.
func guestToolsIDs(p *apischema.Params) (uuid.UUID, int, error) {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return uuid.Nil, 0, err
	}
	return clusterID, int(p.Int("vmid")), nil
}
