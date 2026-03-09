import { useState, useMemo, useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";
import { LayoutGrid, Lock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useDashboardData } from "../api/dashboard-queries";
import type { ClusterSummary } from "../api/dashboard-queries";
import { useHistoricalMetrics, useSeedMetrics } from "../api/historical-queries";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { useWebSocket } from "@/hooks/useWebSocket";
import { StatsOverview } from "../components/StatsOverview";
import { ClusterCard } from "../components/ClusterCard";
import { EmptyState } from "../components/EmptyState";
import { AddClusterDialog } from "../components/AddClusterDialog";
import { TimeRangeSelector } from "../components/TimeRangeSelector";
import { RefreshRateSelector } from "../components/RefreshRateSelector";
import { LiveMetricCards } from "../components/LiveMetricCards";
import { MetricChart } from "../components/MetricChart";
import { TopConsumers } from "../components/TopConsumers";
import { DashboardGrid } from "../components/DashboardGrid";
import type { LayoutItem } from "react-grid-layout";
import { DashboardPresetSelector } from "../components/DashboardPresetSelector";
import {
  buildDefaultPreset,
  parseWidgetId,
  getTemplate,
  type ClusterInfo,
  type DashboardPreset,
} from "../lib/widget-registry";
import {
  useSetting,
  useUpsertSetting,
} from "@/features/settings/api/settings-queries";
import type { TimeRange } from "@/types/api";
import type { AggregatedMetrics } from "@/types/ws";

function ConnectionDot({ status, t }: { status: string; t: (key: string) => string }) {
  const isConnected = status === "connected";
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full ${isConnected ? "bg-green-500" : "bg-red-500"}`}
      title={isConnected ? t("liveConnected") : t("disconnected")}
      data-testid="connection-dot"
    />
  );
}

export function DashboardPage() {
  const { t } = useTranslation("dashboard");
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const [editMode, setEditMode] = useState(false);
  const { data, isLoading, error } = useDashboardData();
  const { status } = useWebSocket();
  const upsertSetting = useUpsertSetting();

  const clusterIds = useMemo(
    () => data?.clusters.map((s) => s.cluster.id) ?? [],
    [data?.clusters],
  );

  const clusters = useMemo<ClusterInfo[]>(
    () => data?.clusters.map((s) => ({ id: s.cluster.id, name: s.cluster.name })) ?? [],
    [data?.clusters],
  );

  const clusterNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of clusters) {
      map.set(c.id, c.name);
    }
    return map;
  }, [clusters]);

  const clusterMap = useMemo(() => {
    const map = new Map<string, ClusterSummary>();
    for (const s of data?.clusters ?? []) {
      map.set(s.cluster.id, s);
    }
    return map;
  }, [data?.clusters]);

  const liveMetrics = useDashboardMetrics(clusterIds);

  // Build default preset based on actual clusters
  const computedDefaultPreset = useMemo(
    () => buildDefaultPreset(clusters),
    [clusters],
  );

  // Load saved dashboard layout from backend
  const layoutQuery = useSetting("dashboard.layout", "user");
  const presetsQuery = useSetting("dashboard.presets", "user");

  const [activePreset, setActivePreset] = useState<DashboardPreset | null>(null);
  const [initializedFromBackend, setInitializedFromBackend] = useState(false);

  // Load layout from backend on first load only — not on subsequent query refreshes
  // to avoid infinite update loops when saveLayout triggers query invalidation.
  useEffect(() => {
    if (initializedFromBackend) return;

    if (layoutQuery.data?.value && typeof layoutQuery.data.value === "object") {
      const saved = layoutQuery.data.value as {
        widgetIds?: string[];
        layouts?: LayoutItem[];
        name?: string;
      };
      if (saved.widgetIds && saved.layouts) {
        // Detect stale layouts from before per-cluster widget IDs were introduced:
        // if any per-cluster widget type appears without a ":clusterId" suffix, discard
        const isStale = saved.widgetIds.some((id) => {
          const tmpl = getTemplate(id);
          return tmpl?.perCluster === true && !id.includes(":");
        });
        if (!isStale) {
          setActivePreset({
            name: saved.name ?? "Custom",
            widgetIds: saved.widgetIds,
            layouts: saved.layouts,
          });
          setInitializedFromBackend(true);
          return;
        }
      }
    }
    // If no saved layout (or stale), use default when clusters are ready
    if (clusters.length > 0) {
      setActivePreset(computedDefaultPreset);
      setInitializedFromBackend(true);
    }
  }, [layoutQuery.data?.value, clusters, computedDefaultPreset, initializedFromBackend]);

  // Effective preset — fallback to computed default
  const effectivePreset = activePreset ?? computedDefaultPreset;

  const savedPresets = useMemo<DashboardPreset[]>(() => {
    if (presetsQuery.data?.value && Array.isArray(presetsQuery.data.value)) {
      return presetsQuery.data.value as DashboardPreset[];
    }
    return [];
  }, [presetsQuery.data?.value]);

  const handleLayoutChange = useCallback(
    (layouts: LayoutItem[], widgetIds: string[]) => {
      const updated: DashboardPreset = {
        ...effectivePreset,
        layouts,
        widgetIds,
      };
      setActivePreset(updated);
    },
    [effectivePreset],
  );

  const saveLayout = useCallback(() => {
    upsertSetting.mutate({
      key: "dashboard.layout",
      value: effectivePreset,
      scope: "user",
    });
  }, [effectivePreset, upsertSetting]);

  const handleReset = useCallback(() => {
    setActivePreset(computedDefaultPreset);
    upsertSetting.mutate({
      key: "dashboard.layout",
      value: computedDefaultPreset,
      scope: "user",
    });
  }, [computedDefaultPreset, upsertSetting]);

  const handlePresetSelect = useCallback(
    (preset: DashboardPreset) => {
      setActivePreset(preset);
      upsertSetting.mutate({
        key: "dashboard.layout",
        value: preset,
        scope: "user",
      });
    },
    [upsertSetting],
  );

  const handlePresetSave = useCallback(
    (name: string) => {
      const newPreset: DashboardPreset = { ...effectivePreset, name };
      const updated = [
        ...savedPresets.filter((p) => p.name !== name),
        newPreset,
      ];
      upsertSetting.mutate({
        key: "dashboard.presets",
        value: updated,
        scope: "user",
      });
      setActivePreset(newPreset);
    },
    [effectivePreset, savedPresets, upsertSetting],
  );

  const handlePresetDelete = useCallback(
    (name: string) => {
      const updated = savedPresets.filter((p) => p.name !== name);
      upsertSetting.mutate({
        key: "dashboard.presets",
        value: updated,
        scope: "user",
      });
      if (effectivePreset.name === name) {
        setActivePreset(computedDefaultPreset);
      }
    },
    [effectivePreset.name, savedPresets, upsertSetting, computedDefaultPreset],
  );

  const toggleEditMode = useCallback(() => {
    if (editMode) {
      saveLayout();
    }
    setEditMode(!editMode);
  }, [editMode, saveLayout]);

  const renderWidget = useCallback(
    (widgetId: string) => {
      if (!data) return null;

      const { type, clusterId } = parseWidgetId(widgetId);

      // For per-cluster widgets, look up the cluster
      const summary = clusterId ? clusterMap.get(clusterId) : undefined;
      const clusterLiveMetrics = clusterId ? liveMetrics.get(clusterId) : undefined;

      switch (type) {
        case "stats-overview":
          return (
            <StatsOverview
              totalNodes={data.totalNodes}
              totalVMs={data.totalVMs}
              totalContainers={data.totalContainers}
              totalStorageBytes={data.totalStorageBytes}
              isLoading={isLoading}
            />
          );

        case "cluster-cards":
          return (
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
              {data.clusters.map((s) => (
                <ClusterCard key={s.cluster.id} summary={s} />
              ))}
            </div>
          );

        case "cpu-chart":
          return summary ? (
            <ClusterChart
              summary={summary}
              timeRange={timeRange}
              liveMetrics={clusterLiveMetrics}
              vmNameMap={data.vmNameMap}
              chartType="cpu"
            />
          ) : null;

        case "memory-chart":
          return summary ? (
            <ClusterChart
              summary={summary}
              timeRange={timeRange}
              liveMetrics={clusterLiveMetrics}
              vmNameMap={data.vmNameMap}
              chartType="memory"
            />
          ) : null;

        case "disk-chart":
          return summary ? (
            <ClusterChart
              summary={summary}
              timeRange={timeRange}
              liveMetrics={clusterLiveMetrics}
              vmNameMap={data.vmNameMap}
              chartType="disk"
            />
          ) : null;

        case "network-chart":
          return summary ? (
            <ClusterChart
              summary={summary}
              timeRange={timeRange}
              liveMetrics={clusterLiveMetrics}
              vmNameMap={data.vmNameMap}
              chartType="network"
            />
          ) : null;

        case "live-metrics":
          return <LiveMetricCards metrics={clusterLiveMetrics} />;

        case "top-consumers": {
          const combinedConsumers = data.clusters.flatMap((s) => {
            const m = liveMetrics.get(s.cluster.id);
            return m?.topConsumers ?? [];
          });
          return (
            <TopConsumers
              consumers={combinedConsumers}
              vmNames={data.vmNameMap}
            />
          );
        }

        default:
          return (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              {t("unknownWidget", { widgetId })}
            </div>
          );
      }
    },
    [data, clusterMap, isLoading, liveMetrics, timeRange, t],
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-bold">{t("dashboard")}</h1>
          <ConnectionDot status={status} t={t} />
        </div>
        <div className="flex items-center gap-3">
          <DashboardPresetSelector
            activePreset={effectivePreset}
            savedPresets={savedPresets}
            onSelect={handlePresetSelect}
            onSave={handlePresetSave}
            onDelete={handlePresetDelete}
            onReset={handleReset}
          />
          <Button
            size="sm"
            variant={editMode ? "default" : "outline"}
            className="gap-1"
            onClick={toggleEditMode}
          >
            {editMode ? (
              <>
                <Lock className="h-4 w-4" />
                {t("lock")}
              </>
            ) : (
              <>
                <LayoutGrid className="h-4 w-4" />
                {t("customize")}
              </>
            )}
          </Button>
          <TimeRangeSelector value={timeRange} onChange={setTimeRange} />
          <RefreshRateSelector />
          <AddClusterDialog />
        </div>
      </div>

      {error != null ? (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          {t("failedLoadDashboard")}
        </div>
      ) : (
        <>
          {!isLoading && data?.clusters.length === 0 && <EmptyState />}

          {data != null && data.clusters.length > 0 && (
            <DashboardGrid
              preset={effectivePreset}
              defaultPreset={computedDefaultPreset}
              clusters={clusters}
              clusterNames={clusterNames}
              onLayoutChange={handleLayoutChange}
              onReset={handleReset}
              editMode={editMode}
            >
              {renderWidget}
            </DashboardGrid>
          )}

          {isLoading && !data && (
            <StatsOverview
              totalNodes={0}
              totalVMs={0}
              totalContainers={0}
              totalStorageBytes={0}
              isLoading={true}
            />
          )}
        </>
      )}

    </div>
  );
}

// Individual chart widget that handles its own data fetching
interface ClusterChartProps {
  summary: ClusterSummary;
  timeRange: TimeRange;
  liveMetrics: AggregatedMetrics | undefined;
  vmNameMap: Map<string, string>;
  chartType: "cpu" | "memory" | "disk" | "network";
}

function ClusterChart({
  summary,
  timeRange,
  liveMetrics,
  chartType,
}: ClusterChartProps) {
  const historicalQuery = useHistoricalMetrics(summary.cluster.id, timeRange);
  const seedData = useSeedMetrics(summary.cluster.id);
  const isLive = timeRange === "live";

  const liveHistory = liveMetrics?.history ?? [];
  const chartData = isLive
    ? (liveHistory.length > 0 ? liveHistory : (seedData ?? []))
    : (historicalQuery.data ?? []);

  const chartConfigs = {
    cpu: { titleKey: "cpuUsage", dataKey: "cpuPercent" as const, color: "#3b82f6" },
    memory: { titleKey: "memoryUsage", dataKey: "memPercent" as const, color: "#8b5cf6" },
    disk: { titleKey: "diskIoRead", dataKey: "diskReadBps" as const, color: "#f59e0b" },
    network: { titleKey: "networkIn", dataKey: "netInBps" as const, color: "#10b981" },
  };

  const config = chartConfigs[chartType];

  if (!isLive && historicalQuery.isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        {i18n.t("common:loading")}
      </div>
    );
  }

  return (
    <MetricChart
      title={`${summary.cluster.name} — ${i18n.t(`dashboard:${config.titleKey}`)}`}
      data={chartData}
      dataKey={config.dataKey}
      color={config.color}
      timeRange={timeRange}
    />
  );
}
