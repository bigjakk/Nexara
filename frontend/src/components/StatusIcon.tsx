import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { isFaultState, isHaltedState } from "@/lib/guest-state";

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
 * paused/transient states are amber. A halted VM's state (lib/guest-state.ts)
 * is red when it is a fault (I/O error, panic, watchdog, internal error),
 * a quiet filled grey when asleep, and amber otherwise.
 */
export function StatusIcon({
  status,
  label: givenLabel,
  className,
}: StatusIconProps) {
  const { t } = useTranslation("vms");
  const normalized = status.toLowerCase();

  const hollow = "ring-[1.5px] ring-inset ring-muted-foreground/70";
  let colorClass = "bg-muted-foreground/40";
  let label = "Unknown";

  if (isHaltedState(normalized)) {
    colorClass = isFaultState(normalized)
      ? "bg-red-500"
      : normalized === "asleep"
        ? "bg-muted-foreground/70"
        : "bg-amber-500";
    label = t(`runState.${normalized}`);
  } else if (
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
