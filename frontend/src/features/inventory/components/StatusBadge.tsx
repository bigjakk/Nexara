import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { ResourceStatus } from "../types/inventory";

const statusConfig: Record<ResourceStatus, { label: string; className: string }> = {
  running: { label: "Running", className: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400" },
  online: { label: "Online", className: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400" },
  stopped: { label: "Stopped", className: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400" },
  offline: { label: "Offline", className: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400" },
  paused: { label: "Paused", className: "border-yellow-500/30 bg-yellow-500/10 text-yellow-700 dark:text-yellow-400" },
  suspended: { label: "Suspended", className: "border-yellow-500/30 bg-yellow-500/10 text-yellow-700 dark:text-yellow-400" },
  unknown: { label: "Unknown", className: "border-muted-foreground/30 bg-muted text-muted-foreground" },
};

interface StatusBadgeProps {
  status: ResourceStatus;
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const config = statusConfig[status];
  return (
    <Badge variant="outline" className={cn("text-xs font-medium", config.className)}>
      {config.label}
    </Badge>
  );
}
