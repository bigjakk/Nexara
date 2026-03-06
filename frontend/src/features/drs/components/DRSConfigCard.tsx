import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Slider } from "@/components/ui/slider";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useDRSConfig, useUpdateDRSConfig } from "../api/drs-queries";
import type { DRSMode, DRSConfigRequest } from "../types/drs";
import { Skeleton } from "@/components/ui/skeleton";
import { Settings } from "lucide-react";

interface DRSConfigCardProps {
  clusterId: string;
}

export function DRSConfigCard({ clusterId }: DRSConfigCardProps) {
  const { data: config, isLoading } = useDRSConfig(clusterId);
  const updateConfig = useUpdateDRSConfig(clusterId);

  const [mode, setMode] = useState<DRSMode>("disabled");
  const [enabled, setEnabled] = useState(false);
  const [cpuWeight, setCpuWeight] = useState(0.4);
  const [memWeight, setMemWeight] = useState(0.4);
  const [netWeight, setNetWeight] = useState(0.2);
  const [threshold, setThreshold] = useState(0.25);
  const [evalInterval, setEvalInterval] = useState(300);

  useEffect(() => {
    if (config) {
      setMode(config.mode);
      setEnabled(config.enabled);
      setCpuWeight(config.weights.cpu);
      setMemWeight(config.weights.memory);
      setNetWeight(config.weights.network);
      setThreshold(config.imbalance_threshold);
      setEvalInterval(config.eval_interval_seconds);
    }
  }, [config]);

  if (isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  const handleSave = () => {
    const request: DRSConfigRequest = {
      mode,
      enabled,
      weights: { cpu: cpuWeight, memory: memWeight, network: netWeight },
      imbalance_threshold: threshold,
      eval_interval_seconds: evalInterval,
    };
    updateConfig.mutate(request);
  };

  const weightSum = cpuWeight + memWeight + netWeight;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Settings className="h-5 w-5" />
          DRS Configuration
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="flex items-center justify-between">
          <Label htmlFor="drs-enabled">Enabled</Label>
          <Switch
            id="drs-enabled"
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </div>

        <div className="space-y-2">
          <Label>Mode</Label>
          <Select value={mode} onValueChange={(v) => setMode(v as DRSMode)}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="disabled">Disabled</SelectItem>
              <SelectItem value="advisory">Advisory</SelectItem>
              <SelectItem value="automatic">Automatic</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-4">
          <Label>Resource Weights</Label>
          <div className="space-y-3">
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>CPU</span>
                <span className="text-muted-foreground">
                  {cpuWeight.toFixed(2)}
                </span>
              </div>
              <Slider
                value={[cpuWeight]}
                onValueChange={([v]) => {
                  if (v !== undefined) setCpuWeight(v);
                }}
                min={0}
                max={1}
                step={0.05}
              />
            </div>
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>Memory</span>
                <span className="text-muted-foreground">
                  {memWeight.toFixed(2)}
                </span>
              </div>
              <Slider
                value={[memWeight]}
                onValueChange={([v]) => {
                  if (v !== undefined) setMemWeight(v);
                }}
                min={0}
                max={1}
                step={0.05}
              />
            </div>
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>Network</span>
                <span className="text-muted-foreground">
                  {netWeight.toFixed(2)}
                </span>
              </div>
              <Slider
                value={[netWeight]}
                onValueChange={([v]) => {
                  if (v !== undefined) setNetWeight(v);
                }}
                min={0}
                max={1}
                step={0.05}
              />
            </div>
            {Math.abs(weightSum - 1.0) > 0.01 && (
              <p className="text-sm text-destructive">
                Weights must sum to 1.0 (current: {weightSum.toFixed(2)})
              </p>
            )}
          </div>
        </div>

        <div className="space-y-2">
          <div className="flex justify-between text-sm">
            <Label>Imbalance Threshold</Label>
            <span className="text-muted-foreground">
              {threshold.toFixed(2)}
            </span>
          </div>
          <Slider
            value={[threshold]}
            onValueChange={([v]) => {
              if (v !== undefined) setThreshold(v);
            }}
            min={0.05}
            max={1}
            step={0.05}
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="eval-interval">Evaluation Interval (seconds)</Label>
          <Input
            id="eval-interval"
            type="number"
            min={60}
            value={evalInterval}
            onChange={(e) => setEvalInterval(Number(e.target.value))}
          />
        </div>

        <Button
          onClick={handleSave}
          disabled={
            updateConfig.isPending || Math.abs(weightSum - 1.0) > 0.01
          }
        >
          {updateConfig.isPending ? "Saving..." : "Save Configuration"}
        </Button>
      </CardContent>
    </Card>
  );
}
