import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleX } from "lucide-react";
import { Card } from "@/components/ui/card";
import { ClusterStatusBadge } from "@/components/ClusterStatusBadge";
import { UsageBar } from "@/components/UsageBar";
import { worstIssueSeverity } from "@/features/ceph/lib/ceph-health";
import { visibleClusterIssues } from "@/lib/health-issues";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { VMResponse } from "@/types/api";
import type { AggregatedMetrics } from "@/types/ws";
import type { ClusterSummary } from "../api/dashboard-queries";
import { guestCounts } from "../lib/estate-summary";

interface ClusterCardProps {
  summary: ClusterSummary;
  /** Live WS metrics for this cluster; CPU/memory bars show a placeholder until the first message arrives. */
  metrics?: AggregatedMetrics | undefined;
  /**
   * The cluster's guest list. With it the VM and container counts come from
   * the list, templates left out, as the header and the Fleet widget count
   * them; the summary's totals are only the fallback without it.
   */
  guests?: readonly VMResponse[] | undefined;
}

/**
 * A labelled utilisation bar, its reading, and the exact amount on a line
 * under it. No reading yet draws an empty track and a dash, never 0%.
 */
function Meter({
  label,
  percent,
  detail,
}: {
  label: string;
  percent: number | null;
  detail?: string | undefined;
}) {
  const clamped = percent === null ? null : Math.min(100, Math.max(0, percent));
  const level = usageLevel(clamped);
  return (
    <div>
      <div className="flex items-center gap-2.5">
        <span className="w-14 shrink-0 text-xs text-muted-foreground">
          {label}
        </span>
        <UsageBar percent={clamped} className="flex-1" />
        <span
          className={cn(
            "w-9 shrink-0 text-right text-xs",
            level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
          )}
        >
          {clamped === null ? "—" : `${String(Math.round(clamped))}%`}
        </span>
      </div>
      {detail !== undefined && (
        <div className="text-right text-[11.5px] leading-tight text-muted-foreground/75">
          {detail}
        </div>
      )}
    </div>
  );
}

/**
 * One cluster in the Fleet board's language: its name and state, what it
 * holds, and how full it is. The whole panel opens the cluster.
 */
export function ClusterCard({ summary, metrics, guests }: ClusterCardProps) {
  const { t } = useTranslation("dashboard");
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  const {
    cluster,
    nodeCount,
    nodesOnline,
    storageTotalBytes,
    storageUsedBytes,
  } = summary;
  const counted = guests !== undefined ? guestCounts([guests]) : null;
  const vmCount = counted?.vms ?? summary.vmCount;
  const containerCount = counted?.containers ?? summary.containerCount;

  const cpuPercent = metrics ? metrics.cpuPercent : null;
  const memPercent = metrics ? metrics.memPercent : null;
  const storagePercent =
    storageTotalBytes > 0 ? (storageUsedBytes / storageTotalBytes) * 100 : null;

  // Respect dismissed/muted so the count matches what the health pill shows.
  const issues = visibleClusterIssues(
    cluster.id,
    cluster.issues ?? [],
    new Set(dismissed),
    new Set(mutedTypes),
  );
  const failed = worstIssueSeverity(issues) === "err";
  const IssueIcon = failed ? CircleX : AlertTriangle;

  return (
    <Card
      className="group relative px-[18px] py-3.5 focus-within:ring-2 focus-within:ring-ring"
      data-testid="cluster-card"
    >
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        {/* The link's ::after covers the panel, so a click anywhere on it
            opens the cluster and the keyboard still finds one named link. */}
        <Link
          to={`/clusters/${cluster.id}`}
          className="min-w-0 truncate text-base font-semibold tracking-tight outline-hidden [font-stretch:108%] after:absolute after:inset-0 after:rounded-[10px] group-hover:underline"
        >
          {cluster.name}
        </Link>
        {issues.length > 0 && (
          <span
            className={cn(
              "flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-semibold",
              failed
                ? "bg-red-500/15 text-red-700 dark:text-red-400"
                : "bg-amber-500/15 text-amber-700 dark:text-amber-400",
            )}
          >
            <IssueIcon className="h-3 w-3" aria-hidden="true" />
            {t("fleetIssueCount", { count: issues.length })}
          </span>
        )}
        <span className="ml-auto shrink-0">
          <ClusterStatusBadge status={cluster.status} />
        </span>
      </div>
      <p className="mt-0.5 text-[12.5px] text-muted-foreground">
        {[
          ...(cluster.pve_version !== "" ? [`PVE ${cluster.pve_version}`] : []),
          t("fleetNodesOnline", { online: nodesOnline, count: nodeCount }),
          [
            t("fleetVmCount", { count: vmCount }),
            t("fleetContainerCount", { count: containerCount }),
          ].join(", "),
        ].join(" · ")}
      </p>

      <div className="mt-3 space-y-1.5">
        <Meter label={t("cpu")} percent={cpuPercent} />
        <Meter
          label={t("memory")}
          percent={memPercent}
          detail={
            metrics && metrics.memTotal > 0
              ? t("statBytesUsedOf", {
                  used: formatBytes(metrics.memUsed),
                  total: formatBytes(metrics.memTotal),
                })
              : undefined
          }
        />
        <Meter
          label={t("storage")}
          percent={storagePercent}
          detail={
            storageTotalBytes > 0
              ? t("statBytesUsedOf", {
                  used: formatBytes(storageUsedBytes),
                  total: formatBytes(storageTotalBytes),
                })
              : undefined
          }
        />
      </div>
    </Card>
  );
}
