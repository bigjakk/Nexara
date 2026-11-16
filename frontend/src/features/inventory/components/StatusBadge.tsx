import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { StatusIcon } from "@/components/StatusIcon";
import { guestState, isFaultState, isHaltedState } from "@/lib/guest-state";

// Keyed by string on purpose: the steady states are enumerated here, but PVE
// also reports transient QEMU states (prelaunch, postmigrate, shutdown,
// migrate, io-error, ...) that arrive as arbitrary strings.
const statusConfig: Record<string, { label: string; className: string }> = {
  // Healthy states stay quiet: the dot carries the green, the chip does not.
  running: {
    label: "Running",
    className: "border-border bg-transparent text-foreground",
  },
  online: {
    label: "Online",
    className: "border-border bg-transparent text-foreground",
  },
  stopped: {
    label: "Stopped",
    className: "border-muted-foreground/30 bg-muted text-muted-foreground",
  },
  offline: {
    label: "Offline",
    className: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400",
  },
  paused: {
    label: "Paused",
    className:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  },
  suspended: {
    label: "Suspended",
    className:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  },
  unknown: {
    label: "Unknown",
    className: "border-muted-foreground/30 bg-muted text-muted-foreground",
  },
};

interface StatusBadgeProps {
  // Intentionally string, not the closed ResourceStatus union: an unguarded
  // exhaustive-Record lookup crashed every page rendering a guest in a
  // transient state ("Cannot read properties of undefined").
  status: string;
  /** A running VM's halted QEMU state (run_state), which it is shown as. */
  runState?: string | undefined;
}

const AMBER =
  "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400";
const QUIET = "border-muted-foreground/30 bg-muted text-muted-foreground";

export function StatusBadge({ status: given, runState }: StatusBadgeProps) {
  const { t } = useTranslation("vms");
  // One source for the state a guest is shown in (lib/guest-state.ts): a
  // paused or I/O-error VM reads as that, not as Running.
  const status = guestState(given, runState);
  const config = isHaltedState(status)
    ? {
        label: t(`runState.${status}`),
        className: isFaultState(status)
          ? "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400"
          : status === "asleep"
            ? QUIET
            : AMBER,
      }
    : (statusConfig[status] ?? {
        // Unrecognized statuses are almost always in-transition states — show
        // them amber with the raw status as the label rather than crashing.
        label: status.charAt(0).toUpperCase() + status.slice(1),
        className: AMBER,
      });
  return (
    <Badge
      variant="outline"
      className={cn("gap-1.5 text-xs font-medium", config.className)}
    >
      <StatusIcon status={status} />
      {config.label}
    </Badge>
  );
}
