import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { StatusIcon } from "@/components/StatusIcon";

// Keyed by string on purpose: the steady states are enumerated here, but PVE
// also reports transient QEMU states (prelaunch, postmigrate, shutdown,
// migrate, io-error, ...) that arrive as arbitrary strings.
const statusConfig: Record<string, { label: string; className: string }> = {
  running: { label: "Running", className: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400" },
  online: { label: "Online", className: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400" },
  stopped: { label: "Stopped", className: "border-muted-foreground/30 bg-muted text-muted-foreground" },
  offline: { label: "Offline", className: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400" },
  paused: { label: "Paused", className: "border-yellow-500/30 bg-yellow-500/10 text-yellow-700 dark:text-yellow-400" },
  suspended: { label: "Suspended", className: "border-yellow-500/30 bg-yellow-500/10 text-yellow-700 dark:text-yellow-400" },
  unknown: { label: "Unknown", className: "border-muted-foreground/30 bg-muted text-muted-foreground" },
};

interface StatusBadgeProps {
  // Intentionally string, not the closed ResourceStatus union: an unguarded
  // exhaustive-Record lookup crashed every page rendering a guest in a
  // transient state ("Cannot read properties of undefined").
  status: string;
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const config = statusConfig[status] ?? {
    // Unrecognized statuses are almost always in-transition states — show
    // them amber with the raw status as the label rather than crashing.
    label: status.charAt(0).toUpperCase() + status.slice(1),
    className:
      "border-yellow-500/30 bg-yellow-500/10 text-yellow-700 dark:text-yellow-400",
  };
  return (
    <Badge variant="outline" className={cn("gap-1.5 text-xs font-medium", config.className)}>
      <StatusIcon status={status} />
      {config.label}
    </Badge>
  );
}
