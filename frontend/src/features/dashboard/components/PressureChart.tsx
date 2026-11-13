import type { ReactNode } from "react";
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Panel } from "@/components/page/Panel";
import { Skeleton } from "@/components/ui/skeleton";
import { DATA_COLOR } from "@/lib/chart-colors";
import { formatPercent } from "@/lib/format";
import type { PressurePoint, TimeRange } from "@/types/api";
import { getTimestampFormatter, timeAxisProps } from "./time-axis";

type PressureKey = Exclude<keyof PressurePoint, "timestamp">;

// All three draw in the data colour: green and amber are states, and each
// chart is named, so a colour per resource would say nothing true.
const RESOURCES: readonly {
  label: string;
  some: PressureKey;
  full: PressureKey;
}[] = [
  { label: "CPU", some: "cpuSome", full: "cpuFull" },
  { label: "Memory", some: "memSome", full: "memFull" },
  { label: "IO", some: "ioSome", full: "ioFull" },
];

function latest(data: readonly PressurePoint[], key: PressureKey) {
  for (let i = data.length - 1; i >= 0; i--) {
    const v = data[i]?.[key];
    if (v !== null && v !== undefined) return v;
  }
  return undefined;
}

function PressurePanel({
  data,
  label,
  some,
  full,
  timeRange,
}: {
  data: PressurePoint[];
  label: string;
  some: PressureKey;
  full: PressureKey;
  timeRange: TimeRange;
}) {
  const color = DATA_COLOR;
  const tsFormatter = getTimestampFormatter(timeRange);
  const someNow = latest(data, some);
  const fullNow = latest(data, full);
  const hasFull = fullNow !== undefined;
  return (
    <div className="min-w-0" data-testid={`pressure-${label.toLowerCase()}`}>
      <div className="mb-1 flex items-baseline gap-2">
        <span className="text-sm text-muted-foreground">{label}</span>
        <span className="ml-auto text-sm font-semibold">
          {someNow !== undefined ? formatPercent(someNow) : "—"}
          {hasFull && (
            <span className="text-[12.5px] font-normal text-muted-foreground">
              {" "}
              · full {formatPercent(fullNow)}
            </span>
          )}
        </span>
      </div>
      <div className="h-32">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart
            data={data}
            margin={{ top: 4, right: 8, bottom: 0, left: 0 }}
          >
            <CartesianGrid
              strokeDasharray="3 4"
              className="stroke-border"
              vertical={false}
            />
            <XAxis {...timeAxisProps(timeRange)} />
            <YAxis
              domain={[0, (max: number) => Math.max(1, Math.ceil(max))]}
              tickFormatter={(v: number) => `${String(v)}%`}
              tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              width={40}
            />
            <Tooltip
              labelFormatter={(l: ReactNode) => tsFormatter(Number(l))}
              formatter={(value: unknown, name: unknown): [string, string] => [
                formatPercent(typeof value === "number" ? value : 0),
                String(name),
              ]}
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "8px",
                fontSize: "12px",
              }}
            />
            <Area
              type="monotone"
              dataKey={some}
              name={`${label} some`}
              stroke={color}
              fill={color}
              fillOpacity={0.15}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
            {hasFull && (
              <Line
                type="monotone"
                dataKey={full}
                name={`${label} full`}
                stroke={color}
                strokeDasharray="4 3"
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
            )}
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

/**
 * A node's or guest's pressure stall information: the share of time its tasks
 * waited for CPU, memory or IO. A storage stall that never shows as low
 * throughput — the tasks simply wait — shows here.
 */
export function PressureChart({
  data,
  timeRange,
  isLoading,
  isError,
}: {
  data: PressurePoint[] | undefined;
  timeRange: TimeRange;
  isLoading: boolean;
  isError: boolean;
}) {
  const points = data ?? [];
  const measured = points.some((p) =>
    RESOURCES.some((r) => p[r.some] !== null || p[r.full] !== null),
  );
  return (
    <Panel title="Pressure">
      <p className="-mt-1.5 mb-4 max-w-[62rem] text-[12.5px] text-muted-foreground">
        Share of time tasks waited for CPU, memory or IO. The area is time at
        least one task waited; the dashed line, time every non-idle task did.
        Each point is the highest in its interval.
      </p>
      {isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : isError && data === undefined ? (
        <div className="py-6 text-center text-sm text-destructive">
          Could not load pressure data.
        </div>
      ) : !measured ? (
        <div className="py-6 text-center text-sm text-muted-foreground">
          No pressure recorded in this range. Proxmox VE 9 or later reports it,
          for a guest only while it runs.
        </div>
      ) : (
        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-3">
          {RESOURCES.map((r) => (
            <PressurePanel
              key={r.label}
              data={points}
              timeRange={timeRange}
              {...r}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}
