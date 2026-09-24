import type { BackupJob, BackupJobRunResult } from "../types/backup";

/** One toast a backup job run gets. */
export interface BackupJobRunNotice {
  level: "success" | "warning" | "error";
  message: string;
  description?: string;
}

/**
 * The run's answer as it may arrive. The server always sends every field, but
 * the answer is read defensively all the same: it is read while backups are
 * starting, and a list that went missing must read as empty rather than throw
 * and turn a running backup into an error toast.
 */
export type BackupJobRunWire = {
  [K in keyof BackupJobRunResult]?: BackupJobRunResult[K] | null;
};

export function normalizeBackupJobRun(
  wire: BackupJobRunWire | null | undefined,
): BackupJobRunResult {
  const w = wire ?? {};
  return {
    tasks: w.tasks ?? [],
    skipped: w.skipped ?? [],
    errors: w.errors ?? [],
    unconfirmed: w.unconfirmed ?? [],
    stops_running_backups: w.stops_running_backups ?? false,
  };
}

function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

function nodeMessages(list: BackupJobRunResult["errors"]): string {
  return list.map((e) => `${e.node}: ${e.message}`).join("; ");
}

/**
 * The toasts for a run that answered 200, one per thing the operator has to
 * know: the tasks that started (and where there was nothing to do), the nodes
 * where no backup started and why, the nodes that did not confirm a start —
 * where one may be running, so running the job again could start a second —
 * and, when nothing started and nothing failed, that there was nothing to back
 * up, so an empty task list is not mistaken for a dropped click. A job with
 * stop set stopped any backup already running on every node that answered with
 * a task or with nothing to back up, and possibly on a node refused afterwards;
 * the toasts say so rather than reading as if nothing happened there.
 */
export function describeBackupJobRun(
  jobId: string,
  result: BackupJobRunResult,
): BackupJobRunNotice[] {
  const notices: BackupJobRunNotice[] = [];
  const started = result.tasks.length;
  const stops = result.stops_running_backups;
  // Every node that answered with a task or with "OK": vzdump ran its stop
  // step on each before answering.
  const answered = [...result.tasks.map((t) => t.node), ...result.skipped]
    .sort()
    .join(", ");
  if (started > 0) {
    const details: string[] = [];
    if (result.skipped.length > 0) {
      details.push(`Nothing to back up on ${result.skipped.join(", ")}.`);
    }
    if (stops) {
      details.push(
        `The job has stop set: any backup already running on ${answered} was stopped first.`,
      );
    }
    notices.push({
      level: "success",
      message: `Backup job ${jobId} started ${String(started)} backup ${plural(
        started,
        "task",
        "tasks",
      )}: ${result.tasks.map((t) => t.node).join(", ")}.`,
      ...(details.length > 0 ? { description: details.join(" ") } : {}),
    });
  }
  if (result.errors.length > 0) {
    const failed = result.errors.length;
    // A refusal can come after vzdump's stop step — the storage permission
    // check does — so with stop set, "could not start" is not "nothing
    // happened" either.
    const stopNote = stops
      ? " — with stop set, vzdump may also have stopped a backup already running on a node it refused afterwards."
      : "";
    notices.push({
      level: "error",
      message: `Backup job ${jobId} could not start on ${String(failed)} ${plural(
        failed,
        "node",
        "nodes",
      )}: ${nodeMessages(result.errors)}${stopNote}`,
    });
  }
  if (result.unconfirmed.length > 0) {
    const unknown = result.unconfirmed.length;
    notices.push({
      level: "warning",
      message: `Backup job ${jobId} did not confirm a start on ${String(unknown)} ${plural(
        unknown,
        "node",
        "nodes",
      )} — check the task list before running it again: ${nodeMessages(
        result.unconfirmed,
      )}`,
    });
  }
  if (
    started === 0 &&
    result.errors.length === 0 &&
    result.unconfirmed.length === 0
  ) {
    let message = `Backup job ${jobId} started no backup.`;
    if (result.skipped.length > 0) {
      message = stops
        ? `Backup job ${jobId} found none of its guests on ${answered}, but has stop set: any backup already running there was stopped.`
        : `Backup job ${jobId} found nothing to back up on ${answered}.`;
    }
    notices.push({ level: "warning", message });
  }
  return notices;
}

/**
 * The body of the confirmation Run now asks for, as the Proxmox GUI asks
 * ("Start the selected backup job now?", www/manager6/dc/Backup.js). It says
 * where the run goes — the job's node, or every online node — and what makes
 * it disruptive: the run may prune older backups as a scheduled one does, and
 * a stop- or suspend-mode job interrupts its guests.
 */
export function describeBackupJobRunConfirm(job: BackupJob): string {
  const where =
    job.node != null && job.node !== ""
      ? `on node ${job.node}`
      : "on every online node";
  let text =
    `Proxmox starts this job's backup now, outside its schedule, ${where}. ` +
    "It runs with the job's own settings, as a scheduled run does, so older " +
    "backups may be pruned by the retention that applies to them.";
  if (job.mode === "stop") {
    text +=
      " This job's mode is stop, which shuts running guests down for their backups.";
  } else if (job.mode === "suspend") {
    text +=
      " This job's mode is suspend, which suspends guests for their backups.";
  }
  return text;
}
