import type { VeeamJob, VeeamSession } from "../types/backup";

/**
 * Shared Veeam row fixtures for the table and action tests.
 *
 * Test-only — the `.fixtures.ts` infix says so in the filename rather than
 * only in this comment, since it otherwise sits among real app modules named
 * `veeam-*.ts`. Nothing outside a `*.test.tsx` imports it, so it never reaches
 * the bundle. It lives here rather than under `src/test/` because the shapes
 * are Veeam's, and they move when `types/backup.ts` does.
 */

/** A finished backup job, the shape the tables render most of the time. */
export function job(over: Partial<VeeamJob> = {}): VeeamJob {
  return {
    id: "job-1",
    veeam_id: "3953c24f-bbe6-41fc-ae2f-a34e25bfd614",
    name: "Daily-Backup",
    job_type: "ProxmoxBackupJob",
    workload: "Vm",
    description: "",
    status: "Stopped",
    last_result: "Success",
    last_run: "2026-08-26T22:00:29Z",
    next_run: "2026-08-27T22:00:00Z",
    next_run_policy: "8/27/2026 10:00 PM",
    repository_name: "repo-nas-01",
    objects_count: 3,
    progress_percent: 100,
    bottleneck: "Source",
    duration: "00:18:27",
    processing_rate: "268 MB",
    processed_size: 1046898278400,
    read_size: 274600034304,
    transferred_size: 8739400042,
    cluster_id: null,
    last_seen_at: "2026-08-26T23:00:00Z",
    running_session_id: "",
    running_session_state: "",
    last_session_id: "",
    ...over,
  };
}

/** A run that has finished — it has a result, a duration and an end time. */
export function session(over: Partial<VeeamSession> = {}): VeeamSession {
  return {
    id: "sess-1",
    veeam_id: "20ff3c43-65c0-414d-ae03-10cf59fd2faa",
    name: "Daily-Backup",
    state: "Stopped",
    result: "Success",
    result_message: "Success",
    algorithm: "Increment",
    bottleneck: "Source",
    duration: "00:10:00",
    processing_rate: "33.3 MB",
    processed_size: 96636764160,
    read_size: 13931380736,
    transferred_size: 2628327455,
    progress_percent: 100,
    creation_time: "2026-08-26T19:56:00Z",
    end_time: "2026-08-26T20:06:01Z",
    initiated_by: "SYSTEM",
    nexara_initiated: false,
    nexara_stopped: false,
    cluster_id: null,
    ...over,
  };
}

/**
 * A run still in flight: no result yet, no end time, partial progress.
 *
 * Separate from `session()` rather than an override at the call site because
 * the difference decides whether a stop control renders at all. A test that
 * asserts the ABSENCE of that control — "renders nothing without
 * execute:veeam" — passes for the wrong reason if handed a finished run,
 * since a terminal state hides the button on its own.
 */
export function runningSession(over: Partial<VeeamSession> = {}): VeeamSession {
  return session({
    state: "Working",
    result: "",
    result_message: "",
    bottleneck: "",
    duration: "",
    processing_rate: "",
    processed_size: 0,
    read_size: 0,
    transferred_size: 0,
    progress_percent: 20,
    end_time: null,
    ...over,
  });
}
