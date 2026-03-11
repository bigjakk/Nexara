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
import { Checkbox } from "@/components/ui/checkbox";
import { useDRSConfig, useUpdateDRSConfig } from "../api/drs-queries";
import type { DRSMode, DRSConfigRequest } from "../types/drs";
import { Skeleton } from "@/components/ui/skeleton";
import { Settings, AlertTriangle } from "lucide-react";

interface DRSConfigCardProps {
  clusterId: string;
}

export function DRSConfigCard({ clusterId }: DRSConfigCardProps) {
  const { data: config, isLoading } = useDRSConfig(clusterId);
  const updateConfig = useUpdateDRSConfig(clusterId);

  const [mode, setMode] = useState<DRSMode>("disabled");
  const [enabled, setEnabled] = useState(false);
  const [cpuWeight, setCpuWeight] = useState(0.5);
  const [memWeight, setMemWeight] = useState(0.5);
  const [threshold, setThreshold] = useState(0.25);
  const [evalInterval, setEvalInterval] = useState(300);
  const [includeContainers, setIncludeContainers] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );

  useEffect(() => {
    if (config) {
      setMode(config.mode);
      setEnabled(config.enabled);
      // Normalize legacy configs that included a network weight.
      const rawCpu = config.weights.cpu;
      const rawMem = config.weights.memory;
      const sum = rawCpu + rawMem;
      if (sum > 0 && Math.abs(sum - 1.0) > 0.01) {
        setCpuWeight(Math.round((rawCpu / sum) * 20) / 20);
        setMemWeight(Math.round((rawMem / sum) * 20) / 20);
      } else {
        setCpuWeight(rawCpu);
        setMemWeight(rawMem);
      }
      setThreshold(config.imbalance_threshold);
      setEvalInterval(config.eval_interval_seconds);
      setIncludeContainers(config.include_containers);
    }
  }, [config]);

  if (isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  const handleSave = () => {
    setSaveStatus("idle");
    const request: DRSConfigRequest = {
      mode,
      enabled,
      weights: { cpu: cpuWeight, memory: memWeight },
      imbalance_threshold: threshold,
      eval_interval_seconds: evalInterval,
      include_containers: includeContainers,
    };
    updateConfig.mutate(request, {
      onSuccess: () => {
        setSaveStatus("saved");
        setTimeout(() => { setSaveStatus("idle"); }, 3000);
      },
      onError: () => {
        setSaveStatus("error");
      },
    });
  };

  const weightSum = cpuWeight + memWeight;

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
          <div>
            <Label htmlFor="drs-enabled">Enabled</Label>
            <p className="text-xs text-muted-foreground">
              Enable automatic workload balancing across nodes
            </p>
          </div>
          <Switch
            id="drs-enabled"
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </div>

        <div className="space-y-2">
          <Label>Mode</Label>
          <Select value={mode} onValueChange={(v) => { setMode(v as DRSMode); }}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="disabled">Disabled</SelectItem>
              <SelectItem value="advisory">Advisory &mdash; recommend only</SelectItem>
              <SelectItem value="automatic">Automatic &mdash; migrate VMs</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            Advisory logs recommendations without acting. Automatic will live-migrate VMs.
          </p>
        </div>

        <div className="space-y-4">
          <div>
            <Label>Resource Weights</Label>
            <p className="text-xs text-muted-foreground">
              How much each resource type influences balance scoring. Higher
              memory weight means DRS prioritizes evening out memory usage.
              Must sum to 1.0.
            </p>
          </div>
          <div className="space-y-3">
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>CPU</span>
                <span className="font-mono text-muted-foreground">
                  {(cpuWeight * 100).toFixed(0)}%
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
                <span className="font-mono text-muted-foreground">
                  {(memWeight * 100).toFixed(0)}%
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
            {Math.abs(weightSum - 1.0) > 0.01 && (
              <p className="text-sm text-destructive">
                Weights must sum to 1.0 (current: {(weightSum * 100).toFixed(0)}%)
              </p>
            )}
          </div>
        </div>

        <div className="space-y-2">
          <div className="flex justify-between text-sm">
            <Label>Imbalance Threshold</Label>
            <span className="font-mono text-muted-foreground">
              {(threshold * 100).toFixed(0)}%
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
          <p className="text-xs text-muted-foreground">
            {threshold <= 0.15
              ? "Aggressive — triggers on small load differences between nodes."
              : threshold <= 0.30
                ? "Balanced — triggers when node loads diverge moderately."
                : "Conservative — only triggers on large load imbalances."}
            {" "}Lower values cause more frequent migrations.
          </p>
        </div>

        <div className="space-y-2">
          <Label htmlFor="eval-interval">Evaluation Interval</Label>
          <div className="flex items-center gap-2">
            <Input
              id="eval-interval"
              type="number"
              min={60}
              value={evalInterval}
              onChange={(e) => { setEvalInterval(Number(e.target.value)); }}
            />
            <span className="text-sm text-muted-foreground whitespace-nowrap">seconds</span>
          </div>
          <p className="text-xs text-muted-foreground">
            How often DRS checks cluster balance.
            {evalInterval >= 300
              ? ` Every ${String(Math.round(evalInterval / 60))} minutes.`
              : ` Every ${String(evalInterval)} seconds.`}
          </p>
        </div>

        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <Checkbox
              id="include-containers"
              checked={includeContainers}
              onCheckedChange={(v) => { setIncludeContainers(v === true); }}
            />
            <Label htmlFor="include-containers">Include containers in balancing</Label>
          </div>
          {includeContainers && (
            <div className="flex items-start gap-2 rounded-md border border-yellow-500/50 bg-yellow-500/10 p-3">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-yellow-500" />
              <p className="text-xs text-yellow-600 dark:text-yellow-400">
                Container migration requires downtime. Unlike VMs which support live migration,
                containers must be stopped, moved, and restarted. Only enable this if container
                downtime during rebalancing is acceptable.
              </p>
            </div>
          )}
        </div>

        <div className="flex items-center gap-3">
          <Button
            onClick={handleSave}
            disabled={
              updateConfig.isPending || Math.abs(weightSum - 1.0) > 0.01
            }
          >
            {updateConfig.isPending ? "Saving..." : "Save Configuration"}
          </Button>
          {saveStatus === "saved" && (
            <span className="text-sm text-green-600">Saved successfully</span>
          )}
          {saveStatus === "error" && (
            <span className="text-sm text-destructive">
              {updateConfig.error instanceof Error
                ? updateConfig.error.message
                : "Failed to save"}
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
