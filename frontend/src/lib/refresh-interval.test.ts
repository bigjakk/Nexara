import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { createWrapper } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { usePreferencesStore } from "@/stores/preferences-store";
import { useRecentActivity } from "@/features/audit/api/audit-queries";
import { refreshIntervalMs } from "./refresh-interval";

describe("refreshIntervalMs", () => {
  // designed × (preference ÷ 30), never under 10 s; 0 is manual.
  it.each<[unknown, number, number | false]>([
    [0, 120_000, false],
    [30, 120_000, 120_000],
    [30, 30_000, 30_000],
    [10, 120_000, 40_000],
    [10, 30_000, 10_000],
    [10, 15_000, 10_000],
    [60, 60_000, 120_000],
    [300, 120_000, 1_200_000],
    [-1, 60_000, 60_000],
    [Number.NaN, 60_000, 60_000],
    ["60", 60_000, 60_000],
    [undefined, 60_000, 60_000],
  ])("%o seconds on a %o ms site → %o", (seconds, designed, want) => {
    expect(refreshIntervalMs(seconds, designed)).toBe(want);
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  usePreferencesStore.getState().setPreferences({ refreshInterval: 30 });
});

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

// A general list scales its own rate by the preference at once, and Manual
// stops its timer. setInterval is faked before the hook mounts: TanStack arms
// it through the global on every options change (as in
// vm-queries.schedules.test.ts); the fetch stub runs on real timers.
describe("a general list's refresh", () => {
  it("scales its designed rate by the preference, and 0 stops it", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const armed = vi.spyOn(globalThis, "setInterval");
    const lastDelay = () => armed.mock.calls.at(-1)?.[1];
    const path = "/api/v1/audit-log/recent";
    const api = stubApi({ [path]: listOf([]) });
    const gets = () => api.sent.filter((r) => r === `GET ${path}`).length;
    renderHook(() => useRecentActivity(), { wrapper: createWrapper() });
    await until("the first read", () => gets() === 1);
    // Designed for 120 s: at the default of 30 nothing changes.
    expect(lastDelay()).toBe(120_000);

    // At 10 it is three times as fast, re-armed at once, and it reads.
    act(() => {
      usePreferencesStore.getState().setPreferences({ refreshInterval: 10 });
    });
    expect(lastDelay()).toBe(40_000);
    act(() => {
      vi.advanceTimersByTime(40_000);
    });
    await until("the 40 s read", () => gets() === 2);

    act(() => {
      usePreferencesStore.getState().setPreferences({ refreshInterval: 0 });
    });
    expect(vi.getTimerCount()).toBe(0);
  });
});
