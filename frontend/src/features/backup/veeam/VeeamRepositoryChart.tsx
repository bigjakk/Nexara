import { useState } from "react";
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
import { formatBytes } from "@/lib/format";
import { useVeeamRepositoryMetrics } from "../api/backup-queries";
import type { VeeamRepository } from "../types/backup";

const RANGES = ["24h", "7d", "30d", "90d"] as const;
type Range = (typeof RANGES)[number];

interface VeeamRepositoryChartProps {
  serverId: string;
  repository: VeeamRepository;
}

/**
 * Used space over time for one repository.
 *
 * Keyed on the repository's VEEAM id, not Nexara's row id: the samples are
 * stored against Veeam's identity so they survive the local row being
 * re-created.
 */
export function VeeamRepositoryChart({
  serverId,
  repository,
}: VeeamRepositoryChartProps) {
  const [range, setRange] = useState<Range>("7d");
  const { data, isLoading } = useVeeamRepositoryMetrics(
    serverId,
    repository.veeam_id,
    range,
  );

  const metrics = data ?? [];

  // Nothing to plot yet — the collector writes one sample per inventory pass,
  // so a freshly added server has none. Rendering an empty axis would look
  // like a broken chart rather than a young one.
  if (isLoading || metrics.length === 0) {
    return null;
  }

  const chartData = metrics.map((m) => ({
    time: new Date(m.time).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
    used: m.used_bytes,
    capacity: m.capacity_bytes,
  }));

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-sm font-medium">
          {repository.name} — used space
        </CardTitle>
        <div className="flex gap-1">
          {RANGES.map((r) => (
            <button
              key={r}
              onClick={() => {
                setRange(r);
              }}
              className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                range === r
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {r}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        <ResponsiveContainer width="100%" height={200}>
          <AreaChart data={chartData}>
            <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
            <XAxis
              dataKey="time"
              tick={{ fontSize: 11 }}
              className="fill-muted-foreground"
            />
            <YAxis
              tickFormatter={(v: number) => formatBytes(v)}
              tick={{ fontSize: 11 }}
              className="fill-muted-foreground"
              width={70}
            />
            <Tooltip
              formatter={(value: unknown) =>
                typeof value === "number" ? formatBytes(value) : "0"
              }
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "0.5rem",
                fontSize: "0.75rem",
              }}
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
      </CardContent>
    </Card>
  );
}
