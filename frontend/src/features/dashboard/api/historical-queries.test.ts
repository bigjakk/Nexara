import { describe, it, expect } from "vitest";
import { toMetricDataPoints } from "./historical-queries";
import type { HistoricalMetricPoint } from "@/types/api";

describe("toMetricDataPoints", () => {
  it("converts API response to MetricDataPoint array", () => {
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

    const result = toMetricDataPoints(input);

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
    expect(toMetricDataPoints([])).toEqual([]);
  });
});
