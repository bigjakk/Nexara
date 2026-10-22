package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nodememory"
)

// nodeMemoryResponse embeds ListResponse so the modules arrive in the same
// {items,total} envelope as every other collection, with the read's summary
// and state on top.
//
// Status is "never" before the node's first read, and otherwise what the last
// attempt found: "ok", "unavailable" (nothing was dialed: the cluster has no SSH
// credentials, or the node no address or pinned key) or "error". A failed
// attempt keeps the last good read, so read_at and the modules can sit beside
// an "error" status, and reason says why the newest attempt failed.
type nodeMemoryResponse struct {
	ListResponse[nodememory.Module]
	Status           string     `json:"status"`
	Reason           string     `json:"reason,omitempty"`
	AttemptedAt      *time.Time `json:"attempted_at,omitempty"`
	ReadAt           *time.Time `json:"read_at,omitempty"`
	InstalledBytes   int64      `json:"installed_bytes"`
	SlotsUsed        int32      `json:"slots_used"`
	SlotsTotal       int32      `json:"slots_total"`
	MaxCapacityBytes int64      `json:"max_capacity_bytes"`
	ECC              string     `json:"ecc,omitempty"`
	// NodeMemTotal is the memory the node's kernel reports. A firmware table
	// that lists less is incomplete: a virtual node's hot-plugged memory is in
	// no DIMM table.
	NodeMemTotal int64 `json:"node_mem_total"`
	// AutoRead says whether the background pass reads nodes; when false, only
	// Read now does.
	AutoRead bool `json:"auto_read"`
}

// SetNodeMemoryReader hands the handler the reader the daily pass shares, so a
// Read now and a pass on the same node open one SSH session between them.
func (h *NodeHandler) SetNodeMemoryReader(r *nodememory.Reader) { h.memory = r }

// GetNodeMemoryModules returns a node's last memory DIMM read. It never dials
// the node: reads happen in the daily pass and on Read now.
func (h *NodeHandler) GetNodeMemoryModules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	node, err := h.nodeByName(c.Context(), clusterID, nodeName)
	if err != nil {
		return err
	}
	resp, err := h.nodeMemoryState(c.Context(), node)
	if err != nil {
		return err
	}
	return c.JSON(resp)
}

// RefreshNodeMemoryModules reads a node's memory layout now, with dmidecode
// over the cluster's SSH credentials, and returns what it stored. A read that
// fails is still a 200: the failure and its reason are the stored state.
func (h *NodeHandler) RefreshNodeMemoryModules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	if h.memory == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Node memory reads are not available on this server")
	}
	node, err := h.nodeByName(c.Context(), clusterID, nodeName)
	if err != nil {
		return err
	}
	// c.Context() is Background; the reader bounds the read itself.
	readErr := h.memory.ReadNode(c.Context(), clusterID, node.ID, nodeName)
	if errors.Is(readErr, nodememory.ErrReadTooSoon) {
		c.Set(fiber.HeaderRetryAfter, "60")
		return fiber.NewError(fiber.StatusTooManyRequests, "This node was read less than a minute ago")
	}
	resp, stateErr := h.nodeMemoryState(c.Context(), node)
	// Audited whatever came of it: a root session may have been opened even
	// when storing its outcome failed.
	status := resp.Status
	if readErr != nil || stateErr != nil {
		status = "store_failed"
	}
	details, _ := json.Marshal(map[string]string{"status": status})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "node", nodeName, "read_memory_modules", details)
	if readErr != nil {
		slog.Warn("store node memory read", "cluster_id", clusterID, "node", nodeName, "error", readErr)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to store the node's memory read")
	}
	if stateErr != nil {
		return stateErr
	}
	return c.JSON(resp)
}

func (h *NodeHandler) nodeByName(ctx context.Context, clusterID uuid.UUID, nodeName string) (db.Node, error) {
	node, err := h.queries.GetNodeByClusterAndName(ctx, db.GetNodeByClusterAndNameParams{ClusterID: clusterID, Name: nodeName})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Node{}, fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")
	}
	if err != nil {
		return db.Node{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the node")
	}
	return node, nil
}

func (h *NodeHandler) nodeMemoryState(ctx context.Context, node db.Node) (nodeMemoryResponse, error) {
	autoRead := h.memory != nil && h.memory.Interval() > 0
	row, err := h.queries.GetNodeMemoryInventory(ctx, node.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		resp := neverReadMemory(autoRead)
		resp.NodeMemTotal = node.MemTotal
		return resp, nil
	}
	if err != nil {
		return nodeMemoryResponse{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the node's memory inventory")
	}
	resp, err := nodeMemoryResponseFrom(row, node.MemTotal, autoRead)
	if err != nil {
		slog.Warn("decode node memory modules", "node_id", node.ID, "error", err)
		return nodeMemoryResponse{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to read the node's memory inventory")
	}
	return resp, nil
}

func neverReadMemory(autoRead bool) nodeMemoryResponse {
	return nodeMemoryResponse{
		ListResponse: ListResponse[nodememory.Module]{Items: []nodememory.Module{}},
		Status:       "never",
		AutoRead:     autoRead,
	}
}

// nodeMemoryResponseFrom renders a stored read. A failed attempt keeps the
// last good read, so its reason sits beside the modules that read found.
func nodeMemoryResponseFrom(row db.NodeMemoryInventory, nodeMemTotal int64, autoRead bool) (nodeMemoryResponse, error) {
	modules, err := nodeMemoryModules(row.Modules)
	if err != nil {
		return nodeMemoryResponse{}, err
	}
	attempted := row.AttemptedAt
	resp := nodeMemoryResponse{
		ListResponse:     ListResponse[nodememory.Module]{Items: modules, Total: int64(len(modules))},
		Status:           row.Status,
		Reason:           row.Reason,
		AttemptedAt:      &attempted,
		InstalledBytes:   row.InstalledBytes,
		SlotsUsed:        row.SlotsUsed,
		SlotsTotal:       row.SlotsTotal,
		MaxCapacityBytes: row.MaxCapacityBytes,
		ECC:              row.Ecc,
		NodeMemTotal:     nodeMemTotal,
		AutoRead:         autoRead,
	}
	if row.ReadAt.Valid {
		read := row.ReadAt.Time
		resp.ReadAt = &read
	}
	return resp, nil
}

func nodeMemoryModules(raw json.RawMessage) ([]nodememory.Module, error) {
	modules := []nodememory.Module{}
	if len(raw) == 0 {
		return modules, nil
	}
	if err := json.Unmarshal(raw, &modules); err != nil {
		return nil, fmt.Errorf("decode node memory modules: %w", err)
	}
	return modules, nil
}
