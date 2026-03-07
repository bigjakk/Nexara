import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useWebSocketStore } from "@/stores/websocket-store";
import type { ProxDashEvent } from "@/types/ws";

const DEBOUNCE_MS = 300;

/**
 * Subscribes to WS event channels and invalidates TanStack Query caches
 * when backend state changes. Replaces aggressive polling with push-driven
 * updates while keeping long-interval polling as a safety fallback.
 */
export function useEventInvalidation(clusterIds: string[]): void {
  const queryClient = useQueryClient();
  const subscribe = useWebSocketStore((s) => s.subscribe);
  const unsubscribe = useWebSocketStore((s) => s.unsubscribe);

  // Accumulate query keys to invalidate, then flush after debounce.
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
      const event = payload as ProxDashEvent;
      if (!event.kind) return;

      const cid = event.cluster_id;

      switch (event.kind) {
        case "task_created":
        case "task_update":
          scheduleInvalidation(["recent-activity"]);
          break;

        case "audit_entry":
          scheduleInvalidation(["audit-log"], ["recent-activity"]);
          break;

        case "vm_state_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "vms"],
              ["clusters", cid, "containers"],
            );
          }
          break;

        case "inventory_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "vms"],
              ["clusters", cid, "vmids"],
              ["clusters", cid, "containers"],
            );
          }
          break;

        case "migration_update":
          scheduleInvalidation(["migrations"], ["recent-activity"]);
          if (cid) {
            scheduleInvalidation(["clusters", cid, "vms"]);
          }
          break;

        case "drs_action":
          scheduleInvalidation(["drs"], ["recent-activity"]);
          if (cid) {
            scheduleInvalidation(["clusters", cid, "vms"]);
          }
          break;

        case "pbs_change":
          scheduleInvalidation(
            ["pbs-servers"],
            ["backup-coverage"],
            ["audit-log"],
            ["recent-activity"],
          );
          break;
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
