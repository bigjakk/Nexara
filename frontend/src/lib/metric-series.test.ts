import { describe, it, expect } from "vitest";
import { bucketSeries, latestValue, spliceLive } from "./metric-series";
import type { MetricSeriesPoint } from "@/types/ws";

const pt = (
  timestamp: number,
  cpuPercent: number | null,
  memPercent: number | null = 50,
): MetricSeriesPoint => ({
  timestamp,
  cpuPercent,
  memPercent,
  diskReadBps: null,
  diskWriteBps: null,
  netInBps: null,
  netOutBps: null,
});

describe("metric-series", () => {
  it("extends the seed with the live ticks newer than it, within the window", () => {
    const seed = [pt(0, 1), pt(10, 2), pt(20, 3), pt(30, 4)];
    const live = [pt(20, 8), pt(30, 9), pt(40, 10)];
    expect(spliceLive(seed, live).map((p) => p.cpuPercent)).toEqual([
      1, 2, 3, 4, 10,
    ]);
    expect(spliceLive(seed, live, 25).map((p) => p.timestamp)).toEqual([
      20, 30, 40,
    ]);
    expect(spliceLive([], live)).toEqual(live);
  });

  it("averages each bucket over the points that measured it", () => {
    const got = bucketSeries(
      [pt(0, 10), pt(100, null), pt(200, 30), pt(300, null, null)],
      300,
    );
    expect(got.map((p) => [p.timestamp, p.cpuPercent, p.memPercent])).toEqual([
      [0, 20, 50],
      [300, null, null],
    ]);
  });

  it("reads the newest measured value past a trailing gap", () => {
    expect(latestValue([pt(0, 5), pt(10, 7), pt(20, null)], "cpuPercent")).toBe(
      7,
    );
    expect(latestValue([pt(0, null)], "cpuPercent")).toBeUndefined();
  });
});
