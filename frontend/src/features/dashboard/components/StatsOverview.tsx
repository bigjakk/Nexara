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
  { key: "nodes", label: "Nodes", icon: Server },
  { key: "vms", label: "Virtual Machines", icon: Monitor },
  { key: "containers", label: "Containers", icon: Box },
  { key: "storage", label: "Total Storage", icon: HardDrive },
] as const;

export function StatsOverview({
  totalNodes,
  totalVMs,
  totalContainers,
  totalStorageBytes,
  isLoading,
}: StatsOverviewProps) {
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
                {stat.label}
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
