import { describe, it, expect, afterEach, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";
import { useWebSocketStore } from "@/stores/websocket-store";
import { useEventInvalidation } from "./useEventInvalidation";

// Events are delivered through the REAL websocket store, the way its onmessage
// does it — each listener on the channel gets the message's payload — so the
// hook's own subscribe calls decide what it hears. What is asserted is the
// query cache: which queries end up stale, not which calls were made (a key is
// invalidated by any call whose key is a prefix of it).

const CLUSTER = "c0000000-0000-4000-8000-00000000000a";
const OTHER_CLUSTER = "c0000000-0000-4000-8000-00000000000b";
const UPID =
  "UPID:pve-01:0000A1B2:0001C3D4:66F4E3C0:qmsnapshot:100:nexara@pve!api:";

/** Hands payload to every listener on channel, as the store's onmessage does. */
function deliver(channel: string, payload: unknown) {
  const listeners = useWebSocketStore.getState().listeners.get(channel);
  if (!listeners?.size) {
    throw new Error(`nothing is subscribed to ${channel}`);
  }
  for (const listener of listeners) {
    listener(payload);
  }
}

/** Mounts the hook for both clusters over a cache holding `keys`. */
function mount(keys: string[][]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  for (const key of keys) {
    client.setQueryData(key, []);
  }
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  renderHook(
    () => {
      useEventInvalidation([CLUSTER, OTHER_CLUSTER]);
    },
    { wrapper },
  );
  return client;
}

/** Runs the hook's debounced flush (300 ms). */
function flush() {
  act(() => {
    vi.advanceTimersByTime(1_000);
  });
}

afterEach(() => {
  vi.useRealTimers();
});

// A scheduled run whose task Proxmox has started sends task_created (the
// scheduler publishes it after recording the task; a claim, or a run that
// fails before reaching Proxmox, sends nothing), and the task ending sends
// task_update (the collector, on finalizing it). An open Schedules tab reads
// its list through ["clusters", <cluster>, "schedules"] (useScheduledTasks)
// with a five-minute staleTime and no refetch on focus, so without this the
// tab showed neither until it was remounted.
describe("useEventInvalidation — task events and the schedule list", () => {
  it.each(["task_created", "task_update"])(
    "%s marks the event's cluster's schedule list stale, and no other cluster's",
    (kind) => {
      vi.useFakeTimers();
      const stale = {
        "the cluster's schedules": ["clusters", CLUSTER, "schedules"],
      };
      const fresh = {
        "another cluster's schedules": ["clusters", OTHER_CLUSTER, "schedules"],
        // Narrow on purpose: a task event is not an inventory change, and
        // prefix-invalidating the cluster would refetch every query under it.
        "the cluster's VM list": ["clusters", CLUSTER, "vms"],
      };
      const client = mount([...Object.values(stale), ...Object.values(fresh)]);

      act(() => {
        deliver(`cluster:${CLUSTER}:events`, {
          kind,
          cluster_id: CLUSTER,
          resource_type: "task",
          resource_id: UPID,
          action: "scheduled_snapshot",
        });
      });
      flush();

      for (const [name, key] of Object.entries(stale)) {
        expect(client.getQueryState(key)?.isInvalidated, name).toBe(true);
      }
      // A hook that invalidated every cluster's list would pass every line
      // above.
      for (const [name, key] of Object.entries(fresh)) {
        expect(client.getQueryState(key)?.isInvalidated, name).toBe(false);
      }
    },
  );

  it("leaves every schedule list alone for a task event that names no cluster", () => {
    vi.useFakeTimers();
    const schedules = [
      ["clusters", CLUSTER, "schedules"],
      ["clusters", OTHER_CLUSTER, "schedules"],
    ];
    const taskPage = ["tasks", "list", "page-1"];
    const client = mount([...schedules, taskPage]);

    // What a hand-made PUT /api/v1/tasks/:upid publishes: a system event
    // with no cluster, so there is no schedule list it could be about.
    act(() => {
      deliver("system:events", { kind: "task_update", action: "completed" });
    });
    flush();

    // The event WAS handled — the task list goes stale — so the schedule
    // lists below stay fresh because it names no cluster, not because
    // nothing ran.
    expect(client.getQueryState(taskPage)?.isInvalidated).toBe(true);
    for (const key of schedules) {
      expect(client.getQueryState(key)?.isInvalidated, key.join("/")).toBe(
        false,
      );
    }
  });
});

// A run settling is announced as schedule_change: the scheduler publishes one
// for every run it settles. It is the only event that follows the row's change
// — the run's own task_update came first, while the row still read
// "dispatched" — so an open Schedules tab depends on it to stop showing
// Running.
describe("useEventInvalidation — schedule_change", () => {
  it("marks the event's cluster's schedule list stale, and nothing else", () => {
    vi.useFakeTimers();
    const stale = {
      "the cluster's schedules": ["clusters", CLUSTER, "schedules"],
    };
    const fresh = {
      "another cluster's schedules": ["clusters", OTHER_CLUSTER, "schedules"],
      "the cluster's VM list": ["clusters", CLUSTER, "vms"],
      // A settle is not a task event: the task views re-read on the task's
      // own task_update, which came first.
      "the task list": ["tasks", "list", "page-1"],
      "the activity feed": ["recent-activity"],
    };
    const client = mount([...Object.values(stale), ...Object.values(fresh)]);

    act(() => {
      deliver(`cluster:${CLUSTER}:events`, {
        kind: "schedule_change",
        cluster_id: CLUSTER,
        resource_type: "schedule",
        resource_id: "d0000000-0000-4000-8000-000000000001",
        action: "failed",
      });
    });
    flush();

    for (const [name, key] of Object.entries(stale)) {
      expect(client.getQueryState(key)?.isInvalidated, name).toBe(true);
    }
    for (const [name, key] of Object.entries(fresh)) {
      expect(client.getQueryState(key)?.isInvalidated, name).toBe(false);
    }
  });
});
