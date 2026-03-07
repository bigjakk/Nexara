import { useState, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus } from "lucide-react";
import {
  useCreateBackupJob,
  useUpdateBackupJob,
} from "../api/backup-queries";
import type { BackupJob } from "../types/backup";

interface BackupJobDialogProps {
  clusterId: string;
  job?: BackupJob | null;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function BackupJobDialog({
  clusterId,
  job,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
}: BackupJobDialogProps) {
  const isEdit = !!job;
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const onOpenChange = controlledOnOpenChange ?? setInternalOpen;

  const [schedule, setSchedule] = useState("");
  const [storage, setStorage] = useState("");
  const [node, setNode] = useState("");
  const [vmid, setVmid] = useState("");
  const [mode, setMode] = useState("snapshot");
  const [compress, setCompress] = useState("zstd");
  const [enabled, setEnabled] = useState(true);
  const [comment, setComment] = useState("");

  const createMutation = useCreateBackupJob();
  const updateMutation = useUpdateBackupJob();

  useEffect(() => {
    if (job && open) {
      setSchedule(job.schedule ?? "");
      setStorage(job.storage ?? "");
      setNode(job.node ?? "");
      setVmid(job.vmid ?? "");
      setMode(job.mode ?? "snapshot");
      setCompress(job.compress ?? "zstd");
      setEnabled(job.enabled !== 0);
      setComment(job.comment ?? "");
    }
  }, [job, open]);

  const handleSubmit = () => {
    const body = {
      enabled: enabled ? 1 : 0,
      schedule,
      storage,
      node,
      vmid,
      mode,
      compress,
      comment,
    };

    if (isEdit && job) {
      updateMutation.mutate(
        { clusterId, jobId: job.id, body },
        {
          onSuccess: () => {
            onOpenChange(false);
          },
        },
      );
    } else {
      createMutation.mutate(
        { clusterId, body },
        {
          onSuccess: () => {
            onOpenChange(false);
            setSchedule("");
            setStorage("");
            setNode("");
            setVmid("");
            setComment("");
          },
        },
      );
    }
  };

  const isPending = createMutation.isPending || updateMutation.isPending;

  const trigger = !isEdit ? (
    <DialogTrigger asChild>
      <Button variant="outline" size="sm">
        <Plus className="mr-1.5 h-3.5 w-3.5" />
        Add Schedule
      </Button>
    </DialogTrigger>
  ) : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {trigger}
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? "Edit Backup Job" : "Create Backup Job"}
          </DialogTitle>
          <DialogDescription>
            {isEdit
              ? "Modify the vzdump backup job schedule."
              : "Create a new vzdump backup job schedule."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="space-y-2">
            <Label>Schedule</Label>
            <Input
              value={schedule}
              onChange={(e) => {
                setSchedule(e.target.value);
              }}
              placeholder="e.g., daily, sun 02:00, */6:00"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Storage</Label>
              <Input
                value={storage}
                onChange={(e) => {
                  setStorage(e.target.value);
                }}
                placeholder="e.g., local, pbs-store"
              />
            </div>
            <div className="space-y-2">
              <Label>Node (optional)</Label>
              <Input
                value={node}
                onChange={(e) => {
                  setNode(e.target.value);
                }}
                placeholder="e.g., pve1 or empty for all"
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label>VMIDs (optional)</Label>
            <Input
              value={vmid}
              onChange={(e) => {
                setVmid(e.target.value);
              }}
              placeholder="e.g., 100,101,102 or empty for all"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Mode</Label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={mode}
                onChange={(e) => {
                  setMode(e.target.value);
                }}
              >
                <option value="snapshot">Snapshot</option>
                <option value="suspend">Suspend</option>
                <option value="stop">Stop</option>
              </select>
            </div>
            <div className="space-y-2">
              <Label>Compression</Label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={compress}
                onChange={(e) => {
                  setCompress(e.target.value);
                }}
              >
                <option value="zstd">zstd</option>
                <option value="lzo">lzo</option>
                <option value="gzip">gzip</option>
                <option value="0">None</option>
              </select>
            </div>
          </div>
          <div className="space-y-2">
            <Label>Comment</Label>
            <Input
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
              }}
              placeholder="Optional description"
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enabled"
              checked={enabled}
              onChange={(e) => {
                setEnabled(e.target.checked);
              }}
              className="h-4 w-4"
            />
            <Label htmlFor="enabled">Enabled</Label>
          </div>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={isPending}>
            {isPending ? "Saving..." : isEdit ? "Update" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
