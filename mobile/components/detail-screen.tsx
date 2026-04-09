/**
 * Shared primitives for detail screens (VM detail, node detail, storage
 * detail, cluster detail). Extracted after the third copy of `Row` and
 * `SectionHeader` landed during the v1.1 storage detail screen work, plus
 * a fourth copy of `SectionHeader` already living in cluster detail.
 *
 * Scope of this module: only the helpers that are duplicated across
 * **multiple** detail screens. Screen-specific helpers stay where they
 * are:
 *
 *   - `SummaryStat` (cluster detail) — has an `icon` prop, only one consumer
 *   - `Stat` (storage detail) — different font sizes from `LiveStat`
 *   - `CapacityBar` / `deriveStatus` / `describeStorageType` (storage detail)
 *   - `NodeRow` / `GuestRow` / `StorageRow` (cluster detail list rows)
 *   - `dedupStoragePools` (cluster detail dedup logic)
 *
 * Single-file layout (rather than a `components/detail-screen/` directory
 * with one file per primitive) because the total surface is small (~6
 * components, ~150 lines), and a single file is easier to navigate, has
 * fewer imports for consumers to track, and resists accidental sprawl.
 */

import { ActivityIndicator, Text, View } from "react-native";

import { Sparkline } from "@/components/Sparkline";
import type { MetricPoint } from "@/features/api/types";

// ─── Static layout primitives ───────────────────────────────────────────────

/**
 * Section title. Uppercase muted-foreground text with top + bottom margin
 * matching the spacing on the four detail screens that use it.
 */
export function SectionHeader({ title }: { title: string }) {
  return (
    <Text className="mt-6 mb-3 text-xs font-bold uppercase text-muted-foreground">
      {title}
    </Text>
  );
}

/**
 * Single-line label/value row inside a card. Used for spec tables, config
 * tables, and metadata sections. `last` skips the bottom border so the
 * card edge looks clean.
 */
export function Row({
  label,
  value,
  last,
}: {
  label: string;
  value: string;
  last?: boolean;
}) {
  return (
    <View
      className={`flex-row items-center justify-between px-4 py-3 ${
        last ? "" : "border-b border-border"
      }`}
    >
      <Text className="text-sm text-muted-foreground">{label}</Text>
      <Text
        className="flex-1 pl-3 text-right text-sm text-foreground"
        numberOfLines={1}
      >
        {value}
      </Text>
    </View>
  );
}

/**
 * Compact stat tile used in header cards for live values (e.g. live CPU%
 * / Memory% from the metric WS channel). Two side-by-side instances are
 * the typical layout — wrap them in a `flex-row gap-3` parent.
 *
 * Distinct from storage detail's local `Stat` (smaller font) and cluster
 * detail's local `SummaryStat` (has an icon prop).
 */
export function LiveStat({
  label,
  value,
}: {
  label: string;
  value: string;
}) {
  return (
    <View className="flex-1 rounded border border-border bg-background p-3">
      <Text className="text-xs text-muted-foreground">{label}</Text>
      <Text className="mt-1 text-lg font-semibold text-foreground">
        {value}
      </Text>
    </View>
  );
}

// ─── Metric history card with sparklines ───────────────────────────────────

/**
 * Last-hour metric history card with CPU / Memory / Net in / Net out
 * sparklines. Consumes `MetricPoint[]` straight from `useVMMetrics` /
 * `useNodeMetrics`. Renders a loading spinner before data lands and an
 * empty-state message when the collector hasn't published a window yet.
 */
export function MetricsCard({
  points,
  loading,
}: {
  points: MetricPoint[] | undefined;
  loading: boolean;
}) {
  if (loading && !points) {
    return (
      <View className="rounded-lg border border-border bg-card p-4">
        <ActivityIndicator color="#22c55e" />
      </View>
    );
  }
  if (!points || points.length === 0) {
    return (
      <View className="rounded-lg border border-border bg-card p-4">
        <Text className="text-xs text-muted-foreground">
          No metric data yet — collector hasn&apos;t published a window for
          this resource.
        </Text>
      </View>
    );
  }

  const cpu = points.map((p) => p.cpuPercent);
  const mem = points.map((p) => p.memPercent);
  const netIn = points.map((p) => p.netInBps);
  const netOut = points.map((p) => p.netOutBps);
  const lastCpu = cpu[cpu.length - 1] ?? 0;
  const lastMem = mem[mem.length - 1] ?? 0;

  return (
    <View className="gap-3 rounded-lg border border-border bg-card p-4">
      <MetricRow
        label="CPU"
        valueText={`${lastCpu.toFixed(1)}%`}
        values={cpu}
        range={[0, 100]}
      />
      <MetricRow
        label="Memory"
        valueText={`${lastMem.toFixed(1)}%`}
        values={mem}
        range={[0, 100]}
      />
      <MetricRow
        label="Net in"
        valueText={formatBps(netIn[netIn.length - 1] ?? 0)}
        values={netIn}
      />
      <MetricRow
        label="Net out"
        valueText={formatBps(netOut[netOut.length - 1] ?? 0)}
        values={netOut}
      />
    </View>
  );
}

function MetricRow({
  label,
  valueText,
  values,
  range,
}: {
  label: string;
  valueText: string;
  values: number[];
  range?: [number, number];
}) {
  return (
    <View>
      <View className="flex-row items-center justify-between">
        <Text className="text-xs text-muted-foreground">{label}</Text>
        <Text className="text-xs font-medium text-foreground">{valueText}</Text>
      </View>
      <Sparkline values={values} height={40} width={300} range={range} />
    </View>
  );
}

/**
 * Bytes-per-second formatter. Public so other screens can use it for
 * one-off display (e.g. live cluster aggregate BPS in a future dashboard
 * widget) without re-importing the whole MetricsCard.
 */
export function formatBps(bps: number): string {
  if (!Number.isFinite(bps) || bps < 0) return "—";
  if (bps < 1024) return `${bps.toFixed(0)} B/s`;
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  if (bps < 1024 * 1024 * 1024)
    return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
  return `${(bps / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
}
