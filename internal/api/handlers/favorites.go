package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// FavoritesHandler serves each user's own starred clusters, nodes and guests.
//
// Favorites are pure Nexara state and never touch Proxmox. They are also
// personal: a favorite is one user's shortcut, invisible to everyone else, in
// the same category as which sidebar branches they leave expanded. Nothing here
// writes an audit row for that reason — starring changes no infrastructure, and
// an audit row per toggle would be an unbounded write available to any
// authenticated user for what is a UI preference.
//
// The decisive reason, against this project's audit-everything default: view:audit
// is granted to every Viewer by default, so auditing stars would publish which
// guests each user watches to everyone on the instance. That makes it a privacy
// regression rather than merely noise.
type FavoritesHandler struct {
	queries *db.Queries
}

// NewFavoritesHandler constructs a FavoritesHandler.
func NewFavoritesHandler(queries *db.Queries) *FavoritesHandler {
	return &FavoritesHandler{queries: queries}
}

// Favorite resource types. These are the values stored in
// user_favorites.resource_type and constrained by 000101's CHECK.
const (
	favoriteCluster = "cluster"
	favoriteNode    = "node"
	favoriteVM      = "vm"
)

// FavoriteResourceTypes returns the accepted resource_type values, sorted.
//
// Exported for the guard in package api that compares them against the Enum
// declared in internal/api/registry_favorites.go: they are two copies of one
// list, and the direction that bites is a schema accepting a type
// parseFavoriteTarget has no branch for. Package handlers cannot import package
// api, so the comparison reads this from the other side.
func FavoriteResourceTypes() []string {
	return []string{favoriteCluster, favoriteNode, favoriteVM}
}

// maxFavoritesPerUser caps how many favorites one user can hold at once.
//
// It is the limit the operator sees, not the thing that bounds the table: it
// counts only favorites that still resolve, so on its own it would let a caller
// mint unlimited rows that never resolve. The actual bound is the existence
// check in AddFavorite — every row has to name a real resource the caller can
// already see. This number just stops the sidebar becoming a second inventory,
// and sits far above any plausible use.
const maxFavoritesPerUser = 200

// maxNodeRefBytes bounds a node-name ref. A hostname is at most 253 bytes and a
// PVE node name is a single label, so this only ever rejects junk.
const maxNodeRefBytes = 255

// maxVMID is the largest VMID Proxmox itself allows, and the same ceiling
// 000101's `^[0-9]{1,9}$` CHECK enforces.
//
// It has to be checked explicitly: ParseInt(ref, 10, 32) accepts up to
// 2147483647, which is ten digits, so 1000000000..2147483647 would pass the Go
// validation and then violate the CHECK — a 500 for what is a client error, and
// not a foreign-key violation, so the mapping below would not catch it.
const maxVMID = 999999999

// favoriteResponse is one starred resource, resolved to everything the sidebar
// needs to draw and navigate to it without expanding its cluster first.
type favoriteResponse struct {
	ResourceType string `json:"resource_type"`
	ClusterID    string `json:"cluster_id"`
	ClusterName  string `json:"cluster_name"`
	// Ref is the stable identity the favorite is stored against, and what the
	// client sends back to unstar: empty for a cluster, the node name for a
	// node, the VMID for a guest.
	Ref string `json:"ref"`
	// TargetID is the row id to route to *right now*. It is resolved on every
	// read rather than stored, because the collector re-issues these UUIDs (see
	// migrations/000101) — a stored one would be stale after any live
	// migration.
	TargetID string `json:"target_id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	// VMKind is "qemu" or "lxc" for a guest and empty otherwise. It selects the
	// SPA's /inventory/:kind route segment as well as the icon.
	VMKind string `json:"vm_kind"`
	VMID   int32  `json:"vmid"`
	// NodeName is the node a guest is on right now — what the row's context
	// menu needs to offer migrate and console. Empty for the other types.
	NodeName     string `json:"node_name"`
	Template     bool   `json:"template"`
	HAState      string `json:"ha_state"`
	Ostype       string `json:"ostype"`
	ConfigOstype string `json:"config_ostype"`
	CreatedAt    string `json:"created_at"`
}

// ListFavorites handles GET /api/v1/favorites.
//
// Returns the caller's own favorites across every cluster, so the sidebar can
// render them above the tree without expanding anything.
//
// The listing is scoped per resource type against the same permission the
// endpoint that normally lists that resource requires — view:cluster,
// view:node, view:vm — so a favorite can never show a name the caller has since
// lost the right to see. The rows stay in the table either way; only the
// listing is filtered, so restoring the grant restores the shortcut.
func (h *FavoritesHandler) ListFavorites(c fiber.Ctx, _ *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// Ordered clusters → nodes → guests, each alphabetically by the SQL, which
	// is the same top-down order the tree below reads in.
	items := make([]favoriteResponse, 0)

	clusterAccess, err := accessibleClusters(c, "view", favoriteCluster)
	if err != nil {
		return err
	}
	if scope, query := clusterScopeFilter(clusterAccess); query {
		rows, listErr := h.queries.ListFavoriteClusters(c.Context(), db.ListFavoriteClustersParams{
			UserID:               userID,
			AccessibleClusterIds: scope,
		})
		if listErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list favorites")
		}
		for _, r := range rows {
			items = append(items, favoriteResponse{
				ResourceType: favoriteCluster,
				ClusterID:    r.ClusterID.String(),
				ClusterName:  r.ClusterName,
				TargetID:     r.ClusterID.String(),
				Name:         r.ClusterName,
				CreatedAt:    r.CreatedAt.Format(time.RFC3339Nano),
			})
		}
	}

	nodeAccess, err := accessibleClusters(c, "view", favoriteNode)
	if err != nil {
		return err
	}
	if scope, query := clusterScopeFilter(nodeAccess); query {
		rows, listErr := h.queries.ListFavoriteNodes(c.Context(), db.ListFavoriteNodesParams{
			UserID:               userID,
			AccessibleClusterIds: scope,
		})
		if listErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list favorites")
		}
		for _, r := range rows {
			items = append(items, favoriteResponse{
				ResourceType: favoriteNode,
				ClusterID:    r.ClusterID.String(),
				ClusterName:  r.ClusterName,
				Ref:          r.NodeName,
				TargetID:     r.NodeID.String(),
				Name:         r.NodeName,
				Status:       r.Status,
				HAState:      r.HaState,
				CreatedAt:    r.CreatedAt.Format(time.RFC3339Nano),
			})
		}
	}

	vmAccess, err := accessibleClusters(c, "view", favoriteVM)
	if err != nil {
		return err
	}
	if scope, query := clusterScopeFilter(vmAccess); query {
		rows, listErr := h.queries.ListFavoriteVMs(c.Context(), db.ListFavoriteVMsParams{
			UserID:               userID,
			AccessibleClusterIds: scope,
		})
		if listErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list favorites")
		}
		for _, r := range rows {
			items = append(items, favoriteResponse{
				ResourceType: favoriteVM,
				ClusterID:    r.ClusterID.String(),
				ClusterName:  r.ClusterName,
				Ref:          strconv.FormatInt(int64(r.Vmid), 10),
				TargetID:     r.VmID.String(),
				Name:         r.VmName,
				Status:       r.Status,
				VMKind:       r.Type,
				VMID:         r.Vmid,
				Template:     r.Template,
				NodeName:     r.NodeName,
				Ostype:       r.Ostype,
				ConfigOstype: r.ConfigOstype,
				CreatedAt:    r.CreatedAt.Format(time.RFC3339Nano),
			})
		}
	}

	return RespondItems(c, items)
}

// favoriteRequest is the validated reference both the add and the remove carry
// — in a body on one and in a query string on the other, declared once as
// favoriteTargetParams (internal/api/registry_favorites.go).
type favoriteRequest struct {
	ResourceType string
	ClusterID    string
	Ref          string
}

// favoriteRequestFromParams reads the three fields out of the validated
// parameters. One reader for both routes, so they cannot drift on what a
// reference is — the cluster arrives under the declared name
// favorite_cluster_id, whatever spelling the caller used for it.
func favoriteRequestFromParams(p *apischema.Params) favoriteRequest {
	return favoriteRequest{
		ResourceType: p.String("resource_type"),
		ClusterID:    p.String("favorite_cluster_id"),
		Ref:          p.String("ref"),
	}
}

// favoriteTarget is a validated favoriteRequest.
type favoriteTarget struct {
	ResourceType string
	ClusterID    uuid.UUID
	Ref          string
	// VMID is the parsed Ref for a guest and 0 otherwise. Carried as an int so
	// the existence check can compare against vms.vmid without casting the text
	// column, which is the thing ListFavoriteVMs needs a fence for.
	VMID int32
}

// parseFavoriteTarget validates a favorite reference and normalises its ref.
//
// The accepted refs are exactly what 000101's CHECK constraints allow — the two
// bounds are kept deliberately equal, because a row that passes here and
// violates the CHECK fails the INSERT with a 500 rather than a useful message:
// empty for a cluster (cluster_id already names it), a non-empty node name, or
// a non-negative decimal VMID. Returning the parsed VMID's canonical decimal
// form matters — "0101" and "101" name the same guest but would be two
// different primary keys, so one could be starred twice and unstarred never.
func parseFavoriteTarget(req favoriteRequest) (favoriteTarget, error) {
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ref := strings.TrimSpace(req.Ref)
	var vmidValue int32
	switch req.ResourceType {
	case favoriteCluster:
		// The cluster is named by cluster_id; anything in ref would create a
		// second, unreachable primary key for the same star.
		ref = ""
	case favoriteNode:
		if ref == "" {
			return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest, "ref must be the node name")
		}
		// Bounded because resource_ref is part of the primary key: a ref past
		// the btree index-tuple limit fails the INSERT with an index-size error,
		// which would surface as an opaque 500. A PVE node name is a hostname,
		// so this is far above anything real.
		if len(ref) > maxNodeRefBytes {
			return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest, "node name is too long")
		}
		// A NUL is a valid Go string byte and cannot be stored in a Postgres
		// text column at all, so it fails at encode time as another opaque 500.
		// No control character belongs in a hostname regardless.
		if strings.ContainsFunc(ref, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest, "node name contains a control character")
		}
	case favoriteVM:
		vmid, convErr := strconv.ParseInt(ref, 10, 32)
		if convErr != nil || vmid < 0 || vmid > maxVMID {
			return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest, "ref must be the VMID")
		}
		ref = strconv.FormatInt(vmid, 10)
		vmidValue = int32(vmid)
	default:
		// Unreachable through the declared Enum, and kept fail-closed for the
		// same reason parseParamUUID keeps its branch: it fires if a
		// declaration ever drops the Enum, and a favorite with an unrecognised
		// type would otherwise be stored against a CHECK constraint that
		// refuses it.
		return favoriteTarget{}, fiber.NewError(fiber.StatusBadRequest,
			"resource_type must be one of: cluster, node, vm")
	}

	return favoriteTarget{
		ResourceType: req.ResourceType,
		ClusterID:    clusterID,
		Ref:          ref,
		VMID:         vmidValue,
	}, nil
}

// AddFavorite handles POST /api/v1/favorites.
//
// Gated on view access to the target's own resource type in its cluster, so
// starring cannot be used to confirm that a cluster id exists: without the
// check, a caller who cannot see a cluster would get a foreign-key 500 for a
// real id and a quiet success for an invented one.
func (h *FavoritesHandler) AddFavorite(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	target, err := parseFavoriteTarget(favoriteRequestFromParams(p))
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", target.ResourceType, target.ClusterID); err != nil {
		return err
	}

	// Refuse to store a favorite for something that is not there. This is what
	// bounds the table: without it, CountResolvableFavorites — which by design
	// ignores rows that no longer resolve — would never count a ref that never
	// resolved, so refs that match nothing could be inserted without limit. With
	// it, every row costs a distinct real resource the caller can already see.
	//
	// It also gives the caller a usable answer for the ordinary race: a guest
	// destroyed between the sidebar rendering and the star being clicked.
	exists, err := h.queries.FavoriteTargetExists(c.Context(), db.FavoriteTargetExistsParams{
		ResourceType: target.ResourceType,
		ClusterID:    target.ClusterID,
		NodeName:     target.Ref,
		Vmid:         target.VMID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up favorite target")
	}
	if !exists {
		return fiber.NewError(fiber.StatusNotFound, "Not found")
	}

	// Checked before the insert rather than enforced by the database, so the
	// caller gets a message they can act on. Two concurrent adds can both pass
	// this and land 201 rows; the point is the bound, not the exact number.
	//
	// Counts only favorites that still resolve — see the note on the query. A
	// bare count would include rows whose target has been destroyed, which the
	// listing never returns and the UI therefore offers no way to remove, so the
	// cap could be reached with an all-but-empty sidebar and no way to clear it.
	count, err := h.queries.CountResolvableFavorites(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count favorites")
	}
	if count >= maxFavoritesPerUser {
		return fiber.NewError(fiber.StatusBadRequest,
			"Favorite limit reached ("+strconv.Itoa(maxFavoritesPerUser)+"); remove one first")
	}

	if err := h.queries.AddFavorite(c.Context(), db.AddFavoriteParams{
		UserID:       userID,
		ClusterID:    target.ClusterID,
		ResourceType: target.ResourceType,
		ResourceRef:  target.Ref,
	}); err != nil {
		// A cluster deleted between the sidebar rendering and the star being
		// clicked violates the foreign key. That is the caller naming something
		// that no longer exists, not a server fault, and reporting it as a 500
		// would put a routine race into the error logs.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to add favorite")
	}

	return c.JSON(fiber.Map{"message": "Favorite added"})
}

// RemoveFavorite handles DELETE /api/v1/favorites.
//
// Deliberately ungated, unlike AddFavorite. The delete is keyed by the
// authenticated user id, so it can only ever reach the caller's own row, and
// requiring view access to unstar would strand rows: someone who loses a grant
// would be left with a favorite they can neither see nor delete.
//
// Idempotent — removing a favorite that is not there succeeds, which is what a
// star toggle wants when two clicks race.
func (h *FavoritesHandler) RemoveFavorite(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	target, err := parseFavoriteTarget(favoriteRequestFromParams(p))
	if err != nil {
		return err
	}

	if err := h.queries.RemoveFavorite(c.Context(), db.RemoveFavoriteParams{
		UserID:       userID,
		ClusterID:    target.ClusterID,
		ResourceType: target.ResourceType,
		ResourceRef:  target.Ref,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to remove favorite")
	}

	return c.JSON(fiber.Map{"message": "Favorite removed"})
}
