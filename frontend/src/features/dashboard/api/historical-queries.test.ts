import { describe, it, expect, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { focusManager } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { createWrapper } from "@/test/test-utils";
import { toSeriesPoints, useSeedMetrics } from "./historical-queries";
import type { HistoricalMetricPoint } from "@/types/api";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// The app does not refetch on focus, and refetches on mount only what is
// stale, which a seed a minute old is not: the seed has to ask for both itself,
// or a live chart come back to waits a minute for the stretch it missed.
describe("useSeedMetrics", () => {
  it.each([
    [
      "its tab is shown again",
      () => {
        focusManager.setFocused(false);
        focusManager.setFocused(true);
      },
    ],
    [
      "its chart is mounted again",
      (remount: () => void) => {
        remount();
      },
    ],
  ])("refetches the moment %s, though fresh", async (_, comeBack) => {
    vi.mocked(apiClient.list).mockReset().mockResolvedValue([]);
    const client = createAppQueryClient();
    const mount = () =>
      renderHook(() => useSeedMetrics("c1"), {
        wrapper: createWrapper({ client }),
      });
    const first = mount();
    await waitFor(() => {
      expect(first.result.current).toBeDefined();
    });
    expect(apiClient.list).toHaveBeenCalledTimes(1);

    try {
      comeBack(() => {
        first.unmount();
        mount();
      });
      await waitFor(() => {
        expect(apiClient.list).toHaveBeenCalledTimes(2);
      });
    } finally {
      focusManager.setFocused(undefined);
      client.clear();
    }
  });
});

describe("toSeriesPoints", () => {
  it("converts API response to chart points", () => {
    const input: HistoricalMetricPoint[] = [
      {
        timestamp: 1700000000000,
        cpuPercent: 45.2,
        memPercent: 62.1,
        diskReadBps: 1024,
        diskWriteBps: 2048,
        netInBps: 512,
        netOutBps: 256,
      },
    ];

    const result = toSeriesPoints(input);

    expect(result).toHaveLength(1);
    expect(result[0]).toEqual({
      timestamp: 1700000000000,
      cpuPercent: 45.2,
      memPercent: 62.1,
      diskReadBps: 1024,
      diskWriteBps: 2048,
      netInBps: 512,
      netOutBps: 256,
    });
  });

  it("returns empty array for empty input", () => {
    expect(toSeriesPoints([])).toEqual([]);
  });
});
