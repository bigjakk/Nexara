import { useCallback, useRef, useState } from "react";
import {
  ArrowRightLeft,
  CheckCircle2,
  Circle,
  CircleMinus,
  Clock,
  Loader2,
  XCircle,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { apiClient, sessionScope } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useTaskLogStore } from "@/stores/task-log-store";
import { MOVE_NOTES, resolveVolume, waitForTask } from "../lib/resolve-disk";
import type { VMActionResponse } from "@/features/vms/types/vm";

export interface MigrateBatchJob {
  /** Display label for the row (e.g. "web-01 (100)" or just the volid). */
  label: string;
  /** Owning guest's UUID (already resolved by the caller). */
  guestId: string;
  guestKind: "vm" | "ct";
  vmid: number;
  volid: string;
  /** Whether the guest is running, which decides what may move now. */
  running: boolean;
}

type JobStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  // Left running in Proxmox: after Stop, or with its status unreadable.
  | "detached"
  | "unknown"
  // Never started, because the batch stopped first.
  | "skipped";

interface JobState extends MigrateBatchJob {
  status: JobStatus;
  error?: string;
  upid?: string;
}

interface MigrateBatchDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  jobs: MigrateBatchJob[];
  /** Storage names eligible as a target. */
  targetOptions: string[];
  /** Title shown in the dialog header. */
  title: string;
}

export function MigrateBatchDialog({
  open,
  onOpenChange,
  clusterId,
  jobs,
  targetOptions,
  title,
}: MigrateBatchDialogProps) {
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const [running, setRunning] = useState(false);
  const [states, setStates] = useState<JobState[]>([]);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  // The running batch's Stop: aborted, it starts no further move and ends the
  // wait for the current one. A closed dialog's loop would otherwise run on
  // out of sight beside the next batch, so a close waits for Stop.
  const stopRef = useRef<AbortController | null>(null);
  const [stopping, setStopping] = useState(false);
  // The batch whose loop may write the rows; one closed after Stop may not.
  const batchRef = useRef(0);

  // Reset state when the dialog closes so the next open starts clean.
  const handleOpenChange = useCallback(
    (next: boolean) => {
      if (!next) {
        if (running && !stopping) return;
        batchRef.current++;
        setTargetStorage("");
        setStates([]);
        setRunning(false);
        setStopping(false);
      }
      onOpenChange(next);
    },
    [onOpenChange, running, stopping],
  );

  const handleMigrate = useCallback(async () => {
    if (!targetStorage || jobs.length === 0) return;
    setRunning(true);
    setStopping(false);
    const stop = new AbortController();
    stopRef.current = stop;
    const stopped = () => stop.signal.aborted;
    const batch = ++batchRef.current;
    const show = (rows: JobState[]) => {
      if (batchRef.current === batch) setStates(rows);
    };

    const initial: JobState[] = jobs.map((j) => ({ ...j, status: "pending" }));
    show(initial);

    const working = [...initial];

    // The session this batch was started in. The loop outlives the dialog, and
    // a sign-out with it: once that session has ended, no request goes out for
    // it — it would carry whoever signs in next's token, and each is a disk move
    // on the cluster — and nothing it started is put in their task log.
    const ended = sessionScope();

    for (let i = 0; i < working.length; i++) {
      if (ended() || stopped()) break;
      const job = working[i];
      if (!job) continue;

      working[i] = { ...job, status: "running" };
      show([...working]);

      try {
        const resolved = await resolveVolume(
          clusterId,
          { id: job.guestId, kind: job.guestKind, running: job.running },
          job.volid,
        );

        if (ended()) break;
        // Stop came during the config read: the move was never sent.
        if (stopped()) {
          working[i] = job;
          break;
        }

        const configKey = resolved.key;
        if (configKey === null) {
          working[i] = { ...job, status: "failed", error: resolved.reason };
          show([...working]);
          continue;
        }

        const path =
          job.guestKind === "ct"
            ? apiPath`/api/v1/clusters/${clusterId}/containers/${job.guestId}/volumes/move`
            : apiPath`/api/v1/clusters/${clusterId}/vms/${job.guestId}/disks/move`;

        const body =
          job.guestKind === "ct"
            ? {
                volume: configKey,
                storage: targetStorage,
                delete: deleteOriginal,
              }
            : {
                disk: configKey,
                storage: targetStorage,
                delete: deleteOriginal,
              };

        const resp = await apiClient.post<VMActionResponse>(path, body);

        if (resp.upid && i === 0 && !ended()) {
          // Surface the first task in the global task log so the user can
          // open it from the activity panel; subsequent jobs are tracked
          // inline in the dialog.
          setFocusedTask({
            clusterId,
            upid: resp.upid,
            description: `Migrate ${job.label} → ${targetStorage}`,
          });
        }

        // Ticked when the move has ended, not when Proxmox took it, and the
        // next starts only then: a guest's second move would meet the lock.
        const outcome = resp.upid
          ? await waitForTask(clusterId, resp.upid, ended, stop.signal)
          : { status: "ended" as const, ok: true, exit: "" };
        if (ended()) break;
        const upid = resp.upid;
        if (outcome.status === "ended") {
          working[i] = outcome.ok
            ? { ...job, status: "completed", upid }
            : { ...job, status: "failed", upid, error: outcome.exit };
        } else {
          // Stopped, or the status could not be read: the move may still
          // hold the guest's lock, so nothing further starts.
          working[i] = {
            ...job,
            upid,
            status: outcome.status === "unknown" ? "unknown" : "detached",
          };
          show([...working]);
          break;
        }
        show([...working]);
      } catch (err) {
        working[i] = {
          ...job,
          status: "failed",
          error: err instanceof Error ? err.message : "Migration failed",
        };
        show([...working]);
      }
    }

    if (!ended()) {
      show(
        working.map((j) =>
          j.status === "pending" ? { ...j, status: "skipped" } : j,
        ),
      );
    }
    if (batchRef.current === batch) setRunning(false);
  }, [clusterId, deleteOriginal, jobs, setFocusedTask, targetStorage]);

  const total = jobs.length;
  const done = states.filter((s) => s.status === "completed").length;
  const failed = states.filter((s) => s.status === "failed").length;
  const finished = states.length > 0 && !running;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowRightLeft className="h-4 w-4" />
            {title}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            {total} {total === 1 ? "volume" : "volumes"} will move sequentially.
            Each one runs as a separate Proxmox task.
          </p>
          <div className="space-y-2">
            <Label htmlFor="batch-target-storage">Target storage</Label>
            <select
              id="batch-target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => {
                setTargetStorage(e.target.value);
              }}
              disabled={running || finished}
            >
              <option value="">Select storage…</option>
              {targetOptions.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="batch-delete-original"
              checked={deleteOriginal}
              onChange={(e) => {
                setDeleteOriginal(e.target.checked);
              }}
              disabled={running || finished}
              className="h-4 w-4 rounded accent-primary"
            />
            <Label htmlFor="batch-delete-original">
              Delete original after move completes
            </Label>
          </div>

          {states.length > 0 && (
            <div className="max-h-64 space-y-1 overflow-y-auto rounded-md bg-foreground/[0.04] p-2 text-sm">
              {states.map((s) => (
                <div
                  key={s.volid}
                  className="flex items-center gap-2 rounded px-2 py-1"
                >
                  <JobIcon status={s.status} />
                  <span className="flex-1 truncate font-mono text-xs">
                    {s.label}
                  </span>
                  {(s.status === "detached" ||
                    s.status === "unknown" ||
                    s.status === "skipped") && (
                    <span className="text-xs text-muted-foreground">
                      {MOVE_NOTES[s.status]}
                    </span>
                  )}
                  {s.error && (
                    <span className="text-xs text-destructive" title={s.error}>
                      {s.error.length > 40
                        ? `${s.error.slice(0, 40)}…`
                        : s.error}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}

          {finished && (
            <p className="text-sm">
              {done} of {total} completed
              {failed > 0 ? `, ${String(failed)} failed` : ""}.
            </p>
          )}
        </div>
        <DialogFooter>
          {running ? (
            <Button
              variant="outline"
              onClick={() => {
                setStopping(true);
                stopRef.current?.abort();
              }}
              disabled={stopping}
            >
              Stop
            </Button>
          ) : (
            <Button
              variant="outline"
              onClick={() => {
                handleOpenChange(false);
              }}
            >
              {finished ? "Close" : "Cancel"}
            </Button>
          )}
          {!finished && (
            <Button
              onClick={() => {
                void handleMigrate();
              }}
              disabled={!targetStorage || running || jobs.length === 0}
            >
              {running ? "Migrating…" : "Migrate"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function JobIcon({ status }: { status: JobStatus }) {
  switch (status) {
    case "pending":
      return <Circle className="h-3.5 w-3.5 text-muted-foreground" />;
    case "running":
      return <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />;
    case "completed":
      return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />;
    case "failed":
      return <XCircle className="h-3.5 w-3.5 text-destructive" />;
    case "detached":
    case "unknown":
      return <Clock className="h-3.5 w-3.5 text-muted-foreground" />;
    case "skipped":
      return <CircleMinus className="h-3.5 w-3.5 text-muted-foreground" />;
  }
}
