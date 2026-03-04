import { useMemo } from "react";
import { useDashboardData } from "../api/dashboard-queries";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { useWebSocket } from "@/hooks/useWebSocket";
import { StatsOverview } from "../components/StatsOverview";
import { ClusterCard } from "../components/ClusterCard";
import { EmptyState } from "../components/EmptyState";
import { AddClusterDialog } from "../components/AddClusterDialog";
import { TimeRangeSelector } from "../components/TimeRangeSelector";
import { LiveMetricCards } from "../components/LiveMetricCards";
import { MetricChart } from "../components/MetricChart";
import { HealthScore } from "../components/HealthScore";
import { TopConsumers } from "../components/TopConsumers";

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

export function DashboardPage() {
  const { data, isLoading, error } = useDashboardData();
  const { status } = useWebSocket();

  const clusterIds = useMemo(
    () => data?.clusters.map((s) => s.cluster.id) ?? [],
    [data?.clusters],
  );

  const liveMetrics = useDashboardMetrics(clusterIds);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <ConnectionDot status={status} />
        </div>
        <div className="flex items-center gap-3">
          <TimeRangeSelector />
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

              {/* Per-cluster live metrics */}
              {data.clusters.map((summary) => {
                const metrics = liveMetrics.get(summary.cluster.id);
                return (
                  <div
                    key={`metrics-${summary.cluster.id}`}
                    className="space-y-4"
                  >
                    <h2 className="text-lg font-semibold">
                      {summary.cluster.name} — Live Metrics
                    </h2>

                    <div className="grid gap-4 lg:grid-cols-[100px_1fr]">
                      <div className="flex items-start justify-center pt-2">
                        <HealthScore score={metrics?.healthScore ?? 0} />
                      </div>
                      <LiveMetricCards metrics={metrics} />
                    </div>

                    <div className="grid gap-4 md:grid-cols-2">
                      <MetricChart
                        title="CPU Usage"
                        data={metrics?.history ?? []}
                        dataKey="cpuPercent"
                        color="#3b82f6"
                      />
                      <MetricChart
                        title="Memory Usage"
                        data={metrics?.history ?? []}
                        dataKey="memPercent"
                        color="#8b5cf6"
                      />
                      <MetricChart
                        title="Disk I/O (Read)"
                        data={metrics?.history ?? []}
                        dataKey="diskReadBps"
                        color="#f59e0b"
                      />
                      <MetricChart
                        title="Network In"
                        data={metrics?.history ?? []}
                        dataKey="netInBps"
                        color="#10b981"
                      />
                    </div>

                    <TopConsumers consumers={metrics?.topConsumers ?? []} />
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}
