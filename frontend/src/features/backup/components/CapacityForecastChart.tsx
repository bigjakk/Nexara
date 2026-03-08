import { useMemo } from "react";
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { usePBSDatastoreMetrics } from "../api/backup-queries";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

function formatDate(ts: number): string {
  return new Date(ts).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

interface ForecastPoint {
  timestamp: number;
  used: number | null;
  forecast: number | null;
  total: number | null;
}

function linearRegression(points: { x: number; y: number }[]): { slope: number; intercept: number } | null {
  const n = points.length;
  if (n < 2) return null;

  let sumX = 0, sumY = 0, sumXY = 0, sumXX = 0;
  for (const p of points) {
    sumX += p.x;
    sumY += p.y;
    sumXY += p.x * p.y;
    sumXX += p.x * p.x;
  }

  const denom = n * sumXX - sumX * sumX;
  if (denom === 0) return null;

  const slope = (n * sumXY - sumX * sumY) / denom;
  const intercept = (sumY - slope * sumX) / n;

  return { slope, intercept };
}

interface CapacityForecastChartProps {
  pbsId: string;
  store: string;
}

export function CapacityForecastChart({ pbsId, store }: CapacityForecastChartProps) {
  const { data: metrics } = usePBSDatastoreMetrics(pbsId, "7d");

  const { chartData, fullDate, totalCapacity } = useMemo(() => {
    const metricsList = metrics ?? [];
    const storeMetrics = metricsList.filter((m) => m.datastore === store);

    if (storeMetrics.length < 3) {
      return { chartData: [] as ForecastPoint[], fullDate: null, totalCapacity: 0 };
    }

    // Get total capacity from latest metric
    const latestTotal = storeMetrics[storeMetrics.length - 1]?.total ?? 0;

    // Build regression data points
    const regressionPoints = storeMetrics.map((m) => ({
      x: new Date(m.time).getTime(),
      y: m.used,
    }));

    const reg = linearRegression(regressionPoints);

    // Build chart data with actual values
    const data: ForecastPoint[] = storeMetrics.map((m) => ({
      timestamp: new Date(m.time).getTime(),
      used: m.used,
      forecast: null,
      total: latestTotal,
    }));

    let projectedFullDate: Date | null = null;

    if (reg && reg.slope > 0) {
      // Project forward up to 90 days
      const lastTs = data[data.length - 1]?.timestamp ?? Date.now();
      const maxForecastMs = 90 * 24 * 3600 * 1000;
      const stepMs = 6 * 3600 * 1000; // 6-hour steps

      // Add the transition point (last actual value is also first forecast)
      const lastUsed = reg.slope * lastTs + reg.intercept;
      const lastEntry = data[data.length - 1];
      if (lastEntry) {
        data[data.length - 1] = {
          ...lastEntry,
          forecast: lastUsed,
        };
      }

      for (let t = lastTs + stepMs; t <= lastTs + maxForecastMs; t += stepMs) {
        const projected = reg.slope * t + reg.intercept;
        if (projected >= latestTotal) {
          projectedFullDate = new Date(t);
          data.push({
            timestamp: t,
            used: null,
            forecast: latestTotal,
            total: latestTotal,
          });
          break;
        }
        data.push({
          timestamp: t,
          used: null,
          forecast: projected,
          total: latestTotal,
        });
      }
    }

    return {
      chartData: data,
      fullDate: projectedFullDate,
      totalCapacity: latestTotal,
    };
  }, [metrics, store]);

  if (chartData.length === 0) {
    return null;
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm font-medium">
            {store} — Capacity Forecast
          </CardTitle>
          {fullDate ? (
            <span className="text-xs text-destructive font-medium">
              Projected full: {fullDate.toLocaleDateString()}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">
              No capacity issue projected (90d)
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent>
        <ResponsiveContainer width="100%" height={200}>
          <ComposedChart data={chartData}>
            <defs>
              <linearGradient id={`fg-used-${store}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.3} />
                <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
              </linearGradient>
              <linearGradient id={`fg-forecast-${store}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#f59e0b" stopOpacity={0.2} />
                <stop offset="95%" stopColor="#f59e0b" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
            <XAxis
              dataKey="timestamp"
              tickFormatter={formatDate}
              tick={{ fontSize: 10 }}
            />
            <YAxis
              tickFormatter={formatBytes}
              tick={{ fontSize: 10 }}
              width={60}
            />
            <Tooltip
              labelFormatter={(label) => new Date(Number(label)).toLocaleString()}
              formatter={(value: number | undefined, name: string | undefined) => [
                formatBytes(value ?? 0),
                name === "used" ? "Used" : name === "forecast" ? "Forecast" : "Total",
              ]}
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "6px",
                fontSize: "12px",
              }}
            />
            {totalCapacity > 0 && (
              <ReferenceLine
                y={totalCapacity}
                stroke="#ef4444"
                strokeDasharray="4 4"
                label={{ value: "Capacity", position: "right", fontSize: 10, fill: "#ef4444" }}
              />
            )}
            <Area
              type="monotone"
              dataKey="used"
              stroke="#3b82f6"
              fill={`url(#fg-used-${store})`}
              strokeWidth={2}
              isAnimationActive={false}
              connectNulls={false}
            />
            <Line
              type="monotone"
              dataKey="forecast"
              stroke="#f59e0b"
              strokeWidth={2}
              strokeDasharray="6 3"
              dot={false}
              isAnimationActive={false}
              connectNulls={false}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}
