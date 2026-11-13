import { cn } from "@/lib/utils";

interface StatusIconProps {
  status: string;
  /**
   * What the dot says to a screen reader, where the status word only picks
   * its colour: a UPS drawn as "offline" is in fact "Low battery".
   */
  label?: string | undefined;
  className?: string;
}

/**
 * Small status dot. Running/online is emerald, stopped is a hollow neutral
 * ring (a powered-off guest is not an error, and the shape says "off" without
 * relying on colour), offline is red (an unreachable node/cluster is), and
 * paused/transient states are amber.
 */
export function StatusIcon({
  status,
  label: givenLabel,
  className,
}: StatusIconProps) {
  const normalized = status.toLowerCase();

  const hollow = "ring-[1.5px] ring-inset ring-muted-foreground/70";
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
    colorClass = hollow;
    label = "Stopped";
  } else if (normalized === "offline") {
    colorClass = "bg-red-500";
    label = "Offline";
  } else if (
    normalized === "paused" ||
    normalized === "suspended" ||
    normalized === "degraded" ||
    // A node in HA maintenance: up, and deliberately taking no guests.
    normalized === "maintenance" ||
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
      aria-label={givenLabel ?? label}
      className={cn("h-2 w-2 shrink-0 rounded-full", colorClass, className)}
    />
  );
}
