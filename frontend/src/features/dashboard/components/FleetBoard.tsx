import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { Card } from "@/components/ui/card";
import { StatusIcon } from "@/components/StatusIcon";
import { cn } from "@/lib/utils";
import { formatBytes } from "@/lib/format";
import { visibleClusterIssues } from "@/lib/health-issues";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import type { AggregatedMetrics } from "@/types/ws";
import type { DashboardData } from "../api/dashboard-queries";
import { buildFleet, type FleetNode } from "../lib/fleet";
import { GuestCells, GuestCellSwatch } from "./GuestCell";

/** One column template for the header line and every node row, from md up. */
const COLUMNS = "md:grid-cols-[9rem_11rem_17rem_minmax(0,1fr)]";

/**
 * A utilisation bar in the data colour, amber from 75% and red from 90% (the
 * thresholds the cluster cards use). No reading yet draws an empty track and a
 * dash, never 0%.
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
  const level =
    clamped === null || clamped < 75 ? "ok" : clamped < 90 ? "warn" : "err";
  return (
    <div className="flex items-center gap-2.5">
      {/* Named beside the bar on a phone, where there is no column header. */}
      <span className="w-12 shrink-0 text-xs text-muted-foreground md:sr-only">
        {label}
      </span>
      <div
        aria-hidden="true"
        className="h-1.5 min-w-8 flex-1 overflow-hidden rounded-full bg-foreground/10"
      >
        {clamped !== null && (
          <div
            className={cn(
              "h-full rounded-full transition-[width] duration-700",
              level === "err"
                ? "bg-red-500"
                : level === "warn"
                  ? "bg-amber-500"
                  : "bg-data",
            )}
            style={{ width: `${String(Math.round(clamped))}%` }}
          />
        )}
      </div>
      <span
        className={cn(
          "w-9 shrink-0 text-right text-xs",
          level === "err"
            ? "font-semibold text-red-700 dark:text-red-400"
            : level === "warn"
              ? "font-semibold text-amber-700 dark:text-amber-400"
              : "text-muted-foreground",
        )}
      >
        {clamped === null ? "—" : `${String(Math.round(clamped))}%`}
      </span>
      {detail !== undefined && (
        <span className="hidden w-28 shrink-0 truncate text-xs text-muted-foreground lg:inline">
          {detail}
        </span>
      )}
    </div>
  );
}

function NodeRow({ node }: { node: FleetNode }) {
  const { t } = useTranslation("dashboard");
  return (
    <div
      className={cn(
        "grid grid-cols-2 items-center gap-x-6 gap-y-1.5 border-t py-2 md:min-h-10 md:py-1",
        COLUMNS,
      )}
    >
      <Link
        to={node.href}
        className="col-span-2 flex min-w-0 items-center gap-2 text-sm font-medium hover:underline md:col-span-1"
      >
        <StatusIcon status={node.status} />
        <span className="truncate">{node.name}</span>
      </Link>
      <Meter label={t("cpu")} percent={node.cpuPercent} />
      <Meter
        label={t("memory")}
        percent={node.memPercent}
        detail={
          node.memUsedBytes === null
            ? undefined
            : t("fleetMemoryOf", {
                used: formatBytes(node.memUsedBytes),
                total: formatBytes(node.memTotalBytes),
              })
        }
      />
      <GuestCells guests={node.guests} className="col-span-2 md:col-span-1" />
    </div>
  );
}

interface FleetBoardProps {
  data: DashboardData;
  metrics: ReadonlyMap<string, AggregatedMetrics>;
}

/**
 * The estate at a glance: every node with its live load, and one cell per
 * guest shaded by how busy it is. Sits in a fixed-height grid cell, so it
 * scrolls inside when the estate outgrows the cell.
 */
export function FleetBoard({ data, metrics }: FleetBoardProps) {
  const { t } = useTranslation("dashboard");
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  const dismissedSet = new Set(dismissed);
  const mutedSet = new Set(mutedTypes);
  const fleet = buildFleet(
    data.clusters,
    data.nodesByCluster,
    data.vmsByCluster,
    metrics,
  );
  const issuesByCluster = new Map(
    data.clusters.map((s) => [
      s.cluster.id,
      visibleClusterIssues(
        s.cluster.id,
        s.cluster.issues ?? [],
        dismissedSet,
        mutedSet,
      ).length,
    ]),
  );

  return (
    <Card className="flex h-full flex-col overflow-hidden">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-5 pb-2 pt-4">
        <h2 className="text-[15px] font-semibold">{t("fleet")}</h2>
        <span className="text-sm text-muted-foreground">
          {[
            t("fleetNodeCount", { count: fleet.nodeCount }),
            t("fleetGuestCount", { count: fleet.guestCount }),
            t("fleetRunningCount", { count: fleet.guestsRunning }),
          ].join(", ")}
        </span>
        <div
          aria-hidden="true"
          className="ml-auto hidden items-center gap-3.5 text-xs text-muted-foreground lg:flex"
        >
          <span className="flex items-center gap-1.5">
            {t("fleetLegendCpu")}: {t("fleetLegendIdle")}
            <span className="flex gap-0.5">
              {([1, 2, 3, 4] as const).map((level) => (
                <GuestCellSwatch key={level} load={level} />
              ))}
            </span>
            {t("fleetLegendBusy")}
          </span>
          <span className="flex items-center gap-1.5">
            <GuestCellSwatch />
            {t("fleetLegendStopped")}
          </span>
          <span className="flex items-center gap-1.5">
            <GuestCellSwatch load={2} container />
            {t("fleetLegendContainer")}
          </span>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-3">
        <div
          aria-hidden="true"
          className={cn(
            "hidden gap-x-6 pb-1.5 text-xs text-muted-foreground md:grid",
            COLUMNS,
          )}
        >
          <span>{t("fleetColNode")}</span>
          <span>{t("cpu")}</span>
          <span>{t("memory")}</span>
          <span>{t("fleetColGuests")}</span>
        </div>

        {fleet.clusters.map((cluster) => {
          const issues = issuesByCluster.get(cluster.id) ?? 0;
          return (
            <section key={cluster.id} className="border-t border-input pb-2">
              <div className="flex min-h-11 flex-wrap items-center gap-x-3.5 gap-y-1 py-2 text-sm">
                <Link
                  to={cluster.href}
                  className="text-base font-semibold tracking-tight [font-stretch:108%] hover:underline"
                >
                  {cluster.name}
                </Link>
                {cluster.version !== "" && (
                  <span className="text-muted-foreground">
                    PVE {cluster.version}
                  </span>
                )}
                <span className="text-muted-foreground">
                  {t("fleetNodesOnline", {
                    online: cluster.nodesOnline,
                    count: cluster.nodeCount,
                  })}
                </span>
                <span className="text-muted-foreground">
                  {[
                    t("fleetVmCount", { count: cluster.vmCount }),
                    t("fleetContainerCount", { count: cluster.containerCount }),
                  ].join(", ")}
                </span>
                {issues > 0 && (
                  <span className="flex items-center gap-1 rounded-md bg-amber-500/15 px-1.5 py-0.5 text-xs font-semibold text-amber-700 dark:text-amber-400">
                    <AlertTriangle className="h-3 w-3" aria-hidden="true" />
                    {t("fleetIssueCount", { count: issues })}
                  </span>
                )}
                {cluster.storagePercent !== null && (
                  <div className="ml-auto flex items-center gap-2.5 text-muted-foreground">
                    <span>{t("storage")}</span>
                    <div
                      aria-hidden="true"
                      className="h-1.5 w-24 overflow-hidden rounded-full bg-foreground/10"
                    >
                      <div
                        className={cn(
                          "h-full rounded-full",
                          cluster.storagePercent >= 90
                            ? "bg-red-500"
                            : cluster.storagePercent >= 75
                              ? "bg-amber-500"
                              : "bg-data",
                        )}
                        style={{
                          width: `${String(Math.round(cluster.storagePercent))}%`,
                        }}
                      />
                    </div>
                    <span>
                      {t("fleetStorageOf", {
                        percent: Math.round(cluster.storagePercent),
                        total: formatBytes(cluster.storageTotalBytes),
                      })}
                    </span>
                  </div>
                )}
              </div>
              {cluster.nodes.map((node) => (
                <NodeRow key={node.id} node={node} />
              ))}
            </section>
          );
        })}
      </div>
    </Card>
  );
}
