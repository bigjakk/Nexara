package handlers

import (
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

// What a UPS powers, by hand. The routes are Deferred: each needs global
// manage:ups, and a node also view:node on its cluster, checked before
// anything says whether the node exists, is fed or is in a feed at all; a feed
// in a cluster the caller cannot see answers as if it were not there.

// AddFeed handles POST /api/v1/ups-devices/:id/feeds: a node the UPS powers,
// or a load that is not a node, such as a switch.
func (h *NutHandler) AddFeed(c fiber.Ctx, p *apischema.Params) error {
	if err := requirePerm(c, "manage", "ups"); err != nil {
		return err
	}
	dev, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	node, label := p.String("node_name"), strings.TrimSpace(p.String("label"))
	arg := db.InsertManualUpsFeedParams{UpsID: dev.ID, Label: label}
	arg.CreatedBy, _ = c.Locals("user_id").(uuid.UUID)
	switch {
	case node != "" && label != "":
		return fiber.NewError(fiber.StatusBadRequest, "Name a node or a label, not both")
	case node != "":
		cluster, err := parseParamUUID(p.String("feed_cluster_id"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "A node needs its feed_cluster_id")
		}
		if err := requireClusterPerm(c, "view", "node", cluster); err != nil {
			return err
		}
		if err := requireNodeInCluster(c, lookupOf(h.queries), cluster, node); err != nil {
			return err
		}
		arg.ClusterID = pgtype.UUID{Bytes: cluster, Valid: true}
		arg.NodeName = pgtype.Text{String: node, Valid: true}
	case label == "":
		return fiber.NewError(fiber.StatusBadRequest, "Name a node or a label")
	}
	feed, err := h.queries.InsertManualUpsFeed(c.Context(), arg)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fiber.NewError(fiber.StatusConflict, "This UPS already powers that node")
	case isUniqueViolation(err):
		return fiber.NewError(fiber.StatusConflict, "This UPS already powers a load by that name")
	case err != nil:
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to add what the UPS powers")
	}
	h.auditFeed(c, dev, feed, "ups_feed_added", nil)
	return c.Status(fiber.StatusCreated).JSON(upsFeedOf(feed))
}

// RemoveFeed handles DELETE /api/v1/ups-devices/:id/feeds/:feed_id. A node
// is kept as removed, so a login from it does not find it again; adding it by
// hand brings it back. A label is deleted.
func (h *NutHandler) RemoveFeed(c fiber.Ctx, p *apischema.Params) error {
	dev, feed, err := h.authorizedFeed(c, p)
	if err != nil {
		return err
	}
	if feed.ClusterID.Valid {
		_, err = h.queries.DismissUpsNodeFeed(c.Context(), db.DismissUpsNodeFeedParams{ID: feed.ID, UpsID: dev.ID})
	} else {
		_, err = h.queries.DeleteUpsLabelFeed(c.Context(), db.DeleteUpsLabelFeedParams{ID: feed.ID, UpsID: dev.ID})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fiber.NewError(fiber.StatusNotFound, "Feed not found")
	case err != nil:
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to remove what the UPS powers")
	}
	h.auditFeed(c, dev, feed, "ups_feed_removed", nil)
	return c.SendStatus(fiber.StatusNoContent)
}

// UpdateFeed handles PUT /api/v1/ups-devices/:id/feeds/:feed_id: whether a
// node shuts down some way Nexara cannot see (P11).
func (h *NutHandler) UpdateFeed(c fiber.Ctx, p *apischema.Params) error {
	dev, feed, err := h.authorizedFeed(c, p)
	if err != nil {
		return err
	}
	if !feed.ClusterID.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "Only a node can be protected another way")
	}
	elsewhere := p.Bool("protected_elsewhere")
	updated, err := h.queries.SetUpsFeedProtectedElsewhere(c.Context(), db.SetUpsFeedProtectedElsewhereParams{
		ID: feed.ID, UpsID: dev.ID, ProtectedElsewhere: elsewhere,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fiber.NewError(fiber.StatusNotFound, "Feed not found")
	case err != nil:
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to change what the UPS powers")
	}
	h.auditFeed(c, dev, updated, "ups_feed_updated", map[string]any{"protected_elsewhere": elsewhere})
	return c.JSON(upsFeedOf(updated))
}

// authorizedFeed reads the device and the feed a route names, after manage:ups,
// and refuses a node feed in a cluster whose nodes the caller cannot view as
// not found.
func (h *NutHandler) authorizedFeed(c fiber.Ctx, p *apischema.Params) (db.ListUpsDevicesRow, db.UpsFeed, error) {
	if err := requirePerm(c, "manage", "ups"); err != nil {
		return db.ListUpsDevicesRow{}, db.UpsFeed{}, err
	}
	dev, err := h.fetchDevice(c, p)
	if err != nil {
		return db.ListUpsDevicesRow{}, db.UpsFeed{}, err
	}
	id, err := parseParamUUID(p.String("feed_id"))
	if err != nil {
		return db.ListUpsDevicesRow{}, db.UpsFeed{}, err
	}
	feed, err := h.queries.GetUpsFeed(c.Context(), db.GetUpsFeedParams{ID: id, UpsID: dev.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ListUpsDevicesRow{}, db.UpsFeed{}, fiber.NewError(fiber.StatusNotFound, "Feed not found")
	}
	if err != nil {
		return db.ListUpsDevicesRow{}, db.UpsFeed{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read what the UPS powers")
	}
	if feed.ClusterID.Valid {
		ok, err := hasClusterPerm(c, "view", "node", uuid.UUID(feed.ClusterID.Bytes))
		if err != nil {
			return db.ListUpsDevicesRow{}, db.UpsFeed{}, err
		}
		if !ok {
			return db.ListUpsDevicesRow{}, db.UpsFeed{}, fiber.NewError(fiber.StatusNotFound, "Feed not found")
		}
	}
	return dev, feed, nil
}

// auditFeed records a change to what a UPS powers, on the node's cluster, so
// only those who may audit that cluster read it; a label's row is global.
func (h *NutHandler) auditFeed(c fiber.Ctx, dev db.ListUpsDevicesRow, feed db.UpsFeed, action string, extra map[string]any) {
	details := map[string]any{"ups": dev.UpsName, "server": dev.ServerName, "feed_id": feed.ID}
	if feed.ClusterID.Valid {
		details["node"] = feed.NodeName.String
	} else {
		details["label"] = feed.Label
	}
	for k, v := range extra {
		details[k] = v
	}
	blob, err := json.Marshal(details)
	if err != nil {
		blob = json.RawMessage(`{}`)
	}
	AuditLog(c, h.queries, h.eventPub, feed.ClusterID, "ups_device", dev.ID.String(), action, blob)
	if h.eventPub != nil {
		h.eventPub.SystemEvent(c.Context(), events.KindUPSChange, "devices_changed")
	}
}

// upsFeedOf is a feed as a write answers it: without a verdict, which the
// device list judges.
func upsFeedOf(f db.UpsFeed) upsFeedResponse {
	resp := upsFeedResponse{ID: f.ID, Kind: "label", Label: f.Label, Source: f.Source, CreatedAt: f.CreatedAt}
	if f.ClusterID.Valid {
		cluster := uuid.UUID(f.ClusterID.Bytes)
		resp.Kind, resp.ClusterID, resp.NodeName = "node", &cluster, f.NodeName.String
		resp.ProtectedElsewhere = f.ProtectedElsewhere
	}
	return resp
}

// SetHostNode handles PUT /api/v1/nut-servers/:id/host-node: the node the NUT
// server runs on, or detect to hand it back to the poller.
func (h *NutHandler) SetHostNode(c fiber.Ctx, p *apischema.Params) error {
	if err := requirePerm(c, "manage", "ups"); err != nil {
		return err
	}
	// A server has one host node, in any cluster: replacing it, or handing it
	// back to detection, could overwrite one the caller cannot see, so this
	// takes view:node everywhere, and refuses alike whatever is stored.
	if ok, err := hasGlobalPerm(c, "view", "node"); err != nil {
		return err
	} else if !ok {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}
	server, err := h.fetchServer(c, p)
	if err != nil {
		return err
	}
	arg := db.SetNutServerHostNodeByHandParams{ID: server.ID}
	node := p.String("node_name")
	switch {
	case p.Bool("detect") && (node != "" || p.Has("host_cluster_id")):
		return fiber.NewError(fiber.StatusBadRequest, "Name a node or ask for detection, not both")
	case !p.Bool("detect"):
		cluster, err := parseParamUUID(p.String("host_cluster_id"))
		if err != nil || node == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Name the node, with its host_cluster_id, or set detect")
		}
		if err := requireClusterPerm(c, "view", "node", cluster); err != nil {
			return err
		}
		if err := requireNodeInCluster(c, lookupOf(h.queries), cluster, node); err != nil {
			return err
		}
		arg.ClusterID = pgtype.UUID{Bytes: cluster, Valid: true}
		arg.NodeName = pgtype.Text{String: node, Valid: true}
	}
	if _, err := h.queries.SetNutServerHostNodeByHand(c.Context(), arg); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "NUT server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to set the NUT server's host node")
	}
	// On the node's cluster, like a feed, and on the cluster that lost its host
	// as well when that is another one; each row names only its own cluster's node.
	details, _ := json.Marshal(map[string]any{"name": server.Name, "host_node": node, "detect": p.Bool("detect")})
	AuditLog(c, h.queries, h.eventPub, arg.ClusterID, "nut_server", server.ID.String(), "nut_server_host_node_set", details)
	if server.HostClusterID.Valid && server.HostClusterID != arg.ClusterID {
		was, _ := json.Marshal(map[string]any{"name": server.Name, "host_node_removed": server.HostNodeName.String, "detect": p.Bool("detect")})
		AuditLog(c, h.queries, h.eventPub, server.HostClusterID, "nut_server", server.ID.String(), "nut_server_host_node_set", was)
	}
	if h.eventPub != nil {
		h.eventPub.SystemEvent(c.Context(), events.KindUPSChange, "server_changed")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
