import type {
  PBSDatastoreMetric,
  PBSSnapshot,
  PBSTask,
  PBSVerifyJob,
} from "../types/backup";
import {
  pbsTaskOutcome,
  pbsWorkerId,
  type PBSTaskOutcome,
} from "./pbs-live-tasks";

// What the overview says about one datastore, derived from lists the page
// already reads: its snapshots, the server's recent tasks, its verify jobs and
// a week of usage samples.

export type RunTone = "ok" | "warn" | "bad" | "idle";

/**
 * How a PBS job's last-run-state reads. PBS words success "OK", a finish with
 * warnings "WARNINGS: n" and a failure by its error; a job that has never run
 * reports nothing.
 */
export function runStateTone(state: string): RunTone {
  if (state === "") return "idle";
  if (state.toUpperCase() === "OK") return "ok";
  return state.startsWith("WARNINGS") ? "warn" : "bad";
}

const TONE_RANK: Record<RunTone, number> = { idle: 0, ok: 1, warn: 2, bad: 3 };

export interface SnapshotFacts {
  count: number;
  /** Distinct backup groups (a VM or a container) the snapshots belong to. */
  guests: number;
  verified: number;
}

export function snapshotFacts(
  snapshots: readonly PBSSnapshot[],
  store: string,
): SnapshotFacts {
  const mine = snapshots.filter((s) => s.datastore === store);
  return {
    count: mine.length,
    guests: new Set(mine.map((s) => `${s.backup_type}/${s.backup_id}`)).size,
    verified: mine.filter((s) => s.verified).length,
  };
}

export interface LastRun {
  outcome: PBSTaskOutcome;
  /** Unix seconds: when it ended, or began if it is still running. */
  at: number;
}

/**
 * The datastore's latest garbage collection among the server's recent tasks,
 * or null when the list no longer reaches back to one.
 */
export function lastGarbageCollection(
  tasks: readonly PBSTask[],
  store: string,
): LastRun | null {
  const latest = tasks
    .filter(
      (t) =>
        t.worker_type === "garbage_collection" && pbsWorkerId(t.upid) === store,
    )
    .sort((a, b) => b.starttime - a.starttime)[0];
  if (latest === undefined) return null;
  return {
    outcome: pbsTaskOutcome(latest),
    at: latest.endtime ?? latest.starttime,
  };
}

export interface VerifyFacts {
  jobs: number;
  /** The jobs' schedules, or "" when none of them is scheduled. */
  schedule: string;
  /** The worst last run across the jobs. */
  tone: RunTone;
}

export function verifyFacts(
  jobs: readonly PBSVerifyJob[],
  store: string,
): VerifyFacts {
  const mine = jobs.filter((j) => j.store === store);
  const schedules = [
    ...new Set(mine.map((j) => j.schedule).filter((s) => s !== "")),
  ];
  const tone = mine
    .map((j) => runStateTone(j.last_run_state))
    .reduce<RunTone>((a, b) => (TONE_RANK[b] > TONE_RANK[a] ? b : a), "idle");
  return { jobs: mine.length, schedule: schedules.join(", "), tone };
}

export interface ForecastPoint {
  timestamp: number;
  used: number | null;
  forecast: number | null;
  total: number | null;
}

export interface CapacityForecast {
  /** A week of usage, then the projection. Empty with too few samples. */
  points: ForecastPoint[];
  /** When the trend reaches capacity, if it does within the horizon. */
  fullDate: Date | null;
  totalCapacity: number;
}

function linearRegression(
  points: { x: number; y: number }[],
): { slope: number; intercept: number } | null {
  const n = points.length;
  if (n < 2) return null;

  let sumX = 0,
    sumY = 0,
    sumXY = 0,
    sumXX = 0;
  for (const p of points) {
    sumX += p.x;
    sumY += p.y;
    sumXY += p.x * p.y;
    sumXX += p.x * p.x;
  }

  const denom = n * sumXX - sumX * sumX;
  if (denom === 0) return null;

  const slope = (n * sumXY - sumX * sumY) / denom;
  const intercept = (sumY - slope * sumX) / n;

  return { slope, intercept };
}

/** How far ahead the trend is projected. */
export const FORECAST_HORIZON_DAYS = 90;

/**
 * A straight line through the datastore's usage samples, projected forward in
 * six-hour steps until it reaches capacity or the horizon.
 */
export function capacityForecast(
  metrics: readonly PBSDatastoreMetric[],
  store: string,
): CapacityForecast {
  const storeMetrics = metrics.filter((m) => m.datastore === store);
  if (storeMetrics.length < 3) {
    return { points: [], fullDate: null, totalCapacity: 0 };
  }

  // Capacity as of the latest sample.
  const latestTotal = storeMetrics[storeMetrics.length - 1]?.total ?? 0;

  const reg = linearRegression(
    storeMetrics.map((m) => ({ x: new Date(m.time).getTime(), y: m.used })),
  );

  const points: ForecastPoint[] = storeMetrics.map((m) => ({
    timestamp: new Date(m.time).getTime(),
    used: m.used,
    forecast: null,
    total: latestTotal,
  }));

  let fullDate: Date | null = null;

  if (reg && reg.slope > 0) {
    const lastTs = points[points.length - 1]?.timestamp ?? Date.now();
    const maxForecastMs = FORECAST_HORIZON_DAYS * 24 * 3600 * 1000;
    const stepMs = 6 * 3600 * 1000;

    // The last sample is also where the projection starts.
    const lastEntry = points[points.length - 1];
    if (lastEntry) {
      points[points.length - 1] = {
        ...lastEntry,
        forecast: reg.slope * lastTs + reg.intercept,
      };
    }

    for (let t = lastTs + stepMs; t <= lastTs + maxForecastMs; t += stepMs) {
      const projected = reg.slope * t + reg.intercept;
      if (projected >= latestTotal) {
        fullDate = new Date(t);
        points.push({
          timestamp: t,
          used: null,
          forecast: latestTotal,
          total: latestTotal,
        });
        break;
      }
      points.push({
        timestamp: t,
        used: null,
        forecast: projected,
        total: latestTotal,
      });
    }
  }

  return { points, fullDate, totalCapacity: latestTotal };
}
