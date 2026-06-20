package drs

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/bigjakk/nexara/internal/proxmox"
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
	logger *slog.Logger,
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
				logger.Debug("DRS planner: no workloads on source", "source", source)
				continue
			}

			logger.Info("DRS planner: trying pair",
				"source", source,
				"target", target,
				"source_score", fmt.Sprintf("%.4f", currentScores[source].Score),
				"target_score", fmt.Sprintf("%.4f", currentScores[target].Score),
				"source_workloads", len(workloads),
			)

			// Sort by estimated impact (largest contributors first).
			sort.Slice(workloads, func(a, b int) bool {
				return estimateWorkloadImpact(workloads[a], weights) > estimateWorkloadImpact(workloads[b], weights)
			})

			for j, w := range workloads {
				// Pinned workloads (PCI/USB passthrough, containers when
				// include_containers is off, etc.) must never be migrated, but
				// they remain in currentWorkloads so they still count toward node
				// scores and stay visible to affinity/anti-affinity/pin checks —
				// e.g. a movable VM must not be placed onto a node hosting its
				// pinned anti-affinity partner.
				if w.Pinned {
					continue
				}

				migration := Recommendation{
					VMID:       w.VMID,
					VMType:     w.Type,
					SourceNode: source,
					TargetNode: target,
				}

				if allowed, reason := checkMigration(migration, currentWorkloads, rules); !allowed {
					logger.Info("DRS planner: migration blocked by rule",
						"vmid", w.VMID, "source", source, "target", target, "reason", reason)
					continue
				}

				scoreBefore := CalculateImbalance(currentScores)
				// Capture the pre-move node scores for the recommendation's
				// human-readable reason. Read currentScores (which reflects every
				// prior simulated move this run) rather than the immutable `scores`
				// map, which is stale from the 2nd iteration onward — and read it
				// *before* applying this move so the reason shows the loads that
				// motivated it, not the post-move ones.
				sourceScore := currentScores[source].Score
				targetScore := currentScores[target].Score

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
					logger.Info("DRS planner: move worsens balance, reverting",
						"vmid", w.VMID,
						"name", w.Name,
						"source", source,
						"target", target,
						"before", fmt.Sprintf("%.4f", scoreBefore),
						"after", fmt.Sprintf("%.4f", scoreAfter),
					)
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
					source, sourceScore, w.Type, w.VMID, target, targetScore,
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

	return collapseHops(recommendations)
}

// collapseHops merges the multiple migration hops the greedy loop can emit for a
// single guest in one run (e.g. a→b in one iteration, then b→c in a later one)
// into a single source→target recommendation. Recommendations are computed
// against each guest's real current location, so the intermediate node is a
// simulation artifact only — executing the net move reaches the same final
// placement with one live migration instead of two. A guest that nets back to
// its origin node is dropped entirely. First-appearance order is preserved.
func collapseHops(recs []Recommendation) []Recommendation {
	if len(recs) <= 1 {
		return recs
	}

	order := make([]int, 0, len(recs))
	merged := make(map[int]Recommendation, len(recs))
	for _, r := range recs {
		existing, ok := merged[r.VMID]
		if !ok {
			order = append(order, r.VMID)
			merged[r.VMID] = r
			continue
		}
		// Extend the net move: keep the original source and the first hop's
		// score-before, adopt the latest hop's target and score-after.
		existing.TargetNode = r.TargetNode
		existing.ScoreAfter = r.ScoreAfter
		existing.ExpectedImprovement = existing.ScoreBefore - r.ScoreAfter
		existing.Reason = fmt.Sprintf(
			"rebalance: consolidate %s %d to a single migration %s -> %s",
			existing.VMType, existing.VMID, existing.SourceNode, r.TargetNode,
		)
		merged[r.VMID] = existing
	}

	out := make([]Recommendation, 0, len(order))
	for _, vmid := range order {
		r := merged[vmid]
		if r.SourceNode == r.TargetNode {
			continue // net no-op: guest returned to its origin node
		}
		out = append(out, r)
	}
	return out
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
