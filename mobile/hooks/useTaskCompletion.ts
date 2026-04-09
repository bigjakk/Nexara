/**
 * Subscribes to backend WS event channels and completes any pending
 * task in the task tracker store whose `wsExpect` matches the event.
 *
 * Pairs with `useTaskTrackerStore.start({ wsExpect: ... })` — the store
 * is strategy-agnostic and doesn't watch the WS itself; this hook is
 * the bridge that turns matching events into `complete()` calls.
 *
 * Mounted once at the (app) layout level alongside `useEventInvalidation`.
 * Subscribes to the same channels (`system:events` + `cluster:<id>:events`
 * for every cluster the user can see).
 *
 * Currently correlates on:
 *   - `vm_state_change` — fired by `task_watcher.go` AFTER Proxmox
 *     finishes a power action. The event arrives ~2-30 seconds after
 *     dispatch depending on Proxmox completion time. This is the
 *     "honest" path used by power actions.
 *
 * Snapshots are NOT correlated here. The backend publishes
 * `vm_state_change` for snapshot ops inline (right after dispatch),
 * not after Proxmox completes — so correlating on that event would lie
 * about completion timing. The snapshot mutation wirings call
 * `tracker.complete()` directly with a min-display delay instead.
 */

import { useCallback, useEffect } from "react";

import { useWsStore } from "@/stores/ws-store";
import { useTaskTrackerStore } from "@/stores/task-tracker-store";
import type { NexaraEvent } from "@/features/api/ws-types";

/**
 * Map sanitized backend error codes (see `task_watcher.go::ErrTask*` and
 * security review H2) to friendly user-facing labels. Anything not in
 * the map falls back to the raw code so a future backend code that the
 * mobile build doesn't know about still surfaces something useful.
 */
const ERROR_CODE_LABELS: Record<string, string> = {
  task_query_failed: "Couldn't reach Proxmox to check on the task.",
  task_failed: "The Proxmox task reported a failure.",
  task_timeout: "The Proxmox task didn't complete in time.",
  db_update_failed:
    "Proxmox finished the task but Nexara couldn't persist the new state.",
};

function friendlyErrorLabel(code: string): string {
  return ERROR_CODE_LABELS[code] ?? code;
}

export function useTaskCompletion(clusterIds: string[]): void {
  const subscribe = useWsStore((s) => s.subscribe);
  const unsubscribe = useWsStore((s) => s.unsubscribe);

  const handleEvent = useCallback((payload: unknown) => {
    const event = payload as Partial<NexaraEvent>;
    if (!event.kind || !event.cluster_id) return;

    // We only correlate on vm_state_change for now. The action field
    // discriminates between real power-action completions and the
    // inline snapshot events — snapshot events have action ===
    // "snapshot_create" / "snapshot_delete" / "snapshot_rollback" and
    // we deliberately ignore them here because they're dispatched
    // before Proxmox actually completes the work.
    if (event.kind !== "vm_state_change") return;

    const action = event.action ?? "";
    if (
      action === "snapshot_create" ||
      action === "snapshot_delete" ||
      action === "snapshot_rollback"
    ) {
      return;
    }

    const matches = useTaskTrackerStore
      .getState()
      .findMatching("vm_state_change", event.cluster_id, event.resource_id);
    // Backend carries a non-empty `error` field on the vm_state_change
    // event when the underlying Proxmox task exited non-OK (see
    // task_watcher.go::ClusterEventWithError). Flip matching tasks to
    // failed instead of success so the task bar surfaces a friendly
    // label rendered from the sanitized backend code (per security
    // review H2 — the raw Proxmox text stays server-side).
    const errorCode = event.error ?? "";
    for (const id of matches) {
      if (errorCode) {
        useTaskTrackerStore
          .getState()
          .fail(id, friendlyErrorLabel(errorCode));
      } else {
        useTaskTrackerStore.getState().complete(id);
      }
    }
  }, []);

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
    };
  }, [clusterIds, subscribe, unsubscribe, handleEvent]);
}
