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
import { getTimestampFormatter, timeAxisProps } from "@/features/dashboard/components/time-axis";
import { DATA_COLOR } from "@/lib/chart-colors";
import type { UpsMetricPoint, UpsRange } from "../types/ups";
import { formatRuntime, onBatteryRuns } from "../lib/ups-state";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

/** The measure the UPS page's strip has selected. Input and output share one chart. */
export type UpsChartFocus = "charge" | "runtime" | "load" | "voltage";

/**
 * How a line is told from its neighbours. Every reading is drawn in the one
 * data colour, so a second line is the same colour fainter and a reference
 * line is dashed and neutral.
 */
type LineStyle = "solid" | "faint" | "reference";

const STROKE: Record<LineStyle, { stroke: string; strokeOpacity?: number; strokeDasharray?: string; strokeWidth: number }> = {
  solid: { stroke: DATA_COLOR, strokeWidth: 2 },
  faint: { stroke: DATA_COLOR, strokeOpacity: 0.45, strokeWidth: 2 },
  reference: { stroke: "hsl(var(--muted-foreground))", strokeDasharray: "5 4", strokeWidth: 1.5 },
};

interface Series {
  key: keyof UpsMetricPoint;
  label: string;
  style: LineStyle;
}

const pct = (v: number) => `${String(Math.round(v))}%`;
const volts = (v: number) => `${String(Math.round(v))} V`;
/** A percentage axis that still shows an overload above 100%. */
const PERCENT_DOMAIN: [number, (max: number) => number] = [0, (max) => Math.max(100, Math.ceil(max))];

/**
 * Each bucket is its worst value (lowest charge, runtime and input; highest
 * load), so a short dip shows. Voltage draws input against output: the gap
 * between them is the UPS doing its job.
 */
const CHARTS: Record<
  UpsChartFocus,
  { title: string; subtitle: string; format: (v: number) => string; domain: [number | "auto", number | "auto" | ((max: number) => number)]; series: Series[] }
> = {
  charge: {
    get title() { return i18n.t("power:upsCharts.batteryCharge"); },
    get subtitle() { return i18n.t("power:upsCharts.lowestChargeInEachBucket"); },
    format: pct,
    domain: PERCENT_DOMAIN,
    series: [{ key: "charge", get label() { return i18n.t("power:upsCharts.charge"); }, style: "solid" }],
  },
  runtime: {
    get title() { return i18n.t("power:upsCharts.runtime"); },
    get subtitle() { return i18n.t("power:upsCharts.shortestRuntimeInEachBucket"); },
    format: (v) => formatRuntime(v),
    domain: [0, "auto"],
    series: [{ key: "runtime", get label() { return i18n.t("power:upsCharts.runtime"); }, style: "solid" }],
  },
  load: {
    get title() { return i18n.t("power:upsCharts.load"); },
    get subtitle() { return i18n.t("power:upsCharts.highestLoadInEachBucket"); },
    format: pct,
    domain: PERCENT_DOMAIN,
    series: [{ key: "load", get label() { return i18n.t("power:upsCharts.load"); }, style: "solid" }],
  },
  voltage: {
    get title() { return i18n.t("power:upsCharts.inputAndOutputVoltage"); },
    get subtitle() { return i18n.t("power:upsCharts.inputSLowAndHigh"); },
    format: volts,
    domain: [0, "auto"],
    series: [
      { key: "input_voltage_min", get label() { return i18n.t("power:upsCharts.inputLow"); }, style: "solid" },
      { key: "input_voltage_max", get label() { return i18n.t("power:upsCharts.inputHigh"); }, style: "faint" },
      { key: "output_voltage", get label() { return i18n.t("power:upsCharts.output"); }, style: "reference" },
    ],
  },
};

function LegendLine({ style }: { style: LineStyle }) {
  const s = STROKE[style];
  return (
    <svg width="16" height="6" viewBox="0 0 16 6" aria-hidden="true">
      <line x1="0" y1="3" x2="16" y2="3" {...s} />
    </svg>
  );
}

/**
 * One measure of a UPS over the chosen range, shaded where it was on battery.
 * Sits bare inside the readings panel, under the strip that selects it.
 */
export function UpsChart({
  focus,
  points,
  range,
  bucketSeconds,
}: {
  focus: UpsChartFocus;
  points: UpsMetricPoint[];
  range: UpsRange;
  bucketSeconds: number;
}) {
  const { t } = useTranslation("power");
  const chart = CHARTS[focus];
  const ts = getTimestampFormatter(range);
  const outages = onBatteryRuns(points, bucketSeconds * 1000);
  return (
    <div>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <h3 className="text-[13.5px] font-semibold">{chart.title}</h3>
        <p className="text-[12.5px] text-muted-foreground">{chart.subtitle}</p>
      </div>
      <div className="mt-2 h-64">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={points} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
            <CartesianGrid strokeDasharray="3 4" className="stroke-border" vertical={false} />
            {outages.map(([x1, x2]) => (
              <ReferenceArea key={x1} x1={x1} x2={x2} fill="#f59e0b" fillOpacity={0.12} ifOverflow="extendDomain" />
            ))}
            <XAxis {...timeAxisProps(range)} />
            <YAxis
              tickFormatter={chart.format}
              tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              width={52}
              domain={chart.domain}
            />
            <Tooltip
              labelFormatter={(label: ReactNode) => ts(Number(label))}
              formatter={(value: unknown, name: unknown) => {
                const key = String(name);
                const s = chart.series.find((x) => x.key === key);
                return [typeof value === "number" ? chart.format(value) : "—", s?.label ?? key];
              }}
              contentStyle={{
                backgroundColor: "hsl(var(--popover))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "8px",
                fontSize: "12px",
              }}
            />
            {chart.series.map((s) => (
              <Line
                key={s.key}
                type="monotone"
                dataKey={s.key}
                name={s.key}
                {...STROKE[s.style]}
                dot={false}
                isAnimationActive={false}
                connectNulls={false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {chart.series.length > 1 &&
          chart.series.map((s) => (
            <span key={s.key} className="flex items-center gap-1.5">
              <LegendLine style={s.style} />
              {s.label}
            </span>
          ))}
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-3 rounded-sm bg-amber-500/30" aria-hidden="true" />
          {t("upsCharts.onBattery")}
        </span>
      </div>
    </div>
  );
}
