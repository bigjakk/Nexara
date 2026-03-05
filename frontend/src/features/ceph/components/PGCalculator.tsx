import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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

export function PGCalculator({ osdCount, onPGNumChange }: PGCalculatorProps) {
  const [poolSize, setPoolSize] = useState(3);
  const [targetPGsPerOSD, setTargetPGsPerOSD] = useState(100);

  const raw = (osdCount * targetPGsPerOSD) / poolSize;
  const recommended = nearestPowerOf2(raw);

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-sm">PG Calculator</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <div>
            <Label className="text-xs">OSD Count</Label>
            <Input
              type="number"
              value={osdCount}
              disabled
              className="mt-1"
            />
          </div>
          <div>
            <Label className="text-xs">Pool Size (replicas)</Label>
            <Input
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
            <Label className="text-xs">Target PGs/OSD</Label>
            <Input
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
            <Label className="text-xs">Recommended pg_num</Label>
            <div className="mt-1 flex h-9 items-center rounded-md border bg-muted px-3 text-sm font-bold">
              {recommended}
            </div>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Formula: nearest_power_of_2(OSD_count x PGs_per_OSD / pool_size) ={" "}
          nearest_power_of_2({osdCount} x {targetPGsPerOSD} / {poolSize}) ={" "}
          {recommended}
        </p>
        {onPGNumChange && (
          <button
            className="text-xs text-primary underline"
            onClick={() => {
              onPGNumChange(recommended);
            }}
          >
            Use this value
          </button>
        )}
      </CardContent>
    </Card>
  );
}
