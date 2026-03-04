import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Cpu, MemoryStick, HardDrive, Network } from "lucide-react";
import { formatPercent, formatBytesPerSecond } from "@/lib/format";
import type { AggregatedMetrics } from "@/types/ws";

interface LiveMetricCardsProps {
  metrics: AggregatedMetrics | undefined;
}

export function LiveMetricCards({ metrics }: LiveMetricCardsProps) {
  const cards = [
    {
      key: "cpu",
      label: "CPU Usage",
      icon: Cpu,
      value: metrics ? formatPercent(metrics.cpuPercent) : null,
    },
    {
      key: "memory",
      label: "Memory Usage",
      icon: MemoryStick,
      value: metrics ? formatPercent(metrics.memPercent) : null,
    },
    {
      key: "disk",
      label: "Disk I/O",
      icon: HardDrive,
      value: metrics
        ? `${formatBytesPerSecond(metrics.diskReadBps)} / ${formatBytesPerSecond(metrics.diskWriteBps)}`
        : null,
    },
    {
      key: "network",
      label: "Network",
      icon: Network,
      value: metrics
        ? `${formatBytesPerSecond(metrics.netInBps)} / ${formatBytesPerSecond(metrics.netOutBps)}`
        : null,
    },
  ] as const;

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      {cards.map((card) => {
        const Icon = card.icon;
        return (
          <Card key={card.key}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{card.label}</CardTitle>
              <Icon className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              {card.value === null ? (
                <Skeleton className="h-7 w-24" data-testid="metric-skeleton" />
              ) : (
                <div className="text-lg font-semibold">{card.value}</div>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
