/**
 * Global "task tracker" store. Each tracked task represents a long-running
 * mutation (power action, snapshot create/restore/delete, etc.) that the
 * user fired but whose completion needs to be surfaced as a popup.
 *
 * The store is strategy-agnostic — it doesn't know HOW completion is
 * detected. Different mutation wirings use different completion strategies:
 *
 *   - **Power actions** use WS event correlation. The task is created
 *     with `wsExpect = { kind, clusterId, vmId }` and a separate hook
 *     (`useTaskCompletion`) watches the WS for matching events and calls
 *     `complete(id)` when one arrives. This is the *honest* path —
 *     `task_watcher.go` on the backend polls Proxmox to actual completion
 *     before publishing the event, so when we receive it the operation
 *     is genuinely done.
 *
 *   - **Snapshots** use direct completion. The task is created with
 *     `wsExpect = undefined` and the calling code calls `complete(id)`
 *     directly in the mutation's `onSuccess` (after a min-display delay
 *     so the user actually sees the banner). The event the backend
 *     publishes for snapshots fires immediately on dispatch, so WS
 *     correlation would lie about completion. This is the "approximate"
 *     path — the banner says complete a few seconds after dispatch, but
 *     Proxmox may still be working in the background. Documented as a
 *     known limitation; the proper fix is a backend change to add a
 *     task_watcher for snapshot handlers.
 *
 * 90-second timeout safety net: if a task has been pending for more
 * than 90 seconds with no completion call, auto-complete with a special
 * "no confirmation" state. Prevents stuck spinners if the WS connection
 * drops or the backend fails to publish.
 *
 * Auto-cleanup: completed (success) tasks auto-dismiss after 2.5 seconds.
 * Failed tasks stay until manually dismissed.
 */

import { create } from "zustand";

export type TaskState = "pending" | "success" | "error" | "no_confirmation";

/**
 * Hint that lets `useTaskCompletion` correlate WS events back to a
 * pending task. When undefined, the task is responsible for completing
 * itself via direct calls (e.g. snapshot mutations).
 */
export interface TaskWsExpect {
  kind: "vm_state_change" | "inventory_change";
  clusterId: string;
  /** Either the VM/CT row UUID (for vm_state_change) or undefined for cluster-wide events. */
  vmId?: string;
}

export interface TrackedTask {
  id: string;
  /** What we show while the task is pending — usually present-tense ("Rebooting web-prod-01"). */
  pendingLabel: string;
  /** What we show on success — usually past-tense ("web-prod-01 rebooted"). Falls back to pendingLabel if undefined. */
  successLabel?: string;
  state: TaskState;
  errorMessage?: string;
  startedAt: number;
  wsExpect?: TaskWsExpect;
}

interface TaskTrackerState {
  tasks: TrackedTask[];
  start: (input: {
    pendingLabel: string;
    successLabel?: string;
    wsExpect?: TaskWsExpect;
  }) => string;
  complete: (id: string) => void;
  fail: (id: string, message: string) => void;
  dismiss: (id: string) => void;
  /**
   * Wipe all pending tasks and their timers. Called on logout so a
   * previous user's in-flight power/snapshot actions don't leak into
   * the next session on the same device. Security review R2-H4 /
   * R2-L5.
   */
  clear: () => void;
  /**
   * Internal: returns currently pending tasks whose wsExpect matches the
   * given event shape. Used by `useTaskCompletion`. Returns the task IDs.
   */
  findMatching: (
    kind: TaskWsExpect["kind"],
    clusterId: string,
    vmId: string | undefined,
  ) => string[];
}

const TIMEOUT_MS = 90_000;
const SUCCESS_AUTO_DISMISS_MS = 2_500;
const NO_CONFIRMATION_AUTO_DISMISS_MS = 4_000;

// Per-task timer handles (timeout safety net + auto-dismiss). Kept
// outside Zustand state because they're not serializable and don't
// affect rendering.
const timeouts = new Map<string, ReturnType<typeof setTimeout>>();
const autoDismissTimers = new Map<string, ReturnType<typeof setTimeout>>();

function clearTimer(map: Map<string, ReturnType<typeof setTimeout>>, id: string) {
  const t = map.get(id);
  if (t !== undefined) {
    clearTimeout(t);
    map.delete(id);
  }
}

function nextId(): string {
  return `task-${String(Date.now())}-${Math.random().toString(36).slice(2, 8)}`;
}

export const useTaskTrackerStore = create<TaskTrackerState>((set, get) => ({
  tasks: [],

  start: (input) => {
    const id = nextId();
    const task: TrackedTask = {
      id,
      pendingLabel: input.pendingLabel,
      ...(input.successLabel !== undefined && {
        successLabel: input.successLabel,
      }),
      state: "pending",
      startedAt: Date.now(),
      ...(input.wsExpect !== undefined && { wsExpect: input.wsExpect }),
    };
    set((s) => ({ tasks: [...s.tasks, task] }));

    // 90-second safety net — auto-mark as no_confirmation if nothing
    // calls complete()/fail() in time.
    const timeout = setTimeout(() => {
      const stillPending = get().tasks.find(
        (t) => t.id === id && t.state === "pending",
      );
      if (!stillPending) return;
      set((s) => ({
        tasks: s.tasks.map((t) =>
          t.id === id ? { ...t, state: "no_confirmation" } : t,
        ),
      }));
      // Auto-dismiss the no-confirmation badge after a few seconds.
      const dismiss = setTimeout(() => {
        get().dismiss(id);
      }, NO_CONFIRMATION_AUTO_DISMISS_MS);
      autoDismissTimers.set(id, dismiss);
      timeouts.delete(id);
    }, TIMEOUT_MS);
    timeouts.set(id, timeout);

    return id;
  },

  complete: (id) => {
    const task = get().tasks.find((t) => t.id === id);
    if (!task || task.state !== "pending") return;
    clearTimer(timeouts, id);
    set((s) => ({
      tasks: s.tasks.map((t) =>
        t.id === id ? { ...t, state: "success" } : t,
      ),
    }));
    const dismiss = setTimeout(() => {
      get().dismiss(id);
    }, SUCCESS_AUTO_DISMISS_MS);
    autoDismissTimers.set(id, dismiss);
  },

  fail: (id, message) => {
    const task = get().tasks.find((t) => t.id === id);
    if (!task) return;
    clearTimer(timeouts, id);
    // Errors are sticky — no auto-dismiss. User must tap to dismiss.
    set((s) => ({
      tasks: s.tasks.map((t) =>
        t.id === id ? { ...t, state: "error", errorMessage: message } : t,
      ),
    }));
  },

  dismiss: (id) => {
    clearTimer(timeouts, id);
    clearTimer(autoDismissTimers, id);
    set((s) => ({ tasks: s.tasks.filter((t) => t.id !== id) }));
  },

  clear: () => {
    for (const [, t] of timeouts) clearTimeout(t);
    for (const [, t] of autoDismissTimers) clearTimeout(t);
    timeouts.clear();
    autoDismissTimers.clear();
    set({ tasks: [] });
  },

  findMatching: (kind, clusterId, vmId) => {
    const out: string[] = [];
    for (const t of get().tasks) {
      if (t.state !== "pending") continue;
      if (!t.wsExpect) continue;
      if (t.wsExpect.kind !== kind) continue;
      if (t.wsExpect.clusterId !== clusterId) continue;
      // If the task expects a specific vmId, require an exact match.
      // If the task expects any (vmId undefined), accept any matching event.
      if (t.wsExpect.vmId !== undefined && t.wsExpect.vmId !== vmId) continue;
      out.push(t.id);
    }
    return out;
  },
}));
