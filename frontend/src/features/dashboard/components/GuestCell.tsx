import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import type { FleetGuest, FleetLoad } from "../lib/fleet";

/**
 * A running guest's fill, idle to busy: the data colour at four strengths.
 * The palest fills are too faint to find on their own, so a running cell also
 * wears RUNNING_RING, which clears 3:1 against the card in both themes.
 */
const LOAD_CLASS: Record<FleetLoad, string> = {
  1: "bg-data/25",
  2: "bg-data/50",
  3: "bg-data/75",
  4: "bg-data",
};
const RUNNING_RING = "ring-1 ring-inset ring-data/80";
const STOPPED_RING = "ring-1 ring-inset ring-muted-foreground/70";

/** The cell's look without the link, for a legend. */
export function GuestCellSwatch({
  load,
  container = false,
}: {
  /** Omit for a stopped guest. */
  load?: FleetLoad;
  container?: boolean;
}) {
  return (
    <span
      className={cn(
        "h-3 w-3",
        container ? "rounded-full" : "rounded-[3px]",
        load === undefined ? STOPPED_RING : cn(LOAD_CLASS[load], RUNNING_RING),
      )}
    />
  );
}

/**
 * One guest as a small linked cell: a square is a VM and a circle a container
 * (so the type reads without colour), hollow is stopped, and a running one is
 * shaded by how busy it is.
 */
export function GuestCell({ guest }: { guest: FleetGuest }) {
  const { t } = useTranslation("dashboard");
  const label = guest.running
    ? guest.cpuPercent === null
      ? t("fleetGuestRunningNoMetric", { vmid: guest.vmid, name: guest.name })
      : t("fleetGuestRunning", {
          vmid: guest.vmid,
          name: guest.name,
          cpu: Math.round(guest.cpuPercent),
        })
    : t("fleetGuestOther", {
        vmid: guest.vmid,
        name: guest.name,
        status: guest.status,
      });
  return (
    <Link
      to={guest.href}
      title={label}
      aria-label={label}
      className={cn(
        "h-3.5 w-3.5 shrink-0 transition-transform hover:scale-125 pointer-coarse:h-5 pointer-coarse:w-5 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-card",
        guest.kind === "ct" ? "rounded-full" : "rounded-[3px]",
        guest.load !== null
          ? cn(LOAD_CLASS[guest.load], RUNNING_RING)
          : guest.status === "stopped"
            ? STOPPED_RING
            : // Paused, suspended, mid-migration: not running, not simply off.
              "ring-1 ring-inset ring-amber-600 dark:ring-amber-500",
      )}
    />
  );
}

/** A node's guests as a wrapping strip of cells, or a note that it has none. */
export function GuestCells({
  guests,
  className,
}: {
  guests: FleetGuest[];
  className?: string;
}) {
  const { t } = useTranslation("dashboard");
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-1 pointer-coarse:gap-2",
        className,
      )}
    >
      {guests.length === 0 ? (
        <span className="text-xs text-muted-foreground">
          {t("fleetNoGuests")}
        </span>
      ) : (
        guests.map((guest) => <GuestCell key={guest.id} guest={guest} />)
      )}
    </div>
  );
}
