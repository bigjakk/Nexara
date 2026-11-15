import { useState, useCallback, useRef } from "react";
import {
  ArrowRightLeft,
  Loader2,
  CheckCircle2,
  XCircle,
  Circle,
  CircleMinus,
  Clock,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { apiClient, sessionScope } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useStorageContent } from "../api/storage-queries";
import type { StorageContentItem } from "../types/storage";
import type { VMActionResponse } from "@/features/vms/types/vm";
import { MOVE_NOTES, resolveVolume, waitForTask } from "../lib/resolve-disk";

interface DiskMoveJob {
  vmid: number;
  diskKey: string;
  volid: string;
  // detached and unknown: left running in Proxmox, after Stop or with its
  // status unreadable; skipped: never started, as the evacuation stopped.
  status:
    | "pending"
    | "running"
    | "completed"
    | "failed"
    | "detached"
    | "unknown"
    | "skipped";
  error?: string;
  upid?: string;
}

interface VMListEntry {
  id: string;
  vmid: number;
  status: string;
}

interface BulkMoveDialogProps {
  clusterId: string;
  storageId: string;
  storageName: string;
  targetOptions: string[];
  /**
   * With `open` the dialog is controlled and draws no trigger of its own: the
   * caller opens it, from a menu item.
   */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function BulkMoveDialog({
  clusterId,
  storageId,
  storageName,
  targetOptions,
  open: openProp,
  onOpenChange,
}: BulkMoveDialogProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : internalOpen;
  const setOpen = (v: boolean) => {
    if (!controlled) setInternalOpen(v);
    onOpenChange?.(v);
  };
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const [jobs, setJobs] = useState<DiskMoveJob[]>([]);
  const [running, setRunning] = useState(false);
  const [buildingJobs, setBuildingJobs] = useState(false);
  // Stop: aborted, no further move starts and the current wait ends.
  const stopRef = useRef<AbortController | null>(null);
  const [stopping, setStopping] = useState(false);

  const contentQuery = useStorageContent(clusterId, storageId);

  const imageItems = (contentQuery.data ?? []).filter(
    (item: StorageContentItem) =>
      item.content === "images" && item.vmid != null,
  );

  const vmCount = new Set(imageItems.map((i: StorageContentItem) => i.vmid))
    .size;

  const handleEvacuate = useCallback(async () => {
    if (!targetStorage || imageItems.length === 0) return;

    // The session this evacuation was started in. Its loops outlive the dialog,
    // and a sign-out with it: once that session has ended, no request goes out
    // for it — it would carry whoever signs in next's token, and each move is a
    // disk move on the cluster.
    const ended = sessionScope();
    const stop = new AbortController();
    stopRef.current = stop;
    setStopping(false);

    setRunning(true);
    setBuildingJobs(true);

    // Fetch all VMs once to map vmid → UUID
    // Assigned in the try; the catch returns, so no initializer is needed
    // (an empty-array default would be a dead store).
    let vmList: VMListEntry[];
    try {
      vmList = await apiClient.list<VMListEntry>(
        apiPath`/api/v1/clusters/${clusterId}/vms`,
      );
    } catch {
      setRunning(false);
      setBuildingJobs(false);
      return;
    }
    const vmidToUuid = new Map(vmList.map((v) => [v.vmid, v.id]));
    const runningVmids = new Set(
      vmList.filter((v) => v.status === "running").map((v) => v.vmid),
    );

    // Build job list by resolving each volid to a disk key
    const newJobs: DiskMoveJob[] = [];
    for (const item of imageItems) {
      if (ended()) break;
      if (item.vmid == null) continue;
      const vmUuid = vmidToUuid.get(item.vmid);
      if (!vmUuid) {
        newJobs.push({
          vmid: item.vmid,
          diskKey: "unknown",
          volid: item.volid,
          status: "failed",
          error: "VM not found in cluster",
        });
        continue;
      }
      const resolved = await resolveVolume(
        clusterId,
        { id: vmUuid, kind: "vm", running: runningVmids.has(item.vmid) },
        item.volid,
      );
      if (resolved.key !== null) {
        newJobs.push({
          vmid: item.vmid,
          diskKey: resolved.key,
          volid: item.volid,
          status: "pending",
        });
      } else {
        newJobs.push({
          vmid: item.vmid,
          diskKey: resolved.slot ?? "unknown",
          volid: item.volid,
          status: "failed",
          error: resolved.reason,
        });
      }
    }
    setJobs([...newJobs]);
    setBuildingJobs(false);

    // Execute moves sequentially
    for (let i = 0; i < newJobs.length; i++) {
      if (ended() || stop.signal.aborted) break;
      const job = newJobs[i];
      if (!job || job.status === "failed") continue;

      newJobs[i] = { ...job, status: "running" };
      setJobs([...newJobs]);

      try {
        const vmUuid = vmidToUuid.get(job.vmid);
        if (!vmUuid) {
          newJobs[i] = { ...job, status: "failed", error: "VM not found" };
          setJobs([...newJobs]);
          continue;
        }

        const resp = await apiClient.post<VMActionResponse>(
          apiPath`/api/v1/clusters/${clusterId}/vms/${vmUuid}/disks/move`,
          { disk: job.diskKey, storage: targetStorage, delete: deleteOriginal },
        );

        // Ticked when the move has ended, and the next starts only then: a
        // guest's second move would meet the lock the first holds.
        const outcome = resp.upid
          ? await waitForTask(clusterId, resp.upid, ended, stop.signal)
          : { status: "ended" as const, ok: true, exit: "" };
        if (ended()) break;
        const upid = resp.upid;
        if (outcome.status === "ended") {
          newJobs[i] = outcome.ok
            ? { ...job, status: "completed", upid }
            : { ...job, status: "failed", upid, error: outcome.exit };
        } else {
          // Stopped, or the status could not be read: the move may still
          // hold the guest's lock, so nothing further starts.
          newJobs[i] = {
            ...job,
            upid,
            status: outcome.status === "unknown" ? "unknown" : "detached",
          };
          setJobs([...newJobs]);
          break;
        }
        setJobs([...newJobs]);
      } catch (err) {
        newJobs[i] = {
          ...job,
          status: "failed",
          error: err instanceof Error ? err.message : "Move failed",
        };
        setJobs([...newJobs]);
      }
    }

    if (!ended()) {
      setJobs(
        newJobs.map((j) =>
          j.status === "pending" ? { ...j, status: "skipped" } : j,
        ),
      );
    }
    setRunning(false);
  }, [targetStorage, imageItems, clusterId, deleteOriginal]);

  const completedCount = jobs.filter((j) => j.status === "completed").length;
  const failedCount = jobs.filter((j) => j.status === "failed").length;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!running || stopping) setOpen(v);
      }}
    >
      {!controlled && (
        <DialogTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5">
            <ArrowRightLeft className="h-3.5 w-3.5" />
            Evacuate
          </Button>
        </DialogTrigger>
      )}
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Evacuate {storageName}</DialogTitle>
        </DialogHeader>

        <p className="text-sm text-muted-foreground">
          Move all VM disk images off <strong>{storageName}</strong> to another
          store.
          {imageItems.length > 0 && (
            <>
              {" "}
              Found <strong>{imageItems.length}</strong> disk
              {imageItems.length !== 1 ? "s" : ""} across{" "}
              <strong>{vmCount}</strong> VM{vmCount !== 1 ? "s" : ""}.
            </>
          )}
        </p>

        {imageItems.length === 0 && (
          <p className="text-sm text-muted-foreground">
            No VM disk images found on this storage.
          </p>
        )}

        {imageItems.length > 0 && (
          <>
            <div className="space-y-2">
              <Label htmlFor="bulk-target-storage">Target storage</Label>
              <select
                id="bulk-target-storage"
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                value={targetStorage}
                onChange={(e) => {
                  setTargetStorage(e.target.value);
                }}
                disabled={running}
              >
                <option value="">Select target...</option>
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
                id="bulk-delete-original"
                checked={deleteOriginal}
                onChange={(e) => {
                  setDeleteOriginal(e.target.checked);
                }}
                disabled={running}
                className="h-4 w-4 rounded accent-primary"
              />
              <Label htmlFor="bulk-delete-original">
                Delete originals after move completes
              </Label>
            </div>

            {jobs.length > 0 && (
              <div className="max-h-48 space-y-1 overflow-y-auto rounded-md bg-foreground/[0.04] p-2.5">
                {jobs.map((job, idx) => (
                  <div key={idx} className="flex items-center gap-2 text-xs">
                    {job.status === "pending" && (
                      <Circle className="h-3 w-3 text-muted-foreground" />
                    )}
                    {job.status === "running" && (
                      <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />
                    )}
                    {job.status === "completed" && (
                      <CheckCircle2 className="h-3 w-3 text-emerald-500" />
                    )}
                    {job.status === "failed" && (
                      <XCircle className="h-3 w-3 text-destructive" />
                    )}
                    {(job.status === "detached" ||
                      job.status === "unknown") && (
                      <Clock className="h-3 w-3 text-muted-foreground" />
                    )}
                    {job.status === "skipped" && (
                      <CircleMinus className="h-3 w-3 text-muted-foreground" />
                    )}
                    <span className="font-mono">VM {job.vmid}</span>
                    <span className="text-muted-foreground">{job.diskKey}</span>
                    {(job.status === "detached" ||
                      job.status === "unknown" ||
                      job.status === "skipped") && (
                      <span className="truncate text-muted-foreground">
                        {MOVE_NOTES[job.status]}
                      </span>
                    )}
                    {job.error && (
                      <span className="truncate text-destructive">
                        {job.error}
                      </span>
                    )}
                  </div>
                ))}
                {!running && jobs.length > 0 && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    {completedCount} completed, {failedCount} failed
                  </p>
                )}
              </div>
            )}

            <Button
              onClick={() => {
                void handleEvacuate();
              }}
              disabled={
                !targetStorage || running || (jobs.length > 0 && !running)
              }
              className="w-full"
            >
              {buildingJobs ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" /> Resolving
                  disks...
                </>
              ) : running ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />{" "}
                  Evacuating...
                </>
              ) : jobs.length > 0 ? (
                "Evacuation complete"
              ) : (
                "Start evacuation"
              )}
            </Button>
            {running && !buildingJobs && (
              <Button
                variant="outline"
                onClick={() => {
                  setStopping(true);
                  stopRef.current?.abort();
                }}
                disabled={stopping}
                className="w-full"
              >
                Stop
              </Button>
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
