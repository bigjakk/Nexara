package drs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Weights holds the scoring weight configuration.
type Weights struct {
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
}

// DefaultWeights returns the default scoring weights.
// Memory is weighted higher because it is a more stable and constraining
// resource than CPU, which tends to be spiky and transient.
func DefaultWeights() Weights {
	return Weights{CPU: 0.3, Memory: 0.7}
}

// NodeScore holds the computed load score for a node.
type NodeScore struct {
	Node    string
	Score   float64
	CPULoad float64
	MemLoad float64
}

// Workload represents a VM or CT running on a node.
type Workload struct {
	VMID     int
	Name     string
	Type     string // "qemu" or "lxc"
	Node     string
	CPUUsage float64
	CPUs     int
	Mem      int64
	MaxMem   int64
	NetIn    int64
	NetOut   int64
	Status   string
	Pinned   bool // true = must not be migrated (PCI passthrough, HA pin, etc.)
}

// Recommendation is a single migration recommendation.
type Recommendation struct {
	VMID                int
	VMType              string
	SourceNode          string
	TargetNode          string
	Reason              string
	ScoreBefore         float64
	ScoreAfter          float64
	ExpectedImprovement float64
}

// EvalResult contains the full evaluation output including node scores.
type EvalResult struct {
	Recommendations []Recommendation
	NodeScores      map[string]NodeScore
	Imbalance       float64
	Threshold       float64
	Weights         Weights
}

// Engine is the DRS evaluation engine.
type Engine struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
}

// NewEngine creates a new DRS engine.
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Engine {
	return &Engine{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
	}
}

// Evaluate runs DRS evaluation for a single cluster and returns recommendations.
func (e *Engine) Evaluate(ctx context.Context, clusterID uuid.UUID) (*EvalResult, error) {
	cfg, err := e.queries.GetDRSConfig(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get DRS config: %w", err)
	}
	if !cfg.Enabled || cfg.Mode == "disabled" {
		return nil, nil
	}

	var weights Weights
	if err := json.Unmarshal(cfg.Weights, &weights); err != nil {
		weights = DefaultWeights()
	}

	client, err := e.createClient(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("create proxmox client: %w", err)
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}

	// Collect workloads per node.
	// When include_containers is false, containers still count toward node load
	// scoring (so DRS has accurate scores) but are marked as pinned so they
	// won't be selected for migration. Container migration requires downtime
	// (stop → move → start) unlike VM live migration.
	includeContainers := cfg.IncludeContainers
	nodeWorkloads := make(map[string][]Workload)
	nodeEntries := make(map[string]proxmox.NodeListEntry)
	for _, n := range nodes {
		if n.Status != "online" {
			continue
		}
		nodeEntries[n.Node] = n

		vms, err := client.GetVMs(ctx, n.Node)
		if err != nil {
			e.logger.Warn("failed to get VMs for node", "node", n.Node, "error", err)
			continue
		}
		for _, vm := range vms {
			if vm.Status != "running" || vm.Template == 1 {
				continue
			}
			nodeWorkloads[n.Node] = append(nodeWorkloads[n.Node], Workload{
				VMID:     vm.VMID,
				Name:     vm.Name,
				Type:     "qemu",
				Node:     n.Node,
				CPUUsage: vm.CPU,
				CPUs:     vm.CPUs,
				Mem:      vm.Mem,
				MaxMem:   vm.MaxMem,
				NetIn:    vm.NetIn,
				NetOut:   vm.NetOut,
				Status:   vm.Status,
			})
		}

		cts, err := client.GetContainers(ctx, n.Node)
		if err != nil {
			e.logger.Warn("failed to get containers for node", "node", n.Node, "error", err)
			continue
		}
		for _, ct := range cts {
			if ct.Status != "running" || ct.Template == 1 {
				continue
			}
			nodeWorkloads[n.Node] = append(nodeWorkloads[n.Node], Workload{
				VMID:     ct.VMID,
				Name:     ct.Name,
				Type:     "lxc",
				Node:     n.Node,
				CPUUsage: ct.CPU,
				CPUs:     ct.CPUs,
				Mem:      ct.Mem,
				MaxMem:   ct.MaxMem,
				NetIn:    ct.NetIn,
				NetOut:   ct.NetOut,
				Status:   ct.Status,
				Pinned:   !includeContainers,
			})
		}
	}

	// Auto-import HA pin rules.
	haRules := e.importHARules(ctx, client, nodeWorkloads)

	// Detect PCI/USB passthrough VMs and mark as pinned.
	e.detectPassthrough(ctx, client, nodeWorkloads)

	// Score nodes (pinned workloads still count toward node load).
	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		wl := nodeWorkloads[name]
		scores[name] = ScoreNode(n, wl, weights)
	}

	imbalance := CalculateImbalance(scores)

	// Log per-node scores and per-dimension imbalance for debugging.
	for name, s := range scores {
		totalWL := len(nodeWorkloads[name])
		e.logger.Info("DRS node score",
			"cluster_id", clusterID,
			"node", name,
			"score", fmt.Sprintf("%.4f", s.Score),
			"cpu_load", fmt.Sprintf("%.4f", s.CPULoad),
			"mem_load", fmt.Sprintf("%.4f", s.MemLoad),
			"workloads", totalWL,
		)
	}

	result := &EvalResult{
		NodeScores: scores,
		Imbalance:  imbalance,
		Threshold:  cfg.ImbalanceThreshold,
		Weights:    weights,
	}

	if imbalance <= cfg.ImbalanceThreshold {
		e.logger.Info("cluster balanced", "cluster_id", clusterID,
			"imbalance", fmt.Sprintf("%.4f", imbalance),
			"threshold", cfg.ImbalanceThreshold)
		return result, nil
	}

	e.logger.Info("cluster imbalanced, planning migrations",
		"cluster_id", clusterID,
		"imbalance", fmt.Sprintf("%.4f", imbalance),
		"threshold", cfg.ImbalanceThreshold)

	// Load rules.
	dbRules, err := e.queries.ListDRSRules(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list DRS rules: %w", err)
	}
	rules := ParseDBRules(dbRules)
	rules = append(rules, haRules...)

	// Build filtered workloads for the planner (exclude pinned VMs).
	plannerWorkloads := make(map[string][]Workload, len(nodeWorkloads))
	pinnedCount := 0
	for node, wls := range nodeWorkloads {
		for _, w := range wls {
			if !w.Pinned {
				plannerWorkloads[node] = append(plannerWorkloads[node], w)
			} else {
				pinnedCount++
			}
		}
	}

	e.logger.Info("DRS planner input",
		"cluster_id", clusterID,
		"pinned_count", pinnedCount,
		"rule_count", len(rules),
	)

	// Plan migrations using filtered workloads but original scores.
	result.Recommendations = Plan(scores, plannerWorkloads, nodeEntries, rules, weights, cfg.ImbalanceThreshold, e.logger)

	return result, nil
}

// ScoreNode computes a weighted load score for a node (0.0 = idle, 1.0 = fully loaded).
// CPU and memory are derived from the workloads placed on the node (not node-level metrics)
// so that the planner's move simulation produces accurate score changes.
func ScoreNode(node proxmox.NodeListEntry, workloads []Workload, weights Weights) NodeScore {
	var cpuLoad float64
	if node.MaxCPU > 0 {
		var totalCPU float64
		for _, w := range workloads {
			totalCPU += w.CPUUsage * float64(w.CPUs)
		}
		cpuLoad = totalCPU / float64(node.MaxCPU)
		if cpuLoad > 1.0 {
			cpuLoad = 1.0
		}
	}

	var memLoad float64
	if node.MaxMem > 0 {
		var totalMem int64
		for _, w := range workloads {
			totalMem += w.Mem
		}
		memLoad = float64(totalMem) / float64(node.MaxMem)
		if memLoad > 1.0 {
			memLoad = 1.0
		}
	}

	score := weights.CPU*cpuLoad + weights.Memory*memLoad

	return NodeScore{
		Node:    node.Node,
		Score:   score,
		CPULoad: cpuLoad,
		MemLoad: memLoad,
	}
}

// CalculateImbalance computes the coefficient of variation (stddev/mean) of node scores.
func CalculateImbalance(scores map[string]NodeScore) float64 {
	if len(scores) < 2 {
		return 0
	}

	var sum float64
	for _, s := range scores {
		sum += s.Score
	}
	mean := sum / float64(len(scores))
	if mean == 0 {
		return 0
	}

	var varianceSum float64
	for _, s := range scores {
		diff := s.Score - mean
		varianceSum += diff * diff
	}
	stddev := math.Sqrt(varianceSum / float64(len(scores)))

	return stddev / mean
}

func (e *Engine) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := e.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, e.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return client, nil
}

// importHARules fetches HA rules from Proxmox. It first tries the PVE 9+ rules API
// (GET /cluster/ha/rules) which supports node-affinity and resource-affinity rules.
// If that fails (PVE 8 or earlier), it falls back to the legacy HA resources + groups approach.
func (e *Engine) importHARules(ctx context.Context, client *proxmox.Client, _ map[string][]Workload) []Rule {
	// Try PVE 9+ HA rules API first.
	if rules := e.importHARulesPVE9(ctx, client); rules != nil {
		return rules
	}

	// Fallback to legacy HA resources + groups (PVE 8).
	return e.importHARulesLegacy(ctx, client)
}

// importHARulesPVE9 tries the PVE 9+ GET /cluster/ha/rules endpoint.
// Returns nil if the endpoint is not available (PVE 8 or error).
func (e *Engine) importHARulesPVE9(ctx context.Context, client *proxmox.Client) []Rule {
	haRules, err := client.GetHARules(ctx)
	if err != nil {
		e.logger.Debug("PVE 9 HA rules API not available, falling back to legacy", "error", err)
		return nil
	}

	var rules []Rule
	for _, entry := range haRules {
		if entry.Disable != 0 {
			continue
		}

		// Parse resource SIDs into VMIDs.
		var vmIDs []int
		for _, res := range strings.Split(entry.Resources, ",") {
			res = strings.TrimSpace(res)
			if vmid, ok := parseSIDToVMID(res); ok {
				vmIDs = append(vmIDs, vmid)
			}
		}
		if len(vmIDs) == 0 {
			continue
		}

		switch entry.Type {
		case "node-affinity":
			nodes := parseHAGroupNodes(entry.Nodes)
			e.logger.Info("HA node-affinity rule (pin)", "rule", entry.Rule, "vmids", vmIDs, "nodes", nodes)
			rules = append(rules, Rule{
				Type:      RuleTypePin,
				VMIDs:     vmIDs,
				NodeNames: nodes,
				Enabled:   true,
			})

		case "resource-affinity":
			if entry.Affinity == "negative" {
				e.logger.Info("HA resource-affinity rule (anti-affinity)", "rule", entry.Rule, "vmids", vmIDs)
				rules = append(rules, Rule{
					Type:    RuleTypeAntiAffinity,
					VMIDs:   vmIDs,
					Enabled: true,
				})
			} else {
				e.logger.Info("HA resource-affinity rule (affinity)", "rule", entry.Rule, "vmids", vmIDs)
				rules = append(rules, Rule{
					Type:    RuleTypeAffinity,
					VMIDs:   vmIDs,
					Enabled: true,
				})
			}
		}
	}

	return rules
}

// importHARulesLegacy uses the PVE 8 HA resources + groups API to derive pin rules
// for VMs in restricted HA groups.
func (e *Engine) importHARulesLegacy(ctx context.Context, client *proxmox.Client) []Rule {
	haResources, err := client.GetHAResources(ctx)
	if err != nil {
		e.logger.Warn("failed to fetch HA resources, skipping HA rule import", "error", err)
		return nil
	}
	haGroups, err := client.GetHAGroups(ctx)
	if err != nil {
		e.logger.Warn("failed to fetch HA groups, skipping HA rule import", "error", err)
		return nil
	}

	// Build group→nodes map for restricted groups only.
	restrictedGroups := make(map[string][]string)
	for _, g := range haGroups {
		if g.Restricted == 1 {
			restrictedGroups[g.Group] = parseHAGroupNodes(g.Nodes)
		}
	}

	if len(restrictedGroups) == 0 {
		return nil
	}

	rules := make([]Rule, 0, len(haResources))
	for _, res := range haResources {
		if res.Group == "" {
			continue
		}
		nodes, ok := restrictedGroups[res.Group]
		if !ok {
			continue
		}

		vmid, ok := parseSIDToVMID(res.SID)
		if !ok {
			continue
		}

		e.logger.Info("HA pin rule: VM restricted to nodes",
			"vmid", vmid, "nodes", nodes, "group", res.Group)

		rules = append(rules, Rule{
			Type:      RuleTypePin,
			VMIDs:     []int{vmid},
			NodeNames: nodes,
			Enabled:   true,
		})
	}

	return rules
}

// detectPassthrough checks QEMU VMs for PCI passthrough devices and marks them as pinned.
func (e *Engine) detectPassthrough(ctx context.Context, client *proxmox.Client, nodeWorkloads map[string][]Workload) {
	for node, workloads := range nodeWorkloads {
		for i, w := range workloads {
			if w.Type != "qemu" {
				continue
			}
			config, err := client.GetVMConfig(ctx, node, w.VMID)
			if err != nil {
				e.logger.Warn("failed to get VM config for passthrough check",
					"vmid", w.VMID, "node", node, "error", err)
				continue
			}
			if hasPassthrough(config) {
				e.logger.Info("skipping VM with hardware passthrough",
					"vmid", w.VMID, "node", node, "name", w.Name)
				workloads[i].Pinned = true
			}
		}
	}
}

// parseHAGroupNodes parses an HA group nodes string like "node1:100,node2:50" or "node1,node2"
// into a slice of node names (stripping optional priority suffixes).
func parseHAGroupNodes(nodesStr string) []string {
	if nodesStr == "" {
		return nil
	}
	parts := strings.Split(nodesStr, ",")
	nodes := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Strip optional priority suffix (e.g. "node1:100" → "node1").
		if idx := strings.Index(p, ":"); idx >= 0 {
			p = p[:idx]
		}
		nodes = append(nodes, p)
	}
	return nodes
}

// hasPassthrough checks if a VM config contains any PCI passthrough (hostpci0..hostpci15)
// or USB passthrough (usb0..usb4) devices.
func hasPassthrough(config proxmox.VMConfig) bool {
	for i := 0; i <= 15; i++ {
		key := fmt.Sprintf("hostpci%d", i)
		if _, ok := config[key]; ok {
			return true
		}
	}
	for i := 0; i <= 4; i++ {
		key := fmt.Sprintf("usb%d", i)
		if val, ok := config[key]; ok {
			// USB devices configured as "spice" are virtual (SPICE redirection),
			// not physical passthrough — these are safe to migrate.
			if s, isStr := val.(string); isStr && s == "spice" {
				continue
			}
			return true
		}
	}
	return false
}

// parseSIDToVMID extracts the VMID from an HA resource SID like "vm:101" or "ct:200".
func parseSIDToVMID(sid string) (int, bool) {
	parts := strings.SplitN(sid, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	vmid, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return vmid, true
}
