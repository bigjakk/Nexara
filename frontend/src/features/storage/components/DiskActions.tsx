import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { useResizeDisk, useMoveDisk, useAddTaskHistory } from "@/features/vms/api/vm-queries";
import { useTaskLogStore } from "@/stores/task-log-store";

// --- Resize Disk Dialog ---

interface ResizeDiskDialogProps {
  clusterId: string;
  vmId: string;
  diskName: string;
}

export function ResizeDiskDialog({
  clusterId,
  vmId,
  diskName,
}: ResizeDiskDialogProps) {
  const [open, setOpen] = useState(false);
  const [size, setSize] = useState("");
  const resizeMutation = useResizeDisk();

  function handleResize() {
    if (!size) return;
    resizeMutation.mutate(
      { clusterId, vmId, disk: diskName, size },
      {
        onSuccess: () => {
          setOpen(false);
          setSize("");
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          Resize
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Resize Disk: {diskName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="resize-size">New Size (e.g. +10G or 50G)</Label>
            <Input
              id="resize-size"
              value={size}
              onChange={(e) => { setSize(e.target.value); }}
              placeholder="+10G"
            />
          </div>
          <Button
            onClick={handleResize}
            disabled={!size || resizeMutation.isPending}
            className="w-full"
          >
            {resizeMutation.isPending ? "Resizing..." : "Resize"}
          </Button>
          {resizeMutation.isError && (
            <p className="text-sm text-destructive">
              {resizeMutation.error instanceof Error
                ? resizeMutation.error.message
                : "Resize failed"}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

// --- Move Disk Dialog ---

interface MoveDiskDialogProps {
  clusterId: string;
  vmId: string;
  diskName: string;
  storageOptions: string[];
  currentStorage?: string;
}

export function MoveDiskDialog({
  clusterId,
  vmId,
  diskName,
  storageOptions,
  currentStorage,
}: MoveDiskDialogProps) {
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const moveMutation = useMoveDisk();
  const addTask = useAddTaskHistory();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  // Filter out current storage from options
  const filteredOptions = currentStorage
    ? storageOptions.filter((s) => s !== currentStorage)
    : storageOptions;

  function handleMove() {
    if (!targetStorage) return;
    moveMutation.mutate(
      {
        clusterId,
        vmId,
        disk: diskName,
        storage: targetStorage,
        deleteOriginal,
      },
      {
        onSuccess: (data) => {
          const desc = `Move disk ${diskName} → ${targetStorage}`;
          if (data.upid) {
            addTask.mutate({
              clusterId,
              upid: data.upid,
              description: desc,
              taskType: "move_disk",
            });
            // Open the progress dialog
            setFocusedTask({
              clusterId,
              upid: data.upid,
              description: desc,
            });
          }
          setOpen(false);
          setTargetStorage("");
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          Move
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move Disk: {diskName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="target-storage">Target Storage</Label>
            <select
              id="target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => { setTargetStorage(e.target.value); }}
            >
              <option value="">Select storage...</option>
              {filteredOptions.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="delete-original"
              checked={deleteOriginal}
              onChange={(e) => { setDeleteOriginal(e.target.checked); }}
              className="h-4 w-4 rounded border-gray-300"
            />
            <Label htmlFor="delete-original">
              Delete original after move completes
            </Label>
          </div>
          <Button
            onClick={handleMove}
            disabled={!targetStorage || moveMutation.isPending}
            className="w-full"
          >
            {moveMutation.isPending ? "Moving disk..." : "Move Disk"}
          </Button>
          {moveMutation.isError && (
            <p className="text-sm text-destructive">
              {moveMutation.error instanceof Error
                ? moveMutation.error.message
                : "Move failed"}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
