import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useNodeHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import type { TimeRange } from "@/types/api";
import type { MetricDataPoint, VmLiveMetric } from "@/types/ws";

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "Live", value: "live" },
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

const MAX_LIVE_POINTS = 60;

export function NodeMetricsPanel({
  clusterId,
  nodeId,
  liveMetric,
  memTotalBytes,
}: {
  clusterId: string;
  nodeId: string;
  liveMetric: VmLiveMetric | undefined;
  /** Node RAM size; turns the memory percent into exact used/total bytes. */
  memTotalBytes?: number | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const isLive = timeRange === "live";
  // Live charts are seeded with the last hour of stored data so they're full
  // on page load; WS ticks are appended on top.
  const { data: historicalData, isLoading } = useNodeHistoricalMetrics(
    clusterId,
    nodeId,
    isLive ? "1h" : timeRange,
  );

  // Accumulate per-node live ticks locally — the WS store only keeps the
  // latest snapshot per node, not a history. The ref guards against the same
  // snapshot being appended twice (StrictMode's dev double-mount).
  const [liveHistory, setLiveHistory] = useState<MetricDataPoint[]>([]);
  const lastAppendedRef = useRef<VmLiveMetric | undefined>(undefined);
  useEffect(() => {
    if (!liveMetric || liveMetric === lastAppendedRef.current) return;
    lastAppendedRef.current = liveMetric;
    setLiveHistory((prev) => {
      const next = [
        ...prev,
        {
          timestamp: Date.now(),
          cpuPercent: liveMetric.cpuPercent,
          memPercent: liveMetric.memPercent,
          diskReadBps: liveMetric.diskReadBps,
          diskWriteBps: liveMetric.diskWriteBps,
          netInBps: liveMetric.netInBps,
          netOutBps: liveMetric.netOutBps,
        },
      ];
      return next.length > MAX_LIVE_POINTS
        ? next.slice(next.length - MAX_LIVE_POINTS)
        : next;
    });
  }, [liveMetric]);

  const chartData = useMemo(() => {
    const seed = historicalData ?? [];
    if (!isLive) return seed;
    if (liveHistory.length === 0) return seed;
    const firstLiveTs = liveHistory[0]?.timestamp ?? 0;
    return [...seed.filter((p) => p.timestamp < firstLiveTs), ...liveHistory];
  }, [isLive, historicalData, liveHistory]);

  const lastMemPercent = chartData[chartData.length - 1]?.memPercent;
  const memDetail =
    lastMemPercent !== undefined &&
    memTotalBytes !== undefined &&
    memTotalBytes > 0
      ? `${formatBytes((lastMemPercent / 100) * memTotalBytes)} of ${formatBytes(memTotalBytes)}`
      : undefined;

  return (
    <div className="space-y-4">
      {/* Time range selector */}
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">Range:</span>
        <div className="flex gap-1">
          {TIME_RANGES.map((tr) => (
            <Button
              key={tr.value}
              size="sm"
              variant={timeRange === tr.value ? "default" : "outline"}
              className="h-7 px-2.5 text-xs"
              onClick={() => { setTimeRange(tr.value); }}
            >
              {tr.label}
            </Button>
          ))}
        </div>
        {isLoading && (
          <span className="text-xs text-muted-foreground">Loading...</span>
        )}
      </div>

      {/* Charts (live = seeded 1h + WS ticks) */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="h-64">
          <MetricChart title="CPU Usage" data={chartData} dataKey="cpuPercent" color="hsl(221, 83%, 53%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Memory Usage" data={chartData} dataKey="memPercent" color="hsl(142, 71%, 45%)" timeRange={timeRange} headerDetail={memDetail} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Read" data={chartData} dataKey="diskReadBps" color="hsl(38, 92%, 50%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Write" data={chartData} dataKey="diskWriteBps" color="hsl(0, 84%, 60%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network In" data={chartData} dataKey="netInBps" color="hsl(262, 83%, 58%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network Out" data={chartData} dataKey="netOutBps" color="hsl(330, 81%, 60%)" timeRange={timeRange} />
        </div>
      </div>
    </div>
  );
}
