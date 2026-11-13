import { useState } from "react";

import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import {
  useNodeHistoricalMetrics,
  useNodePressure,
} from "@/features/dashboard/api/historical-queries";
import { PressureChart } from "@/features/dashboard/components/PressureChart";
import { useLiveSeries } from "@/features/dashboard/hooks/useLiveSeries";
import { latestValue } from "@/lib/metric-series";
import { DATA_COLOR } from "@/lib/chart-colors";
import type { TimeRange } from "@/types/api";
import type { VmLiveMetric } from "@/types/ws";

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "Live", value: "live" },
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

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
    isLive,
  );

  const chartData = useLiveSeries(historicalData, liveMetric, isLive);
  const pressure = useNodePressure(clusterId, nodeId, timeRange);

  const lastMemPercent = latestValue(chartData, "memPercent");
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
        <span className="text-sm font-medium text-muted-foreground">
          Range:
        </span>
        <div className="flex gap-1">
          {TIME_RANGES.map((tr) => (
            <Button
              key={tr.value}
              size="sm"
              variant={timeRange === tr.value ? "default" : "outline"}
              className="h-7 px-2.5 text-xs"
              onClick={() => {
                setTimeRange(tr.value);
              }}
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
          <MetricChart
            title="CPU Usage"
            data={chartData}
            dataKey="cpuPercent"
            color={DATA_COLOR}
            timeRange={timeRange}
          />
        </div>
        <div className="h-64">
          <MetricChart
            title="Memory Usage"
            data={chartData}
            dataKey="memPercent"
            color={DATA_COLOR}
            timeRange={timeRange}
            headerDetail={memDetail}
          />
        </div>
        <div className="h-64">
          <MetricChart
            title="Disk Read"
            data={chartData}
            dataKey="diskReadBps"
            color={DATA_COLOR}
            timeRange={timeRange}
          />
        </div>
        <div className="h-64">
          <MetricChart
            title="Disk Write"
            data={chartData}
            dataKey="diskWriteBps"
            color={DATA_COLOR}
            timeRange={timeRange}
          />
        </div>
        <div className="h-64">
          <MetricChart
            title="Network In"
            data={chartData}
            dataKey="netInBps"
            color={DATA_COLOR}
            timeRange={timeRange}
          />
        </div>
        <div className="h-64">
          <MetricChart
            title="Network Out"
            data={chartData}
            dataKey="netOutBps"
            color={DATA_COLOR}
            timeRange={timeRange}
          />
        </div>
      </div>
      <PressureChart
        data={pressure.data}
        timeRange={timeRange}
        isLoading={pressure.isLoading}
        isError={pressure.isError}
      />
    </div>
  );
}
