/**
 * How the Schedules tab reads a scheduled task's `last_status`.
 *
 * These are the values the scheduler writes (internal/scheduler, the
 * runStatus* constants, plus the "running" that ClaimDueTasks writes when it
 * claims a run). A run is "running" while the scheduler starts it and
 * "dispatched" while the Proxmox task it started is under way; it then
 * settles to "success" or "failed" by how that task ended. A run that fails
 * before reaching Proxmox goes straight to "failed".
 *
 * "dispatched" is new. The scheduler used to write "success" as soon as
 * Proxmox accepted the call, so the tab read Success while every run's task
 * failed. TestRunStatusVocabularyMatchesTheSPA (internal/scheduler) pins this
 * union to what the scheduler writes, and the Record below makes tsc hold the
 * table to the union.
 */
export type ScheduleLastStatus =
  "running" | "dispatched" | "success" | "failed";

/** What the Schedules tab shows for a row. */
export type ScheduleRunState = "pending" | "running" | "success" | "failed";

const RUN_STATES: Record<ScheduleLastStatus, ScheduleRunState> = {
  running: "running",
  dispatched: "running",
  success: "success",
  failed: "failed",
};

/**
 * The state a row's last_status shows as. No status means the task has never
 * run. A value outside the vocabulary shows as failed — what the tab has
 * always shown for anything but a success — rather than as a success nobody
 * reported.
 */
export function scheduleRunState(lastStatus: string | null): ScheduleRunState {
  if (!lastStatus) return "pending";
  return Object.hasOwn(RUN_STATES, lastStatus)
    ? RUN_STATES[lastStatus as ScheduleLastStatus]
    : "failed";
}

/**
 * How long a Schedules view keeps polling for a run, from when THAT VIEW first
 * saw it in flight.
 *
 * Polling is the fallback. The settle is announced — the scheduler publishes
 * schedule_change for every run it settles, and the view re-reads on it — so
 * this only matters when that event is lost on the server side, where nothing
 * replays it: the WebSocket hub's Redis subscriber was reconnecting, the
 * PUBLISH failed (events.Publish logs it and drops it), or the hub dropped the
 * frame because this client's send buffer was full. A socket that was down is
 * already covered — the store refetches every active query on reconnect — and
 * a background tab still re-reads on the event; only interval polling pauses
 * there. Thirty minutes covers the run
 * itself (the scheduler's claim goes stale after ten, and a snapshot or reboot
 * task normally ends within minutes) and its settling (one collector sync,
 * then at most 15 s). A run still in flight after that — a long snapshot that
 * saves a large VM's memory, or a cluster that cannot be reached — is left to
 * the schedule_change event and to the next time the list is read.
 */
export const IN_FLIGHT_POLL_LIMIT_MS = 30 * 60_000;

/** The fields of a schedule row the poll decision reads. */
export interface PollableSchedule {
  id: string;
  enabled: boolean;
  last_status: string | null;
  last_run_at: string | null;
}

/**
 * When a view first saw each run in flight, on the browser's clock. Keyed by
 * the schedule and its run's start as the server recorded it (last_run_at) —
 * an identity only, never compared with the browser's clock, so a skewed
 * server clock can neither suppress polling nor stretch it.
 */
export type InFlightSightings = Map<string, number>;

/**
 * Whether a row's run is one to poll for: its schedule is enabled and the run
 * is under way.
 *
 * Enabled, because nothing re-claims a disabled schedule: one left reading
 * "running" — a claim whose record never landed — reads that for good, and
 * every view showing it would poll forever. A disabled schedule's dispatched
 * run is still settled, and announced, on the server.
 */
export function isRunInFlight(row: PollableSchedule): boolean {
  return row.enabled && scheduleRunState(row.last_status) === "running";
}

/**
 * Whether the list should keep polling at `now` (the browser's clock): some
 * row's run is in flight and this view first saw it less than
 * IN_FLIGHT_POLL_LIMIT_MS ago.
 *
 * Records each run's first sighting in `sightings` and forgets runs that are
 * no longer in flight, so the map never holds more than the rows on screen,
 * and a schedule's next run is timed from when it is seen, not from its last.
 */
export function anyRunInFlight(
  rows: readonly PollableSchedule[] | undefined,
  now: number,
  sightings: InFlightSightings,
): boolean {
  const current = new Set<string>();
  let polling = false;
  for (const row of rows ?? []) {
    if (!isRunInFlight(row)) continue;
    const key = `${row.id}@${row.last_run_at ?? ""}`;
    current.add(key);
    let seen = sightings.get(key);
    if (seen === undefined) {
      seen = now;
      sightings.set(key, now);
    }
    if (now - seen < IN_FLIGHT_POLL_LIMIT_MS) polling = true;
  }
  for (const key of sightings.keys()) {
    if (!current.has(key)) sightings.delete(key);
  }
  return polling;
}
