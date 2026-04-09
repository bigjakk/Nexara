/**
 * Subscribes to backend event channels via WebSocket and invalidates
 * matching TanStack Query caches when state changes server-side.
 *
 * Mirrors `frontend/src/hooks/useEventInvalidation.ts` exactly so the same
 * event kinds map to the same query keys on web and mobile. The mobile app
 * can therefore consume any future event kinds added on the backend with
 * minimal extra work — just add the case here.
 */

import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { useWsStore } from "@/stores/ws-store";
import type { NexaraEvent } from "@/features/api/ws-types";

const DEBOUNCE_MS = 300;

export function useEventInvalidation(clusterIds: string[]): void {
  const queryClient = useQueryClient();
  const subscribe = useWsStore((s) => s.subscribe);
  const unsubscribe = useWsStore((s) => s.unsubscribe);

  const pendingKeys = useRef<Set<string>>(new Set());
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flush = useCallback(() => {
    const keys = Array.from(pendingKeys.current);
    pendingKeys.current.clear();
    timerRef.current = null;

    for (const serialized of keys) {
      const key = JSON.parse(serialized) as string[];
      void queryClient.invalidateQueries({ queryKey: key });
    }
  }, [queryClient]);

  const scheduleInvalidation = useCallback(
    (...queryKeys: string[][]) => {
      for (const key of queryKeys) {
        pendingKeys.current.add(JSON.stringify(key));
      }
      if (timerRef.current === null) {
        timerRef.current = setTimeout(flush, DEBOUNCE_MS);
      }
    },
    [flush],
  );

  const handleEvent = useCallback(
    (payload: unknown) => {
      const event = payload as Partial<NexaraEvent>;
      if (!event.kind) return;

      const cid = event.cluster_id;

      switch (event.kind) {
        case "vm_state_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "vms"],
              ["clusters", cid, "containers"],
            );
            // Also invalidate the single-VM query key when the event
            // carries a resource ID. Without this, the VM detail screen
            // waits for the next 10s polling interval before reflecting
            // the new status — which means PowerActions' "buttons stay
            // disabled until the button set swaps" UX breaks. The list
            // invalidation above only affects the VM list screen, not
            // the per-VM detail query. The query key shape matches
            // `queryKeys.vm(cid, vmId)` in features/api/query-keys.ts.
            if (event.resource_id) {
              scheduleInvalidation([
                "clusters",
                cid,
                "vm",
                event.resource_id,
              ]);
            }
          }
          break;

        case "inventory_change":
          scheduleInvalidation(["clusters"]);
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "nodes"],
              ["clusters", cid, "vms"],
              ["clusters", cid, "containers"],
            );
            // Same reasoning as vm_state_change above — if the event
            // targets a specific VM, also invalidate its detail query.
            if (event.resource_id) {
              scheduleInvalidation([
                "clusters",
                cid,
                "vm",
                event.resource_id,
              ]);
            }
          }
          break;

        case "alert_fired":
        case "alert_state_change":
          scheduleInvalidation(["alerts"], ["alert-summary"]);
          if (cid) {
            scheduleInvalidation(["cluster-alerts", cid]);
          }
          break;

        // M2 ignores task / audit / migration / DRS / PBS / CVE / rolling /
        // HA / pool / replication / ACME events because we don't show those
        // resources on mobile yet. Add cases here as new screens land.
      }
    },
    [scheduleInvalidation],
  );

  useEffect(() => {
    const channels: string[] = ["system:events"];
    for (const cid of clusterIds) {
      channels.push(`cluster:${cid}:events`);
    }

    for (const ch of channels) {
      subscribe(ch, handleEvent);
    }

    return () => {
      for (const ch of channels) {
        unsubscribe(ch, handleEvent);
      }
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [clusterIds, subscribe, unsubscribe, handleEvent]);
}
