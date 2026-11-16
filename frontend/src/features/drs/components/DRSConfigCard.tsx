import { useState, useEffect } from "react";
import { useTranslation, Trans } from "react-i18next";
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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";

interface DRSConfigCardProps {
  clusterId: string;
}

export function DRSConfigCard({ clusterId }: DRSConfigCardProps) {
  const { t } = useTranslation("clusters");
  const { data: config, isLoading } = useDRSConfig(clusterId);
  const updateConfig = useUpdateDRSConfig(clusterId);
  // The card shows a failed save itself (the hook opts out of the app's
  // toast), and one that lands after it was left is toasted instead.
  const settle = useSaveOutcome(clusterId);
  const nativeRebalance = config?.native_crs?.auto_rebalance ?? false;

  const [mode, setMode] = useState<DRSMode>("disabled");
  const [cpuWeight, setCpuWeight] = useState(0.5);
  const [memWeight, setMemWeight] = useState(0.5);
  const [threshold, setThreshold] = useState(0.25);
  const [evalInterval, setEvalInterval] = useState(300);
  const [bootHoldoff, setBootHoldoff] = useState(10);
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
      setBootHoldoff(config.boot_holdoff_minutes);
      setIncludeContainers(config.include_containers);
      setExcludeVeeamWorkers(config.exclude_veeam_workers);
    }
  }, [config]);

  if (isLoading) {
    return (
      <Panel title={t("drsConfigCard.drsConfiguration")}>
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
      boot_holdoff_minutes: bootHoldoff,
      include_containers: includeContainers,
      exclude_veeam_workers: excludeVeeamWorkers,
    };
    settle(updateConfig.mutateAsync(request), {
      action: t("drsSaveAction"),
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
      title={t("drsConfigCard.drsConfiguration")}
      description={t("drsConfigCard.howNexaraSpreadsGuestsAcross")}
    >
      {nativeRebalance && (
        <div className="pb-3.5">
          <WarningCallout
            title={t("drsConfigCard.proxmoxNativeDynamicLoadBalancer")}
          >
            <p>
              <Trans
                t={t}
                i18nKey="drsConfigCard.thisClusterSProxmoxCRS"
                components={{
                  strong: <strong />,
                  strong2: <strong />,
                  link: (
                    <Link
                      to={`/clusters/${clusterId}?tab=options`}
                      className="underline"
                    />
                  ),
                }}
              />
            </p>
          </WarningCallout>
        </div>
      )}
      <SettingRow
        label={t("drsConfigCard.mode")}
        description={
          nativeRebalance
            ? t("drsConfigCard.disabledTurnsDRSOffAdvisoryNative")
            : t("drsConfigCard.disabledTurnsDRSOffAdvisory")
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
            <SelectItem value="disabled">
              {t("drsConfigCard.disabledDoNothing")}
            </SelectItem>
            <SelectItem value="advisory">
              {t("drsConfigCard.advisoryRecommendOnly")}
            </SelectItem>
            <SelectItem value="automatic" disabled={nativeRebalance}>
              {t("drsConfigCard.automaticMigrateVMs")}
            </SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>

      <SettingRow
        label={t("drsConfigCard.cpuWeight")}
        description={t("drsConfigCard.howMuchEachResourceType")}
      >
        <Slider
          aria-label={t("drsConfigCard.cpuWeight")}
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
        label={t("drsConfigCard.memoryWeight")}
        description={t("drsConfigCard.aHigherMemoryWeightMeans")}
      >
        <Slider
          aria-label={t("drsConfigCard.memoryWeight")}
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
            {t("drsConfigCard.weightsMustSumTo1", {
              weightSum: (weightSum * 100).toFixed(0),
            })}
          </p>
        )}
      </SettingRow>

      <SettingRow
        label={t("drsConfigCard.imbalanceThreshold")}
        description={
          <>
            {threshold <= 0.15
              ? t("drsConfigCard.aggressiveTriggersOnSmallLoad")
              : threshold <= 0.3
                ? t("drsConfigCard.balancedTriggersWhenNodeLoads")
                : t("drsConfigCard.conservativeOnlyTriggersOnLarge")}{" "}
            {t("drsConfigCard.lowerValuesCauseMoreFrequent")}
          </>
        }
      >
        <Slider
          aria-label={t("drsConfigCard.imbalanceThreshold")}
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
        label={t("drsConfigCard.evaluationInterval")}
        description={
          evalInterval >= 300
            ? t("drsConfigCard.evaluationIntervalMinutes", {
                minutes: String(Math.round(evalInterval / 60)),
              })
            : t("drsConfigCard.evaluationIntervalSeconds", {
                seconds: String(evalInterval),
              })
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
          {t("drsConfigCard.seconds")}
        </span>
      </SettingRow>

      <SettingRow
        label={t("drsBootHoldoff")}
        description={t("drsBootHoldoffDesc")}
        htmlFor="boot-holdoff"
      >
        <Input
          id="boot-holdoff"
          type="number"
          min={0}
          max={1440}
          className="w-32"
          value={bootHoldoff}
          onChange={(e) => {
            setBootHoldoff(Number(e.target.value));
          }}
        />
        <span className="whitespace-nowrap text-sm text-muted-foreground">
          {t("drsBootHoldoffUnit")}
        </span>
      </SettingRow>

      <SettingRow
        label={t("drsConfigCard.includeContainersInBalancing")}
        description={
          includeContainers ? (
            <Caution>
              {t("drsConfigCard.containerMigrationRequiresDowntimeUnlike")}
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
        label={t("drsConfigCard.neverMigrateVeeamSOwn")}
        description={
          excludeVeeamWorkers ? undefined : (
            <Caution>{t("drsConfigCard.drsMayThenMigrateA")}</Caution>
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
            {t("drsConfigCard.savedSuccessfully")}
          </span>
        )}
        {saveStatus === "error" && (
          <span className="text-sm text-destructive">
            {updateConfig.error instanceof Error
              ? updateConfig.error.message
              : t("drsConfigCard.failedToSave")}
          </span>
        )}
        <Button
          size="sm"
          onClick={handleSave}
          disabled={updateConfig.isPending || weightsOff}
        >
          {updateConfig.isPending
            ? t("drsConfigCard.saving")
            : t("drsConfigCard.saveConfiguration")}
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
