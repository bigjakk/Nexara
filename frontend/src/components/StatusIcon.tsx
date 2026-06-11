import { cn } from "@/lib/utils";

interface StatusIconProps {
  status: string;
  className?: string;
}

/**
 * Small status dot. Running/online is emerald, stopped is neutral (a powered-
 * off guest is not an error), offline is red (an unreachable node/cluster is),
 * and paused/transient states are amber.
 */
export function StatusIcon({ status, className }: StatusIconProps) {
  const normalized = status.toLowerCase();

  let colorClass = "bg-muted-foreground/40";
  let label = "Unknown";

  if (
    normalized === "running" ||
    normalized === "online" ||
    normalized === "active"
  ) {
    colorClass = "bg-emerald-500";
    label = "Running";
  } else if (normalized === "stopped") {
    colorClass = "bg-muted-foreground/40";
    label = "Stopped";
  } else if (normalized === "offline") {
    colorClass = "bg-red-500";
    label = "Offline";
  } else if (
    normalized === "paused" ||
    normalized === "suspended" ||
    normalized === "degraded" ||
    // Transient QEMU states — the guest is between steady states.
    normalized === "prelaunch" ||
    normalized === "postmigrate" ||
    normalized === "shutdown" ||
    normalized === "migrate"
  ) {
    colorClass = "bg-amber-500";
    label = normalized;
  }

  return (
    <span
      role="img"
      aria-label={label}
      className={cn("h-2 w-2 shrink-0 rounded-full", colorClass, className)}
    />
  );
}
