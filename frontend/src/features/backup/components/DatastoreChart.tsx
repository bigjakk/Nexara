import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { usePBSDatastoreMetrics } from "../api/backup-queries";
import type { PBSDatastoreMetric } from "../types/backup";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

interface DatastoreChartProps {
  pbsId: string;
}

export function DatastoreChart({ pbsId }: DatastoreChartProps) {
  const { data: metrics, isLoading } = usePBSDatastoreMetrics(pbsId, "24h");

  if (isLoading) {
    return null;
  }

  const metricsList: PBSDatastoreMetric[] = metrics ?? [];

  if (metricsList.length === 0) {
    return null;
  }

  const chartData = metricsList.map((m) => ({
    time: new Date(m.time).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    }),
    used: m.used,
    total: m.total,
    datastore: m.datastore,
  }));

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">
          Datastore Usage (24h)
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
              <XAxis
                dataKey="time"
                className="text-xs"
                tick={{ fontSize: 11 }}
              />
              <YAxis
                className="text-xs"
                tick={{ fontSize: 11 }}
                tickFormatter={(v: number) => formatBytes(v)}
              />
              <Tooltip
                formatter={(value: number | undefined) =>
                  value != null ? formatBytes(value) : "0"
                }
              />
              <Area
                type="monotone"
                dataKey="used"
                name="Used"
                stroke="hsl(var(--primary))"
                fill="hsl(var(--primary))"
                fillOpacity={0.2}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}
