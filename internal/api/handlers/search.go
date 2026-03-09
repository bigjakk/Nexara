package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
)

// SearchHandler handles global search endpoints.
type SearchHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewSearchHandler creates a new SearchHandler.
func NewSearchHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *SearchHandler {
	return &SearchHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

type searchResult struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Node        string `json:"node,omitempty"`
	Status      string `json:"status,omitempty"`
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	VMID        int    `json:"vmid,omitempty"`
}

// GlobalSearch handles GET /api/v1/search?q=...
func (h *SearchHandler) GlobalSearch(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
		return err
	}
	query := strings.TrimSpace(c.Query("q"))
	if query == "" || len(query) < 2 {
		return c.JSON([]searchResult{})
	}
	queryLower := strings.ToLower(query)

	var results []searchResult

	// Search VMs and containers from database.
	allVMs, err := h.queries.ListAllVMs(c.Context())
	if err == nil {
		// Build a node name cache.
		nodeNames := make(map[string]string)

		for _, vm := range allVMs {
			nameLower := strings.ToLower(vm.Name)
			vmidStr := fmt.Sprintf("%d", vm.Vmid)
			if strings.Contains(nameLower, queryLower) || strings.Contains(vmidStr, queryLower) {
				// Resolve node name lazily.
				nodeID := vm.NodeID.String()
				nodeName, ok := nodeNames[nodeID]
				if !ok {
					node, nodeErr := h.queries.GetNode(c.Context(), vm.NodeID)
					if nodeErr == nil {
						nodeName = node.Name
					}
					nodeNames[nodeID] = nodeName
				}

				resType := "vm"
				if vm.Type == "lxc" {
					resType = "ct"
				}
				results = append(results, searchResult{
					Type:        resType,
					ID:          vm.ID.String(),
					Name:        vm.Name,
					Node:        nodeName,
					Status:      vm.Status,
					ClusterID:   vm.ClusterID.String(),
					ClusterName: vm.ClusterName,
					VMID:        int(vm.Vmid),
				})
			}
			if len(results) > 100 {
				return c.JSON(results)
			}
		}
	}

	// Search nodes from database.
	clusters, err := h.queries.ListClusters(c.Context())
	if err == nil {
		for _, cluster := range clusters {
			// Nodes.
			nodes, nodeErr := h.queries.ListNodesByCluster(c.Context(), cluster.ID)
			if nodeErr == nil {
				for _, node := range nodes {
					if strings.Contains(strings.ToLower(node.Name), queryLower) {
						results = append(results, searchResult{
							Type:        "node",
							ID:          node.ID.String(),
							Name:        node.Name,
							Status:      node.Status,
							ClusterID:   cluster.ID.String(),
							ClusterName: cluster.Name,
						})
					}
				}
			}

			// Storage pools.
			pools, poolErr := h.queries.ListStoragePoolsByCluster(c.Context(), cluster.ID)
			if poolErr == nil {
				// Deduplicate shared storage (same name appears on multiple nodes).
				seen := make(map[string]bool)
				for _, pool := range pools {
					dedupKey := cluster.ID.String() + ":" + pool.Storage
					if seen[dedupKey] {
						continue
					}
					seen[dedupKey] = true

					if strings.Contains(strings.ToLower(pool.Storage), queryLower) {
						// Resolve node name for context.
						node, nodeErr := h.queries.GetNode(c.Context(), pool.NodeID)
						nodeName := ""
						if nodeErr == nil {
							nodeName = node.Name
						}
						results = append(results, searchResult{
							Type:        "storage",
							ID:          pool.ID.String(),
							Name:        pool.Storage,
							Node:        nodeName,
							Status:      fmt.Sprintf("%s (%s)", pool.Type, pool.Content),
							ClusterID:   cluster.ID.String(),
							ClusterName: cluster.Name,
						})
					}
				}
			}

			if len(results) > 100 {
				break
			}
		}
	}

	// Search clusters themselves.
	if clusters != nil {
		for _, cluster := range clusters {
			if strings.Contains(strings.ToLower(cluster.Name), queryLower) {
				results = append(results, searchResult{
					Type:        "cluster",
					ID:          cluster.ID.String(),
					Name:        cluster.Name,
					ClusterID:   cluster.ID.String(),
					ClusterName: cluster.Name,
				})
			}
		}
	}

	if results == nil {
		results = []searchResult{}
	}

	return c.JSON(results)
}
