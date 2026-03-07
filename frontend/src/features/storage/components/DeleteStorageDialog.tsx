import { useState } from "react";
import { Trash2, AlertTriangle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useDeleteStorage } from "../api/storage-queries";

interface DeleteStorageDialogProps {
  clusterId: string;
  storageId: string;
  storageName: string;
  onDeleted?: () => void;
}

export function DeleteStorageDialog({
  clusterId,
  storageId,
  storageName,
  onDeleted,
}: DeleteStorageDialogProps) {
  const [open, setOpen] = useState(false);
  const [confirmName, setConfirmName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const deleteMutation = useDeleteStorage();

  function handleDelete() {
    if (confirmName !== storageName) return;
    setError(null);

    deleteMutation.mutate(
      { clusterId, storageId },
      {
        onSuccess: () => {
          setOpen(false);
          setConfirmName("");
          onDeleted?.();
        },
        onError: (err) => {
          setError(
            err instanceof Error ? err.message : "Failed to delete storage",
          );
        },
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) {
          setConfirmName("");
          setError(null);
          deleteMutation.reset();
        }
      }}
    >
      <DialogTrigger asChild>
        <Button variant="destructive" size="sm">
          <Trash2 className="mr-1 h-3.5 w-3.5" />
          Delete
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-destructive">
            <AlertTriangle className="h-5 w-5" />
            Delete Storage
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 pt-2">
          <p className="text-sm text-muted-foreground">
            This will permanently remove the storage pool{" "}
            <strong className="text-foreground">{storageName}</strong> from
            the Proxmox cluster configuration. Any data on this storage will
            become inaccessible.
          </p>

          <div className="rounded-md border border-destructive/50 bg-destructive/5 p-3 text-sm">
            <p className="font-medium text-destructive">Warning:</p>
            <ul className="mt-1 list-inside list-disc text-muted-foreground">
              <li>VMs/CTs using this storage may become unbootable</li>
              <li>Backups stored here will be inaccessible</li>
              <li>This action cannot be undone</li>
            </ul>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="confirm-name">
              Type <strong>{storageName}</strong> to confirm
            </Label>
            <Input
              id="confirm-name"
              value={confirmName}
              onChange={(e) => { setConfirmName(e.target.value); }}
              placeholder={storageName}
              autoFocus
            />
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}

          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => { setOpen(false); }}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={
                confirmName !== storageName || deleteMutation.isPending
              }
            >
              {deleteMutation.isPending
                ? "Deleting..."
                : "Delete Storage"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
