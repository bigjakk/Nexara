import type { ReactNode } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getTimestampFormatter, timeAxisProps } from "@/features/dashboard/components/time-axis";
import type { UpsMetricPoint, UpsRange } from "../types/ups";
import { formatRuntime, onBatteryRuns } from "../lib/ups-state";

interface Series {
  key: keyof UpsMetricPoint;
  label: string;
  color: string;
  axis?: "left" | "right";
}

function UpsChart({
  title,
  subtitle,
  points,
  bucketMs,
  series,
  range,
  left,
  right,
}: {
  title: string;
  subtitle: string;
  points: UpsMetricPoint[];
  bucketMs: number;
  series: Series[];
  range: UpsRange;
  left: (v: number) => string;
  right?: (v: number) => string;
}) {
  const ts = getTimestampFormatter(range);
  const fmt: Record<string, (v: number) => string> = {};
  for (const s of series) fmt[s.key] = s.axis === "right" && right ? right : left;
  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        <p className="text-xs text-muted-foreground">{subtitle}</p>
      </CardHeader>
      <CardContent className="h-56 p-2 pt-0">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={points} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
            <CartesianGrid strokeDasharray="3 4" className="stroke-border" vertical={false} />
            {onBatteryRuns(points, bucketMs).map(([x1, x2]) => (
              <ReferenceArea key={x1} x1={x1} x2={x2} yAxisId="left" fill="#f59e0b" fillOpacity={0.12} ifOverflow="extendDomain" />
            ))}
            <XAxis {...timeAxisProps(range)} />
            <YAxis
              yAxisId="left"
              tickFormatter={left}
              tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              width={52}
              domain={[0, "auto"]}
            />
            {right !== undefined && (
              <YAxis
                yAxisId="right"
                orientation="right"
                tickFormatter={right}
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                axisLine={false}
                tickLine={false}
                width={52}
                domain={[0, "auto"]}
              />
            )}
            <Tooltip
              labelFormatter={(label: ReactNode) => ts(Number(label))}
              formatter={(value: unknown, name: unknown) => {
                const key = String(name);
                const s = series.find((x) => x.key === key);
                return [typeof value === "number" ? (fmt[key] ?? left)(value) : "—", s?.label ?? key];
              }}
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "8px",
                fontSize: "12px",
              }}
            />
            {series.map((s) => (
              <Line
                key={s.key}
                yAxisId={s.axis ?? "left"}
                type="monotone"
                dataKey={s.key}
                name={s.key}
                stroke={s.color}
                strokeWidth={2}
                dot={false}
                isAnimationActive={false}
                connectNulls={false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
      </CardContent>
      <div className="flex flex-wrap gap-4 px-4 pb-3 text-xs">
        {series.map((s) => (
          <span key={s.key} className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: s.color }} aria-hidden="true" />
            {s.label}
          </span>
        ))}
        <span className="flex items-center gap-1.5 text-muted-foreground">
          <span className="h-2 w-3 rounded-sm bg-amber-500/30" aria-hidden="true" />
          On battery
        </span>
      </div>
    </Card>
  );
}

const pct = (v: number) => `${String(Math.round(v))}%`;
const volts = (v: number) => `${String(Math.round(v))} V`;

/**
 * Charge and runtime, then input against output voltage: the gap between the
 * two voltage lines is the UPS doing its job. Each bucket is its worst value
 * (lowest charge, runtime and input; highest load), so a short dip shows.
 */
export function UpsCharts({
  points,
  range,
  bucketSeconds,
}: {
  points: UpsMetricPoint[];
  range: UpsRange;
  bucketSeconds: number;
}) {
  const bucketMs = bucketSeconds * 1000;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <UpsChart
        title="Battery charge and runtime"
        subtitle="Lowest value in each bucket · shaded where the UPS was on battery"
        points={points}
        bucketMs={bucketMs}
        range={range}
        left={pct}
        right={(v) => formatRuntime(v)}
        series={[
          { key: "charge", label: "Charge", color: "hsl(var(--primary))" },
          { key: "runtime", label: "Runtime", color: "#3b82f6", axis: "right" },
        ]}
      />
      <UpsChart
        title="Input and output voltage"
        subtitle="Input's low and high in each bucket, against the output the UPS holds"
        points={points}
        bucketMs={bucketMs}
        range={range}
        left={volts}
        series={[
          { key: "input_voltage_min", label: "Input (low)", color: "#a855f7" },
          { key: "input_voltage_max", label: "Input (high)", color: "#f97316" },
          { key: "output_voltage", label: "Output", color: "hsl(var(--primary))" },
        ]}
      />
      <UpsChart
        title="Load"
        subtitle="Highest load in each bucket"
        points={points}
        bucketMs={bucketMs}
        range={range}
        left={pct}
        series={[{ key: "load", label: "Load", color: "#3b82f6" }]}
      />
    </div>
  );
}
