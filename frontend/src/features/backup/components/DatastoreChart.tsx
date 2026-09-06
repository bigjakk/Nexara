import { useMemo, useState } from "react";
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { usePBSDatastoreMetrics } from "../api/backup-queries";
import {
  pivotByDatastore,
  datastoreSeriesKey,
  datastoreSeriesColor,
  datastoreSeriesDash,
} from "./datastore-usage-series";
import { formatBytes } from "@/lib/format";

const TIMEFRAMES = ["1h", "6h", "24h", "7d"] as const;
type Timeframe = (typeof TIMEFRAMES)[number];

// Built once. Constructing an Intl formatter dwarfs the cost of using one.
const CLOCK_FMT = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
});
const DAY_FMT = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
});

// Both branches are module-level constants, so the tickFormatter prop keeps a
// stable identity across renders instead of handing Recharts a fresh closure
// every time.
const formatAxisClock = (ts: number): string => CLOCK_FMT.format(ts);
const formatAxisDay = (ts: number): string => DAY_FMT.format(ts);

// A clock reading repeated across seven days names no point in particular, so
// the longer windows label by date instead — the same split DatastoreIOChart
// already makes.
function axisFormatter(timeframe: Timeframe): (ts: number) => string {
  return timeframe === "7d" ? formatAxisDay : formatAxisClock;
}

interface DatastoreChartProps {
  pbsId: string;
}

export function DatastoreChart({ pbsId }: DatastoreChartProps) {
  const [timeframe, setTimeframe] = useState<Timeframe>("24h");
  const { data: metrics, isLoading } = usePBSDatastoreMetrics(pbsId, timeframe);

  // Keyed on the raw metrics, so an unrelated parent re-render no longer
  // re-derives the whole series. (A timeframe pill swaps the query key, so
  // `metrics` is genuinely new and this recomputes — as it should.) The
  // timestamps stay numeric and are formatted at the axis, which means only
  // the ticks actually drawn pay for formatting, not every point.
  const { rows, datastores } = useMemo(
    () => pivotByDatastore(metrics ?? []),
    [metrics],
  );

  if (isLoading || rows.length === 0) {
    return null;
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-sm font-medium">Datastore Usage</CardTitle>
        <div className="flex gap-1">
          {TIMEFRAMES.map((tf) => (
            <button
              key={tf}
              onClick={() => {
                setTimeframe(tf);
              }}
              className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                timeframe === tf
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {tf}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
              {/* A time axis rather than the default category one, so a gap
                  in collection reads as a gap instead of being closed up —
                  bucketing emits no row for an empty bucket. The freeze this
                  page had was fixed by bucketing server-side, not here.
                  minTickGap thins the labels actually drawn, and the same tick
                  list drives CartesianGrid's vertical lines. */}
              <XAxis
                dataKey="timestamp"
                type="number"
                scale="time"
                domain={["dataMin", "dataMax"]}
                tickFormatter={axisFormatter(timeframe)}
                minTickGap={56}
                className="text-xs"
                tick={{ fontSize: 11 }}
              />
              <YAxis
                className="text-xs"
                tick={{ fontSize: 11 }}
                tickFormatter={(v: number) => formatBytes(v)}
              />
              {/* labelFormatter is not optional now that the X dataKey is
                  epoch milliseconds rather than a pre-formatted string:
                  Recharts renders the tooltip header verbatim and never
                  consults the axis tickFormatter, so without this the header
                  reads "1757116800000". */}
              <Tooltip
                labelFormatter={(label) =>
                  new Date(Number(label)).toLocaleString()
                }
                formatter={(value: unknown) =>
                  typeof value === "number" ? formatBytes(value) : "0"
                }
              />
              {/* One series per datastore. A legend only earns its space when
                  there is more than one to tell apart, and the fills are
                  lightened in that case so overlapping stores stay readable —
                  they are deliberately not stacked, which would imply a
                  combined total the data does not claim. */}
              {datastores.length > 1 && (
                <Legend wrapperStyle={{ fontSize: 11 }} />
              )}
              {datastores.map((store, i) => (
                <Area
                  key={store}
                  type="monotone"
                  dataKey={datastoreSeriesKey(store)}
                  name={store}
                  stroke={datastoreSeriesColor(i)}
                  strokeDasharray={datastoreSeriesDash(i)}
                  fill={datastoreSeriesColor(i)}
                  fillOpacity={datastores.length > 1 ? 0.12 : 0.2}
                />
              ))}
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}
