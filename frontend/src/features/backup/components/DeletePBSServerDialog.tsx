import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useDeletePBSServer } from "../api/backup-queries";
import type { PBSServer } from "../types/backup";

interface DeletePBSServerDialogProps {
  server: PBSServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeletePBSServerDialog({
  server,
  open,
  onOpenChange,
}: DeletePBSServerDialogProps) {
  const [confirmName, setConfirmName] = useState("");
  const deleteMutation = useDeletePBSServer();

  function handleDelete() {
    deleteMutation.mutate(server.id, {
      onSuccess: () => {
        onOpenChange(false);
      },
    });
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) setConfirmName("");
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete PBS Server</DialogTitle>
          <DialogDescription>
            This will permanently remove <strong>{server.name}</strong> and
            all associated backup data (snapshots, sync jobs, verify jobs,
            metrics). Type the server name to confirm.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <Input
            placeholder={server.name}
            value={confirmName}
            onChange={(e) => {
              setConfirmName(e.target.value);
            }}
          />
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error
                ? deleteMutation.error.message
                : "Delete failed"}
            </p>
          )}
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
          <Button
            variant="destructive"
            disabled={
              confirmName !== server.name || deleteMutation.isPending
            }
            onClick={handleDelete}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete Server"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
