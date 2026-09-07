package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
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

// findHARule looks up the rule matching name in the cluster's rule list. Used
// by the delete handler to snapshot a rule before removing it.
//
// Three outcomes, and callers must keep them apart:
//
//	(rule, nil) — present, with its content
//	(nil, nil)  — confirmed absent
//	(nil, err)  — the list read failed, so the prior state is unknown
//
// It used to return a bare *HARuleEntry, which collapsed the last two into one
// nil. That is how DeleteRule came to record a rule as deleted on the strength
// of a failed list read: a snapshot helper that cannot say "I did not check"
// makes its callers' absent-branch silently unconditional, so it never gets
// exercised and never gets doubted.
//
// This lists and filters because the client has no single-rule getter. PVE does
// expose GET /cluster/ha/rules/{rule} (Rules.pm read_rule, present for as long
// as the rules API has been) if one is ever added — and it would be the better
// source, because "confirmed absent" here is only ever as trustworthy as the
// list read is complete. The index takes optional `type` and `resource`
// filters; GetHARules sends neither, so what comes back is every rule and
// absence from it is absence. That is the invariant, and it lives in the
// client, not in PVE: repointing this at a filtered read — a GetHARulesByType,
// say — would report a rule of another type as confirmed absent and audit its
// real deletion as a no-op, inverting the very bug this exists to fix. Keep it
// on the unfiltered index.
func findHARule(ctx context.Context, pxClient *proxmox.Client, name string) (*proxmox.HARuleEntry, error) {
	rules, err := pxClient.GetHARules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].Rule == name {
			return &rules[i], nil
		}
	}
	return nil, nil
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
	return RespondItems(c, resources)
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
			return RespondItems(c, []proxmox.HAGroup{})
		}
		return mapProxmoxError(err)
	}
	return RespondItems(c, groups)
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

// haRuleMissingPhrases are the die() strings PVE uses for an HA rule that is no
// longer there, from pve-ha-manager src/PVE/API2/HA/Rules.pm: update_rule says
// "HA rule '<id>' does not exist", and read_rule — GET {rule}, which Nexara
// does not call, since ListRules lists and DeleteRule filters that list — says
// "no such ha rule '<id>'" via $get_api_ha_rule.
//
// delete_rule is deliberately absent. It runs an unconditional
// `delete $rules->{ids}->{$ruleid}`, and deleting a key that is not there is a
// no-op in Perl, so PVE answers 200 for an already-deleted rule and DeleteRule
// has no error to map. Adding one here would be unreachable code.
//
// Two near-misses to keep in view if PVE ever rewords, because both would be
// actively wrong as a 404 — they describe a rule that is present:
// "cannot use non-existent node(s) …" and "cannot use unmanaged resource(s) …"
// (a typo'd node or guest on this same PUT) escape only because neither is
// spelled "does not exist"; and update_rule's
// delete_from_config can die "no such option '<k>'" (pve-common
// SectionConfig.pm), which is why this set names "no such ha rule" in full
// rather than "no such". UpdateHARuleParams has no delete field today, so that
// one is out of reach — adding one brings it into range.
var haRuleMissingPhrases = []string{"does not exist", "no such ha rule"}

// mapHARuleError is the HA-rule half of mapMissingObjectError, in the same
// wrapper shape as mapFirewallRuleError so the sentence, the phrase set and the
// handlers that use them are wired together in one testable place.
func mapHARuleError(err error) error {
	return mapMissingObjectError("No HA rule by that name — the list may be out of date",
		haRuleMissingPhrases, err)
}

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
			return RespondItems(c, []proxmox.HARuleEntry{})
		}
		return mapProxmoxError(err)
	}
	return RespondItems(c, rules)
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
		return mapHARuleError(err)
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

// classifyHARuleDelete decides what a completed DELETE of an HA rule is
// entitled to claim, from the pre-delete snapshot attempt.
//
// It exists because a 200 from that endpoint is not evidence of a deletion.
// PVE's delete_rule (pve-ha-manager src/PVE/API2/HA/Rules.pm) is an
// unconditional `delete $rules->{ids}->{$ruleid}` with no die, and deleting an
// absent key is a no-op in Perl — so the 200 says the rule is gone now, never
// that this request is what removed it. The snapshot is the only evidence of
// the prior state, and it has three answers rather than two.
//
// Split out from DeleteRule so the decision is unit-testable without a cluster,
// a database or a request: the branch that was wrong is the one that used to be
// unreachable in every test.
//
// What it narrows, and what it cannot close. The snapshot and the delete are
// two round trips, so this is check-then-act and the window between them is
// real. Inside it, a second operator deleting the same rule leaves both
// requests saying "deleted", and a second operator *creating* that rule leaves
// a real deletion recorded as "already_deleted". PVE's rules delete takes no
// digest — unlike update — so there is no compare-and-swap to close it with.
// The window shrinks from "every stale-list delete" to "a collision inside one
// round trip"; it does not vanish, and `already_deleted` is evidence of a
// no-op rather than proof of one.
func classifyHARuleDelete(snapshot *proxmox.HARuleEntry, snapErr error) (action string, priorStateUnknown bool) {
	switch {
	case snapshot != nil:
		// Seen immediately before the call, and PVE's delete is unconditional,
		// so the rule is gone and this request is the best available account of
		// why. A concurrent delete inside the round trip is indistinguishable.
		return "deleted", false
	case snapErr != nil:
		// Could not read the list, so whether the rule was there is unknowable
		// after the fact. The verb stays "deleted" deliberately: the delete was
		// issued and accepted, and a SIEM rule or audit filter keyed on
		// "deleted" must not miss a deletion that really happened. Over-
		// claiming here is the safer direction — but the caller has to set
		// prior_state_unknown, both to carry the doubt and so the row's thin
		// detail does not read as a rule that carried no fields.
		//
		// So: action "deleted" alone is not proof of a state change. Anything
		// auditing these rows has to consult details.prior_state_unknown.
		return "deleted", true
	default:
		// Confirmed absent a moment before the call: nothing was removed. Two
		// operators racing on a stale rules list both get a 200 here, and only
		// one of them changed anything.
		return "already_deleted", false
	}
}

// haRuleDeleteDetails builds the audit detail for a completed HA-rule delete.
//
// Shared by both delete endpoints — HAHandler.DeleteRule behind the HA tab and
// DRSHandler.DeleteHARule behind the DRS page. They file rows under the same
// resource_type against the same PVE objects, so a reader who has to know which
// page the operator used before they can read the row is being told two stories
// about one system. One builder is what stops them drifting apart again.
//
// Only the detail is shared: each handler still calls AuditLog itself, because
// there is exactly one audit path in this package and it should be visible at
// the call site. A helper that wrapped the audit call would read as a
// per-handler audit wrapper, which TestGuard_NoHandlerAuditLogWrappers exists
// to prevent.
//
// The map holds only strings, ints and bools, so the marshal cannot fail.
func haRuleDeleteDetails(ctx context.Context, queries *db.Queries, clusterID uuid.UUID, rule string, snapshot *proxmox.HARuleEntry, priorStateUnknown bool) json.RawMessage {
	detailMap := map[string]any{"rule": rule}
	if priorStateUnknown {
		// Mark the row, so its thin detail reads as "nobody looked" rather than
		// "the rule held nothing". The error itself stays out of the row and
		// goes to the caller's log instead: view:audit is granted to every
		// Viewer by default, and a connection failure names the PVE host and
		// port.
		detailMap["prior_state_unknown"] = true
	}
	if snapshot != nil {
		if snapshot.Type != "" {
			detailMap["type"] = snapshot.Type
		}
		if snapshot.Resources != "" {
			detailMap["resources"] = snapshot.Resources
			if names := resolveResourceNames(ctx, queries, clusterID, snapshot.Resources); names != nil {
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
		if snapshot.Disable != 0 {
			// UpdateRule records this, so a delete that omitted it made a
			// disabled rule's row byte-identical to an active one's — and an
			// operator rebuilding the rule from the audit log would bring it
			// back enabled.
			detailMap["disable"] = snapshot.Disable
		}
	}
	details, _ := json.Marshal(detailMap)
	return details
}

// DeleteRule handles DELETE /clusters/:cluster_id/ha/rules/:rule.
//
// Deliberately idempotent, matching the PVE endpoint underneath: deleting a
// rule that is already gone succeeds, which is the kinder answer for two
// operators racing on one rules table. UpdateRule answers 404 for the same
// stale-list situation because PVE's update_rule genuinely dies there; the
// difference is Proxmox's, not ours. What the two must not do is disagree in
// the audit log, so the no-op case is recorded as the no-op it was.
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
	// Snapshot the rule before deleting it, both for the audit detail and so
	// the audit action can tell a real deletion from a no-op.
	snapshot, snapErr := findHARule(c.Context(), pxClient, rule)
	if err := pxClient.DeleteHARule(c.Context(), rule); err != nil {
		return mapProxmoxError(err)
	}
	action, priorStateUnknown := classifyHARuleDelete(snapshot, snapErr)
	if priorStateUnknown {
		slog.Warn("HA rule delete: could not read the rules list to snapshot the rule; auditing without its detail",
			"cluster_id", clusterID, "rule", rule, "error", snapErr)
	}
	details := haRuleDeleteDetails(c.Context(), h.queries, clusterID, rule, snapshot, priorStateUnknown)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", rule, action, details)
	// Published for all three outcomes. ha_change is a cache-invalidation
	// signal, not a claim about what changed, and the already-absent case is
	// precisely the one where some client is holding a stale rules list.
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
	return RespondItems(c, status)
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
