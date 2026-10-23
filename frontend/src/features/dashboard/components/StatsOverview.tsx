import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Server, Monitor, Cpu, MemoryStick, HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { AnimatedNumber } from "@/components/AnimatedNumber";
import { Sparkline } from "@/components/Sparkline";
import type { AggregatedMetrics, MetricSeriesPoint } from "@/types/ws";
import { sparklineSeries } from "@/lib/metric-series";

interface StatsOverviewProps {
  totalNodes: number;
  totalNodesOnline: number;
  totalVMs: number;
  totalVMsRunning: number;
  totalContainers: number;
  totalContainersRunning: number;
  totalStorageBytes: number;
  totalStorageUsedBytes: number;
  isLoading: boolean;
  /** Live per-cluster metrics; drives the datacenter CPU card and sparkline. */
  metrics?: Map<string, AggregatedMetrics> | undefined;
  /** Stored 1h history per cluster, so sparklines are full on page load. */
  seeds?: Map<string, MetricSeriesPoint[]> | undefined;
}

/** Same card chrome as LiveMetricCards, so dashboard and cluster cards match. */
function StatCard({
  icon,
  label,
  isLoading,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  isLoading: boolean;
  children: React.ReactNode;
}) {
  return (
    <Card className="flex flex-col">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{label}</CardTitle>
        <span className="text-muted-foreground">{icon}</span>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col">
        {isLoading ? (
          <Skeleton className="h-7 w-24" data-testid="stat-skeleton" />
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

export function StatsOverview({
  totalNodes,
  totalNodesOnline,
  totalVMs,
  totalVMsRunning,
  totalContainers,
  totalContainersRunning,
  totalStorageBytes,
  totalStorageUsedBytes,
  isLoading,
  metrics,
  seeds,
}: StatsOverviewProps) {
  const { t } = useTranslation("dashboard");

  const guestsTotal = totalVMs + totalContainers;
  const guestsRunning = totalVMsRunning + totalContainersRunning;
  const storagePercent =
    totalStorageBytes > 0
      ? Math.round((totalStorageUsedBytes / totalStorageBytes) * 100)
      : 0;

  const clusterMetrics = useMemo(
    () => (metrics ? [...metrics.values()] : []),
    [metrics],
  );
  const cpuNow =
    clusterMetrics.length > 0
      ? clusterMetrics.reduce((s, m) => s + m.cpuPercent, 0) /
        clusterMetrics.length
      : null;
  // Capacity-weighted so the % agrees with the "X of Y used" bytes below it.
  // The sparkline averages per-cluster memPercent unweighted (history carries
  // no byte totals), so its trend can drift from this headline — accepted.
  const memUsed = clusterMetrics.reduce((s, m) => s + m.memUsed, 0);
  const memTotal = clusterMetrics.reduce((s, m) => s + m.memTotal, 0);
  const memNow = memTotal > 0 ? (memUsed / memTotal) * 100 : null;
  const [cpuSeries, memSeries] = useMemo(() => {
    // Each cluster's last hour, history plus live ticks in minute buckets,
    // then averaged point-wise across clusters (aligned from the newest point
    // backwards).
    const ids = new Set([...(metrics?.keys() ?? []), ...(seeds?.keys() ?? [])]);
    const hists: MetricSeriesPoint[][] = [];
    for (const id of ids) {
      const merged = sparklineSeries(
        seeds?.get(id) ?? [],
        metrics?.get(id)?.history ?? [],
      );
      if (merged.length > 1) hists.push(merged);
    }
    if (hists.length === 0) return [[], []];
    // A cluster whose seed hasn't loaded yet has only a couple of live ticks;
    // don't let it collapse the whole strip to that length while others have
    // a full hour. (Series are index-aligned from the newest point backwards;
    // their minute buckets keep that even in time.) A point no cluster
    // measured is left out rather than drawn as 0%.
    const longest = Math.max(...hists.map((h) => h.length));
    const usable = hists.filter((h) => h.length >= Math.min(5, longest));
    const n = Math.min(90, ...usable.map((h) => h.length));
    const cpu: number[] = [];
    const mem: number[] = [];
    for (let i = 0; i < n; i++) {
      let cpuSum = 0;
      let cpuCount = 0;
      let memSum = 0;
      let memCount = 0;
      for (const h of usable) {
        const p = h[h.length - n + i];
        if (p?.cpuPercent !== null && p?.cpuPercent !== undefined) {
          cpuSum += p.cpuPercent;
          cpuCount++;
        }
        if (p?.memPercent !== null && p?.memPercent !== undefined) {
          memSum += p.memPercent;
          memCount++;
        }
      }
      if (cpuCount > 0) cpu.push(cpuSum / cpuCount);
      if (memCount > 0) mem.push(memSum / memCount);
    }
    return [cpu, mem];
  }, [metrics, seeds]);

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
      <StatCard
        icon={<Server className="h-4 w-4" />}
        label={t("nodes")}
        isLoading={isLoading}
      >
        <div className="text-lg font-semibold tabular-nums">{totalNodes}</div>
        <div className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
          <span
            className={cn(
              "h-1.5 w-1.5 rounded-full",
              totalNodes > 0 && totalNodesOnline === totalNodes
                ? "bg-emerald-500"
                : totalNodesOnline > 0
                  ? "bg-amber-500"
                  : "bg-muted-foreground/40",
            )}
          />
          {t("statOnline", { online: totalNodesOnline, total: totalNodes })}
        </div>
      </StatCard>

      <StatCard
        icon={<Monitor className="h-4 w-4" />}
        label={t("guests")}
        isLoading={isLoading}
      >
        <div className="text-lg font-semibold tabular-nums">
          <AnimatedNumber
            value={guestsRunning}
            format={(v) => String(Math.round(v))}
          />
          <span className="text-sm font-medium text-muted-foreground">
            /{guestsTotal}
          </span>
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {t("statGuestMix", { vms: totalVMs, cts: totalContainers })}
        </div>
      </StatCard>

      <StatCard
        icon={<Cpu className="h-4 w-4" />}
        label={t("datacenterCpu")}
        isLoading={isLoading}
      >
        <div className="text-lg font-semibold tabular-nums">
          {cpuNow === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <AnimatedNumber value={cpuNow} format={(v) => `${v.toFixed(1)}%`} />
          )}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {cpuNow === null
            ? t("waitingForData")
            : t("statAcrossClusters", { count: clusterMetrics.length })}
        </div>
        <div className="mt-auto pt-2">
          <Sparkline
            points={cpuSeries}
            className="h-7 w-full text-violet-500"
          />
        </div>
      </StatCard>

      <StatCard
        icon={<MemoryStick className="h-4 w-4" />}
        label={t("datacenterMemory")}
        isLoading={isLoading}
      >
        <div className="text-lg font-semibold tabular-nums">
          {memNow === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <AnimatedNumber value={memNow} format={(v) => `${v.toFixed(1)}%`} />
          )}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {memNow === null
            ? t("waitingForData")
            : t("statBytesUsedOf", {
                used: formatBytes(memUsed),
                total: formatBytes(memTotal),
              })}
        </div>
        <div className="mt-auto pt-2">
          <Sparkline points={memSeries} className="h-7 w-full text-rose-500" />
        </div>
      </StatCard>

      <StatCard
        icon={<HardDrive className="h-4 w-4" />}
        label={t("totalStorage")}
        isLoading={isLoading}
      >
        <div className="text-lg font-semibold tabular-nums">
          {formatBytes(totalStorageBytes)}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {t("statUsedPercent", { percent: storagePercent })}
        </div>
        <div className="mt-auto pt-2">
          <div className="h-1 overflow-hidden rounded-full bg-foreground/10">
            <div
              className={cn(
                "h-full rounded-full transition-[width] duration-700",
                storagePercent >= 90
                  ? "bg-red-500"
                  : storagePercent >= 75
                    ? "bg-amber-500"
                    : "bg-emerald-500",
              )}
              style={{ width: `${String(storagePercent)}%` }}
            />
          </div>
        </div>
      </StatCard>
    </div>
  );
}
