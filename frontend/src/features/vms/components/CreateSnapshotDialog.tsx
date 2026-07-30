import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useCreateSnapshot, useTaskStatus } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import { useTaskLogStore } from "@/stores/task-log-store";
import { snapshotNameError, SNAPSHOT_NAME_RULES } from "../lib/snapshot-name";
import type { ResourceKind } from "../types/vm";

interface CreateSnapshotDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  resourceName: string;
}

/** A snapshot task dispatched from this dialog. Captured at dispatch time so
 * the completion invalidation targets the guest the task ran on, even if the
 * dialog's props later point elsewhere (shared context-menu instance). */
interface ActiveSnapshotTask {
  upid: string;
  clusterId: string;
  resourceId: string;
  queryKey: string[];
  description: string;
}

export function CreateSnapshotDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  resourceName,
}: CreateSnapshotDialogProps) {
  const queryClient = useQueryClient();
  const createMutation = useCreateSnapshot();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const [task, setTask] = useState<ActiveSnapshotTask | null>(null);
  const [snapName, setSnapName] = useState("");
  const [description, setDescription] = useState("");
  const [vmstate, setVmstate] = useState(false);

  const { data: taskStatus } = useTaskStatus(
    task?.clusterId ?? "",
    task?.upid ?? null,
  );
  const taskStopped = taskStatus?.status === "stopped";
  const taskOk =
    taskStopped &&
    (taskStatus.exit_status === "OK" ||
      taskStatus.exit_status === "" ||
      taskStatus.exit_status.startsWith("WARNINGS"));
  const taskFailed = taskStopped && !taskOk;

  const nameError = snapshotNameError(snapName);
  const canSubmit = snapName.length > 0 && nameError === null;

  // The hosts keep this component mounted while the dialog is closed, so this
  // effect outlives a mid-task dismissal: the snapshot list refreshes when the
  // task actually finishes, not just at dispatch time.
  const handledRef = useRef<string | null>(null);
  useEffect(() => {
    if (!task || !taskStopped || handledRef.current === task.upid) return;
    handledRef.current = task.upid;
    void queryClient.invalidateQueries({ queryKey: task.queryKey });
    if (taskOk) {
      setTask(null);
      if (open) {
        setSnapName("");
        setDescription("");
        setVmstate(false);
        onOpenChange(false);
      }
    } else if (!open) {
      // Failed after the dialog was dismissed: the task-log panel and
      // activity feed already surface the error — just stop watching.
      setTask(null);
    }
    // Failed while open: keep `task` so the banner shows the error.
  }, [task, taskStopped, taskOk, open, queryClient, onOpenChange]);

  function resetForm() {
    setSnapName("");
    setDescription("");
    setVmstate(false);
    createMutation.reset();
  }

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    createMutation.mutate(
      {
        clusterId,
        resourceId,
        kind,
        body: {
          snap_name: snapName,
          ...(description ? { description } : {}),
          ...(kind === "vm" ? { vmstate } : {}),
        },
      },
      {
        onSuccess: (data) => {
          // A new dispatch replaces any still-watched previous task; the old
          // one stays visible in the task log, it just loses this watcher.
          setTask({
            upid: data.upid,
            clusterId,
            resourceId,
            queryKey: [
              "clusters",
              clusterId,
              kind === "ct" ? "containers" : "vms",
              resourceId,
              "snapshots",
            ],
            description: `Snapshot ${resourceName}`,
          });
        },
      },
    );
  }

  /** After a failure: back to the form with the previous inputs intact. */
  function handleTryAgain() {
    setTask(null);
    createMutation.reset();
  }

  function handleClose() {
    if (task && !taskStopped) {
      // Dismissed mid-task: surface the running task in the global task-log
      // panel; the completion effect above keeps watching in the background.
      setFocusedTask({
        clusterId: task.clusterId,
        upid: task.upid,
        description: task.description,
      });
      setPanelOpen(true);
    } else {
      setTask(null);
    }
    resetForm();
    onOpenChange(false);
  }

  const showTask = task !== null && task.resourceId === resourceId;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Take Snapshot</DialogTitle>
          <DialogDescription>
            Create a point-in-time snapshot of <strong>{resourceName}</strong>.
          </DialogDescription>
        </DialogHeader>

        {showTask ? (
          <div className="space-y-4">
            <TaskProgressBanner
              clusterId={task.clusterId}
              upid={task.upid}
              description={task.description}
            />
            {taskFailed && (
              <DialogFooter>
                <Button type="button" variant="outline" onClick={handleTryAgain}>
                  Try Again
                </Button>
                <Button type="button" onClick={handleClose}>
                  Close
                </Button>
              </DialogFooter>
            )}
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="snapshot-name">Name</Label>
              <Input
                id="snapshot-name"
                value={snapName}
                onChange={(e) => {
                  setSnapName(e.target.value);
                }}
                placeholder="e.g. before-upgrade"
                maxLength={40}
                required
              />
              <p
                className={
                  nameError
                    ? "text-xs text-destructive"
                    : "text-xs text-muted-foreground"
                }
              >
                {nameError ?? SNAPSHOT_NAME_RULES}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="snapshot-desc">Description</Label>
              <Input
                id="snapshot-desc"
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                }}
                placeholder="Optional"
              />
            </div>

            {kind === "vm" && (
              <div className="flex items-center gap-2">
                <Checkbox
                  id="snapshot-vmstate"
                  checked={vmstate}
                  onCheckedChange={(checked) => {
                    setVmstate(Boolean(checked));
                  }}
                />
                <Label htmlFor="snapshot-vmstate" className="text-sm">
                  Include RAM state
                </Label>
              </div>
            )}

            {createMutation.isError && (
              <p className="text-sm text-destructive">
                {createMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!canSubmit || createMutation.isPending}
              >
                {createMutation.isPending ? "Creating..." : "Create Snapshot"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
