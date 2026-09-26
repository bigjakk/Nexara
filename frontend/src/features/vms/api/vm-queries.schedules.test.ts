import { describe, it, expect, afterEach, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { createWrapper } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import {
  SCHEDULE_IN_FLIGHT_POLL_MS,
  useScheduledTasks,
  type ScheduledTask,
} from "./vm-queries";

// A dispatched run settles on the server after its task's own task_update: the
// collector marks the task finished first, and the scheduler's reconcile tick
// rewrites the row after that, announcing it as schedule_change. That event is
// what normally refreshes the tab; while a row is in flight the list also
// re-reads itself, as the fallback for an event lost on the way. The polling
// must stop once every row has settled — a tab left open would otherwise poll
// forever — and thirty minutes after the view first saw a run in flight,
// timed on the browser's clock so a skewed server clock cannot move it.
//
// setInterval and Date are faked: the first is what TanStack's
// refetchInterval runs on, the second the clock the polling limit is timed
// on. Both are installed before the hook mounts, because TanStack re-arms the
// interval through the global setInterval on every update. RTL's waitFor polls
// on setInterval too, so the waiting here is done on setTimeout instead.

const CLUSTER = "c1";
const LIST = `/api/v1/clusters/${CLUSTER}/schedules`;
const MINUTE = 60_000;
/** "Now" when each test starts: a minute after the fixture run began. */
const NOW = new Date("2026-09-26T02:01:00Z");

function schedule(
  lastStatus: string | null,
  lastRunAt: string = "2026-09-26T02:00:00Z",
): ScheduledTask {
  return {
    id: "sch-1",
    cluster_id: CLUSTER,
    resource_type: "vm",
    resource_id: "101",
    node: "pve-01",
    action: "snapshot",
    schedule: "0 2 * * *",
    params: {},
    enabled: true,
    last_run_at: lastRunAt,
    next_run_at: "2026-09-27T02:00:00Z",
    last_status: lastStatus,
    last_error: null,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T02:00:00Z",
  };
}

/** Every fixture here is guest 101's, which is what this caller shows. */
const guest101 = (s: ScheduledTask) => s.resource_id === "101";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** Mounts the hook over a stub answering `reads`, with the clock at NOW. */
function mount(reads: Record<string, unknown>) {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
  vi.setSystemTime(NOW);
  const api = stubApi(reads);
  const { result } = renderHook(() => useScheduledTasks(CLUSTER, guest101), {
    wrapper: createWrapper(),
  });
  return {
    result,
    gets: () => api.sent.filter((r) => r === `GET ${LIST}`).length,
  };
}

/** Moves the faked clock — and any interval due on it — forward. */
function advance(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

/** Waits, on real timers and inside act, until cond holds. */
async function until(what: string, cond: () => boolean) {
  for (let i = 0; i < 200; i++) {
    if (cond()) return;
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** Lets a refetch the interval started reach the stub and settle. */
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 50));
  });
}

describe("useScheduledTasks", () => {
  it.each(["dispatched", "running"])(
    "re-reads the list while a row reads %j, and stops once it settles",
    async (inFlight) => {
      const reads: Record<string, unknown> = {
        [LIST]: listOf([schedule(inFlight)]),
      };
      const { result, gets } = mount(reads);
      await until(
        "the first read",
        () => result.current.data?.[0]?.last_status === inFlight,
      );
      expect(gets()).toBe(1);

      // The task ends and the reconcile settles the row.
      reads[LIST] = listOf([schedule("failed")]);
      advance(SCHEDULE_IN_FLIGHT_POLL_MS);
      await until(
        "the settled row",
        () => result.current.data?.[0]?.last_status === "failed",
      );
      expect(gets()).toBe(2);

      // Settled: no more reads. Ten minutes on, so a slower poller than the
      // in-flight one — once a minute, say — would have shown itself.
      advance(10 * MINUTE);
      await settle();
      expect(gets()).toBe(2);
    },
  );

  it("does not poll a list with nothing in flight", async () => {
    const { result, gets } = mount({
      [LIST]: listOf([schedule(null), schedule("success")]),
    });
    await until("the first read", () => result.current.data?.length === 2);
    advance(10 * MINUTE);
    await settle();
    expect(gets()).toBe(1);
  });

  it.each([
    // The server stamped the run 25 minutes before this view saw it.
    ["a run the server says started 25 minutes ago", -25],
    // The server's clock is two hours ahead of the browser's.
    ["a run stamped two hours in the browser's future", 120],
  ])(
    "polls %s for thirty minutes from when this view first saw it",
    async (_name, stampedMinutesFromNow) => {
      const stamped = new Date(
        NOW.getTime() + stampedMinutesFromNow * MINUTE,
      ).toISOString();
      const { result, gets } = mount({
        [LIST]: listOf([schedule("dispatched", stamped)]),
      });
      await until("the first read", () => result.current.data?.length === 1);

      // Six minutes in: still polling, whatever the server's stamp says.
      let lastRead = result.current.dataUpdatedAt;
      advance(SCHEDULE_IN_FLIGHT_POLL_MS);
      await until("a poll", () => result.current.dataUpdatedAt > lastRead);
      lastRead = result.current.dataUpdatedAt;
      advance(6 * MINUTE);
      await until("a poll", () => result.current.dataUpdatedAt > lastRead);
      expect(gets()).toBe(3);

      // The poll that lands past thirty minutes is the last.
      lastRead = result.current.dataUpdatedAt;
      advance(30 * MINUTE);
      await until("a poll", () => result.current.dataUpdatedAt > lastRead);
      expect(gets()).toBe(4);
      advance(10 * MINUTE);
      await settle();
      expect(gets()).toBe(4);
      expect(result.current.data?.[0]?.last_status).toBe("dispatched");
    },
  );
});
