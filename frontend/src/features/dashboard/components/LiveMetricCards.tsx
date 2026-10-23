import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Cpu, MemoryStick, HardDrive, Network } from "lucide-react";
import { formatBytes, formatPercent, formatBytesPerSecond } from "@/lib/format";
import { Sparkline } from "@/components/Sparkline";
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

interface LiveMetricCardsProps {
  metrics: AggregatedMetrics | undefined;
  /**
   * When set, the trend strips are seeded from the cluster's stored 1h history
   * so they are fully drawn on page load instead of building up tick by tick.
   */
  clusterId?: string | undefined;
}

export function LiveMetricCards({ metrics, clusterId }: LiveMetricCardsProps) {
  const { t } = useTranslation("dashboard");
  const seed = useSeedMetrics(clusterId ?? "");
  const liveHistory = metrics?.history;
  const history = useMemo(
    () => sparklineSeries(seed ?? [], liveHistory ?? []),
    [seed, liveHistory],
  );
  const cards = [
    {
      key: "cpu",
      label: "CPU Usage",
      icon: Cpu,
      value: metrics ? formatPercent(metrics.cpuPercent) : null,
      sub: null,
      spark: measured(history, (p) => p.cpuPercent),
      sparkClass: "text-sky-400",
    },
    {
      key: "memory",
      label: "Memory Usage",
      icon: MemoryStick,
      value: metrics ? formatPercent(metrics.memPercent) : null,
      sub:
        metrics && metrics.memTotal > 0
          ? t("statBytesUsedOf", {
              used: formatBytes(metrics.memUsed),
              total: formatBytes(metrics.memTotal),
            })
          : null,
      spark: measured(history, (p) => p.memPercent),
      sparkClass: "text-violet-400",
    },
    {
      key: "disk",
      label: "Disk I/O",
      icon: HardDrive,
      value: metrics
        ? `${formatBytesPerSecond(metrics.diskReadBps)} / ${formatBytesPerSecond(metrics.diskWriteBps)}`
        : null,
      sub: null,
      spark: measured(history, (p) => sum(p.diskReadBps, p.diskWriteBps)),
      sparkClass: "text-amber-500",
    },
    {
      key: "network",
      label: "Network",
      icon: Network,
      value: metrics
        ? `${formatBytesPerSecond(metrics.netInBps)} / ${formatBytesPerSecond(metrics.netOutBps)}`
        : null,
      sub: null,
      spark: measured(history, (p) => sum(p.netInBps, p.netOutBps)),
      sparkClass: "text-emerald-500",
    },
  ] as const;

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      {cards.map((card) => {
        const Icon = card.icon;
        return (
          <Card key={card.key} className="flex flex-col">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">
                {card.label}
              </CardTitle>
              <Icon className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent className="flex flex-1 flex-col">
              {card.value === null ? (
                <Skeleton className="h-7 w-24" data-testid="metric-skeleton" />
              ) : (
                <>
                  <div className="text-lg font-semibold">{card.value}</div>
                  {card.sub !== null && (
                    <div className="mt-0.5 text-xs text-muted-foreground">
                      {card.sub}
                    </div>
                  )}
                  <div className="mt-auto pt-2">
                    <Sparkline
                      points={card.spark}
                      className={`h-7 w-full ${card.sparkClass}`}
                    />
                  </div>
                </>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
