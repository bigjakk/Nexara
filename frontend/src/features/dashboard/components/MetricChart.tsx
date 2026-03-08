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

  const tooltipValueFormatter = (
    value: number | string | Array<number | string> | undefined,
  ): [string, string] => {
    return [formatter(Number(value ?? 0)), title];
  };

  if (data.length === 0) {
    return (
      <Card className="flex h-full flex-col">
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-1 items-center justify-center">
          <div className="text-sm text-muted-foreground">
            Waiting for data...
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="flex h-full flex-col">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex-1 min-h-0">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data}>
            <defs>
              <linearGradient id={`gradient-${dataKey}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={color} stopOpacity={0.3} />
                <stop offset="95%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
            <XAxis
              dataKey="timestamp"
              tickFormatter={getTimestampFormatter(timeRange)}
              className="text-xs"
              tick={{ fontSize: 10 }}
            />
            <YAxis
              tickFormatter={formatter}
              className="text-xs"
              tick={{ fontSize: 10 }}
              width={60}
            />
            <Tooltip
              labelFormatter={tooltipLabelFormatter}
              formatter={tooltipValueFormatter}
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
              stroke={color}
              fill={`url(#gradient-${dataKey})`}
              strokeWidth={2}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}
