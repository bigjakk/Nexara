import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { Panel } from "@/components/page/Panel";
import {
  SettingRow,
  SettingsFooter,
  SettingsSection,
} from "@/components/page/Settings";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Slider } from "@/components/ui/slider";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useDRSConfig, useUpdateDRSConfig } from "../api/drs-queries";
import type { DRSMode, DRSConfigRequest } from "../types/drs";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertTriangle } from "lucide-react";
import { WarningCallout } from "@/components/WarningCallout";

interface DRSConfigCardProps {
  clusterId: string;
}

export function DRSConfigCard({ clusterId }: DRSConfigCardProps) {
  const { data: config, isLoading } = useDRSConfig(clusterId);
  const updateConfig = useUpdateDRSConfig(clusterId);
  const nativeRebalance = config?.native_crs?.auto_rebalance ?? false;

  const [mode, setMode] = useState<DRSMode>("disabled");
  const [cpuWeight, setCpuWeight] = useState(0.5);
  const [memWeight, setMemWeight] = useState(0.5);
  const [threshold, setThreshold] = useState(0.25);
  const [evalInterval, setEvalInterval] = useState(300);
  const [includeContainers, setIncludeContainers] = useState(false);
  // Armed by default, matching the column default: an unsaved form must not
  // be the thing that disarms it.
  const [excludeVeeamWorkers, setExcludeVeeamWorkers] = useState(true);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );

  useEffect(() => {
    if (config) {
      setMode(config.mode);
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
      setExcludeVeeamWorkers(config.exclude_veeam_workers);
    }
  }, [config]);

  if (isLoading) {
    return (
      <Panel title="DRS configuration">
        <Skeleton className="h-64 w-full" />
      </Panel>
    );
  }

  const handleSave = () => {
    setSaveStatus("idle");
    const request: DRSConfigRequest = {
      mode,
      weights: { cpu: cpuWeight, memory: memWeight },
      imbalance_threshold: threshold,
      eval_interval_seconds: evalInterval,
      include_containers: includeContainers,
      exclude_veeam_workers: excludeVeeamWorkers,
    };
    updateConfig.mutate(request, {
      onSuccess: () => {
        setSaveStatus("saved");
        setTimeout(() => {
          setSaveStatus("idle");
        }, 3000);
      },
      onError: () => {
        setSaveStatus("error");
      },
    });
  };

  const weightSum = cpuWeight + memWeight;
  const weightsOff = Math.abs(weightSum - 1.0) > 0.01;

  return (
    <SettingsSection
      title="DRS configuration"
      description="How Nexara spreads guests across this cluster's nodes."
    >
      {nativeRebalance && (
        <div className="pb-3.5">
          <WarningCallout title="Proxmox native Dynamic Load Balancer is active">
            <p>
              This cluster&apos;s Proxmox CRS is auto-rebalancing HA-managed
              guests. Nexara DRS <strong>Automatic</strong> mode is disabled to
              avoid conflicting migrations; <strong>Advisory</strong> still
              works as a read-only second opinion. Manage the native balancer
              under{" "}
              <Link
                to={`/clusters/${clusterId}?tab=options`}
                className="underline"
              >
                Datacenter &rarr; Options &rarr; CRS
              </Link>
              .
            </p>
          </WarningCallout>
        </div>
      )}
      <SettingRow
        label="Mode"
        description={
          <>
            Disabled turns DRS off. Advisory logs recommendations without
            acting. Automatic will live-migrate VMs.
            {nativeRebalance &&
              " Automatic is unavailable while Proxmox native CRS rebalancing is on."}
          </>
        }
        htmlFor="drs-mode"
      >
        <Select
          value={mode}
          onValueChange={(v) => {
            setMode(v as DRSMode);
          }}
        >
          <SelectTrigger id="drs-mode">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="disabled">Disabled: do nothing</SelectItem>
            <SelectItem value="advisory">Advisory: recommend only</SelectItem>
            <SelectItem value="automatic" disabled={nativeRebalance}>
              Automatic: migrate VMs
            </SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>

      <SettingRow
        label="CPU weight"
        description="How much each resource type influences balance scoring. The two weights must sum to 1.0."
      >
        <Slider
          aria-label="CPU weight"
          className="min-w-0 flex-1"
          value={[cpuWeight]}
          onValueChange={([v]) => {
            if (v !== undefined) setCpuWeight(v);
          }}
          min={0}
          max={1}
          step={0.05}
        />
        <span className="w-10 text-right font-mono text-sm text-muted-foreground">
          {(cpuWeight * 100).toFixed(0)}%
        </span>
      </SettingRow>
      <SettingRow
        label="Memory weight"
        description="A higher memory weight means DRS prioritizes evening out memory usage."
      >
        <Slider
          aria-label="Memory weight"
          className="min-w-0 flex-1"
          value={[memWeight]}
          onValueChange={([v]) => {
            if (v !== undefined) setMemWeight(v);
          }}
          min={0}
          max={1}
          step={0.05}
        />
        <span className="w-10 text-right font-mono text-sm text-muted-foreground">
          {(memWeight * 100).toFixed(0)}%
        </span>
        {weightsOff && (
          <p className="w-full text-sm text-destructive sm:text-right">
            Weights must sum to 1.0 (current: {(weightSum * 100).toFixed(0)}
            %)
          </p>
        )}
      </SettingRow>

      <SettingRow
        label="Imbalance threshold"
        description={
          <>
            {threshold <= 0.15
              ? "Aggressive: triggers on small load differences between nodes."
              : threshold <= 0.3
                ? "Balanced: triggers when node loads diverge moderately."
                : "Conservative: only triggers on large load imbalances."}{" "}
            Lower values cause more frequent migrations.
          </>
        }
      >
        <Slider
          aria-label="Imbalance threshold"
          className="min-w-0 flex-1"
          value={[threshold]}
          onValueChange={([v]) => {
            if (v !== undefined) setThreshold(v);
          }}
          min={0.05}
          max={1}
          step={0.05}
        />
        <span className="w-10 text-right font-mono text-sm text-muted-foreground">
          {(threshold * 100).toFixed(0)}%
        </span>
      </SettingRow>

      <SettingRow
        label="Evaluation interval"
        description={
          <>
            How often DRS checks cluster balance.
            {evalInterval >= 300
              ? ` Every ${String(Math.round(evalInterval / 60))} minutes.`
              : ` Every ${String(evalInterval)} seconds.`}
          </>
        }
        htmlFor="eval-interval"
      >
        <Input
          id="eval-interval"
          type="number"
          min={60}
          className="w-32"
          value={evalInterval}
          onChange={(e) => {
            setEvalInterval(Number(e.target.value));
          }}
        />
        <span className="whitespace-nowrap text-sm text-muted-foreground">
          seconds
        </span>
      </SettingRow>

      <SettingRow
        label="Include containers in balancing"
        description={
          includeContainers ? (
            <Caution>
              Container migration requires downtime. Unlike VMs which support
              live migration, containers must be stopped, moved, and restarted.
              Only enable this if container downtime during rebalancing is
              acceptable.
            </Caution>
          ) : undefined
        }
        htmlFor="include-containers"
      >
        <Switch
          id="include-containers"
          checked={includeContainers}
          onCheckedChange={setIncludeContainers}
        />
      </SettingRow>
      <SettingRow
        label="Never migrate Veeam's own guests"
        description={
          excludeVeeamWorkers ? undefined : (
            <Caution>
              DRS may then migrate a Veeam worker appliance mid-backup, which
              fails the job running on it and leaves nothing behind but a failed
              session. Veeam&apos;s guests are still counted toward node load
              either way.
            </Caution>
          )
        }
        htmlFor="exclude-veeam-workers"
      >
        <Switch
          id="exclude-veeam-workers"
          checked={excludeVeeamWorkers}
          onCheckedChange={setExcludeVeeamWorkers}
        />
      </SettingRow>

      <SettingsFooter>
        {saveStatus === "saved" && (
          <span className="text-sm text-muted-foreground">
            Saved successfully
          </span>
        )}
        {saveStatus === "error" && (
          <span className="text-sm text-destructive">
            {updateConfig.error instanceof Error
              ? updateConfig.error.message
              : "Failed to save"}
          </span>
        )}
        <Button
          size="sm"
          onClick={handleSave}
          disabled={updateConfig.isPending || weightsOff}
        >
          {updateConfig.isPending ? "Saving..." : "Save Configuration"}
        </Button>
      </SettingsFooter>
    </SettingsSection>
  );
}

/** What turning a setting this way costs, said under it in amber. */
function Caution({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex items-start gap-1.5 text-amber-700 dark:text-amber-400">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span>{children}</span>
    </span>
  );
}
