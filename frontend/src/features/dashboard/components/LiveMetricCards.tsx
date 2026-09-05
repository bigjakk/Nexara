import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Cpu, MemoryStick, HardDrive, Network } from "lucide-react";
import { formatBytes, formatPercent, formatBytesPerSecond } from "@/lib/format";
import { Sparkline } from "@/components/Sparkline";
import { useSeedMetrics } from "../api/historical-queries";
import type { AggregatedMetrics } from "@/types/ws";

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
  // Seed buckets are 5-minute, live ticks are per refresh interval; the
  // sparkline spaces points by index, so the live tail renders wider than its
  // wall-clock share — acceptable for an axis-less trend strip.
  const history = useMemo(() => {
    const live = liveHistory ?? [];
    const seeded = seed ?? [];
    if (live.length === 0) return seeded;
    if (seeded.length === 0) return live;
    const firstLiveTs = live[0]?.timestamp ?? 0;
    return [...seeded.filter((p) => p.timestamp < firstLiveTs), ...live];
  }, [seed, liveHistory]);
  const cards = [
    {
      key: "cpu",
      label: "CPU Usage",
      icon: Cpu,
      value: metrics ? formatPercent(metrics.cpuPercent) : null,
      sub: null,
      spark: history.map((p) => p.cpuPercent),
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
      spark: history.map((p) => p.memPercent),
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
      spark: history.map((p) => p.diskReadBps + p.diskWriteBps),
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
      spark: history.map((p) => p.netInBps + p.netOutBps),
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
