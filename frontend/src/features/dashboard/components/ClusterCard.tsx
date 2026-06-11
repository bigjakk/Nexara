import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Card } from "@/components/ui/card";
import { ClusterStatusBadge } from "@/components/ClusterStatusBadge";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ClusterResponse } from "@/types/api";
import type { AggregatedMetrics } from "@/types/ws";
import type { ClusterSummary } from "../api/dashboard-queries";

interface ClusterCardProps {
  summary: ClusterSummary;
  /** Live WS metrics for this cluster; CPU/memory bars show a placeholder until the first message arrives. */
  metrics?: AggregatedMetrics | undefined;
}

const statusDotClass: Record<ClusterResponse["status"], string> = {
  online: "bg-emerald-500 shadow-[0_0_8px_2px] shadow-emerald-500/40",
  degraded: "bg-amber-500 shadow-[0_0_8px_2px] shadow-amber-500/40",
  offline: "bg-red-500 shadow-[0_0_8px_2px] shadow-red-500/40",
  inactive: "bg-muted-foreground/50",
  unknown: "bg-muted-foreground/50",
};

function UtilBar({
  label,
  percent,
  barClass,
}: {
  label: string;
  percent: number | null;
  barClass: string;
}) {
  const clamped =
    percent === null ? null : Math.min(100, Math.max(0, percent));
  const fillClass =
    clamped === null
      ? barClass
      : clamped >= 90
        ? "bg-red-500"
        : clamped >= 75
          ? "bg-amber-500"
          : barClass;

  return (
    <div className="flex items-center gap-2.5">
      <span className="w-14 shrink-0 text-xs text-muted-foreground">
        {label}
      </span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-foreground/10">
        {clamped !== null && (
          <div
            className={cn(
              "h-full rounded-full transition-[width] duration-700",
              fillClass,
            )}
            style={{ width: `${String(Math.round(clamped))}%` }}
          />
        )}
      </div>
      <span className="w-9 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
        {clamped === null ? "—" : `${String(Math.round(clamped))}%`}
      </span>
    </div>
  );
}

export function ClusterCard({ summary, metrics }: ClusterCardProps) {
  const navigate = useNavigate();
  const { t } = useTranslation("dashboard");
  const {
    cluster,
    nodeCount,
    nodesOnline,
    vmCount,
    containerCount,
    storageTotalBytes,
    storageUsedBytes,
  } = summary;

  const cpuPercent = metrics ? metrics.cpuPercent : null;
  const memPercent = metrics ? metrics.memPercent : null;
  const storagePercent =
    storageTotalBytes > 0
      ? (storageUsedBytes / storageTotalBytes) * 100
      : null;

  const dotCount = Math.min(nodeCount, 8);

  return (
    <Card
      className="cursor-pointer p-4 transition-all hover:-translate-y-0.5 hover:border-muted-foreground/30 hover:shadow-md"
      onClick={() => {
        void navigate(`/clusters/${cluster.id}`);
      }}
      data-testid="cluster-card"
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "h-2 w-2 shrink-0 rounded-full",
            statusDotClass[cluster.status],
          )}
        />
        <span className="truncate text-base font-semibold tracking-tight">
          {cluster.name}
        </span>
        {cluster.pve_version !== "" && (
          <span className="max-w-24 shrink-0 truncate rounded-md border px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground">
            PVE {cluster.pve_version}
          </span>
        )}
        <div className="ml-auto shrink-0">
          <ClusterStatusBadge status={cluster.status} />
        </div>
      </div>

      <div className="mt-4 space-y-2">
        <UtilBar label={t("cpu")} percent={cpuPercent} barClass="bg-sky-500" />
        <UtilBar
          label={t("memory")}
          percent={memPercent}
          barClass="bg-violet-500"
        />
        <UtilBar
          label={t("storage")}
          percent={storagePercent}
          barClass="bg-emerald-500"
        />
      </div>

      <div className="mt-4 flex items-center gap-2 text-xs text-muted-foreground">
        <span className="flex items-center gap-1" data-testid="node-dots">
          {Array.from({ length: dotCount }, (_, i) => (
            <span
              key={i}
              className={cn(
                "h-1.5 w-1.5 rounded-full",
                i < nodesOnline ? "bg-emerald-500" : "bg-red-500",
              )}
            />
          ))}
          {nodeCount > dotCount && <span>+{nodeCount - dotCount}</span>}
        </span>
        <span>
          {nodeCount} {nodeCount === 1 ? "node" : "nodes"}
        </span>
        <span className="ml-auto flex items-center gap-1.5 truncate">
          <span>
            {vmCount} {vmCount === 1 ? "VM" : "VMs"}
          </span>
          <span className="text-border">·</span>
          <span>
            {containerCount} {containerCount === 1 ? "CT" : "CTs"}
          </span>
          <span className="text-border">·</span>
          <span>{formatBytes(storageTotalBytes)}</span>
        </span>
      </div>
    </Card>
  );
}
