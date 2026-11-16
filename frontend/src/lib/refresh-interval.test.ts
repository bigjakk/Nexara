import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { createWrapper } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { usePreferencesStore } from "@/stores/preferences-store";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { refreshIntervalMs } from "./refresh-interval";

describe("refreshIntervalMs", () => {
  it.each<[unknown, number | false]>([
    [0, false],
    [10, 10_000],
    [30, 30_000],
    [300, 300_000],
    [5, 10_000],
    [-1, 30_000],
    [Number.NaN, 30_000],
    ["60", 30_000],
    [undefined, 30_000],
  ])("%o seconds → %o", (seconds, want) => {
    expect(refreshIntervalMs(seconds)).toBe(want);
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  usePreferencesStore.getState().setPreferences({ refreshInterval: 30 });
});

/** Lets a refetch the interval started reach the stub and settle. */
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
}

// A general list follows the preference at once, and Manual stops its timer.
// setInterval is faked before the hook mounts: TanStack arms it through the
// global on every options change (vm-queries.schedules.test.ts does the same).
describe("a general list's refresh", () => {
  it("follows the Default refresh interval, and 0 stops it", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    usePreferencesStore.getState().setPreferences({ refreshInterval: 300 });
    const api = stubApi({ "/api/v1/clusters": listOf([]) });
    const gets = () => api.sent.filter((r) => r === "GET /api/v1/clusters").length;
    renderHook(() => useClusters(), { wrapper: createWrapper() });
    await settle();
    expect(gets()).toBe(1);

    act(() => {
      usePreferencesStore.getState().setPreferences({ refreshInterval: 10 });
    });
    act(() => {
      vi.advanceTimersByTime(10_000);
    });
    await settle();
    expect(gets()).toBe(2);

    act(() => {
      usePreferencesStore.getState().setPreferences({ refreshInterval: 0 });
    });
    act(() => {
      vi.advanceTimersByTime(600_000);
    });
    await settle();
    expect(gets()).toBe(2);
  });
});
