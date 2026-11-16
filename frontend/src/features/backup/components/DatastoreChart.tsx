import { useId, useMemo, useState } from "react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from "recharts";
import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
import { usePBSDatastoreMetrics } from "../api/backup-queries";
import {
  pivotByDatastore,
  datastoreSeriesKey,
  datastoreSeriesColor,
  datastoreSeriesDash,
  type DatastoreUsageRow,
} from "./datastore-usage-series";
import {
  AXIS_TICK,
  TOOLTIP_CURSOR,
  TOOLTIP_STYLE,
  unbroken,
} from "./chart-style";
import { USAGE_TIMEFRAMES, type UsageTimeframe } from "./datastore-timeframes";
import { DATA_COLOR } from "@/lib/chart-colors";
import {
  formatAxisClock,
  formatAxisDay,
  formatBytes,
  formatExactDateTime,
} from "@/lib/format";

// Both branches are module-level functions (lib/format.ts), so the
// tickFormatter prop keeps a stable identity across renders instead of handing
// Recharts a fresh closure every time.

// A clock reading repeated across seven days names no point in particular, so
// the longer windows label by date instead — the same split DatastoreIOChart
// already makes.
function axisFormatter(timeframe: UsageTimeframe): (ts: number) => string {
  return timeframe === "7d" ? formatAxisDay : formatAxisClock;
}

/**
 * Usage samples for a window, pivoted to one series per datastore: every
 * store on the server, or just `store`.
 */
function useDatastoreUsage(
  pbsId: string,
  timeframe: UsageTimeframe,
  store?: string,
) {
  const {
    data: metrics,
    isLoading,
    isError,
  } = usePBSDatastoreMetrics(pbsId, timeframe);

  // Keyed on the raw metrics, so an unrelated parent re-render no longer
  // re-derives the whole series. (A timeframe change swaps the query key, so
  // `metrics` is genuinely new and this recomputes — as it should.) The
  // timestamps stay numeric and are formatted at the axis, which means only
  // the ticks actually drawn pay for formatting, not every point.
  const { rows, datastores } = useMemo(
    () =>
      pivotByDatastore(
        store === undefined
          ? (metrics ?? [])
          : (metrics ?? []).filter((m) => m.datastore === store),
      ),
    [metrics, store],
  );
  return { rows, datastores, isLoading, isError };
}

function UsageAreaChart({
  rows,
  datastores,
  timeframe,
}: {
  rows: DatastoreUsageRow[];
  datastores: string[];
  timeframe: UsageTimeframe;
}) {
  // A lone series fades out under its line, as the metric charts do. Unique
  // per chart: several datastores each draw one on the same page.
  const gradientId = useId();
  return (
    <div className="h-64">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart
          data={rows}
          margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
        >
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={DATA_COLOR} stopOpacity={0.25} />
              <stop offset="95%" stopColor={DATA_COLOR} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid
            strokeDasharray="3 4"
            className="stroke-border"
            vertical={false}
          />
          {/* A time axis rather than the default category one, so a gap
              in collection reads as a gap instead of being closed up —
              bucketing emits no row for an empty bucket. The freeze this
              page had was fixed by bucketing server-side, not here.
              minTickGap thins the labels actually drawn. */}
          <XAxis
            dataKey="timestamp"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            tickFormatter={axisFormatter(timeframe)}
            minTickGap={56}
            tick={AXIS_TICK}
            axisLine={false}
            tickLine={false}
          />
          <YAxis
            tick={AXIS_TICK}
            tickFormatter={(v: number) => unbroken(formatBytes(v))}
            axisLine={false}
            tickLine={false}
            width={64}
          />
          {/* labelFormatter is not optional now that the X dataKey is
              epoch milliseconds rather than a pre-formatted string:
              Recharts renders the tooltip header verbatim and never
              consults the axis tickFormatter, so without this the header
              reads "1757116800000". */}
          <Tooltip
            labelFormatter={(label) => formatExactDateTime(Number(label))}
            formatter={(value: unknown) =>
              typeof value === "number" ? formatBytes(value) : "0"
            }
            cursor={TOOLTIP_CURSOR}
            contentStyle={TOOLTIP_STYLE}
          />
          {/* One series per datastore. A legend only earns its space when
              there is more than one to tell apart, and the fills are
              lightened in that case so overlapping stores stay readable —
              they are deliberately not stacked, which would imply a
              combined total the data does not claim. */}
          {datastores.length > 1 && <Legend wrapperStyle={{ fontSize: 11 }} />}
          {datastores.map((store, i) => (
            <Area
              key={store}
              type="monotone"
              dataKey={datastoreSeriesKey(store)}
              name={store}
              stroke={
                datastores.length > 1 ? datastoreSeriesColor(i) : DATA_COLOR
              }
              strokeWidth={2}
              strokeDasharray={datastoreSeriesDash(i)}
              fill={
                datastores.length > 1
                  ? datastoreSeriesColor(i)
                  : `url(#${gradientId})`
              }
              fillOpacity={datastores.length > 1 ? 0.1 : 1}
              isAnimationActive={false}
              activeDot={{ r: 3, strokeWidth: 0 }}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

/**
 * One datastore's used space over a window: the Usage view of its panel,
 * which owns the window.
 */
export function DatastoreUsageView({
  pbsId,
  store,
  timeframe,
}: {
  pbsId: string;
  store: string;
  timeframe: UsageTimeframe;
}) {
  const { rows, datastores, isLoading, isError } = useDatastoreUsage(
    pbsId,
    timeframe,
    store,
  );
  if (rows.length === 0) {
    // A read that failed is not a window with nothing recorded.
    return (
      <p className="grid h-64 place-items-center text-sm text-muted-foreground">
        {isLoading
          ? "Loading usage..."
          : isError
            ? "Usage could not be loaded."
            : "No usage has been recorded in this window yet."}
      </p>
    );
  }
  return (
    <UsageAreaChart rows={rows} datastores={datastores} timeframe={timeframe} />
  );
}

interface DatastoreChartProps {
  pbsId: string;
}

/**
 * Every datastore of a server on one chart, to compare them. Renders nothing
 * until there is a sample to draw.
 */
export function DatastoreChart({ pbsId }: DatastoreChartProps) {
  const [timeframe, setTimeframe] = useState<UsageTimeframe>("24h");
  const { rows, datastores, isLoading } = useDatastoreUsage(pbsId, timeframe);

  if (isLoading || rows.length === 0) {
    return null;
  }

  return (
    <Panel
      title="Usage by datastore"
      actions={
        <Segmented
          aria-label="Time range"
          value={timeframe}
          onValueChange={setTimeframe}
          options={USAGE_TIMEFRAMES.map((tf) => ({ value: tf, label: tf }))}
        />
      }
    >
      <UsageAreaChart
        rows={rows}
        datastores={datastores}
        timeframe={timeframe}
      />
    </Panel>
  );
}
