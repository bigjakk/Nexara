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

		// Build a ranked list of source-target pairs to try.
		// Primary: highest-scored → lowest-scored.
		// Fallback: try all above-average → below-average combinations.
		pairs := findSourceTargetPairs(currentScores)
		if len(pairs) == 0 {
			break
		}

		moved := false
		for _, pair := range pairs {
			source, target := pair[0], pair[1]

			workloads := currentWorkloads[source]
			if len(workloads) == 0 {
				continue
			}

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

				if !IsMigrationAllowed(migration, currentWorkloads, rules) {
					continue
				}

				scoreBefore := CalculateImbalance(currentScores)

				// Simulate the move.
				currentWorkloads[source] = append(workloads[:j], workloads[j+1:]...)
				w.Node = target
				currentWorkloads[target] = append(currentWorkloads[target], w)

				if entry, ok := nodeEntries[source]; ok {
					currentScores[source] = ScoreNode(entry, currentWorkloads[source], weights)
				}
				if entry, ok := nodeEntries[target]; ok {
					currentScores[target] = ScoreNode(entry, currentWorkloads[target], weights)
				}

				scoreAfter := CalculateImbalance(currentScores)

				if scoreAfter >= scoreBefore {
					// Undo the move.
					currentWorkloads[target] = currentWorkloads[target][:len(currentWorkloads[target])-1]
					w.Node = source
					currentWorkloads[source] = append(currentWorkloads[source][:j], append([]Workload{w}, currentWorkloads[source][j:]...)...)
					currentScores[source] = ScoreNode(nodeEntries[source], currentWorkloads[source], weights)
					currentScores[target] = ScoreNode(nodeEntries[target], currentWorkloads[target], weights)
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

			if moved {
				break
			}
		}

		if !moved {
			break // no valid migration found in any pair
		}
	}

	return recommendations
}

// findSourceTargetPairs returns candidate source→target pairs ordered by priority.
// Pairs are all combinations where source score > target score, ordered by
// score difference (largest gap first). The scoreAfter >= scoreBefore check
// in the main loop prevents bad moves, so we cast a wide net here.
func findSourceTargetPairs(scores map[string]NodeScore) [][2]string {
	if len(scores) < 2 {
		return nil
	}

	type nodeEntry struct {
		name  string
		score float64
	}
	sorted := make([]nodeEntry, 0, len(scores))
	for name, s := range scores {
		sorted = append(sorted, nodeEntry{name, s.Score})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	// Generate all pairs where source has a higher score than target.
	type scoredPair struct {
		pair [2]string
		diff float64
	}
	var candidates []scoredPair
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].score <= sorted[j].score {
				continue
			}
			candidates = append(candidates, scoredPair{
				pair: [2]string{sorted[i].name, sorted[j].name},
				diff: sorted[i].score - sorted[j].score,
			})
		}
	}

	// Sort by score difference descending (biggest gap first).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].diff > candidates[j].diff
	})

	pairs := make([][2]string, len(candidates))
	for i, c := range candidates {
		pairs[i] = c.pair
	}
	return pairs
}

func estimateWorkloadImpact(w Workload, weights Weights) float64 {
	var impact float64
	if w.CPUs > 0 {
		impact += weights.CPU * w.CPUUsage
	}
	if w.MaxMem > 0 {
		impact += weights.Memory * (float64(w.Mem) / float64(w.MaxMem))
	}
	return impact
}
