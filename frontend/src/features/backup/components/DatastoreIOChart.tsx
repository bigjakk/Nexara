import { useState } from "react";
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
import { usePBSDatastoreRRD } from "../api/backup-queries";
import type { PBSDatastoreRRDEntry } from "../types/backup";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B/s", "KB/s", "MB/s", "GB/s"];
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

function formatIOPS(value: number): string {
  if (value < 1) return value.toFixed(2);
  if (value < 100) return value.toFixed(1);
  if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
  return Math.round(value).toString();
}

function formatTime(ts: number, timeframe: string): string {
  const d = new Date(ts * 1000);
  if (timeframe === "week" || timeframe === "month") {
    return d.toLocaleDateString([], { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

const TIMEFRAMES = [
  { value: "hour", label: "1h" },
  { value: "day", label: "24h" },
  { value: "week", label: "7d" },
  { value: "month", label: "30d" },
] as const;

type RRDTimeframe = (typeof TIMEFRAMES)[number]["value"];

interface DatastoreIOChartProps {
  pbsId: string;
  store: string;
}

function computeAverages(entries: PBSDatastoreRRDEntry[]) {
  let readSum = 0;
  let writeSum = 0;
  let readIOSum = 0;
  let writeIOSum = 0;
  let count = 0;

  for (const e of entries) {
    if (
      e.read_bytes != null ||
      e.write_bytes != null ||
      e.read_ios != null ||
      e.write_ios != null
    ) {
      readSum += e.read_bytes ?? 0;
      writeSum += e.write_bytes ?? 0;
      readIOSum += e.read_ios ?? 0;
      writeIOSum += e.write_ios ?? 0;
      count++;
    }
  }

  if (count === 0) return null;

  return {
    avgReadBytes: readSum / count,
    avgWriteBytes: writeSum / count,
    avgReadIOs: readIOSum / count,
    avgWriteIOs: writeIOSum / count,
  };
}

export function DatastoreIOChart({ pbsId, store }: DatastoreIOChartProps) {
  const [timeframe, setTimeframe] = useState<RRDTimeframe>("hour");
  const { data: entries, isLoading } = usePBSDatastoreRRD(
    pbsId,
    store,
    timeframe,
  );

  if (isLoading) return null;

  const rrdData = entries ?? [];
  if (rrdData.length === 0) return null;

  const avgs = computeAverages(rrdData);

  const chartData = rrdData.map((e) => ({
    time: formatTime(e.time, timeframe),
    readBytes: e.read_bytes ?? 0,
    writeBytes: e.write_bytes ?? 0,
    readIOs: e.read_ios ?? 0,
    writeIOs: e.write_ios ?? 0,
  }));

  return (
    <div className="space-y-4">
      {/* Averages summary */}
      {avgs && (
        <div className="grid grid-cols-4 gap-3">
          <StatCard label="Avg Read" value={formatBytes(avgs.avgReadBytes)} />
          <StatCard label="Avg Write" value={formatBytes(avgs.avgWriteBytes)} />
          <StatCard label="Avg Read IOPS" value={formatIOPS(avgs.avgReadIOs)} />
          <StatCard label="Avg Write IOPS" value={formatIOPS(avgs.avgWriteIOs)} />
        </div>
      )}

      {/* Transfer Rate + IOPS side by side */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium">Transfer Rate</CardTitle>
            <TimeframePills value={timeframe} onChange={setTimeframe} />
          </CardHeader>
          <CardContent>
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData}>
                  <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
                  <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                  <YAxis tick={{ fontSize: 10 }} tickFormatter={(v: number) => formatBytes(v)} />
                  <Tooltip
                    formatter={(value: number | undefined, name: string | undefined) => [
                      formatBytes(value ?? 0),
                      name ?? "",
                    ]}
                  />
                  <Legend wrapperStyle={{ fontSize: 11 }} />
                  <Area
                    type="monotone"
                    dataKey="readBytes"
                    name="Read"
                    stroke="hsl(var(--chart-1, 220 70% 50%))"
                    fill="hsl(var(--chart-1, 220 70% 50%))"
                    fillOpacity={0.15}
                  />
                  <Area
                    type="monotone"
                    dataKey="writeBytes"
                    name="Write"
                    stroke="hsl(var(--chart-2, 160 60% 45%))"
                    fill="hsl(var(--chart-2, 160 60% 45%))"
                    fillOpacity={0.15}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium">IOPS</CardTitle>
            <TimeframePills value={timeframe} onChange={setTimeframe} />
          </CardHeader>
          <CardContent>
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData}>
                  <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
                  <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                  <YAxis tick={{ fontSize: 10 }} tickFormatter={(v: number) => formatIOPS(v)} />
                  <Tooltip
                    formatter={(value: number | undefined, name: string | undefined) => [
                      formatIOPS(value ?? 0),
                      name ?? "",
                    ]}
                  />
                  <Legend wrapperStyle={{ fontSize: 11 }} />
                  <Area
                    type="monotone"
                    dataKey="readIOs"
                    name="Read IOPS"
                    stroke="hsl(var(--chart-3, 30 80% 55%))"
                    fill="hsl(var(--chart-3, 30 80% 55%))"
                    fillOpacity={0.15}
                  />
                  <Area
                    type="monotone"
                    dataKey="writeIOs"
                    name="Write IOPS"
                    stroke="hsl(var(--chart-4, 280 65% 60%))"
                    fill="hsl(var(--chart-4, 280 65% 60%))"
                    fillOpacity={0.15}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-card p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-lg font-semibold">{value}</p>
    </div>
  );
}

function TimeframePills({
  value,
  onChange,
}: {
  value: RRDTimeframe;
  onChange: (v: RRDTimeframe) => void;
}) {
  return (
    <div className="flex gap-1">
      {TIMEFRAMES.map((tf) => (
        <button
          key={tf.value}
          onClick={() => {
            onChange(tf.value);
          }}
          className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
            value === tf.value
              ? "bg-primary text-primary-foreground"
              : "bg-muted text-muted-foreground hover:bg-accent"
          }`}
        >
          {tf.label}
        </button>
      ))}
    </div>
  );
}
