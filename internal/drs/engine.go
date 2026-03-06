package drs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// Weights holds the scoring weight configuration.
type Weights struct {
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
}

// DefaultWeights returns the default scoring weights.
func DefaultWeights() Weights {
	return Weights{CPU: 0.5, Memory: 0.5}
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
func (e *Engine) Evaluate(ctx context.Context, clusterID uuid.UUID) ([]Recommendation, error) {
	cfg, err := e.queries.GetDRSConfig(ctx, clusterID)
	if err != nil {
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
			})
		}
	}

	// Score nodes.
	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		wl := nodeWorkloads[name]
		scores[name] = ScoreNode(n, wl, weights)
	}

	imbalance := CalculateImbalance(scores)
	if imbalance <= cfg.ImbalanceThreshold {
		e.logger.Info("cluster balanced", "cluster_id", clusterID, "imbalance", imbalance, "threshold", cfg.ImbalanceThreshold)
		return nil, nil
	}

	e.logger.Info("cluster imbalanced, planning migrations",
		"cluster_id", clusterID, "imbalance", imbalance, "threshold", cfg.ImbalanceThreshold)

	// Load rules.
	dbRules, err := e.queries.ListDRSRules(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list DRS rules: %w", err)
	}
	rules := parseDBRules(dbRules)

	// Plan migrations.
	recommendations := Plan(scores, nodeWorkloads, nodeEntries, rules, weights, cfg.ImbalanceThreshold)

	return recommendations, nil
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
