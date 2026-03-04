import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { formatPercent } from "@/lib/format";
import type { TopConsumer } from "@/types/ws";

interface TopConsumersProps {
  consumers: TopConsumer[];
}

type SortField = "cpu" | "memory";

export function TopConsumers({ consumers }: TopConsumersProps) {
  const [sortBy, setSortBy] = useState<SortField>("cpu");

  const sorted = [...consumers].sort((a, b) => {
    if (sortBy === "cpu") return b.cpuPercent - a.cpuPercent;
    return b.memPercent - a.memPercent;
  });

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">Top Consumers</CardTitle>
        <div className="flex gap-1">
          <Button
            variant={sortBy === "cpu" ? "default" : "ghost"}
            size="sm"
            className="h-6 px-2 text-xs"
            onClick={() => { setSortBy("cpu"); }}
            data-testid="sort-cpu"
          >
            CPU
          </Button>
          <Button
            variant={sortBy === "memory" ? "default" : "ghost"}
            size="sm"
            className="h-6 px-2 text-xs"
            onClick={() => { setSortBy("memory"); }}
            data-testid="sort-memory"
          >
            Memory
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground" data-testid="empty-consumers">
            No VMs running
          </p>
        ) : (
          <div className="space-y-2" data-testid="consumer-list">
            {sorted.map((vm) => {
              const value = sortBy === "cpu" ? vm.cpuPercent : vm.memPercent;
              return (
                <div key={vm.vmId} className="space-y-1">
                  <div className="flex items-center justify-between text-xs">
                    <span className="truncate font-mono">{vm.vmId.slice(0, 8)}</span>
                    <span className="text-muted-foreground">
                      {formatPercent(value)}
                    </span>
                  </div>
                  <div className="h-1.5 w-full rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-primary transition-all"
                      style={{ width: `${Math.min(value, 100).toFixed(1)}%` }}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
