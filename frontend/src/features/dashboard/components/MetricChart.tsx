import type { ReactNode } from "react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatTimestamp, formatTimestampShort, formatTimestampLong, formatPercent, formatBytesPerSecond } from "@/lib/format";
import type { MetricDataPoint } from "@/types/ws";
import type { TimeRange } from "@/types/api";

type MetricField = "cpuPercent" | "memPercent" | "diskReadBps" | "diskWriteBps" | "netInBps" | "netOutBps";

interface MetricChartProps {
  title: string;
  data: MetricDataPoint[];
  dataKey: MetricField;
  color: string;
  formatValue?: (value: number) => string;
  timeRange?: TimeRange;
}

function getTimestampFormatter(timeRange?: TimeRange): (ts: number) => string {
  if (timeRange === "7d") return formatTimestampLong;
  if (timeRange === "1h" || timeRange === "6h" || timeRange === "24h") return formatTimestampShort;
  return formatTimestamp;
}

function ChartHeader({
  title,
  color,
  currentValue,
}: {
  title: string;
  color: string;
  currentValue?: string | undefined;
}) {
  return (
    <CardHeader className="flex-row items-center gap-2 space-y-0 p-4 pb-2">
      <span
        className="h-2 w-2 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
      />
      <CardTitle className="truncate text-xs font-medium text-muted-foreground">
        {title}
      </CardTitle>
      {currentValue !== undefined && (
        <span className="ml-auto shrink-0 text-base font-semibold tabular-nums tracking-tight">
          {currentValue}
        </span>
      )}
    </CardHeader>
  );
}

export function MetricChart({
  title,
  data,
  dataKey,
  color,
  formatValue,
  timeRange,
}: MetricChartProps) {
  const formatter = formatValue ?? ((v: number) => {
    if (dataKey === "cpuPercent" || dataKey === "memPercent") {
      return formatPercent(v);
    }
    return formatBytesPerSecond(v);
  });

  const tsFormatter = getTimestampFormatter(timeRange);
  const tooltipLabelFormatter = (label: ReactNode): ReactNode => {
    return tsFormatter(Number(label));
  };

  const tooltipValueFormatter = (value: unknown): [string, string] => {
    return [formatter(typeof value === "number" ? value : Number(value ?? 0)), title];
  };

  if (data.length === 0) {
    return (
      <Card className="flex h-full flex-col">
        <ChartHeader title={title} color={color} />
        <CardContent className="flex flex-1 items-center justify-center">
          <div className="text-sm text-muted-foreground">
            Waiting for data...
          </div>
        </CardContent>
      </Card>
    );
  }

  const lastPoint = data[data.length - 1];
  const currentValue =
    lastPoint !== undefined ? formatter(lastPoint[dataKey]) : undefined;

  return (
    <Card className="flex h-full flex-col">
      <ChartHeader title={title} color={color} currentValue={currentValue} />
      <CardContent className="min-h-0 flex-1 p-2 pt-0">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
            <defs>
              <linearGradient id={`gradient-${dataKey}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={color} stopOpacity={0.25} />
                <stop offset="95%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid
              strokeDasharray="3 4"
              className="stroke-border"
              vertical={false}
            />
            <XAxis
              dataKey="timestamp"
              tickFormatter={getTimestampFormatter(timeRange)}
              tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              minTickGap={48}
              tickMargin={6}
            />
            <YAxis
              tickFormatter={formatter}
              tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              width={52}
            />
            <Tooltip
              labelFormatter={tooltipLabelFormatter}
              formatter={tooltipValueFormatter}
              cursor={{ stroke: "hsl(var(--muted-foreground))", strokeOpacity: 0.4 }}
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
              stroke={color}
              fill={`url(#gradient-${dataKey})`}
              strokeWidth={2}
              isAnimationActive={false}
              activeDot={{ r: 3, strokeWidth: 0 }}
            />
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}
