import { useState } from "react";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCephMetrics } from "../api/ceph-queries";

interface CephMetricsChartProps {
  clusterId: string;
}

const TIMEFRAMES = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
] as const;

function formatTime(ts: string): string {
  const d = new Date(ts);
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

export function CephMetricsChart({ clusterId }: CephMetricsChartProps) {
  const [timeframe, setTimeframe] = useState("1h");
  const metricsQuery = useCephMetrics(clusterId, timeframe);
  const metrics = metricsQuery.data ?? [];

  const chartData = metrics.map((m) => ({
    time: formatTime(m.time),
    readOps: m.read_ops_sec,
    writeOps: m.write_ops_sec,
    readBytes: m.read_bytes_sec,
    writeBytes: m.write_bytes_sec,
  }));

  return (
    <div className="space-y-4">
      <div className="flex gap-1">
        {TIMEFRAMES.map((tf) => (
          <button
            key={tf.value}
            onClick={() => {
              setTimeframe(tf.value);
            }}
            className={`rounded-md px-3 py-1 text-xs font-medium transition-colors ${
              timeframe === tf.value
                ? "bg-primary text-primary-foreground"
                : "bg-muted text-muted-foreground hover:bg-accent"
            }`}
          >
            {tf.label}
          </button>
        ))}
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">IOPS</CardTitle>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                <YAxis tick={{ fontSize: 10 }} />
                <Tooltip />
                <Legend />
                <Line
                  type="monotone"
                  dataKey="readOps"
                  name="Read IOPS"
                  stroke="hsl(var(--chart-1))"
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="writeOps"
                  name="Write IOPS"
                  stroke="hsl(var(--chart-2))"
                  strokeWidth={2}
                  dot={false}
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Throughput</CardTitle>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                <YAxis tick={{ fontSize: 10 }} tickFormatter={formatBytes} />
                <Tooltip
                  formatter={(value: number | undefined) =>
                    value != null ? formatBytes(value) + "/s" : "0"
                  }
                />
                <Legend />
                <Line
                  type="monotone"
                  dataKey="readBytes"
                  name="Read"
                  stroke="hsl(var(--chart-3))"
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="writeBytes"
                  name="Write"
                  stroke="hsl(var(--chart-4))"
                  strokeWidth={2}
                  dot={false}
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>

      {metrics.length === 0 && !metricsQuery.isLoading && (
        <p className="text-center text-sm text-muted-foreground">
          No historical metrics available yet.
        </p>
      )}
    </div>
  );
}
