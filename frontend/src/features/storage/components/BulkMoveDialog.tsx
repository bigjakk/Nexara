import { useState, useCallback } from "react";
import { ArrowRightLeft, Loader2, CheckCircle2, XCircle, Circle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { apiClient } from "@/lib/api-client";
import { useStorageContent } from "../api/storage-queries";
import type { StorageContentItem } from "../types/storage";
import type { VMActionResponse } from "@/features/vms/types/vm";

interface VMConfig {
  [key: string]: unknown;
}

interface DiskMoveJob {
  vmid: number;
  diskKey: string;
  volid: string;
  status: "pending" | "running" | "completed" | "failed";
  error?: string;
  upid?: string;
}

interface VMListEntry {
  id: string;
  vmid: number;
}

const DISK_KEY_RE = /^(scsi|ide|sata|virtio)\d+$/;

/** Resolve volid → disk config key by fetching VM config and scanning disk keys. */
async function resolveVolidToDiskKey(
  clusterId: string,
  vmUuid: string,
  volid: string,
): Promise<string | null> {
  try {
    const config = await apiClient.get<VMConfig>(
      `/api/v1/clusters/${clusterId}/vms/${vmUuid}/config`,
    );
    for (const [key, val] of Object.entries(config)) {
      if (DISK_KEY_RE.test(key) && typeof val === "string" && val.includes(volid)) {
        return key;
      }
    }
  } catch {
    // VM might not be accessible
  }
  return null;
}

interface BulkMoveDialogProps {
  clusterId: string;
  storageId: string;
  storageName: string;
  targetOptions: string[];
}

export function BulkMoveDialog({
  clusterId,
  storageId,
  storageName,
  targetOptions,
}: BulkMoveDialogProps) {
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const [jobs, setJobs] = useState<DiskMoveJob[]>([]);
  const [running, setRunning] = useState(false);
  const [buildingJobs, setBuildingJobs] = useState(false);

  const contentQuery = useStorageContent(clusterId, storageId);

  const imageItems = (contentQuery.data ?? []).filter(
    (item: StorageContentItem) => item.content === "images" && item.vmid != null,
  );

  const vmCount = new Set(imageItems.map((i: StorageContentItem) => i.vmid)).size;

  const handleEvacuate = useCallback(async () => {
    if (!targetStorage || imageItems.length === 0) return;
    setRunning(true);
    setBuildingJobs(true);

    // Fetch all VMs once to map vmid → UUID
    let vmList: VMListEntry[] = [];
    try {
      vmList = await apiClient.get<VMListEntry[]>(`/api/v1/clusters/${clusterId}/vms`);
    } catch {
      setRunning(false);
      setBuildingJobs(false);
      return;
    }
    const vmidToUuid = new Map(vmList.map((v) => [v.vmid, v.id]));

    // Build job list by resolving each volid to a disk key
    const newJobs: DiskMoveJob[] = [];
    for (const item of imageItems) {
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
      const diskKey = await resolveVolidToDiskKey(clusterId, vmUuid, item.volid);
      if (diskKey) {
        newJobs.push({ vmid: item.vmid, diskKey, volid: item.volid, status: "pending" });
      } else {
        newJobs.push({
          vmid: item.vmid,
          diskKey: "unknown",
          volid: item.volid,
          status: "failed",
          error: "Could not resolve disk key from VM config",
        });
      }
    }
    setJobs([...newJobs]);
    setBuildingJobs(false);

    // Execute moves sequentially
    for (let i = 0; i < newJobs.length; i++) {
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
          `/api/v1/clusters/${clusterId}/vms/${vmUuid}/disks/move`,
          { disk: job.diskKey, storage: targetStorage, delete: deleteOriginal },
        );

        newJobs[i] = { ...job, status: "completed", upid: resp.upid };
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

    setRunning(false);
  }, [targetStorage, imageItems, clusterId, deleteOriginal]);

  const completedCount = jobs.filter((j) => j.status === "completed").length;
  const failedCount = jobs.filter((j) => j.status === "failed").length;

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!running) setOpen(v); }}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1.5">
          <ArrowRightLeft className="h-3.5 w-3.5" />
          Evacuate
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Evacuate Storage: {storageName}</DialogTitle>
        </DialogHeader>

        <p className="text-sm text-muted-foreground">
          Move all VM disk images off <strong>{storageName}</strong> to another storage pool.
          {imageItems.length > 0 && (
            <> Found <strong>{imageItems.length}</strong> disk{imageItems.length !== 1 ? "s" : ""} across <strong>{vmCount}</strong> VM{vmCount !== 1 ? "s" : ""}.</>
          )}
        </p>

        {imageItems.length === 0 && (
          <p className="text-sm text-muted-foreground">No VM disk images found on this storage.</p>
        )}

        {imageItems.length > 0 && (
          <>
            <div className="space-y-2">
              <Label htmlFor="bulk-target-storage">Target Storage</Label>
              <select
                id="bulk-target-storage"
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                value={targetStorage}
                onChange={(e) => { setTargetStorage(e.target.value); }}
                disabled={running}
              >
                <option value="">Select target...</option>
                {targetOptions.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </div>

            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="bulk-delete-original"
                checked={deleteOriginal}
                onChange={(e) => { setDeleteOriginal(e.target.checked); }}
                disabled={running}
                className="h-4 w-4 rounded border-gray-300"
              />
              <Label htmlFor="bulk-delete-original">Delete originals after move completes</Label>
            </div>

            {jobs.length > 0 && (
              <div className="max-h-48 space-y-1 overflow-y-auto rounded border p-2">
                {jobs.map((job, idx) => (
                  <div key={idx} className="flex items-center gap-2 text-xs">
                    {job.status === "pending" && <Circle className="h-3 w-3 text-muted-foreground" />}
                    {job.status === "running" && <Loader2 className="h-3 w-3 animate-spin text-blue-500" />}
                    {job.status === "completed" && <CheckCircle2 className="h-3 w-3 text-green-500" />}
                    {job.status === "failed" && <XCircle className="h-3 w-3 text-red-500" />}
                    <span className="font-mono">VM {job.vmid}</span>
                    <span className="text-muted-foreground">{job.diskKey}</span>
                    {job.error && <span className="truncate text-red-500">{job.error}</span>}
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
              onClick={() => { void handleEvacuate(); }}
              disabled={!targetStorage || running || (jobs.length > 0 && !running)}
              className="w-full"
            >
              {buildingJobs ? (
                <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> Resolving disks...</>
              ) : running ? (
                <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> Evacuating...</>
              ) : jobs.length > 0 ? (
                "Evacuation Complete"
              ) : (
                "Start Evacuation"
              )}
            </Button>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
