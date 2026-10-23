import type { MetricSeriesPoint } from "@/types/ws";

type ValueKey = Exclude<keyof MetricSeriesPoint, "timestamp">;

/** How much a live chart shows: its seed is the last hour of history. */
export const LIVE_WINDOW_MS = 60 * 60_000;

const VALUE_KEYS: readonly ValueKey[] = [
  "cpuPercent",
  "memPercent",
  "diskReadBps",
  "diskWriteBps",
  "netInBps",
  "netOutBps",
];

/**
 * The newest measured value of `key`. A series ends in a gap when its last
 * sample was missed or its counter went unreported; the header should still
 * show the last real reading.
 */
export function latestValue(
  points: readonly MetricSeriesPoint[],
  key: ValueKey,
): number | undefined {
  for (let i = points.length - 1; i >= 0; i--) {
    const v = points[i]?.[key];
    if (v !== null && v !== undefined) return v;
  }
  return undefined;
}

/**
 * Stored history extended by the live ticks newer than its newest point. The
 * history is what to show — rated with the rules that keep a stall from
 * reading as a ramp or a reset as a zero dip — and it is refetched while a
 * live chart is open, so the ticks only fill in the time since. Both carry
 * the collector's own sample time, so the join is exact. With `windowMs`,
 * only the newest `windowMs` of the result is kept, so a live chart keeps its
 * hour.
 */
export function spliceLive<T extends { timestamp: number }>(
  seed: readonly T[],
  live: readonly T[],
  windowMs?: number,
): T[] {
  const seedEnd = seed[seed.length - 1]?.timestamp ?? -Infinity;
  const merged = [...seed, ...live.filter((p) => p.timestamp > seedEnd)];
  if (windowMs === undefined || merged.length === 0) return merged;
  const newest = merged[merged.length - 1]?.timestamp ?? 0;
  return merged.filter((p) => p.timestamp >= newest - windowMs);
}

/**
 * Averages a series into buckets of `widthMs`, each value over the points
 * that have one; a bucket with none for a value has null there.
 */
export function bucketSeries(
  points: readonly MetricSeriesPoint[],
  widthMs: number,
): MetricSeriesPoint[] {
  const out: MetricSeriesPoint[] = [];
  let sums: number[] = [];
  let counts: number[] = [];
  let bucketTs: number | undefined;
  const flush = () => {
    if (bucketTs === undefined) return;
    const p: MetricSeriesPoint = {
      timestamp: bucketTs,
      cpuPercent: null,
      memPercent: null,
      diskReadBps: null,
      diskWriteBps: null,
      netInBps: null,
      netOutBps: null,
    };
    VALUE_KEYS.forEach((k, i) => {
      const n = counts[i] ?? 0;
      if (n > 0) p[k] = (sums[i] ?? 0) / n;
    });
    out.push(p);
  };
  for (const point of points) {
    const ts = Math.floor(point.timestamp / widthMs) * widthMs;
    if (ts !== bucketTs) {
      flush();
      bucketTs = ts;
      sums = VALUE_KEYS.map(() => 0);
      counts = VALUE_KEYS.map(() => 0);
    }
    VALUE_KEYS.forEach((k, i) => {
      const v = point[k];
      if (v !== null) {
        sums[i] = (sums[i] ?? 0) + v;
        counts[i] = (counts[i] ?? 0) + 1;
      }
    });
  }
  flush();
  return out;
}

/** How finely a stat card's trend strip is drawn. */
const SPARKLINE_BUCKET_MS = 60_000;

/**
 * A stat card's trend: the last hour of history extended by the live ticks,
 * averaged into minutes. The strip spaces points by index, so even buckets
 * keep it even in time, and the dashboard's strips and the cluster page's
 * draw alike.
 */
export function sparklineSeries(
  seed: readonly MetricSeriesPoint[],
  live: readonly MetricSeriesPoint[],
): MetricSeriesPoint[] {
  return bucketSeries(
    spliceLive(seed, live, LIVE_WINDOW_MS),
    SPARKLINE_BUCKET_MS,
  );
}
