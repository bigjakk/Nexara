import { useState } from "react";
import { ArrowRightLeft } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { apiClient } from "@/lib/api-client";
import { useMoveDisk, useMoveContainerVolume } from "@/features/vms/api/vm-queries";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  resolveVolidToDiskKey,
  resolveVolidToCTVolumeKey,
} from "../lib/resolve-disk";
import type { StorageContentItem } from "../types/storage";

interface GuestListEntry {
  id: string;
  vmid: number;
}

interface MigrateItemDialogProps {
  clusterId: string;
  item: StorageContentItem;
  /** Storage names this volume can be moved to (other pools in the same cluster supporting the same content). */
  targetOptions: string[];
}

export function MigrateItemDialog({
  clusterId,
  item,
  targetOptions,
}: MigrateItemDialogProps) {
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const [resolving, setResolving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const moveDisk = useMoveDisk();
  const moveCTVolume = useMoveContainerVolume();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const isContainer = item.content === "rootdir";
  const guestKind = isContainer ? "container" : "VM";

  async function handleMigrate() {
    if (!targetStorage || item.vmid == null) return;
    setError(null);
    setResolving(true);
    try {
      const listPath = isContainer
        ? `/api/v1/clusters/${clusterId}/containers`
        : `/api/v1/clusters/${clusterId}/vms`;
      const guestList = await apiClient.get<GuestListEntry[]>(listPath);
      const guest = guestList.find((g) => g.vmid === item.vmid);
      if (!guest) {
        setError(`${guestKind} ${String(item.vmid)} not found in cluster`);
        setResolving(false);
        return;
      }

      const configKey = isContainer
        ? await resolveVolidToCTVolumeKey(clusterId, guest.id, item.volid)
        : await resolveVolidToDiskKey(clusterId, guest.id, item.volid);
      if (!configKey) {
        setError(
          `Could not find ${item.volid} in ${guestKind} ${String(item.vmid)} config — disk may have been detached.`,
        );
        setResolving(false);
        return;
      }

      setResolving(false);

      const onSuccess = (data: { upid?: string }) => {
        if (data.upid) {
          setFocusedTask({
            clusterId,
            upid: data.upid,
            description: `Migrate ${item.volid} → ${targetStorage}`,
          });
        }
        setOpen(false);
        setTargetStorage("");
      };

      if (isContainer) {
        moveCTVolume.mutate(
          {
            clusterId,
            ctId: guest.id,
            volume: configKey,
            storage: targetStorage,
            deleteOriginal,
          },
          { onSuccess, onError: (e) => { setError(e instanceof Error ? e.message : "Migration failed"); } },
        );
      } else {
        moveDisk.mutate(
          {
            clusterId,
            vmId: guest.id,
            disk: configKey,
            storage: targetStorage,
            deleteOriginal,
          },
          { onSuccess, onError: (e) => { setError(e instanceof Error ? e.message : "Migration failed"); } },
        );
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Migration failed");
      setResolving(false);
    }
  }

  const pending = resolving || moveDisk.isPending || moveCTVolume.isPending;
  const canMigrate = item.vmid != null && targetOptions.length > 0;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          title={canMigrate ? "Migrate to another storage" : "No compatible target storage available"}
          disabled={!canMigrate}
        >
          <ArrowRightLeft className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Migrate {guestKind} {item.vmid != null ? String(item.vmid) : ""}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-xs text-muted-foreground break-all">
            {item.volid}
          </p>
          <div className="space-y-2">
            <Label htmlFor={`target-storage-${item.volid}`}>Target storage</Label>
            <select
              id={`target-storage-${item.volid}`}
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => { setTargetStorage(e.target.value); }}
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
              id={`delete-original-${item.volid}`}
              checked={deleteOriginal}
              onChange={(e) => { setDeleteOriginal(e.target.checked); }}
              className="h-4 w-4 rounded border-gray-300"
            />
            <Label htmlFor={`delete-original-${item.volid}`}>
              Delete original after move completes
            </Label>
          </div>
          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
          <Button onClick={() => { void handleMigrate(); }} disabled={!targetStorage || pending}>
            {pending ? "Migrating…" : "Migrate"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
