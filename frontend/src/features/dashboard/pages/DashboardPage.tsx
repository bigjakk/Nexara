import { useState, useMemo } from "react";
import { Plus } from "lucide-react";
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
import { CreateVMDialog } from "@/features/vms/components/CreateVMDialog";
import { CreateCTDialog } from "@/features/vms/components/CreateCTDialog";
import type { TimeRange } from "@/types/api";
import type { AggregatedMetrics } from "@/types/ws";

function ConnectionDot({ status }: { status: string }) {
  const isConnected = status === "connected";
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full ${isConnected ? "bg-green-500" : "bg-red-500"}`}
      title={isConnected ? "Live connected" : "Disconnected"}
      data-testid="connection-dot"
    />
  );
}

interface ClusterMetricsSectionProps {
  summary: ClusterSummary;
  timeRange: TimeRange;
  liveMetrics: AggregatedMetrics | undefined;
  vmNameMap: Map<string, string>;
}

function ClusterMetricsSection({
  summary,
  timeRange,
  liveMetrics,
  vmNameMap,
}: ClusterMetricsSectionProps) {
  const historicalQuery = useHistoricalMetrics(summary.cluster.id, timeRange);
  const seedData = useSeedMetrics(summary.cluster.id);
  const isLive = timeRange === "live";

  // For live mode, use live history but fall back to seed data (recent 1h) until live data accumulates
  const liveHistory = liveMetrics?.history ?? [];
  const chartData = isLive
    ? (liveHistory.length > 0 ? liveHistory : (seedData ?? []))
    : (historicalQuery.data ?? []);

  const heading = isLive
    ? `${summary.cluster.name} — Live Metrics`
    : `${summary.cluster.name} — ${timeRange} Historical`;

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold">{heading}</h2>

      {isLive && <LiveMetricCards metrics={liveMetrics} />}

      {!isLive && historicalQuery.isLoading && (
        <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
          Loading historical data...
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <MetricChart
          title="CPU Usage"
          data={chartData}
          dataKey="cpuPercent"
          color="#3b82f6"
          timeRange={timeRange}
        />
        <MetricChart
          title="Memory Usage"
          data={chartData}
          dataKey="memPercent"
          color="#8b5cf6"
          timeRange={timeRange}
        />
        <MetricChart
          title="Disk I/O (Read)"
          data={chartData}
          dataKey="diskReadBps"
          color="#f59e0b"
          timeRange={timeRange}
        />
        <MetricChart
          title="Network In"
          data={chartData}
          dataKey="netInBps"
          color="#10b981"
          timeRange={timeRange}
        />
      </div>

      {isLive && (
        <TopConsumers
          consumers={liveMetrics?.topConsumers ?? []}
          vmNames={vmNameMap}
        />
      )}
    </div>
  );
}

export function DashboardPage() {
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const [createVMOpen, setCreateVMOpen] = useState(false);
  const [createCTOpen, setCreateCTOpen] = useState(false);
  const { data, isLoading, error } = useDashboardData();
  const { status } = useWebSocket();

  const clusterIds = useMemo(
    () => data?.clusters.map((s) => s.cluster.id) ?? [],
    [data?.clusters],
  );

  // Auto-select first cluster for create dialogs
  const firstClusterId = data?.clusters[0]?.cluster.id ?? "";

  const liveMetrics = useDashboardMetrics(clusterIds);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <ConnectionDot status={status} />
        </div>
        <div className="flex items-center gap-3">
          {firstClusterId && (
            <>
              <Button
                size="sm"
                className="gap-1"
                onClick={() => { setCreateVMOpen(true); }}
              >
                <Plus className="h-4 w-4" />
                New VM
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="gap-1"
                onClick={() => { setCreateCTOpen(true); }}
              >
                <Plus className="h-4 w-4" />
                New CT
              </Button>
            </>
          )}
          <TimeRangeSelector value={timeRange} onChange={setTimeRange} />
          <RefreshRateSelector />
          <AddClusterDialog />
        </div>
      </div>

      {error != null ? (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          Failed to load dashboard data. Please try again.
        </div>
      ) : (
        <>
          <StatsOverview
            totalNodes={data?.totalNodes ?? 0}
            totalVMs={data?.totalVMs ?? 0}
            totalContainers={data?.totalContainers ?? 0}
            totalStorageBytes={data?.totalStorageBytes ?? 0}
            isLoading={isLoading}
          />

          {!isLoading && data?.clusters.length === 0 && <EmptyState />}

          {data != null && data.clusters.length > 0 && (
            <div className="space-y-8">
              {/* Cluster cards grid */}
              <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
                {data.clusters.map((summary) => (
                  <ClusterCard key={summary.cluster.id} summary={summary} />
                ))}
              </div>

              {/* Per-cluster metrics */}
              {data.clusters.map((summary) => (
                <ClusterMetricsSection
                  key={`metrics-${summary.cluster.id}`}
                  summary={summary}
                  timeRange={timeRange}
                  liveMetrics={liveMetrics.get(summary.cluster.id)}
                  vmNameMap={data.vmNameMap}
                />
              ))}
            </div>
          )}
        </>
      )}

      <CreateVMDialog
        open={createVMOpen}
        onOpenChange={setCreateVMOpen}
        clusterId={firstClusterId}
      />
      <CreateCTDialog
        open={createCTOpen}
        onOpenChange={setCreateCTOpen}
        clusterId={firstClusterId}
      />
    </div>
  );
}
