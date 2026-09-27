package handlers

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Both routes are declared in internal/api/registry_mappings.go, which states
// their permissions and parameters and records why they exist: a token can
// pass a host device through only by mapping. What stays here is the audit row
// and the mapping of Proxmox's duplicate-id refusal onto 409.

// ListNodeUSBMappings handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/usb-mappings.
func (h *VMHandler) ListNodeUSBMappings(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	mappings, err := pxClient.ListUSBMappings(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, mappings)
}

// CreateUSBMapping handles POST /api/v1/clusters/:cluster_id/usb-mappings.
func (h *VMHandler) CreateUSBMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreateUSBMappingParams{
		ID:          p.String("mapping_id"),
		Description: p.String("description"),
		Node:        p.String("node"),
		DeviceID:    p.String("device_id"),
		Path:        p.String("path"),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateUSBMapping(c.Context(), req); err != nil {
		return mapUSBMappingCreateError(err)
	}

	// The device id as Proxmox stores it — the client lowercases it — and the
	// port only when there is one.
	audit := map[string]string{
		"mapping_id": req.ID,
		"node":       req.Node,
		"device_id":  strings.ToLower(req.DeviceID),
	}
	if req.Path != "" {
		audit["path"] = req.Path
	}
	details, _ := json.Marshal(audit)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "usb_mapping", req.ID, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// usbMappingTakenPhrases is how Proxmox refuses a mapping id that is taken:
// pve-manager PVE/API2/Cluster/Mapping/USB.pm, create, dies
// "usb ID '$id' already defined" inside lock_usb_config, which re-dies it as
// "create hardware mapping failed: usb ID '…' already defined" — a plain 500.
// It says "already defined", not "already exists", so mapDuplicateNameError
// cannot see it. A mapping id is a pve-configid and cannot hold a space, so
// the id Proxmox echoes back can never be what matches.
var usbMappingTakenPhrases = []string{"already defined"}

// mapUSBMappingCreateError answers a taken mapping id with 409, where
// mapProxmoxError alone would report the cluster as failing.
func mapUSBMappingCreateError(err error) error {
	return mapProxmoxDieError(fiber.StatusConflict,
		"A USB mapping with that ID already exists", usbMappingTakenPhrases, err)
}
