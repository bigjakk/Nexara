import { useMemo } from "react";

import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  GuestCells,
  GuestCellSwatch,
} from "@/features/dashboard/components/GuestCell";
import {
  fleetGuestFromRow,
  type FleetGuest,
} from "@/features/dashboard/lib/fleet";
import { InventoryUnavailableNote } from "@/features/inventory/components/InventoryUnavailableNote";
import type { InventoryRow } from "@/features/inventory/types/inventory";
import { summariseNodeGuests } from "./node-guest-summary";

/**
 * Who is on the node, at a glance: one cell per guest, shaded by how busy it
 * is, each a link to the guest. The table of them is the VMs tab.
 */
export function NodeGuestsPanel({
  rows,
  loading,
  unavailable,
  onViewAll,
}: {
  /** The inventory's rows for the guests resident on this node. */
  rows: readonly InventoryRow[];
  /** The inventory has not answered yet: an empty list is not "no guests". */
  loading: boolean;
  /** The cluster's inventory could not be read. */
  unavailable: boolean;
  onViewAll: () => void;
}) {
  // In VMID order, as the cluster page's Nodes table draws them.
  const guests = useMemo(() => {
    const list: FleetGuest[] = [];
    for (const row of rows) {
      const guest = fleetGuestFromRow(row);
      if (guest) list.push(guest);
    }
    return list.sort((a, b) => a.vmid - b.vmid);
  }, [rows]);
  const known = !loading && !unavailable;
  const summary = known ? summariseNodeGuests(rows) : "";

  return (
    <Panel
      title="Guests on this node"
      aside={summary !== "" ? summary : undefined}
      actions={
        <Button
          variant="link"
          size="sm"
          className="h-auto p-0"
          onClick={onViewAll}
        >
          View all
        </Button>
      }
    >
      {unavailable ? (
        <div className="[&>p]:mb-0">
          <InventoryUnavailableNote />
        </div>
      ) : loading ? (
        <Skeleton className="h-5 w-48" />
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
          <GuestCells guests={guests} />
          {guests.length > 0 && (
            <div
              aria-hidden="true"
              className="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground"
            >
              <span className="flex items-center gap-1.5">
                CPU: idle
                <span className="flex gap-0.5">
                  {([1, 2, 3, 4] as const).map((level) => (
                    <GuestCellSwatch key={level} load={level} />
                  ))}
                </span>
                busy
              </span>
              <span className="flex items-center gap-1.5">
                <GuestCellSwatch />
                stopped
              </span>
              <span className="flex items-center gap-1.5">
                <GuestCellSwatch load={2} container />
                container
              </span>
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}
