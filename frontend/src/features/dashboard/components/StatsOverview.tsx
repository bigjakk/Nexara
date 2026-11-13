import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { formatBytes } from "@/lib/format";
import { usageLevel } from "@/lib/usage-level";
import { AnimatedNumber } from "@/components/AnimatedNumber";
import type { VMResponse } from "@/types/api";
import type { AggregatedMetrics, MetricSeriesPoint } from "@/types/ws";
import { sparklineSeries } from "@/lib/metric-series";
import { guestCounts } from "../lib/estate-summary";

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
  /**
   * Every cluster's guest list. With it the guest numbers are counted from
   * the lists, templates left out, as the header and the Fleet widget count
   * them; the four totals above are only the fallback without it.
   */
  vmsByCluster?: ReadonlyMap<string, VMResponse[]> | undefined;
  /** Live per-cluster metrics; drives the datacenter CPU card and sparkline. */
  metrics?: Map<string, AggregatedMetrics> | undefined;
  /** Stored 1h history per cluster, so sparklines are full on page load. */
  seeds?: Map<string, MetricSeriesPoint[]> | undefined;
}

/** A reading that is filling up is a problem from 75%; below that it is plain. */
function usageTone(percent: number | null): StatTone {
  const level = usageLevel(percent);
  return level === "err" ? "bad" : level === "warn" ? "warn" : "default";
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
  vmsByCluster,
  metrics,
  seeds,
}: StatsOverviewProps) {
  const { t } = useTranslation("dashboard");

  const guests = useMemo(
    () =>
      vmsByCluster !== undefined
        ? guestCounts(vmsByCluster.values())
        : {
            vms: totalVMs,
            vmsRunning: totalVMsRunning,
            containers: totalContainers,
            containersRunning: totalContainersRunning,
          },
    [
      vmsByCluster,
      totalVMs,
      totalVMsRunning,
      totalContainers,
      totalContainersRunning,
    ],
  );
  const guestsTotal = guests.vms + guests.containers;
  const guestsRunning = guests.vmsRunning + guests.containersRunning;
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

  const loading = (value: React.ReactNode) =>
    isLoading ? (
      <Skeleton className="h-7 w-20" data-testid="stat-skeleton" />
    ) : (
      value
    );
  const nodesDown = totalNodes - totalNodesOnline;

  return (
    <Panel
      flush
      title={t("allClusters")}
      className="flex h-full flex-col overflow-hidden"
      bodyClassName="min-h-0 flex-1 overflow-y-auto"
    >
      <StatStrip>
        <Stat
          label={t("nodes")}
          value={loading(totalNodes)}
          tone={
            isLoading || nodesDown === 0
              ? totalNodes === 0
                ? "quiet"
                : "default"
              : totalNodesOnline === 0
                ? "bad"
                : "warn"
          }
          sub={
            isLoading
              ? undefined
              : t("statOnline", { online: totalNodesOnline, total: totalNodes })
          }
        />
        <Stat
          label={t("guests")}
          value={loading(
            <>
              <AnimatedNumber
                value={guestsRunning}
                format={(v) => String(Math.round(v))}
              />
              <span className="text-base font-medium text-muted-foreground">
                /{guestsTotal}
              </span>
            </>,
          )}
          sub={
            isLoading
              ? undefined
              : t("statGuestMix", { vms: guests.vms, cts: guests.containers })
          }
        />
        <Stat
          label={t("cpu")}
          value={loading(
            cpuNow === null ? (
              "—"
            ) : (
              <AnimatedNumber
                value={cpuNow}
                format={(v) => `${v.toFixed(1)}%`}
              />
            ),
          )}
          tone={cpuNow === null ? "quiet" : usageTone(cpuNow)}
          sub={
            isLoading
              ? undefined
              : cpuNow === null
                ? t("waitingForData")
                : t("statAcrossClusters", { count: clusterMetrics.length })
          }
          trend={isLoading ? undefined : cpuSeries}
        />
        <Stat
          label={t("memory")}
          value={loading(
            memNow === null ? (
              "—"
            ) : (
              <AnimatedNumber
                value={memNow}
                format={(v) => `${v.toFixed(1)}%`}
              />
            ),
          )}
          tone={memNow === null ? "quiet" : usageTone(memNow)}
          sub={
            isLoading
              ? undefined
              : memNow === null
                ? t("waitingForData")
                : t("statBytesUsedOf", {
                    used: formatBytes(memUsed),
                    total: formatBytes(memTotal),
                  })
          }
          trend={isLoading ? undefined : memSeries}
        />
        <Stat
          label={t("storage")}
          value={loading(
            totalStorageBytes > 0 ? `${String(storagePercent)}%` : "—",
          )}
          tone={totalStorageBytes > 0 ? usageTone(storagePercent) : "quiet"}
          sub={
            isLoading
              ? undefined
              : t("statBytesUsedOf", {
                  used: formatBytes(totalStorageUsedBytes),
                  total: formatBytes(totalStorageBytes),
                })
          }
          percent={
            isLoading
              ? undefined
              : totalStorageBytes > 0
                ? storagePercent
                : null
          }
        />
      </StatStrip>
    </Panel>
  );
}
