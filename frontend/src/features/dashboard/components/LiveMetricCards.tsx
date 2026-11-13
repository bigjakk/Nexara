import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { formatBytes, formatPercent, formatBytesPerSecond } from "@/lib/format";
import { usageLevel } from "@/lib/usage-level";
import { useSeedMetrics } from "../api/historical-queries";
import { sparklineSeries } from "@/lib/metric-series";
import type { AggregatedMetrics, MetricSeriesPoint } from "@/types/ws";

/** A sparkline's values: the points that measured it, the rest left out. */
function measured(
  history: readonly MetricSeriesPoint[],
  value: (p: MetricSeriesPoint) => number | null,
): number[] {
  const out: number[] = [];
  for (const p of history) {
    const v = value(p);
    if (v !== null) out.push(v);
  }
  return out;
}

/** The sum of two rates, or nothing when either was not measured. */
function sum(a: number | null, b: number | null): number | null {
  return a === null || b === null ? null : a + b;
}

function usageTone(percent: number): StatTone {
  const level = usageLevel(percent);
  return level === "err" ? "bad" : level === "warn" ? "warn" : "default";
}

interface LiveMetricCardsProps {
  metrics: AggregatedMetrics | undefined;
  /**
   * When set, the trend strips are seeded from the cluster's stored 1h history
   * so they are fully drawn on page load instead of building up tick by tick.
   */
  clusterId?: string | undefined;
  /** Whose load this is, where the page does not already say (the dashboard). */
  title?: ReactNode;
}

/**
 * One cluster's load right now: CPU, memory, disk and network as one strip of
 * readings, each over its last hour.
 */
export function LiveMetricCards({
  metrics,
  clusterId,
  title,
}: LiveMetricCardsProps) {
  const { t } = useTranslation("dashboard");
  const seed = useSeedMetrics(clusterId ?? "");
  const liveHistory = metrics?.history;
  const history = useMemo(
    () => sparklineSeries(seed ?? [], liveHistory ?? []),
    [seed, liveHistory],
  );
  const pending = (
    <Skeleton className="h-7 w-20" data-testid="metric-skeleton" />
  );

  return (
    <Panel
      flush
      title={title ?? t("liveLoad")}
      aside={title !== undefined ? t("liveLoad") : undefined}
      className="flex h-full flex-col overflow-hidden"
      bodyClassName="min-h-0 flex-1 overflow-y-auto"
    >
      <StatStrip>
        <Stat
          label={t("cpu")}
          value={metrics ? formatPercent(metrics.cpuPercent) : pending}
          tone={metrics ? usageTone(metrics.cpuPercent) : "default"}
          trend={metrics ? measured(history, (p) => p.cpuPercent) : undefined}
        />
        <Stat
          label={t("memory")}
          value={metrics ? formatPercent(metrics.memPercent) : pending}
          tone={metrics ? usageTone(metrics.memPercent) : "default"}
          sub={
            metrics && metrics.memTotal > 0
              ? t("statBytesUsedOf", {
                  used: formatBytes(metrics.memUsed),
                  total: formatBytes(metrics.memTotal),
                })
              : undefined
          }
          trend={metrics ? measured(history, (p) => p.memPercent) : undefined}
        />
        <Stat
          label={t("diskIo")}
          value={
            metrics
              ? formatBytesPerSecond(metrics.diskReadBps + metrics.diskWriteBps)
              : pending
          }
          sub={
            metrics
              ? t("statReadWrite", {
                  read: formatBytesPerSecond(metrics.diskReadBps),
                  write: formatBytesPerSecond(metrics.diskWriteBps),
                })
              : undefined
          }
          trend={
            metrics
              ? measured(history, (p) => sum(p.diskReadBps, p.diskWriteBps))
              : undefined
          }
        />
        <Stat
          label={t("network")}
          value={
            metrics
              ? formatBytesPerSecond(metrics.netInBps + metrics.netOutBps)
              : pending
          }
          sub={
            metrics
              ? t("statInOut", {
                  in: formatBytesPerSecond(metrics.netInBps),
                  out: formatBytesPerSecond(metrics.netOutBps),
                })
              : undefined
          }
          trend={
            metrics
              ? measured(history, (p) => sum(p.netInBps, p.netOutBps))
              : undefined
          }
        />
      </StatStrip>
    </Panel>
  );
}
