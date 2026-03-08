import { useState } from "react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { useCephMetrics } from "../api/ceph-queries";
import type { CephStatus } from "../types/ceph";

interface CephMetricsChartProps {
  clusterId: string;
  status: CephStatus | undefined;
}

const TIMEFRAMES = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
] as const;

type Timeframe = (typeof TIMEFRAMES)[number]["value"];

function formatTime(ts: string, timeframe: Timeframe): string {
  const d = new Date(ts);
  if (timeframe === "7d") {
    return d.toLocaleDateString([], { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const k = 1024;
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k));
  const idx = Math.min(i, units.length - 1);
  return `${(bytes / Math.pow(k, idx)).toFixed(1)} ${units[idx] ?? "?"}`;
}

function formatOps(ops: number): string {
  if (ops >= 1000000) return `${(ops / 1000000).toFixed(1)}M`;
  if (ops >= 1000) return `${(ops / 1000).toFixed(1)}K`;
  return String(Math.round(ops));
}

interface MiniChartProps {
  title: string;
  data: Array<Record<string, unknown>>;
  dataKey: string;
  color: string;
  formatter: (v: number) => string;
  timeframe: Timeframe;
  secondaryDataKey?: string;
  secondaryColor?: string;
  secondaryName?: string;
  primaryName?: string;
}

function MiniChart({
  title,
  data,
  dataKey,
  color,
  formatter,
  timeframe,
  secondaryDataKey,
  secondaryColor,
  secondaryName,
  primaryName,
}: MiniChartProps) {
  if (data.length === 0) {
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
            Waiting for data...
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <ResponsiveContainer width="100%" height={200}>
          <AreaChart data={data}>
            <defs>
              <linearGradient id={`ceph-grad-${dataKey}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={color} stopOpacity={0.3} />
                <stop offset="95%" stopColor={color} stopOpacity={0} />
              </linearGradient>
              {secondaryDataKey && secondaryColor && (
                <linearGradient id={`ceph-grad-${secondaryDataKey}`} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={secondaryColor} stopOpacity={0.3} />
                  <stop offset="95%" stopColor={secondaryColor} stopOpacity={0} />
                </linearGradient>
              )}
            </defs>
            <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
            <XAxis
              dataKey="time"
              tickFormatter={(ts: string) => formatTime(ts, timeframe)}
              tick={{ fontSize: 10 }}
            />
            <YAxis tickFormatter={formatter} tick={{ fontSize: 10 }} width={60} />
            <Tooltip
              labelFormatter={(label: unknown) => {
                const d = new Date(String(label));
                return d.toLocaleString();
              }}
              formatter={(value: unknown, name: unknown) => [
                value != null ? formatter(Number(value)) : "0",
                typeof name === "string" ? name : "",
              ]}
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "6px",
                fontSize: "12px",
              }}
            />
            <Area
              type="monotone"
              dataKey={dataKey}
              name={primaryName ?? dataKey}
              stroke={color}
              fill={`url(#ceph-grad-${dataKey})`}
              strokeWidth={2}
              isAnimationActive={false}
            />
            {secondaryDataKey && secondaryColor && (
              <Area
                type="monotone"
                dataKey={secondaryDataKey}
                name={secondaryName ?? secondaryDataKey}
                stroke={secondaryColor}
                fill={`url(#ceph-grad-${secondaryDataKey})`}
                strokeWidth={2}
                isAnimationActive={false}
              />
            )}
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}

export function CephMetricsChart({ clusterId, status }: CephMetricsChartProps) {
  const [timeframe, setTimeframe] = useState<Timeframe>("1h");
  const metricsQuery = useCephMetrics(clusterId, timeframe);
  const metrics = metricsQuery.data ?? [];

  const chartData = metrics.map((m) => ({
    time: m.time,
    readOps: m.read_ops_sec,
    writeOps: m.write_ops_sec,
    readBytes: m.read_bytes_sec,
    writeBytes: m.write_bytes_sec,
    usedPct:
      m.bytes_total > 0 ? (m.bytes_used / m.bytes_total) * 100 : 0,
    osdsUp: m.osds_up,
    osdsTotal: m.osds_total,
  }));

  // Live values from status (30s refetch)
  const liveReadOps = status?.pgmap.read_op_per_sec ?? 0;
  const liveWriteOps = status?.pgmap.write_op_per_sec ?? 0;
  const liveReadBytes = status?.pgmap.read_bytes_sec ?? 0;
  const liveWriteBytes = status?.pgmap.write_bytes_sec ?? 0;
  const liveCapPct =
    status && status.pgmap.bytes_total > 0
      ? (status.pgmap.bytes_used / status.pgmap.bytes_total) * 100
      : null;

  return (
    <div className="space-y-4">
      {/* Live gauges */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-foreground/60">Read IOPS (Live)</p>
          <p className="text-lg font-semibold tabular-nums">
            {status ? formatOps(liveReadOps) : "--"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-foreground/60">Write IOPS (Live)</p>
          <p className="text-lg font-semibold tabular-nums">
            {status ? formatOps(liveWriteOps) : "--"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-foreground/60">Throughput (Live)</p>
          <p className="text-lg font-semibold tabular-nums">
            {status
              ? `R: ${formatBytes(liveReadBytes)}/s  W: ${formatBytes(liveWriteBytes)}/s`
              : "--"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-foreground/60">Capacity (Live)</p>
          <MetricMiniBar value={liveCapPct} />
          <p className="mt-1 text-xs text-foreground/60">
            {status
              ? `${formatBytes(status.pgmap.bytes_used)} / ${formatBytes(status.pgmap.bytes_total)}`
              : "No live data"}
          </p>
        </div>
      </div>

      {/* Time range selector */}
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-foreground/60">Historical:</span>
        <div className="flex gap-1">
          {TIMEFRAMES.map((tf) => (
            <Button
              key={tf.value}
              size="sm"
              variant={timeframe === tf.value ? "default" : "outline"}
              className="h-7 px-2.5 text-xs"
              onClick={() => { setTimeframe(tf.value); }}
            >
              {tf.label}
            </Button>
          ))}
        </div>
        {metricsQuery.isLoading && (
          <span className="text-xs text-foreground/60">Loading...</span>
        )}
      </div>

      {/* Historical charts */}
      <div className="grid gap-4 sm:grid-cols-2">
        <MiniChart
          title="IOPS"
          data={chartData}
          dataKey="readOps"
          primaryName="Read"
          color="hsl(221, 83%, 53%)"
          secondaryDataKey="writeOps"
          secondaryName="Write"
          secondaryColor="hsl(262, 83%, 58%)"
          formatter={formatOps}
          timeframe={timeframe}
        />
        <MiniChart
          title="Throughput"
          data={chartData}
          dataKey="readBytes"
          primaryName="Read"
          color="hsl(38, 92%, 50%)"
          secondaryDataKey="writeBytes"
          secondaryName="Write"
          secondaryColor="hsl(0, 84%, 60%)"
          formatter={(v) => `${formatBytes(v)}/s`}
          timeframe={timeframe}
        />
        <MiniChart
          title="Capacity Usage %"
          data={chartData}
          dataKey="usedPct"
          primaryName="Used %"
          color="hsl(330, 81%, 60%)"
          formatter={(v) => `${v.toFixed(1)}%`}
          timeframe={timeframe}
        />
        <MiniChart
          title="OSDs Up"
          data={chartData}
          dataKey="osdsUp"
          primaryName="Up"
          color="hsl(142, 71%, 45%)"
          secondaryDataKey="osdsTotal"
          secondaryName="Total"
          secondaryColor="hsl(240, 5%, 34%)"
          formatter={(v) => String(Math.round(v))}
          timeframe={timeframe}
        />
      </div>

      {metrics.length === 0 && !metricsQuery.isLoading && (
        <p className="text-center text-sm text-muted-foreground">
          No historical metrics available yet.
        </p>
      )}
    </div>
  );
}
