import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Server, Monitor, Box, HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

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
}

const stats = [
  {
    key: "nodes",
    labelKey: "nodes",
    icon: Server,
    iconClass: "bg-sky-500/10 text-sky-500",
  },
  {
    key: "vms",
    labelKey: "virtualMachines",
    icon: Monitor,
    iconClass: "bg-emerald-500/10 text-emerald-500",
  },
  {
    key: "containers",
    labelKey: "containers",
    icon: Box,
    iconClass: "bg-violet-500/10 text-violet-500",
  },
  {
    key: "storage",
    labelKey: "totalStorage",
    icon: HardDrive,
    iconClass: "bg-amber-500/10 text-amber-500",
  },
] as const;

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
}: StatsOverviewProps) {
  const { t } = useTranslation("dashboard");
  const storagePercent =
    totalStorageBytes > 0
      ? Math.round((totalStorageUsedBytes / totalStorageBytes) * 100)
      : 0;

  const values: Record<string, string> = {
    nodes: String(totalNodes),
    vms: String(totalVMs),
    containers: String(totalContainers),
    storage: formatBytes(totalStorageBytes),
  };

  const sublines: Record<string, ReactNode> = {
    nodes: (
      <>
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
      </>
    ),
    vms: t("statRunningStopped", {
      running: totalVMsRunning,
      stopped: totalVMs - totalVMsRunning,
    }),
    containers: t("statRunning", { running: totalContainersRunning }),
    storage: t("statUsedPercent", { percent: storagePercent }),
  };

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      {stats.map((stat) => {
        const Icon = stat.icon;
        return (
          <Card key={stat.key} className="p-4">
            <div className="flex items-center gap-2.5">
              <span
                className={cn(
                  "flex h-7 w-7 shrink-0 items-center justify-center rounded-md",
                  stat.iconClass,
                )}
              >
                <Icon className="h-4 w-4" />
              </span>
              <span className="truncate text-xs font-medium text-muted-foreground">
                {t(stat.labelKey)}
              </span>
            </div>
            {isLoading ? (
              <Skeleton
                className="mt-3 h-8 w-20"
                data-testid="stat-skeleton"
              />
            ) : (
              <>
                <div className="mt-3 text-2xl font-semibold tabular-nums tracking-tight">
                  {values[stat.key]}
                </div>
                <div className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                  {sublines[stat.key]}
                </div>
              </>
            )}
          </Card>
        );
      })}
    </div>
  );
}
