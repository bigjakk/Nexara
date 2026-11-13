import { useState } from "react";

import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { formatBytes, formatBytesPerSecond, formatPercent } from "@/lib/format";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { TimeRangeSelector } from "@/features/dashboard/components/TimeRangeSelector";
import {
  useNodeHistoricalMetrics,
  useNodePressure,
} from "@/features/dashboard/api/historical-queries";
import { PressureChart } from "@/features/dashboard/components/PressureChart";
import { useLiveSeries } from "@/features/dashboard/hooks/useLiveSeries";
import { latestValue, seriesTail } from "@/lib/metric-series";
import { DATA_COLOR } from "@/lib/chart-colors";
import type { TimeRange } from "@/types/api";
import type { VmLiveMetric } from "@/types/ws";

/** Which measure the large chart under the strip is drawing. */
type MetricFocus = "cpu" | "memory" | "disk" | "network";

/**
 * What the node is doing: each measure's latest reading over its recent shape,
 * and the one that is pressed drawn large below. Load average and IO wait are
 * readings without a stored series, so they sit beside the title instead of
 * in the strip.
 */
export function NodeMetricsPanel({
  clusterId,
  nodeId,
  liveMetric,
  memTotalBytes,
  cpuCount,
  loadAvg,
  ioWait,
}: {
  clusterId: string;
  nodeId: string;
  liveMetric: VmLiveMetric | undefined;
  /** Node RAM size; turns the memory percent into exact used/total bytes. */
  memTotalBytes?: number | undefined;
  cpuCount: number;
  /** The node list's 1, 5 and 15 minute load averages, as one string. */
  loadAvg?: string | undefined;
  /** Share of CPU time spent waiting on IO, 0 to 1. Zero is not reported. */
  ioWait?: number | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const [focus, setFocus] = useState<MetricFocus>("cpu");
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

  const cpu = latestValue(chartData, "cpuPercent");
  const mem = latestValue(chartData, "memPercent");
  const diskRead = latestValue(chartData, "diskReadBps");
  const diskWrite = latestValue(chartData, "diskWriteBps");
  const netIn = latestValue(chartData, "netInBps");
  const netOut = latestValue(chartData, "netOutBps");
  const memDetail =
    mem !== undefined && memTotalBytes !== undefined && memTotalBytes > 0
      ? `${formatBytes((mem / 100) * memTotalBytes)} of ${formatBytes(memTotalBytes)}`
      : undefined;
  const rate = (a: number | undefined, b: number | undefined) =>
    a === undefined && b === undefined
      ? "—"
      : formatBytesPerSecond((a ?? 0) + (b ?? 0));
  // The two halves of a rate. The strip cuts a long line short, so all of it
  // is also the hover text.
  const pair = (
    a: number | undefined,
    b: number | undefined,
    aLabel: string,
    bLabel: string,
  ) => {
    if (a === undefined && b === undefined) return undefined;
    const text = `${formatBytesPerSecond(a ?? 0)} ${aLabel}, ${formatBytesPerSecond(b ?? 0)} ${bLabel}`;
    return <span title={text}>{text}</span>;
  };

  const readings = [
    loadAvg !== undefined && loadAvg !== "" ? `Load ${loadAvg}` : "",
    ioWait !== undefined && ioWait > 0
      ? `IO wait ${(ioWait * 100).toFixed(1)}%`
      : "",
  ].filter((part) => part !== "");

  const chart = (
    title: string,
    dataKey: React.ComponentProps<typeof MetricChart>["dataKey"],
    headerDetail?: string,
  ) => (
    <div className="h-64 min-w-0">
      <MetricChart
        bare
        title={title}
        data={chartData}
        dataKey={dataKey}
        color={DATA_COLOR}
        timeRange={timeRange}
        headerDetail={headerDetail}
      />
    </div>
  );

  return (
    <>
      <Panel
        flush
        title="Performance"
        aside={
          isLoading
            ? "Loading..."
            : readings.length > 0
              ? readings.join(" · ")
              : undefined
        }
        actions={
          <TimeRangeSelector value={timeRange} onChange={setTimeRange} />
        }
      >
        <StatStrip>
          <Stat
            label="CPU"
            value={cpu === undefined ? "—" : formatPercent(cpu)}
            sub={`of ${String(cpuCount)} vCPU`}
            trend={seriesTail(chartData, (p) => p.cpuPercent)}
            trendMax={100}
            selected={focus === "cpu"}
            onSelect={() => {
              setFocus("cpu");
            }}
          />
          <Stat
            label="Memory"
            value={mem === undefined ? "—" : formatPercent(mem)}
            sub={memDetail}
            trend={seriesTail(chartData, (p) => p.memPercent)}
            trendMax={100}
            selected={focus === "memory"}
            onSelect={() => {
              setFocus("memory");
            }}
          />
          <Stat
            label="Disk I/O"
            value={rate(diskRead, diskWrite)}
            sub={pair(diskRead, diskWrite, "read", "write")}
            trend={seriesTail(chartData, (p) =>
              p.diskReadBps === null && p.diskWriteBps === null
                ? null
                : (p.diskReadBps ?? 0) + (p.diskWriteBps ?? 0),
            )}
            selected={focus === "disk"}
            onSelect={() => {
              setFocus("disk");
            }}
          />
          <Stat
            label="Network"
            value={rate(netIn, netOut)}
            sub={pair(netIn, netOut, "in", "out")}
            trend={seriesTail(chartData, (p) =>
              p.netInBps === null && p.netOutBps === null
                ? null
                : (p.netInBps ?? 0) + (p.netOutBps ?? 0),
            )}
            selected={focus === "network"}
            onSelect={() => {
              setFocus("network");
            }}
          />
        </StatStrip>
        <div className="-mt-px border-t px-[18px] pt-4">
          {focus === "cpu" && chart("CPU Usage", "cpuPercent")}
          {focus === "memory" && chart("Memory Usage", "memPercent", memDetail)}
          {focus === "disk" && (
            <div className="grid gap-6 sm:grid-cols-2">
              {chart("Disk Read", "diskReadBps")}
              {chart("Disk Write", "diskWriteBps")}
            </div>
          )}
          {focus === "network" && (
            <div className="grid gap-6 sm:grid-cols-2">
              {chart("Network In", "netInBps")}
              {chart("Network Out", "netOutBps")}
            </div>
          )}
        </div>
      </Panel>
      <PressureChart
        data={pressure.data}
        timeRange={timeRange}
        isLoading={pressure.isLoading}
        isError={pressure.isError}
      />
    </>
  );
}
