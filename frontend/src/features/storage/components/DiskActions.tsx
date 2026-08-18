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
import { useResizeDisk, useMoveDisk } from "@/features/vms/api/vm-queries";
import { STORAGE_TYPE_LABELS } from "@/features/storage/types/storage";
import type { StorageType } from "@/features/storage/types/storage";
import { DiskMoveOptions } from "@/features/storage/components/DiskMoveOptions";
import {
  parseBwlimit,
  resolveDiskFormat,
} from "@/features/storage/lib/disk-move";
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

/** A candidate target storage. `type` drives whether a format can be chosen. */
export interface MoveDiskStorageOption {
  storage: string;
  type: string;
}

interface MoveDiskDialogProps {
  clusterId: string;
  vmId: string;
  diskName: string;
  storageOptions: MoveDiskStorageOption[];
  currentStorage?: string;
  currentFormat?: string;
}

export function MoveDiskDialog({
  clusterId,
  vmId,
  diskName,
  storageOptions,
  currentStorage,
  currentFormat,
}: MoveDiskDialogProps) {
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  // null = untouched, so the source format is used; "" is an explicit "let the
  // target storage decide".
  const [format, setFormat] = useState<string | null>(null);
  // Off by default, matching Proxmox: the source is kept as an unused disk on
  // the guest so the move can be undone by hand.
  const [deleteOriginal, setDeleteOriginal] = useState(false);
  const [bwlimit, setBwlimit] = useState("");
  const moveMutation = useMoveDisk();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  // Proxmox rejects moving a disk onto the storage it already lives on.
  const filteredOptions = currentStorage
    ? storageOptions.filter((s) => s.storage !== currentStorage)
    : storageOptions;

  const target = filteredOptions.find((s) => s.storage === targetStorage);
  const { value: bwlimitKib, invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  function reset() {
    setTargetStorage("");
    setFormat(null);
    setDeleteOriginal(false);
    setBwlimit("");
  }

  function handleMove() {
    if (!targetStorage || bwlimitInvalid) return;
    moveMutation.mutate(
      {
        clusterId,
        vmId,
        disk: diskName,
        storage: targetStorage,
        deleteOriginal,
        format: resolveDiskFormat(format, currentFormat, target?.type),
        bwlimitKib,
      },
      {
        onSuccess: (data) => {
          if (data.upid) {
            setFocusedTask({
              clusterId,
              upid: data.upid,
              description: `Move disk ${diskName} → ${targetStorage}`,
            });
          }
          setOpen(false);
          reset();
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
          {currentStorage && (
            <p className="text-sm text-muted-foreground">
              Currently on{" "}
              <span className="font-mono font-medium">{currentStorage}</span>
              {currentFormat && (
                <>
                  {" "}
                  as <span className="font-mono font-medium">{currentFormat}</span>
                </>
              )}
            </p>
          )}
          <div className="space-y-2">
            <Label htmlFor="target-storage">Target Storage</Label>
            <select
              id="target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => { setTargetStorage(e.target.value); }}
            >
              <option value="">Select storage...</option>
              {filteredOptions.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage}
                  {STORAGE_TYPE_LABELS[s.type as StorageType]
                    ? ` (${STORAGE_TYPE_LABELS[s.type as StorageType]})`
                    : ""}
                </option>
              ))}
            </select>
          </div>
          <DiskMoveOptions
            idPrefix="move-disk"
            targetStorageType={target?.type}
            sourceFormat={currentFormat}
            format={format}
            onFormatChange={setFormat}
            bwlimit={bwlimit}
            onBwlimitChange={setBwlimit}
            deleteSource={deleteOriginal}
            onDeleteSourceChange={setDeleteOriginal}
            keptHint="The source volume is kept as an unused disk on this VM. Remove it later to reclaim the space."
          />
          <Button
            onClick={handleMove}
            disabled={!targetStorage || bwlimitInvalid || moveMutation.isPending}
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
