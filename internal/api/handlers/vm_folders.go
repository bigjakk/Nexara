package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// 4 of the 5 routes are declared in internal/api/registry_vm_folders.go, which
// states their cluster-scoped permission (view:vm_folder for the listing,
// manage:vm_folder for the writes) and their parameters; nothing below
// re-checks either.
//
// Update — PATCH /vm-folders/:folder_id — stays in router.go with its
// hand-written body, and jsonNullUUID below is why: its parent_id has to tell
// "absent" from "explicit null", and apischema reads an explicit null as
// absent. See registerVMFolderEndpoints and TestVMFolderReparentIsStillLegacy.
//
// What stays here for all five is what a declaration cannot see: the
// cluster-membership check on every folder and guest the request names, the
// cycle check on a re-parent, and the unique-violation mapping onto 409. Every
// change to a cluster's tree — a move, a filing, a delete — runs under the
// cluster's folder lock (inFolderTree).

// VMFoldersHandler handles CRUD for the VM folder organisation layer
// surfaced by the "VMs & Templates" tree perspective. Folders are pure
// Nexara state — they never touch Proxmox.
type VMFoldersHandler struct {
	pool     txOptionsBeginner
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewVMFoldersHandler constructs a VMFoldersHandler. pool begins the
// transactions the tree changes run in (inFolderTree).
func NewVMFoldersHandler(pool txOptionsBeginner, queries *db.Queries, eventPub *events.Publisher) *VMFoldersHandler {
	return &VMFoldersHandler{pool: pool, queries: queries, eventPub: eventPub}
}

// inFolderTree runs fn in one READ COMMITTED transaction holding the cluster's
// folder lock (LockClusterFolders), committed when fn succeeds. Every move,
// filing and delete in the cluster's tree runs in one, so each reads the tree
// as the last one left it: a move's cycle check, the folder a filing or a
// move left. READ COMMITTED because each statement after the lock then reads
// a snapshot taken after it; REPEATABLE READ's would date from the lock's own
// statement, before the wait.
func (h *VMFoldersHandler) inFolderTree(ctx context.Context, clusterID uuid.UUID, fn func(*db.Queries) error) error {
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to change the folder tree")
	}
	defer releaseTx(tx, "folder tree change")()
	q := h.queries.WithTx(tx)
	err = q.SetLocalLockTimeout(ctx, folderLockTimeout)
	if err == nil {
		err = q.LockClusterFolders(ctx, clusterID)
	}
	if isTransientDBError(err) {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Another change to this cluster's folders is under way; try again")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to change the folder tree")
	}
	if err := fn(q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to change the folder tree")
	}
	return nil
}

// folderLockTimeout is the longest a change to a cluster's tree waits for the
// one before it: a change takes milliseconds, so a wait this long is a stuck
// one, and the request answers 503 rather than hold a pool connection.
const folderLockTimeout = "5s"

// treeFolder is the folder as the transaction reads it: Not Found when it is
// gone, or not the cluster's.
func treeFolder(ctx context.Context, q *db.Queries, folderID, clusterID uuid.UUID) (db.VmFolder, error) {
	folder, err := q.GetVMFolder(ctx, folderID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && folder.ClusterID != clusterID {
		return db.VmFolder{}, fiber.NewError(fiber.StatusNotFound, "Folder not found")
	}
	if err != nil {
		return db.VmFolder{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get folder")
	}
	return folder, nil
}

type vmFolderResponse struct {
	ID        uuid.UUID  `json:"id"`
	ClusterID uuid.UUID  `json:"cluster_id"`
	ParentID  *uuid.UUID `json:"parent_id"`
	Name      string     `json:"name"`
}

func toVMFolderResponse(f db.VmFolder) vmFolderResponse {
	resp := vmFolderResponse{
		ID:        f.ID,
		ClusterID: f.ClusterID,
		Name:      f.Name,
	}
	if f.ParentID.Valid {
		id, _ := uuid.FromBytes(f.ParentID.Bytes[:])
		resp.ParentID = &id
	}
	return resp
}

type vmFolderMembershipResponse struct {
	VMID     uuid.UUID `json:"vm_id"`
	FolderID uuid.UUID `json:"folder_id"`
}

type vmFolderListResponse struct {
	Folders     []vmFolderResponse           `json:"folders"`
	Memberships []vmFolderMembershipResponse `json:"memberships"`
}

// List returns every folder for the cluster plus every (vm_id, folder_id)
// membership so the frontend can render the whole tree from a single round
// trip.
func (h *VMFoldersHandler) List(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	folders, err := h.queries.ListVMFoldersByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list folders")
	}

	members, err := h.queries.ListVMFolderMembershipsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list memberships")
	}

	folderResp := make([]vmFolderResponse, len(folders))
	for i, f := range folders {
		folderResp[i] = toVMFolderResponse(f)
	}
	memberResp := make([]vmFolderMembershipResponse, len(members))
	for i, m := range members {
		memberResp[i] = vmFolderMembershipResponse{VMID: m.VmID, FolderID: m.FolderID}
	}

	return c.JSON(vmFolderListResponse{Folders: folderResp, Memberships: memberResp})
}

// Create handles POST /api/v1/clusters/:cluster_id/vm-folders.
func (h *VMFoldersHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// Trimmed HERE rather than in the schema: apischema validates the value as
	// sent, and this rule is about the stored name. The declaration's bound is
	// what closes the parameter; this is what decides the row.
	name := strings.TrimSpace(p.String("name"))
	if name == "" || len(name) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "Folder name must be 1-128 characters")
	}

	var parentID *uuid.UUID
	if raw, supplied := p.OptString("parent_id"); supplied {
		parsed, parseErr := parseParamUUID(raw)
		if parseErr != nil {
			return parseErr
		}
		parentID = &parsed
	}

	parent := pgtype.UUID{}
	if parentID != nil {
		if err := h.ensureFolderInCluster(c, *parentID, clusterID); err != nil {
			return err
		}
		parent = pgtype.UUID{Bytes: *parentID, Valid: true}
	}

	folder, err := h.queries.CreateVMFolder(c.Context(), db.CreateVMFolderParams{
		ClusterID: clusterID,
		ParentID:  parent,
		Name:      name,
	})
	if err != nil {
		// 23505 = unique_violation in Postgres; surfaces our (cluster_id,
		// parent_id, name) UNIQUE constraint as a 409 instead of 500.
		if isUniqueViolation(err) {
			return fiber.NewError(fiber.StatusConflict, "A folder with this name already exists at this level")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create folder")
	}

	details, _ := json.Marshal(map[string]any{"name": folder.Name, "parent_id": parentID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_folder", folder.ID.String(), "create", details)

	return c.Status(fiber.StatusCreated).JSON(toVMFolderResponse(folder))
}

type vmFolderUpdateRequest struct {
	// Pointer fields so we can tell "absent" from "explicit null".
	Name     *string      `json:"name,omitempty"`
	ParentID jsonNullUUID `json:"parent_id,omitempty"`
}

// jsonNullUUID distinguishes "field absent" / "field: null" / "field: <uuid>"
// in PATCH bodies. The standard encoding/json call leaves it Set=false and
// Value=zero when the key is missing, and decodes Set=true with Value as
// either the parsed UUID or zero when the key is present.
type jsonNullUUID struct {
	Set   bool
	Null  bool
	Value uuid.UUID
}

func (n *jsonNullUUID) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// Update handles PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id.
// Supports renaming (name) and re-parenting (parent_id, null for top level).
func (h *VMFoldersHandler) Update(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm_folder", clusterID); err != nil {
		return err
	}

	folderID, err := uuid.Parse(c.Params("folder_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid folder ID")
	}

	folder, err := h.queries.GetVMFolder(c.Context(), folderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Folder not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get folder")
	}
	if folder.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Folder not found")
	}

	var req vmFolderUpdateRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	auditFields := map[string]any{}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 128 {
			return fiber.NewError(fiber.StatusBadRequest, "Folder name must be 1-128 characters")
		}
		updated, renameErr := h.queries.RenameVMFolder(c.Context(), db.RenameVMFolderParams{ID: folderID, Name: name})
		if renameErr != nil {
			if isUniqueViolation(renameErr) {
				return fiber.NewError(fiber.StatusConflict, "A folder with this name already exists at this level")
			}
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to rename folder")
		}
		folder = updated
		auditFields["name"] = name
	}

	if req.ParentID.Set {
		var newParent pgtype.UUID
		if !req.ParentID.Null {
			if req.ParentID.Value == folderID {
				return fiber.NewError(fiber.StatusBadRequest, "Folder cannot be its own parent")
			}
			if err := h.ensureFolderInCluster(c, req.ParentID.Value, clusterID); err != nil {
				return err
			}
			newParent = pgtype.UUID{Bytes: req.ParentID.Value, Valid: true}
			auditFields["parent_id"] = req.ParentID.Value
		} else {
			auditFields["parent_id"] = nil
		}
		ctx := c.Context()
		if err := h.inFolderTree(ctx, clusterID, func(q *db.Queries) error {
			current, err := treeFolder(ctx, q, folderID, clusterID)
			if err != nil {
				return err
			}
			// The folder it left finds the move by from_parent_id, as it finds
			// a guest's departure by from_folder_id.
			auditFields["from_parent_id"] = folderParentID(current)
			moved, err := q.MoveVMFolder(ctx, db.MoveVMFolderParams{ID: folderID, ParentID: newParent})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return fiber.NewError(fiber.StatusBadRequest, "Cannot move folder into one of its descendants")
			case isUniqueViolation(err):
				return fiber.NewError(fiber.StatusConflict, "A folder with this name already exists at the new location")
			case err != nil:
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to move folder")
			}
			folder = moved
			return nil
		}); err != nil {
			return err
		}
	}

	if len(auditFields) > 0 {
		details, _ := json.Marshal(auditFields)
		AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_folder", folderID.String(), "update", details)
	}

	return c.JSON(toVMFolderResponse(folder))
}

// Delete handles DELETE /api/v1/clusters/:cluster_id/vm-folders/:folder_id.
// Cascade in the schema removes child folders and memberships; VMs are not
// touched, they simply lose their folder assignment and fall back to the
// implicit "unassigned" pseudo-folder on the frontend.
func (h *VMFoldersHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	folderID, err := parseParamUUID(p.String("folder_id"))
	if err != nil {
		return err
	}

	folder, err := h.queries.GetVMFolder(c.Context(), folderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Folder not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get folder")
	}
	if folder.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Folder not found")
	}

	ctx := c.Context()
	if err := h.inFolderTree(ctx, clusterID, func(q *db.Queries) error {
		// The parent it is deleted from, as the lock leaves it.
		current, err := treeFolder(ctx, q, folderID, clusterID)
		if err != nil {
			return err
		}
		folder = current
		if err := q.DeleteVMFolder(ctx, folderID); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete folder")
		}
		return nil
	}); err != nil {
		return err
	}

	// parent_id, so the folder it was in finds the delete: it has no row left.
	details, _ := json.Marshal(map[string]any{"name": folder.Name, "parent_id": folderParentID(folder)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_folder", folderID.String(), "delete", details)

	return c.SendStatus(fiber.StatusNoContent)
}

// AssignVM handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/folder.
// The VM must belong to the URL cluster and (if specified) the target
// folder must also belong to that same cluster.
func (h *VMFoldersHandler) AssignVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmID, err := parseParamUUID(p.String("vm_id"))
	if err != nil {
		return err
	}

	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM")
	}
	if vm.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "VM not found")
	}

	// Absent and an explicit null both mean "unassign", which is what the
	// declaration says and what this has always read out of a *uuid.UUID.
	var folderID *uuid.UUID
	if raw, supplied := p.OptString("folder_id"); supplied {
		parsed, parseErr := parseParamUUID(raw)
		if parseErr != nil {
			return parseErr
		}
		folderID = &parsed
	}

	// from_folder_id names the folder the guest left, when it was in one: that
	// folder's History finds the departure by it, having no other record.
	ctx := c.Context()
	if folderID == nil {
		fields := map[string]any{"vmid": vm.Vmid, "folder_id": nil}
		if err := h.inFolderTree(ctx, clusterID, func(q *db.Queries) error {
			from, err := q.UnassignVMFromFolder(ctx, db.UnassignVMFromFolderParams{ClusterID: clusterID, Vmid: vm.Vmid})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to unassign VM")
			}
			if err == nil {
				fields["from_folder_id"] = from
			}
			return nil
		}); err != nil {
			return err
		}
		details, _ := json.Marshal(fields)
		AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_folder_membership", vmID.String(), "unassign", details)
		return c.SendStatus(fiber.StatusNoContent)
	}

	if err := h.ensureFolderInCluster(c, *folderID, clusterID); err != nil {
		return err
	}
	var from string
	if err := h.inFolderTree(ctx, clusterID, func(q *db.Queries) error {
		var err error
		from, err = q.AssignVMToFolder(ctx, db.AssignVMToFolderParams{ClusterID: clusterID, Vmid: vm.Vmid, FolderID: *folderID})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to assign VM")
		}
		return nil
	}); err != nil {
		return err
	}

	fields := map[string]any{"vmid": vm.Vmid, "folder_id": *folderID}
	if from != "" {
		fields["from_folder_id"] = from
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm_folder_membership", vmID.String(), "assign", details)

	return c.SendStatus(fiber.StatusNoContent)
}

// folderParentID is a folder's parent as an audit row names it: its id, or
// null for a folder at the top level.
func folderParentID(f db.VmFolder) any {
	if !f.ParentID.Valid {
		return nil
	}
	return uuid.UUID(f.ParentID.Bytes)
}

// ensureFolderInCluster returns 404 if the folder doesn't exist or doesn't
// belong to the supplied cluster. Centralised so cross-cluster references
// can't leak via crafted requests.
func (h *VMFoldersHandler) ensureFolderInCluster(c fiber.Ctx, folderID, clusterID uuid.UUID) error {
	folder, err := h.queries.GetVMFolder(c.Context(), folderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Folder not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up folder")
	}
	if folder.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Folder not found")
	}
	return nil
}

// folderDepthCap is the deepest folder tree Nexara walks: a runaway guard, far
// beyond any tree a person builds. MoveVMFolder's cycle check holds to the same
// 1,024 levels, refusing a move above a deeper tree.
const folderDepthCap = 1024

// isUniqueViolation reports whether err comes from a Postgres unique
// constraint failure (SQLSTATE 23505). Keeps the dependency on pgconn out
// of every call site.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps SQLSTATE errors in *pgconn.PgError; rather than importing
	// pgconn for one check, just inspect the error string. The state code
	// is stable across pgx versions.
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}
