import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Server, Monitor, Cpu, HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { AnimatedNumber } from "@/components/AnimatedNumber";
import type { AggregatedMetrics } from "@/types/ws";

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
}

function Sparkline({
  points,
  className,
}: {
  points: number[];
  className?: string;
}) {
  if (points.length < 2) return null;
  const W = 64;
  const H = 22;
  const lo = Math.min(...points);
  const hi = Math.max(...points);
  const span = hi - lo || 1;
  const coords = points
    .map(
      (p, i) =>
        `${((i / (points.length - 1)) * W).toFixed(1)},${(H - 2 - ((p - lo) / span) * (H - 4)).toFixed(1)}`,
    )
    .join(" ");
  return (
    <svg
      width={W}
      height={H}
      className={cn("shrink-0", className)}
      aria-hidden="true"
    >
      <polyline
        points={coords}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      />
    </svg>
  );
}

function StatCard({
  icon,
  iconClass,
  label,
  isLoading,
  children,
}: {
  icon: React.ReactNode;
  iconClass: string;
  label: string;
  isLoading: boolean;
  children: React.ReactNode;
}) {
  return (
    <Card className="p-4">
      <div className="flex items-center gap-2.5">
        <span
          className={cn(
            "flex h-7 w-7 shrink-0 items-center justify-center rounded-md",
            iconClass,
          )}
        >
          {icon}
        </span>
        <span className="truncate text-xs font-medium text-muted-foreground">
          {label}
        </span>
      </div>
      {isLoading ? (
        <Skeleton className="mt-3 h-8 w-20" data-testid="stat-skeleton" />
      ) : (
        children
      )}
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
  const cpuSeries = useMemo(() => {
    const hists = clusterMetrics
      .map((m) => m.history)
      .filter((h) => h.length > 1);
    if (hists.length === 0) return [];
    const n = Math.min(30, ...hists.map((h) => h.length));
    const out: number[] = [];
    for (let i = 0; i < n; i++) {
      let sum = 0;
      for (const h of hists) {
        sum += h[h.length - n + i]?.cpuPercent ?? 0;
      }
      out.push(sum / hists.length);
    }
    return out;
  }, [clusterMetrics]);

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      <StatCard
        icon={<Server className="h-4 w-4" />}
        iconClass="bg-sky-500/10 text-sky-500"
        label={t("nodes")}
        isLoading={isLoading}
      >
        <div className="mt-3 text-2xl font-semibold tabular-nums tracking-tight">
          {totalNodes}
        </div>
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
        iconClass="bg-emerald-500/10 text-emerald-500"
        label={t("guests")}
        isLoading={isLoading}
      >
        <div className="mt-3 text-2xl font-semibold tabular-nums tracking-tight">
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
        iconClass="bg-violet-500/10 text-violet-500"
        label={t("datacenterCpu")}
        isLoading={isLoading}
      >
        <div className="mt-3 flex items-end justify-between gap-2">
          <div className="text-2xl font-semibold tabular-nums tracking-tight">
            {cpuNow === null ? (
              <span className="text-muted-foreground">—</span>
            ) : (
              <AnimatedNumber
                value={cpuNow}
                format={(v) => `${v.toFixed(1)}%`}
              />
            )}
          </div>
          <Sparkline points={cpuSeries} className="mb-1 text-violet-500" />
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {cpuNow === null
            ? t("waitingForData")
            : t("statAcrossClusters", { count: clusterMetrics.length })}
        </div>
      </StatCard>

      <StatCard
        icon={<HardDrive className="h-4 w-4" />}
        iconClass="bg-amber-500/10 text-amber-500"
        label={t("totalStorage")}
        isLoading={isLoading}
      >
        <div className="mt-3 text-2xl font-semibold tabular-nums tracking-tight">
          {formatBytes(totalStorageBytes)}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {t("statUsedPercent", { percent: storagePercent })}
        </div>
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-foreground/10">
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
      </StatCard>
    </div>
  );
}
