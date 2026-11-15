import { useState, useMemo, useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";
import { LayoutGrid, Lock, Plus, ServerCrash } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import { useDashboardData } from "../api/dashboard-queries";
import type { ClusterSummary } from "../api/dashboard-queries";
import {
  useHistoricalMetrics,
  useSeedMetrics,
  useSeedMetricsForClusters,
} from "../api/historical-queries";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useIsMobile } from "@/hooks/useIsMobile";
import { StatsOverview } from "../components/StatsOverview";
import { FleetBoard } from "../components/FleetBoard";
import { NeedsAttention } from "../components/NeedsAttention";
import { Skeleton } from "@/components/ui/skeleton";
import { ClusterCard } from "../components/ClusterCard";
import { EmptyState } from "@/components/EmptyState";
import { AddClusterDialog } from "../components/AddClusterDialog";
import { TimeRangeSelector } from "../components/TimeRangeSelector";
import { RefreshRateSelector } from "../components/RefreshRateSelector";
import { LiveMetricCards } from "../components/LiveMetricCards";
import { MetricChart } from "../components/MetricChart";
import { TopConsumers } from "../components/TopConsumers";
import { UpsStatusWidget } from "@/features/power/components/UpsStatusWidget";
import { usePermissions } from "@/hooks/usePermissions";
import { DashboardGrid } from "../components/DashboardGrid";
import type { LayoutItem } from "react-grid-layout";
import { DashboardPresetSelector } from "../components/DashboardPresetSelector";
import {
  appendWidgets,
  buildDefaultPreset,
  parseWidgetId,
  getTemplate,
  getWidgetLabel,
  unplacedDefaultWidgets,
  type ClusterInfo,
  type DashboardPreset,
} from "../lib/widget-registry";
import { ApiClientError } from "@/lib/api-client";
import {
  useSetting,
  useUpsertSettings,
} from "@/features/settings/api/settings-queries";
import type { TimeRange } from "@/types/api";
import type {
  AggregatedMetrics,
  MetricSeriesPoint,
  WsConnectionState,
} from "@/types/ws";
import { estateCounts } from "../lib/estate-summary";
import { LIVE_WINDOW_MS, spliceLive } from "@/lib/metric-series";
import { DATA_COLOR } from "@/lib/chart-colors";

/** The write that makes `preset` the layout a reload opens on. */
function layoutWrite(preset: DashboardPreset) {
  return { key: "dashboard.layout", value: preset as unknown, scope: "user" };
}

/** The user setting that lists the widgets the dashboard has offered. */
const OFFERED_WIDGETS_KEY = "dashboard.offered_widgets";

/**
 * Whether the numbers on the page are live: a dot and a word, quiet while
 * the stream is up and coloured only once it is not.
 */
function ConnectionState({ status }: { status: WsConnectionState }) {
  const { t } = useTranslation("dashboard");
  const connected = status === "connected";
  const down = status === "disconnected";
  return (
    <span
      className="flex items-center gap-[7px]"
      title={connected ? t("liveConnected") : t(status)}
      data-testid="connection-state"
    >
      <span
        aria-hidden="true"
        className={cn(
          "h-[7px] w-[7px] shrink-0 rounded-full",
          connected ? "bg-emerald-500" : down ? "bg-red-500" : "bg-amber-500",
        )}
        data-testid="connection-dot"
      />
      <span
        className={cn(
          "font-[550]",
          connected
            ? "text-foreground"
            : down
              ? "text-red-700 dark:text-red-400"
              : "text-amber-700 dark:text-amber-400",
        )}
      >
        {connected ? t("live") : t(status)}
      </span>
    </span>
  );
}

export function DashboardPage() {
  const { t } = useTranslation("dashboard");
  const { hasGlobalPermission } = usePermissions();
  const canViewUps = hasGlobalPermission("view", "ups");
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const [editMode, setEditMode] = useState(false);
  const isMobile = useIsMobile();

  // Grid drag/resize is a precision-pointer interaction — leave edit mode
  // when shrinking to the mobile layout (the Customize button hides too).
  useEffect(() => {
    if (isMobile) setEditMode(false);
  }, [isMobile]);
  const { data, isLoading, error } = useDashboardData();
  const { status } = useWebSocket();
  const saveSettings = useUpsertSettings();

  const clusterIds = useMemo(
    () => data?.clusters.map((s) => s.cluster.id) ?? [],
    [data?.clusters],
  );

  const clusters = useMemo<ClusterInfo[]>(
    () =>
      data?.clusters.map((s) => ({
        id: s.cluster.id,
        name: s.cluster.name,
        nodeCount: s.nodeCount,
      })) ?? [],
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
  const seedMetrics = useSeedMetricsForClusters(clusterIds);

  // Build default preset based on actual clusters
  const computedDefaultPreset = useMemo(
    () => buildDefaultPreset(clusters),
    [clusters],
  );

  // Load saved dashboard layout from backend
  const layoutQuery = useSetting("dashboard.layout", "user");
  const presetsQuery = useSetting("dashboard.presets", "user");

  const [activePreset, setActivePreset] = useState<DashboardPreset | null>(
    null,
  );
  const [initializedFromBackend, setInitializedFromBackend] = useState(false);
  // The widgets of the saved layout the page opened on; null when it opened
  // on the default. Only a saved layout is offered what it lacks.
  const [savedWidgetIds, setSavedWidgetIds] = useState<string[] | null>(null);

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
          // Saved layouts may predate the trend strips added to the stats and
          // live-metrics cards — grow those widgets once so they don't clip.
          // Keyed on the old minH so a later deliberate user resize sticks.
          const layouts = saved.layouts.map((l) =>
            (l.i === "stats-overview" || l.i.startsWith("live-metrics")) &&
            (l.minH ?? 0) < 3
              ? { ...l, h: Math.max(l.h, 4), minH: 3 }
              : l,
          );
          setActivePreset({
            name: saved.name ?? "Custom",
            widgetIds: saved.widgetIds,
            layouts,
          });
          setSavedWidgetIds(saved.widgetIds);
          setInitializedFromBackend(true);
          return;
        }
      }
    }
    // Not before the saved layout has answered: a default chosen while it is
    // still on its way would replace it for good.
    if (layoutQuery.isPending) return;
    // If no saved layout (or stale), use default when clusters are ready
    if (clusters.length > 0) {
      setActivePreset(computedDefaultPreset);
      setInitializedFromBackend(true);
    }
  }, [
    layoutQuery.data?.value,
    layoutQuery.isPending,
    clusters,
    computedDefaultPreset,
    initializedFromBackend,
  ]);

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

  // Widgets the default layout has and a saved one lacks (Fleet, a new
  // cluster's charts): offered once, never inserted. Add or Dismiss records
  // them, so the offer does not come back for them on any browser.
  const offeredQuery = useSetting(OFFERED_WIDGETS_KEY, "user");
  const [offerClosed, setOfferClosed] = useState(false);
  const offeredBefore = useMemo<string[] | null>(() => {
    const value = offeredQuery.data?.value;
    if (Array.isArray(value)) {
      return value.filter((v): v is string => typeof v === "string");
    }
    const noneYet =
      offeredQuery.error instanceof ApiClientError &&
      offeredQuery.error.status === 404;
    return noneYet ? [] : null;
  }, [offeredQuery.data?.value, offeredQuery.error]);

  // A layout written without a default widget records that widget as
  // offered: one removed on purpose is not announced as new next time.
  const writesFor = useCallback(
    (preset: DashboardPreset, alsoOffered: readonly string[] = []) => {
      const writes = [layoutWrite(preset)];
      if (offeredBefore !== null) {
        const fresh = [
          ...new Set([
            ...alsoOffered,
            ...unplacedDefaultWidgets(
              preset.widgetIds,
              computedDefaultPreset.widgetIds,
              offeredBefore,
            ),
          ]),
        ].filter((id) => !offeredBefore.includes(id));
        if (fresh.length > 0) {
          writes.push({
            key: OFFERED_WIDGETS_KEY,
            value: [...offeredBefore, ...fresh] as unknown,
            scope: "user",
          });
        }
      }
      return writes;
    },
    [offeredBefore, computedDefaultPreset.widgetIds],
  );

  // Every save below goes out as one mutation, however many settings it
  // writes, so a failure is reported once.
  const saveLayout = useCallback(() => {
    saveSettings.mutate(writesFor(effectivePreset));
  }, [effectivePreset, saveSettings, writesFor]);

  const handleReset = useCallback(() => {
    setActivePreset(computedDefaultPreset);
    saveSettings.mutate(writesFor(computedDefaultPreset));
  }, [computedDefaultPreset, saveSettings, writesFor]);

  const handlePresetSelect = useCallback(
    (preset: DashboardPreset) => {
      setActivePreset(preset);
      saveSettings.mutate(writesFor(preset));
    },
    [saveSettings, writesFor],
  );

  const handlePresetSave = useCallback(
    (name: string) => {
      const newPreset: DashboardPreset = { ...effectivePreset, name };
      const updated = [
        ...savedPresets.filter((p) => p.name !== name),
        newPreset,
      ];
      // The preset is now the one shown, so it is the layout a reload opens
      // on, as a pick from the list is; not only once the layout is locked.
      saveSettings.mutate([
        { key: "dashboard.presets", value: updated, scope: "user" },
        ...writesFor(newPreset),
      ]);
      setActivePreset(newPreset);
    },
    [effectivePreset, savedPresets, saveSettings, writesFor],
  );

  const handlePresetDelete = useCallback(
    (name: string) => {
      const updated = savedPresets.filter((p) => p.name !== name);
      const writes = [
        { key: "dashboard.presets", value: updated as unknown, scope: "user" },
      ];
      if (effectivePreset.name === name) {
        setActivePreset(computedDefaultPreset);
        writes.push(...writesFor(computedDefaultPreset));
      }
      saveSettings.mutate(writes);
    },
    [
      effectivePreset.name,
      savedPresets,
      saveSettings,
      computedDefaultPreset,
      writesFor,
    ],
  );

  const newWidgetIds = useMemo(() => {
    if (savedWidgetIds === null || offeredBefore === null) return [];
    return unplacedDefaultWidgets(
      [...savedWidgetIds, ...effectivePreset.widgetIds],
      computedDefaultPreset.widgetIds,
      offeredBefore,
    );
  }, [
    savedWidgetIds,
    offeredBefore,
    effectivePreset.widgetIds,
    computedDefaultPreset.widgetIds,
  ]);

  const offeredWrite = useCallback(
    () => ({
      key: OFFERED_WIDGETS_KEY,
      value: [...(offeredBefore ?? []), ...newWidgetIds] as unknown,
      scope: "user",
    }),
    [offeredBefore, newWidgetIds],
  );

  const closeOffer = useCallback(() => {
    setOfferClosed(true);
    saveSettings.mutate([offeredWrite()]);
  }, [offeredWrite, saveSettings]);

  const addNewWidgets = useCallback(() => {
    const updated = appendWidgets(effectivePreset, newWidgetIds, clusters);
    setActivePreset(updated);
    setOfferClosed(true);
    saveSettings.mutate(writesFor(updated, newWidgetIds));
  }, [effectivePreset, newWidgetIds, clusters, saveSettings, writesFor]);

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
      const clusterLiveMetrics = clusterId
        ? liveMetrics.get(clusterId)
        : undefined;

      switch (type) {
        case "fleet-board":
          return <FleetBoard data={data} metrics={liveMetrics} />;

        case "stats-overview":
          return (
            <StatsOverview
              totalNodes={data.totalNodes}
              totalNodesOnline={data.totalNodesOnline}
              totalVMs={data.totalVMs}
              totalVMsRunning={data.totalVMsRunning}
              totalContainers={data.totalContainers}
              totalContainersRunning={data.totalContainersRunning}
              totalStorageBytes={data.totalStorageBytes}
              totalStorageUsedBytes={data.totalStorageUsedBytes}
              isLoading={isLoading}
              vmsByCluster={data.vmsByCluster}
              metrics={liveMetrics}
              seeds={seedMetrics}
            />
          );

        case "cluster-cards":
          return (
            <div className="grid h-full content-start gap-4 overflow-y-auto [grid-template-columns:repeat(auto-fit,minmax(min(17rem,100%),1fr))]">
              {data.clusters.map((s) => (
                <ClusterCard
                  key={s.cluster.id}
                  summary={s}
                  metrics={liveMetrics.get(s.cluster.id)}
                  guests={data.vmsByCluster.get(s.cluster.id)}
                />
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
          return (
            <LiveMetricCards
              metrics={clusterLiveMetrics}
              clusterId={clusterId ?? undefined}
              title={summary?.cluster.name}
            />
          );

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

        case "ups-status":
          // A saved layout can keep the widget after its grant is gone; its
          // reads would be refused, so say why instead.
          return canViewUps ? (
            <UpsStatusWidget />
          ) : (
            <Card className="flex h-full items-center justify-center p-4 text-center text-sm text-muted-foreground">
              {t("upsWidgetNeedsPermission")}
            </Card>
          );

        default:
          return (
            <Card className="flex h-full items-center justify-center p-4 text-center text-sm text-muted-foreground">
              {t("unknownWidget", { widgetId })}
            </Card>
          );
      }
    },
    [data, clusterMap, isLoading, liveMetrics, seedMetrics, timeRange, t, canViewUps],
  );

  const hasClusters = data != null && data.clusters.length > 0;
  const showOffer =
    hasClusters && !editMode && !offerClosed && newWidgetIds.length > 0;
  const counts = hasClusters
    ? estateCounts(data.clusters, data.vmsByCluster)
    : null;

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("dashboard")}
        summary={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-0.5">
            <ConnectionState status={status} />
            <span>
              {counts === null
                ? t("dashboardPurpose")
                : [
                    t("summaryClusterCount", { count: counts.clusters }),
                    t("fleetNodeCount", { count: counts.nodes }),
                    t("fleetGuestCount", { count: counts.guests }),
                    t("fleetRunningCount", { count: counts.guestsRunning }),
                  ].join(", ")}
            </span>
            {counts !== null && counts.nodesOffline > 0 && (
              <span className="font-semibold text-red-700 dark:text-red-400">
                {t("summaryNodesOffline", { count: counts.nodesOffline })}
              </span>
            )}
          </span>
        }
        actions={
          <>
            <TimeRangeSelector value={timeRange} onChange={setTimeRange} />
            <RefreshRateSelector />
            <DashboardPresetSelector
              activePreset={effectivePreset}
              savedPresets={savedPresets}
              onSelect={handlePresetSelect}
              onSave={handlePresetSave}
              onDelete={handlePresetDelete}
              onReset={handleReset}
            />
            {!isMobile && (
              <Button
                size="sm"
                // The page's one primary button, and only while customising:
                // it is then the way out.
                variant={editMode ? "default" : "outline"}
                className="gap-1.5"
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
            )}
            {/* With no cluster yet the empty state below carries the one
                primary "Add cluster"; with one, adding another is a quiet
                option here. */}
            {!(data != null && data.clusters.length === 0) && (
              <AddClusterDialog
                trigger={
                  <Button size="sm" variant="outline" className="gap-1.5">
                    <Plus className="h-4 w-4" />
                    {t("addCluster")}
                  </Button>
                }
              />
            )}
          </>
        }
      />

      {error != null ? (
        <Panel>
          <p className="text-sm text-destructive">{t("failedLoadDashboard")}</p>
        </Panel>
      ) : (
        <>
          {!isLoading && data?.clusters.length === 0 && (
            <EmptyState
              icon={ServerCrash}
              title={t("noClustersRegistered")}
              description={t("addClusterToGetStarted")}
              action={<AddClusterDialog />}
            />
          )}

          <NeedsAttention />

          {showOffer && (
            <Panel
              title={t("newWidgetsTitle")}
              actions={
                <>
                  <Button size="sm" variant="ghost" onClick={closeOffer}>
                    {t("dismissNewWidgets")}
                  </Button>
                  <Button size="sm" variant="outline" onClick={addNewWidgets}>
                    <Plus className="h-4 w-4" />
                    {t("addNewWidgets")}
                  </Button>
                </>
              }
            >
              <p className="text-sm text-muted-foreground">
                {t("newWidgetsBody", {
                  widgets: newWidgetIds
                    .map((id) => getWidgetLabel(id, clusterNames))
                    .join(", "),
                })}
              </p>
            </Panel>
          )}

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

          {/* Shaped like nothing in particular: which widgets come next is
              the viewer's own layout, not yet known. */}
          {isLoading && !data && (
            <Skeleton className="h-72 w-full rounded-[10px]" />
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
  const isLive = timeRange === "live";
  // The seed is only drawn in live mode; an empty id leaves it unfetched.
  const seedData = useSeedMetrics(isLive ? summary.cluster.id : "");

  const liveHistory = liveMetrics?.history;
  const chartData = useMemo<MetricSeriesPoint[]>(() => {
    if (!isLive) return historicalQuery.data ?? [];
    return spliceLive(seedData ?? [], liveHistory ?? [], LIVE_WINDOW_MS);
  }, [isLive, liveHistory, seedData, historicalQuery.data]);

  const chartConfigs = {
    cpu: {
      titleKey: "cpuUsage",
      dataKey: "cpuPercent" as const,
      color: DATA_COLOR,
    },
    memory: {
      titleKey: "memoryUsage",
      dataKey: "memPercent" as const,
      color: DATA_COLOR,
    },
    disk: {
      titleKey: "diskIoRead",
      dataKey: "diskReadBps" as const,
      color: DATA_COLOR,
    },
    network: {
      titleKey: "networkIn",
      dataKey: "netInBps" as const,
      color: DATA_COLOR,
    },
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
