import type { VeeamSession } from "../types/backup";

/** How many of a job's latest runs its History lists above the first page. */
export const LIVE_RUN_COUNT = 10;

/**
 * A job's latest runs, newest first, from the runs Nexara has collected, and
 * within the time filter: from `since` (epoch milliseconds), or any time for
 * null.
 */
export function latestRuns(
  sessions: readonly VeeamSession[],
  jobId: string,
  since: number | null,
  count = LIVE_RUN_COUNT,
): VeeamSession[] {
  return sessions
    .filter(
      (session) =>
        session.job_veeam_id === jobId &&
        (since === null || new Date(session.creation_time).getTime() >= since),
    )
    .sort(
      (a, b) =>
        new Date(b.creation_time).getTime() -
        new Date(a.creation_time).getTime(),
    )
    .slice(0, count);
}

/** Whether a run is still going: "Stopped" is Veeam's one terminal state, and
 *  a row with no state at all reads as finished (VeeamSessionActions). */
export function runIsActive(session: VeeamSession): boolean {
  return session.state !== "" && session.state !== "Stopped";
}
