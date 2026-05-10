import { Play, Square, Pause, Circle } from "lucide-react";
import { cn } from "@/lib/utils";

interface StatusIconProps {
  status: string;
  className?: string;
}

export function StatusIcon({ status, className }: StatusIconProps) {
  const normalized = status.toLowerCase();

  if (normalized === "running" || normalized === "online" || normalized === "active") {
    return (
      <Play
        aria-label="Running"
        className={cn("h-3 w-3 shrink-0 text-green-600 dark:text-green-500", className)}
        fill="currentColor"
        strokeWidth={0}
      />
    );
  }

  if (normalized === "stopped" || normalized === "offline") {
    return (
      <Square
        aria-label="Stopped"
        className={cn("h-2.5 w-2.5 shrink-0 text-red-600 dark:text-red-500", className)}
        fill="currentColor"
        strokeWidth={0}
      />
    );
  }

  if (normalized === "paused" || normalized === "suspended" || normalized === "degraded") {
    return (
      <Pause
        aria-label={normalized}
        className={cn("h-3 w-3 shrink-0 text-yellow-600 dark:text-yellow-500", className)}
        fill="currentColor"
        strokeWidth={0}
      />
    );
  }

  return (
    <Circle
      aria-label="Unknown"
      className={cn("h-2 w-2 shrink-0 text-muted-foreground", className)}
      fill="currentColor"
      strokeWidth={0}
    />
  );
}
