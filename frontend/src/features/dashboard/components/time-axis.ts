import type { XAxisProps } from "recharts";
import {
  formatTimestamp,
  formatTimestampShort,
  formatTimestampLong,
} from "@/lib/format";
import type { TimeRange } from "@/types/api";

export function getTimestampFormatter(
  timeRange?: TimeRange,
): (ts: number) => string {
  if (timeRange === "7d") return formatTimestampLong;
  if (timeRange === "1h" || timeRange === "6h" || timeRange === "24h")
    return formatTimestampShort;
  return formatTimestamp;
}

/**
 * X-axis props for a metric time series. The scale is numeric time, so points
 * sit at their timestamps: a gap in sampling, or uneven spacing, shows as
 * such instead of every point getting an equal slot.
 */
export function timeAxisProps(timeRange?: TimeRange): XAxisProps {
  return {
    dataKey: "timestamp",
    type: "number",
    scale: "time",
    domain: ["dataMin", "dataMax"],
    tickFormatter: getTimestampFormatter(timeRange),
    tick: { fontSize: 10, fill: "hsl(var(--muted-foreground))" },
    axisLine: false,
    tickLine: false,
    minTickGap: 48,
    tickMargin: 6,
  };
}
