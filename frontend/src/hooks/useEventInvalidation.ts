import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useWebSocketStore } from "@/stores/websocket-store";
import type { NexaraEvent } from "@/types/ws";

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
  // Prefix keys match hierarchically (TanStack default); exact keys match
  // only the literal key — used where a prefix would fan out too far.
  const pendingPrefixKeys = useRef<Set<string>>(new Set());
  const pendingExactKeys = useRef<Set<string>>(new Set());
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flush = useCallback(() => {
    const prefixKeys = Array.from(pendingPrefixKeys.current);
    const exactKeys = Array.from(pendingExactKeys.current);
    pendingPrefixKeys.current.clear();
    pendingExactKeys.current.clear();
    timerRef.current = null;

    for (const serialized of prefixKeys) {
      const key = JSON.parse(serialized) as string[];
      void queryClient.invalidateQueries({ queryKey: key });
    }
    for (const serialized of exactKeys) {
      const key = JSON.parse(serialized) as string[];
      void queryClient.invalidateQueries({ queryKey: key, exact: true });
    }
  }, [queryClient]);

  const armFlush = useCallback(() => {
    if (timerRef.current === null) {
      timerRef.current = setTimeout(flush, DEBOUNCE_MS);
    }
  }, [flush]);

  const scheduleInvalidation = useCallback(
    (...queryKeys: string[][]) => {
      for (const key of queryKeys) {
        pendingPrefixKeys.current.add(JSON.stringify(key));
      }
      armFlush();
    },
    [armFlush],
  );

  const scheduleExactInvalidation = useCallback(
    (...queryKeys: string[][]) => {
      for (const key of queryKeys) {
        pendingExactKeys.current.add(JSON.stringify(key));
      }
      armFlush();
    },
    [armFlush],
  );

  const handleEvent = useCallback(
    (payload: unknown) => {
      const event = payload as Partial<NexaraEvent>;
      if (!event.kind) return;

      const cid = event.cluster_id;

      switch (event.kind) {
        case "task_created":
        case "task_update":
          scheduleInvalidation(["recent-activity"], ["tasks"]);
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
            // Exact-match the clusters list and this cluster's detail row;
            // scoped prefixes for the affected collections. A bare
            // ["clusters"] prefix here used to refetch every cluster-scoped
            // query in the whole app on any VM change anywhere.
            scheduleExactInvalidation(["clusters"], ["clusters", cid]);
            scheduleInvalidation(
              ["clusters", cid, "nodes"],
              ["clusters", cid, "vms"],
              ["clusters", cid, "vmids"],
              ["clusters", cid, "containers"],
              ["clusters", cid, "storage"],
              ["clusters", cid, "vm-folders"],
              ["clusters", cid, "pools"],
              ["clusters", cid, "ha"],
              ["clusters", cid, "backup-jobs"],
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

        case "cve_scan":
          if (cid) {
            scheduleInvalidation(
              ["cve-scans", cid],
              ["security-posture", cid],
            );
          }
          break;

        case "alert_fired":
        case "alert_state_change":
          scheduleInvalidation(
            ["alerts"],
            ["alert-rules"],
            ["alert-summary"],
          );
          if (cid) {
            scheduleInvalidation(
              ["cluster-alerts", cid],
              ["cluster-alert-count", cid],
            );
          }
          break;

        case "rolling_update":
          if (cid) {
            scheduleInvalidation(
              ["rolling-update-jobs", cid],
              ["rolling-update-job"],
              ["rolling-update-nodes"],
            );
          }
          break;

        case "ha_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "ha"],
            );
          }
          break;

        case "pool_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "pools"],
            );
          }
          break;

        case "replication_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "replication"],
            );
          }
          break;

        case "acme_change":
          if (cid) {
            scheduleInvalidation(
              ["clusters", cid, "acme"],
            );
          }
          break;
      }
    },
    [scheduleInvalidation, scheduleExactInvalidation],
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
