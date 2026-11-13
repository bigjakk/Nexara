import { useMemo, useState } from "react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { Segmented } from "@/components/ui/segmented";
import {
  getTimestampFormatter,
  timeAxisProps,
} from "@/features/dashboard/components/time-axis";
import { DATA_COLOR } from "@/lib/chart-colors";
import { useCephMetrics } from "../api/ceph-queries";
import type { CephStatus } from "../types/ceph";
import { formatBytes, formatBytesPerSecond, formatPercent } from "@/lib/format";

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

/** Which measure the large chart under the strip is drawing. */
type Focus = "iops" | "throughput" | "capacity" | "osds";

interface ChartPoint {
  timestamp: number;
  readOps: number;
  writeOps: number;
  readBytes: number;
  writeBytes: number;
  usedPct: number;
  osdsUp: number;
  osdsTotal: number;
}

type SeriesKey = Exclude<keyof ChartPoint, "timestamp">;

/** How many of the newest points a strip's sparkline draws. */
const TREND_POINTS = 60;

function formatOps(ops: number): string {
  if (ops >= 1000000) return `${(ops / 1000000).toFixed(1)}M`;
  if (ops >= 1000) return `${(ops / 1000).toFixed(1)}K`;
  return String(Math.round(ops));
}

function formatCount(v: number): string {
  return String(Math.round(v));
}

interface SeriesChartProps {
  title: string;
  data: ChartPoint[];
  dataKey: SeriesKey;
  formatter: (v: number) => string;
  timeframe: Timeframe;
  /** The axis ceiling, where the series has one: 100 for a percentage. */
  max?: number | undefined;
  /** A count: whole-number ticks, so a small range is not "0, 0, 1, 1". */
  whole?: boolean | undefined;
}

/** One measured series over time, in the data colour. */
function SeriesChart({
  title,
  data,
  dataKey,
  formatter,
  timeframe,
  max,
  whole = false,
}: SeriesChartProps) {
  const last = data[data.length - 1]?.[dataKey];
  const labelFormatter = getTimestampFormatter(timeframe);

  return (
    <div className="flex h-64 min-w-0 flex-col">
      <div className="flex items-center gap-2 pb-2">
        <span
          aria-hidden="true"
          className="h-2 w-2 shrink-0 rounded-full bg-data"
        />
        <h3 className="truncate text-xs font-medium text-muted-foreground">
          {title}
        </h3>
        {last !== undefined && (
          <span className="ml-auto shrink-0 text-base font-semibold tabular-nums tracking-tight">
            {formatter(last)}
          </span>
        )}
      </div>
      {data.length === 0 ? (
        <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
          Waiting for data...
        </div>
      ) : (
        <div className="min-h-0 flex-1">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart
              data={data}
              margin={{ top: 4, right: 8, bottom: 0, left: 0 }}
            >
              <defs>
                <linearGradient
                  id={`ceph-grad-${dataKey}`}
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="1"
                >
                  <stop offset="5%" stopColor={DATA_COLOR} stopOpacity={0.25} />
                  <stop offset="95%" stopColor={DATA_COLOR} stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 4"
                className="stroke-border"
                vertical={false}
              />
              {/* A time axis, not the default category axis: a category axis
                  spaces points evenly whatever time they stand for, so a gap
                  in collection (bucketing emits no row for an empty bucket)
                  collapsed to nothing instead of showing as one. */}
              <XAxis {...timeAxisProps(timeframe)} />
              <YAxis
                tickFormatter={formatter}
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                axisLine={false}
                tickLine={false}
                width={60}
                {...(max === undefined ? {} : { domain: [0, max] })}
                allowDecimals={!whole}
              />
              {/* Number(), not String(), in labelFormatter: the X dataKey is
                  epoch milliseconds, and new Date("1757116800000") is an
                  Invalid Date. */}
              <Tooltip
                labelFormatter={(label: unknown) =>
                  labelFormatter(Number(label))
                }
                formatter={(value: unknown) => [
                  value != null ? formatter(Number(value)) : "0",
                  title,
                ]}
                cursor={{
                  stroke: "hsl(var(--muted-foreground))",
                  strokeOpacity: 0.4,
                }}
                contentStyle={{
                  backgroundColor: "hsl(var(--popover))",
                  border: "1px solid hsl(var(--border))",
                  borderRadius: "8px",
                  fontSize: "12px",
                  boxShadow: "0 4px 12px rgba(0,0,0,0.25)",
                }}
              />
              <Area
                type="monotone"
                dataKey={dataKey}
                name={title}
                stroke={DATA_COLOR}
                fill={`url(#ceph-grad-${dataKey})`}
                strokeWidth={2}
                isAnimationActive={false}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </div>
  );
}

export function CephMetricsChart({ clusterId, status }: CephMetricsChartProps) {
  const [timeframe, setTimeframe] = useState<Timeframe>("1h");
  const [focus, setFocus] = useState<Focus>("iops");
  const metricsQuery = useCephMetrics(clusterId, timeframe);

  // Keyed on metricsQuery.data, not on a `?? []` of it, which would mint a
  // fresh array whenever data is undefined and recompute on every render.
  // Worth having: the strip and the charts share this array and the query
  // refetches on a 60s interval. Timestamps stay numeric and are formatted at
  // the axis, so only the ticks actually drawn pay for formatting.
  const chartData = useMemo<ChartPoint[]>(
    () =>
      (metricsQuery.data ?? []).map((m) => ({
        timestamp: new Date(m.time).getTime(),
        readOps: m.read_ops_sec,
        writeOps: m.write_ops_sec,
        readBytes: m.read_bytes_sec,
        writeBytes: m.write_bytes_sec,
        usedPct: m.bytes_total > 0 ? (m.bytes_used / m.bytes_total) * 100 : 0,
        osdsUp: m.osds_up,
        osdsTotal: m.osds_total,
      })),
    [metricsQuery.data],
  );
  const recent = chartData.slice(-TREND_POINTS);

  // The headline numbers are live, from status (30s refetch); the trends and
  // the chart are the stored history for the chosen range.
  const pg = status?.pgmap;
  const capacityPct =
    pg && pg.bytes_total > 0 ? (pg.bytes_used / pg.bytes_total) * 100 : null;
  const osdsTotal = status?.osdmap.num_osds;

  const chart = (
    title: string,
    dataKey: SeriesKey,
    formatter: (v: number) => string,
    opts: { max?: number | undefined; whole?: boolean } = {},
  ) => (
    <SeriesChart
      title={title}
      data={chartData}
      dataKey={dataKey}
      formatter={formatter}
      timeframe={timeframe}
      max={opts.max}
      whole={opts.whole}
    />
  );

  return (
    <Panel
      flush
      title="Performance"
      aside={metricsQuery.isLoading ? "Loading..." : undefined}
      actions={
        <Segmented
          aria-label="Time range"
          value={timeframe}
          onValueChange={setTimeframe}
          options={TIMEFRAMES}
        />
      }
    >
      {/* The strip is the summary and the chart's selector in one: each
          measure's live reading over its recent shape, and the one that is
          pressed drawn large below. */}
      <StatStrip>
        <Stat
          label="IOPS"
          value={pg ? formatOps(pg.read_op_per_sec + pg.write_op_per_sec) : "—"}
          sub={
            pg
              ? `${formatOps(pg.read_op_per_sec)} read, ${formatOps(pg.write_op_per_sec)} write`
              : undefined
          }
          trend={recent.map((p) => p.readOps + p.writeOps)}
          selected={focus === "iops"}
          onSelect={() => {
            setFocus("iops");
          }}
        />
        <Stat
          label="Throughput"
          value={
            pg
              ? formatBytesPerSecond(pg.read_bytes_sec + pg.write_bytes_sec)
              : "—"
          }
          sub={
            pg
              ? `${formatBytesPerSecond(pg.read_bytes_sec)} read, ${formatBytesPerSecond(pg.write_bytes_sec)} write`
              : undefined
          }
          trend={recent.map((p) => p.readBytes + p.writeBytes)}
          selected={focus === "throughput"}
          onSelect={() => {
            setFocus("throughput");
          }}
        />
        <Stat
          label="Capacity used"
          value={capacityPct === null ? "—" : formatPercent(capacityPct)}
          sub={
            pg
              ? `${formatBytes(pg.bytes_used)} of ${formatBytes(pg.bytes_total)}`
              : undefined
          }
          trend={recent.map((p) => p.usedPct)}
          trendMax={100}
          selected={focus === "capacity"}
          onSelect={() => {
            setFocus("capacity");
          }}
        />
        <Stat
          label="OSDs up"
          value={
            status
              ? `${String(status.osdmap.num_up_osds)} of ${String(status.osdmap.num_osds)}`
              : "—"
          }
          trend={recent.map((p) => p.osdsUp)}
          trendMax={osdsTotal}
          selected={focus === "osds"}
          onSelect={() => {
            setFocus("osds");
          }}
        />
      </StatStrip>
      <div className="-mt-px border-t px-[18px] pt-4">
        {focus === "iops" && (
          <div className="grid gap-6 sm:grid-cols-2">
            {chart("Read IOPS", "readOps", formatOps, { whole: true })}
            {chart("Write IOPS", "writeOps", formatOps, { whole: true })}
          </div>
        )}
        {focus === "throughput" && (
          <div className="grid gap-6 sm:grid-cols-2">
            {chart("Read throughput", "readBytes", formatBytesPerSecond)}
            {chart("Write throughput", "writeBytes", formatBytesPerSecond)}
          </div>
        )}
        {focus === "capacity" &&
          chart("Capacity used", "usedPct", formatPercent, { max: 100 })}
        {focus === "osds" &&
          chart("OSDs up", "osdsUp", formatCount, {
            max: osdsTotal || undefined,
            whole: true,
          })}
        {chartData.length === 0 && !metricsQuery.isLoading && (
          <p className="pt-2 text-center text-[12.5px] text-muted-foreground">
            No historical metrics available yet.
          </p>
        )}
      </div>
    </Panel>
  );
}
