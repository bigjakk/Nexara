package handlers

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// decodePathParam returns the path param URL-decoded, falling back to the raw
// value if the input is malformed. Audit logs and Proxmox SID paths both want
// the literal value (e.g. "vm:109"), not "vm%3A109".
func decodePathParam(c fiber.Ctx, name string) string {
	raw := c.Params(name)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// resolveSIDName looks up the friendly VM/CT name for a SID like "vm:109".
// Returns "" if the SID can't be parsed or the VM isn't in the inventory.
func resolveSIDName(ctx context.Context, queries *db.Queries, clusterID uuid.UUID, sid string) string {
	parts := strings.SplitN(sid, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	vmid, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return ""
	}
	vm, err := queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID,
		Vmid:      int32(vmid),
	})
	if err != nil {
		return ""
	}
	return vm.Name
}

// resolveResourceNames parses a comma-separated SID list (e.g. "vm:100,ct:101")
// and returns a map of sid → friendly name for those that resolve. Returns nil
// when nothing resolves so callers can omit the field from audit details.
func resolveResourceNames(ctx context.Context, queries *db.Queries, clusterID uuid.UUID, resources string) map[string]string {
	if resources == "" {
		return nil
	}
	out := make(map[string]string)
	for _, sid := range strings.Split(resources, ",") {
		sid = strings.TrimSpace(sid)
		if sid == "" {
			continue
		}
		if name := resolveSIDName(ctx, queries, clusterID, sid); name != "" {
			out[sid] = name
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// findHARule returns the rule matching name from the cluster's rule list.
// Used by the delete handler so we can capture a snapshot before removal,
// since the Proxmox API has no GET /cluster/ha/rules/{rule} endpoint.
func findHARule(ctx context.Context, pxClient *proxmox.Client, name string) *proxmox.HARuleEntry {
	rules, err := pxClient.GetHARules(ctx)
	if err != nil {
		return nil
	}
	for i := range rules {
		if rules[i].Rule == name {
			return &rules[i]
		}
	}
	return nil
}

// HAHandler handles HA resources, groups, and status endpoints.
type HAHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewHAHandler creates a new HAHandler.
func NewHAHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *HAHandler {
	return &HAHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *HAHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// requireArmDisarmSupport rejects Arm/Disarm HA on clusters older than PVE 9.2.
// Best-effort: if the cached version can't be read, defer to Proxmox to reject.
func (h *HAHandler) requireArmDisarmSupport(c fiber.Ctx, clusterID uuid.UUID) error {
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return nil
	}
	if !proxmox.VersionAtLeast(cluster.PveVersion, proxmox.CapHAArmDisarm) {
		return fiber.NewError(fiber.StatusBadRequest, "Arm/Disarm HA requires Proxmox VE 9.2 or newer")
	}
	return nil
}

// ArmHA handles POST /clusters/:cluster_id/ha/arm — re-arms the HA stack
// cluster-wide after a disarm window.
func (h *HAHandler) ArmHA(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	if err := h.requireArmDisarmSupport(c, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.ArmHA(c.Context()); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"action": "arm-ha"})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha", clusterID.String(), "arm_ha", details)
	h.publishHA(c, clusterID, clusterID.String(), "arm_ha")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DisarmHA handles POST /clusters/:cluster_id/ha/disarm — disarms the HA stack
// cluster-wide for planned maintenance. Body: {"resource_mode": "freeze"|"ignore"}.
func (h *HAHandler) DisarmHA(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	if err := h.requireArmDisarmSupport(c, clusterID); err != nil {
		return err
	}
	var req struct {
		ResourceMode string `json:"resource_mode"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.ResourceMode != "freeze" && req.ResourceMode != "ignore" {
		return fiber.NewError(fiber.StatusBadRequest, "resource_mode must be 'freeze' or 'ignore'")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DisarmHA(c.Context(), req.ResourceMode); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"action": "disarm-ha", "resource_mode": req.ResourceMode})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha", clusterID.String(), "disarm_ha", details)
	h.publishHA(c, clusterID, clusterID.String(), "disarm_ha")
	return c.JSON(fiber.Map{"status": "ok"})
}

func (h *HAHandler) publishHA(c fiber.Ctx, clusterID uuid.UUID, resourceID, action string) {
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindHAChange, "ha", resourceID, action)
}

// --- HA Resources ---

// ListResources handles GET /clusters/:cluster_id/ha/resources.
func (h *HAHandler) ListResources(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	resources, err := pxClient.GetHAResources(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(resources)
}

// CreateResource handles POST /clusters/:cluster_id/ha/resources.
func (h *HAHandler) CreateResource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	var req proxmox.CreateHAResourceParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.SID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateHAResource(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"sid": req.SID}
	if name := resolveSIDName(c.Context(), h.queries, clusterID, req.SID); name != "" {
		detailMap["name"] = name
	}
	if req.State != "" {
		detailMap["state"] = req.State
	}
	if req.Group != "" {
		detailMap["group"] = req.Group
	}
	if req.MaxRestart != 0 {
		detailMap["max_restart"] = req.MaxRestart
	}
	if req.MaxRelocate != 0 {
		detailMap["max_relocate"] = req.MaxRelocate
	}
	if req.Comment != "" {
		detailMap["comment"] = req.Comment
	}
	if req.Failback != nil {
		detailMap["failback"] = *req.Failback
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_resource", req.SID, "created", details)
	h.publishHA(c, clusterID, req.SID, "resource_created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetResource handles GET /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) GetResource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	sid := decodePathParam(c, "sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	resource, err := pxClient.GetHAResource(c.Context(), sid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(resource)
}

// UpdateResource handles PUT /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) UpdateResource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	sid := decodePathParam(c, "sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	var req proxmox.UpdateHAResourceParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateHAResource(c.Context(), sid, req); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"sid": sid}
	if name := resolveSIDName(c.Context(), h.queries, clusterID, sid); name != "" {
		detailMap["name"] = name
	}
	if req.State != nil {
		detailMap["state"] = *req.State
	}
	if req.Group != nil {
		detailMap["group"] = *req.Group
	}
	if req.MaxRestart != nil {
		detailMap["max_restart"] = *req.MaxRestart
	}
	if req.MaxRelocate != nil {
		detailMap["max_relocate"] = *req.MaxRelocate
	}
	if req.Comment != nil {
		detailMap["comment"] = *req.Comment
	}
	if req.Failback != nil {
		detailMap["failback"] = *req.Failback
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_resource", sid, "updated", details)
	h.publishHA(c, clusterID, sid, "resource_updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteResource handles DELETE /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) DeleteResource(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	sid := decodePathParam(c, "sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// Snapshot the resource before deletion so the audit entry has context.
	// Best-effort: a fetch failure should not block the delete itself.
	snapshot, _ := pxClient.GetHAResource(c.Context(), sid)
	if err := pxClient.DeleteHAResource(c.Context(), sid); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"sid": sid}
	if name := resolveSIDName(c.Context(), h.queries, clusterID, sid); name != "" {
		detailMap["name"] = name
	}
	if snapshot != nil {
		if snapshot.Type != "" {
			detailMap["resource_type"] = snapshot.Type
		}
		if snapshot.State != "" {
			detailMap["state"] = snapshot.State
		}
		if snapshot.Group != "" {
			detailMap["group"] = snapshot.Group
		}
		if snapshot.Comment != "" {
			detailMap["comment"] = snapshot.Comment
		}
		if snapshot.MaxRestart != 0 {
			detailMap["max_restart"] = snapshot.MaxRestart
		}
		if snapshot.MaxRelocate != 0 {
			detailMap["max_relocate"] = snapshot.MaxRelocate
		}
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_resource", sid, "deleted", details)
	h.publishHA(c, clusterID, sid, "resource_deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- HA Groups ---

// haGroupsMigratedMsg is returned for HA group write attempts on PVE 9.x
// clusters, where the groups API is soft-disabled in favor of HA rules.
const haGroupsMigratedMsg = "HA Groups were migrated to HA Rules in Proxmox VE 9 — use the HA Rules tab instead."

// ListGroups handles GET /clusters/:cluster_id/ha/groups.
// On PVE 9.x where groups have been migrated to rules, returns an empty array.
func (h *HAHandler) ListGroups(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	groups, err := pxClient.GetHAGroups(c.Context())
	if err != nil {
		// PVE 9.x soft-disables the groups API once migrated to rules — there
		// are simply no groups to list anymore, so return an empty array.
		if proxmox.IsGroupsMigratedError(err) {
			return c.JSON([]proxmox.HAGroup{})
		}
		return mapProxmoxError(err)
	}
	return c.JSON(groups)
}

// CreateGroup handles POST /clusters/:cluster_id/ha/groups.
func (h *HAHandler) CreateGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	var req proxmox.CreateHAGroupParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	if req.Nodes == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Nodes are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateHAGroup(c.Context(), req); err != nil {
		if proxmox.IsGroupsMigratedError(err) {
			return fiber.NewError(fiber.StatusConflict, haGroupsMigratedMsg)
		}
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"group": req.Group, "nodes": req.Nodes}
	if req.Restricted != 0 {
		detailMap["restricted"] = req.Restricted
	}
	if req.NoFailback != 0 {
		detailMap["nofailback"] = req.NoFailback
	}
	if req.Comment != "" {
		detailMap["comment"] = req.Comment
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_group", req.Group, "created", details)
	h.publishHA(c, clusterID, req.Group, "group_created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateGroup handles PUT /clusters/:cluster_id/ha/groups/:group.
func (h *HAHandler) UpdateGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	group := decodePathParam(c, "group")
	if group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	var req proxmox.UpdateHAGroupParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateHAGroup(c.Context(), group, req); err != nil {
		if proxmox.IsGroupsMigratedError(err) {
			return fiber.NewError(fiber.StatusConflict, haGroupsMigratedMsg)
		}
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"group": group}
	if req.Nodes != nil {
		detailMap["nodes"] = *req.Nodes
	}
	if req.Restricted != nil {
		detailMap["restricted"] = *req.Restricted
	}
	if req.NoFailback != nil {
		detailMap["nofailback"] = *req.NoFailback
	}
	if req.Comment != nil {
		detailMap["comment"] = *req.Comment
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_group", group, "updated", details)
	h.publishHA(c, clusterID, group, "group_updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteGroup handles DELETE /clusters/:cluster_id/ha/groups/:group.
func (h *HAHandler) DeleteGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	group := decodePathParam(c, "group")
	if group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// Snapshot the group before deletion so the audit entry has context.
	snapshot, _ := pxClient.GetHAGroup(c.Context(), group)
	if err := pxClient.DeleteHAGroup(c.Context(), group); err != nil {
		if proxmox.IsGroupsMigratedError(err) {
			return fiber.NewError(fiber.StatusConflict, haGroupsMigratedMsg)
		}
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"group": group}
	if snapshot != nil {
		if snapshot.Nodes != "" {
			detailMap["nodes"] = snapshot.Nodes
		}
		if snapshot.Restricted != 0 {
			detailMap["restricted"] = snapshot.Restricted
		}
		if snapshot.NoFailback != 0 {
			detailMap["nofailback"] = snapshot.NoFailback
		}
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_group", group, "deleted", details)
	h.publishHA(c, clusterID, group, "group_deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- HA Rules (PVE 8.3+) ---

// ListRules handles GET /clusters/:cluster_id/ha/rules.
func (h *HAHandler) ListRules(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	rules, err := pxClient.GetHARules(c.Context())
	if err != nil {
		// Older PVE without rules support — return empty list
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return c.JSON([]proxmox.HARuleEntry{})
		}
		return mapProxmoxError(err)
	}
	return c.JSON(rules)
}

// CreateRule handles POST /clusters/:cluster_id/ha/rules.
func (h *HAHandler) CreateRule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	var req struct {
		Type string `json:"type"` // "node-affinity" or "resource-affinity"
		proxmox.CreateHARuleParams
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Rule == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Rule name is required")
	}
	if req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Rule type is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateHARule(c.Context(), req.Type, req.CreateHARuleParams); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"rule": req.Rule, "type": req.Type}
	if req.Resources != "" {
		detailMap["resources"] = req.Resources
		if names := resolveResourceNames(c.Context(), h.queries, clusterID, req.Resources); names != nil {
			detailMap["resource_names"] = names
		}
	}
	if req.Nodes != "" {
		detailMap["nodes"] = req.Nodes
	}
	if req.Strict != 0 {
		detailMap["strict"] = req.Strict
	}
	if req.Affinity != "" {
		detailMap["affinity"] = req.Affinity
	}
	if req.Comment != "" {
		detailMap["comment"] = req.Comment
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", req.Rule, "created", details)
	h.publishHA(c, clusterID, req.Rule, "rule_created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateRule handles PUT /clusters/:cluster_id/ha/rules/:rule.
func (h *HAHandler) UpdateRule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	rule := decodePathParam(c, "rule")
	if rule == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Rule name is required")
	}
	var req struct {
		Type string `json:"type"`
		proxmox.UpdateHARuleParams
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Rule type is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateHARule(c.Context(), rule, req.Type, req.UpdateHARuleParams); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"rule": rule, "type": req.Type}
	if req.Resources != nil {
		detailMap["resources"] = *req.Resources
		if names := resolveResourceNames(c.Context(), h.queries, clusterID, *req.Resources); names != nil {
			detailMap["resource_names"] = names
		}
	}
	if req.Nodes != nil {
		detailMap["nodes"] = *req.Nodes
	}
	if req.Strict != nil {
		detailMap["strict"] = *req.Strict
	}
	if req.Affinity != nil {
		detailMap["affinity"] = *req.Affinity
	}
	if req.Comment != nil {
		detailMap["comment"] = *req.Comment
	}
	if req.Disable != nil {
		detailMap["disable"] = *req.Disable
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", rule, "updated", details)
	h.publishHA(c, clusterID, rule, "rule_updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteRule handles DELETE /clusters/:cluster_id/ha/rules/:rule.
func (h *HAHandler) DeleteRule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ha", clusterID); err != nil {
		return err
	}
	rule := decodePathParam(c, "rule")
	if rule == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Rule name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// Snapshot the rule before deletion so the audit entry has context.
	// Proxmox has no GET /cluster/ha/rules/{rule} endpoint, so we list and filter.
	snapshot := findHARule(c.Context(), pxClient, rule)
	if err := pxClient.DeleteHARule(c.Context(), rule); err != nil {
		return mapProxmoxError(err)
	}
	detailMap := map[string]any{"rule": rule}
	if snapshot != nil {
		if snapshot.Type != "" {
			detailMap["type"] = snapshot.Type
		}
		if snapshot.Resources != "" {
			detailMap["resources"] = snapshot.Resources
			if names := resolveResourceNames(c.Context(), h.queries, clusterID, snapshot.Resources); names != nil {
				detailMap["resource_names"] = names
			}
		}
		if snapshot.Nodes != "" {
			detailMap["nodes"] = snapshot.Nodes
		}
		if snapshot.Strict != 0 {
			detailMap["strict"] = snapshot.Strict
		}
		if snapshot.Affinity != "" {
			detailMap["affinity"] = snapshot.Affinity
		}
		if snapshot.Comment != "" {
			detailMap["comment"] = snapshot.Comment
		}
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", rule, "deleted", details)
	h.publishHA(c, clusterID, rule, "rule_deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- HA Status ---

// GetStatus handles GET /clusters/:cluster_id/ha/status.
func (h *HAHandler) GetStatus(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	status, err := pxClient.GetHAStatus(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(status)
}

// GetManagerStatus handles GET /clusters/:cluster_id/ha/manager-status.
func (h *HAHandler) GetManagerStatus(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ha", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	status, err := pxClient.GetHAManagerStatus(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(status)
}
