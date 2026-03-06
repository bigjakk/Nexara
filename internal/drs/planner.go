package drs

import (
	"fmt"
	"sort"

	"github.com/proxdash/proxdash/internal/proxmox"
)

// Plan generates migration recommendations using a greedy approach:
// move VMs from the most-loaded node to the least-loaded until imbalance < threshold.
func Plan(
	scores map[string]NodeScore,
	nodeWorkloads map[string][]Workload,
	nodeEntries map[string]proxmox.NodeListEntry,
	rules []Rule,
	weights Weights,
	threshold float64,
	totalClusterNet int64,
) []Recommendation {
	if len(scores) < 2 {
		return nil
	}

	// Work with mutable copies.
	currentScores := make(map[string]NodeScore, len(scores))
	for k, v := range scores {
		currentScores[k] = v
	}
	currentWorkloads := make(map[string][]Workload, len(nodeWorkloads))
	for k, v := range nodeWorkloads {
		wl := make([]Workload, len(v))
		copy(wl, v)
		currentWorkloads[k] = wl
	}

	var recommendations []Recommendation
	maxIterations := 20 // safety limit

	for i := 0; i < maxIterations; i++ {
		imbalance := CalculateImbalance(currentScores)
		if imbalance <= threshold {
			break
		}

		// Find highest and lowest scored nodes.
		source, target := findSourceTarget(currentScores)
		if source == "" || target == "" {
			break
		}

		// Pick the best VM to move from source to target.
		moved := false
		workloads := currentWorkloads[source]

		// Sort by estimated impact (largest contributors first).
		sort.Slice(workloads, func(a, b int) bool {
			return estimateWorkloadImpact(workloads[a], weights) > estimateWorkloadImpact(workloads[b], weights)
		})

		for j, w := range workloads {
			migration := Recommendation{
				VMID:       w.VMID,
				VMType:     w.Type,
				SourceNode: source,
				TargetNode: target,
			}

			// Check rules.
			if !IsMigrationAllowed(migration, currentWorkloads, rules) {
				continue
			}

			scoreBefore := CalculateImbalance(currentScores)

			// Simulate the move.
			currentWorkloads[source] = append(workloads[:j], workloads[j+1:]...)
			w.Node = target
			currentWorkloads[target] = append(currentWorkloads[target], w)

			// Rescore affected nodes.
			if entry, ok := nodeEntries[source]; ok {
				currentScores[source] = ScoreNode(entry, currentWorkloads[source], totalClusterNet, weights)
			}
			if entry, ok := nodeEntries[target]; ok {
				currentScores[target] = ScoreNode(entry, currentWorkloads[target], totalClusterNet, weights)
			}

			scoreAfter := CalculateImbalance(currentScores)

			// Only accept if it actually improves balance.
			if scoreAfter >= scoreBefore {
				// Undo the move.
				currentWorkloads[target] = currentWorkloads[target][:len(currentWorkloads[target])-1]
				w.Node = source
				currentWorkloads[source] = append(currentWorkloads[source][:j], append([]Workload{w}, currentWorkloads[source][j:]...)...)
				currentScores[source] = ScoreNode(nodeEntries[source], currentWorkloads[source], totalClusterNet, weights)
				currentScores[target] = ScoreNode(nodeEntries[target], currentWorkloads[target], totalClusterNet, weights)
				continue
			}

			migration.ScoreBefore = scoreBefore
			migration.ScoreAfter = scoreAfter
			migration.ExpectedImprovement = scoreBefore - scoreAfter
			migration.Reason = fmt.Sprintf(
				"rebalance: node %s score %.3f -> move %s %d to %s (score %.3f)",
				source, scores[source].Score, w.Type, w.VMID, target, scores[target].Score,
			)

			recommendations = append(recommendations, migration)
			moved = true
			break
		}

		if !moved {
			break // no valid migration found
		}
	}

	return recommendations
}

func findSourceTarget(scores map[string]NodeScore) (string, string) {
	var source, target string
	var maxScore, minScore float64
	first := true

	for name, s := range scores {
		if first {
			source = name
			target = name
			maxScore = s.Score
			minScore = s.Score
			first = false
			continue
		}
		if s.Score > maxScore {
			maxScore = s.Score
			source = name
		}
		if s.Score < minScore {
			minScore = s.Score
			target = name
		}
	}

	if source == target {
		return "", ""
	}
	return source, target
}

func estimateWorkloadImpact(w Workload, weights Weights) float64 {
	var impact float64
	if w.CPUs > 0 {
		impact += weights.CPU * w.CPUUsage
	}
	if w.MaxMem > 0 {
		impact += weights.Memory * (float64(w.Mem) / float64(w.MaxMem))
	}
	impact += weights.Network * float64(w.NetIn+w.NetOut)
	return impact
}
