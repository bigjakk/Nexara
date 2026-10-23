import { useRef } from "react";
import type { BlockJob } from "../api/vm-queries";

/** A job's progress between two polls: bytes per second, and the time left. */
export interface JobPace {
  bps: number;
  etaSeconds: number | null;
}

/** What a fetch saw of each job: when, and how far each had got. */
interface PaceSnapshot {
  at: number;
  done: Map<string, number>;
}

/**
 * Each job's pace, from how far it got since the previous fetch. QEMU reports
 * only progress, so the rate is derived here; a job seen once has none yet,
 * and one that went backwards (restarted) has none either.
 */
export function jobPace(
  before: PaceSnapshot | null,
  jobs: readonly BlockJob[],
  at: number,
): Map<string, JobPace> {
  const pace = new Map<string, JobPace>();
  for (const job of jobs) {
    const was = before?.done.get(job.device);
    const dt = before ? (at - before.at) / 1000 : 0;
    if (was !== undefined && dt > 0 && job.completed >= was) {
      const bps = (job.completed - was) / dt;
      pace.set(job.device, {
        bps,
        etaSeconds: bps > 0 ? (job.total - job.completed) / bps : null,
      });
    }
  }
  return pace;
}

/**
 * jobPace once per fetch (`at`, the query's dataUpdatedAt), kept in a ref
 * rather than state, so a render cannot set off another.
 */
export function useJobPace(
  jobs: BlockJob[] | undefined,
  at: number,
): Map<string, JobPace> {
  const prev = useRef<PaceSnapshot | null>(null);
  const last = useRef<{ at: number; pace: Map<string, JobPace> }>({
    at: -1,
    pace: new Map(),
  });
  if (jobs !== undefined && at !== last.current.at) {
    last.current = { at, pace: jobPace(prev.current, jobs, at) };
    prev.current = {
      at,
      done: new Map(jobs.map((j) => [j.device, j.completed])),
    };
  }
  return last.current.pace;
}
