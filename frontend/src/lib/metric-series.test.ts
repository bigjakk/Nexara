import { describe, it, expect } from "vitest";
import {
  breakAtGaps,
  bucketSeries,
  latestValue,
  spliceLive,
} from "./metric-series";
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

  // A chart on a time axis joins its points: a stretch nothing measured has
  // to be broken, or it reads as a slope.
  it.each([
    [
      "a stretch far past the usual spacing is broken after its last sample",
      [0, 10_000, 20_000, 30_000, 2_000_000, 2_010_000],
      [0, 10_000, 20_000, 30_000, 30_001, 2_000_000, 2_010_000],
    ],
    [
      "a missed sample or two is not a gap",
      [0, 10_000, 20_000, 130_000, 140_000],
      [0, 10_000, 20_000, 130_000, 140_000],
    ],
    [
      "a series sampled every five minutes is not all gaps",
      [0, 300_000, 600_000, 900_000],
      [0, 300_000, 600_000, 900_000],
    ],
  ])("%s", (_, timestamps, want) => {
    const got = breakAtGaps(timestamps.map((ts) => pt(ts, 1)));
    expect(got.map((p) => p.timestamp)).toEqual(want);
    // The point put in is unmeasured, which is what breaks the line.
    for (const p of got) {
      expect(p.cpuPercent).toBe(timestamps.includes(p.timestamp) ? 1 : null);
    }
  });

  it("breaks the line between history that stopped and the ticks kept since", () => {
    // The tab was hidden for half an hour: the history ends where it was last
    // fetched and the ticks kept are the newest few.
    const seed = [pt(0, 1), pt(10_000, 2), pt(20_000, 3)];
    const live = [pt(10_000, 9), pt(1_800_000, 4), pt(1_810_000, 5)];
    expect(spliceLive(seed, live).map((p) => p.cpuPercent)).toEqual([
      1,
      2,
      3,
      null,
      4,
      5,
    ]);
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
