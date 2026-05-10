import { useState } from "react";

import { Button } from "@/components/ui/button";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useNodeHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import type { TimeRange } from "@/types/api";

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

export function NodeMetricsPanel({
  clusterId,
  nodeId,
  liveMetric,
}: {
  clusterId: string;
  nodeId: string;
  liveMetric: { cpuPercent: number; memPercent: number } | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("1h");
  const { data: historicalData, isLoading } = useNodeHistoricalMetrics(clusterId, nodeId, timeRange);
  const chartData = historicalData ?? [];

  return (
    <div className="space-y-4">
      {/* Live gauges */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">CPU Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.cpuPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.cpuPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">Memory Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.memPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.memPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
      </div>

      {/* Time range selector */}
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">Historical:</span>
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

      {/* Historical charts */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="h-64">
          <MetricChart title="CPU Usage" data={chartData} dataKey="cpuPercent" color="hsl(221, 83%, 53%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Memory Usage" data={chartData} dataKey="memPercent" color="hsl(142, 71%, 45%)" timeRange={timeRange} />
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
