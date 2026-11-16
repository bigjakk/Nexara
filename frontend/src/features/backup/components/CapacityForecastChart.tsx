import { useId } from "react";
import {
  ResponsiveContainer,
  ComposedChart,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ReferenceLine,
} from "recharts";
import { DATA_COLOR } from "@/lib/chart-colors";
import {
  formatAxisDay,
  formatBytes,
  formatExactDate,
  formatExactDateTime,
} from "@/lib/format";
import {
  FORECAST_HORIZON_DAYS,
  type CapacityForecast,
} from "../lib/datastore-facts";
import {
  AXIS_TICK,
  SECOND_SERIES_COLOR,
  SECOND_SERIES_DASH,
  TOOLTIP_CURSOR,
  TOOLTIP_STYLE,
  unbroken,
} from "./chart-style";

function formatDate(ts: number): string {
  return formatAxisDay(ts);
}

/**
 * A week of a datastore's usage and the straight line through it, drawn out
 * to capacity or the horizon. The panel it sits in reads the samples, since
 * its Free figure quotes the same projection.
 */
export function CapacityForecastView({
  forecast,
  query,
}: {
  forecast: CapacityForecast;
  /** How the read of the samples behind it stands. */
  query: { isLoading: boolean; isError: boolean };
}) {
  const { points, fullDate, totalCapacity } = forecast;
  // Unique per chart: several datastores each draw one on the same page.
  const gradientId = useId();

  if (points.length === 0) {
    return (
      <p className="grid h-64 place-items-center text-sm text-muted-foreground">
        {query.isLoading
          ? "Loading usage..."
          : query.isError
            ? "Usage could not be loaded, so there is nothing to project."
            : "A forecast needs a few more usage samples than have been recorded."}
      </p>
    );
  }

  return (
    <div>
      <p className="pb-2 text-[12.5px] text-muted-foreground">
        {fullDate ? (
          <span className="font-medium text-red-700 dark:text-red-400">
            Projected full: {formatExactDate(fullDate)}
          </span>
        ) : (
          `No capacity issue projected (${String(FORECAST_HORIZON_DAYS)}d)`
        )}
        {". "}A straight line through the last 7 days of usage.
      </p>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart
            data={points}
            margin={{ top: 12, right: 16, bottom: 0, left: 0 }}
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
            {/* A time axis, not the default category axis — and here it is
                a correctness fix, not the performance one. This series splices
                bucketed history onto 6-hourly forecast steps, and a category
                axis spaces every point equally regardless of the time it
                stands for, so the ~360 forecast points covering 90 days were
                squeezed into a sliver while a week of history took nearly the
                whole width. (The freeze was fixed by bucketing the query
                server-side; a number axis still maps one tick candidate per
                datum.) minTickGap thins the labels actually drawn. */}
            <XAxis
              dataKey="timestamp"
              type="number"
              scale="time"
              domain={["dataMin", "dataMax"]}
              tickFormatter={formatDate}
              minTickGap={56}
              tick={AXIS_TICK}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              tickFormatter={(v: number) => unbroken(formatBytes(v))}
              tick={AXIS_TICK}
              axisLine={false}
              tickLine={false}
              width={64}
            />
            <Tooltip
              labelFormatter={(label) =>
                formatExactDateTime(Number(label))
              }
              formatter={(value: unknown, name: unknown) => [
                formatBytes(
                  typeof value === "number" ? value : Number(value ?? 0),
                ),
                name === "used"
                  ? "Used"
                  : name === "forecast"
                    ? "Forecast"
                    : "Total",
              ]}
              cursor={TOOLTIP_CURSOR}
              contentStyle={TOOLTIP_STYLE}
            />
            {totalCapacity > 0 && (
              <ReferenceLine
                y={totalCapacity}
                stroke="hsl(var(--foreground))"
                strokeOpacity={0.45}
                strokeDasharray="2 4"
                label={{
                  value: "Capacity",
                  position: "insideTopRight",
                  fontSize: 10,
                  fill: "hsl(var(--muted-foreground))",
                }}
              />
            )}
            <Area
              type="monotone"
              dataKey="used"
              stroke={DATA_COLOR}
              fill={`url(#${gradientId})`}
              strokeWidth={2}
              isAnimationActive={false}
              connectNulls={false}
            />
            <Line
              type="monotone"
              dataKey="forecast"
              stroke={fullDate ? "hsl(0 72% 51%)" : SECOND_SERIES_COLOR}
              strokeWidth={2}
              strokeDasharray={SECOND_SERIES_DASH}
              dot={false}
              isAnimationActive={false}
              connectNulls={false}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}
