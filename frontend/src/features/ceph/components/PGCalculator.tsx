import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

function nearestPowerOf2(n: number): number {
  if (n <= 0) return 1;
  return Math.pow(2, Math.round(Math.log2(n)));
}

interface PGCalculatorProps {
  osdCount: number;
  onPGNumChange?: (pgNum: number) => void;
}

/** Suggests a pg_num for a pool from the OSD count, under a hairline. */
export function PGCalculator({ osdCount, onPGNumChange }: PGCalculatorProps) {
  const [poolSize, setPoolSize] = useState(3);
  const [targetPGsPerOSD, setTargetPGsPerOSD] = useState(100);

  const raw = (osdCount * targetPGsPerOSD) / poolSize;
  const recommended = nearestPowerOf2(raw);

  return (
    <div className="space-y-3 border-t pt-4">
      <h3 className="text-sm font-semibold">Placement group calculator</h3>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <Label htmlFor="pg-calc-osds" className="text-xs">
            OSD count
          </Label>
          <Input
            id="pg-calc-osds"
            type="number"
            value={osdCount}
            disabled
            className="mt-1"
          />
        </div>
        <div>
          <Label htmlFor="pg-calc-size" className="text-xs">
            Replicas
          </Label>
          <Input
            id="pg-calc-size"
            type="number"
            value={poolSize}
            min={1}
            max={10}
            onChange={(e) => {
              setPoolSize(Number(e.target.value) || 1);
            }}
            className="mt-1"
          />
        </div>
        <div>
          <Label htmlFor="pg-calc-target" className="text-xs">
            Target PGs per OSD
          </Label>
          <Input
            id="pg-calc-target"
            type="number"
            value={targetPGsPerOSD}
            min={1}
            onChange={(e) => {
              setTargetPGsPerOSD(Number(e.target.value) || 100);
            }}
            className="mt-1"
          />
        </div>
        <div>
          <div className="text-xs font-medium">Recommended pg_num</div>
          <div className="mt-1 flex h-9 items-center justify-between gap-2">
            <span className="text-lg font-semibold tabular-nums">
              {recommended}
            </span>
            {onPGNumChange && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  onPGNumChange(recommended);
                }}
              >
                Use this value
              </Button>
            )}
          </div>
        </div>
      </div>
      <p className="text-[12.5px] text-muted-foreground">
        The nearest power of two to OSDs x PGs per OSD / replicas: {osdCount} x{" "}
        {targetPGsPerOSD} / {poolSize} gives {recommended}.
      </p>
    </div>
  );
}
