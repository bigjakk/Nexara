import { describe, it, expect, afterEach, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { createTestQueryClient, createWrapper } from "@/test/test-utils";
import { useWebSocketStore } from "@/stores/websocket-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  HISTORY_THROTTLE_MS,
  useEventInvalidation,
} from "./useEventInvalidation";

// Events are delivered through the REAL websocket store, the way its onmessage
// does it — each listener on the channel gets the message's payload — so the
// hook's own subscribe calls decide what it hears. What is asserted is the
// query cache: which queries end up stale, not which calls were made (a key is
// invalidated by any call whose key is a prefix of it).

const CLUSTER = "c0000000-0000-4000-8000-00000000000a";
const OTHER_CLUSTER = "c0000000-0000-4000-8000-00000000000b";

/** A guest's History page in useHistory's key: cluster, route, then filters. */
function guestHistoryKey(cluster: string) {
  return [
    "history",
    cluster,
    `/api/v1/clusters/${cluster}/guests/100/history`,
    0,
    "",
    "",
    "",
  ];
}

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
function mount(keys: (string | number)[][]) {
  const client = createTestQueryClient({ keepCache: true });
  for (const key of keys) {
    client.setQueryData(key, []);
  }
  renderHook(
    () => {
      useEventInvalidation([CLUSTER, OTHER_CLUSTER]);
    },
    { wrapper: createWrapper({ client, router: false }) },
  );
  return client;
}

/** Runs the hook's debounced flushes: 300 ms, and the Histories' throttle. */
function flush() {
  act(() => {
    vi.advanceTimersByTime(HISTORY_THROTTLE_MS);
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
        "a guest's History": guestHistoryKey(CLUSTER),
      };
      const fresh = {
        "another cluster's schedules": ["clusters", OTHER_CLUSTER, "schedules"],
        "another cluster's guest History": guestHistoryKey(OTHER_CLUSTER),
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

// A change with no task — a config edit, an HA resource — reaches a guest's
// History only as its audit entry.
describe("useEventInvalidation — audit_entry and guest History", () => {
  it("marks the event's cluster's guest histories stale, and no other cluster's", () => {
    vi.useFakeTimers();
    const history = guestHistoryKey(CLUSTER);
    const otherHistory = guestHistoryKey(OTHER_CLUSTER);
    const globalHistory = ["history", "global", "/api/v1/pbs/s1/history", 0];
    const client = mount([history, otherHistory, globalHistory]);

    act(() => {
      deliver(`cluster:${CLUSTER}:events`, {
        kind: "audit_entry",
        cluster_id: CLUSTER,
        resource_type: "vm",
        resource_id: "d0000000-0000-4000-8000-000000000002",
        action: "config_update",
      });
    });
    flush();

    expect(client.getQueryState(history)?.isInvalidated).toBe(true);
    expect(client.getQueryState(otherHistory)?.isInvalidated).toBe(false);
    // A global kind's History reads every cluster's rows.
    expect(client.getQueryState(globalHistory)?.isInvalidated).toBe(true);
  });

  // The first entry re-reads at once; the rest of its burst are one re-read at
  // the window's end — however late in it they came, as a debounce would not —
  // which opens a window of its own. setQueryData stands in for the re-read.
  it("re-reads a History at once, then at most once per throttle window", () => {
    vi.useFakeTimers();
    const history = guestHistoryKey(CLUSTER);
    const client = mount([history]);
    const stale = () => client.getQueryState(history)?.isInvalidated;
    const entry = () => {
      act(() => {
        deliver(`cluster:${CLUSTER}:events`, {
          kind: "audit_entry",
          cluster_id: CLUSTER,
          resource_type: "vm",
          resource_id: "x",
          action: "config_update",
        });
      });
    };
    const wait = (ms: number) => {
      act(() => {
        vi.advanceTimersByTime(ms);
      });
    };
    entry();
    expect(stale()).toBe(true);
    client.setQueryData(history, []);
    entry();
    wait(HISTORY_THROTTLE_MS - 1);
    entry();
    expect(stale()).toBe(false);
    wait(1);
    expect(stale()).toBe(true);
    client.setQueryData(history, []);
    entry();
    wait(HISTORY_THROTTLE_MS - 1);
    expect(stale()).toBe(false);
    wait(1);
    expect(stale()).toBe(true);
  });
});

// A run settling is announced as schedule_change: the scheduler publishes one
// for every run it settles. It is the only event that follows the row's change
// — the run's own task_update came first, while the row still read
// "dispatched" — so an open Schedules tab depends on it to stop showing
// Running.
// A finished disk move rewrites its guest's config; with the task progress
// dialog closed nothing else re-read it, and the Hardware panel kept the old
// storage. The dialog re-reads itself when it shows the task, and any re-read
// drops staged edits, so only config-changing workers, and only once.
describe("useEventInvalidation — task_update and guest configs", () => {
  const MOVE =
    "UPID:pve-01:0000A1B2:0001C3D4:66F4E3C0:qmmove:100:nexara@pve!api:";
  it.each([
    ["re-reads after a disk move", MOVE, null, true],
    ["leaves them for a task that changes no config", UPID, null, false],
    ["leaves them to the dialog showing the move", MOVE, MOVE, false],
  ])("%s", (_name, upid, shownUpid, reread) => {
    vi.useFakeTimers();
    useTaskLogStore
      .getState()
      .setFocusedTask(
        shownUpid
          ? { clusterId: CLUSTER, upid: shownUpid, description: "" }
          : null,
      );
    const changed = [
      ["clusters", CLUSTER, "vms", "vm-1", "config"],
      ["clusters", CLUSTER, "containers", "ct-1", "config"],
      ["clusters", CLUSTER, "storage"],
    ];
    const fresh = [
      ["clusters", OTHER_CLUSTER, "vms", "vm-2", "config"],
      ["clusters", CLUSTER, "vms"],
      ["clusters", CLUSTER, "vms", "vm-1", "agent"],
    ];
    const client = mount([...changed, ...fresh]);

    act(() => {
      deliver(`cluster:${CLUSTER}:events`, {
        kind: "task_update",
        cluster_id: CLUSTER,
        resource_type: "task",
        resource_id: upid,
        action: "completed",
      });
    });
    flush();
    useTaskLogStore.getState().setFocusedTask(null);

    for (const key of changed) {
      expect(client.getQueryState(key)?.isInvalidated, key.join("/")).toBe(
        reread,
      );
    }
    for (const key of fresh) {
      expect(client.getQueryState(key)?.isInvalidated, key.join("/")).toBe(
        false,
      );
    }
  });
});

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

// The poller publishes ups_change when a UPS changes state or reachability, and
// the NUT server routes when the server list moves. Every Power query re-reads.
describe("useEventInvalidation — ups_change", () => {
  it("marks the Power queries stale, and nothing else", () => {
    vi.useFakeTimers();
    const stale = {
      "the NUT servers": ["nut-servers"],
      "the UPS list": ["ups-devices"],
      "a UPS": ["ups-device", "u1"],
      "a UPS's chart": ["ups-device", "u1", "metrics", "1h"],
      "the power events": ["ups-events", "all"],
    };
    const fresh = {
      "the cluster's VM list": ["clusters", CLUSTER, "vms"],
      "the PBS servers": ["pbs-servers"],
    };
    const client = mount([...Object.values(stale), ...Object.values(fresh)]);

    act(() => {
      deliver("system:events", { kind: "ups_change", action: "state_changed" });
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

// The server published these four and no page heard them; each re-read only
// at its next poll. The other cluster's import list is the control.
describe("useEventInvalidation — kinds the pages missed", () => {
  it.each([
    [
      "report_generated",
      "system:events",
      undefined,
      undefined,
      ["report-runs"],
    ],
    [
      "apt_repo_change",
      `cluster:${CLUSTER}:events`,
      CLUSTER,
      "pve-01",
      ["clusters", CLUSTER, "nodes", "pve-01", "apt-repositories"],
    ],
    [
      "vm_import",
      `cluster:${CLUSTER}:events`,
      CLUSTER,
      "job-1",
      ["clusters", CLUSTER, "vm-imports"],
    ],
    [
      "veeam_change",
      "system:events",
      undefined,
      undefined,
      ["veeam-servers", "s1", "jobs"],
    ],
  ])("%s marks its page stale", (kind, channel, cluster, resource, key) => {
    vi.useFakeTimers();
    const control = ["clusters", OTHER_CLUSTER, "vm-imports"];
    const client = mount([key, control]);
    act(() => {
      deliver(channel, {
        kind,
        cluster_id: cluster,
        resource_type: "x",
        resource_id: resource,
        action: "changed",
      });
    });
    flush();
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    expect(client.getQueryState(control)?.isInvalidated).toBe(false);
  });
});
