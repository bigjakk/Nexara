import type { ReactNode } from "react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
} from "recharts";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { DATA_COLOR } from "@/lib/chart-colors";
import { usePBSDatastoreRRD } from "../api/backup-queries";
import type { PBSDatastoreRRDEntry } from "../types/backup";
import { formatBytesPerSecond } from "@/lib/format";
import {
  AXIS_TICK,
  SECOND_SERIES_COLOR,
  SECOND_SERIES_DASH,
  TOOLTIP_CURSOR,
  TOOLTIP_STYLE,
  unbroken,
} from "./chart-style";
import type { IOTimeframe } from "./datastore-timeframes";

function formatIOPS(value: number): string {
  if (value < 1) return value.toFixed(2);
  if (value < 100) return value.toFixed(1);
  if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
  return Math.round(value).toString();
}

function formatTime(ts: number, timeframe: string): string {
  const d = new Date(ts * 1000);
  if (timeframe === "week" || timeframe === "month") {
    return d.toLocaleDateString([], { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

interface DatastoreIOViewProps {
  pbsId: string;
  store: string;
  timeframe: IOTimeframe;
}

function computeAverages(entries: PBSDatastoreRRDEntry[]) {
  let readSum = 0;
  let writeSum = 0;
  let readIOSum = 0;
  let writeIOSum = 0;
  let count = 0;

  for (const e of entries) {
    if (
      e.read_bytes != null ||
      e.write_bytes != null ||
      e.read_ios != null ||
      e.write_ios != null
    ) {
      readSum += e.read_bytes ?? 0;
      writeSum += e.write_bytes ?? 0;
      readIOSum += e.read_ios ?? 0;
      writeIOSum += e.write_ios ?? 0;
      count++;
    }
  }

  if (count === 0) return null;

  return {
    avgReadBytes: readSum / count,
    avgWriteBytes: writeSum / count,
    avgReadIOs: readIOSum / count,
    avgWriteIOs: writeIOSum / count,
  };
}

interface IORow {
  time: string;
  readBytes: number;
  writeBytes: number;
  readIOs: number;
  writeIOs: number;
}

/** Read drawn solid in the data colour, write as a neutral dashed line. */
function ReadWriteChart({
  title,
  data,
  readKey,
  writeKey,
  format,
}: {
  title: string;
  data: IORow[];
  readKey: keyof IORow;
  writeKey: keyof IORow;
  format: (value: number) => string;
}) {
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 pb-2 text-xs text-muted-foreground">
        <span className="font-medium">{title}</span>
        <LegendKey solid>Read</LegendKey>
        <LegendKey>Write</LegendKey>
      </div>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart
            data={data}
            margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
          >
            <CartesianGrid
              strokeDasharray="3 4"
              className="stroke-border"
              vertical={false}
            />
            <XAxis
              dataKey="time"
              tick={AXIS_TICK}
              axisLine={false}
              tickLine={false}
              minTickGap={32}
            />
            <YAxis
              tick={AXIS_TICK}
              tickFormatter={(v: number) => unbroken(format(v))}
              axisLine={false}
              tickLine={false}
              width={64}
            />
            <Tooltip
              formatter={(value: unknown, name: unknown) => [
                format(typeof value === "number" ? value : Number(value ?? 0)),
                typeof name === "string" ? name : "",
              ]}
              cursor={TOOLTIP_CURSOR}
              contentStyle={TOOLTIP_STYLE}
            />
            <Area
              type="monotone"
              dataKey={readKey}
              name="Read"
              stroke={DATA_COLOR}
              strokeWidth={2}
              fill={DATA_COLOR}
              fillOpacity={0.14}
              isAnimationActive={false}
              activeDot={{ r: 3, strokeWidth: 0 }}
            />
            <Area
              type="monotone"
              dataKey={writeKey}
              name="Write"
              stroke={SECOND_SERIES_COLOR}
              strokeWidth={1.5}
              strokeDasharray={SECOND_SERIES_DASH}
              fill="none"
              isAnimationActive={false}
              activeDot={{ r: 3, strokeWidth: 0 }}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function LegendKey({
  solid = false,
  children,
}: {
  solid?: boolean;
  children: ReactNode;
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        aria-hidden="true"
        className={
          solid
            ? "h-0.5 w-3.5 rounded-full bg-data"
            : "w-3.5 border-t-2 border-dashed border-muted-foreground"
        }
      />
      {children}
    </span>
  );
}

/**
 * A datastore's read and write activity over a window, from the Proxmox
 * Backup Server's own RRD: the averages as one strip, then transfer rate and
 * IOPS side by side. The panel it sits in owns the window.
 */
export function DatastoreIOView({
  pbsId,
  store,
  timeframe,
}: DatastoreIOViewProps) {
  const {
    data: entries,
    isLoading,
    isError,
  } = usePBSDatastoreRRD(pbsId, store, timeframe);

  const rrdData = entries ?? [];
  if (rrdData.length === 0) {
    // A read that failed is not a server with nothing to report.
    return (
      <p className="grid h-48 place-items-center text-sm text-muted-foreground">
        {isLoading
          ? "Loading I/O..."
          : isError
            ? "I/O could not be loaded from the server."
            : "The server reported no I/O for this window."}
      </p>
    );
  }

  const avgs = computeAverages(rrdData);

  const chartData: IORow[] = rrdData.map((e) => ({
    time: formatTime(e.time, timeframe),
    readBytes: e.read_bytes ?? 0,
    writeBytes: e.write_bytes ?? 0,
    readIOs: e.read_ios ?? 0,
    writeIOs: e.write_ios ?? 0,
  }));

  return (
    <div className="space-y-5">
      {avgs && (
        // Pulled out to the panel's edges, as the strip above it runs.
        <StatStrip className="-mx-[18px]">
          <Stat
            label="Average read"
            value={formatBytesPerSecond(avgs.avgReadBytes)}
            tone={avgs.avgReadBytes === 0 ? "quiet" : "default"}
          />
          <Stat
            label="Average write"
            value={formatBytesPerSecond(avgs.avgWriteBytes)}
            tone={avgs.avgWriteBytes === 0 ? "quiet" : "default"}
          />
          <Stat
            label="Average read IOPS"
            value={formatIOPS(avgs.avgReadIOs)}
            tone={avgs.avgReadIOs === 0 ? "quiet" : "default"}
          />
          <Stat
            label="Average write IOPS"
            value={formatIOPS(avgs.avgWriteIOs)}
            tone={avgs.avgWriteIOs === 0 ? "quiet" : "default"}
          />
        </StatStrip>
      )}

      <div className="grid gap-6 sm:grid-cols-2">
        <ReadWriteChart
          title="Transfer rate"
          data={chartData}
          readKey="readBytes"
          writeKey="writeBytes"
          format={formatBytesPerSecond}
        />
        <ReadWriteChart
          title="IOPS"
          data={chartData}
          readKey="readIOs"
          writeKey="writeIOs"
          format={formatIOPS}
        />
      </div>
    </div>
  );
}
