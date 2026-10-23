import { describe, it, expect } from "vitest";
import { toSeriesPoints } from "./historical-queries";
import type { HistoricalMetricPoint } from "@/types/api";

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
