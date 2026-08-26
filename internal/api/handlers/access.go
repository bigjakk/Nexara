package handlers

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// AccessHandler manages a cluster's Proxmox access control: PVE users, API
// tokens, groups, roles and ACLs.
//
// Everything here is a synchronous proxy — no /access/* endpoint returns a
// UPID — so these handlers use AuditLog and never TrackTask.
//
// Two invariants run through the whole file:
//
//   - Token secrets never leave this process except in the single response to
//     the operator who minted them. Proxmox returns a secret exactly once and
//     has no read-back endpoint, so there is nothing to re-fetch and nothing
//     that belongs in an audit row. view:audit is held by every built-in
//     Viewer, which makes audit details the wrong place for any credential.
//   - Nexara refuses, by default, to destroy the credential it authenticates
//     with. See guardSelfCredential.
type AccessHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewAccessHandler creates a new AccessHandler.
func NewAccessHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *AccessHandler {
	return &AccessHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *AccessHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// accessResource is the RBAC resource name for this whole family.
// manage:access can mint an Administrator token, so it is Admin-only by
// default — see migrations/000085_access_permissions.up.sql.
const accessResource = "access"

// publishAccessChange emits the WS event that invalidates the cluster's access
// queries in connected browsers.
func (h *AccessHandler) publishAccessChange(ctx context.Context, clusterID uuid.UUID, resourceType, resourceID, action string) {
	if h.eventPub == nil {
		return
	}
	h.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindAccessChange, resourceType, resourceID, action)
}

// splitFullTokenID splits "user@realm!tokenname" into its two halves.
// Returns ok=false when s carries no "!", i.e. it is a plain user id.
func splitFullTokenID(s string) (userid, tokenid string, ok bool) {
	userid, tokenid, ok = strings.Cut(s, "!")
	if !ok || userid == "" || tokenid == "" {
		return "", "", false
	}
	return userid, tokenid, true
}

// selfCredentialSubject reports whether operating on (userid, tokenid) would
// destroy the credential Nexara authenticates with, described by ownTokenID in
// its full "user@realm!tokenname" form.
//
// An empty tokenid means the whole user is the target, which takes its tokens
// with it — so that collides whenever the user matches, whatever the token name.
//
// Kept pure and separate from the handler so the decision is testable without a
// database; the handler supplies ownTokenID from the cluster row.
func selfCredentialSubject(ownTokenID, userid, tokenid string) (string, bool) {
	ownUser, ownToken, ok := splitFullTokenID(ownTokenID)
	if !ok {
		return "", false
	}
	if !strings.EqualFold(ownUser, userid) {
		return "", false
	}
	if tokenid != "" && !strings.EqualFold(ownToken, tokenid) {
		return "", false
	}
	if tokenid == "" {
		return "user " + ownUser, true
	}
	return "token " + ownTokenID, true
}

// guardSelfCredential refuses operations that would sever Nexara's own access
// to the cluster: deleting or regenerating the token it authenticates with, or
// deleting the user that owns it.
//
// This warns rather than blocks outright — an operator rotating credentials by
// hand has a legitimate reason to do exactly this, and a hard block would send
// them to the Proxmox UI to do it less safely. force=true proceeds, and the UI
// puts that behind a type-the-name confirmation.
//
// A failure to read the cluster row is deliberately NOT fatal: the caller
// already passed an RBAC check, and refusing an otherwise-valid operation
// because a lookup hiccuped would be worse than losing the guard for one call.
func (h *AccessHandler) guardSelfCredential(c fiber.Ctx, clusterID uuid.UUID, userid, tokenid string, force bool) error {
	if force || h.queries == nil {
		return nil
	}
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		return nil
	}
	subject, conflict := selfCredentialSubject(cluster.TokenID, userid, tokenid)
	if !conflict {
		return nil
	}
	return fiber.NewError(fiber.StatusConflict,
		"This is the "+subject+" Nexara uses to reach this cluster. "+
			"Continuing will cut Nexara off until the cluster is reconfigured with new credentials. "+
			"Retry with force=true to proceed anyway.")
}

// accessUpdateAffectsAccess reports whether a user update could stop the
// account's API tokens from working.
//
// Disabling the user, giving it an expiry, or changing its group membership all
// can: PVE validates the owning user when it verifies a token, and
// group-derived ACLs disappear with the group. Cosmetic fields (comment, email,
// names, SSH keys) cannot, and are deliberately not guarded — making every edit
// to nexara@pve demand a force flag would train operators to pass it reflexively.
//
// Expire is treated as risky whenever it is set to anything but 0 (never),
// rather than by comparing against the clock: an expiry in the future is still a
// scheduled outage, and the operator should say so deliberately.
func accessUpdateAffectsAccess(req proxmox.UpdateAccessUserParams) bool {
	if req.Enable != nil && !*req.Enable {
		return true
	}
	if req.Expire != nil && *req.Expire != 0 {
		return true
	}
	return req.Groups != nil
}

// forceRequested reports whether the caller passed ?force=true.
func forceRequested(c fiber.Ctx) bool {
	v := strings.ToLower(c.Query("force"))
	return v == "1" || v == "true"
}

// accessParam reads a path parameter and percent-decodes it.
//
// Fiber v3 does NOT decode path params — c.Params returns the raw segment. That
// matters more here than for most resources because these identifiers legally
// contain characters a correct client must encode: a PVE user id is
// "name@realm", so encodeURIComponent sends "nexara%40pve". Without decoding,
// every user, token, group, role and realm lookup would fail validation on the
// stray "%" and 400 — from a client doing exactly the right thing.
//
// Decoding here is safe because validation happens afterwards, in the proxmox
// client: "%2e%2e" becomes ".." and is then rejected outright, and the outbound
// path is re-escaped with url.PathEscape. Decoding without that ordering would
// reintroduce the traversal the validators exist to stop.
func accessParam(c fiber.Ctx, name string) (string, error) {
	raw := c.Params(name)
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "Malformed "+name+" in path")
	}
	return decoded, nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// ListUsers handles GET /clusters/:cluster_id/access/users.
func (h *AccessHandler) ListUsers(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	users, err := pxClient.GetAccessUsers(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, users)
}

// GetUser handles GET /clusters/:cluster_id/access/users/:userid.
func (h *AccessHandler) GetUser(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	user, err := pxClient.GetAccessUser(c.Context(), userid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(user)
}

// CreateUser handles POST /clusters/:cluster_id/access/users.
func (h *AccessHandler) CreateUser(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	var req proxmox.CreateAccessUserParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.UserID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "User ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateAccessUser(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	// Whether the account has a password is useful audit context — a
	// passwordless @pve user cannot log in interactively at all — but the
	// password itself must never appear. Derived here, as a bool, so the
	// marshalled map never reads the field.
	hasPassword := req.Password != ""
	details, _ := json.Marshal(map[string]any{
		"userid":       req.UserID,
		"has_password": hasPassword,
		"groups":       derefString(req.Groups),
		"comment":      derefString(req.Comment),
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_user", req.UserID, "created", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_user", req.UserID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateUser handles PUT /clusters/:cluster_id/access/users/:userid.
func (h *AccessHandler) UpdateUser(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	var req proxmox.UpdateAccessUserParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	// An update can sever Nexara's access just as thoroughly as a delete, so
	// the same guard applies. PVE checks the owning user's enabled/expired
	// state when it verifies an API token, so disabling or expiring
	// nexara@pve makes every subsequent call 401 even though the token still
	// exists. Rewriting groups can drop the group-derived ACLs the token
	// depends on. A comment or email change cannot, so those pass freely.
	if accessUpdateAffectsAccess(req) {
		if err := h.guardSelfCredential(c, clusterID, userid, "", forceRequested(c)); err != nil {
			return err
		}
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateAccessUser(c.Context(), userid, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"userid": userid})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_user", userid, "updated", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_user", userid, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteUser handles DELETE /clusters/:cluster_id/access/users/:userid.
func (h *AccessHandler) DeleteUser(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	force := forceRequested(c)
	if err := h.guardSelfCredential(c, clusterID, userid, "", force); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteAccessUser(c.Context(), userid); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"userid": userid, "forced": force})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_user", userid, "deleted", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_user", userid, "deleted")
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------------
// API tokens
// ---------------------------------------------------------------------------

// ListTokens handles GET /clusters/:cluster_id/access/users/:userid/tokens.
func (h *AccessHandler) ListTokens(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	tokens, err := pxClient.GetAccessUserTokens(c.Context(), userid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, tokens)
}

// GetToken handles GET /clusters/:cluster_id/access/users/:userid/tokens/:tokenid.
func (h *AccessHandler) GetToken(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	tokenid, err := accessParam(c, "tokenid")
	if err != nil {
		return err
	}
	token, err := pxClient.GetAccessUserToken(c.Context(), userid, tokenid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(token)
}

// CreateToken handles POST /clusters/:cluster_id/access/users/:userid/tokens/:tokenid.
//
// The response carries the token secret. This is the only time it exists
// outside the cluster's own config — Proxmox has no read-back endpoint — so the
// caller must surface it to the operator immediately. It is deliberately kept
// out of the audit row.
func (h *AccessHandler) CreateToken(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	tokenid, err := accessParam(c, "tokenid")
	if err != nil {
		return err
	}

	var req proxmox.CreateAccessTokenParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	created, err := pxClient.CreateAccessUserToken(c.Context(), userid, tokenid, req)
	if err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]any{
		"userid":  userid,
		"tokenid": tokenid,
		"privsep": bool(created.Info.PrivSep),
		"comment": req.Comment,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_token", created.FullTokenID, "created", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_token", created.FullTokenID, "created")
	return c.Status(fiber.StatusCreated).JSON(created)
}

// UpdateToken handles PUT /clusters/:cluster_id/access/users/:userid/tokens/:tokenid.
//
// With regenerate set, the response carries a fresh secret and the old one
// stops working immediately — hence the self-credential guard.
func (h *AccessHandler) UpdateToken(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	tokenid, err := accessParam(c, "tokenid")
	if err != nil {
		return err
	}

	var req proxmox.UpdateAccessTokenParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	force := forceRequested(c)
	if req.Regenerate {
		if err := h.guardSelfCredential(c, clusterID, userid, tokenid, force); err != nil {
			return err
		}
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	updated, err := pxClient.UpdateAccessUserToken(c.Context(), userid, tokenid, req)
	if err != nil {
		return mapProxmoxError(err)
	}

	action := "updated"
	if req.Regenerate {
		action = "regenerated"
	}
	details, _ := json.Marshal(map[string]any{
		"userid":     userid,
		"tokenid":    tokenid,
		"regenerate": req.Regenerate,
		"forced":     force && req.Regenerate,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_token", userid+"!"+tokenid, action, details)
	h.publishAccessChange(c.Context(), clusterID, "pve_token", userid+"!"+tokenid, action)
	return c.JSON(updated)
}

// DeleteToken handles DELETE /clusters/:cluster_id/access/users/:userid/tokens/:tokenid.
func (h *AccessHandler) DeleteToken(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	userid, err := accessParam(c, "userid")
	if err != nil {
		return err
	}
	tokenid, err := accessParam(c, "tokenid")
	if err != nil {
		return err
	}
	force := forceRequested(c)
	if err := h.guardSelfCredential(c, clusterID, userid, tokenid, force); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteAccessUserToken(c.Context(), userid, tokenid); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"userid": userid, "tokenid": tokenid, "forced": force})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_token", userid+"!"+tokenid, "deleted", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_token", userid+"!"+tokenid, "deleted")
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

// accessGroupRequest is the body for group create and update.
// Comment is a pointer on update so an omitted field leaves the existing
// comment alone rather than clearing it.
type accessGroupRequest struct {
	GroupID string  `json:"groupid"`
	Comment *string `json:"comment"`
}

// ListGroups handles GET /clusters/:cluster_id/access/groups.
func (h *AccessHandler) ListGroups(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	groups, err := pxClient.GetAccessGroups(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, groups)
}

// GetGroup handles GET /clusters/:cluster_id/access/groups/:groupid.
func (h *AccessHandler) GetGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	groupid, err := accessParam(c, "groupid")
	if err != nil {
		return err
	}
	group, err := pxClient.GetAccessGroup(c.Context(), groupid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(group)
}

// CreateGroup handles POST /clusters/:cluster_id/access/groups.
func (h *AccessHandler) CreateGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	var req accessGroupRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.GroupID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateAccessGroup(c.Context(), req.GroupID, derefString(req.Comment)); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"groupid": req.GroupID, "comment": derefString(req.Comment)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_group", req.GroupID, "created", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_group", req.GroupID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateGroup handles PUT /clusters/:cluster_id/access/groups/:groupid.
func (h *AccessHandler) UpdateGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	groupid, err := accessParam(c, "groupid")
	if err != nil {
		return err
	}
	var req accessGroupRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateAccessGroup(c.Context(), groupid, proxmox.UpdateAccessGroupParams{Comment: req.Comment}); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"groupid": groupid, "comment": derefString(req.Comment)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_group", groupid, "updated", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_group", groupid, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteGroup handles DELETE /clusters/:cluster_id/access/groups/:groupid.
func (h *AccessHandler) DeleteGroup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	groupid, err := accessParam(c, "groupid")
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteAccessGroup(c.Context(), groupid); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"groupid": groupid})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_group", groupid, "deleted", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_group", groupid, "deleted")
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

// accessRoleRequest is the body for role create and update. Privs is a
// comma-separated privilege list, matching what Proxmox accepts and returns.
type accessRoleRequest struct {
	RoleID string  `json:"roleid"`
	Privs  *string `json:"privs"`
	Append bool    `json:"append"`
}

// ListRoles handles GET /clusters/:cluster_id/access/roles.
//
// The built-in Administrator role's privilege list is Proxmox's complete set of
// valid privileges, so a client can build a privilege picker from this response
// without hardcoding one that drifts as Proxmox adds privileges.
func (h *AccessHandler) ListRoles(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	roles, err := pxClient.GetAccessRoles(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, roles)
}

// GetRole handles GET /clusters/:cluster_id/access/roles/:roleid.
func (h *AccessHandler) GetRole(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	roleid, err := accessParam(c, "roleid")
	if err != nil {
		return err
	}
	privs, err := pxClient.GetAccessRole(c.Context(), roleid)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{"roleid": roleid, "privs": privs})
}

// CreateRole handles POST /clusters/:cluster_id/access/roles.
func (h *AccessHandler) CreateRole(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	var req accessRoleRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.RoleID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Role ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateAccessRole(c.Context(), req.RoleID, derefString(req.Privs)); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"roleid": req.RoleID, "privs": derefString(req.Privs)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_role", req.RoleID, "created", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_role", req.RoleID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateRole handles PUT /clusters/:cluster_id/access/roles/:roleid.
func (h *AccessHandler) UpdateRole(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	roleid, err := accessParam(c, "roleid")
	if err != nil {
		return err
	}
	var req accessRoleRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateAccessRole(c.Context(), roleid, proxmox.UpdateAccessRoleParams{
		Privs:  req.Privs,
		Append: req.Append,
	}); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"roleid": roleid, "privs": derefString(req.Privs), "append": req.Append})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_role", roleid, "updated", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_role", roleid, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteRole handles DELETE /clusters/:cluster_id/access/roles/:roleid.
func (h *AccessHandler) DeleteRole(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	roleid, err := accessParam(c, "roleid")
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteAccessRole(c.Context(), roleid); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]any{"roleid": roleid})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_role", roleid, "deleted", details)
	h.publishAccessChange(c.Context(), clusterID, "pve_role", roleid, "deleted")
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------------
// ACL
// ---------------------------------------------------------------------------

// ListACL handles GET /clusters/:cluster_id/access/acl.
func (h *AccessHandler) ListACL(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetAccessACL(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, entries)
}

// UpdateACL handles PUT /clusters/:cluster_id/access/acl.
//
// Proxmox uses one endpoint for grant and revoke; the request's delete flag
// selects which. The audit action follows suit so the trail reads correctly.
func (h *AccessHandler) UpdateACL(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", accessResource, clusterID); err != nil {
		return err
	}
	var req proxmox.UpdateAccessACLParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateAccessACL(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	action := "granted"
	if req.Delete {
		action = "revoked"
	}
	details, _ := json.Marshal(map[string]any{
		"path":   req.Path,
		"roles":  req.Roles,
		"users":  req.Users,
		"groups": req.Groups,
		"tokens": req.Tokens,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pve_acl", req.Path, action, details)
	h.publishAccessChange(c.Context(), clusterID, "pve_acl", req.Path, action)
	return c.JSON(fiber.Map{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Realms (read-only) and the capability probe
// ---------------------------------------------------------------------------

// ListDomains handles GET /clusters/:cluster_id/access/domains.
//
// Read-only: realm create/update/delete needs the Realm.Allocate privilege,
// which Proxmox places in its root privilege tier — no built-in role except
// Administrator carries it.
func (h *AccessHandler) ListDomains(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	domains, err := pxClient.GetAccessDomains(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, domains)
}

// GetDomain handles GET /clusters/:cluster_id/access/domains/:realm.
func (h *AccessHandler) GetDomain(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	realm, err := accessParam(c, "realm")
	if err != nil {
		return err
	}
	domain, err := pxClient.GetAccessDomain(c.Context(), realm)
	if err != nil {
		return mapProxmoxError(err)
	}
	// AccessDomain carries no credential field by construction — the struct
	// deliberately omits the LDAP/AD bind password rather than fetching and
	// blanking it.
	return c.JSON(domain)
}

// GetPermissions handles GET /clusters/:cluster_id/access/permissions.
//
// Reports what Nexara's own cluster credential is allowed to do. Proxmox
// declares this endpoint `user => 'all'`, so it answers even for a token that
// can do nothing else, which makes it a dependable capability probe: the UI
// can disable what will not work instead of surfacing a 403 after a form
// submit. That matters here because two common setups fall short —
// a privilege-separated token has no User.Modify unless explicitly granted,
// and PVEAdmin lacks both Sys.Modify (role management) and Realm.Allocate.
func (h *AccessHandler) GetPermissions(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", accessResource, clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	perms, err := pxClient.GetEffectivePermissions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(perms)
}

// derefString returns the pointed-to string, or "" for a nil pointer.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
