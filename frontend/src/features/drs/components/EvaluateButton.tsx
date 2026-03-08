import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useTriggerEvaluation } from "../api/drs-queries";
import type { DRSRecommendation } from "../types/drs";
import { Play, Loader2 } from "lucide-react";

interface EvaluateButtonProps {
  clusterId: string;
}

export function EvaluateButton({ clusterId }: EvaluateButtonProps) {
  const evaluation = useTriggerEvaluation(clusterId);
  const [results, setResults] = useState<DRSRecommendation[] | null>(null);

  const [evaluated, setEvaluated] = useState(false);

  const handleEvaluate = () => {
    evaluation.mutate(undefined, {
      onSuccess: (data) => {
        setResults(data.recommendations);
        setEvaluated(true);
      },
      onError: () => {
        setResults([]);
        setEvaluated(true);
      },
    });
  };

  const errorMessage =
    evaluation.error instanceof Error ? evaluation.error.message : "";

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

      {evaluated && results !== null && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">
              Evaluation Results ({results.length} recommendation
              {results.length !== 1 ? "s" : ""})
            </CardTitle>
          </CardHeader>
          <CardContent>
            {results.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                Cluster is balanced. No migrations recommended.
              </p>
            ) : (
              <div className="space-y-2">
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
                    <div className="flex items-center gap-2">
                      <span className="text-muted-foreground">
                        {rec.reason}
                      </span>
                      <Badge variant="secondary">
                        +{(rec.improvement * 100).toFixed(1)}%
                      </Badge>
                    </div>
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
