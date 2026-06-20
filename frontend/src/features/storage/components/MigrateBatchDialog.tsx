import { useCallback, useState } from "react";
import {
  ArrowRightLeft,
  CheckCircle2,
  Circle,
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
import { apiClient } from "@/lib/api-client";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  resolveVolidToCTVolumeKey,
  resolveVolidToDiskKey,
} from "../lib/resolve-disk";
import type { VMActionResponse } from "@/features/vms/types/vm";

export interface MigrateBatchJob {
  /** Display label for the row (e.g. "web-01 (100)" or just the volid). */
  label: string;
  /** Owning guest's UUID (already resolved by the caller). */
  guestId: string;
  guestKind: "vm" | "ct";
  vmid: number;
  volid: string;
}

type JobStatus = "pending" | "running" | "completed" | "failed";

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

  // Reset state when the dialog closes so the next open starts clean.
  const handleOpenChange = useCallback(
    (next: boolean) => {
      if (!next) {
        setTargetStorage("");
        setStates([]);
        setRunning(false);
      }
      onOpenChange(next);
    },
    [onOpenChange],
  );

  const handleMigrate = useCallback(async () => {
    if (!targetStorage || jobs.length === 0) return;
    setRunning(true);

    const initial: JobState[] = jobs.map((j) => ({ ...j, status: "pending" }));
    setStates(initial);

    const working = [...initial];

    for (let i = 0; i < working.length; i++) {
      const job = working[i];
      if (!job) continue;

      working[i] = { ...job, status: "running" };
      setStates([...working]);

      try {
        const configKey =
          job.guestKind === "ct"
            ? await resolveVolidToCTVolumeKey(clusterId, job.guestId, job.volid)
            : await resolveVolidToDiskKey(clusterId, job.guestId, job.volid);

        if (!configKey) {
          working[i] = {
            ...job,
            status: "failed",
            error: "Volume not found in guest config (detached?)",
          };
          setStates([...working]);
          continue;
        }

        const path =
          job.guestKind === "ct"
            ? `/api/v1/clusters/${clusterId}/containers/${job.guestId}/volumes/move`
            : `/api/v1/clusters/${clusterId}/vms/${job.guestId}/disks/move`;

        const body =
          job.guestKind === "ct"
            ? { volume: configKey, storage: targetStorage, delete: deleteOriginal }
            : { disk: configKey, storage: targetStorage, delete: deleteOriginal };

        const resp = await apiClient.post<VMActionResponse>(path, body);

        working[i] = { ...job, status: "completed", upid: resp.upid };
        setStates([...working]);

        if (resp.upid && i === 0) {
          // Surface the first task in the global task log so the user can
          // open it from the activity panel; subsequent jobs are tracked
          // inline in the dialog.
          setFocusedTask({
            clusterId,
            upid: resp.upid,
            description: `Migrate ${job.label} → ${targetStorage}`,
          });
        }
      } catch (err) {
        working[i] = {
          ...job,
          status: "failed",
          error: err instanceof Error ? err.message : "Migration failed",
        };
        setStates([...working]);
      }
    }

    setRunning(false);
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
              onChange={(e) => { setTargetStorage(e.target.value); }}
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
              onChange={(e) => { setDeleteOriginal(e.target.checked); }}
              disabled={running || finished}
              className="h-4 w-4 rounded border-gray-300"
            />
            <Label htmlFor="batch-delete-original">
              Delete original after move completes
            </Label>
          </div>

          {states.length > 0 && (
            <div className="max-h-64 space-y-1 overflow-y-auto rounded-md border p-2 text-sm">
              {states.map((s) => (
                <div
                  key={s.volid}
                  className="flex items-center gap-2 rounded px-2 py-1"
                >
                  <JobIcon status={s.status} />
                  <span className="flex-1 truncate font-mono text-xs">
                    {s.label}
                  </span>
                  {s.error && (
                    <span
                      className="text-xs text-destructive"
                      title={s.error}
                    >
                      {s.error.length > 40 ? `${s.error.slice(0, 40)}…` : s.error}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}

          {finished && (
            <p className="text-sm">
              {done} of {total} completed
              {failed > 0 ? ` • ${String(failed)} failed` : ""}.
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => { handleOpenChange(false); }}
            disabled={running}
          >
            {finished ? "Close" : "Cancel"}
          </Button>
          {!finished && (
            <Button
              onClick={() => { void handleMigrate(); }}
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
      return <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />;
    case "completed":
      return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />;
    case "failed":
      return <XCircle className="h-3.5 w-3.5 text-destructive" />;
  }
}
