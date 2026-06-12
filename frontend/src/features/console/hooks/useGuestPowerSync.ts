import { useEffect } from "react";
import { useVM } from "@/features/vms/api/vm-queries";
import { useConsoleStore } from "@/stores/console-store";
import type { ConsoleTab } from "../types/console";

/**
 * Keeps a console tab's lifecycle in sync with its guest's power state.
 *
 * - Parks a dead tab (disconnected/error) as "guest-stopped" when the guest
 *   is reported stopped, so the UI shows a powered-off message instead of a
 *   black screen.
 * - Auto-reconnects a parked tab when the guest comes back up. The vm query
 *   is invalidated by vm_state_change WS events, so this reacts within a
 *   couple of seconds of a power-on (60s polling as fallback).
 *
 * Tabs without a resourceId (e.g. node shells) are left untouched.
 * Returns the guest's current status ("" while unknown).
 */
export function useGuestPowerSync(tab: ConsoleTab): string {
  const { data } = useVM(tab.clusterID, tab.resourceId ?? "", tab.kind ?? "vm");
  const guestStatus = data?.status.toLowerCase() ?? "";
  const { id: tabId, status: tabStatus } = tab;
  const hasResource = tab.resourceId !== undefined && tab.kind !== undefined;

  useEffect(() => {
    if (!hasResource) return;
    const store = useConsoleStore.getState();
    if (tabStatus === "guest-stopped" && guestStatus === "running") {
      // resolveAndReconnect re-checks status and re-resolves the node, so a
      // guest that started on a different node reconnects correctly.
      void store.resolveAndReconnect(tabId);
    } else if (
      (tabStatus === "disconnected" || tabStatus === "error") &&
      guestStatus === "stopped"
    ) {
      store.updateTabStatus(tabId, "guest-stopped");
    }
  }, [hasResource, tabStatus, guestStatus, tabId]);

  return guestStatus;
}
