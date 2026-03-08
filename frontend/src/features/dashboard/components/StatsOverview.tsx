import { useTranslation } from "react-i18next";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Server, Monitor, Box, HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";

interface StatsOverviewProps {
  totalNodes: number;
  totalVMs: number;
  totalContainers: number;
  totalStorageBytes: number;
  isLoading: boolean;
}

const stats = [
  { key: "nodes", labelKey: "nodes", icon: Server },
  { key: "vms", labelKey: "virtualMachines", icon: Monitor },
  { key: "containers", labelKey: "containers", icon: Box },
  { key: "storage", labelKey: "totalStorage", icon: HardDrive },
] as const;

export function StatsOverview({
  totalNodes,
  totalVMs,
  totalContainers,
  totalStorageBytes,
  isLoading,
}: StatsOverviewProps) {
  const { t } = useTranslation("dashboard");
  const values: Record<string, string> = {
    nodes: String(totalNodes),
    vms: String(totalVMs),
    containers: String(totalContainers),
    storage: formatBytes(totalStorageBytes),
  };

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      {stats.map((stat) => {
        const Icon = stat.icon;
        return (
          <Card key={stat.key}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">
                {t(stat.labelKey)}
              </CardTitle>
              <Icon className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              {isLoading ? (
                <Skeleton className="h-8 w-20" data-testid="stat-skeleton" />
              ) : (
                <div className="text-2xl font-bold">{values[stat.key]}</div>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
