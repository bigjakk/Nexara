import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { UsageBar } from "@/components/UsageBar";
import { cn } from "@/lib/utils";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { formatUptime } from "@/lib/format";
import { useTriggerEvaluation } from "../api/drs-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { DRSRecommendation, HeldNode, NodeScore } from "../types/drs";
import { Play, Loader2, AlertTriangle } from "lucide-react";

interface EvaluateButtonProps {
  clusterId: string;
}

function pct(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

/** A verdict that asks for a look: the balanced one stays plain. */
const VERDICT_WARN = "font-medium text-amber-700 dark:text-amber-400";

/** One of a node's loads: the shared capacity bar and its reading. */
function LoadBar({ value, label }: { value: number; label: string }) {
  const percent = Math.min(value * 100, 100);
  const level = usageLevel(percent);
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-10 text-muted-foreground">{label}</span>
      <UsageBar percent={percent} className="flex-1" />
      <span
        className={cn(
          "w-12 text-right font-mono",
          level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
        )}
      >
        {pct(value)}
      </span>
    </div>
  );
}

export function EvaluateButton({ clusterId }: EvaluateButtonProps) {
  const { t } = useTranslation("clusters");
  const evaluation = useTriggerEvaluation(clusterId);
  // The panel shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was left is toasted instead.
  const settle = useSaveOutcome(clusterId);
  const [results, setResults] = useState<DRSRecommendation[] | null>(null);
  const [nodeScores, setNodeScores] = useState<NodeScore[] | null>(null);
  const [heldNodes, setHeldNodes] = useState<HeldNode[]>([]);
  const [imbalance, setImbalance] = useState(0);
  const [threshold, setThreshold] = useState(0);
  const [evaluated, setEvaluated] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const [blockReason, setBlockReason] = useState("");
  const [queued, setQueued] = useState(false);

  /**
   * Clear every field a verdict is derived from. Both the cluster-switch
   * reset and the failure path go through here on purpose: the false
   * all-clear this replaced came from those two clears drifting apart — one
   * set `results` to `null`, the other to `[]`, and `[]` reads as a
   * successful evaluation that found zero moves. `results === null` is the
   * sentinel the whole verdict card gates on, via `hasResults`.
   */
  const clearEvaluation = useCallback((didEvaluate: boolean) => {
    setResults(null);
    setNodeScores(null);
    setHeldNodes([]);
    setImbalance(0);
    setThreshold(0);
    setBlocked(false);
    setBlockReason("");
    setQueued(false);
    // True after a failed run too — a request finished, it just produced
    // nothing to show. Never branch on this alone.
    setEvaluated(didEvaluate);
  }, []);

  // Reset state when switching clusters.
  useEffect(() => {
    clearEvaluation(false);
  }, [clusterId, clearEvaluation]);

  const handleEvaluate = () => {
    settle(evaluation.mutateAsync(), {
      action: t("drsEvaluateAction"),
      onSuccess: (data) => {
        setResults(data.recommendations);
        setNodeScores(data.node_scores);
        setHeldNodes(data.held_nodes ?? []);
        setImbalance(data.imbalance);
        setThreshold(data.threshold);
        setBlocked(data.blocked ?? false);
        setBlockReason(data.block_reason ?? "");
        setQueued(data.queued ?? false);
        setEvaluated(true);
      },
      onError: () => {
        clearEvaluation(true);
      },
    });
  };

  const errorMessage =
    evaluation.error instanceof Error ? evaluation.error.message : "";

  const isBlocked = evaluated && blocked;
  const hasResults = evaluated && results !== null && !blocked;
  const isBalanced = hasResults && imbalance <= threshold;
  const isImbalancedNoMoves =
    hasResults && imbalance > threshold && results.length === 0;
  const isImbalancedWithMoves = hasResults && results.length > 0;

  // Sort node scores by score descending for display.
  const sortedScores = nodeScores
    ? [...nodeScores].sort((a, b) => b.score - a.score)
    : [];

  return (
    <Panel
      title="Evaluation"
      aside={
        isBalanced ? (
          "Cluster balanced"
        ) : isImbalancedNoMoves ? (
          <span className={VERDICT_WARN}>
            Imbalanced, no beneficial migrations
          </span>
        ) : isImbalancedWithMoves ? (
          <span className={VERDICT_WARN}>
            {results.length}{" "}
            {results.length === 1 ? "recommendation" : "recommendations"}
          </span>
        ) : undefined
      }
      actions={
        <Button
          onClick={handleEvaluate}
          disabled={evaluation.isPending || clusterId.length === 0}
          variant="outline"
          size="sm"
        >
          {evaluation.isPending ? (
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          ) : (
            <Play className="mr-2 h-4 w-4" />
          )}
          Run Evaluation
        </Button>
      }
    >
      <div className="space-y-4">
        {!evaluated && !evaluation.isError && (
          <p className="text-sm text-muted-foreground">
            Score the nodes now and see what DRS would move. Nothing is migrated
            unless the mode is Automatic.
          </p>
        )}

        {/*
          Gate on isError, not on the message: a proxy 502 with an HTML body
          falls back to res.statusText, which is "" over HTTP/2, and a failure
          now clears the results — so keying off the message alone would render
          the whole panel blank.
        */}
        {evaluation.isError && (
          <p className="text-sm text-destructive">
            {errorMessage || "Evaluation failed."}
          </p>
        )}

        {isBlocked && (
          <div className="flex items-start gap-2 text-sm">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div>
              <p className="font-medium">
                Evaluation skipped: Proxmox native CRS active
              </p>
              <p className="mt-1 text-muted-foreground">
                {blockReason ||
                  "Proxmox native CRS auto-rebalance is enabled on this cluster, so Nexara automatic migrations are paused. Switch DRS to Advisory mode to generate recommendations without migrating."}
              </p>
            </div>
          </div>
        )}

        {hasResults && (
          <>
            {/* Node Score Breakdown */}
            {sortedScores.length > 0 && (
              <div className="space-y-3">
                <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <span className="text-[12.5px] font-medium text-muted-foreground">
                    Node load scores
                  </span>
                  <span
                    className={cn(
                      "font-mono text-xs",
                      imbalance > threshold
                        ? "font-semibold text-amber-700 dark:text-amber-400"
                        : "text-muted-foreground",
                    )}
                  >
                    Variance: {pct(imbalance)} / Threshold: {pct(threshold)}
                  </span>
                </div>
                <div className="divide-y">
                  {sortedScores.map((ns) => (
                    <div
                      key={ns.node}
                      className="space-y-1 py-2.5 first:pt-0 last:pb-0"
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-sm font-medium">{ns.node}</span>
                        <span className="font-mono text-xs text-muted-foreground">
                          {pct(ns.score)}
                        </span>
                      </div>
                      <LoadBar value={ns.cpu_load} label="CPU" />
                      <LoadBar value={ns.mem_load} label="Mem" />
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Balanced message */}
            {isBalanced && (
              <p className="text-sm text-muted-foreground">
                Load variance ({pct(imbalance)}) is within the configured
                threshold ({pct(threshold)}). No migrations needed.
              </p>
            )}

            {/* Nodes in their boot hold-off: no move onto or off them */}
            {heldNodes.map((h) => (
              <p key={h.node} className="text-sm text-muted-foreground">
                {t("drsHeldNode", {
                  node: h.node,
                  uptime:
                    h.uptime_seconds < 60
                      ? "<1m"
                      : formatUptime(h.uptime_seconds),
                  holdoff: Math.round(h.holdoff_seconds / 60),
                })}
              </p>
            ))}

            {/* Imbalanced but no moves help */}
            {isImbalancedNoMoves && heldNodes.length === 0 && (
              <p className="text-sm text-muted-foreground">
                Load variance ({pct(imbalance)}) exceeds the threshold (
                {pct(threshold)}), but no single VM migration would improve the
                balance. This typically happens when nodes have very few VMs or
                when workloads are too large relative to the load difference:
                moving any one VM would overshoot and make things worse.
              </p>
            )}

            {/* Recommendations */}
            {isImbalancedWithMoves && (
              <div className="space-y-2">
                <span className="text-[12.5px] font-medium text-muted-foreground">
                  Recommended migrations
                </span>
                {queued && (
                  <p className="text-xs text-muted-foreground">
                    Automatic mode: an evaluation has been queued. The scheduler
                    re-plans against live cluster state on its next pass
                    (usually within a minute) and executes what it finds then,
                    so the moves below are the plan as of now and may differ.
                    Results appear in DRS history.
                  </p>
                )}
                <ul className="divide-y text-sm">
                  {results.map((rec, i) => (
                    <li
                      key={i}
                      className="flex items-center justify-between gap-3 py-2 first:pt-0 last:pb-0"
                    >
                      <div className="min-w-0">
                        <span className="font-medium">
                          {rec.vm_type.toUpperCase()} {rec.vmid}
                        </span>
                        <span className="text-muted-foreground">
                          {" "}
                          {rec.from} &rarr; {rec.to}
                        </span>
                      </div>
                      <span className="shrink-0 font-mono text-xs text-muted-foreground">
                        +{(rec.improvement * 100).toFixed(1)}%
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </div>
    </Panel>
  );
}
