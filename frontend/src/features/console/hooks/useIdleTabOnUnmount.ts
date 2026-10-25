import { useEffect, useRef } from "react";
import { useConsoleStoreApi } from "@/stores/console-store";
import type { ConsoleStatus } from "../types/console";

/**
 * Statuses that claim a live socket, and so cannot survive the viewer.
 * Typed, so a status that stops existing fails to compile here.
 */
const LIVE_STATUSES: ReadonlySet<ConsoleStatus> = new Set<ConsoleStatus>([
  "connected",
  "connecting",
  "reconnecting",
]);

/**
 * Reset this tab to "idle" when the viewer goes away.
 *
 * A viewer that unmounts (console closed, tab removed) leaves no socket
 * behind, so the tab must not stay marked live. Reset it to the same pre-dial
 * state a page reload leaves it in and let whoever mounts next dial from a
 * clean slate. Settled states — "disconnected", "error" and the parked
 * "guest-stopped" — remain true with no socket, so they stand.
 *
 * Two things about the shape are load-bearing:
 *
 * - **Empty deps, tabId and the store read through refs.** The cleanup must
 *   run exactly once, at unmount. Listing tabId would make it fire on every id
 *   change instead — harmless today, since the floating console keys its
 *   viewers by tab id and a console page's tab id never changes, but it is not
 *   what this is for.
 * - **Call it after the connect effect.** React runs cleanups in the order the
 *   effects were defined, so this one lands after the socket teardown and
 *   synchronously before any replacement mounts, and cannot stamp "idle" on
 *   top of a newer connection's status the way an inline teardown write did.
 */
export function useIdleTabOnUnmount(tabId: string) {
  const tabIdRef = useRef(tabId);
  tabIdRef.current = tabId;
  const store = useConsoleStoreApi();
  const storeRef = useRef(store);
  storeRef.current = store;

  useEffect(
    () => () => {
      const id = tabIdRef.current;
      const { tabs, updateTabStatus } = storeRef.current.getState();
      const status = tabs.find((t) => t.id === id)?.status;
      if (status !== undefined && LIVE_STATUSES.has(status)) {
        updateTabStatus(id, "idle");
      }
    },
    [],
  );
}
