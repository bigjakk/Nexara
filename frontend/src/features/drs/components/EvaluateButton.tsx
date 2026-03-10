import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useTriggerEvaluation } from "../api/drs-queries";
import type { DRSRecommendation, NodeScore } from "../types/drs";
import { Play, Loader2, CheckCircle2, AlertTriangle } from "lucide-react";

interface EvaluateButtonProps {
  clusterId: string;
}

function pct(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

/** Color class based on load percentage. */
function loadColor(v: number): string {
  if (v >= 0.8) return "text-red-500";
  if (v >= 0.6) return "text-yellow-500";
  return "text-green-600 dark:text-green-400";
}

/** Simple horizontal bar showing load visually. */
function LoadBar({ value, label }: { value: number; label: string }) {
  const widthPct = Math.min(value * 100, 100);
  let barColor = "bg-green-500/70";
  if (value >= 0.8) barColor = "bg-red-500/70";
  else if (value >= 0.6) barColor = "bg-yellow-500/70";
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-10 text-muted-foreground">{label}</span>
      <div className="h-2 flex-1 rounded-full bg-muted">
        <div
          className={`h-2 rounded-full ${barColor}`}
          style={{ width: `${String(widthPct)}%` }}
        />
      </div>
      <span className={`w-12 text-right font-mono ${loadColor(value)}`}>
        {pct(value)}
      </span>
    </div>
  );
}

export function EvaluateButton({ clusterId }: EvaluateButtonProps) {
  const evaluation = useTriggerEvaluation(clusterId);
  const [results, setResults] = useState<DRSRecommendation[] | null>(null);
  const [nodeScores, setNodeScores] = useState<NodeScore[] | null>(null);
  const [imbalance, setImbalance] = useState(0);
  const [threshold, setThreshold] = useState(0);
  const [evaluated, setEvaluated] = useState(false);

  // Reset state when switching clusters.
  useEffect(() => {
    setResults(null);
    setNodeScores(null);
    setImbalance(0);
    setThreshold(0);
    setEvaluated(false);
  }, [clusterId]);

  const handleEvaluate = () => {
    evaluation.mutate(undefined, {
      onSuccess: (data) => {
        setResults(data.recommendations);
        setNodeScores(data.node_scores);
        setImbalance(data.imbalance);
        setThreshold(data.threshold);
        setEvaluated(true);
      },
      onError: () => {
        setResults([]);
        setNodeScores(null);
        setEvaluated(true);
      },
    });
  };

  const errorMessage =
    evaluation.error instanceof Error ? evaluation.error.message : "";

  const hasResults = evaluated && results !== null;
  const isBalanced = hasResults && imbalance <= threshold;
  const isImbalancedNoMoves = hasResults && imbalance > threshold && results.length === 0;
  const isImbalancedWithMoves = hasResults && results.length > 0;

  // Sort node scores by score descending for display.
  const sortedScores = nodeScores
    ? [...nodeScores].sort((a, b) => b.score - a.score)
    : [];

  return (
    <div className="space-y-4">
      <Button
        onClick={handleEvaluate}
        disabled={evaluation.isPending || clusterId.length === 0}
        variant="outline"
      >
        {evaluation.isPending ? (
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        ) : (
          <Play className="mr-2 h-4 w-4" />
        )}
        Run Evaluation
      </Button>

      {errorMessage && (
        <p className="text-sm text-destructive">{errorMessage}</p>
      )}

      {hasResults && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-sm">
              {isBalanced && (
                <>
                  <CheckCircle2 className="h-4 w-4 text-green-500" />
                  Cluster Balanced
                </>
              )}
              {isImbalancedNoMoves && (
                <>
                  <AlertTriangle className="h-4 w-4 text-yellow-500" />
                  Imbalanced — No Beneficial Migrations
                </>
              )}
              {isImbalancedWithMoves && (
                <>
                  <AlertTriangle className="h-4 w-4 text-yellow-500" />
                  {results.length} Recommendation{results.length !== 1 ? "s" : ""}
                </>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* Node Score Breakdown */}
            {sortedScores.length > 0 && (
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">
                    Node Load Scores
                  </span>
                  <Badge
                    variant={imbalance > threshold ? "destructive" : "secondary"}
                    className="font-mono text-xs"
                  >
                    Variance: {pct(imbalance)} / Threshold: {pct(threshold)}
                  </Badge>
                </div>
                <div className="space-y-3">
                  {sortedScores.map((ns) => (
                    <div key={ns.node} className="space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="text-sm font-medium">{ns.node}</span>
                        <Badge variant="outline" className="font-mono text-xs">
                          {pct(ns.score)}
                        </Badge>
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
                Load variance ({pct(imbalance)}) is within the configured threshold ({pct(threshold)}).
                No migrations needed.
              </p>
            )}

            {/* Imbalanced but no moves help */}
            {isImbalancedNoMoves && (
              <p className="text-sm text-muted-foreground">
                Load variance ({pct(imbalance)}) exceeds the threshold ({pct(threshold)}),
                but no single VM migration would improve the balance. This typically happens
                when nodes have very few VMs or when workloads are too large relative to the
                load difference — moving any one VM would overshoot and make things worse.
              </p>
            )}

            {/* Recommendations */}
            {isImbalancedWithMoves && (
              <div className="space-y-2">
                <span className="text-xs font-medium text-muted-foreground">
                  Recommended Migrations
                </span>
                {results.map((rec, i) => (
                  <div
                    key={i}
                    className="flex items-center justify-between rounded-md border p-3 text-sm"
                  >
                    <div>
                      <span className="font-medium">
                        {rec.vm_type.toUpperCase()} {rec.vmid}
                      </span>
                      <span className="text-muted-foreground">
                        {" "}
                        {rec.from} &rarr; {rec.to}
                      </span>
                    </div>
                    <Badge variant="secondary">
                      +{(rec.improvement * 100).toFixed(1)}%
                    </Badge>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
